/*
Copyright 2014 Google Inc. All rights reserved.

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
	"time"

	"github.com/GoogleCloudPlatform/kubernetes/pkg/api"
	"github.com/GoogleCloudPlatform/kubernetes/pkg/client"
	"github.com/GoogleCloudPlatform/kubernetes/pkg/util"

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
)

var _ = Describe("Secrets", func() {
	var c *client.Client

	BeforeEach(func() {
		var err error
		c, err = loadClient()
		Expect(err).NotTo(HaveOccurred())
	})

	It("should be consumable from pods", func() {
		ns := api.NamespaceDefault
		name := "secret-test-" + string(util.NewUUID())
		volumeName := "secret-volume"
		volumeMountPath := "/etc/secret-volume"

		secret := &api.Secret{
			ObjectMeta: api.ObjectMeta{
				Namespace: ns,
				Name:      name,
			},
			Data: map[string][]byte{
				"data-1": []byte("value-1\n"),
				"data-2": []byte("value-2\n"),
				"data-3": []byte("value-3\n"),
			},
		}

		secret, err := c.Secrets(ns).Create(secret)
		By(fmt.Sprintf("Creating secret with name %s", secret.Name))
		if err != nil {
			Fail(fmt.Sprintf("unable to create test secret %s: %v", secret.Name, err))
		}

		// Clean up secret
		defer func() {
			defer GinkgoRecover()
			By("Cleaning up the secret")
			if err = c.Secrets(ns).Delete(secret.Name); err != nil {
				Fail(fmt.Sprintf("unable to delete secret %v: %v", secret.Name, err))
			}
		}()

		By(fmt.Sprintf("Creating a pod to consume secret %v", secret.Name))
		// Make a client pod that verifies that it has the service environment variables.
		clientName := "client-secrets-" + string(util.NewUUID())
		clientPod := &api.Pod{
			ObjectMeta: api.ObjectMeta{
				Name: clientName,
			},
			Spec: api.PodSpec{
				Volumes: []api.Volume{
					{
						Name: volumeName,
						Source: api.VolumeSource{
							Secret: &api.SecretSource{
								Target: api.ObjectReference{
									Kind:      "Secret",
									Namespace: ns,
									Name:      name,
								},
							},
						},
					},
				},
				Containers: []api.Container{
					{
						Name:  "catcont",
						Image: "busybox",
						// Command: []string{"cat", "/etc/nsswitch.conf"},
						Command: []string{"cat", "/etc/secret-volume/data-1"},
						VolumeMounts: []api.VolumeMount{
							{
								Name:      volumeName,
								MountPath: volumeMountPath,
								ReadOnly:  true,
							},
						},
					},
				},
				RestartPolicy: api.RestartPolicy{
					Never: &api.RestartPolicyNever{},
				},
			},
		}

		_, err = c.Pods(ns).Create(clientPod)
		if err != nil {
			Fail(fmt.Sprintf("Failed to create pod: %v", err))
		}
		defer func() {
			defer GinkgoRecover()
			c.Pods(ns).Delete(clientPod.Name)
		}()

		// Wait for client pod to complete.
		err = waitForPodSuccess(c, clientPod.Name, clientPod.Spec.Containers[0].Name, 60*time.Second)
		Expect(err).NotTo(HaveOccurred())

		// Grab its logs.  Get host first.
		clientPodStatus, err := c.Pods(api.NamespaceDefault).Get(clientPod.Name)
		if err != nil {
			Fail(fmt.Sprintf("Failed to get clientPod to know host: %v", err))
		}
		By(fmt.Sprintf("Trying to get logs from host %s pod %s container %s: %v",
			clientPodStatus.Status.Host, clientPodStatus.Name, clientPodStatus.Spec.Containers[0].Name, err))
		logs, err := c.Get().
			Prefix("proxy").
			Resource("minions").
			Name(clientPodStatus.Status.Host).
			Suffix("containerLogs", api.NamespaceDefault, clientPodStatus.Name, clientPodStatus.Spec.Containers[0].Name).
			Do().
			Raw()
		if err != nil {
			Fail(fmt.Sprintf("Failed to get logs from host %s pod %s container %s: %v",
				clientPodStatus.Status.Host, clientPodStatus.Name, clientPodStatus.Spec.Containers[0].Name, err))
		}
		fmt.Sprintf("clientPod logs:%v\n", string(logs))

		toFind := []string{
			"value-1",
		}

		for _, m := range toFind {
			Expect(string(logs)).To(ContainSubstring(m), "%q in secret data", m)
		}

		// We could try a wget the service from the client pod.  But services.sh e2e test covers that pretty well.
	})
})
