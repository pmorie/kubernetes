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

package labelselector

import (
	"fmt"
	"io"

	clientset "k8s.io/kubernetes/pkg/client/clientset_generated/internalclientset"

	"k8s.io/kubernetes/pkg/admission"
	"k8s.io/kubernetes/pkg/api"
	apierrors "k8s.io/kubernetes/pkg/api/errors"
)

func init() {
	admission.RegisterPlugin("PVSelector", func(client clientset.Interface, config io.Reader) (admission.Interface, error) {
		return NewSecurityContextDeny(client), nil
	})
}

// plugin contains the client used by the  admission controller
type plugin struct {
	*admission.Handler
	client clientset.Interface
}

func NewPVSelector(client clientset.Interface) admission.Interface {
	return &plugin{
		Handler: admission.NewHandler(admission.Create, admission.Update),
		client:  client,
	}
}

func (p *plugin) Admit(a admission.Attributes) (err error) {
	if a.GetResource().GroupResource() != api.Resource("persistentvolumeclaims") {
		return nil
	}

	pvc, ok := a.GetObject().(*api.PersistentVolumeClaim)
	if !ok {
		return apierrors.NewBadRequest("Resource was marked with kind PersistentVolumeClaim but was unable to be converted")
	}

	if pvc.Spec.Selector == nil {
		pvc.Spec.Selector = &unversioned.LabelSelector{
			MatchClaims: map[string]string{
				"general-availability-pool": "true",
			},
		}
	}

	// if pod.Spec.SecurityContext != nil && pod.Spec.SecurityContext.SupplementalGroups != nil {
	// 	return apierrors.NewForbidden(a.GetResource().GroupResource(), pod.Name, fmt.Errorf("SecurityContext.SupplementalGroups is forbidden"))
	// }
	// if pod.Spec.SecurityContext != nil {
	// 	if pod.Spec.SecurityContext.SELinuxOptions != nil {
	// 		return apierrors.NewForbidden(a.GetResource().GroupResource(), pod.Name, fmt.Errorf("pod.Spec.SecurityContext.SELinuxOptions is forbidden"))
	// 	}
	// 	if pod.Spec.SecurityContext.RunAsUser != nil {
	// 		return apierrors.NewForbidden(a.GetResource().GroupResource(), pod.Name, fmt.Errorf("pod.Spec.SecurityContext.RunAsUser is forbidden"))
	// 	}
	// }

	// if pod.Spec.SecurityContext != nil && pod.Spec.SecurityContext.FSGroup != nil {
	// 	return apierrors.NewForbidden(a.GetResource().GroupResource(), pod.Name, fmt.Errorf("SecurityContext.FSGroup is forbidden"))
	// }

	// for _, v := range pod.Spec.InitContainers {
	// 	if v.SecurityContext != nil {
	// 		if v.SecurityContext.SELinuxOptions != nil {
	// 			return apierrors.NewForbidden(a.GetResource().GroupResource(), pod.Name, fmt.Errorf("SecurityContext.SELinuxOptions is forbidden"))
	// 		}
	// 		if v.SecurityContext.RunAsUser != nil {
	// 			return apierrors.NewForbidden(a.GetResource().GroupResource(), pod.Name, fmt.Errorf("SecurityContext.RunAsUser is forbidden"))
	// 		}
	// 	}
	// }

	// for _, v := range pod.Spec.Containers {
	// 	if v.SecurityContext != nil {
	// 		if v.SecurityContext.SELinuxOptions != nil {
	// 			return apierrors.NewForbidden(a.GetResource().GroupResource(), pod.Name, fmt.Errorf("SecurityContext.SELinuxOptions is forbidden"))
	// 		}
	// 		if v.SecurityContext.RunAsUser != nil {
	// 			return apierrors.NewForbidden(a.GetResource().GroupResource(), pod.Name, fmt.Errorf("SecurityContext.RunAsUser is forbidden"))
	// 		}
	// 	}
	// }
	return nil
}
