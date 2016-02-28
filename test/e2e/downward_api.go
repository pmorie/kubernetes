/*
Copyright 2015 The Kubernetes Authors All rights reserved.

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

package e2e

import (
	"fmt"

	"k8s.io/kubernetes/pkg/api"
	"k8s.io/kubernetes/pkg/util"

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
)

var _ = Describe("Downward API", func() {
	framework := NewDefaultFramework("downward-api")

	It("should provide pod name and namespace as env vars [Conformance]", func() {
		podName := "downward-api-" + string(util.NewUUID())
		env := []api.EnvVar{
			{
				Name: "POD_NAME",
				ValueFrom: &api.EnvVarSource{
					FieldRef: &api.ObjectFieldSelector{
						APIVersion: "v1",
						FieldPath:  "metadata.name",
					},
				},
			},
			{
				Name: "POD_NAMESPACE",
				ValueFrom: &api.EnvVarSource{
					FieldRef: &api.ObjectFieldSelector{
						APIVersion: "v1",
						FieldPath:  "metadata.namespace",
					},
				},
			},
		}

		expectations := []string{
			fmt.Sprintf("POD_NAME=%v", podName),
			fmt.Sprintf("POD_NAMESPACE=%v", framework.Namespace.Name),
		}

		testDownwardAPI(framework, podName, env, expectations)
	})

	It("should provide pod IP as an env var", func() {
		podName := "downward-api-" + string(util.NewUUID())
		env := []api.EnvVar{
			{
				Name: "POD_IP",
				ValueFrom: &api.EnvVarSource{
					FieldRef: &api.ObjectFieldSelector{
						APIVersion: "v1",
						FieldPath:  "status.podIP",
					},
				},
			},
		}

		expectations := []string{
			"POD_IP=(?:\\d+)\\.(?:\\d+)\\.(?:\\d+)\\.(?:\\d+)",
		}

		testDownwardAPI(framework, podName, env, expectations)
	})

	It("new test", func() {
		var err error
		useProxySubresource, err := serverVersionGTE(subResourceServiceAndNodeProxyVersion, framework.Client)
		if err != nil {
			Failf("unexpected error determining whether server supports subproxy resource")
		}

		podName := "dapi-new-test-" + string(util.NewUUID())
		env := []api.EnvVar{
			{
				Name: "POD_IP",
				ValueFrom: &api.EnvVarSource{
					FieldRef: &api.ObjectFieldSelector{
						APIVersion: "v1",
						FieldPath:  "status.podIP",
					},
				},
			},
		}

		pod := createNetExecPodSpec(podName, framework.Namespace.Name)
		pod.Spec.Containers[0].Env = env
		_, err = framework.Client.Pods(framework.Namespace.Name).Create(pod)
		if err != nil {
			Failf("unexpected error creating netexec pod")
		}

		framework.WaitForPodRunning(podName)

		output := framework.RunShellCommandInNetexecContainer(podName, "env", useProxySubresource)
		Expect(output).To(MatchRegexp("POD_IP=(?:\\d+)\\.(?:\\d+)\\.(?:\\d+)\\.(?:\\d+)"), "%q in container output", output)

		// kill container in pod
		framework.ExitNetexecContainer(podName, 40, true)

		framework.WaitForPodReady(podName)

		output = framework.RunShellCommandInNetexecContainer(podName, "env", useProxySubresource)
		Expect(output).To(MatchRegexp("POD_IP=(?:\\d+)\\.(?:\\d+)\\.(?:\\d+)\\.(?:\\d+)"), "%q in container output", output)

	})
})

const (
	netexecContainerName = "netexec-container"
)

func createNetExecPodSpec(name, namespace string) *api.Pod {
	pod := &api.Pod{
		ObjectMeta: api.ObjectMeta{
			Name:      name,
			Namespace: namespace,
		},
		Spec: api.PodSpec{
			Containers: []api.Container{
				{
					Name:            netexecContainerName,
					Image:           netexecImageName,
					ImagePullPolicy: api.PullIfNotPresent,
					Command: []string{
						"/netexec",
						fmt.Sprintf("--http-port=%d", endpointHttpPort),
					},
					Ports: []api.ContainerPort{
						{
							Name:          "http",
							ContainerPort: endpointHttpPort,
						},
					},
				},
			},
		},
	}
	return pod
}

func testDownwardAPI(framework *Framework, podName string, env []api.EnvVar, expectations []string) {
	pod := &api.Pod{
		ObjectMeta: api.ObjectMeta{
			Name:   podName,
			Labels: map[string]string{"name": podName},
		},
		Spec: api.PodSpec{
			Containers: []api.Container{
				{
					Name:    "dapi-container",
					Image:   "gcr.io/google_containers/busybox:1.24",
					Command: []string{"sh", "-c", "env"},
					Env:     env,
				},
			},
			RestartPolicy: api.RestartPolicyNever,
		},
	}

	framework.TestContainerOutputRegexp("downward api env vars", pod, 0, expectations)
}
