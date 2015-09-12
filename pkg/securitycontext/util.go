/*
Copyright 2014 The Kubernetes Authors All rights reserved.

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

package securitycontext

import (
	"fmt"
	"strings"

	"k8s.io/kubernetes/pkg/api"
)

// HasPrivilegedRequest returns the value of SecurityContext.Privileged, taking into account
// the possibility of nils
func HasPrivilegedRequest(container *api.Container) bool {
	if container.SecurityContext == nil {
		return false
	}
	if container.SecurityContext.Privileged == nil {
		return false
	}
	return *container.SecurityContext.Privileged
}

// HasCapabilitiesRequest returns true if Adds or Drops are defined in the security context
// capabilities, taking into account nils
func HasCapabilitiesRequest(container *api.Container) bool {
	if container.SecurityContext == nil {
		return false
	}
	if container.SecurityContext.Capabilities == nil {
		return false
	}
	return len(container.SecurityContext.Capabilities.Add) > 0 || len(container.SecurityContext.Capabilities.Drop) > 0
}

const expectedSELinuxContextFields = 4

// ParseSELinuxOptions parses a string containing a full SELinux context
// (user, role, type, and level) into an SELinuxOptions object.  If the
// context is malformed, an error is returned.
func ParseSELinuxOptions(context string) (*api.SELinuxOptions, error) {
	fields := strings.SplitN(context, ":", expectedSELinuxContextFields)

	if len(fields) != expectedSELinuxContextFields {
		return nil, fmt.Errorf("expected %v fields in selinuxcontext; got %v (context: %v)", expectedSELinuxContextFields, len(fields), context)
	}

	return &api.SELinuxOptions{
		User:  fields[0],
		Role:  fields[1],
		Type:  fields[2],
		Level: fields[3],
	}, nil
}

// HasNonRootUID returns true if the runAsUser is set and is greater than 0.
func HasRootUID(container *api.Container) bool {
	if container.SecurityContext == nil {
		return false
	}
	if container.SecurityContext.RunAsUser == nil {
		return false
	}
	return *container.SecurityContext.RunAsUser == 0
}

// HasRunAsUser determines if the sc's runAsUser field is set.
func HasRunAsUser(container *api.Container) bool {
	return container.SecurityContext != nil && container.SecurityContext.RunAsUser != nil
}

// HasRootRunAsUser returns true if the run as user is set and it is set to 0.
func HasRootRunAsUser(container *api.Container) bool {
	return HasRunAsUser(container) && HasRootUID(container)
}

// TODO: does this belong in another package?
func SynthesizeContainerDefaults(spec *api.PodSpec) *api.SecurityContext {
	var (
		tmpContext = new(api.SecurityContext)

		createDefaultCapabilities   = true
		createDefaultPrivileged     = true
		createDefaultSELinuxOptions = true
		createDefaultRunAsUser      = true
		createDefaultRunAsNonRoot   = true
	)

	for _, container := range spec.Containers {
		if container.SecurityContext == nil {
			return nil
		}

		if createDefaultCapabilities {
			if container.SecurityContext.Capabilities == nil {
				createDefaultCapabilities = false
				tmpContext.Capabilities = nil
			} else {
				if tmpContext.Capabilities == nil {
					// this has to be the first container in the cluster if tmpContext.Capabilities is unset
					tmpContext.Capabilities = new(api.Capabilities)
					*tmpContext.Capabilities = *container.SecurityContext.Capabilities
				} else {
					if api.Semantic.DeepEqual(tmpContext.Capabilities, container.SecurityContext.Capabilities) {
						// continue; keep looking
					} else {
						createDefaultCapabilities = false
						tmpContext.Capabilities = nil
					}
				}
			}
		}

		if createDefaultPrivileged {
			if container.SecurityContext.Privileged == nil {
				createDefaultPrivileged = false
				tmpContext.Privileged = nil
			} else {
				if tmpContext.Privileged == nil {
					tmpContext.Privileged = new(bool)
					*tmpContext.Privileged = *container.SecurityContext.Privileged
				} else {
					if api.Semantic.DeepEqual(tmpContext.Privileged, container.SecurityContext.Privileged) {
						// continue; keep looking
					} else {
						createDefaultPrivileged = false
						tmpContext.Privileged = nil
					}
				}
			}
		}

		if createDefaultSELinuxOptions {
			if container.SecurityContext.SELinuxOptions == nil {
				createDefaultSELinuxOptions = false
				tmpContext.SELinuxOptions = nil
			} else {
				if tmpContext.SELinuxOptions == nil {
					tmpContext.SELinuxOptions = new(api.SELinuxOptions)
					*tmpContext.SELinuxOptions = *container.SecurityContext.SELinuxOptions
				} else {
					if api.Semantic.DeepEqual(tmpContext.SELinuxOptions, container.SecurityContext.SELinuxOptions) {
						// continue
					} else {
						createDefaultSELinuxOptions = false
						tmpContext.SELinuxOptions = nil
					}
				}
			}
		}

		if createDefaultRunAsUser {
			if container.SecurityContext.RunAsUser == nil {
				createDefaultRunAsUser = false
				tmpContext.RunAsUser = nil
			} else {
				if tmpContext.RunAsUser == nil {
					tmpContext.RunAsUser = new(int64)
					*tmpContext.RunAsUser = *container.SecurityContext.RunAsUser
				} else {
					if api.Semantic.DeepEqual(tmpContext.RunAsUser, container.SecurityContext.RunAsUser) {
						// continue
					} else {
						createDefaultRunAsUser = false
						tmpContext.RunAsUser = nil
					}
				}
			}
		}

		if createDefaultRunAsNonRoot {
			if !container.SecurityContext.RunAsNonRoot {
				createDefaultRunAsNonRoot = false
				tmpContext.RunAsNonRoot = false
			} else {
				if tmpContext.RunAsNonRoot {
					// continue
				} else {
					tmpContext.RunAsNonRoot = true
				}
			}
		}
	}

	return tmpContext
}
