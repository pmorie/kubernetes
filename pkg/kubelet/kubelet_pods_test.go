/*
Copyright 2016 The Kubernetes Authors.

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
	"io"
	"net"
	"reflect"
	"sort"
	"testing"

	"k8s.io/kubernetes/pkg/api"
	"k8s.io/kubernetes/pkg/api/testapi"
	"k8s.io/kubernetes/pkg/util/diff"
	"k8s.io/kubernetes/pkg/util/term"
	"k8s.io/kubernetes/pkg/volume"
)

type stubVolume struct {
	path string
	volume.MetricsNil
}

func (f *stubVolume) GetPath() string {
	return f.path
}

func (f *stubVolume) GetAttributes() volume.Attributes {
	return volume.Attributes{}
}

func (f *stubVolume) SetUp(fsGroup *int64) error {
	return nil
}

func (f *stubVolume) SetUpAt(dir string, fsGroup *int64) error {
	return nil
}

func TestMakeVolumeMounts(t *testing.T) {
	container := api.Container{
		VolumeMounts: []api.VolumeMount{
			{
				MountPath: "/etc/hosts",
				Name:      "disk",
				ReadOnly:  false,
			},
			{
				MountPath: "/mnt/path3",
				Name:      "disk",
				ReadOnly:  true,
			},
			{
				MountPath: "/mnt/path4",
				Name:      "disk4",
				ReadOnly:  false,
			},
			{
				MountPath: "/mnt/path5",
				Name:      "disk5",
				ReadOnly:  false,
			},
		},
	}

	podVolumes := kubecontainer.VolumeMap{
		"disk":  kubecontainer.VolumeInfo{Mounter: &stubVolume{path: "/mnt/disk"}},
		"disk4": kubecontainer.VolumeInfo{Mounter: &stubVolume{path: "/mnt/host"}},
		"disk5": kubecontainer.VolumeInfo{Mounter: &stubVolume{path: "/var/lib/kubelet/podID/volumes/empty/disk5"}},
	}

	pod := api.Pod{
		Spec: api.PodSpec{
			SecurityContext: &api.PodSecurityContext{
				HostNetwork: true,
			},
		},
	}

	mounts, _ := makeMounts(&pod, "/pod", &container, "fakepodname", "", "", podVolumes)

	expectedMounts := []kubecontainer.Mount{
		{
			Name:           "disk",
			ContainerPath:  "/etc/hosts",
			HostPath:       "/mnt/disk",
			ReadOnly:       false,
			SELinuxRelabel: false,
		},
		{
			Name:           "disk",
			ContainerPath:  "/mnt/path3",
			HostPath:       "/mnt/disk",
			ReadOnly:       true,
			SELinuxRelabel: false,
		},
		{
			Name:           "disk4",
			ContainerPath:  "/mnt/path4",
			HostPath:       "/mnt/host",
			ReadOnly:       false,
			SELinuxRelabel: false,
		},
		{
			Name:           "disk5",
			ContainerPath:  "/mnt/path5",
			HostPath:       "/var/lib/kubelet/podID/volumes/empty/disk5",
			ReadOnly:       false,
			SELinuxRelabel: false,
		},
	}
	if !reflect.DeepEqual(mounts, expectedMounts) {
		t.Errorf("Unexpected mounts: Expected %#v got %#v.  Container was: %#v", expectedMounts, mounts, container)
	}
}

type fakeContainerCommandRunner struct {
	Cmd    []string
	ID     kubecontainer.ContainerID
	PodID  types.UID
	E      error
	Stdin  io.Reader
	Stdout io.WriteCloser
	Stderr io.WriteCloser
	TTY    bool
	Port   uint16
	Stream io.ReadWriteCloser
}

func (f *fakeContainerCommandRunner) ExecInContainer(id kubecontainer.ContainerID, cmd []string, in io.Reader, out, err io.WriteCloser, tty bool, resize <-chan term.Size) error {
	f.Cmd = cmd
	f.ID = id
	f.Stdin = in
	f.Stdout = out
	f.Stderr = err
	f.TTY = tty
	return f.E
}

func (f *fakeContainerCommandRunner) PortForward(pod *kubecontainer.Pod, port uint16, stream io.ReadWriteCloser) error {
	f.PodID = pod.ID
	f.Port = port
	f.Stream = stream
	return nil
}

func TestRunInContainerNoSuchPod(t *testing.T) {
	testKubelet := newTestKubelet(t, false /* controllerAttachDetachEnabled */)
	kubelet := testKubelet.kubelet
	fakeRuntime := testKubelet.fakeRuntime
	fakeRuntime.PodList = []*containertest.FakePod{}

	podName := "podFoo"
	podNamespace := "nsFoo"
	containerName := "containerFoo"
	output, err := kubelet.RunInContainer(
		kubecontainer.GetPodFullName(&api.Pod{ObjectMeta: api.ObjectMeta{Name: podName, Namespace: podNamespace}}),
		"",
		containerName,
		[]string{"ls"})
	if output != nil {
		t.Errorf("unexpected non-nil command: %v", output)
	}
	if err == nil {
		t.Error("unexpected non-error")
	}
}

func TestRunInContainer(t *testing.T) {
	testKubelet := newTestKubelet(t, false /* controllerAttachDetachEnabled */)
	kubelet := testKubelet.kubelet
	fakeRuntime := testKubelet.fakeRuntime
	fakeCommandRunner := fakeContainerCommandRunner{}
	kubelet.runner = &fakeCommandRunner

	containerID := kubecontainer.ContainerID{Type: "test", ID: "abc1234"}
	fakeRuntime.PodList = []*containertest.FakePod{
		{Pod: &kubecontainer.Pod{
			ID:        "12345678",
			Name:      "podFoo",
			Namespace: "nsFoo",
			Containers: []*kubecontainer.Container{
				{Name: "containerFoo",
					ID: containerID,
				},
			},
		}},
	}
	cmd := []string{"ls"}
	_, err := kubelet.RunInContainer("podFoo_nsFoo", "", "containerFoo", cmd)
	if fakeCommandRunner.ID != containerID {
		t.Errorf("unexpected Name: %s", fakeCommandRunner.ID)
	}
	if !reflect.DeepEqual(fakeCommandRunner.Cmd, cmd) {
		t.Errorf("unexpected command: %s", fakeCommandRunner.Cmd)
	}
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestDNSConfigurationParams(t *testing.T) {
	testKubelet := newTestKubelet(t, false /* controllerAttachDetachEnabled */)
	kubelet := testKubelet.kubelet

	clusterNS := "203.0.113.1"
	kubelet.clusterDomain = "kubernetes.io"
	kubelet.clusterDNS = net.ParseIP(clusterNS)

	pods := newTestPods(2)
	pods[0].Spec.DNSPolicy = api.DNSClusterFirst
	pods[1].Spec.DNSPolicy = api.DNSDefault

	options := make([]*kubecontainer.RunContainerOptions, 2)
	for i, pod := range pods {
		var err error
		options[i], err = kubelet.GenerateRunContainerOptions(pod, &api.Container{}, "")
		if err != nil {
			t.Fatalf("failed to generate container options: %v", err)
		}
	}
	if len(options[0].DNS) != 1 || options[0].DNS[0] != clusterNS {
		t.Errorf("expected nameserver %s, got %+v", clusterNS, options[0].DNS)
	}
	if len(options[0].DNSSearch) == 0 || options[0].DNSSearch[0] != ".svc."+kubelet.clusterDomain {
		t.Errorf("expected search %s, got %+v", ".svc."+kubelet.clusterDomain, options[0].DNSSearch)
	}
	if len(options[1].DNS) != 1 || options[1].DNS[0] != "127.0.0.1" {
		t.Errorf("expected nameserver 127.0.0.1, got %+v", options[1].DNS)
	}
	if len(options[1].DNSSearch) != 1 || options[1].DNSSearch[0] != "." {
		t.Errorf("expected search \".\", got %+v", options[1].DNSSearch)
	}

	kubelet.resolverConfig = "/etc/resolv.conf"
	for i, pod := range pods {
		var err error
		options[i], err = kubelet.GenerateRunContainerOptions(pod, &api.Container{}, "")
		if err != nil {
			t.Fatalf("failed to generate container options: %v", err)
		}
	}
	t.Logf("nameservers %+v", options[1].DNS)
	if len(options[0].DNS) != 1 {
		t.Errorf("expected cluster nameserver only, got %+v", options[0].DNS)
	} else if options[0].DNS[0] != clusterNS {
		t.Errorf("expected nameserver %s, got %v", clusterNS, options[0].DNS[0])
	}
	if len(options[0].DNSSearch) != len(options[1].DNSSearch)+3 {
		t.Errorf("expected prepend of cluster domain, got %+v", options[0].DNSSearch)
	} else if options[0].DNSSearch[0] != ".svc."+kubelet.clusterDomain {
		t.Errorf("expected domain %s, got %s", ".svc."+kubelet.clusterDomain, options[0].DNSSearch)
	}
}

type testServiceLister struct {
	services []api.Service
}

func (ls testServiceLister) List() (api.ServiceList, error) {
	return api.ServiceList{
		Items: ls.services,
	}, nil
}

type testNodeLister struct {
	nodes []api.Node
}

type testNodeInfo struct {
	nodes []api.Node
}

func (ls testNodeInfo) GetNodeInfo(id string) (*api.Node, error) {
	for _, node := range ls.nodes {
		if node.Name == id {
			return &node, nil
		}
	}
	return nil, fmt.Errorf("Node with name: %s does not exist", id)
}

func (ls testNodeLister) List() (api.NodeList, error) {
	return api.NodeList{
		Items: ls.nodes,
	}, nil
}

type envs []kubecontainer.EnvVar

func (e envs) Len() int {
	return len(e)
}

func (e envs) Swap(i, j int) { e[i], e[j] = e[j], e[i] }

func (e envs) Less(i, j int) bool { return e[i].Name < e[j].Name }

func buildService(name, namespace, clusterIP, protocol string, port int) api.Service {
	return api.Service{
		ObjectMeta: api.ObjectMeta{Name: name, Namespace: namespace},
		Spec: api.ServiceSpec{
			Ports: []api.ServicePort{{
				Protocol: api.Protocol(protocol),
				Port:     int32(port),
			}},
			ClusterIP: clusterIP,
		},
	}
}

func TestMakeEnvironmentVariables(t *testing.T) {
	services := []api.Service{
		buildService("kubernetes", api.NamespaceDefault, "1.2.3.1", "TCP", 8081),
		buildService("test", "test1", "1.2.3.3", "TCP", 8083),
		buildService("kubernetes", "test2", "1.2.3.4", "TCP", 8084),
		buildService("test", "test2", "1.2.3.5", "TCP", 8085),
		buildService("test", "test2", "None", "TCP", 8085),
		buildService("test", "test2", "", "TCP", 8085),
		buildService("kubernetes", "kubernetes", "1.2.3.6", "TCP", 8086),
		buildService("not-special", "kubernetes", "1.2.3.8", "TCP", 8088),
		buildService("not-special", "kubernetes", "None", "TCP", 8088),
		buildService("not-special", "kubernetes", "", "TCP", 8088),
	}

	testCases := []struct {
		name            string                 // the name of the test case
		ns              string                 // the namespace to generate environment for
		container       *api.Container         // the container to use
		masterServiceNs string                 // the namespace to read master service info from
		nilLister       bool                   // whether the lister should be nil
		expectedEnvs    []kubecontainer.EnvVar // a set of expected environment vars
	}{
		{
			name: "api server = Y, kubelet = Y",
			ns:   "test1",
			container: &api.Container{
				Env: []api.EnvVar{
					{Name: "FOO", Value: "BAR"},
					{Name: "TEST_SERVICE_HOST", Value: "1.2.3.3"},
					{Name: "TEST_SERVICE_PORT", Value: "8083"},
					{Name: "TEST_PORT", Value: "tcp://1.2.3.3:8083"},
					{Name: "TEST_PORT_8083_TCP", Value: "tcp://1.2.3.3:8083"},
					{Name: "TEST_PORT_8083_TCP_PROTO", Value: "tcp"},
					{Name: "TEST_PORT_8083_TCP_PORT", Value: "8083"},
					{Name: "TEST_PORT_8083_TCP_ADDR", Value: "1.2.3.3"},
				},
			},
			masterServiceNs: api.NamespaceDefault,
			nilLister:       false,
			expectedEnvs: []kubecontainer.EnvVar{
				{Name: "FOO", Value: "BAR"},
				{Name: "TEST_SERVICE_HOST", Value: "1.2.3.3"},
				{Name: "TEST_SERVICE_PORT", Value: "8083"},
				{Name: "TEST_PORT", Value: "tcp://1.2.3.3:8083"},
				{Name: "TEST_PORT_8083_TCP", Value: "tcp://1.2.3.3:8083"},
				{Name: "TEST_PORT_8083_TCP_PROTO", Value: "tcp"},
				{Name: "TEST_PORT_8083_TCP_PORT", Value: "8083"},
				{Name: "TEST_PORT_8083_TCP_ADDR", Value: "1.2.3.3"},
				{Name: "KUBERNETES_SERVICE_PORT", Value: "8081"},
				{Name: "KUBERNETES_SERVICE_HOST", Value: "1.2.3.1"},
				{Name: "KUBERNETES_PORT", Value: "tcp://1.2.3.1:8081"},
				{Name: "KUBERNETES_PORT_8081_TCP", Value: "tcp://1.2.3.1:8081"},
				{Name: "KUBERNETES_PORT_8081_TCP_PROTO", Value: "tcp"},
				{Name: "KUBERNETES_PORT_8081_TCP_PORT", Value: "8081"},
				{Name: "KUBERNETES_PORT_8081_TCP_ADDR", Value: "1.2.3.1"},
			},
		},
		{
			name: "api server = Y, kubelet = N",
			ns:   "test1",
			container: &api.Container{
				Env: []api.EnvVar{
					{Name: "FOO", Value: "BAR"},
					{Name: "TEST_SERVICE_HOST", Value: "1.2.3.3"},
					{Name: "TEST_SERVICE_PORT", Value: "8083"},
					{Name: "TEST_PORT", Value: "tcp://1.2.3.3:8083"},
					{Name: "TEST_PORT_8083_TCP", Value: "tcp://1.2.3.3:8083"},
					{Name: "TEST_PORT_8083_TCP_PROTO", Value: "tcp"},
					{Name: "TEST_PORT_8083_TCP_PORT", Value: "8083"},
					{Name: "TEST_PORT_8083_TCP_ADDR", Value: "1.2.3.3"},
				},
			},
			masterServiceNs: api.NamespaceDefault,
			nilLister:       true,
			expectedEnvs: []kubecontainer.EnvVar{
				{Name: "FOO", Value: "BAR"},
				{Name: "TEST_SERVICE_HOST", Value: "1.2.3.3"},
				{Name: "TEST_SERVICE_PORT", Value: "8083"},
				{Name: "TEST_PORT", Value: "tcp://1.2.3.3:8083"},
				{Name: "TEST_PORT_8083_TCP", Value: "tcp://1.2.3.3:8083"},
				{Name: "TEST_PORT_8083_TCP_PROTO", Value: "tcp"},
				{Name: "TEST_PORT_8083_TCP_PORT", Value: "8083"},
				{Name: "TEST_PORT_8083_TCP_ADDR", Value: "1.2.3.3"},
			},
		},
		{
			name: "api server = N; kubelet = Y",
			ns:   "test1",
			container: &api.Container{
				Env: []api.EnvVar{
					{Name: "FOO", Value: "BAZ"},
				},
			},
			masterServiceNs: api.NamespaceDefault,
			nilLister:       false,
			expectedEnvs: []kubecontainer.EnvVar{
				{Name: "FOO", Value: "BAZ"},
				{Name: "TEST_SERVICE_HOST", Value: "1.2.3.3"},
				{Name: "TEST_SERVICE_PORT", Value: "8083"},
				{Name: "TEST_PORT", Value: "tcp://1.2.3.3:8083"},
				{Name: "TEST_PORT_8083_TCP", Value: "tcp://1.2.3.3:8083"},
				{Name: "TEST_PORT_8083_TCP_PROTO", Value: "tcp"},
				{Name: "TEST_PORT_8083_TCP_PORT", Value: "8083"},
				{Name: "TEST_PORT_8083_TCP_ADDR", Value: "1.2.3.3"},
				{Name: "KUBERNETES_SERVICE_HOST", Value: "1.2.3.1"},
				{Name: "KUBERNETES_SERVICE_PORT", Value: "8081"},
				{Name: "KUBERNETES_PORT", Value: "tcp://1.2.3.1:8081"},
				{Name: "KUBERNETES_PORT_8081_TCP", Value: "tcp://1.2.3.1:8081"},
				{Name: "KUBERNETES_PORT_8081_TCP_PROTO", Value: "tcp"},
				{Name: "KUBERNETES_PORT_8081_TCP_PORT", Value: "8081"},
				{Name: "KUBERNETES_PORT_8081_TCP_ADDR", Value: "1.2.3.1"},
			},
		},
		{
			name: "master service in pod ns",
			ns:   "test2",
			container: &api.Container{
				Env: []api.EnvVar{
					{Name: "FOO", Value: "ZAP"},
				},
			},
			masterServiceNs: "kubernetes",
			nilLister:       false,
			expectedEnvs: []kubecontainer.EnvVar{
				{Name: "FOO", Value: "ZAP"},
				{Name: "TEST_SERVICE_HOST", Value: "1.2.3.5"},
				{Name: "TEST_SERVICE_PORT", Value: "8085"},
				{Name: "TEST_PORT", Value: "tcp://1.2.3.5:8085"},
				{Name: "TEST_PORT_8085_TCP", Value: "tcp://1.2.3.5:8085"},
				{Name: "TEST_PORT_8085_TCP_PROTO", Value: "tcp"},
				{Name: "TEST_PORT_8085_TCP_PORT", Value: "8085"},
				{Name: "TEST_PORT_8085_TCP_ADDR", Value: "1.2.3.5"},
				{Name: "KUBERNETES_SERVICE_HOST", Value: "1.2.3.4"},
				{Name: "KUBERNETES_SERVICE_PORT", Value: "8084"},
				{Name: "KUBERNETES_PORT", Value: "tcp://1.2.3.4:8084"},
				{Name: "KUBERNETES_PORT_8084_TCP", Value: "tcp://1.2.3.4:8084"},
				{Name: "KUBERNETES_PORT_8084_TCP_PROTO", Value: "tcp"},
				{Name: "KUBERNETES_PORT_8084_TCP_PORT", Value: "8084"},
				{Name: "KUBERNETES_PORT_8084_TCP_ADDR", Value: "1.2.3.4"},
			},
		},
		{
			name:            "pod in master service ns",
			ns:              "kubernetes",
			container:       &api.Container{},
			masterServiceNs: "kubernetes",
			nilLister:       false,
			expectedEnvs: []kubecontainer.EnvVar{
				{Name: "NOT_SPECIAL_SERVICE_HOST", Value: "1.2.3.8"},
				{Name: "NOT_SPECIAL_SERVICE_PORT", Value: "8088"},
				{Name: "NOT_SPECIAL_PORT", Value: "tcp://1.2.3.8:8088"},
				{Name: "NOT_SPECIAL_PORT_8088_TCP", Value: "tcp://1.2.3.8:8088"},
				{Name: "NOT_SPECIAL_PORT_8088_TCP_PROTO", Value: "tcp"},
				{Name: "NOT_SPECIAL_PORT_8088_TCP_PORT", Value: "8088"},
				{Name: "NOT_SPECIAL_PORT_8088_TCP_ADDR", Value: "1.2.3.8"},
				{Name: "KUBERNETES_SERVICE_HOST", Value: "1.2.3.6"},
				{Name: "KUBERNETES_SERVICE_PORT", Value: "8086"},
				{Name: "KUBERNETES_PORT", Value: "tcp://1.2.3.6:8086"},
				{Name: "KUBERNETES_PORT_8086_TCP", Value: "tcp://1.2.3.6:8086"},
				{Name: "KUBERNETES_PORT_8086_TCP_PROTO", Value: "tcp"},
				{Name: "KUBERNETES_PORT_8086_TCP_PORT", Value: "8086"},
				{Name: "KUBERNETES_PORT_8086_TCP_ADDR", Value: "1.2.3.6"},
			},
		},
		{
			name: "downward api pod",
			ns:   "downward-api",
			container: &api.Container{
				Env: []api.EnvVar{
					{
						Name: "POD_NAME",
						ValueFrom: &api.EnvVarSource{
							FieldRef: &api.ObjectFieldSelector{
								APIVersion: testapi.Default.GroupVersion().String(),
								FieldPath:  "metadata.name",
							},
						},
					},
					{
						Name: "POD_NAMESPACE",
						ValueFrom: &api.EnvVarSource{
							FieldRef: &api.ObjectFieldSelector{
								APIVersion: testapi.Default.GroupVersion().String(),
								FieldPath:  "metadata.namespace",
							},
						},
					},
					{
						Name: "POD_IP",
						ValueFrom: &api.EnvVarSource{
							FieldRef: &api.ObjectFieldSelector{
								APIVersion: testapi.Default.GroupVersion().String(),
								FieldPath:  "status.podIP",
							},
						},
					},
				},
			},
			masterServiceNs: "nothing",
			nilLister:       true,
			expectedEnvs: []kubecontainer.EnvVar{
				{Name: "POD_NAME", Value: "dapi-test-pod-name"},
				{Name: "POD_NAMESPACE", Value: "downward-api"},
				{Name: "POD_IP", Value: "1.2.3.4"},
			},
		},
		{
			name: "env expansion",
			ns:   "test1",
			container: &api.Container{
				Env: []api.EnvVar{
					{
						Name:  "TEST_LITERAL",
						Value: "test-test-test",
					},
					{
						Name: "POD_NAME",
						ValueFrom: &api.EnvVarSource{
							FieldRef: &api.ObjectFieldSelector{
								APIVersion: testapi.Default.GroupVersion().String(),
								FieldPath:  "metadata.name",
							},
						},
					},
					{
						Name:  "OUT_OF_ORDER_TEST",
						Value: "$(OUT_OF_ORDER_TARGET)",
					},
					{
						Name:  "OUT_OF_ORDER_TARGET",
						Value: "FOO",
					},
					{
						Name: "EMPTY_VAR",
					},
					{
						Name:  "EMPTY_TEST",
						Value: "foo-$(EMPTY_VAR)",
					},
					{
						Name:  "POD_NAME_TEST2",
						Value: "test2-$(POD_NAME)",
					},
					{
						Name:  "POD_NAME_TEST3",
						Value: "$(POD_NAME_TEST2)-3",
					},
					{
						Name:  "LITERAL_TEST",
						Value: "literal-$(TEST_LITERAL)",
					},
					{
						Name:  "SERVICE_VAR_TEST",
						Value: "$(TEST_SERVICE_HOST):$(TEST_SERVICE_PORT)",
					},
					{
						Name:  "TEST_UNDEFINED",
						Value: "$(UNDEFINED_VAR)",
					},
				},
			},
			masterServiceNs: "nothing",
			nilLister:       false,
			expectedEnvs: []kubecontainer.EnvVar{
				{
					Name:  "TEST_LITERAL",
					Value: "test-test-test",
				},
				{
					Name:  "POD_NAME",
					Value: "dapi-test-pod-name",
				},
				{
					Name:  "POD_NAME_TEST2",
					Value: "test2-dapi-test-pod-name",
				},
				{
					Name:  "POD_NAME_TEST3",
					Value: "test2-dapi-test-pod-name-3",
				},
				{
					Name:  "LITERAL_TEST",
					Value: "literal-test-test-test",
				},
				{
					Name:  "TEST_SERVICE_HOST",
					Value: "1.2.3.3",
				},
				{
					Name:  "TEST_SERVICE_PORT",
					Value: "8083",
				},
				{
					Name:  "TEST_PORT",
					Value: "tcp://1.2.3.3:8083",
				},
				{
					Name:  "TEST_PORT_8083_TCP",
					Value: "tcp://1.2.3.3:8083",
				},
				{
					Name:  "TEST_PORT_8083_TCP_PROTO",
					Value: "tcp",
				},
				{
					Name:  "TEST_PORT_8083_TCP_PORT",
					Value: "8083",
				},
				{
					Name:  "TEST_PORT_8083_TCP_ADDR",
					Value: "1.2.3.3",
				},
				{
					Name:  "SERVICE_VAR_TEST",
					Value: "1.2.3.3:8083",
				},
				{
					Name:  "OUT_OF_ORDER_TEST",
					Value: "$(OUT_OF_ORDER_TARGET)",
				},
				{
					Name:  "OUT_OF_ORDER_TARGET",
					Value: "FOO",
				},
				{
					Name:  "TEST_UNDEFINED",
					Value: "$(UNDEFINED_VAR)",
				},
				{
					Name: "EMPTY_VAR",
				},
				{
					Name:  "EMPTY_TEST",
					Value: "foo-",
				},
			},
		},
	}

	for i, tc := range testCases {
		testKubelet := newTestKubelet(t, false /* controllerAttachDetachEnabled */)
		kl := testKubelet.kubelet
		kl.masterServiceNamespace = tc.masterServiceNs
		if tc.nilLister {
			kl.serviceLister = nil
		} else {
			kl.serviceLister = testServiceLister{services}
		}

		testPod := &api.Pod{
			ObjectMeta: api.ObjectMeta{
				Namespace: tc.ns,
				Name:      "dapi-test-pod-name",
			},
		}
		podIP := "1.2.3.4"

		result, err := kl.makeEnvironmentVariables(testPod, tc.container, podIP)
		if err != nil {
			t.Errorf("[%v] Unexpected error: %v", tc.name, err)
		}

		sort.Sort(envs(result))
		sort.Sort(envs(tc.expectedEnvs))

		if !reflect.DeepEqual(result, tc.expectedEnvs) {
			t.Errorf("%d: [%v] Unexpected env entries; expected {%v}, got {%v}", i, tc.name, tc.expectedEnvs, result)
		}
	}
}

func waitingState(cName string) api.ContainerStatus {
	return api.ContainerStatus{
		Name: cName,
		State: api.ContainerState{
			Waiting: &api.ContainerStateWaiting{},
		},
	}
}
func waitingStateWithLastTermination(cName string) api.ContainerStatus {
	return api.ContainerStatus{
		Name: cName,
		State: api.ContainerState{
			Waiting: &api.ContainerStateWaiting{},
		},
		LastTerminationState: api.ContainerState{
			Terminated: &api.ContainerStateTerminated{
				ExitCode: 0,
			},
		},
	}
}
func runningState(cName string) api.ContainerStatus {
	return api.ContainerStatus{
		Name: cName,
		State: api.ContainerState{
			Running: &api.ContainerStateRunning{},
		},
	}
}
func stoppedState(cName string) api.ContainerStatus {
	return api.ContainerStatus{
		Name: cName,
		State: api.ContainerState{
			Terminated: &api.ContainerStateTerminated{},
		},
	}
}
func succeededState(cName string) api.ContainerStatus {
	return api.ContainerStatus{
		Name: cName,
		State: api.ContainerState{
			Terminated: &api.ContainerStateTerminated{
				ExitCode: 0,
			},
		},
	}
}
func failedState(cName string) api.ContainerStatus {
	return api.ContainerStatus{
		Name: cName,
		State: api.ContainerState{
			Terminated: &api.ContainerStateTerminated{
				ExitCode: -1,
			},
		},
	}
}

func TestPodPhaseWithRestartAlways(t *testing.T) {
	desiredState := api.PodSpec{
		NodeName: "machine",
		Containers: []api.Container{
			{Name: "containerA"},
			{Name: "containerB"},
		},
		RestartPolicy: api.RestartPolicyAlways,
	}

	tests := []struct {
		pod    *api.Pod
		status api.PodPhase
		test   string
	}{
		{&api.Pod{Spec: desiredState, Status: api.PodStatus{}}, api.PodPending, "waiting"},
		{
			&api.Pod{
				Spec: desiredState,
				Status: api.PodStatus{
					ContainerStatuses: []api.ContainerStatus{
						runningState("containerA"),
						runningState("containerB"),
					},
				},
			},
			api.PodRunning,
			"all running",
		},
		{
			&api.Pod{
				Spec: desiredState,
				Status: api.PodStatus{
					ContainerStatuses: []api.ContainerStatus{
						stoppedState("containerA"),
						stoppedState("containerB"),
					},
				},
			},
			api.PodRunning,
			"all stopped with restart always",
		},
		{
			&api.Pod{
				Spec: desiredState,
				Status: api.PodStatus{
					ContainerStatuses: []api.ContainerStatus{
						runningState("containerA"),
						stoppedState("containerB"),
					},
				},
			},
			api.PodRunning,
			"mixed state #1 with restart always",
		},
		{
			&api.Pod{
				Spec: desiredState,
				Status: api.PodStatus{
					ContainerStatuses: []api.ContainerStatus{
						runningState("containerA"),
					},
				},
			},
			api.PodPending,
			"mixed state #2 with restart always",
		},
		{
			&api.Pod{
				Spec: desiredState,
				Status: api.PodStatus{
					ContainerStatuses: []api.ContainerStatus{
						runningState("containerA"),
						waitingState("containerB"),
					},
				},
			},
			api.PodPending,
			"mixed state #3 with restart always",
		},
		{
			&api.Pod{
				Spec: desiredState,
				Status: api.PodStatus{
					ContainerStatuses: []api.ContainerStatus{
						runningState("containerA"),
						waitingStateWithLastTermination("containerB"),
					},
				},
			},
			api.PodRunning,
			"backoff crashloop container with restart always",
		},
	}
	for _, test := range tests {
		if status := GetPhase(&test.pod.Spec, test.pod.Status.ContainerStatuses); status != test.status {
			t.Errorf("In test %s, expected %v, got %v", test.test, test.status, status)
		}
	}
}

func TestPodPhaseWithRestartNever(t *testing.T) {
	desiredState := api.PodSpec{
		NodeName: "machine",
		Containers: []api.Container{
			{Name: "containerA"},
			{Name: "containerB"},
		},
		RestartPolicy: api.RestartPolicyNever,
	}

	tests := []struct {
		pod    *api.Pod
		status api.PodPhase
		test   string
	}{
		{&api.Pod{Spec: desiredState, Status: api.PodStatus{}}, api.PodPending, "waiting"},
		{
			&api.Pod{
				Spec: desiredState,
				Status: api.PodStatus{
					ContainerStatuses: []api.ContainerStatus{
						runningState("containerA"),
						runningState("containerB"),
					},
				},
			},
			api.PodRunning,
			"all running with restart never",
		},
		{
			&api.Pod{
				Spec: desiredState,
				Status: api.PodStatus{
					ContainerStatuses: []api.ContainerStatus{
						succeededState("containerA"),
						succeededState("containerB"),
					},
				},
			},
			api.PodSucceeded,
			"all succeeded with restart never",
		},
		{
			&api.Pod{
				Spec: desiredState,
				Status: api.PodStatus{
					ContainerStatuses: []api.ContainerStatus{
						failedState("containerA"),
						failedState("containerB"),
					},
				},
			},
			api.PodFailed,
			"all failed with restart never",
		},
		{
			&api.Pod{
				Spec: desiredState,
				Status: api.PodStatus{
					ContainerStatuses: []api.ContainerStatus{
						runningState("containerA"),
						succeededState("containerB"),
					},
				},
			},
			api.PodRunning,
			"mixed state #1 with restart never",
		},
		{
			&api.Pod{
				Spec: desiredState,
				Status: api.PodStatus{
					ContainerStatuses: []api.ContainerStatus{
						runningState("containerA"),
					},
				},
			},
			api.PodPending,
			"mixed state #2 with restart never",
		},
		{
			&api.Pod{
				Spec: desiredState,
				Status: api.PodStatus{
					ContainerStatuses: []api.ContainerStatus{
						runningState("containerA"),
						waitingState("containerB"),
					},
				},
			},
			api.PodPending,
			"mixed state #3 with restart never",
		},
	}
	for _, test := range tests {
		if status := GetPhase(&test.pod.Spec, test.pod.Status.ContainerStatuses); status != test.status {
			t.Errorf("In test %s, expected %v, got %v", test.test, test.status, status)
		}
	}
}

func TestPodPhaseWithRestartOnFailure(t *testing.T) {
	desiredState := api.PodSpec{
		NodeName: "machine",
		Containers: []api.Container{
			{Name: "containerA"},
			{Name: "containerB"},
		},
		RestartPolicy: api.RestartPolicyOnFailure,
	}

	tests := []struct {
		pod    *api.Pod
		status api.PodPhase
		test   string
	}{
		{&api.Pod{Spec: desiredState, Status: api.PodStatus{}}, api.PodPending, "waiting"},
		{
			&api.Pod{
				Spec: desiredState,
				Status: api.PodStatus{
					ContainerStatuses: []api.ContainerStatus{
						runningState("containerA"),
						runningState("containerB"),
					},
				},
			},
			api.PodRunning,
			"all running with restart onfailure",
		},
		{
			&api.Pod{
				Spec: desiredState,
				Status: api.PodStatus{
					ContainerStatuses: []api.ContainerStatus{
						succeededState("containerA"),
						succeededState("containerB"),
					},
				},
			},
			api.PodSucceeded,
			"all succeeded with restart onfailure",
		},
		{
			&api.Pod{
				Spec: desiredState,
				Status: api.PodStatus{
					ContainerStatuses: []api.ContainerStatus{
						failedState("containerA"),
						failedState("containerB"),
					},
				},
			},
			api.PodRunning,
			"all failed with restart never",
		},
		{
			&api.Pod{
				Spec: desiredState,
				Status: api.PodStatus{
					ContainerStatuses: []api.ContainerStatus{
						runningState("containerA"),
						succeededState("containerB"),
					},
				},
			},
			api.PodRunning,
			"mixed state #1 with restart onfailure",
		},
		{
			&api.Pod{
				Spec: desiredState,
				Status: api.PodStatus{
					ContainerStatuses: []api.ContainerStatus{
						runningState("containerA"),
					},
				},
			},
			api.PodPending,
			"mixed state #2 with restart onfailure",
		},
		{
			&api.Pod{
				Spec: desiredState,
				Status: api.PodStatus{
					ContainerStatuses: []api.ContainerStatus{
						runningState("containerA"),
						waitingState("containerB"),
					},
				},
			},
			api.PodPending,
			"mixed state #3 with restart onfailure",
		},
		{
			&api.Pod{
				Spec: desiredState,
				Status: api.PodStatus{
					ContainerStatuses: []api.ContainerStatus{
						runningState("containerA"),
						waitingStateWithLastTermination("containerB"),
					},
				},
			},
			api.PodRunning,
			"backoff crashloop container with restart onfailure",
		},
	}
	for _, test := range tests {
		if status := GetPhase(&test.pod.Spec, test.pod.Status.ContainerStatuses); status != test.status {
			t.Errorf("In test %s, expected %v, got %v", test.test, test.status, status)
		}
	}
}

func TestExecInContainerNoSuchPod(t *testing.T) {
	testKubelet := newTestKubelet(t, false /* controllerAttachDetachEnabled */)
	kubelet := testKubelet.kubelet
	fakeRuntime := testKubelet.fakeRuntime
	fakeCommandRunner := fakeContainerCommandRunner{}
	kubelet.runner = &fakeCommandRunner
	fakeRuntime.PodList = []*containertest.FakePod{}

	podName := "podFoo"
	podNamespace := "nsFoo"
	containerID := "containerFoo"
	err := kubelet.ExecInContainer(
		kubecontainer.GetPodFullName(&api.Pod{ObjectMeta: api.ObjectMeta{Name: podName, Namespace: podNamespace}}),
		"",
		containerID,
		[]string{"ls"},
		nil,
		nil,
		nil,
		false,
		nil,
	)
	if err == nil {
		t.Fatal("unexpected non-error")
	}
	if !fakeCommandRunner.ID.IsEmpty() {
		t.Fatal("unexpected invocation of runner.ExecInContainer")
	}
}

func TestExecInContainerNoSuchContainer(t *testing.T) {
	testKubelet := newTestKubelet(t, false /* controllerAttachDetachEnabled */)
	kubelet := testKubelet.kubelet
	fakeRuntime := testKubelet.fakeRuntime
	fakeCommandRunner := fakeContainerCommandRunner{}
	kubelet.runner = &fakeCommandRunner

	podName := "podFoo"
	podNamespace := "nsFoo"
	containerID := "containerFoo"
	fakeRuntime.PodList = []*containertest.FakePod{
		{Pod: &kubecontainer.Pod{
			ID:        "12345678",
			Name:      podName,
			Namespace: podNamespace,
			Containers: []*kubecontainer.Container{
				{Name: "bar",
					ID: kubecontainer.ContainerID{Type: "test", ID: "barID"}},
			},
		}},
	}

	err := kubelet.ExecInContainer(
		kubecontainer.GetPodFullName(&api.Pod{ObjectMeta: api.ObjectMeta{
			UID:       "12345678",
			Name:      podName,
			Namespace: podNamespace,
		}}),
		"",
		containerID,
		[]string{"ls"},
		nil,
		nil,
		nil,
		false,
		nil,
	)
	if err == nil {
		t.Fatal("unexpected non-error")
	}
	if !fakeCommandRunner.ID.IsEmpty() {
		t.Fatal("unexpected invocation of runner.ExecInContainer")
	}
}

type fakeReadWriteCloser struct{}

func (f *fakeReadWriteCloser) Write(data []byte) (int, error) {
	return 0, nil
}

func (f *fakeReadWriteCloser) Read(data []byte) (int, error) {
	return 0, nil
}

func (f *fakeReadWriteCloser) Close() error {
	return nil
}

func TestExecInContainer(t *testing.T) {
	testKubelet := newTestKubelet(t, false /* controllerAttachDetachEnabled */)
	kubelet := testKubelet.kubelet
	fakeRuntime := testKubelet.fakeRuntime
	fakeCommandRunner := fakeContainerCommandRunner{}
	kubelet.runner = &fakeCommandRunner

	podName := "podFoo"
	podNamespace := "nsFoo"
	containerID := "containerFoo"
	command := []string{"ls"}
	stdin := &bytes.Buffer{}
	stdout := &fakeReadWriteCloser{}
	stderr := &fakeReadWriteCloser{}
	tty := true
	fakeRuntime.PodList = []*containertest.FakePod{
		{Pod: &kubecontainer.Pod{
			ID:        "12345678",
			Name:      podName,
			Namespace: podNamespace,
			Containers: []*kubecontainer.Container{
				{Name: containerID,
					ID: kubecontainer.ContainerID{Type: "test", ID: containerID},
				},
			},
		}},
	}

	err := kubelet.ExecInContainer(
		kubecontainer.GetPodFullName(podWithUidNameNs("12345678", podName, podNamespace)),
		"",
		containerID,
		[]string{"ls"},
		stdin,
		stdout,
		stderr,
		tty,
		nil,
	)
	if err != nil {
		t.Fatalf("unexpected error: %s", err)
	}
	if e, a := containerID, fakeCommandRunner.ID.ID; e != a {
		t.Fatalf("container name: expected %q, got %q", e, a)
	}
	if e, a := command, fakeCommandRunner.Cmd; !reflect.DeepEqual(e, a) {
		t.Fatalf("command: expected '%v', got '%v'", e, a)
	}
	if e, a := stdin, fakeCommandRunner.Stdin; e != a {
		t.Fatalf("stdin: expected %#v, got %#v", e, a)
	}
	if e, a := stdout, fakeCommandRunner.Stdout; e != a {
		t.Fatalf("stdout: expected %#v, got %#v", e, a)
	}
	if e, a := stderr, fakeCommandRunner.Stderr; e != a {
		t.Fatalf("stderr: expected %#v, got %#v", e, a)
	}
	if e, a := tty, fakeCommandRunner.TTY; e != a {
		t.Fatalf("tty: expected %t, got %t", e, a)
	}
}

func TestPortForwardNoSuchPod(t *testing.T) {
	testKubelet := newTestKubelet(t, false /* controllerAttachDetachEnabled */)
	kubelet := testKubelet.kubelet
	fakeRuntime := testKubelet.fakeRuntime
	fakeRuntime.PodList = []*containertest.FakePod{}
	fakeCommandRunner := fakeContainerCommandRunner{}
	kubelet.runner = &fakeCommandRunner

	podName := "podFoo"
	podNamespace := "nsFoo"
	var port uint16 = 5000

	err := kubelet.PortForward(
		kubecontainer.GetPodFullName(&api.Pod{ObjectMeta: api.ObjectMeta{Name: podName, Namespace: podNamespace}}),
		"",
		port,
		nil,
	)
	if err == nil {
		t.Fatal("unexpected non-error")
	}
	if !fakeCommandRunner.ID.IsEmpty() {
		t.Fatal("unexpected invocation of runner.PortForward")
	}
}

func TestPortForward(t *testing.T) {
	testKubelet := newTestKubelet(t, false /* controllerAttachDetachEnabled */)
	kubelet := testKubelet.kubelet
	fakeRuntime := testKubelet.fakeRuntime

	podName := "podFoo"
	podNamespace := "nsFoo"
	podID := types.UID("12345678")
	fakeRuntime.PodList = []*containertest.FakePod{
		{Pod: &kubecontainer.Pod{
			ID:        podID,
			Name:      podName,
			Namespace: podNamespace,
			Containers: []*kubecontainer.Container{
				{
					Name: "foo",
					ID:   kubecontainer.ContainerID{Type: "test", ID: "containerFoo"},
				},
			},
		}},
	}
	fakeCommandRunner := fakeContainerCommandRunner{}
	kubelet.runner = &fakeCommandRunner

	var port uint16 = 5000
	stream := &fakeReadWriteCloser{}
	err := kubelet.PortForward(
		kubecontainer.GetPodFullName(&api.Pod{ObjectMeta: api.ObjectMeta{
			UID:       "12345678",
			Name:      podName,
			Namespace: podNamespace,
		}}),
		"",
		port,
		stream,
	)
	if err != nil {
		t.Fatalf("unexpected error: %s", err)
	}
	if e, a := podID, fakeCommandRunner.PodID; e != a {
		t.Fatalf("container id: expected %q, got %q", e, a)
	}
	if e, a := port, fakeCommandRunner.Port; e != a {
		t.Fatalf("port: expected %v, got %v", e, a)
	}
	if e, a := stream, fakeCommandRunner.Stream; e != a {
		t.Fatalf("stream: expected %v, got %v", e, a)
	}
}

func TestValidateContainerLogStatus(t *testing.T) {
	testKubelet := newTestKubelet(t, false /* controllerAttachDetachEnabled */)
	kubelet := testKubelet.kubelet
	containerName := "x"
	testCases := []struct {
		statuses []api.ContainerStatus
		success  bool
	}{
		{
			statuses: []api.ContainerStatus{
				{
					Name: containerName,
					State: api.ContainerState{
						Running: &api.ContainerStateRunning{},
					},
					LastTerminationState: api.ContainerState{
						Terminated: &api.ContainerStateTerminated{},
					},
				},
			},
			success: true,
		},
		{
			statuses: []api.ContainerStatus{
				{
					Name: containerName,
					State: api.ContainerState{
						Running: &api.ContainerStateRunning{},
					},
				},
			},
			success: true,
		},
		{
			statuses: []api.ContainerStatus{
				{
					Name: containerName,
					State: api.ContainerState{
						Terminated: &api.ContainerStateTerminated{},
					},
				},
			},
			success: true,
		},
		{
			statuses: []api.ContainerStatus{
				{
					Name: containerName,
					State: api.ContainerState{
						Waiting: &api.ContainerStateWaiting{},
					},
				},
			},
			success: false,
		},
		{
			statuses: []api.ContainerStatus{
				{
					Name:  containerName,
					State: api.ContainerState{Waiting: &api.ContainerStateWaiting{Reason: "ErrImagePull"}},
				},
			},
			success: false,
		},
		{
			statuses: []api.ContainerStatus{
				{
					Name:  containerName,
					State: api.ContainerState{Waiting: &api.ContainerStateWaiting{Reason: "ErrImagePullBackOff"}},
				},
			},
			success: false,
		},
	}

	for i, tc := range testCases {
		_, err := kubelet.validateContainerLogStatus("podName", &api.PodStatus{
			ContainerStatuses: tc.statuses,
		}, containerName, false)
		if tc.success {
			if err != nil {
				t.Errorf("[case %d]: unexpected failure - %v", i, err)
			}
		} else if err == nil {
			t.Errorf("[case %d]: unexpected success", i)
		}
	}
	if _, err := kubelet.validateContainerLogStatus("podName", &api.PodStatus{
		ContainerStatuses: testCases[0].statuses,
	}, "blah", false); err == nil {
		t.Errorf("expected error with invalid container name")
	}
	if _, err := kubelet.validateContainerLogStatus("podName", &api.PodStatus{
		ContainerStatuses: testCases[0].statuses,
	}, containerName, true); err != nil {
		t.Errorf("unexpected error with for previous terminated container - %v", err)
	}
	if _, err := kubelet.validateContainerLogStatus("podName", &api.PodStatus{
		ContainerStatuses: testCases[0].statuses,
	}, containerName, false); err != nil {
		t.Errorf("unexpected error with for most recent container - %v", err)
	}
	if _, err := kubelet.validateContainerLogStatus("podName", &api.PodStatus{
		ContainerStatuses: testCases[1].statuses,
	}, containerName, true); err == nil {
		t.Errorf("expected error with for previous terminated container")
	}
	if _, err := kubelet.validateContainerLogStatus("podName", &api.PodStatus{
		ContainerStatuses: testCases[1].statuses,
	}, containerName, false); err != nil {
		t.Errorf("unexpected error with for most recent container")
	}
}

func TestGenerateAPIPodStatusWithSortedContainers(t *testing.T) {
	testKubelet := newTestKubelet(t, false /* controllerAttachDetachEnabled */)
	testKubelet.fakeCadvisor.On("VersionInfo").Return(&cadvisorapi.VersionInfo{}, nil)
	testKubelet.fakeCadvisor.On("MachineInfo").Return(&cadvisorapi.MachineInfo{}, nil)
	testKubelet.fakeCadvisor.On("ImagesFsInfo").Return(cadvisorapiv2.FsInfo{}, nil)
	testKubelet.fakeCadvisor.On("RootFsInfo").Return(cadvisorapiv2.FsInfo{}, nil)
	kubelet := testKubelet.kubelet
	numContainers := 10
	expectedOrder := []string{}
	cStatuses := []*kubecontainer.ContainerStatus{}
	specContainerList := []api.Container{}
	for i := 0; i < numContainers; i++ {
		id := fmt.Sprintf("%v", i)
		containerName := fmt.Sprintf("%vcontainer", id)
		expectedOrder = append(expectedOrder, containerName)
		cStatus := &kubecontainer.ContainerStatus{
			ID:   kubecontainer.BuildContainerID("test", id),
			Name: containerName,
		}
		// Rearrange container statuses
		if i%2 == 0 {
			cStatuses = append(cStatuses, cStatus)
		} else {
			cStatuses = append([]*kubecontainer.ContainerStatus{cStatus}, cStatuses...)
		}
		specContainerList = append(specContainerList, api.Container{Name: containerName})
	}
	pod := podWithUidNameNs("uid1", "foo", "test")
	pod.Spec = api.PodSpec{
		Containers: specContainerList,
	}

	status := &kubecontainer.PodStatus{
		ID:                pod.UID,
		Name:              pod.Name,
		Namespace:         pod.Namespace,
		ContainerStatuses: cStatuses,
	}
	for i := 0; i < 5; i++ {
		apiStatus := kubelet.generateAPIPodStatus(pod, status)
		for i, c := range apiStatus.ContainerStatuses {
			if expectedOrder[i] != c.Name {
				t.Fatalf("Container status not sorted, expected %v at index %d, but found %v", expectedOrder[i], i, c.Name)
			}
		}
	}
}

func verifyContainerStatuses(statuses []api.ContainerStatus, state, lastTerminationState map[string]api.ContainerState) error {
	for _, s := range statuses {
		if !reflect.DeepEqual(s.State, state[s.Name]) {
			return fmt.Errorf("unexpected state: %s", diff.ObjectDiff(state[s.Name], s.State))
		}
		if !reflect.DeepEqual(s.LastTerminationState, lastTerminationState[s.Name]) {
			return fmt.Errorf("unexpected last termination state %s", diff.ObjectDiff(
				lastTerminationState[s.Name], s.LastTerminationState))
		}
	}
	return nil
}

// Test generateAPIPodStatus with different reason cache and old api pod status.
func TestGenerateAPIPodStatusWithReasonCache(t *testing.T) {
	// The following waiting reason and message  are generated in convertStatusToAPIStatus()
	startWaitingReason := "ContainerCreating"
	initWaitingReason := "PodInitializing"
	testTimestamp := time.Unix(123456789, 987654321)
	testErrorReason := fmt.Errorf("test-error")
	emptyContainerID := (&kubecontainer.ContainerID{}).String()
	testKubelet := newTestKubelet(t, false /* controllerAttachDetachEnabled */)
	testKubelet.fakeCadvisor.On("VersionInfo").Return(&cadvisorapi.VersionInfo{}, nil)
	testKubelet.fakeCadvisor.On("MachineInfo").Return(&cadvisorapi.MachineInfo{}, nil)
	testKubelet.fakeCadvisor.On("ImagesFsInfo").Return(cadvisorapiv2.FsInfo{}, nil)
	testKubelet.fakeCadvisor.On("RootFsInfo").Return(cadvisorapiv2.FsInfo{}, nil)
	kubelet := testKubelet.kubelet
	pod := podWithUidNameNs("12345678", "foo", "new")
	pod.Spec = api.PodSpec{RestartPolicy: api.RestartPolicyOnFailure}

	podStatus := &kubecontainer.PodStatus{
		ID:        pod.UID,
		Name:      pod.Name,
		Namespace: pod.Namespace,
	}
	tests := []struct {
		containers    []api.Container
		statuses      []*kubecontainer.ContainerStatus
		reasons       map[string]error
		oldStatuses   []api.ContainerStatus
		expectedState map[string]api.ContainerState
		// Only set expectedInitState when it is different from expectedState
		expectedInitState            map[string]api.ContainerState
		expectedLastTerminationState map[string]api.ContainerState
	}{
		// For container with no historical record, State should be Waiting, LastTerminationState should be retrieved from
		// old status from apiserver.
		{
			containers: []api.Container{{Name: "without-old-record"}, {Name: "with-old-record"}},
			statuses:   []*kubecontainer.ContainerStatus{},
			reasons:    map[string]error{},
			oldStatuses: []api.ContainerStatus{{
				Name:                 "with-old-record",
				LastTerminationState: api.ContainerState{Terminated: &api.ContainerStateTerminated{}},
			}},
			expectedState: map[string]api.ContainerState{
				"without-old-record": {Waiting: &api.ContainerStateWaiting{
					Reason: startWaitingReason,
				}},
				"with-old-record": {Waiting: &api.ContainerStateWaiting{
					Reason: startWaitingReason,
				}},
			},
			expectedInitState: map[string]api.ContainerState{
				"without-old-record": {Waiting: &api.ContainerStateWaiting{
					Reason: initWaitingReason,
				}},
				"with-old-record": {Waiting: &api.ContainerStateWaiting{
					Reason: initWaitingReason,
				}},
			},
			expectedLastTerminationState: map[string]api.ContainerState{
				"with-old-record": {Terminated: &api.ContainerStateTerminated{}},
			},
		},
		// For running container, State should be Running, LastTerminationState should be retrieved from latest terminated status.
		{
			containers: []api.Container{{Name: "running"}},
			statuses: []*kubecontainer.ContainerStatus{
				{
					Name:      "running",
					State:     kubecontainer.ContainerStateRunning,
					StartedAt: testTimestamp,
				},
				{
					Name:     "running",
					State:    kubecontainer.ContainerStateExited,
					ExitCode: 1,
				},
			},
			reasons:     map[string]error{},
			oldStatuses: []api.ContainerStatus{},
			expectedState: map[string]api.ContainerState{
				"running": {Running: &api.ContainerStateRunning{
					StartedAt: unversioned.NewTime(testTimestamp),
				}},
			},
			expectedLastTerminationState: map[string]api.ContainerState{
				"running": {Terminated: &api.ContainerStateTerminated{
					ExitCode:    1,
					ContainerID: emptyContainerID,
				}},
			},
		},
		// For terminated container:
		// * If there is no recent start error record, State should be Terminated, LastTerminationState should be retrieved from
		// second latest terminated status;
		// * If there is recent start error record, State should be Waiting, LastTerminationState should be retrieved from latest
		// terminated status;
		// * If ExitCode = 0, restart policy is RestartPolicyOnFailure, the container shouldn't be restarted. No matter there is
		// recent start error or not, State should be Terminated, LastTerminationState should be retrieved from second latest
		// terminated status.
		{
			containers: []api.Container{{Name: "without-reason"}, {Name: "with-reason"}},
			statuses: []*kubecontainer.ContainerStatus{
				{
					Name:     "without-reason",
					State:    kubecontainer.ContainerStateExited,
					ExitCode: 1,
				},
				{
					Name:     "with-reason",
					State:    kubecontainer.ContainerStateExited,
					ExitCode: 2,
				},
				{
					Name:     "without-reason",
					State:    kubecontainer.ContainerStateExited,
					ExitCode: 3,
				},
				{
					Name:     "with-reason",
					State:    kubecontainer.ContainerStateExited,
					ExitCode: 4,
				},
				{
					Name:     "succeed",
					State:    kubecontainer.ContainerStateExited,
					ExitCode: 0,
				},
				{
					Name:     "succeed",
					State:    kubecontainer.ContainerStateExited,
					ExitCode: 5,
				},
			},
			reasons:     map[string]error{"with-reason": testErrorReason, "succeed": testErrorReason},
			oldStatuses: []api.ContainerStatus{},
			expectedState: map[string]api.ContainerState{
				"without-reason": {Terminated: &api.ContainerStateTerminated{
					ExitCode:    1,
					ContainerID: emptyContainerID,
				}},
				"with-reason": {Waiting: &api.ContainerStateWaiting{Reason: testErrorReason.Error()}},
				"succeed": {Terminated: &api.ContainerStateTerminated{
					ExitCode:    0,
					ContainerID: emptyContainerID,
				}},
			},
			expectedLastTerminationState: map[string]api.ContainerState{
				"without-reason": {Terminated: &api.ContainerStateTerminated{
					ExitCode:    3,
					ContainerID: emptyContainerID,
				}},
				"with-reason": {Terminated: &api.ContainerStateTerminated{
					ExitCode:    2,
					ContainerID: emptyContainerID,
				}},
				"succeed": {Terminated: &api.ContainerStateTerminated{
					ExitCode:    5,
					ContainerID: emptyContainerID,
				}},
			},
		},
	}

	for i, test := range tests {
		kubelet.reasonCache = NewReasonCache()
		for n, e := range test.reasons {
			kubelet.reasonCache.add(pod.UID, n, e, "")
		}
		pod.Spec.Containers = test.containers
		pod.Status.ContainerStatuses = test.oldStatuses
		podStatus.ContainerStatuses = test.statuses
		apiStatus := kubelet.generateAPIPodStatus(pod, podStatus)
		assert.NoError(t, verifyContainerStatuses(apiStatus.ContainerStatuses, test.expectedState, test.expectedLastTerminationState), "case %d", i)
	}

	// Everything should be the same for init containers
	for i, test := range tests {
		kubelet.reasonCache = NewReasonCache()
		for n, e := range test.reasons {
			kubelet.reasonCache.add(pod.UID, n, e, "")
		}
		pod.Spec.InitContainers = test.containers
		pod.Status.InitContainerStatuses = test.oldStatuses
		podStatus.ContainerStatuses = test.statuses
		apiStatus := kubelet.generateAPIPodStatus(pod, podStatus)
		expectedState := test.expectedState
		if test.expectedInitState != nil {
			expectedState = test.expectedInitState
		}
		assert.NoError(t, verifyContainerStatuses(apiStatus.InitContainerStatuses, expectedState, test.expectedLastTerminationState), "case %d", i)
	}
}

// Test generateAPIPodStatus with different restart policies.
func TestGenerateAPIPodStatusWithDifferentRestartPolicies(t *testing.T) {
	testErrorReason := fmt.Errorf("test-error")
	emptyContainerID := (&kubecontainer.ContainerID{}).String()
	testKubelet := newTestKubelet(t, false /* controllerAttachDetachEnabled */)
	testKubelet.fakeCadvisor.On("VersionInfo").Return(&cadvisorapi.VersionInfo{}, nil)
	testKubelet.fakeCadvisor.On("MachineInfo").Return(&cadvisorapi.MachineInfo{}, nil)
	testKubelet.fakeCadvisor.On("ImagesFsInfo").Return(cadvisorapiv2.FsInfo{}, nil)
	testKubelet.fakeCadvisor.On("RootFsInfo").Return(cadvisorapiv2.FsInfo{}, nil)
	kubelet := testKubelet.kubelet
	pod := podWithUidNameNs("12345678", "foo", "new")
	containers := []api.Container{{Name: "succeed"}, {Name: "failed"}}
	podStatus := &kubecontainer.PodStatus{
		ID:        pod.UID,
		Name:      pod.Name,
		Namespace: pod.Namespace,
		ContainerStatuses: []*kubecontainer.ContainerStatus{
			{
				Name:     "succeed",
				State:    kubecontainer.ContainerStateExited,
				ExitCode: 0,
			},
			{
				Name:     "failed",
				State:    kubecontainer.ContainerStateExited,
				ExitCode: 1,
			},
			{
				Name:     "succeed",
				State:    kubecontainer.ContainerStateExited,
				ExitCode: 2,
			},
			{
				Name:     "failed",
				State:    kubecontainer.ContainerStateExited,
				ExitCode: 3,
			},
		},
	}
	kubelet.reasonCache.add(pod.UID, "succeed", testErrorReason, "")
	kubelet.reasonCache.add(pod.UID, "failed", testErrorReason, "")
	for c, test := range []struct {
		restartPolicy                api.RestartPolicy
		expectedState                map[string]api.ContainerState
		expectedLastTerminationState map[string]api.ContainerState
		// Only set expectedInitState when it is different from expectedState
		expectedInitState map[string]api.ContainerState
		// Only set expectedInitLastTerminationState when it is different from expectedLastTerminationState
		expectedInitLastTerminationState map[string]api.ContainerState
	}{
		{
			restartPolicy: api.RestartPolicyNever,
			expectedState: map[string]api.ContainerState{
				"succeed": {Terminated: &api.ContainerStateTerminated{
					ExitCode:    0,
					ContainerID: emptyContainerID,
				}},
				"failed": {Terminated: &api.ContainerStateTerminated{
					ExitCode:    1,
					ContainerID: emptyContainerID,
				}},
			},
			expectedLastTerminationState: map[string]api.ContainerState{
				"succeed": {Terminated: &api.ContainerStateTerminated{
					ExitCode:    2,
					ContainerID: emptyContainerID,
				}},
				"failed": {Terminated: &api.ContainerStateTerminated{
					ExitCode:    3,
					ContainerID: emptyContainerID,
				}},
			},
		},
		{
			restartPolicy: api.RestartPolicyOnFailure,
			expectedState: map[string]api.ContainerState{
				"succeed": {Terminated: &api.ContainerStateTerminated{
					ExitCode:    0,
					ContainerID: emptyContainerID,
				}},
				"failed": {Waiting: &api.ContainerStateWaiting{Reason: testErrorReason.Error()}},
			},
			expectedLastTerminationState: map[string]api.ContainerState{
				"succeed": {Terminated: &api.ContainerStateTerminated{
					ExitCode:    2,
					ContainerID: emptyContainerID,
				}},
				"failed": {Terminated: &api.ContainerStateTerminated{
					ExitCode:    1,
					ContainerID: emptyContainerID,
				}},
			},
		},
		{
			restartPolicy: api.RestartPolicyAlways,
			expectedState: map[string]api.ContainerState{
				"succeed": {Waiting: &api.ContainerStateWaiting{Reason: testErrorReason.Error()}},
				"failed":  {Waiting: &api.ContainerStateWaiting{Reason: testErrorReason.Error()}},
			},
			expectedLastTerminationState: map[string]api.ContainerState{
				"succeed": {Terminated: &api.ContainerStateTerminated{
					ExitCode:    0,
					ContainerID: emptyContainerID,
				}},
				"failed": {Terminated: &api.ContainerStateTerminated{
					ExitCode:    1,
					ContainerID: emptyContainerID,
				}},
			},
			// If the init container is terminated with exit code 0, it won't be restarted even when the
			// restart policy is RestartAlways.
			expectedInitState: map[string]api.ContainerState{
				"succeed": {Terminated: &api.ContainerStateTerminated{
					ExitCode:    0,
					ContainerID: emptyContainerID,
				}},
				"failed": {Waiting: &api.ContainerStateWaiting{Reason: testErrorReason.Error()}},
			},
			expectedInitLastTerminationState: map[string]api.ContainerState{
				"succeed": {Terminated: &api.ContainerStateTerminated{
					ExitCode:    2,
					ContainerID: emptyContainerID,
				}},
				"failed": {Terminated: &api.ContainerStateTerminated{
					ExitCode:    1,
					ContainerID: emptyContainerID,
				}},
			},
		},
	} {
		pod.Spec.RestartPolicy = test.restartPolicy
		// Test normal containers
		pod.Spec.Containers = containers
		apiStatus := kubelet.generateAPIPodStatus(pod, podStatus)
		expectedState, expectedLastTerminationState := test.expectedState, test.expectedLastTerminationState
		assert.NoError(t, verifyContainerStatuses(apiStatus.ContainerStatuses, expectedState, expectedLastTerminationState), "case %d", c)
		pod.Spec.Containers = nil

		// Test init containers
		pod.Spec.InitContainers = containers
		apiStatus = kubelet.generateAPIPodStatus(pod, podStatus)
		if test.expectedInitState != nil {
			expectedState = test.expectedInitState
		}
		if test.expectedInitLastTerminationState != nil {
			expectedLastTerminationState = test.expectedInitLastTerminationState
		}
		assert.NoError(t, verifyContainerStatuses(apiStatus.InitContainerStatuses, expectedState, expectedLastTerminationState), "case %d", c)
		pod.Spec.InitContainers = nil
	}
}

func TestMakePortMappings(t *testing.T) {
	port := func(name string, protocol api.Protocol, containerPort, hostPort int32, ip string) api.ContainerPort {
		return api.ContainerPort{
			Name:          name,
			Protocol:      protocol,
			ContainerPort: containerPort,
			HostPort:      hostPort,
			HostIP:        ip,
		}
	}
	portMapping := func(name string, protocol api.Protocol, containerPort, hostPort int, ip string) kubecontainer.PortMapping {
		return kubecontainer.PortMapping{
			Name:          name,
			Protocol:      protocol,
			ContainerPort: containerPort,
			HostPort:      hostPort,
			HostIP:        ip,
		}
	}

	tests := []struct {
		container            *api.Container
		expectedPortMappings []kubecontainer.PortMapping
	}{
		{
			&api.Container{
				Name: "fooContainer",
				Ports: []api.ContainerPort{
					port("", api.ProtocolTCP, 80, 8080, "127.0.0.1"),
					port("", api.ProtocolTCP, 443, 4343, "192.168.0.1"),
					port("foo", api.ProtocolUDP, 555, 5555, ""),
					// Duplicated, should be ignored.
					port("foo", api.ProtocolUDP, 888, 8888, ""),
					// Duplicated, should be ignored.
					port("", api.ProtocolTCP, 80, 8888, ""),
				},
			},
			[]kubecontainer.PortMapping{
				portMapping("fooContainer-TCP:80", api.ProtocolTCP, 80, 8080, "127.0.0.1"),
				portMapping("fooContainer-TCP:443", api.ProtocolTCP, 443, 4343, "192.168.0.1"),
				portMapping("fooContainer-foo", api.ProtocolUDP, 555, 5555, ""),
			},
		},
	}

	for i, tt := range tests {
		actual := makePortMappings(tt.container)
		if !reflect.DeepEqual(tt.expectedPortMappings, actual) {
			t.Errorf("%d: Expected: %#v, saw: %#v", i, tt.expectedPortMappings, actual)
		}
	}
}

// TestGenerateAPIPodStatusInvokesPodSyncHandlers invokes the handlers and reports the proper status
func TestGenerateAPIPodStatusInvokesPodSyncHandlers(t *testing.T) {
	testKubelet := newTestKubelet(t, false /* controllerAttachDetachEnabled */)
	kubelet := testKubelet.kubelet
	pod := newTestPods(1)[0]
	podsToEvict := []*api.Pod{pod}
	kubelet.AddPodSyncHandler(&testPodSyncHandler{podsToEvict, "Evicted", "because"})
	status := &kubecontainer.PodStatus{
		ID:        pod.UID,
		Name:      pod.Name,
		Namespace: pod.Namespace,
	}
	apiStatus := kubelet.generateAPIPodStatus(pod, status)
	if apiStatus.Phase != api.PodFailed {
		t.Fatalf("Expected phase %v, but got %v", api.PodFailed, apiStatus.Phase)
	}
	if apiStatus.Reason != "Evicted" {
		t.Fatalf("Expected reason %v, but got %v", "Evicted", apiStatus.Reason)
	}
	if apiStatus.Message != "because" {
		t.Fatalf("Expected message %v, but got %v", "because", apiStatus.Message)
	}
}
