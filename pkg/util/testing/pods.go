/*
Copyright 2016 The Kubernetes Authors All rights reserved.

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

package testing

import (
	"k8s.io/kubernetes/pkg/api"
	"k8s.io/kubernetes/pkg/api/unversioned"
	"k8s.io/kubernetes/pkg/types"
)

// PodWithUidNameNs returns a pod with the given UID, name, and namespace.
// Use this function with literal string arguments such as:
//
//   PodWithUidNameNs("12345", "name", "namespace")
func PodWithUidNameNs(uid types.UID, name, namespace string) *api.Pod {
	return &api.Pod{
		TypeMeta: unversioned.TypeMeta{
			Kind: "Pod",
		},
		ObjectMeta: api.ObjectMeta{
			UID:         uid,
			Name:        name,
			Namespace:   namespace,
			Annotations: map[string]string{},
		},
	}
}

// PodWithUidNameNs returns a pod with the given UID, name, and namespace, and pod spec
// Use this function with literal string arguments such as:
//
//   PodWithUidNameNs("12345", "name", "namespace", api.PodSpec{})
func PodWithUidNameNsSpec(uid types.UID, name, namespace string, spec api.PodSpec) *api.Pod {
	pod := PodWithUidNameNs(uid, name, namespace)
	pod.Spec = spec
	return pod
}
