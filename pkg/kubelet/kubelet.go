/*
Copyright 2015 The Kubernetes Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package kubelet

import (
	"bytes"
	"fmt"
	"io"
	"io/ioutil"
	"net"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/golang/glog"
	cadvisorapi "github.com/google/cadvisor/info/v1"
	"k8s.io/kubernetes/pkg/api"
	utilpod "k8s.io/kubernetes/pkg/api/pod"
	"k8s.io/kubernetes/pkg/api/unversioned"
	"k8s.io/kubernetes/pkg/api/validation"
	"k8s.io/kubernetes/pkg/apis/componentconfig"
	"k8s.io/kubernetes/pkg/client/cache"
	clientset "k8s.io/kubernetes/pkg/client/clientset_generated/internalclientset"
	"k8s.io/kubernetes/pkg/client/record"
	"k8s.io/kubernetes/pkg/cloudprovider"
	"k8s.io/kubernetes/pkg/fieldpath"
	"k8s.io/kubernetes/pkg/fields"
	"k8s.io/kubernetes/pkg/kubelet/cadvisor"
	"k8s.io/kubernetes/pkg/kubelet/cm"
	"k8s.io/kubernetes/pkg/kubelet/config"
	kubecontainer "k8s.io/kubernetes/pkg/kubelet/container"
	"k8s.io/kubernetes/pkg/kubelet/dockertools"
	"k8s.io/kubernetes/pkg/kubelet/envvars"
	"k8s.io/kubernetes/pkg/kubelet/events"
	"k8s.io/kubernetes/pkg/kubelet/eviction"
	"k8s.io/kubernetes/pkg/kubelet/images"
	"k8s.io/kubernetes/pkg/kubelet/lifecycle"
	"k8s.io/kubernetes/pkg/kubelet/metrics"
	"k8s.io/kubernetes/pkg/kubelet/network"
	"k8s.io/kubernetes/pkg/kubelet/pleg"
	kubepod "k8s.io/kubernetes/pkg/kubelet/pod"
	"k8s.io/kubernetes/pkg/kubelet/prober"
	proberesults "k8s.io/kubernetes/pkg/kubelet/prober/results"
	"k8s.io/kubernetes/pkg/kubelet/rkt"
	"k8s.io/kubernetes/pkg/kubelet/server"
	"k8s.io/kubernetes/pkg/kubelet/server/stats"
	"k8s.io/kubernetes/pkg/kubelet/status"
	kubetypes "k8s.io/kubernetes/pkg/kubelet/types"
	"k8s.io/kubernetes/pkg/kubelet/util/format"
	"k8s.io/kubernetes/pkg/kubelet/util/ioutils"
	"k8s.io/kubernetes/pkg/kubelet/util/queue"
	"k8s.io/kubernetes/pkg/kubelet/volumemanager"
	"k8s.io/kubernetes/pkg/runtime"
	"k8s.io/kubernetes/pkg/security/apparmor"
	"k8s.io/kubernetes/pkg/securitycontext"
	"k8s.io/kubernetes/pkg/types"
	"k8s.io/kubernetes/pkg/util/bandwidth"
	"k8s.io/kubernetes/pkg/util/clock"
	utildbus "k8s.io/kubernetes/pkg/util/dbus"
	utilerrors "k8s.io/kubernetes/pkg/util/errors"
	utilexec "k8s.io/kubernetes/pkg/util/exec"
	"k8s.io/kubernetes/pkg/util/flowcontrol"
	"k8s.io/kubernetes/pkg/util/integer"
	kubeio "k8s.io/kubernetes/pkg/util/io"
	utilipt "k8s.io/kubernetes/pkg/util/iptables"
	"k8s.io/kubernetes/pkg/util/mount"
	"k8s.io/kubernetes/pkg/util/oom"
	"k8s.io/kubernetes/pkg/util/procfs"
	utilruntime "k8s.io/kubernetes/pkg/util/runtime"
	"k8s.io/kubernetes/pkg/util/selinux"
	"k8s.io/kubernetes/pkg/util/sets"
	"k8s.io/kubernetes/pkg/util/term"
	utilvalidation "k8s.io/kubernetes/pkg/util/validation"
	"k8s.io/kubernetes/pkg/util/validation/field"
	"k8s.io/kubernetes/pkg/util/wait"
	"k8s.io/kubernetes/pkg/volume"
	"k8s.io/kubernetes/pkg/volume/util/volumehelper"
	"k8s.io/kubernetes/pkg/watch"
	"k8s.io/kubernetes/plugin/pkg/scheduler/algorithm/predicates"
	"k8s.io/kubernetes/plugin/pkg/scheduler/schedulercache"
	"k8s.io/kubernetes/third_party/forked/golang/expansion"
)

const (
	// Max amount of time to wait for the container runtime to come up.
	maxWaitForContainerRuntime = 5 * time.Minute

	// nodeStatusUpdateRetry specifies how many times kubelet retries when posting node status failed.
	nodeStatusUpdateRetry = 5

	// Location of container logs.
	containerLogsDir = "/var/log/containers"

	// max backoff period, exported for the e2e test
	MaxContainerBackOff = 300 * time.Second

	// Capacity of the channel for storing pods to kill. A small number should
	// suffice because a goroutine is dedicated to check the channel and does
	// not block on anything else.
	podKillingChannelCapacity = 50

	// Period for performing global cleanup tasks.
	housekeepingPeriod = time.Second * 2

	// Period for performing eviction monitoring.
	// TODO ensure this is in sync with internal cadvisor housekeeping.
	evictionMonitoringPeriod = time.Second * 10

	// The path in containers' filesystems where the hosts file is mounted.
	etcHostsPath = "/etc/hosts"

	// Capacity of the channel for receiving pod lifecycle events. This number
	// is a bit arbitrary and may be adjusted in the future.
	plegChannelCapacity = 1000

	// Generic PLEG relies on relisting for discovering container events.
	// A longer period means that kubelet will take longer to detect container
	// changes and to update pod status. On the other hand, a shorter period
	// will cause more frequent relisting (e.g., container runtime operations),
	// leading to higher cpu usage.
	// Note that even though we set the period to 1s, the relisting itself can
	// take more than 1s to finish if the container runtime responds slowly
	// and/or when there are many container changes in one cycle.
	plegRelistPeriod = time.Second * 1

	// backOffPeriod is the period to back off when pod syncing results in an
	// error. It is also used as the base period for the exponential backoff
	// container restarts and image pulls.
	backOffPeriod = time.Second * 10

	// Period for performing container garbage collection.
	ContainerGCPeriod = time.Minute
	// Period for performing image garbage collection.
	ImageGCPeriod = 5 * time.Minute

	// maxImagesInStatus is the number of max images we store in image status.
	maxImagesInNodeStatus = 50

	// Minimum number of dead containers to keep in a pod
	minDeadContainerInPod = 1
)

// SyncHandler is an interface implemented by Kubelet, for testability
type SyncHandler interface {
	HandlePodAdditions(pods []*api.Pod)
	HandlePodUpdates(pods []*api.Pod)
	HandlePodRemoves(pods []*api.Pod)
	HandlePodReconcile(pods []*api.Pod)
	HandlePodSyncs(pods []*api.Pod)
	HandlePodCleanups() error
}

// Option is a functional option type for Kubelet
type Option func(*Kubelet)

// NewMainKubelet instantiates a new Kubelet object along with all the required internal modules.
// No initialization of Kubelet and its modules should happen here.
func NewMainKubelet(
	hostname string,
	nodeName string,
	dockerClient dockertools.DockerInterface,
	kubeClient clientset.Interface,
	rootDirectory string,
	seccompProfileRoot string,
	podInfraContainerImage string,
	resyncInterval time.Duration,
	pullQPS float32,
	pullBurst int,
	eventQPS float32,
	eventBurst int,
	containerGCPolicy kubecontainer.ContainerGCPolicy,
	sourcesReadyFn config.SourcesReadyFn,
	registerNode bool,
	registerSchedulable bool,
	standaloneMode bool,
	clusterDomain string,
	clusterDNS net.IP,
	masterServiceNamespace string,
	volumePlugins []volume.VolumePlugin,
	networkPlugins []network.NetworkPlugin,
	networkPluginName string,
	streamingConnectionIdleTimeout time.Duration,
	recorder record.EventRecorder,
	cadvisorInterface cadvisor.Interface,
	imageGCPolicy images.ImageGCPolicy,
	diskSpacePolicy DiskSpacePolicy,
	cloud cloudprovider.Interface,
	autoDetectCloudProvider bool,
	nodeLabels map[string]string,
	nodeStatusUpdateFrequency time.Duration,
	osInterface kubecontainer.OSInterface,
	CgroupsPerQOS bool,
	cgroupRoot string,
	containerRuntime string,
	runtimeRequestTimeout time.Duration,
	rktPath string,
	rktAPIEndpoint string,
	rktStage1Image string,
	mounter mount.Interface,
	writer kubeio.Writer,
	configureCBR0 bool,
	nonMasqueradeCIDR string,
	podCIDR string,
	reconcileCIDR bool,
	maxPods int,
	podsPerCore int,
	nvidiaGPUs int,
	dockerExecHandler dockertools.ExecHandler,
	resolverConfig string,
	cpuCFSQuota bool,
	daemonEndpoints *api.NodeDaemonEndpoints,
	oomAdjuster *oom.OOMAdjuster,
	serializeImagePulls bool,
	containerManager cm.ContainerManager,
	outOfDiskTransitionFrequency time.Duration,
	flannelExperimentalOverlay bool,
	nodeIP net.IP,
	reservation kubetypes.Reservation,
	enableCustomMetrics bool,
	volumeStatsAggPeriod time.Duration,
	containerRuntimeOptions []kubecontainer.Option,
	hairpinMode string,
	babysitDaemons bool,
	evictionConfig eviction.Config,
	kubeOptions []Option,
	enableControllerAttachDetach bool,
) (*Kubelet, error) {
	if rootDirectory == "" {
		return nil, fmt.Errorf("invalid root directory %q", rootDirectory)
	}
	if resyncInterval <= 0 {
		return nil, fmt.Errorf("invalid sync frequency %d", resyncInterval)
	}

	serviceStore := cache.NewStore(cache.MetaNamespaceKeyFunc)
	if kubeClient != nil {
		// TODO: cache.NewListWatchFromClient is limited as it takes a client implementation rather
		// than an interface. There is no way to construct a list+watcher using resource name.
		listWatch := &cache.ListWatch{
			ListFunc: func(options api.ListOptions) (runtime.Object, error) {
				return kubeClient.Core().Services(api.NamespaceAll).List(options)
			},
			WatchFunc: func(options api.ListOptions) (watch.Interface, error) {
				return kubeClient.Core().Services(api.NamespaceAll).Watch(options)
			},
		}
		cache.NewReflector(listWatch, &api.Service{}, serviceStore, 0).Run()
	}
	serviceLister := &cache.StoreToServiceLister{Store: serviceStore}

	nodeStore := cache.NewStore(cache.MetaNamespaceKeyFunc)
	if kubeClient != nil {
		// TODO: cache.NewListWatchFromClient is limited as it takes a client implementation rather
		// than an interface. There is no way to construct a list+watcher using resource name.
		fieldSelector := fields.Set{api.ObjectNameField: nodeName}.AsSelector()
		listWatch := &cache.ListWatch{
			ListFunc: func(options api.ListOptions) (runtime.Object, error) {
				options.FieldSelector = fieldSelector
				return kubeClient.Core().Nodes().List(options)
			},
			WatchFunc: func(options api.ListOptions) (watch.Interface, error) {
				options.FieldSelector = fieldSelector
				return kubeClient.Core().Nodes().Watch(options)
			},
		}
		cache.NewReflector(listWatch, &api.Node{}, nodeStore, 0).Run()
	}
	nodeLister := &cache.StoreToNodeLister{Store: nodeStore}
	nodeInfo := &predicates.CachedNodeInfo{StoreToNodeLister: nodeLister}

	// TODO: get the real node object of ourself,
	// and use the real node name and UID.
	// TODO: what is namespace for node?
	nodeRef := &api.ObjectReference{
		Kind:      "Node",
		Name:      nodeName,
		UID:       types.UID(nodeName),
		Namespace: "",
	}

	diskSpaceManager, err := newDiskSpaceManager(cadvisorInterface, diskSpacePolicy)
	if err != nil {
		return nil, fmt.Errorf("failed to initialize disk manager: %v", err)
	}
	containerRefManager := kubecontainer.NewRefManager()

	oomWatcher := NewOOMWatcher(cadvisorInterface, recorder)

	// TODO: remove when internal cbr0 implementation gets removed in favor
	// of the kubenet network plugin
	if networkPluginName == "kubenet" {
		configureCBR0 = false
		flannelExperimentalOverlay = false
	}

	klet := &Kubelet{
		hostname:                       hostname,
		nodeName:                       nodeName,
		dockerClient:                   dockerClient,
		kubeClient:                     kubeClient,
		rootDirectory:                  rootDirectory,
		resyncInterval:                 resyncInterval,
		containerRefManager:            containerRefManager,
		httpClient:                     &http.Client{},
		sourcesReady:                   config.NewSourcesReady(sourcesReadyFn),
		registerNode:                   registerNode,
		registerSchedulable:            registerSchedulable,
		standaloneMode:                 standaloneMode,
		clusterDomain:                  clusterDomain,
		clusterDNS:                     clusterDNS,
		serviceLister:                  serviceLister,
		nodeLister:                     nodeLister,
		nodeInfo:                       nodeInfo,
		masterServiceNamespace:         masterServiceNamespace,
		streamingConnectionIdleTimeout: streamingConnectionIdleTimeout,
		recorder:                       recorder,
		cadvisor:                       cadvisorInterface,
		diskSpaceManager:               diskSpaceManager,
		cloud:                          cloud,
		autoDetectCloudProvider:   autoDetectCloudProvider,
		nodeRef:                   nodeRef,
		nodeLabels:                nodeLabels,
		nodeStatusUpdateFrequency: nodeStatusUpdateFrequency,
		os:                         osInterface,
		oomWatcher:                 oomWatcher,
		CgroupsPerQOS:              CgroupsPerQOS,
		cgroupRoot:                 cgroupRoot,
		mounter:                    mounter,
		writer:                     writer,
		configureCBR0:              configureCBR0,
		nonMasqueradeCIDR:          nonMasqueradeCIDR,
		reconcileCIDR:              reconcileCIDR,
		maxPods:                    maxPods,
		podsPerCore:                podsPerCore,
		nvidiaGPUs:                 nvidiaGPUs,
		syncLoopMonitor:            atomic.Value{},
		resolverConfig:             resolverConfig,
		cpuCFSQuota:                cpuCFSQuota,
		daemonEndpoints:            daemonEndpoints,
		containerManager:           containerManager,
		flannelExperimentalOverlay: flannelExperimentalOverlay,
		flannelHelper:              nil,
		nodeIP:                     nodeIP,
		clock:                      clock.RealClock{},
		outOfDiskTransitionFrequency: outOfDiskTransitionFrequency,
		reservation:                  reservation,
		enableCustomMetrics:          enableCustomMetrics,
		babysitDaemons:               babysitDaemons,
		enableControllerAttachDetach: enableControllerAttachDetach,
		iptClient:                    utilipt.New(utilexec.New(), utildbus.New(), utilipt.ProtocolIpv4),
	}

	if klet.flannelExperimentalOverlay {
		klet.flannelHelper = NewFlannelHelper()
		glog.Infof("Flannel is in charge of podCIDR and overlay networking.")
	}
	if klet.nodeIP != nil {
		if err := klet.validateNodeIP(); err != nil {
			return nil, err
		}
		glog.Infof("Using node IP: %q", klet.nodeIP.String())
	}

	if mode, err := effectiveHairpinMode(componentconfig.HairpinMode(hairpinMode), containerRuntime, configureCBR0, networkPluginName); err != nil {
		// This is a non-recoverable error. Returning it up the callstack will just
		// lead to retries of the same failure, so just fail hard.
		glog.Fatalf("Invalid hairpin mode: %v", err)
	} else {
		klet.hairpinMode = mode
	}
	glog.Infof("Hairpin mode set to %q", klet.hairpinMode)

	if plug, err := network.InitNetworkPlugin(networkPlugins, networkPluginName, &networkHost{klet}, klet.hairpinMode, klet.nonMasqueradeCIDR); err != nil {
		return nil, err
	} else {
		klet.networkPlugin = plug
	}

	machineInfo, err := klet.GetCachedMachineInfo()
	if err != nil {
		return nil, err
	}

	procFs := procfs.NewProcFS()
	imageBackOff := flowcontrol.NewBackOff(backOffPeriod, MaxContainerBackOff)

	klet.livenessManager = proberesults.NewManager()

	klet.podCache = kubecontainer.NewCache()
	klet.podManager = kubepod.NewBasicPodManager(kubepod.NewBasicMirrorClient(klet.kubeClient))

	// Initialize the runtime.
	switch containerRuntime {
	case "docker":
		// Only supported one for now, continue.
		klet.containerRuntime = dockertools.NewDockerManager(
			dockerClient,
			kubecontainer.FilterEventRecorder(recorder),
			klet.livenessManager,
			containerRefManager,
			klet.podManager,
			machineInfo,
			podInfraContainerImage,
			pullQPS,
			pullBurst,
			containerLogsDir,
			osInterface,
			klet.networkPlugin,
			klet,
			klet.httpClient,
			dockerExecHandler,
			oomAdjuster,
			procFs,
			klet.cpuCFSQuota,
			imageBackOff,
			serializeImagePulls,
			enableCustomMetrics,
			// If using "kubenet", the Kubernetes network plugin that wraps
			// CNI's bridge plugin, it knows how to set the hairpin veth flag
			// so we tell the container runtime to back away from setting it.
			// If the kubelet is started with any other plugin we can't be
			// sure it handles the hairpin case so we instruct the docker
			// runtime to set the flag instead.
			klet.hairpinMode == componentconfig.HairpinVeth && networkPluginName != "kubenet",
			seccompProfileRoot,
			containerRuntimeOptions...,
		)
	case "rkt":
		// TODO: Include hairpin mode settings in rkt?
		conf := &rkt.Config{
			Path:            rktPath,
			Stage1Image:     rktStage1Image,
			InsecureOptions: "image,ondisk",
		}
		rktRuntime, err := rkt.New(
			rktAPIEndpoint,
			conf,
			klet,
			recorder,
			containerRefManager,
			klet.podManager,
			klet.livenessManager,
			klet.httpClient,
			klet.networkPlugin,
			klet.hairpinMode == componentconfig.HairpinVeth,
			utilexec.New(),
			kubecontainer.RealOS{},
			imageBackOff,
			serializeImagePulls,
			runtimeRequestTimeout,
		)
		if err != nil {
			return nil, err
		}
		klet.containerRuntime = rktRuntime
	default:
		return nil, fmt.Errorf("unsupported container runtime %q specified", containerRuntime)
	}

	// TODO: Factor out "StatsProvider" from Kubelet so we don't have a cyclic dependency
	klet.resourceAnalyzer = stats.NewResourceAnalyzer(klet, volumeStatsAggPeriod, klet.containerRuntime)

	klet.pleg = pleg.NewGenericPLEG(klet.containerRuntime, plegChannelCapacity, plegRelistPeriod, klet.podCache, clock.RealClock{})
	klet.runtimeState = newRuntimeState(maxWaitForContainerRuntime)
	klet.updatePodCIDR(podCIDR)

	// setup containerGC
	containerGC, err := kubecontainer.NewContainerGC(klet.containerRuntime, containerGCPolicy)
	if err != nil {
		return nil, err
	}
	klet.containerGC = containerGC
	klet.containerDeletor = newPodContainerDeletor(klet.containerRuntime, integer.IntMax(containerGCPolicy.MaxPerPodContainer, minDeadContainerInPod))

	// setup imageManager
	imageManager, err := images.NewImageGCManager(klet.containerRuntime, cadvisorInterface, recorder, nodeRef, imageGCPolicy)
	if err != nil {
		return nil, fmt.Errorf("failed to initialize image manager: %v", err)
	}
	klet.imageManager = imageManager

	klet.runner = klet.containerRuntime
	klet.statusManager = status.NewManager(kubeClient, klet.podManager)

	klet.probeManager = prober.NewManager(
		klet.statusManager,
		klet.livenessManager,
		klet.runner,
		containerRefManager,
		recorder)

	klet.volumePluginMgr, err =
		NewInitializedVolumePluginMgr(klet, volumePlugins)
	if err != nil {
		return nil, err
	}

	// setup volumeManager
	klet.volumeManager, err = volumemanager.NewVolumeManager(
		enableControllerAttachDetach,
		nodeName,
		klet.podManager,
		klet.kubeClient,
		klet.volumePluginMgr,
		klet.containerRuntime,
		mounter,
		klet.getPodsDir())

	runtimeCache, err := kubecontainer.NewRuntimeCache(klet.containerRuntime)
	if err != nil {
		return nil, err
	}
	klet.runtimeCache = runtimeCache
	klet.reasonCache = NewReasonCache()
	klet.workQueue = queue.NewBasicWorkQueue(klet.clock)
	klet.podWorkers = newPodWorkers(klet.syncPod, recorder, klet.workQueue, klet.resyncInterval, backOffPeriod, klet.podCache)

	klet.backOff = flowcontrol.NewBackOff(backOffPeriod, MaxContainerBackOff)
	klet.podKillingCh = make(chan *kubecontainer.PodPair, podKillingChannelCapacity)
	klet.setNodeStatusFuncs = klet.defaultNodeStatusFuncs()

	// setup eviction manager
	evictionManager, evictionAdmitHandler, err := eviction.NewManager(klet.resourceAnalyzer, evictionConfig, killPodNow(klet.podWorkers), klet.imageManager, recorder, nodeRef, klet.clock)
	if err != nil {
		return nil, fmt.Errorf("failed to initialize eviction manager: %v", err)
	}
	klet.evictionManager = evictionManager
	klet.AddPodAdmitHandler(evictionAdmitHandler)

	// enable active deadline handler
	activeDeadlineHandler, err := newActiveDeadlineHandler(klet.statusManager, klet.recorder, klet.clock)
	if err != nil {
		return nil, err
	}
	klet.AddPodSyncLoopHandler(activeDeadlineHandler)
	klet.AddPodSyncHandler(activeDeadlineHandler)

	klet.AddPodAdmitHandler(apparmor.NewValidator(containerRuntime))

	// apply functional Option's
	for _, opt := range kubeOptions {
		opt(klet)
	}
	return klet, nil
}

type serviceLister interface {
	List() (api.ServiceList, error)
}

type nodeLister interface {
	List() (machines api.NodeList, err error)
}

// Kubelet is the main kubelet implementation.
type Kubelet struct {
	hostname      string
	nodeName      string
	dockerClient  dockertools.DockerInterface
	runtimeCache  kubecontainer.RuntimeCache
	kubeClient    clientset.Interface
	iptClient     utilipt.Interface
	rootDirectory string

	// podWorkers handle syncing Pods in response to events.
	podWorkers PodWorkers

	// resyncInterval is the interval between periodic full reconciliations of
	// pods on this node.
	resyncInterval time.Duration

	// sourcesReady records the sources seen by the kubelet, it is thread-safe.
	sourcesReady config.SourcesReady

	// podManager is a facade that abstracts away the various sources of pods
	// this Kubelet services.
	podManager kubepod.Manager

	// Needed to observe and respond to situations that could impact node stability
	evictionManager eviction.Manager

	// Needed to report events for containers belonging to deleted/modified pods.
	// Tracks references for reporting events
	containerRefManager *kubecontainer.RefManager

	// Optional, defaults to /logs/ from /var/log
	logServer http.Handler
	// Optional, defaults to simple Docker implementation
	runner kubecontainer.ContainerCommandRunner
	// Optional, client for http requests, defaults to empty client
	httpClient kubetypes.HttpGetter

	// cAdvisor used for container information.
	cadvisor cadvisor.Interface

	// Set to true to have the node register itself with the apiserver.
	registerNode bool
	// Set to true to have the node register itself as schedulable.
	registerSchedulable bool
	// for internal book keeping; access only from within registerWithApiserver
	registrationCompleted bool

	// Set to true if the kubelet is in standalone mode (i.e. setup without an apiserver)
	standaloneMode bool

	// If non-empty, use this for container DNS search.
	clusterDomain string

	// If non-nil, use this for container DNS server.
	clusterDNS net.IP

	// masterServiceNamespace is the namespace that the master service is exposed in.
	masterServiceNamespace string
	// serviceLister knows how to list services
	serviceLister serviceLister
	// nodeLister knows how to list nodes
	nodeLister nodeLister
	// nodeInfo knows how to get information about the node for this kubelet.
	nodeInfo predicates.NodeInfo

	// a list of node labels to register
	nodeLabels map[string]string

	// Last timestamp when runtime responded on ping.
	// Mutex is used to protect this value.
	runtimeState *runtimeState

	// Volume plugins.
	volumePluginMgr *volume.VolumePluginMgr

	// Network plugin.
	networkPlugin network.NetworkPlugin

	// Handles container probing.
	probeManager prober.Manager
	// Manages container health check results.
	livenessManager proberesults.Manager

	// How long to keep idle streaming command execution/port forwarding
	// connections open before terminating them
	streamingConnectionIdleTimeout time.Duration

	// The EventRecorder to use
	recorder record.EventRecorder

	// Policy for handling garbage collection of dead containers.
	containerGC kubecontainer.ContainerGC

	// Manager for image garbage collection.
	imageManager images.ImageGCManager

	// Diskspace manager.
	diskSpaceManager diskSpaceManager

	// Cached MachineInfo returned by cadvisor.
	machineInfo *cadvisorapi.MachineInfo

	// Syncs pods statuses with apiserver; also used as a cache of statuses.
	statusManager status.Manager

	// VolumeManager runs a set of asynchronous loops that figure out which
	// volumes need to be attached/mounted/unmounted/detached based on the pods
	// scheduled on this node and makes it so.
	volumeManager volumemanager.VolumeManager

	// Cloud provider interface.
	cloud                   cloudprovider.Interface
	autoDetectCloudProvider bool

	// Reference to this node.
	nodeRef *api.ObjectReference

	// Container runtime.
	containerRuntime kubecontainer.Runtime

	// reasonCache caches the failure reason of the last creation of all containers, which is
	// used for generating ContainerStatus.
	reasonCache *ReasonCache

	// nodeStatusUpdateFrequency specifies how often kubelet posts node status to master.
	// Note: be cautious when changing the constant, it must work with nodeMonitorGracePeriod
	// in nodecontroller. There are several constraints:
	// 1. nodeMonitorGracePeriod must be N times more than nodeStatusUpdateFrequency, where
	//    N means number of retries allowed for kubelet to post node status. It is pointless
	//    to make nodeMonitorGracePeriod be less than nodeStatusUpdateFrequency, since there
	//    will only be fresh values from Kubelet at an interval of nodeStatusUpdateFrequency.
	//    The constant must be less than podEvictionTimeout.
	// 2. nodeStatusUpdateFrequency needs to be large enough for kubelet to generate node
	//    status. Kubelet may fail to update node status reliably if the value is too small,
	//    as it takes time to gather all necessary node information.
	nodeStatusUpdateFrequency time.Duration

	// Generates pod events.
	pleg pleg.PodLifecycleEventGenerator

	// Store kubecontainer.PodStatus for all pods.
	podCache kubecontainer.Cache

	// os is a facade for various syscalls that need to be mocked during testing.
	os kubecontainer.OSInterface

	// Watcher of out of memory events.
	oomWatcher OOMWatcher

	// Monitor resource usage
	resourceAnalyzer stats.ResourceAnalyzer

	// Whether or not we should have the QOS cgroup hierarchy for resource management
	CgroupsPerQOS bool

	// If non-empty, pass this to the container runtime as the root cgroup.
	cgroupRoot string

	// Mounter to use for volumes.
	mounter mount.Interface

	// Writer interface to use for volumes.
	writer kubeio.Writer

	// Manager of non-Runtime containers.
	containerManager cm.ContainerManager
	nodeConfig       cm.NodeConfig

	// Whether or not kubelet should take responsibility for keeping cbr0 in
	// the correct state.
	configureCBR0 bool
	reconcileCIDR bool

	// Traffic to IPs outside this range will use IP masquerade.
	nonMasqueradeCIDR string

	// Maximum Number of Pods which can be run by this Kubelet
	maxPods int

	// Number of NVIDIA GPUs on this node
	nvidiaGPUs int

	// Monitor Kubelet's sync loop
	syncLoopMonitor atomic.Value

	// Container restart Backoff
	backOff *flowcontrol.Backoff

	// Channel for sending pods to kill.
	podKillingCh chan *kubecontainer.PodPair

	// The configuration file used as the base to generate the container's
	// DNS resolver configuration file. This can be used in conjunction with
	// clusterDomain and clusterDNS.
	resolverConfig string

	// Optionally shape the bandwidth of a pod
	// TODO: remove when kubenet plugin is ready
	shaper bandwidth.BandwidthShaper

	// True if container cpu limits should be enforced via cgroup CFS quota
	cpuCFSQuota bool

	// Information about the ports which are opened by daemons on Node running this Kubelet server.
	daemonEndpoints *api.NodeDaemonEndpoints

	// A queue used to trigger pod workers.
	workQueue queue.WorkQueue

	// oneTimeInitializer is used to initialize modules that are dependent on the runtime to be up.
	oneTimeInitializer sync.Once

	// flannelExperimentalOverlay determines whether the experimental flannel
	// network overlay is active.
	flannelExperimentalOverlay bool

	// TODO: Flannelhelper doesn't store any state, we can instantiate it
	// on the fly if we're confident the dbus connetions it opens doesn't
	// put the system under duress.
	flannelHelper *FlannelHelper

	// If non-nil, use this IP address for the node
	nodeIP net.IP

	// clock is an interface that provides time related functionality in a way that makes it
	// easy to test the code.
	clock clock.Clock

	// outOfDiskTransitionFrequency specifies the amount of time the kubelet has to be actually
	// not out of disk before it can transition the node condition status from out-of-disk to
	// not-out-of-disk. This prevents a pod that causes out-of-disk condition from repeatedly
	// getting rescheduled onto the node.
	outOfDiskTransitionFrequency time.Duration

	// reservation specifies resources which are reserved for non-pod usage, including kubernetes and
	// non-kubernetes system processes.
	reservation kubetypes.Reservation

	// support gathering custom metrics.
	enableCustomMetrics bool

	// How the Kubelet should setup hairpin NAT. Can take the values: "promiscuous-bridge"
	// (make cbr0 promiscuous), "hairpin-veth" (set the hairpin flag on veth interfaces)
	// or "none" (do nothing).
	hairpinMode componentconfig.HairpinMode

	// The node has babysitter process monitoring docker and kubelet
	babysitDaemons bool

	// handlers called during the tryUpdateNodeStatus cycle
	setNodeStatusFuncs []func(*api.Node) error

	// TODO: think about moving this to be centralized in PodWorkers in follow-on.
	// the list of handlers to call during pod admission.
	lifecycle.PodAdmitHandlers

	// the list of handlers to call during pod sync loop.
	lifecycle.PodSyncLoopHandlers

	// the list of handlers to call during pod sync.
	lifecycle.PodSyncHandlers

	// the number of allowed pods per core
	podsPerCore int

	// enableControllerAttachDetach indicates the Attach/Detach controller
	// should manage attachment/detachment of volumes scheduled to this node,
	// and disable kubelet from executing any attach/detach operations
	enableControllerAttachDetach bool

	// trigger deleting containers in a pod
	containerDeletor *podContainerDeletor
}

// setupDataDirs creates data directories for the kubelet:
// 1.  the root directory
// 2.  the pods directory
// 3.  the plugins directory
func (kl *Kubelet) setupDataDirs() error {
	kl.rootDirectory = path.Clean(kl.rootDirectory)
	if err := os.MkdirAll(kl.getRootDir(), 0750); err != nil {
		return fmt.Errorf("error creating root directory: %v", err)
	}
	if err := os.MkdirAll(kl.getPodsDir(), 0750); err != nil {
		return fmt.Errorf("error creating pods directory: %v", err)
	}
	if err := os.MkdirAll(kl.getPluginsDir(), 0750); err != nil {
		return fmt.Errorf("error creating plugins directory: %v", err)
	}
	return nil
}

// Starts garbage collection threads.
func (kl *Kubelet) StartGarbageCollection() {
	go wait.Until(func() {
		if err := kl.containerGC.GarbageCollect(kl.sourcesReady.AllReady()); err != nil {
			glog.Errorf("Container garbage collection failed: %v", err)
		}
	}, ContainerGCPeriod, wait.NeverStop)

	go wait.Until(func() {
		if err := kl.imageManager.GarbageCollect(); err != nil {
			glog.Errorf("Image garbage collection failed: %v", err)
		}
	}, ImageGCPeriod, wait.NeverStop)
}

// initializeModules will initialize internal modules that do not require the
// container runtime to be up. Note that the modules here must not depend on
// modules that are not initialized here.
func (kl *Kubelet) initializeModules() error {
	// Step 1: Promethues metrics.
	metrics.Register(kl.runtimeCache)

	// Step 2: Setup filesystem directories.
	if err := kl.setupDataDirs(); err != nil {
		return err
	}

	// Step 3: If the container logs directory does not exist, create it.
	if _, err := os.Stat(containerLogsDir); err != nil {
		if err := kl.os.MkdirAll(containerLogsDir, 0755); err != nil {
			glog.Errorf("Failed to create directory %q: %v", containerLogsDir, err)
		}
	}

	// Step 4: Start the image manager.
	if err := kl.imageManager.Start(); err != nil {
		return fmt.Errorf("Failed to start ImageManager, images may not be garbage collected: %v", err)
	}

	// Step 5: Start container manager.
	if err := kl.containerManager.Start(); err != nil {
		return fmt.Errorf("Failed to start ContainerManager %v", err)
	}

	// Step 6: Start out of memory watcher.
	if err := kl.oomWatcher.Start(kl.nodeRef); err != nil {
		return fmt.Errorf("Failed to start OOM watcher %v", err)
	}

	// Step 7: Start resource analyzer
	kl.resourceAnalyzer.Start()

	return nil
}

// initializeRuntimeDependentModules will initialize internal modules that
// require the container runtime to be up.
func (kl *Kubelet) initializeRuntimeDependentModules() {
	if err := kl.cadvisor.Start(); err != nil {
		// Fail kubelet and rely on the babysitter to retry starting kubelet.
		// TODO(random-liu): Add backoff logic in the babysitter
		glog.Fatalf("Failed to start cAdvisor %v", err)
	}
	// eviction manager must start after cadvisor because it needs to know if the container runtime has a dedicated imagefs
	if err := kl.evictionManager.Start(kl, kl.getActivePods, evictionMonitoringPeriod); err != nil {
		kl.runtimeState.setInternalError(fmt.Errorf("failed to start eviction manager %v", err))
	}
}

// Run starts the kubelet reacting to config updates
func (kl *Kubelet) Run(updates <-chan kubetypes.PodUpdate) {
	if kl.logServer == nil {
		kl.logServer = http.StripPrefix("/logs/", http.FileServer(http.Dir("/var/log/")))
	}
	if kl.kubeClient == nil {
		glog.Warning("No api server defined - no node status update will be sent.")
	}
	if err := kl.initializeModules(); err != nil {
		kl.recorder.Eventf(kl.nodeRef, api.EventTypeWarning, events.KubeletSetupFailed, err.Error())
		glog.Error(err)
		kl.runtimeState.setInitError(err)
	}

	// Start volume manager
	go kl.volumeManager.Run(kl.sourcesReady, wait.NeverStop)

	if kl.kubeClient != nil {
		// Start syncing node status immediately, this may set up things the runtime needs to run.
		go wait.Until(kl.syncNodeStatus, kl.nodeStatusUpdateFrequency, wait.NeverStop)
	}
	go wait.Until(kl.syncNetworkStatus, 30*time.Second, wait.NeverStop)
	go wait.Until(kl.updateRuntimeUp, 5*time.Second, wait.NeverStop)

	// Start a goroutine responsible for killing pods (that are not properly
	// handled by pod workers).
	go wait.Until(kl.podKiller, 1*time.Second, wait.NeverStop)

	// Start component sync loops.
	kl.statusManager.Start()
	kl.probeManager.Start()

	// Start the pod lifecycle event generator.
	kl.pleg.Start()
	kl.syncLoop(updates, kl)
}

// syncPod is the transaction script for the sync of a single pod.
//
// Arguments:
//
// o - the SyncPodOptions for this invocation
//
// The workflow is:
// * If the pod is being created, record pod worker start latency
// * Call generateAPIPodStatus to prepare an api.PodStatus for the pod
// * If the pod is being seen as running for the first time, record pod
//   start latency
// * Update the status of the pod in the status manager
// * Kill the pod if it should not be running
// * Create a mirror pod if the pod is a static pod, and does not
//   already have a mirror pod
// * Create the data directories for the pod if they do not exist
// * Wait for volumes to attach/mount
// * Fetch the pull secrets for the pod
// * Call the container runtime's SyncPod callback
// * Update the traffic shaping for the pod's ingress and egress limits
//
// If any step if this workflow errors, the error is returned, and is repeated
// on the next syncPod call.
func (kl *Kubelet) syncPod(o syncPodOptions) error {
	// pull out the required options
	pod := o.pod
	mirrorPod := o.mirrorPod
	podStatus := o.podStatus
	updateType := o.updateType

	// if we want to kill a pod, do it now!
	if updateType == kubetypes.SyncPodKill {
		killPodOptions := o.killPodOptions
		if killPodOptions == nil || killPodOptions.PodStatusFunc == nil {
			return fmt.Errorf("kill pod options are required if update type is kill")
		}
		apiPodStatus := killPodOptions.PodStatusFunc(pod, podStatus)
		kl.statusManager.SetPodStatus(pod, apiPodStatus)
		// we kill the pod with the specified grace period since this is a termination
		if err := kl.killPod(pod, nil, podStatus, killPodOptions.PodTerminationGracePeriodSecondsOverride); err != nil {
			// there was an error killing the pod, so we return that error directly
			utilruntime.HandleError(err)
			return err
		}
		return nil
	}

	// Latency measurements for the main workflow are relative to the
	// (first time the pod was seen by the API server.
	var firstSeenTime time.Time
	if firstSeenTimeStr, ok := pod.Annotations[kubetypes.ConfigFirstSeenAnnotationKey]; ok {
		firstSeenTime = kubetypes.ConvertToTimestamp(firstSeenTimeStr).Get()
	}

	// Record pod worker start latency if being created
	// TODO: make pod workers record their own latencies
	if updateType == kubetypes.SyncPodCreate {
		if !firstSeenTime.IsZero() {
			// This is the first time we are syncing the pod. Record the latency
			// since kubelet first saw the pod if firstSeenTime is set.
			metrics.PodWorkerStartLatency.Observe(metrics.SinceInMicroseconds(firstSeenTime))
		} else {
			glog.V(3).Infof("First seen time not recorded for pod %q", pod.UID)
		}
	}

	// Generate final API pod status with pod and status manager status
	apiPodStatus := kl.generateAPIPodStatus(pod, podStatus)
	// The pod IP may be changed in generateAPIPodStatus if the pod is using host network. (See #24576)
	// TODO(random-liu): After writing pod spec into container labels, check whether pod is using host network, and
	// set pod IP to hostIP directly in runtime.GetPodStatus
	podStatus.IP = apiPodStatus.PodIP

	// Record the time it takes for the pod to become running.
	existingStatus, ok := kl.statusManager.GetPodStatus(pod.UID)
	if !ok || existingStatus.Phase == api.PodPending && apiPodStatus.Phase == api.PodRunning &&
		!firstSeenTime.IsZero() {
		metrics.PodStartLatency.Observe(metrics.SinceInMicroseconds(firstSeenTime))
	}

	// Update status in the status manager
	kl.statusManager.SetPodStatus(pod, apiPodStatus)

	// Kill pod if it should not be running
	if errOuter := canRunPod(pod); errOuter != nil || pod.DeletionTimestamp != nil || apiPodStatus.Phase == api.PodFailed {
		if errInner := kl.killPod(pod, nil, podStatus, nil); errInner != nil {
			errOuter = fmt.Errorf("error killing pod: %v", errInner)
			utilruntime.HandleError(errOuter)
		}
		// there was no error killing the pod, but the pod cannot be run, so we return that err (if any)
		return errOuter
	}

	// Create Mirror Pod for Static Pod if it doesn't already exist
	if kubepod.IsStaticPod(pod) {
		podFullName := kubecontainer.GetPodFullName(pod)
		deleted := false
		if mirrorPod != nil {
			if mirrorPod.DeletionTimestamp != nil || !kl.podManager.IsMirrorPodOf(mirrorPod, pod) {
				// The mirror pod is semantically different from the static pod. Remove
				// it. The mirror pod will get recreated later.
				glog.Errorf("Deleting mirror pod %q because it is outdated", format.Pod(mirrorPod))
				if err := kl.podManager.DeleteMirrorPod(podFullName); err != nil {
					glog.Errorf("Failed deleting mirror pod %q: %v", format.Pod(mirrorPod), err)
				} else {
					deleted = true
				}
			}
		}
		if mirrorPod == nil || deleted {
			glog.V(3).Infof("Creating a mirror pod for static pod %q", format.Pod(pod))
			if err := kl.podManager.CreateMirrorPod(pod); err != nil {
				glog.Errorf("Failed creating a mirror pod for %q: %v", format.Pod(pod), err)
			}
		}
	}

	// Make data directories for the pod
	if err := kl.makePodDataDirs(pod); err != nil {
		glog.Errorf("Unable to make pod data directories for pod %q: %v", format.Pod(pod), err)
		return err
	}

	// Wait for volumes to attach/mount
	if err := kl.volumeManager.WaitForAttachAndMount(pod); err != nil {
		kl.recorder.Eventf(pod, api.EventTypeWarning, events.FailedMountVolume, "Unable to mount volumes for pod %q: %v", format.Pod(pod), err)
		glog.Errorf("Unable to mount volumes for pod %q: %v; skipping pod", format.Pod(pod), err)
		return err
	}

	// Fetch the pull secrets for the pod
	pullSecrets, err := kl.getPullSecretsForPod(pod)
	if err != nil {
		glog.Errorf("Unable to get pull secrets for pod %q: %v", format.Pod(pod), err)
		return err
	}

	// Call the container runtime's SyncPod callback
	result := kl.containerRuntime.SyncPod(pod, apiPodStatus, podStatus, pullSecrets, kl.backOff)
	kl.reasonCache.Update(pod.UID, result)
	if err = result.Error(); err != nil {
		return err
	}

	// early successful exit if pod is not bandwidth-constrained
	if !kl.shapingEnabled() {
		return nil
	}

	// Update the traffic shaping for the pod's ingress and egress limits
	ingress, egress, err := bandwidth.ExtractPodBandwidthResources(pod.Annotations)
	if err != nil {
		return err
	}
	if egress != nil || ingress != nil {
		if podUsesHostNetwork(pod) {
			kl.recorder.Event(pod, api.EventTypeWarning, events.HostNetworkNotSupported, "Bandwidth shaping is not currently supported on the host network")
		} else if kl.shaper != nil {
			if len(apiPodStatus.PodIP) > 0 {
				err = kl.shaper.ReconcileCIDR(fmt.Sprintf("%s/32", apiPodStatus.PodIP), egress, ingress)
			}
		} else {
			kl.recorder.Event(pod, api.EventTypeWarning, events.UndefinedShaper, "Pod requests bandwidth shaping, but the shaper is undefined")
		}
	}

	return nil
}

// getPullSecretsForPod inspects the Pod and retrieves the referenced pull
// secrets.
// TODO: duplicate secrets are being retrieved multiple times and there
// is no cache.  Creating and using a secret manager interface will make this
// easier to address.
func (kl *Kubelet) getPullSecretsForPod(pod *api.Pod) ([]api.Secret, error) {
	pullSecrets := []api.Secret{}

	for _, secretRef := range pod.Spec.ImagePullSecrets {
		secret, err := kl.kubeClient.Core().Secrets(pod.Namespace).Get(secretRef.Name)
		if err != nil {
			glog.Warningf("Unable to retrieve pull secret %s/%s for %s/%s due to %v.  The image pull may not succeed.", pod.Namespace, secretRef.Name, pod.Namespace, pod.Name, err)
			continue
		}

		pullSecrets = append(pullSecrets, *secret)
	}

	return pullSecrets, nil
}

// Get pods which should be resynchronized. Currently, the following pod should be resynchronized:
//   * pod whose work is ready.
//   * internal modules that request sync of a pod.
func (kl *Kubelet) getPodsToSync() []*api.Pod {
	allPods := kl.podManager.GetPods()
	podUIDs := kl.workQueue.GetWork()
	podUIDSet := sets.NewString()
	for _, podUID := range podUIDs {
		podUIDSet.Insert(string(podUID))
	}
	var podsToSync []*api.Pod
	for _, pod := range allPods {
		if podUIDSet.Has(string(pod.UID)) {
			// The work of the pod is ready
			podsToSync = append(podsToSync, pod)
			continue
		}
		for _, podSyncLoopHandler := range kl.PodSyncLoopHandlers {
			if podSyncLoopHandler.ShouldSync(pod) {
				podsToSync = append(podsToSync, pod)
				break
			}
		}
	}
	return podsToSync
}

// HandlePodCleanups performs a series of cleanup work, including terminating
// pod workers, killing unwanted pods, and removing orphaned volumes/pod
// directories.
// NOTE: This function is executed by the main sync loop, so it
// should not contain any blocking calls.
func (kl *Kubelet) HandlePodCleanups() error {
	allPods, mirrorPods := kl.podManager.GetPodsAndMirrorPods()
	// Pod phase progresses monotonically. Once a pod has reached a final state,
	// it should never leave regardless of the restart policy. The statuses
	// of such pods should not be changed, and there is no need to sync them.
	// TODO: the logic here does not handle two cases:
	//   1. If the containers were removed immediately after they died, kubelet
	//      may fail to generate correct statuses, let alone filtering correctly.
	//   2. If kubelet restarted before writing the terminated status for a pod
	//      to the apiserver, it could still restart the terminated pod (even
	//      though the pod was not considered terminated by the apiserver).
	// These two conditions could be alleviated by checkpointing kubelet.
	activePods := kl.filterOutTerminatedPods(allPods)

	desiredPods := make(map[types.UID]empty)
	for _, pod := range activePods {
		desiredPods[pod.UID] = empty{}
	}
	// Stop the workers for no-longer existing pods.
	// TODO: is here the best place to forget pod workers?
	kl.podWorkers.ForgetNonExistingPodWorkers(desiredPods)
	kl.probeManager.CleanupPods(activePods)

	runningPods, err := kl.runtimeCache.GetPods()
	if err != nil {
		glog.Errorf("Error listing containers: %#v", err)
		return err
	}
	for _, pod := range runningPods {
		if _, found := desiredPods[pod.ID]; !found {
			kl.podKillingCh <- &kubecontainer.PodPair{APIPod: nil, RunningPod: pod}
		}
	}

	kl.removeOrphanedPodStatuses(allPods, mirrorPods)
	// Note that we just killed the unwanted pods. This may not have reflected
	// in the cache. We need to bypass the cache to get the latest set of
	// running pods to clean up the volumes.
	// TODO: Evaluate the performance impact of bypassing the runtime cache.
	runningPods, err = kl.containerRuntime.GetPods(false)
	if err != nil {
		glog.Errorf("Error listing containers: %#v", err)
		return err
	}

	// Remove any orphaned volumes.
	// Note that we pass all pods (including terminated pods) to the function,
	// so that we don't remove volumes associated with terminated but not yet
	// deleted pods.
	err = kl.cleanupOrphanedPodDirs(allPods, runningPods)
	if err != nil {
		// We want all cleanup tasks to be run even if one of them failed. So
		// we just log an error here and continue other cleanup tasks.
		// This also applies to the other clean up tasks.
		glog.Errorf("Failed cleaning up orphaned pod directories: %v", err)
	}

	// Remove any orphaned mirror pods.
	kl.podManager.DeleteOrphanedMirrorPods()

	// Clear out any old bandwidth rules
	err = kl.cleanupBandwidthLimits(allPods)
	if err != nil {
		glog.Errorf("Failed cleaning up bandwidth limits: %v", err)
	}

	kl.backOff.GC()
	return nil
}

// podKiller launches a goroutine to kill a pod received from the channel if
// another goroutine isn't already in action.
func (kl *Kubelet) podKiller() {
	killing := sets.NewString()
	resultCh := make(chan types.UID)
	defer close(resultCh)
	for {
		select {
		case podPair, ok := <-kl.podKillingCh:
			if !ok {
				return
			}

			runningPod := podPair.RunningPod
			apiPod := podPair.APIPod

			if killing.Has(string(runningPod.ID)) {
				// The pod is already being killed.
				break
			}
			killing.Insert(string(runningPod.ID))
			go func(apiPod *api.Pod, runningPod *kubecontainer.Pod, ch chan types.UID) {
				defer func() {
					ch <- runningPod.ID
				}()
				glog.V(2).Infof("Killing unwanted pod %q", runningPod.Name)
				err := kl.killPod(apiPod, runningPod, nil, nil)
				if err != nil {
					glog.Errorf("Failed killing the pod %q: %v", runningPod.Name, err)
				}
			}(apiPod, runningPod, resultCh)

		case podID := <-resultCh:
			killing.Delete(string(podID))
		}
	}
}

// handleOutOfDisk detects if pods can't fit due to lack of disk space.
func (kl *Kubelet) isOutOfDisk() bool {
	// Check disk space once globally and reject or accept all new pods.
	withinBounds, err := kl.diskSpaceManager.IsRuntimeDiskSpaceAvailable()
	// Assume enough space in case of errors.
	if err != nil {
		glog.Errorf("Failed to check if disk space is available for the runtime: %v", err)
	} else if !withinBounds {
		return true
	}

	withinBounds, err = kl.diskSpaceManager.IsRootDiskSpaceAvailable()
	// Assume enough space in case of errors.
	if err != nil {
		glog.Errorf("Failed to check if disk space is available on the root partition: %v", err)
	} else if !withinBounds {
		return true
	}
	return false
}

// rejectPod records an event about the pod with the given reason and message,
// and updates the pod to the failed phase in the status manage.
func (kl *Kubelet) rejectPod(pod *api.Pod, reason, message string) {
	kl.recorder.Eventf(pod, api.EventTypeWarning, reason, message)
	kl.statusManager.SetPodStatus(pod, api.PodStatus{
		Phase:   api.PodFailed,
		Reason:  reason,
		Message: "Pod " + message})
}

// canAdmitPod determines if a pod can be admitted, and gives a reason if it
// cannot. "pod" is new pod, while "pods" are all admitted pods
// The function returns a boolean value indicating whether the pod
// can be admitted, a brief single-word reason and a message explaining why
// the pod cannot be admitted.
func (kl *Kubelet) canAdmitPod(pods []*api.Pod, pod *api.Pod) (bool, string, string) {
	node, err := kl.getNodeAnyWay()
	if err != nil {
		glog.Errorf("Cannot get Node info: %v", err)
		return false, "InvalidNodeInfo", "Kubelet cannot get node info."
	}

	// the kubelet will invoke each pod admit handler in sequence
	// if any handler rejects, the pod is rejected.
	// TODO: move predicate check into a pod admitter
	// TODO: move out of disk check into a pod admitter
	// TODO: out of resource eviction should have a pod admitter call-out
	attrs := &lifecycle.PodAdmitAttributes{Pod: pod, OtherPods: pods}
	for _, podAdmitHandler := range kl.PodAdmitHandlers {
		if result := podAdmitHandler.Admit(attrs); !result.Admit {
			return false, result.Reason, result.Message
		}
	}
	nodeInfo := schedulercache.NewNodeInfo(pods...)
	nodeInfo.SetNode(node)
	fit, reasons, err := predicates.GeneralPredicates(pod, nil, nodeInfo)
	if err != nil {
		message := fmt.Sprintf("GeneralPredicates failed due to %v, which is unexpected.", err)
		glog.Warningf("Failed to admit pod %v - %s", format.Pod(pod), message)
		return fit, "UnexpectedError", message
	}
	if !fit {
		var reason string
		var message string
		if len(reasons) == 0 {
			message = fmt.Sprint("GeneralPredicates failed due to unknown reason, which is unexpected.")
			glog.Warningf("Failed to admit pod %v - %s", format.Pod(pod), message)
			return fit, "UnknownReason", message
		}
		// If there are failed predicates, we only return the first one as a reason.
		r := reasons[0]
		switch re := r.(type) {
		case *predicates.PredicateFailureError:
			reason = re.PredicateName
			message = re.Error()
			glog.V(2).Infof("Predicate failed on Pod: %v, for reason: %v", format.Pod(pod), message)
		case *predicates.InsufficientResourceError:
			reason = fmt.Sprintf("OutOf%s", re.ResourceName)
			message := re.Error()
			glog.V(2).Infof("Predicate failed on Pod: %v, for reason: %v", format.Pod(pod), message)
		case *predicates.FailureReason:
			reason = re.GetReason()
			message = fmt.Sprintf("Failure: %s", re.GetReason())
			glog.V(2).Infof("Predicate failed on Pod: %v, for reason: %v", format.Pod(pod), message)
		default:
			reason = "UnexpectedPredicateFailureType"
			message := fmt.Sprintf("GeneralPredicates failed due to %v, which is unexpected.", r)
			glog.Warningf("Failed to admit pod %v - %s", format.Pod(pod), message)
		}
		return fit, reason, message
	}
	// TODO: When disk space scheduling is implemented (#11976), remove the out-of-disk check here and
	// add the disk space predicate to predicates.GeneralPredicates.
	if kl.isOutOfDisk() {
		glog.Warningf("Failed to admit pod %v - %s", format.Pod(pod), "predicate fails due to OutOfDisk")
		return false, "OutOfDisk", "cannot be started due to lack of disk space."
	}

	return true, "", ""
}

// syncLoop is the main loop for processing changes. It watches for changes from
// three channels (file, apiserver, and http) and creates a union of them. For
// any new change seen, will run a sync against desired state and running state. If
// no changes are seen to the configuration, will synchronize the last known desired
// state every sync-frequency seconds. Never returns.
func (kl *Kubelet) syncLoop(updates <-chan kubetypes.PodUpdate, handler SyncHandler) {
	glog.Info("Starting kubelet main sync loop.")
	// The resyncTicker wakes up kubelet to checks if there are any pod workers
	// that need to be sync'd. A one-second period is sufficient because the
	// sync interval is defaulted to 10s.
	syncTicker := time.NewTicker(time.Second)
	defer syncTicker.Stop()
	housekeepingTicker := time.NewTicker(housekeepingPeriod)
	defer housekeepingTicker.Stop()
	plegCh := kl.pleg.Watch()
	for {
		if rs := kl.runtimeState.errors(); len(rs) != 0 {
			glog.Infof("skipping pod synchronization - %v", rs)
			time.Sleep(5 * time.Second)
			continue
		}
		if !kl.syncLoopIteration(updates, handler, syncTicker.C, housekeepingTicker.C, plegCh) {
			break
		}
	}
}

// syncLoopIteration reads from various channels and dispatches pods to the
// given handler.
//
// Arguments:
// 1.  configCh:       a channel to read config events from
// 2.  handler:        the SyncHandler to dispatch pods to
// 3.  syncCh:         a channel to read periodic sync events from
// 4.  houseKeepingCh: a channel to read housekeeping events from
// 5.  plegCh:         a channel to read PLEG updates from
//
// Events are also read from the kubelet liveness manager's update channel.
//
// The workflow is to read from one of the channels, handle that event, and
// update the timestamp in the sync loop monitor.
//
// Here is an appropriate place to note that despite the syntactical
// similarity to the switch statement, the case statements in a select are
// evaluated in a pseudorandom order if there are multiple channels ready to
// read from when the select is evaluated.  In other words, case statements
// are evaluated in random order, and you can not assume that the case
// statements evaluate in order if multiple channels have events.
//
// With that in mind, in truly no particular order, the different channels
// are handled as follows:
//
// * configCh: dispatch the pods for the config change to the appropriate
//             handler callback for the event type
// * plegCh: update the runtime cache; sync pod
// * syncCh: sync all pods waiting for sync
// * houseKeepingCh: trigger cleanup of pods
// * liveness manager: sync pods that have failed or in which one or more
//                     containers have failed liveness checks
func (kl *Kubelet) syncLoopIteration(configCh <-chan kubetypes.PodUpdate, handler SyncHandler,
	syncCh <-chan time.Time, housekeepingCh <-chan time.Time, plegCh <-chan *pleg.PodLifecycleEvent) bool {
	kl.syncLoopMonitor.Store(kl.clock.Now())
	select {
	case u, open := <-configCh:
		// Update from a config source; dispatch it to the right handler
		// callback.
		if !open {
			glog.Errorf("Update channel is closed. Exiting the sync loop.")
			return false
		}

		switch u.Op {
		case kubetypes.ADD:
			glog.V(2).Infof("SyncLoop (ADD, %q): %q", u.Source, format.Pods(u.Pods))
			// After restarting, kubelet will get all existing pods through
			// ADD as if they are new pods. These pods will then go through the
			// admission process and *may* be rejcted. This can be resolved
			// once we have checkpointing.
			handler.HandlePodAdditions(u.Pods)
		case kubetypes.UPDATE:
			glog.V(2).Infof("SyncLoop (UPDATE, %q): %q", u.Source, format.PodsWithDeletiontimestamps(u.Pods))
			handler.HandlePodUpdates(u.Pods)
		case kubetypes.REMOVE:
			glog.V(2).Infof("SyncLoop (REMOVE, %q): %q", u.Source, format.Pods(u.Pods))
			handler.HandlePodRemoves(u.Pods)
		case kubetypes.RECONCILE:
			glog.V(4).Infof("SyncLoop (RECONCILE, %q): %q", u.Source, format.Pods(u.Pods))
			handler.HandlePodReconcile(u.Pods)
		case kubetypes.DELETE:
			glog.V(2).Infof("SyncLoop (DELETE, %q): %q", u.Source, format.Pods(u.Pods))
			// DELETE is treated as a UPDATE because of graceful deletion.
			handler.HandlePodUpdates(u.Pods)
		case kubetypes.SET:
			// TODO: Do we want to support this?
			glog.Errorf("Kubelet does not support snapshot update")
		}

		// Mark the source ready after receiving at least one update from the
		// source. Once all the sources are marked ready, various cleanup
		// routines will start reclaiming resources. It is important that this
		// takes place only after kubelet calls the update handler to process
		// the update to ensure the internal pod cache is up-to-date.
		kl.sourcesReady.AddSource(u.Source)

	case e := <-plegCh:
		if isSyncPodWorthy(e) {
			// PLEG event for a pod; sync it.
			if pod, ok := kl.podManager.GetPodByUID(e.ID); ok {
				glog.V(2).Infof("SyncLoop (PLEG): %q, event: %#v", format.Pod(pod), e)
				handler.HandlePodSyncs([]*api.Pod{pod})
			} else {
				// If the pod no longer exists, ignore the event.
				glog.V(4).Infof("SyncLoop (PLEG): ignore irrelevant event: %#v", e)
			}
		}

		if e.Type == pleg.ContainerDied {
			if containerID, ok := e.Data.(string); ok {
				kl.cleanUpContainersInPod(e.ID, containerID)
			}
		}
	case <-syncCh:
		// Sync pods waiting for sync
		podsToSync := kl.getPodsToSync()
		if len(podsToSync) == 0 {
			break
		}
		glog.V(4).Infof("SyncLoop (SYNC): %d pods; %s", len(podsToSync), format.Pods(podsToSync))
		kl.HandlePodSyncs(podsToSync)
	case update := <-kl.livenessManager.Updates():
		if update.Result == proberesults.Failure {
			// The liveness manager detected a failure; sync the pod.

			// We should not use the pod from livenessManager, because it is never updated after
			// initialization.
			pod, ok := kl.podManager.GetPodByUID(update.PodUID)
			if !ok {
				// If the pod no longer exists, ignore the update.
				glog.V(4).Infof("SyncLoop (container unhealthy): ignore irrelevant update: %#v", update)
				break
			}
			glog.V(1).Infof("SyncLoop (container unhealthy): %q", format.Pod(pod))
			handler.HandlePodSyncs([]*api.Pod{pod})
		}
	case <-housekeepingCh:
		if !kl.sourcesReady.AllReady() {
			// If the sources aren't ready, skip housekeeping, as we may
			// accidentally delete pods from unready sources.
			glog.V(4).Infof("SyncLoop (housekeeping, skipped): sources aren't ready yet.")
		} else {
			glog.V(4).Infof("SyncLoop (housekeeping)")
			if err := handler.HandlePodCleanups(); err != nil {
				glog.Errorf("Failed cleaning pods: %v", err)
			}
		}
	}
	kl.syncLoopMonitor.Store(kl.clock.Now())
	return true
}

// dispatchWork starts the asynchronous sync of the pod in a pod worker.
// If the pod is terminated, dispatchWork
func (kl *Kubelet) dispatchWork(pod *api.Pod, syncType kubetypes.SyncPodType, mirrorPod *api.Pod, start time.Time) {
	if kl.podIsTerminated(pod) {
		if pod.DeletionTimestamp != nil {
			// If the pod is in a terminated state, there is no pod worker to
			// handle the work item. Check if the DeletionTimestamp has been
			// set, and force a status update to trigger a pod deletion request
			// to the apiserver.
			kl.statusManager.TerminatePod(pod)
		}
		return
	}
	// Run the sync in an async worker.
	kl.podWorkers.UpdatePod(&UpdatePodOptions{
		Pod:        pod,
		MirrorPod:  mirrorPod,
		UpdateType: syncType,
		OnCompleteFunc: func(err error) {
			if err != nil {
				metrics.PodWorkerLatency.WithLabelValues(syncType.String()).Observe(metrics.SinceInMicroseconds(start))
			}
		},
	})
	// Note the number of containers for new pods.
	if syncType == kubetypes.SyncPodCreate {
		metrics.ContainersPerPodCount.Observe(float64(len(pod.Spec.Containers)))
	}
}

// TODO: handle mirror pods in a separate component (issue #17251)
func (kl *Kubelet) handleMirrorPod(mirrorPod *api.Pod, start time.Time) {
	// Mirror pod ADD/UPDATE/DELETE operations are considered an UPDATE to the
	// corresponding static pod. Send update to the pod worker if the static
	// pod exists.
	if pod, ok := kl.podManager.GetPodByMirrorPod(mirrorPod); ok {
		kl.dispatchWork(pod, kubetypes.SyncPodUpdate, mirrorPod, start)
	}
}

// HandlePodAdditions is the callback in SyncHandler for pods being added from
// a config source.
func (kl *Kubelet) HandlePodAdditions(pods []*api.Pod) {
	start := kl.clock.Now()
	sort.Sort(podsByCreationTime(pods))
	for _, pod := range pods {
		if kubepod.IsMirrorPod(pod) {
			kl.podManager.AddPod(pod)
			kl.handleMirrorPod(pod, start)
			continue
		}
		// Note that allPods excludes the new pod.
		allPods := kl.podManager.GetPods()
		// We failed pods that we rejected, so activePods include all admitted
		// pods that are alive.
		activePods := kl.filterOutTerminatedPods(allPods)
		// Check if we can admit the pod; if not, reject it.
		if ok, reason, message := kl.canAdmitPod(activePods, pod); !ok {
			kl.rejectPod(pod, reason, message)
			continue
		}
		kl.podManager.AddPod(pod)
		mirrorPod, _ := kl.podManager.GetMirrorPodByPod(pod)
		kl.dispatchWork(pod, kubetypes.SyncPodCreate, mirrorPod, start)
		kl.probeManager.AddPod(pod)
	}
}

// HandlePodUpdates is the callback in the SyncHandler interface for pods
// being updated from a config source.
func (kl *Kubelet) HandlePodUpdates(pods []*api.Pod) {
	start := kl.clock.Now()
	for _, pod := range pods {
		kl.podManager.UpdatePod(pod)
		if kubepod.IsMirrorPod(pod) {
			kl.handleMirrorPod(pod, start)
			continue
		}
		// TODO: Evaluate if we need to validate and reject updates.

		mirrorPod, _ := kl.podManager.GetMirrorPodByPod(pod)
		kl.dispatchWork(pod, kubetypes.SyncPodUpdate, mirrorPod, start)
	}
}

// HandlePodRemoves is the callback in the SyncHandler interface for pods
// being removed from a config source.
func (kl *Kubelet) HandlePodRemoves(pods []*api.Pod) {
	start := kl.clock.Now()
	for _, pod := range pods {
		kl.podManager.DeletePod(pod)
		if kubepod.IsMirrorPod(pod) {
			kl.handleMirrorPod(pod, start)
			continue
		}
		// Deletion is allowed to fail because the periodic cleanup routine
		// will trigger deletion again.
		if err := kl.deletePod(pod); err != nil {
			glog.V(2).Infof("Failed to delete pod %q, err: %v", format.Pod(pod), err)
		}
		kl.probeManager.RemovePod(pod)
	}
}

// HandlePodReconcile is the callback in the SyncHandler interface for pods
// that should be reconciled.
func (kl *Kubelet) HandlePodReconcile(pods []*api.Pod) {
	for _, pod := range pods {
		// Update the pod in pod manager, status manager will do periodically reconcile according
		// to the pod manager.
		kl.podManager.UpdatePod(pod)
	}
}

// HandlePodSyncs is the callback in the syncHandler interface for pods
// that should be dispatched to pod workers for sync.
func (kl *Kubelet) HandlePodSyncs(pods []*api.Pod) {
	start := kl.clock.Now()
	for _, pod := range pods {
		mirrorPod, _ := kl.podManager.GetMirrorPodByPod(pod)
		kl.dispatchWork(pod, kubetypes.SyncPodSync, mirrorPod, start)
	}
}

// LatestLoopEntryTime returns the last time in the sync loop monitor.
func (kl *Kubelet) LatestLoopEntryTime() time.Time {
	val := kl.syncLoopMonitor.Load()
	if val == nil {
		return time.Time{}
	}
	return val.(time.Time)
}

// PLEGHealthCheck returns whether the PLEG is healty.
func (kl *Kubelet) PLEGHealthCheck() (bool, error) {
	return kl.pleg.Healthy()
}

// updateRuntimeUp calls the container runtime status callback, initializing
// the runtime dependent modules when the container runtime first comes up,
// and returns an error if the status check fails.  If the status check is OK,
// update the container runtime uptime in the kubelet runtimeState.
func (kl *Kubelet) updateRuntimeUp() {
	if err := kl.containerRuntime.Status(); err != nil {
		glog.Errorf("Container runtime sanity check failed: %v", err)
		return
	}
	kl.oneTimeInitializer.Do(kl.initializeRuntimeDependentModules)
	kl.runtimeState.setRuntimeSync(kl.clock.Now())
}

func (kl *Kubelet) updateCloudProviderFromMachineInfo(node *api.Node, info *cadvisorapi.MachineInfo) {
	if info.CloudProvider != cadvisorapi.UnknownProvider &&
		info.CloudProvider != cadvisorapi.Baremetal {
		// The cloud providers from pkg/cloudprovider/providers/* that update ProviderID
		// will use the format of cloudprovider://project/availability_zone/instance_name
		// here we only have the cloudprovider and the instance name so we leave project
		// and availability zone empty for compatibility.
		node.Spec.ProviderID = strings.ToLower(string(info.CloudProvider)) +
			":////" + string(info.InstanceID)
	}
}

// BirthCry sends an event that the kubelet has started up.
func (kl *Kubelet) BirthCry() {
	// Make an event that kubelet restarted.
	kl.recorder.Eventf(kl.nodeRef, api.EventTypeNormal, events.StartingKubelet, "Starting kubelet.")
}

// StreamingConnectionIdleTimeout returns the timeout for streaming connections to the HTTP server.
func (kl *Kubelet) StreamingConnectionIdleTimeout() time.Duration {
	return kl.streamingConnectionIdleTimeout
}

// ResyncInterval returns the interval used for periodic syncs.
func (kl *Kubelet) ResyncInterval() time.Duration {
	return kl.resyncInterval
}

// ListenAndServe runs the kubelet HTTP server.
func (kl *Kubelet) ListenAndServe(address net.IP, port uint, tlsOptions *server.TLSOptions, auth server.AuthInterface, enableDebuggingHandlers bool) {
	server.ListenAndServeKubeletServer(kl, kl.resourceAnalyzer, address, port, tlsOptions, auth, enableDebuggingHandlers, kl.containerRuntime)
}

// ListenAndServeReadOnly runs the kubelet HTTP server in read-only mode.
func (kl *Kubelet) ListenAndServeReadOnly(address net.IP, port uint) {
	server.ListenAndServeKubeletReadOnlyServer(kl, kl.resourceAnalyzer, address, port, kl.containerRuntime)
}

// Delete the eligible dead container instances in a pod. Depending on the configuration, the latest dead containers may be kept around.
func (kl *Kubelet) cleanUpContainersInPod(podId types.UID, exitedContainerID string) {
	if podStatus, err := kl.podCache.Get(podId); err == nil {
		if status, ok := kl.statusManager.GetPodStatus(podId); ok {
			// If a pod is evicted, we can delete all the dead containers.
			kl.containerDeletor.deleteContainersInPod(exitedContainerID, podStatus, eviction.PodIsEvicted(status))
		}
	}
}

// isSyncPodWorthy filters out events that are not worthy of pod syncing
func isSyncPodWorthy(event *pleg.PodLifecycleEvent) bool {
	// ContatnerRemoved doesn't affect pod state
	return event.Type != pleg.ContainerRemoved
}
