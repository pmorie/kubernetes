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

package registry

import (
	"fmt"

	"code.google.com/p/go-uuid/uuid"
	"github.com/GoogleCloudPlatform/kubernetes/pkg/api"
	"github.com/GoogleCloudPlatform/kubernetes/pkg/apiserver"
	"github.com/GoogleCloudPlatform/kubernetes/pkg/labels"
)

// DeploymentRegistryStorage is an implementation of RESTStorage for the api server.
type DeploymentRegistryStorage struct {
	registry DeploymentRegistry
}

func NewDeploymentRegistryStorage(registry DeploymentRegistry) apiserver.RESTStorage {
	return &DeploymentRegistryStorage{
		registry: registry,
	}
}

// List obtains a list of Deployments that match selector.
func (storage *DeploymentRegistryStorage) List(selector labels.Selector) (interface{}, error) {
	result := api.DeploymentList{}
	deployments, err := storage.registry.ListDeployments()
	if err == nil {
		for _, deployment := range deployments {
			if selector.Matches(labels.Set(deployment.Labels)) {
				result.Items = append(result.Items, deployment)
			}
		}
	}
	return result, err
}

// Get obtains the Deployment specified by its id.
func (storage *DeploymentRegistryStorage) Get(id string) (interface{}, error) {
	deployment, err := storage.registry.GetDeployment(id)
	if err != nil {
		return nil, err
	}
	return deployment, err
}

// Delete asynchronously deletes the Deployment specified by its id.
func (storage *DeploymentRegistryStorage) Delete(id string) (<-chan interface{}, error) {
	return apiserver.MakeAsync(func() (interface{}, error) {
		return api.Status{Status: api.StatusSuccess}, storage.registry.DeleteDeployment(id)
	}), nil
}

// Extract deserializes user provided data into an api.Deployment.
func (storage *DeploymentRegistryStorage) Extract(body []byte) (interface{}, error) {
	result := api.Deployment{}
	err := api.DecodeInto(body, &result)
	return result, err
}

// Create registers a given new Deployment instance to storage.registry.
func (storage *DeploymentRegistryStorage) Create(obj interface{}) (<-chan interface{}, error) {
	deployment, ok := obj.(api.Deployment)
	if !ok {
		return nil, fmt.Errorf("not a deployment: %#v", obj)
	}
	if len(deployment.ID) == 0 {
		deployment.ID = uuid.NewUUID().String()
	}
	deployment.Status = api.DeploymentNew

	return apiserver.MakeAsync(func() (interface{}, error) {
		err := storage.registry.CreateDeployment(deployment)
		if err != nil {
			return nil, err
		}
		return deployment, nil
	}), nil
}

// Update replaces a given Deployment instance with an existing instance in storage.registry.
func (storage *DeploymentRegistryStorage) Update(obj interface{}) (<-chan interface{}, error) {
	deployment, ok := obj.(api.Deployment)
	if !ok {
		return nil, fmt.Errorf("not a replication deployment: %#v", obj)
	}
	if len(deployment.ID) == 0 {
		return nil, fmt.Errorf("ID should not be empty: %#v", deployment)
	}
	return apiserver.MakeAsync(func() (interface{}, error) {
		err := storage.registry.UpdateDeployment(deployment)
		if err != nil {
			return nil, err
		}
		return deployment, nil
	}), nil
}
