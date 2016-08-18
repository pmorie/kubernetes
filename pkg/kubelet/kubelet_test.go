/*
Copyright 2014 The Kubernetes Authors.

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
	"fmt"
	"io/ioutil"
	"os"
	"reflect"
	"testing"
	"time"

	cadvisorapi "github.com/google/cadvisor/info/v1"
	cadvisorapiv2 "github.com/google/cadvisor/info/v2"
	"github.com/stretchr/testify/assert"
	"k8s.io/kubernetes/pkg/api"
	"k8s.io/kubernetes/pkg/api/resource"
	"k8s.io/kubernetes/pkg/api/unversioned"
	"k8s.io/kubernetes/pkg/apis/componentconfig"
	"k8s.io/kubernetes/pkg/capabilities"
	"k8s.io/kubernetes/pkg/client/clientset_generated/internalclientset/fake"
	"k8s.io/kubernetes/pkg/client/record"
	"k8s.io/kubernetes/pkg/client/testing/core"
	cadvisortest "k8s.io/kubernetes/pkg/kubelet/cadvisor/testing"
	"k8s.io/kubernetes/pkg/kubelet/cm"
	"k8s.io/kubernetes/pkg/kubelet/config"
	kubecontainer "k8s.io/kubernetes/pkg/kubelet/container"
	containertest "k8s.io/kubernetes/pkg/kubelet/container/testing"
	"k8s.io/kubernetes/pkg/kubelet/eviction"
	"k8s.io/kubernetes/pkg/kubelet/images"
	"k8s.io/kubernetes/pkg/kubelet/lifecycle"
	"k8s.io/kubernetes/pkg/kubelet/network"
	nettest "k8s.io/kubernetes/pkg/kubelet/network/testing"
	"k8s.io/kubernetes/pkg/kubelet/pleg"
	kubepod "k8s.io/kubernetes/pkg/kubelet/pod"
	podtest "k8s.io/kubernetes/pkg/kubelet/pod/testing"
	proberesults "k8s.io/kubernetes/pkg/kubelet/prober/results"
	probetest "k8s.io/kubernetes/pkg/kubelet/prober/testing"
	"k8s.io/kubernetes/pkg/kubelet/server/stats"
	"k8s.io/kubernetes/pkg/kubelet/status"
	kubetypes "k8s.io/kubernetes/pkg/kubelet/types"
	"k8s.io/kubernetes/pkg/kubelet/util/queue"
	kubeletvolume "k8s.io/kubernetes/pkg/kubelet/volumemanager"
	"k8s.io/kubernetes/pkg/runtime"
	"k8s.io/kubernetes/pkg/types"
	"k8s.io/kubernetes/pkg/util/clock"
	"k8s.io/kubernetes/pkg/util/flowcontrol"
	"k8s.io/kubernetes/pkg/util/mount"
	utilruntime "k8s.io/kubernetes/pkg/util/runtime"
	"k8s.io/kubernetes/pkg/util/sets"
	"k8s.io/kubernetes/pkg/util/wait"
	"k8s.io/kubernetes/pkg/volume"
	_ "k8s.io/kubernetes/pkg/volume/host_path"
	volumetest "k8s.io/kubernetes/pkg/volume/testing"
	"k8s.io/kubernetes/pkg/volume/util/volumehelper"
)

func init() {
	utilruntime.ReallyCrash = true
}

const (
	testKubeletHostname = "127.0.0.1"

	testReservationCPU    = "200m"
	testReservationMemory = "100M"

	maxImageTagsForTest = 3

	// TODO(harry) any global place for these two?
	// Reasonable size range of all container images. 90%ile of images on dockerhub drops into this range.
	minImgSize int64 = 23 * 1024 * 1024
	maxImgSize int64 = 1000 * 1024 * 1024
)

type TestKubelet struct {
	kubelet          *Kubelet
	fakeRuntime      *containertest.FakeRuntime
	fakeCadvisor     *cadvisortest.Mock
	fakeKubeClient   *fake.Clientset
	fakeMirrorClient *podtest.FakeMirrorClient
	fakeClock        *clock.FakeClock
	mounter          mount.Interface
	volumePlugin     *volumetest.FakeVolumePlugin
}

// newTestKubelet returns test kubelet with two images.
func newTestKubelet(t *testing.T, controllerAttachDetachEnabled bool) *TestKubelet {
	imageList := []kubecontainer.Image{
		{
			ID:       "abc",
			RepoTags: []string{"gcr.io/google_containers:v1", "gcr.io/google_containers:v2"},
			Size:     123,
		},
		{
			ID:       "efg",
			RepoTags: []string{"gcr.io/google_containers:v3", "gcr.io/google_containers:v4"},
			Size:     456,
		},
	}
	return newTestKubeletWithImageList(t, imageList, controllerAttachDetachEnabled)
}

func newTestKubeletWithImageList(
	t *testing.T,
	imageList []kubecontainer.Image,
	controllerAttachDetachEnabled bool) *TestKubelet {
	fakeRuntime := &containertest.FakeRuntime{}
	fakeRuntime.RuntimeType = "test"
	fakeRuntime.VersionInfo = "1.5.0"
	fakeRuntime.ImageList = imageList
	fakeRecorder := &record.FakeRecorder{}
	fakeKubeClient := &fake.Clientset{}
	kubelet := &Kubelet{}
	kubelet.recorder = fakeRecorder
	kubelet.kubeClient = fakeKubeClient
	kubelet.os = &containertest.FakeOS{}

	kubelet.hostname = testKubeletHostname
	kubelet.nodeName = testKubeletHostname
	kubelet.runtimeState = newRuntimeState(maxWaitForContainerRuntime)
	kubelet.runtimeState.setNetworkState(nil)
	kubelet.networkPlugin, _ = network.InitNetworkPlugin([]network.NetworkPlugin{}, "", nettest.NewFakeHost(nil), componentconfig.HairpinNone, kubelet.nonMasqueradeCIDR)
	if tempDir, err := ioutil.TempDir("/tmp", "kubelet_test."); err != nil {
		t.Fatalf("can't make a temp rootdir: %v", err)
	} else {
		kubelet.rootDirectory = tempDir
	}
	if err := os.MkdirAll(kubelet.rootDirectory, 0750); err != nil {
		t.Fatalf("can't mkdir(%q): %v", kubelet.rootDirectory, err)
	}
	kubelet.sourcesReady = config.NewSourcesReady(func(_ sets.String) bool { return true })
	kubelet.masterServiceNamespace = api.NamespaceDefault
	kubelet.serviceLister = testServiceLister{}
	kubelet.nodeLister = testNodeLister{}
	kubelet.nodeInfo = testNodeInfo{}
	kubelet.recorder = fakeRecorder
	if err := kubelet.setupDataDirs(); err != nil {
		t.Fatalf("can't initialize kubelet data dirs: %v", err)
	}
	kubelet.daemonEndpoints = &api.NodeDaemonEndpoints{}

	mockCadvisor := &cadvisortest.Mock{}
	kubelet.cadvisor = mockCadvisor

	fakeMirrorClient := podtest.NewFakeMirrorClient()
	kubelet.podManager = kubepod.NewBasicPodManager(fakeMirrorClient)
	kubelet.statusManager = status.NewManager(fakeKubeClient, kubelet.podManager)
	kubelet.containerRefManager = kubecontainer.NewRefManager()
	diskSpaceManager, err := newDiskSpaceManager(mockCadvisor, DiskSpacePolicy{})
	if err != nil {
		t.Fatalf("can't initialize disk space manager: %v", err)
	}
	kubelet.diskSpaceManager = diskSpaceManager

	kubelet.containerRuntime = fakeRuntime
	kubelet.runtimeCache = containertest.NewFakeRuntimeCache(kubelet.containerRuntime)
	kubelet.reasonCache = NewReasonCache()
	kubelet.podCache = containertest.NewFakeCache(kubelet.containerRuntime)
	kubelet.podWorkers = &fakePodWorkers{
		syncPodFn: kubelet.syncPod,
		cache:     kubelet.podCache,
		t:         t,
	}

	kubelet.probeManager = probetest.FakeManager{}
	kubelet.livenessManager = proberesults.NewManager()

	kubelet.containerManager = cm.NewStubContainerManager()
	fakeNodeRef := &api.ObjectReference{
		Kind:      "Node",
		Name:      testKubeletHostname,
		UID:       types.UID(testKubeletHostname),
		Namespace: "",
	}
	fakeImageGCPolicy := images.ImageGCPolicy{
		HighThresholdPercent: 90,
		LowThresholdPercent:  80,
	}
	kubelet.imageManager, err = images.NewImageGCManager(fakeRuntime, mockCadvisor, fakeRecorder, fakeNodeRef, fakeImageGCPolicy)
	fakeClock := clock.NewFakeClock(time.Now())
	kubelet.backOff = flowcontrol.NewBackOff(time.Second, time.Minute)
	kubelet.backOff.Clock = fakeClock
	kubelet.podKillingCh = make(chan *kubecontainer.PodPair, 20)
	kubelet.resyncInterval = 10 * time.Second
	kubelet.reservation = kubetypes.Reservation{
		Kubernetes: api.ResourceList{
			api.ResourceCPU:    resource.MustParse(testReservationCPU),
			api.ResourceMemory: resource.MustParse(testReservationMemory),
		},
	}
	kubelet.workQueue = queue.NewBasicWorkQueue(fakeClock)
	// Relist period does not affect the tests.
	kubelet.pleg = pleg.NewGenericPLEG(fakeRuntime, 100, time.Hour, nil, clock.RealClock{})
	kubelet.clock = fakeClock
	kubelet.setNodeStatusFuncs = kubelet.defaultNodeStatusFuncs()

	// TODO: Factor out "StatsProvider" from Kubelet so we don't have a cyclic dependency
	volumeStatsAggPeriod := time.Second * 10
	kubelet.resourceAnalyzer = stats.NewResourceAnalyzer(kubelet, volumeStatsAggPeriod, kubelet.containerRuntime)
	nodeRef := &api.ObjectReference{
		Kind:      "Node",
		Name:      kubelet.nodeName,
		UID:       types.UID(kubelet.nodeName),
		Namespace: "",
	}
	// setup eviction manager
	evictionManager, evictionAdmitHandler, err := eviction.NewManager(kubelet.resourceAnalyzer, eviction.Config{}, killPodNow(kubelet.podWorkers), kubelet.imageManager, fakeRecorder, nodeRef, kubelet.clock)
	if err != nil {
		t.Fatalf("failed to initialize eviction manager: %v", err)
	}
	kubelet.evictionManager = evictionManager
	kubelet.AddPodAdmitHandler(evictionAdmitHandler)

	plug := &volumetest.FakeVolumePlugin{PluginName: "fake", Host: nil}
	kubelet.volumePluginMgr, err =
		NewInitializedVolumePluginMgr(kubelet, []volume.VolumePlugin{plug})
	if err != nil {
		t.Fatalf("failed to initialize VolumePluginMgr: %v", err)
	}

	kubelet.mounter = &mount.FakeMounter{}
	kubelet.volumeManager, err = kubeletvolume.NewVolumeManager(
		controllerAttachDetachEnabled,
		kubelet.hostname,
		kubelet.podManager,
		fakeKubeClient,
		kubelet.volumePluginMgr,
		fakeRuntime,
		kubelet.mounter,
		kubelet.getPodsDir())
	if err != nil {
		t.Fatalf("failed to initialize volume manager: %v", err)
	}

	// enable active deadline handler
	activeDeadlineHandler, err := newActiveDeadlineHandler(kubelet.statusManager, kubelet.recorder, kubelet.clock)
	if err != nil {
		t.Fatalf("can't initialize active deadline handler: %v", err)
	}
	kubelet.AddPodSyncLoopHandler(activeDeadlineHandler)
	kubelet.AddPodSyncHandler(activeDeadlineHandler)

	return &TestKubelet{kubelet, fakeRuntime, mockCadvisor, fakeKubeClient, fakeMirrorClient, fakeClock, nil, plug}
}

func newTestPods(count int) []*api.Pod {
	pods := make([]*api.Pod, count)
	for i := 0; i < count; i++ {
		pods[i] = &api.Pod{
			Spec: api.PodSpec{
				SecurityContext: &api.PodSecurityContext{
					HostNetwork: true,
				},
			},
			ObjectMeta: api.ObjectMeta{
				UID:  types.UID(10000 + i),
				Name: fmt.Sprintf("pod%d", i),
			},
		}
	}
	return pods
}

var emptyPodUIDs map[types.UID]kubetypes.SyncPodType

func TestSyncLoopTimeUpdate(t *testing.T) {
	testKubelet := newTestKubelet(t, false /* controllerAttachDetachEnabled */)
	testKubelet.fakeCadvisor.On("MachineInfo").Return(&cadvisorapi.MachineInfo{}, nil)
	kubelet := testKubelet.kubelet

	loopTime1 := kubelet.LatestLoopEntryTime()
	if !loopTime1.IsZero() {
		t.Errorf("Unexpected sync loop time: %s, expected 0", loopTime1)
	}

	// Start sync ticker.
	syncCh := make(chan time.Time, 1)
	housekeepingCh := make(chan time.Time, 1)
	plegCh := make(chan *pleg.PodLifecycleEvent)
	syncCh <- time.Now()
	kubelet.syncLoopIteration(make(chan kubetypes.PodUpdate), kubelet, syncCh, housekeepingCh, plegCh)
	loopTime2 := kubelet.LatestLoopEntryTime()
	if loopTime2.IsZero() {
		t.Errorf("Unexpected sync loop time: 0, expected non-zero value.")
	}

	syncCh <- time.Now()
	kubelet.syncLoopIteration(make(chan kubetypes.PodUpdate), kubelet, syncCh, housekeepingCh, plegCh)
	loopTime3 := kubelet.LatestLoopEntryTime()
	if !loopTime3.After(loopTime1) {
		t.Errorf("Sync Loop Time was not updated correctly. Second update timestamp should be greater than first update timestamp")
	}
}

func TestSyncLoopAbort(t *testing.T) {
	testKubelet := newTestKubelet(t, false /* controllerAttachDetachEnabled */)
	testKubelet.fakeCadvisor.On("MachineInfo").Return(&cadvisorapi.MachineInfo{}, nil)
	kubelet := testKubelet.kubelet
	kubelet.runtimeState.setRuntimeSync(time.Now())
	// The syncLoop waits on time.After(resyncInterval), set it really big so that we don't race for
	// the channel close
	kubelet.resyncInterval = time.Second * 30

	ch := make(chan kubetypes.PodUpdate)
	close(ch)

	// sanity check (also prevent this test from hanging in the next step)
	ok := kubelet.syncLoopIteration(ch, kubelet, make(chan time.Time), make(chan time.Time), make(chan *pleg.PodLifecycleEvent, 1))
	if ok {
		t.Fatalf("expected syncLoopIteration to return !ok since update chan was closed")
	}

	// this should terminate immediately; if it hangs then the syncLoopIteration isn't aborting properly
	kubelet.syncLoop(ch, kubelet)
}

func TestSyncPodsStartPod(t *testing.T) {
	testKubelet := newTestKubelet(t, false /* controllerAttachDetachEnabled */)
	testKubelet.fakeCadvisor.On("VersionInfo").Return(&cadvisorapi.VersionInfo{}, nil)
	testKubelet.fakeCadvisor.On("MachineInfo").Return(&cadvisorapi.MachineInfo{}, nil)
	testKubelet.fakeCadvisor.On("ImagesFsInfo").Return(cadvisorapiv2.FsInfo{}, nil)
	testKubelet.fakeCadvisor.On("RootFsInfo").Return(cadvisorapiv2.FsInfo{}, nil)
	kubelet := testKubelet.kubelet
	fakeRuntime := testKubelet.fakeRuntime
	pods := []*api.Pod{
		podWithUidNameNsSpec("12345678", "foo", "new", api.PodSpec{
			Containers: []api.Container{
				{Name: "bar"},
			},
		}),
	}
	kubelet.podManager.SetPods(pods)
	kubelet.HandlePodSyncs(pods)
	fakeRuntime.AssertStartedPods([]string{string(pods[0].UID)})
}

func TestSyncPodsDeletesWhenSourcesAreReady(t *testing.T) {
	ready := false

	testKubelet := newTestKubelet(t, false /* controllerAttachDetachEnabled */)
	fakeRuntime := testKubelet.fakeRuntime
	testKubelet.fakeCadvisor.On("MachineInfo").Return(&cadvisorapi.MachineInfo{}, nil)
	testKubelet.fakeCadvisor.On("ImagesFsInfo").Return(cadvisorapiv2.FsInfo{}, nil)
	testKubelet.fakeCadvisor.On("RootFsInfo").Return(cadvisorapiv2.FsInfo{}, nil)
	kubelet := testKubelet.kubelet
	kubelet.sourcesReady = config.NewSourcesReady(func(_ sets.String) bool { return ready })

	fakeRuntime.PodList = []*containertest.FakePod{
		{Pod: &kubecontainer.Pod{
			ID:        "12345678",
			Name:      "foo",
			Namespace: "new",
			Containers: []*kubecontainer.Container{
				{Name: "bar"},
			},
		}},
	}
	kubelet.HandlePodCleanups()
	// Sources are not ready yet. Don't remove any pods.
	fakeRuntime.AssertKilledPods([]string{})

	ready = true
	kubelet.HandlePodCleanups()

	// Sources are ready. Remove unwanted pods.
	fakeRuntime.AssertKilledPods([]string{"12345678"})
}

func TestVolumeAttachAndMountControllerDisabled(t *testing.T) {
	testKubelet := newTestKubelet(t, false /* controllerAttachDetachEnabled */)
	kubelet := testKubelet.kubelet

	pod := podWithUidNameNsSpec("12345678", "foo", "test", api.PodSpec{
		Volumes: []api.Volume{
			{
				Name: "vol1",
				VolumeSource: api.VolumeSource{
					GCEPersistentDisk: &api.GCEPersistentDiskVolumeSource{
						PDName: "fake-device",
					},
				},
			},
		},
	})

	stopCh := runVolumeManager(kubelet)
	defer func() {
		close(stopCh)
	}()

	kubelet.podManager.SetPods([]*api.Pod{pod})
	err := kubelet.volumeManager.WaitForAttachAndMount(pod)
	if err != nil {
		t.Errorf("Expected success: %v", err)
	}

	podVolumes := kubelet.volumeManager.GetMountedVolumesForPod(
		volumehelper.GetUniquePodName(pod))

	expectedPodVolumes := []string{"vol1"}
	if len(expectedPodVolumes) != len(podVolumes) {
		t.Errorf("Unexpected volumes. Expected %#v got %#v.  Manifest was: %#v", expectedPodVolumes, podVolumes, pod)
	}
	for _, name := range expectedPodVolumes {
		if _, ok := podVolumes[name]; !ok {
			t.Errorf("api.Pod volumes map is missing key: %s. %#v", name, podVolumes)
		}
	}
	if testKubelet.volumePlugin.GetNewAttacherCallCount() < 1 {
		t.Errorf("Expected plugin NewAttacher to be called at least once")
	}

	err = volumetest.VerifyWaitForAttachCallCount(
		1 /* expectedWaitForAttachCallCount */, testKubelet.volumePlugin)
	if err != nil {
		t.Error(err)
	}

	err = volumetest.VerifyAttachCallCount(
		1 /* expectedAttachCallCount */, testKubelet.volumePlugin)
	if err != nil {
		t.Error(err)
	}

	err = volumetest.VerifyMountDeviceCallCount(
		1 /* expectedMountDeviceCallCount */, testKubelet.volumePlugin)
	if err != nil {
		t.Error(err)
	}

	err = volumetest.VerifySetUpCallCount(
		1 /* expectedSetUpCallCount */, testKubelet.volumePlugin)
	if err != nil {
		t.Error(err)
	}
}

func TestVolumeUnmountAndDetachControllerDisabled(t *testing.T) {
	testKubelet := newTestKubelet(t, false /* controllerAttachDetachEnabled */)
	kubelet := testKubelet.kubelet

	pod := podWithUidNameNsSpec("12345678", "foo", "test", api.PodSpec{
		Volumes: []api.Volume{
			{
				Name: "vol1",
				VolumeSource: api.VolumeSource{
					GCEPersistentDisk: &api.GCEPersistentDiskVolumeSource{
						PDName: "fake-device",
					},
				},
			},
		},
	})

	stopCh := runVolumeManager(kubelet)
	defer func() {
		close(stopCh)
	}()

	// Add pod
	kubelet.podManager.SetPods([]*api.Pod{pod})

	// Verify volumes attached
	err := kubelet.volumeManager.WaitForAttachAndMount(pod)
	if err != nil {
		t.Errorf("Expected success: %v", err)
	}

	podVolumes := kubelet.volumeManager.GetMountedVolumesForPod(
		volumehelper.GetUniquePodName(pod))

	expectedPodVolumes := []string{"vol1"}
	if len(expectedPodVolumes) != len(podVolumes) {
		t.Errorf("Unexpected volumes. Expected %#v got %#v.  Manifest was: %#v", expectedPodVolumes, podVolumes, pod)
	}
	for _, name := range expectedPodVolumes {
		if _, ok := podVolumes[name]; !ok {
			t.Errorf("api.Pod volumes map is missing key: %s. %#v", name, podVolumes)
		}
	}
	if testKubelet.volumePlugin.GetNewAttacherCallCount() < 1 {
		t.Errorf("Expected plugin NewAttacher to be called at least once")
	}

	err = volumetest.VerifyWaitForAttachCallCount(
		1 /* expectedWaitForAttachCallCount */, testKubelet.volumePlugin)
	if err != nil {
		t.Error(err)
	}

	err = volumetest.VerifyAttachCallCount(
		1 /* expectedAttachCallCount */, testKubelet.volumePlugin)
	if err != nil {
		t.Error(err)
	}

	err = volumetest.VerifyMountDeviceCallCount(
		1 /* expectedMountDeviceCallCount */, testKubelet.volumePlugin)
	if err != nil {
		t.Error(err)
	}

	err = volumetest.VerifySetUpCallCount(
		1 /* expectedSetUpCallCount */, testKubelet.volumePlugin)
	if err != nil {
		t.Error(err)
	}

	// Remove pod
	kubelet.podManager.SetPods([]*api.Pod{})

	err = waitForVolumeUnmount(kubelet.volumeManager, pod)
	if err != nil {
		t.Error(err)
	}

	// Verify volumes unmounted
	podVolumes = kubelet.volumeManager.GetMountedVolumesForPod(
		volumehelper.GetUniquePodName(pod))

	if len(podVolumes) != 0 {
		t.Errorf("Expected volumes to be unmounted and detached. But some volumes are still mounted: %#v", podVolumes)
	}

	err = volumetest.VerifyTearDownCallCount(
		1 /* expectedTearDownCallCount */, testKubelet.volumePlugin)
	if err != nil {
		t.Error(err)
	}

	// Verify volumes detached and no longer reported as in use
	err = waitForVolumeDetach(api.UniqueVolumeName("fake/vol1"), kubelet.volumeManager)
	if err != nil {
		t.Error(err)
	}

	if testKubelet.volumePlugin.GetNewDetacherCallCount() < 1 {
		t.Errorf("Expected plugin NewDetacher to be called at least once")
	}

	err = volumetest.VerifyDetachCallCount(
		1 /* expectedDetachCallCount */, testKubelet.volumePlugin)
	if err != nil {
		t.Error(err)
	}
}

func TestVolumeAttachAndMountControllerEnabled(t *testing.T) {
	testKubelet := newTestKubelet(t, true /* controllerAttachDetachEnabled */)
	kubelet := testKubelet.kubelet
	kubeClient := testKubelet.fakeKubeClient
	kubeClient.AddReactor("get", "nodes",
		func(action core.Action) (bool, runtime.Object, error) {
			return true, &api.Node{
				ObjectMeta: api.ObjectMeta{Name: testKubeletHostname},
				Status: api.NodeStatus{
					VolumesAttached: []api.AttachedVolume{
						{
							Name:       "fake/vol1",
							DevicePath: "fake/path",
						},
					}},
				Spec: api.NodeSpec{ExternalID: testKubeletHostname},
			}, nil
		})
	kubeClient.AddReactor("*", "*", func(action core.Action) (bool, runtime.Object, error) {
		return true, nil, fmt.Errorf("no reaction implemented for %s", action)
	})

	pod := podWithUidNameNsSpec("12345678", "foo", "test", api.PodSpec{
		Volumes: []api.Volume{
			{
				Name: "vol1",
				VolumeSource: api.VolumeSource{
					GCEPersistentDisk: &api.GCEPersistentDiskVolumeSource{
						PDName: "fake-device",
					},
				},
			},
		},
	})

	stopCh := runVolumeManager(kubelet)
	defer func() {
		close(stopCh)
	}()

	kubelet.podManager.SetPods([]*api.Pod{pod})

	// Fake node status update
	go simulateVolumeInUseUpdate(
		api.UniqueVolumeName("fake/vol1"),
		stopCh,
		kubelet.volumeManager)

	err := kubelet.volumeManager.WaitForAttachAndMount(pod)
	if err != nil {
		t.Errorf("Expected success: %v", err)
	}

	podVolumes := kubelet.volumeManager.GetMountedVolumesForPod(
		volumehelper.GetUniquePodName(pod))

	expectedPodVolumes := []string{"vol1"}
	if len(expectedPodVolumes) != len(podVolumes) {
		t.Errorf("Unexpected volumes. Expected %#v got %#v.  Manifest was: %#v", expectedPodVolumes, podVolumes, pod)
	}
	for _, name := range expectedPodVolumes {
		if _, ok := podVolumes[name]; !ok {
			t.Errorf("api.Pod volumes map is missing key: %s. %#v", name, podVolumes)
		}
	}
	if testKubelet.volumePlugin.GetNewAttacherCallCount() < 1 {
		t.Errorf("Expected plugin NewAttacher to be called at least once")
	}

	err = volumetest.VerifyWaitForAttachCallCount(
		1 /* expectedWaitForAttachCallCount */, testKubelet.volumePlugin)
	if err != nil {
		t.Error(err)
	}

	err = volumetest.VerifyZeroAttachCalls(testKubelet.volumePlugin)
	if err != nil {
		t.Error(err)
	}

	err = volumetest.VerifyMountDeviceCallCount(
		1 /* expectedMountDeviceCallCount */, testKubelet.volumePlugin)
	if err != nil {
		t.Error(err)
	}

	err = volumetest.VerifySetUpCallCount(
		1 /* expectedSetUpCallCount */, testKubelet.volumePlugin)
	if err != nil {
		t.Error(err)
	}
}

func TestVolumeUnmountAndDetachControllerEnabled(t *testing.T) {
	testKubelet := newTestKubelet(t, true /* controllerAttachDetachEnabled */)
	kubelet := testKubelet.kubelet
	kubeClient := testKubelet.fakeKubeClient
	kubeClient.AddReactor("get", "nodes",
		func(action core.Action) (bool, runtime.Object, error) {
			return true, &api.Node{
				ObjectMeta: api.ObjectMeta{Name: testKubeletHostname},
				Status: api.NodeStatus{
					VolumesAttached: []api.AttachedVolume{
						{
							Name:       "fake/vol1",
							DevicePath: "fake/path",
						},
					}},
				Spec: api.NodeSpec{ExternalID: testKubeletHostname},
			}, nil
		})
	kubeClient.AddReactor("*", "*", func(action core.Action) (bool, runtime.Object, error) {
		return true, nil, fmt.Errorf("no reaction implemented for %s", action)
	})

	pod := podWithUidNameNsSpec("12345678", "foo", "test", api.PodSpec{
		Volumes: []api.Volume{
			{
				Name: "vol1",
				VolumeSource: api.VolumeSource{
					GCEPersistentDisk: &api.GCEPersistentDiskVolumeSource{
						PDName: "fake-device",
					},
				},
			},
		},
	})

	stopCh := runVolumeManager(kubelet)
	defer func() {
		close(stopCh)
	}()

	// Add pod
	kubelet.podManager.SetPods([]*api.Pod{pod})

	// Fake node status update
	go simulateVolumeInUseUpdate(
		api.UniqueVolumeName("fake/vol1"),
		stopCh,
		kubelet.volumeManager)

	// Verify volumes attached
	err := kubelet.volumeManager.WaitForAttachAndMount(pod)
	if err != nil {
		t.Errorf("Expected success: %v", err)
	}

	podVolumes := kubelet.volumeManager.GetMountedVolumesForPod(
		volumehelper.GetUniquePodName(pod))

	expectedPodVolumes := []string{"vol1"}
	if len(expectedPodVolumes) != len(podVolumes) {
		t.Errorf("Unexpected volumes. Expected %#v got %#v.  Manifest was: %#v", expectedPodVolumes, podVolumes, pod)
	}
	for _, name := range expectedPodVolumes {
		if _, ok := podVolumes[name]; !ok {
			t.Errorf("api.Pod volumes map is missing key: %s. %#v", name, podVolumes)
		}
	}
	if testKubelet.volumePlugin.GetNewAttacherCallCount() < 1 {
		t.Errorf("Expected plugin NewAttacher to be called at least once")
	}

	err = volumetest.VerifyWaitForAttachCallCount(
		1 /* expectedWaitForAttachCallCount */, testKubelet.volumePlugin)
	if err != nil {
		t.Error(err)
	}

	err = volumetest.VerifyZeroAttachCalls(testKubelet.volumePlugin)
	if err != nil {
		t.Error(err)
	}

	err = volumetest.VerifyMountDeviceCallCount(
		1 /* expectedMountDeviceCallCount */, testKubelet.volumePlugin)
	if err != nil {
		t.Error(err)
	}

	err = volumetest.VerifySetUpCallCount(
		1 /* expectedSetUpCallCount */, testKubelet.volumePlugin)
	if err != nil {
		t.Error(err)
	}

	// Remove pod
	kubelet.podManager.SetPods([]*api.Pod{})

	err = waitForVolumeUnmount(kubelet.volumeManager, pod)
	if err != nil {
		t.Error(err)
	}

	// Verify volumes unmounted
	podVolumes = kubelet.volumeManager.GetMountedVolumesForPod(
		volumehelper.GetUniquePodName(pod))

	if len(podVolumes) != 0 {
		t.Errorf("Expected volumes to be unmounted and detached. But some volumes are still mounted: %#v", podVolumes)
	}

	err = volumetest.VerifyTearDownCallCount(
		1 /* expectedTearDownCallCount */, testKubelet.volumePlugin)
	if err != nil {
		t.Error(err)
	}

	// Verify volumes detached and no longer reported as in use
	err = waitForVolumeDetach(api.UniqueVolumeName("fake/vol1"), kubelet.volumeManager)
	if err != nil {
		t.Error(err)
	}

	if testKubelet.volumePlugin.GetNewDetacherCallCount() < 1 {
		t.Errorf("Expected plugin NewDetacher to be called at least once")
	}

	err = volumetest.VerifyZeroDetachCallCount(testKubelet.volumePlugin)
	if err != nil {
		t.Error(err)
	}
}

// Tests that identify the host port conflicts are detected correctly.
func TestGetHostPortConflicts(t *testing.T) {
	pods := []*api.Pod{
		{Spec: api.PodSpec{Containers: []api.Container{{Ports: []api.ContainerPort{{HostPort: 80}}}}}},
		{Spec: api.PodSpec{Containers: []api.Container{{Ports: []api.ContainerPort{{HostPort: 81}}}}}},
		{Spec: api.PodSpec{Containers: []api.Container{{Ports: []api.ContainerPort{{HostPort: 82}}}}}},
		{Spec: api.PodSpec{Containers: []api.Container{{Ports: []api.ContainerPort{{HostPort: 83}}}}}},
	}
	// Pods should not cause any conflict.
	if hasHostPortConflicts(pods) {
		t.Errorf("expected no conflicts, Got conflicts")
	}

	expected := &api.Pod{
		Spec: api.PodSpec{Containers: []api.Container{{Ports: []api.ContainerPort{{HostPort: 81}}}}},
	}
	// The new pod should cause conflict and be reported.
	pods = append(pods, expected)
	if !hasHostPortConflicts(pods) {
		t.Errorf("expected conflict, Got no conflicts")
	}
}

// Tests that we handle port conflicts correctly by setting the failed status in status map.
func TestHandlePortConflicts(t *testing.T) {
	testKubelet := newTestKubelet(t, false /* controllerAttachDetachEnabled */)
	kl := testKubelet.kubelet
	testKubelet.fakeCadvisor.On("MachineInfo").Return(&cadvisorapi.MachineInfo{}, nil)
	testKubelet.fakeCadvisor.On("ImagesFsInfo").Return(cadvisorapiv2.FsInfo{}, nil)
	testKubelet.fakeCadvisor.On("RootFsInfo").Return(cadvisorapiv2.FsInfo{}, nil)

	kl.nodeLister = testNodeLister{nodes: []api.Node{
		{
			ObjectMeta: api.ObjectMeta{Name: kl.nodeName},
			Status: api.NodeStatus{
				Allocatable: api.ResourceList{
					api.ResourcePods: *resource.NewQuantity(110, resource.DecimalSI),
				},
			},
		},
	}}
	kl.nodeInfo = testNodeInfo{nodes: []api.Node{
		{
			ObjectMeta: api.ObjectMeta{Name: kl.nodeName},
			Status: api.NodeStatus{
				Allocatable: api.ResourceList{
					api.ResourcePods: *resource.NewQuantity(110, resource.DecimalSI),
				},
			},
		},
	}}

	spec := api.PodSpec{NodeName: kl.nodeName, Containers: []api.Container{{Ports: []api.ContainerPort{{HostPort: 80}}}}}
	pods := []*api.Pod{
		podWithUidNameNsSpec("123456789", "newpod", "foo", spec),
		podWithUidNameNsSpec("987654321", "oldpod", "foo", spec),
	}
	// Make sure the Pods are in the reverse order of creation time.
	pods[1].CreationTimestamp = unversioned.NewTime(time.Now())
	pods[0].CreationTimestamp = unversioned.NewTime(time.Now().Add(1 * time.Second))
	// The newer pod should be rejected.
	notfittingPod := pods[0]
	fittingPod := pods[1]

	kl.HandlePodAdditions(pods)
	// Check pod status stored in the status map.
	// notfittingPod should be Failed
	status, found := kl.statusManager.GetPodStatus(notfittingPod.UID)
	if !found {
		t.Fatalf("status of pod %q is not found in the status map", notfittingPod.UID)
	}
	if status.Phase != api.PodFailed {
		t.Fatalf("expected pod status %q. Got %q.", api.PodFailed, status.Phase)
	}
	// fittingPod should be Pending
	status, found = kl.statusManager.GetPodStatus(fittingPod.UID)
	if !found {
		t.Fatalf("status of pod %q is not found in the status map", fittingPod.UID)
	}
	if status.Phase != api.PodPending {
		t.Fatalf("expected pod status %q. Got %q.", api.PodPending, status.Phase)
	}
}

// Tests that we handle host name conflicts correctly by setting the failed status in status map.
func TestHandleHostNameConflicts(t *testing.T) {
	testKubelet := newTestKubelet(t, false /* controllerAttachDetachEnabled */)
	kl := testKubelet.kubelet
	testKubelet.fakeCadvisor.On("MachineInfo").Return(&cadvisorapi.MachineInfo{}, nil)
	testKubelet.fakeCadvisor.On("ImagesFsInfo").Return(cadvisorapiv2.FsInfo{}, nil)
	testKubelet.fakeCadvisor.On("RootFsInfo").Return(cadvisorapiv2.FsInfo{}, nil)

	kl.nodeLister = testNodeLister{nodes: []api.Node{
		{
			ObjectMeta: api.ObjectMeta{Name: "127.0.0.1"},
			Status: api.NodeStatus{
				Allocatable: api.ResourceList{
					api.ResourcePods: *resource.NewQuantity(110, resource.DecimalSI),
				},
			},
		},
	}}
	kl.nodeInfo = testNodeInfo{nodes: []api.Node{
		{
			ObjectMeta: api.ObjectMeta{Name: "127.0.0.1"},
			Status: api.NodeStatus{
				Allocatable: api.ResourceList{
					api.ResourcePods: *resource.NewQuantity(110, resource.DecimalSI),
				},
			},
		},
	}}

	// default NodeName in test is 127.0.0.1
	pods := []*api.Pod{
		podWithUidNameNsSpec("123456789", "notfittingpod", "foo", api.PodSpec{NodeName: "127.0.0.2"}),
		podWithUidNameNsSpec("987654321", "fittingpod", "foo", api.PodSpec{NodeName: "127.0.0.1"}),
	}

	notfittingPod := pods[0]
	fittingPod := pods[1]

	kl.HandlePodAdditions(pods)
	// Check pod status stored in the status map.
	// notfittingPod should be Failed
	status, found := kl.statusManager.GetPodStatus(notfittingPod.UID)
	if !found {
		t.Fatalf("status of pod %q is not found in the status map", notfittingPod.UID)
	}
	if status.Phase != api.PodFailed {
		t.Fatalf("expected pod status %q. Got %q.", api.PodFailed, status.Phase)
	}
	// fittingPod should be Pending
	status, found = kl.statusManager.GetPodStatus(fittingPod.UID)
	if !found {
		t.Fatalf("status of pod %q is not found in the status map", fittingPod.UID)
	}
	if status.Phase != api.PodPending {
		t.Fatalf("expected pod status %q. Got %q.", api.PodPending, status.Phase)
	}
}

// Tests that we handle not matching labels selector correctly by setting the failed status in status map.
func TestHandleNodeSelector(t *testing.T) {
	testKubelet := newTestKubelet(t, false /* controllerAttachDetachEnabled */)
	kl := testKubelet.kubelet
	nodes := []api.Node{
		{
			ObjectMeta: api.ObjectMeta{Name: testKubeletHostname, Labels: map[string]string{"key": "B"}},
			Status: api.NodeStatus{
				Allocatable: api.ResourceList{
					api.ResourcePods: *resource.NewQuantity(110, resource.DecimalSI),
				},
			},
		},
	}
	kl.nodeLister = testNodeLister{nodes: nodes}
	kl.nodeInfo = testNodeInfo{nodes: nodes}
	testKubelet.fakeCadvisor.On("MachineInfo").Return(&cadvisorapi.MachineInfo{}, nil)
	testKubelet.fakeCadvisor.On("ImagesFsInfo").Return(cadvisorapiv2.FsInfo{}, nil)
	testKubelet.fakeCadvisor.On("RootFsInfo").Return(cadvisorapiv2.FsInfo{}, nil)
	pods := []*api.Pod{
		podWithUidNameNsSpec("123456789", "podA", "foo", api.PodSpec{NodeSelector: map[string]string{"key": "A"}}),
		podWithUidNameNsSpec("987654321", "podB", "foo", api.PodSpec{NodeSelector: map[string]string{"key": "B"}}),
	}
	// The first pod should be rejected.
	notfittingPod := pods[0]
	fittingPod := pods[1]

	kl.HandlePodAdditions(pods)
	// Check pod status stored in the status map.
	// notfittingPod should be Failed
	status, found := kl.statusManager.GetPodStatus(notfittingPod.UID)
	if !found {
		t.Fatalf("status of pod %q is not found in the status map", notfittingPod.UID)
	}
	if status.Phase != api.PodFailed {
		t.Fatalf("expected pod status %q. Got %q.", api.PodFailed, status.Phase)
	}
	// fittingPod should be Pending
	status, found = kl.statusManager.GetPodStatus(fittingPod.UID)
	if !found {
		t.Fatalf("status of pod %q is not found in the status map", fittingPod.UID)
	}
	if status.Phase != api.PodPending {
		t.Fatalf("expected pod status %q. Got %q.", api.PodPending, status.Phase)
	}
}

// Tests that we handle exceeded resources correctly by setting the failed status in status map.
func TestHandleMemExceeded(t *testing.T) {
	testKubelet := newTestKubelet(t, false /* controllerAttachDetachEnabled */)
	kl := testKubelet.kubelet
	nodes := []api.Node{
		{ObjectMeta: api.ObjectMeta{Name: testKubeletHostname},
			Status: api.NodeStatus{Capacity: api.ResourceList{}, Allocatable: api.ResourceList{
				api.ResourceCPU:    *resource.NewMilliQuantity(10, resource.DecimalSI),
				api.ResourceMemory: *resource.NewQuantity(100, resource.BinarySI),
				api.ResourcePods:   *resource.NewQuantity(40, resource.DecimalSI),
			}}},
	}
	kl.nodeLister = testNodeLister{nodes: nodes}
	kl.nodeInfo = testNodeInfo{nodes: nodes}
	testKubelet.fakeCadvisor.On("MachineInfo").Return(&cadvisorapi.MachineInfo{}, nil)
	testKubelet.fakeCadvisor.On("ImagesFsInfo").Return(cadvisorapiv2.FsInfo{}, nil)
	testKubelet.fakeCadvisor.On("RootFsInfo").Return(cadvisorapiv2.FsInfo{}, nil)

	spec := api.PodSpec{NodeName: kl.nodeName,
		Containers: []api.Container{{Resources: api.ResourceRequirements{
			Requests: api.ResourceList{
				"memory": resource.MustParse("90"),
			},
		}}}}
	pods := []*api.Pod{
		podWithUidNameNsSpec("123456789", "newpod", "foo", spec),
		podWithUidNameNsSpec("987654321", "oldpod", "foo", spec),
	}
	// Make sure the Pods are in the reverse order of creation time.
	pods[1].CreationTimestamp = unversioned.NewTime(time.Now())
	pods[0].CreationTimestamp = unversioned.NewTime(time.Now().Add(1 * time.Second))
	// The newer pod should be rejected.
	notfittingPod := pods[0]
	fittingPod := pods[1]

	kl.HandlePodAdditions(pods)
	// Check pod status stored in the status map.
	// notfittingPod should be Failed
	status, found := kl.statusManager.GetPodStatus(notfittingPod.UID)
	if !found {
		t.Fatalf("status of pod %q is not found in the status map", notfittingPod.UID)
	}
	if status.Phase != api.PodFailed {
		t.Fatalf("expected pod status %q. Got %q.", api.PodFailed, status.Phase)
	}
	// fittingPod should be Pending
	status, found = kl.statusManager.GetPodStatus(fittingPod.UID)
	if !found {
		t.Fatalf("status of pod %q is not found in the status map", fittingPod.UID)
	}
	if status.Phase != api.PodPending {
		t.Fatalf("expected pod status %q. Got %q.", api.PodPending, status.Phase)
	}
}

// TODO(filipg): This test should be removed once StatusSyncer can do garbage collection without external signal.
func TestPurgingObsoleteStatusMapEntries(t *testing.T) {
	testKubelet := newTestKubelet(t, false /* controllerAttachDetachEnabled */)
	testKubelet.fakeCadvisor.On("MachineInfo").Return(&cadvisorapi.MachineInfo{}, nil)
	testKubelet.fakeCadvisor.On("ImagesFsInfo").Return(cadvisorapiv2.FsInfo{}, nil)
	testKubelet.fakeCadvisor.On("RootFsInfo").Return(cadvisorapiv2.FsInfo{}, nil)
	versionInfo := &cadvisorapi.VersionInfo{
		KernelVersion:      "3.16.0-0.bpo.4-amd64",
		ContainerOsVersion: "Debian GNU/Linux 7 (wheezy)",
		DockerVersion:      "1.5.0",
	}
	testKubelet.fakeCadvisor.On("VersionInfo").Return(versionInfo, nil)

	kl := testKubelet.kubelet
	pods := []*api.Pod{
		{ObjectMeta: api.ObjectMeta{Name: "pod1", UID: "1234"}, Spec: api.PodSpec{Containers: []api.Container{{Ports: []api.ContainerPort{{HostPort: 80}}}}}},
		{ObjectMeta: api.ObjectMeta{Name: "pod2", UID: "4567"}, Spec: api.PodSpec{Containers: []api.Container{{Ports: []api.ContainerPort{{HostPort: 80}}}}}},
	}
	podToTest := pods[1]
	// Run once to populate the status map.
	kl.HandlePodAdditions(pods)
	if _, found := kl.statusManager.GetPodStatus(podToTest.UID); !found {
		t.Fatalf("expected to have status cached for pod2")
	}
	// Sync with empty pods so that the entry in status map will be removed.
	kl.podManager.SetPods([]*api.Pod{})
	kl.HandlePodCleanups()
	if _, found := kl.statusManager.GetPodStatus(podToTest.UID); found {
		t.Fatalf("expected to not have status cached for pod2")
	}
}

// updateDiskSpacePolicy creates a new DiskSpaceManager with a new policy. This new manager along
// with the mock FsInfo values added to Cadvisor should make the kubelet report that it has
// sufficient disk space or it is out of disk, depending on the capacity, availability and
// threshold values.
func updateDiskSpacePolicy(kubelet *Kubelet, mockCadvisor *cadvisortest.Mock, rootCap, dockerCap, rootAvail, dockerAvail uint64, rootThreshold, dockerThreshold int) error {
	dockerimagesFsInfo := cadvisorapiv2.FsInfo{Capacity: rootCap * mb, Available: rootAvail * mb}
	rootFsInfo := cadvisorapiv2.FsInfo{Capacity: dockerCap * mb, Available: dockerAvail * mb}
	mockCadvisor.On("ImagesFsInfo").Return(dockerimagesFsInfo, nil)
	mockCadvisor.On("RootFsInfo").Return(rootFsInfo, nil)

	dsp := DiskSpacePolicy{DockerFreeDiskMB: rootThreshold, RootFreeDiskMB: dockerThreshold}
	diskSpaceManager, err := newDiskSpaceManager(mockCadvisor, dsp)
	if err != nil {
		return err
	}
	kubelet.diskSpaceManager = diskSpaceManager
	return nil
}

func TestCreateMirrorPod(t *testing.T) {
	for _, updateType := range []kubetypes.SyncPodType{kubetypes.SyncPodCreate, kubetypes.SyncPodUpdate} {
		testKubelet := newTestKubelet(t, false /* controllerAttachDetachEnabled */)
		testKubelet.fakeCadvisor.On("Start").Return(nil)
		testKubelet.fakeCadvisor.On("VersionInfo").Return(&cadvisorapi.VersionInfo{}, nil)
		testKubelet.fakeCadvisor.On("MachineInfo").Return(&cadvisorapi.MachineInfo{}, nil)
		testKubelet.fakeCadvisor.On("ImagesFsInfo").Return(cadvisorapiv2.FsInfo{}, nil)
		testKubelet.fakeCadvisor.On("RootFsInfo").Return(cadvisorapiv2.FsInfo{}, nil)

		kl := testKubelet.kubelet
		manager := testKubelet.fakeMirrorClient
		pod := podWithUidNameNs("12345678", "bar", "foo")
		pod.Annotations[kubetypes.ConfigSourceAnnotationKey] = "file"
		pods := []*api.Pod{pod}
		kl.podManager.SetPods(pods)
		err := kl.syncPod(syncPodOptions{
			pod:        pod,
			podStatus:  &kubecontainer.PodStatus{},
			updateType: updateType,
		})
		if err != nil {
			t.Errorf("unexpected error: %v", err)
		}
		podFullName := kubecontainer.GetPodFullName(pod)
		if !manager.HasPod(podFullName) {
			t.Errorf("expected mirror pod %q to be created", podFullName)
		}
		if manager.NumOfPods() != 1 || !manager.HasPod(podFullName) {
			t.Errorf("expected one mirror pod %q, got %v", podFullName, manager.GetPods())
		}
	}
}

func TestDeleteOutdatedMirrorPod(t *testing.T) {
	testKubelet := newTestKubelet(t, false /* controllerAttachDetachEnabled */)
	testKubelet.fakeCadvisor.On("Start").Return(nil)
	testKubelet.fakeCadvisor.On("VersionInfo").Return(&cadvisorapi.VersionInfo{}, nil)
	testKubelet.fakeCadvisor.On("MachineInfo").Return(&cadvisorapi.MachineInfo{}, nil)
	testKubelet.fakeCadvisor.On("ImagesFsInfo").Return(cadvisorapiv2.FsInfo{}, nil)
	testKubelet.fakeCadvisor.On("RootFsInfo").Return(cadvisorapiv2.FsInfo{}, nil)

	kl := testKubelet.kubelet
	manager := testKubelet.fakeMirrorClient
	pod := podWithUidNameNsSpec("12345678", "foo", "ns", api.PodSpec{
		Containers: []api.Container{
			{Name: "1234", Image: "foo"},
		},
	})
	pod.Annotations[kubetypes.ConfigSourceAnnotationKey] = "file"

	// Mirror pod has an outdated spec.
	mirrorPod := podWithUidNameNsSpec("11111111", "foo", "ns", api.PodSpec{
		Containers: []api.Container{
			{Name: "1234", Image: "bar"},
		},
	})
	mirrorPod.Annotations[kubetypes.ConfigSourceAnnotationKey] = "api"
	mirrorPod.Annotations[kubetypes.ConfigMirrorAnnotationKey] = "mirror"

	pods := []*api.Pod{pod, mirrorPod}
	kl.podManager.SetPods(pods)
	err := kl.syncPod(syncPodOptions{
		pod:        pod,
		mirrorPod:  mirrorPod,
		podStatus:  &kubecontainer.PodStatus{},
		updateType: kubetypes.SyncPodUpdate,
	})
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}
	name := kubecontainer.GetPodFullName(pod)
	creates, deletes := manager.GetCounts(name)
	if creates != 1 || deletes != 1 {
		t.Errorf("expected 1 creation and 1 deletion of %q, got %d, %d", name, creates, deletes)
	}
}

func TestDeleteOrphanedMirrorPods(t *testing.T) {
	testKubelet := newTestKubelet(t, false /* controllerAttachDetachEnabled */)
	testKubelet.fakeCadvisor.On("Start").Return(nil)
	testKubelet.fakeCadvisor.On("MachineInfo").Return(&cadvisorapi.MachineInfo{}, nil)
	testKubelet.fakeCadvisor.On("ImagesFsInfo").Return(cadvisorapiv2.FsInfo{}, nil)
	testKubelet.fakeCadvisor.On("RootFsInfo").Return(cadvisorapiv2.FsInfo{}, nil)

	kl := testKubelet.kubelet
	manager := testKubelet.fakeMirrorClient
	orphanPods := []*api.Pod{
		{
			ObjectMeta: api.ObjectMeta{
				UID:       "12345678",
				Name:      "pod1",
				Namespace: "ns",
				Annotations: map[string]string{
					kubetypes.ConfigSourceAnnotationKey: "api",
					kubetypes.ConfigMirrorAnnotationKey: "mirror",
				},
			},
		},
		{
			ObjectMeta: api.ObjectMeta{
				UID:       "12345679",
				Name:      "pod2",
				Namespace: "ns",
				Annotations: map[string]string{
					kubetypes.ConfigSourceAnnotationKey: "api",
					kubetypes.ConfigMirrorAnnotationKey: "mirror",
				},
			},
		},
	}

	kl.podManager.SetPods(orphanPods)
	// Sync with an empty pod list to delete all mirror pods.
	kl.HandlePodCleanups()
	if manager.NumOfPods() != 0 {
		t.Errorf("expected zero mirror pods, got %v", manager.GetPods())
	}
	for _, pod := range orphanPods {
		name := kubecontainer.GetPodFullName(pod)
		creates, deletes := manager.GetCounts(name)
		if creates != 0 || deletes != 1 {
			t.Errorf("expected 0 creation and one deletion of %q, got %d, %d", name, creates, deletes)
		}
	}
}

func TestGetContainerInfoForMirrorPods(t *testing.T) {
	// pods contain one static and one mirror pod with the same name but
	// different UIDs.
	pods := []*api.Pod{
		{
			ObjectMeta: api.ObjectMeta{
				UID:       "1234",
				Name:      "qux",
				Namespace: "ns",
				Annotations: map[string]string{
					kubetypes.ConfigSourceAnnotationKey: "file",
				},
			},
			Spec: api.PodSpec{
				Containers: []api.Container{
					{Name: "foo"},
				},
			},
		},
		{
			ObjectMeta: api.ObjectMeta{
				UID:       "5678",
				Name:      "qux",
				Namespace: "ns",
				Annotations: map[string]string{
					kubetypes.ConfigSourceAnnotationKey: "api",
					kubetypes.ConfigMirrorAnnotationKey: "mirror",
				},
			},
			Spec: api.PodSpec{
				Containers: []api.Container{
					{Name: "foo"},
				},
			},
		},
	}

	containerID := "ab2cdf"
	containerPath := fmt.Sprintf("/docker/%v", containerID)
	containerInfo := cadvisorapi.ContainerInfo{
		ContainerReference: cadvisorapi.ContainerReference{
			Name: containerPath,
		},
	}

	testKubelet := newTestKubelet(t, false /* controllerAttachDetachEnabled */)
	fakeRuntime := testKubelet.fakeRuntime
	mockCadvisor := testKubelet.fakeCadvisor
	cadvisorReq := &cadvisorapi.ContainerInfoRequest{}
	mockCadvisor.On("DockerContainer", containerID, cadvisorReq).Return(containerInfo, nil)
	kubelet := testKubelet.kubelet

	fakeRuntime.PodList = []*containertest.FakePod{
		{Pod: &kubecontainer.Pod{
			ID:        "1234",
			Name:      "qux",
			Namespace: "ns",
			Containers: []*kubecontainer.Container{
				{
					Name: "foo",
					ID:   kubecontainer.ContainerID{Type: "test", ID: containerID},
				},
			},
		}},
	}

	kubelet.podManager.SetPods(pods)
	// Use the mirror pod UID to retrieve the stats.
	stats, err := kubelet.GetContainerInfo("qux_ns", "5678", "foo", cadvisorReq)
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}
	if stats == nil {
		t.Fatalf("stats should not be nil")
	}
	mockCadvisor.AssertExpectations(t)
}

func TestHostNetworkAllowed(t *testing.T) {
	testKubelet := newTestKubelet(t, false /* controllerAttachDetachEnabled */)
	testKubelet.fakeCadvisor.On("Start").Return(nil)
	testKubelet.fakeCadvisor.On("VersionInfo").Return(&cadvisorapi.VersionInfo{}, nil)
	testKubelet.fakeCadvisor.On("MachineInfo").Return(&cadvisorapi.MachineInfo{}, nil)
	testKubelet.fakeCadvisor.On("ImagesFsInfo").Return(cadvisorapiv2.FsInfo{}, nil)
	testKubelet.fakeCadvisor.On("RootFsInfo").Return(cadvisorapiv2.FsInfo{}, nil)

	kubelet := testKubelet.kubelet

	capabilities.SetForTests(capabilities.Capabilities{
		PrivilegedSources: capabilities.PrivilegedSources{
			HostNetworkSources: []string{kubetypes.ApiserverSource, kubetypes.FileSource},
		},
	})
	pod := podWithUidNameNsSpec("12345678", "foo", "new", api.PodSpec{
		Containers: []api.Container{
			{Name: "foo"},
		},
		SecurityContext: &api.PodSecurityContext{
			HostNetwork: true,
		},
	})
	pod.Annotations[kubetypes.ConfigSourceAnnotationKey] = kubetypes.FileSource

	kubelet.podManager.SetPods([]*api.Pod{pod})
	err := kubelet.syncPod(syncPodOptions{
		pod:        pod,
		podStatus:  &kubecontainer.PodStatus{},
		updateType: kubetypes.SyncPodUpdate,
	})
	if err != nil {
		t.Errorf("expected pod infra creation to succeed: %v", err)
	}
}

func TestHostNetworkDisallowed(t *testing.T) {
	testKubelet := newTestKubelet(t, false /* controllerAttachDetachEnabled */)
	testKubelet.fakeCadvisor.On("Start").Return(nil)
	testKubelet.fakeCadvisor.On("VersionInfo").Return(&cadvisorapi.VersionInfo{}, nil)
	testKubelet.fakeCadvisor.On("MachineInfo").Return(&cadvisorapi.MachineInfo{}, nil)
	testKubelet.fakeCadvisor.On("ImagesFsInfo").Return(cadvisorapiv2.FsInfo{}, nil)
	testKubelet.fakeCadvisor.On("RootFsInfo").Return(cadvisorapiv2.FsInfo{}, nil)

	kubelet := testKubelet.kubelet

	capabilities.SetForTests(capabilities.Capabilities{
		PrivilegedSources: capabilities.PrivilegedSources{
			HostNetworkSources: []string{},
		},
	})
	pod := podWithUidNameNsSpec("12345678", "foo", "new", api.PodSpec{
		Containers: []api.Container{
			{Name: "foo"},
		},
		SecurityContext: &api.PodSecurityContext{
			HostNetwork: true,
		},
	})
	pod.Annotations[kubetypes.ConfigSourceAnnotationKey] = kubetypes.FileSource

	err := kubelet.syncPod(syncPodOptions{
		pod:        pod,
		podStatus:  &kubecontainer.PodStatus{},
		updateType: kubetypes.SyncPodUpdate,
	})
	if err == nil {
		t.Errorf("expected pod infra creation to fail")
	}
}

func TestPrivilegeContainerAllowed(t *testing.T) {
	testKubelet := newTestKubelet(t, false /* controllerAttachDetachEnabled */)
	testKubelet.fakeCadvisor.On("Start").Return(nil)
	testKubelet.fakeCadvisor.On("VersionInfo").Return(&cadvisorapi.VersionInfo{}, nil)
	testKubelet.fakeCadvisor.On("MachineInfo").Return(&cadvisorapi.MachineInfo{}, nil)
	testKubelet.fakeCadvisor.On("ImagesFsInfo").Return(cadvisorapiv2.FsInfo{}, nil)
	testKubelet.fakeCadvisor.On("RootFsInfo").Return(cadvisorapiv2.FsInfo{}, nil)

	kubelet := testKubelet.kubelet

	capabilities.SetForTests(capabilities.Capabilities{
		AllowPrivileged: true,
	})
	privileged := true
	pod := podWithUidNameNsSpec("12345678", "foo", "new", api.PodSpec{
		Containers: []api.Container{
			{Name: "foo", SecurityContext: &api.SecurityContext{Privileged: &privileged}},
		},
	})

	kubelet.podManager.SetPods([]*api.Pod{pod})
	err := kubelet.syncPod(syncPodOptions{
		pod:        pod,
		podStatus:  &kubecontainer.PodStatus{},
		updateType: kubetypes.SyncPodUpdate,
	})
	if err != nil {
		t.Errorf("expected pod infra creation to succeed: %v", err)
	}
}

func TestPrivilegedContainerDisallowed(t *testing.T) {
	testKubelet := newTestKubelet(t, false /* controllerAttachDetachEnabled */)
	testKubelet.fakeCadvisor.On("VersionInfo").Return(&cadvisorapi.VersionInfo{}, nil)
	testKubelet.fakeCadvisor.On("MachineInfo").Return(&cadvisorapi.MachineInfo{}, nil)
	testKubelet.fakeCadvisor.On("ImagesFsInfo").Return(cadvisorapiv2.FsInfo{}, nil)
	testKubelet.fakeCadvisor.On("RootFsInfo").Return(cadvisorapiv2.FsInfo{}, nil)
	kubelet := testKubelet.kubelet

	capabilities.SetForTests(capabilities.Capabilities{
		AllowPrivileged: false,
	})
	privileged := true
	pod := podWithUidNameNsSpec("12345678", "foo", "new", api.PodSpec{
		Containers: []api.Container{
			{Name: "foo", SecurityContext: &api.SecurityContext{Privileged: &privileged}},
		},
	})

	err := kubelet.syncPod(syncPodOptions{
		pod:        pod,
		podStatus:  &kubecontainer.PodStatus{},
		updateType: kubetypes.SyncPodUpdate,
	})
	if err == nil {
		t.Errorf("expected pod infra creation to fail")
	}
}

func TestFilterOutTerminatedPods(t *testing.T) {
	testKubelet := newTestKubelet(t, false /* controllerAttachDetachEnabled */)
	kubelet := testKubelet.kubelet
	pods := newTestPods(5)
	pods[0].Status.Phase = api.PodFailed
	pods[1].Status.Phase = api.PodSucceeded
	pods[2].Status.Phase = api.PodRunning
	pods[3].Status.Phase = api.PodPending

	expected := []*api.Pod{pods[2], pods[3], pods[4]}
	kubelet.podManager.SetPods(pods)
	actual := kubelet.filterOutTerminatedPods(pods)
	if !reflect.DeepEqual(expected, actual) {
		t.Errorf("expected %#v, got %#v", expected, actual)
	}
}

func TestSyncPodsSetStatusToFailedForPodsThatRunTooLong(t *testing.T) {
	testKubelet := newTestKubelet(t, false /* controllerAttachDetachEnabled */)
	fakeRuntime := testKubelet.fakeRuntime
	testKubelet.fakeCadvisor.On("MachineInfo").Return(&cadvisorapi.MachineInfo{}, nil)
	kubelet := testKubelet.kubelet

	now := unversioned.Now()
	startTime := unversioned.NewTime(now.Time.Add(-1 * time.Minute))
	exceededActiveDeadlineSeconds := int64(30)

	pods := []*api.Pod{
		{
			ObjectMeta: api.ObjectMeta{
				UID:       "12345678",
				Name:      "bar",
				Namespace: "new",
			},
			Spec: api.PodSpec{
				Containers: []api.Container{
					{Name: "foo"},
				},
				ActiveDeadlineSeconds: &exceededActiveDeadlineSeconds,
			},
			Status: api.PodStatus{
				StartTime: &startTime,
			},
		},
	}

	fakeRuntime.PodList = []*containertest.FakePod{
		{Pod: &kubecontainer.Pod{
			ID:        "12345678",
			Name:      "bar",
			Namespace: "new",
			Containers: []*kubecontainer.Container{
				{Name: "foo"},
			},
		}},
	}

	// Let the pod worker sets the status to fail after this sync.
	kubelet.HandlePodUpdates(pods)
	status, found := kubelet.statusManager.GetPodStatus(pods[0].UID)
	if !found {
		t.Errorf("expected to found status for pod %q", pods[0].UID)
	}
	if status.Phase != api.PodFailed {
		t.Fatalf("expected pod status %q, got %q.", api.PodFailed, status.Phase)
	}
}

func TestSyncPodsDoesNotSetPodsThatDidNotRunTooLongToFailed(t *testing.T) {
	testKubelet := newTestKubelet(t, false /* controllerAttachDetachEnabled */)
	fakeRuntime := testKubelet.fakeRuntime
	testKubelet.fakeCadvisor.On("Start").Return(nil)
	testKubelet.fakeCadvisor.On("VersionInfo").Return(&cadvisorapi.VersionInfo{}, nil)
	testKubelet.fakeCadvisor.On("MachineInfo").Return(&cadvisorapi.MachineInfo{}, nil)
	testKubelet.fakeCadvisor.On("ImagesFsInfo").Return(cadvisorapiv2.FsInfo{}, nil)
	testKubelet.fakeCadvisor.On("RootFsInfo").Return(cadvisorapiv2.FsInfo{}, nil)

	kubelet := testKubelet.kubelet

	now := unversioned.Now()
	startTime := unversioned.NewTime(now.Time.Add(-1 * time.Minute))
	exceededActiveDeadlineSeconds := int64(300)

	pods := []*api.Pod{
		{
			ObjectMeta: api.ObjectMeta{
				UID:       "12345678",
				Name:      "bar",
				Namespace: "new",
			},
			Spec: api.PodSpec{
				Containers: []api.Container{
					{Name: "foo"},
				},
				ActiveDeadlineSeconds: &exceededActiveDeadlineSeconds,
			},
			Status: api.PodStatus{
				StartTime: &startTime,
			},
		},
	}

	fakeRuntime.PodList = []*containertest.FakePod{
		{Pod: &kubecontainer.Pod{
			ID:        "12345678",
			Name:      "bar",
			Namespace: "new",
			Containers: []*kubecontainer.Container{
				{Name: "foo"},
			},
		}},
	}

	kubelet.podManager.SetPods(pods)
	kubelet.HandlePodUpdates(pods)
	status, found := kubelet.statusManager.GetPodStatus(pods[0].UID)
	if !found {
		t.Errorf("expected to found status for pod %q", pods[0].UID)
	}
	if status.Phase == api.PodFailed {
		t.Fatalf("expected pod status to not be %q", status.Phase)
	}
}

func podWithUidNameNs(uid types.UID, name, namespace string) *api.Pod {
	return &api.Pod{
		ObjectMeta: api.ObjectMeta{
			UID:         uid,
			Name:        name,
			Namespace:   namespace,
			Annotations: map[string]string{},
		},
	}
}

func podWithUidNameNsSpec(uid types.UID, name, namespace string, spec api.PodSpec) *api.Pod {
	pod := podWithUidNameNs(uid, name, namespace)
	pod.Spec = spec
	return pod
}

func TestDeletePodDirsForDeletedPods(t *testing.T) {
	testKubelet := newTestKubelet(t, false /* controllerAttachDetachEnabled */)
	testKubelet.fakeCadvisor.On("Start").Return(nil)
	testKubelet.fakeCadvisor.On("VersionInfo").Return(&cadvisorapi.VersionInfo{}, nil)
	testKubelet.fakeCadvisor.On("MachineInfo").Return(&cadvisorapi.MachineInfo{}, nil)
	testKubelet.fakeCadvisor.On("ImagesFsInfo").Return(cadvisorapiv2.FsInfo{}, nil)
	testKubelet.fakeCadvisor.On("RootFsInfo").Return(cadvisorapiv2.FsInfo{}, nil)
	kl := testKubelet.kubelet
	pods := []*api.Pod{
		podWithUidNameNs("12345678", "pod1", "ns"),
		podWithUidNameNs("12345679", "pod2", "ns"),
	}

	kl.podManager.SetPods(pods)
	// Sync to create pod directories.
	kl.HandlePodSyncs(kl.podManager.GetPods())
	for i := range pods {
		if !dirExists(kl.getPodDir(pods[i].UID)) {
			t.Errorf("expected directory to exist for pod %d", i)
		}
	}

	// Pod 1 has been deleted and no longer exists.
	kl.podManager.SetPods([]*api.Pod{pods[0]})
	kl.HandlePodCleanups()
	if !dirExists(kl.getPodDir(pods[0].UID)) {
		t.Errorf("expected directory to exist for pod 0")
	}
	if dirExists(kl.getPodDir(pods[1].UID)) {
		t.Errorf("expected directory to be deleted for pod 1")
	}
}

func syncAndVerifyPodDir(t *testing.T, testKubelet *TestKubelet, pods []*api.Pod, podsToCheck []*api.Pod, shouldExist bool) {
	kl := testKubelet.kubelet

	kl.podManager.SetPods(pods)
	kl.HandlePodSyncs(pods)
	kl.HandlePodCleanups()
	for i, pod := range podsToCheck {
		exist := dirExists(kl.getPodDir(pod.UID))
		if shouldExist && !exist {
			t.Errorf("expected directory to exist for pod %d", i)
		} else if !shouldExist && exist {
			t.Errorf("expected directory to be removed for pod %d", i)
		}
	}
}

func TestDoesNotDeletePodDirsForTerminatedPods(t *testing.T) {
	testKubelet := newTestKubelet(t, false /* controllerAttachDetachEnabled */)
	testKubelet.fakeCadvisor.On("Start").Return(nil)
	testKubelet.fakeCadvisor.On("VersionInfo").Return(&cadvisorapi.VersionInfo{}, nil)
	testKubelet.fakeCadvisor.On("MachineInfo").Return(&cadvisorapi.MachineInfo{}, nil)
	testKubelet.fakeCadvisor.On("ImagesFsInfo").Return(cadvisorapiv2.FsInfo{}, nil)
	testKubelet.fakeCadvisor.On("RootFsInfo").Return(cadvisorapiv2.FsInfo{}, nil)
	kl := testKubelet.kubelet
	pods := []*api.Pod{
		podWithUidNameNs("12345678", "pod1", "ns"),
		podWithUidNameNs("12345679", "pod2", "ns"),
		podWithUidNameNs("12345680", "pod3", "ns"),
	}

	syncAndVerifyPodDir(t, testKubelet, pods, pods, true)
	// Pod 1 failed, and pod 2 succeeded. None of the pod directories should be
	// deleted.
	kl.statusManager.SetPodStatus(pods[1], api.PodStatus{Phase: api.PodFailed})
	kl.statusManager.SetPodStatus(pods[2], api.PodStatus{Phase: api.PodSucceeded})
	syncAndVerifyPodDir(t, testKubelet, pods, pods, true)
}

func TestDoesNotDeletePodDirsIfContainerIsRunning(t *testing.T) {
	testKubelet := newTestKubelet(t, false /* controllerAttachDetachEnabled */)
	testKubelet.fakeCadvisor.On("Start").Return(nil)
	testKubelet.fakeCadvisor.On("VersionInfo").Return(&cadvisorapi.VersionInfo{}, nil)
	testKubelet.fakeCadvisor.On("MachineInfo").Return(&cadvisorapi.MachineInfo{}, nil)
	testKubelet.fakeCadvisor.On("ImagesFsInfo").Return(cadvisorapiv2.FsInfo{}, nil)
	testKubelet.fakeCadvisor.On("RootFsInfo").Return(cadvisorapiv2.FsInfo{}, nil)
	runningPod := &kubecontainer.Pod{
		ID:        "12345678",
		Name:      "pod1",
		Namespace: "ns",
	}
	apiPod := podWithUidNameNs(runningPod.ID, runningPod.Name, runningPod.Namespace)

	// Sync once to create pod directory; confirm that the pod directory has
	// already been created.
	pods := []*api.Pod{apiPod}
	syncAndVerifyPodDir(t, testKubelet, pods, []*api.Pod{apiPod}, true)

	// Pretend the pod is deleted from apiserver, but is still active on the node.
	// The pod directory should not be removed.
	pods = []*api.Pod{}
	testKubelet.fakeRuntime.PodList = []*containertest.FakePod{{runningPod, ""}}
	syncAndVerifyPodDir(t, testKubelet, pods, []*api.Pod{apiPod}, true)

	// The pod is deleted and also not active on the node. The pod directory
	// should be removed.
	pods = []*api.Pod{}
	testKubelet.fakeRuntime.PodList = []*containertest.FakePod{}
	syncAndVerifyPodDir(t, testKubelet, pods, []*api.Pod{apiPod}, false)
}

func TestGetPodsToSync(t *testing.T) {
	testKubelet := newTestKubelet(t, false /* controllerAttachDetachEnabled */)
	kubelet := testKubelet.kubelet
	clock := testKubelet.fakeClock
	pods := newTestPods(5)

	exceededActiveDeadlineSeconds := int64(30)
	notYetActiveDeadlineSeconds := int64(120)
	startTime := unversioned.NewTime(clock.Now())
	pods[0].Status.StartTime = &startTime
	pods[0].Spec.ActiveDeadlineSeconds = &exceededActiveDeadlineSeconds
	pods[1].Status.StartTime = &startTime
	pods[1].Spec.ActiveDeadlineSeconds = &notYetActiveDeadlineSeconds
	pods[2].Status.StartTime = &startTime
	pods[2].Spec.ActiveDeadlineSeconds = &exceededActiveDeadlineSeconds

	kubelet.podManager.SetPods(pods)
	kubelet.workQueue.Enqueue(pods[2].UID, 0)
	kubelet.workQueue.Enqueue(pods[3].UID, 30*time.Second)
	kubelet.workQueue.Enqueue(pods[4].UID, 2*time.Minute)

	clock.Step(1 * time.Minute)

	expectedPods := []*api.Pod{pods[0], pods[2], pods[3]}

	podsToSync := kubelet.getPodsToSync()

	if len(podsToSync) == len(expectedPods) {
		for _, expect := range expectedPods {
			var found bool
			for _, got := range podsToSync {
				if expect.UID == got.UID {
					found = true
					break
				}
			}
			if !found {
				t.Errorf("expected pod not found: %#v", expect)
			}
		}
	} else {
		t.Errorf("expected %d pods to sync, got %d", len(expectedPods), len(podsToSync))
	}
}

// testPodAdmitHandler is a lifecycle.PodAdmitHandler for testing.
type testPodAdmitHandler struct {
	// list of pods to reject.
	podsToReject []*api.Pod
}

// Admit rejects all pods in the podsToReject list with a matching UID.
func (a *testPodAdmitHandler) Admit(attrs *lifecycle.PodAdmitAttributes) lifecycle.PodAdmitResult {
	for _, podToReject := range a.podsToReject {
		if podToReject.UID == attrs.Pod.UID {
			return lifecycle.PodAdmitResult{Admit: false, Reason: "Rejected", Message: "Pod is rejected"}
		}
	}
	return lifecycle.PodAdmitResult{Admit: true}
}

// Test verifies that the kubelet invokes an admission handler during HandlePodAdditions.
func TestHandlePodAdditionsInvokesPodAdmitHandlers(t *testing.T) {
	testKubelet := newTestKubelet(t, false /* controllerAttachDetachEnabled */)
	kl := testKubelet.kubelet
	kl.nodeLister = testNodeLister{nodes: []api.Node{
		{
			ObjectMeta: api.ObjectMeta{Name: kl.nodeName},
			Status: api.NodeStatus{
				Allocatable: api.ResourceList{
					api.ResourcePods: *resource.NewQuantity(110, resource.DecimalSI),
				},
			},
		},
	}}
	kl.nodeInfo = testNodeInfo{nodes: []api.Node{
		{
			ObjectMeta: api.ObjectMeta{Name: kl.nodeName},
			Status: api.NodeStatus{
				Allocatable: api.ResourceList{
					api.ResourcePods: *resource.NewQuantity(110, resource.DecimalSI),
				},
			},
		},
	}}
	testKubelet.fakeCadvisor.On("MachineInfo").Return(&cadvisorapi.MachineInfo{}, nil)
	testKubelet.fakeCadvisor.On("ImagesFsInfo").Return(cadvisorapiv2.FsInfo{}, nil)
	testKubelet.fakeCadvisor.On("RootFsInfo").Return(cadvisorapiv2.FsInfo{}, nil)

	pods := []*api.Pod{
		{
			ObjectMeta: api.ObjectMeta{
				UID:       "123456789",
				Name:      "podA",
				Namespace: "foo",
			},
		},
		{
			ObjectMeta: api.ObjectMeta{
				UID:       "987654321",
				Name:      "podB",
				Namespace: "foo",
			},
		},
	}
	podToReject := pods[0]
	podToAdmit := pods[1]
	podsToReject := []*api.Pod{podToReject}

	kl.AddPodAdmitHandler(&testPodAdmitHandler{podsToReject: podsToReject})

	kl.HandlePodAdditions(pods)
	// Check pod status stored in the status map.
	// podToReject should be Failed
	status, found := kl.statusManager.GetPodStatus(podToReject.UID)
	if !found {
		t.Fatalf("status of pod %q is not found in the status map", podToReject.UID)
	}
	if status.Phase != api.PodFailed {
		t.Fatalf("expected pod status %q. Got %q.", api.PodFailed, status.Phase)
	}
	// podToAdmit should be Pending
	status, found = kl.statusManager.GetPodStatus(podToAdmit.UID)
	if !found {
		t.Fatalf("status of pod %q is not found in the status map", podToAdmit.UID)
	}
	if status.Phase != api.PodPending {
		t.Fatalf("expected pod status %q. Got %q.", api.PodPending, status.Phase)
	}
}

// testPodSyncLoopHandler is a lifecycle.PodSyncLoopHandler that is used for testing.
type testPodSyncLoopHandler struct {
	// list of pods to sync
	podsToSync []*api.Pod
}

// ShouldSync evaluates if the pod should be synced from the kubelet.
func (a *testPodSyncLoopHandler) ShouldSync(pod *api.Pod) bool {
	for _, podToSync := range a.podsToSync {
		if podToSync.UID == pod.UID {
			return true
		}
	}
	return false
}

// TestGetPodsToSyncInvokesPodSyncLoopHandlers ensures that the get pods to sync routine invokes the handler.
func TestGetPodsToSyncInvokesPodSyncLoopHandlers(t *testing.T) {
	testKubelet := newTestKubelet(t, false /* controllerAttachDetachEnabled */)
	kubelet := testKubelet.kubelet
	pods := newTestPods(5)
	podUIDs := []types.UID{}
	for _, pod := range pods {
		podUIDs = append(podUIDs, pod.UID)
	}
	podsToSync := []*api.Pod{pods[0]}
	kubelet.AddPodSyncLoopHandler(&testPodSyncLoopHandler{podsToSync})

	kubelet.podManager.SetPods(pods)

	expectedPodsUID := []types.UID{pods[0].UID}

	podsToSync = kubelet.getPodsToSync()

	if len(podsToSync) == len(expectedPodsUID) {
		var rightNum int
		for _, podUID := range expectedPodsUID {
			for _, podToSync := range podsToSync {
				if podToSync.UID == podUID {
					rightNum++
					break
				}
			}
		}
		if rightNum != len(expectedPodsUID) {
			// Just for report error
			podsToSyncUID := []types.UID{}
			for _, podToSync := range podsToSync {
				podsToSyncUID = append(podsToSyncUID, podToSync.UID)
			}
			t.Errorf("expected pods %v to sync, got %v", expectedPodsUID, podsToSyncUID)
		}

	} else {
		t.Errorf("expected %d pods to sync, got %d", 3, len(podsToSync))
	}
}

// testPodSyncHandler is a lifecycle.PodSyncHandler that is used for testing.
type testPodSyncHandler struct {
	// list of pods to evict.
	podsToEvict []*api.Pod
	// the reason for the eviction
	reason string
	// the message for the eviction
	message string
}

// ShouldEvict evaluates if the pod should be evicted from the kubelet.
func (a *testPodSyncHandler) ShouldEvict(pod *api.Pod) lifecycle.ShouldEvictResponse {
	for _, podToEvict := range a.podsToEvict {
		if podToEvict.UID == pod.UID {
			return lifecycle.ShouldEvictResponse{Evict: true, Reason: a.reason, Message: a.message}
		}
	}
	return lifecycle.ShouldEvictResponse{Evict: false}
}

func TestSyncPodKillPod(t *testing.T) {
	testKubelet := newTestKubelet(t, false /* controllerAttachDetachEnabled */)
	kl := testKubelet.kubelet
	pod := &api.Pod{
		ObjectMeta: api.ObjectMeta{
			UID:       "12345678",
			Name:      "bar",
			Namespace: "foo",
		},
	}
	pods := []*api.Pod{pod}
	kl.podManager.SetPods(pods)
	gracePeriodOverride := int64(0)
	err := kl.syncPod(syncPodOptions{
		pod:        pod,
		podStatus:  &kubecontainer.PodStatus{},
		updateType: kubetypes.SyncPodKill,
		killPodOptions: &KillPodOptions{
			PodStatusFunc: func(p *api.Pod, podStatus *kubecontainer.PodStatus) api.PodStatus {
				return api.PodStatus{
					Phase:   api.PodFailed,
					Reason:  "reason",
					Message: "message",
				}
			},
			PodTerminationGracePeriodSecondsOverride: &gracePeriodOverride,
		},
	})
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}
	// Check pod status stored in the status map.
	status, found := kl.statusManager.GetPodStatus(pod.UID)
	if !found {
		t.Fatalf("status of pod %q is not found in the status map", pod.UID)
	}
	if status.Phase != api.PodFailed {
		t.Fatalf("expected pod status %q. Got %q.", api.PodFailed, status.Phase)
	}
}

func waitForVolumeUnmount(
	volumeManager kubeletvolume.VolumeManager,
	pod *api.Pod) error {
	var podVolumes kubecontainer.VolumeMap
	err := retryWithExponentialBackOff(
		time.Duration(50*time.Millisecond),
		func() (bool, error) {
			// Verify volumes detached
			podVolumes = volumeManager.GetMountedVolumesForPod(
				volumehelper.GetUniquePodName(pod))

			if len(podVolumes) != 0 {
				return false, nil
			}

			return true, nil
		},
	)

	if err != nil {
		return fmt.Errorf(
			"Expected volumes to be unmounted. But some volumes are still mounted: %#v", podVolumes)
	}

	return nil
}

func waitForVolumeDetach(
	volumeName api.UniqueVolumeName,
	volumeManager kubeletvolume.VolumeManager) error {
	attachedVolumes := []api.UniqueVolumeName{}
	err := retryWithExponentialBackOff(
		time.Duration(50*time.Millisecond),
		func() (bool, error) {
			// Verify volumes detached
			volumeAttached := volumeManager.VolumeIsAttached(volumeName)
			return !volumeAttached, nil
		},
	)

	if err != nil {
		return fmt.Errorf(
			"Expected volumes to be detached. But some volumes are still attached: %#v", attachedVolumes)
	}

	return nil
}

func retryWithExponentialBackOff(initialDuration time.Duration, fn wait.ConditionFunc) error {
	backoff := wait.Backoff{
		Duration: initialDuration,
		Factor:   3,
		Jitter:   0,
		Steps:    6,
	}
	return wait.ExponentialBackoff(backoff, fn)
}

func simulateVolumeInUseUpdate(
	volumeName api.UniqueVolumeName,
	stopCh <-chan struct{},
	volumeManager kubeletvolume.VolumeManager) {
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			volumeManager.MarkVolumesAsReportedInUse(
				[]api.UniqueVolumeName{volumeName})
		case <-stopCh:
			return
		}
	}
}

func runVolumeManager(kubelet *Kubelet) chan struct{} {
	stopCh := make(chan struct{})
	go kubelet.volumeManager.Run(kubelet.sourcesReady, stopCh)
	return stopCh
}
