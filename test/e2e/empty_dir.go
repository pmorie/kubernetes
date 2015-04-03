/*
Copyright 2015 Google Inc. All rights reserved.

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
	"github.com/GoogleCloudPlatform/kubernetes/pkg/api"
	"github.com/GoogleCloudPlatform/kubernetes/pkg/client"
	"github.com/GoogleCloudPlatform/kubernetes/pkg/util"

	. "github.com/onsi/ginkgo"
)

var _ = Describe("emptyDir", func() {
	var (
		c         *client.Client
		podClient client.PodInterface
	)

	BeforeEach(func() {
		var err error
		c, err = loadClient()
		expectNoError(err)

		podClient = c.Pods(api.NamespaceDefault)
	})

	It("should support tmpfs", func() {
		path := "/testvol"
		source := &api.EmptyDirVolumeSource{
			Medium: api.StorageTypeMemory,
		}
		pod := testPodWithVolume(path, source)

		pod.Spec.Containers[0].Args = []string{
			fmt.Sprintf("--fs_type=%q", path),
			fmt.Sprintf("--file_mode=%q", path),
		}

		testContainerOutput("tmpfs mount for emptydir", c, pod, []string{
			"mount type: tmpfs",
			"mode: drwxr-xr-x",
		})
	})

	It("should be readable and writeable if we requested R/W on tmpfs", func() {
		path := "/testvol"
		filepath := fmt.Sprintf("%q/testfile", path)
		source := &api.EmptyDirVolumeSource{
			Medium: api.StorageTypeMemory,
		}
		pod := testPodWithVolume(path, source)

		pod.Spec.Containers[0].Args = []string{
			fmt.Sprintf("--fs_type=%q", path),
			fmt.Sprintf("--file_mode=%q", filepath),
			fmt.Sprintf("--rw_new_file=%q", filepath),
		}
		testContainerOutput("tmpfs R/W for emptydir", c, pod, []string{
			"mount type: tmpfs",
			"mode: drwxr-xr-x",
			fmt.Sprintf("content of file %q: mount-tester new file", filepath),
		})
	})
})

const containerName = "test-container"
const volumeName = "test-volume"

func testPodWithVolume(path string, source *api.EmptyDirVolumeSource) *api.Pod {
	podName := "pod-" + string(util.NewUUID())

	return &api.Pod{
		TypeMeta: api.TypeMeta{
			Kind:       "Pod",
			APIVersion: "v1beta1",
		},
		ObjectMeta: api.ObjectMeta{
			Name: podName,
		},
		Spec: api.PodSpec{
			Containers: []api.Container{
				{
					Name:  containerName,
					Image: "kubernetes/mounttest:0.1",
					VolumeMounts: []api.VolumeMount{
						{
							Name:      volumeName,
							MountPath: path,
						},
					},
				},
			},
			Volumes: []api.Volume{
				{
					Name: volumeName,
					VolumeSource: api.VolumeSource{
						EmptyDir: source,
					},
				},
			},
		},
	}
}
