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

package service

import (
	"testing"

	"k8s.io/kubernetes/pkg/api"
)

func TestGetNodeConditionPredicate(t *testing.T) {
	tests := []struct {
		node         api.Node
		expectAccept bool
		name         string
	}{
		{
			node:         api.Node{},
			expectAccept: false,
			name:         "empty",
		},
		{
			node: api.Node{
				Status: api.NodeStatus{
					Conditions: []api.NodeCondition{
						{Type: api.NodeReady, Status: api.ConditionTrue},
					},
				},
			},
			expectAccept: true,
			name:         "basic",
		},
		{
			node: api.Node{
				Spec: api.NodeSpec{Unschedulable: true},
				Status: api.NodeStatus{
					Conditions: []api.NodeCondition{
						{Type: api.NodeReady, Status: api.ConditionTrue},
					},
				},
			},
			expectAccept: false,
			name:         "unschedulable",
		},
	}
	pred := getNodeConditionPredicate()
	for _, test := range tests {
		accept := pred(&test.node)
		if accept != test.expectAccept {
			t.Errorf("Test failed for %s, expected %v, saw %v", test.name, test.expectAccept, accept)
		}
	}
}
