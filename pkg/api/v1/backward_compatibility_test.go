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

package v1_test

import (
	"flag"
	"testing"

	"k8s.io/kubernetes/pkg/api"
	"k8s.io/kubernetes/pkg/api/testutil"
	"k8s.io/kubernetes/pkg/api/validation"
	"k8s.io/kubernetes/pkg/capabilities"
	"k8s.io/kubernetes/pkg/runtime"
	"k8s.io/kubernetes/pkg/util/fielderrors"
)

func init() {
	flag.Set("-v", "vv")
}

func TestCompatibility_v1_PodSecurityContext_ContainerDefaults(t *testing.T) {
	capabilities.SetForTests(capabilities.Capabilities{AllowPrivileged: true})

	cases := []struct {
		name         string
		input        string
		expectedKeys map[string]string
		absentKeys   []string
	}{
		// Simple scenarios
		{
			name: "no container defaults",
			input: `
{
	"kind":"Pod",
	"apiVersion":"v1",
	"metadata":{"name":"my-pod-name", "namespace":"my-pod-namespace"},
	"spec": {
		"containers":[{
			"name":"a",
			"image":"my-container-image"
		}]
	}
}
`,
			expectedKeys: nil,
			absentKeys: []string{
				"spec.securityContext.ContainerDefaults.capabilities",
				"spec.containers[0].securityContext.capabilities",
			},
		},
		{
			name: "only container defaults set",
			input: `
{
	"kind":"Pod",
	"apiVersion":"v1",
	"metadata":{"name":"my-pod-name", "namespace":"my-pod-namespace"},
	"spec": {
		"securityContext": {
			"containerDefaults": {
				"runAsUser": 1001,
				"seLinuxOptions": {
					"user": "user_u",
					"role": "role_r",
					"type": "type_t",
					"level": "s0:c1,c2"
				}
			}
		},
		"containers":[{
			"name":"a",
			"image":"my-container-image"
		},{
			"name":"b",
			"image":"my-container-image"
		}]
	}
}
`,
			expectedKeys: map[string]string{
				"spec.securityContext.containerDefaults.runAsUser":            "1001",
				"spec.securityContext.containerDefaults.seLinuxOptions.user":  "user_u",
				"spec.securityContext.containerDefaults.seLinuxOptions.role":  "role_r",
				"spec.securityContext.containerDefaults.seLinuxOptions.type":  "type_t",
				"spec.securityContext.containerDefaults.seLinuxOptions.level": "s0:c1,c2",
				"spec.containers[0].securityContext.runAsUser":                "1001",
				"spec.containers[0].securityContext.seLinuxOptions.user":      "user_u",
				"spec.containers[0].securityContext.seLinuxOptions.role":      "role_r",
				"spec.containers[0].securityContext.seLinuxOptions.type":      "type_t",
				"spec.containers[0].securityContext.seLinuxOptions.level":     "s0:c1,c2",
				"spec.containers[1].securityContext.runAsUser":                "1001",
				"spec.containers[1].securityContext.seLinuxOptions.user":      "user_u",
				"spec.containers[1].securityContext.seLinuxOptions.role":      "role_r",
				"spec.containers[1].securityContext.seLinuxOptions.type":      "type_t",
				"spec.containers[1].securityContext.seLinuxOptions.level":     "s0:c1,c2",
			},
		},
		// Overlay tests
		{
			name: "overlay selinux options",
			input: `
{
	"kind":"Pod",
	"apiVersion":"v1",
	"metadata":{"name":"my-pod-name", "namespace":"my-pod-namespace"},
	"spec": {
		"securityContext": {
			"containerDefaults": {
				"seLinuxOptions": {
					"user": "user_u",
					"role": "role_r",
					"type": "type_t",
					"level": "s0:c1,c2"
				}
			}
		},
		"containers":[{
			"name":"a",
			"image":"my-container-image",
			"securityContext": {
				"seLinuxOptions": {
					"user": "user_2_u",
					"role": "role_2_r",
					"type": "type_2_t",
					"level": "s1:c3,c4"
				}
			}
		},{
			"name":"b",
			"image":"my-container-image"
		}]
	}
}
`,
			expectedKeys: map[string]string{
				"spec.securityContext.containerDefaults.seLinuxOptions.user":  "user_u",
				"spec.securityContext.containerDefaults.seLinuxOptions.role":  "role_r",
				"spec.securityContext.containerDefaults.seLinuxOptions.type":  "type_t",
				"spec.securityContext.containerDefaults.seLinuxOptions.level": "s0:c1,c2",
				"spec.containers[0].securityContext.seLinuxOptions.user":      "user_2_u",
				"spec.containers[0].securityContext.seLinuxOptions.role":      "role_2_r",
				"spec.containers[0].securityContext.seLinuxOptions.type":      "type_2_t",
				"spec.containers[0].securityContext.seLinuxOptions.level":     "s1:c3,c4",
				"spec.containers[1].securityContext.seLinuxOptions.user":      "user_u",
				"spec.containers[1].securityContext.seLinuxOptions.role":      "role_r",
				"spec.containers[1].securityContext.seLinuxOptions.type":      "type_t",
				"spec.containers[1].securityContext.seLinuxOptions.level":     "s0:c1,c2",
			},
		},
		{
			name: "overlay selinux options when containers partially specify",
			input: `
{
	"kind":"Pod",
	"apiVersion":"v1",
	"metadata":{"name":"my-pod-name", "namespace":"my-pod-namespace"},
	"spec": {
		"securityContext": {
			"containerDefaults": {
				"seLinuxOptions": {
					"user": "user_u",
					"role": "role_r",
					"type": "type_t",
					"level": "s0:c1,c2"
				}
			}
		},
		"containers":[{
			"name":"a",
			"image":"my-container-image",
			"securityContext": {
				"seLinuxOptions": {
					"type": "type_2_t",
					"level": "s1:c3,c4"
				}
			}
		},{
			"name":"b",
			"image":"my-container-image"
		}]
	}
}
`,
			expectedKeys: map[string]string{
				"spec.securityContext.containerDefaults.seLinuxOptions.user":  "user_u",
				"spec.securityContext.containerDefaults.seLinuxOptions.role":  "role_r",
				"spec.securityContext.containerDefaults.seLinuxOptions.type":  "type_t",
				"spec.securityContext.containerDefaults.seLinuxOptions.level": "s0:c1,c2",
				"spec.containers[0].securityContext.seLinuxOptions.type":      "type_2_t",
				"spec.containers[0].securityContext.seLinuxOptions.level":     "s1:c3,c4",
				"spec.containers[1].securityContext.seLinuxOptions.user":      "user_u",
				"spec.containers[1].securityContext.seLinuxOptions.role":      "role_r",
				"spec.containers[1].securityContext.seLinuxOptions.type":      "type_t",
				"spec.containers[1].securityContext.seLinuxOptions.level":     "s0:c1,c2",
			},
			absentKeys: []string{
				"spec.containers[0].securityContext.seLinuxOptions.user",
				"spec.containers[0].securityContext.seLinuxOptions.role",
			},
		},
		{
			name: "overlay runAsUser",
			input: `
{
	"kind":"Pod",
	"apiVersion":"v1",
	"metadata":{"name":"my-pod-name", "namespace":"my-pod-namespace"},
	"spec": {
		"securityContext": {
			"containerDefaults": {
				"runAsUser": 1001
			}
		},
		"containers":[{
			"name":"a",
			"image":"my-container-image",
			"securityContext": {
				"runAsUser": 1002
			}
		},{
			"name":"b",
			"image":"my-container-image",
			"securityContext": {
				"runAsNonRoot": true
			}
		}]
	}
}
`,
			expectedKeys: map[string]string{
				"spec.securityContext.containerDefaults.runAsUser": "1001",
				"spec.containers[0].securityContext.runAsUser":     "1002",
				"spec.containers[1].securityContext.runAsUser":     "1001",
				"spec.containers[1].securityContext.runAsNonRoot":  "true",
			},
		},
		{
			name: "overlay privileged",
			input: `
{
	"kind":"Pod",
	"apiVersion":"v1",
	"metadata":{"name":"my-pod-name", "namespace":"my-pod-namespace"},
	"spec": {
		"securityContext": {
			"containerDefaults": {
				"privileged": true
			}
		},
		"containers":[{
			"name":"a",
			"image":"my-container-image",
			"securityContext": {
				"privileged": false
			}
		},{
			"name":"b",
			"image":"my-container-image"
		}]
	}
}
`,
			expectedKeys: map[string]string{
				"spec.securityContext.containerDefaults.privileged": "true",
				"spec.containers[0].securityContext.privileged":     "false",
				"spec.containers[1].securityContext.privileged":     "true",
			},
		},
		{
			name: "overlay runAsNonRoot",
			input: `
{
	"kind":"Pod",
	"apiVersion":"v1",
	"metadata":{"name":"my-pod-name", "namespace":"my-pod-namespace"},
	"spec": {
		"securityContext": {
			"containerDefaults": {
				"runAsNonRoot": true
			}
		},
		"containers":[{
			"name":"a",
			"image":"my-container-image",
			"securityContext": {
				"runAsUser": 1002
			}
		}]
	}
}
`,
			expectedKeys: map[string]string{
				"spec.securityContext.containerDefaults.runAsNonRoot": "true",
				"spec.containers[0].securityContext.runAsUser":        "1002",
				"spec.containers[0].securityContext.runAsNonRoot":     "true",
			},
		},
		{
			name: "overlay capabilities",
			input: `
{
	"kind":"Pod",
	"apiVersion":"v1",
	"metadata":{"name":"my-pod-name", "namespace":"my-pod-namespace"},
	"spec": {
		"securityContext": {
			"containerDefaults": {
				"capabilities": {
					"add": [
						"FOWNER",
						"SYSADMIN"
					],
					"drop": [
						"MKNOD",
						"CHOWN"
					]
				}
			}
		},
		"containers":[{
			"name":"a",
			"image":"my-container-image",
			"securityContext": {
				"runAsUser": 1002
			}
		},{
			"name":"b",
			"image":"my-container-image",
			"securityContext": {
				"runAsUser": 1003,
				"capabilities": {
					"add": [
						"MKNOD",
						"CHOWN"
					],
					"drop": [
						"FOWNER",
						"SYSADMIN"
					]
				}
			}
		}]
	}
}
`,
			expectedKeys: map[string]string{
				"spec.securityContext.containerDefaults.capabilities.add[0]":  "FOWNER",
				"spec.securityContext.containerDefaults.capabilities.add[1]":  "SYSADMIN",
				"spec.securityContext.containerDefaults.capabilities.drop[0]": "MKNOD",
				"spec.securityContext.containerDefaults.capabilities.drop[1]": "CHOWN",
				"spec.containers[0].securityContext.capabilities.add[0]":      "FOWNER",
				"spec.containers[0].securityContext.capabilities.add[1]":      "SYSADMIN",
				"spec.containers[0].securityContext.capabilities.drop[0]":     "MKNOD",
				"spec.containers[0].securityContext.capabilities.drop[1]":     "CHOWN",
				"spec.containers[1].securityContext.capabilities.add[0]":      "MKNOD",
				"spec.containers[1].securityContext.capabilities.add[1]":      "CHOWN",
				"spec.containers[1].securityContext.capabilities.drop[0]":     "FOWNER",
				"spec.containers[1].securityContext.capabilities.drop[1]":     "SYSADMIN",
			},
		},
		// Default capabilities tests
		{
			name: "determine default capabilities - single container",
			input: `
{
	"kind":"Pod",
	"apiVersion":"v1",
	"metadata":{"name":"my-pod-name", "namespace":"my-pod-namespace"},
	"spec": {
		"containers":[{
			"name":"a",
			"image":"my-container-image",
			"securityContext": {
				"capabilities": {
					"add": [
						"FOWNER",
						"SYSADMIN"
					],
					"drop": [
						"MKNOD",
						"CHOWN"
					]
				}
			}
		}]
	}
}
`,
			expectedKeys: map[string]string{
				"spec.securityContext.containerDefaults.capabilities.add[0]":  "FOWNER",
				"spec.securityContext.containerDefaults.capabilities.add[1]":  "SYSADMIN",
				"spec.securityContext.containerDefaults.capabilities.drop[0]": "MKNOD",
				"spec.securityContext.containerDefaults.capabilities.drop[1]": "CHOWN",
				"spec.containers[0].securityContext.capabilities.add[0]":      "FOWNER",
				"spec.containers[0].securityContext.capabilities.add[1]":      "SYSADMIN",
				"spec.containers[0].securityContext.capabilities.drop[0]":     "MKNOD",
				"spec.containers[0].securityContext.capabilities.drop[1]":     "CHOWN",
			},
		},
		{
			name: "determine default capabilities - multiple containers positive",
			input: `
{
	"kind":"Pod",
	"apiVersion":"v1",
	"metadata":{"name":"my-pod-name", "namespace":"my-pod-namespace"},
	"spec": {
		"containers":[{
			"name":"a",
			"image":"my-container-image",
			"securityContext": {
				"capabilities": {
					"add": [
						"FOWNER",
						"SYSADMIN"
					],
					"drop": [
						"MKNOD",
						"CHOWN"
					]
				}
			}
		},{
			"name":"b",
			"image":"my-container-image",
			"securityContext": {
				"capabilities": {
					"add": [
						"FOWNER",
						"SYSADMIN"
					],
					"drop": [
						"MKNOD",
						"CHOWN"
					]
				}
			}
		}]
	}
}
`,
			expectedKeys: map[string]string{
				"spec.securityContext.containerDefaults.capabilities.add[0]":  "FOWNER",
				"spec.securityContext.containerDefaults.capabilities.add[1]":  "SYSADMIN",
				"spec.securityContext.containerDefaults.capabilities.drop[0]": "MKNOD",
				"spec.securityContext.containerDefaults.capabilities.drop[1]": "CHOWN",
				"spec.containers[0].securityContext.capabilities.add[0]":      "FOWNER",
				"spec.containers[0].securityContext.capabilities.add[1]":      "SYSADMIN",
				"spec.containers[0].securityContext.capabilities.drop[0]":     "MKNOD",
				"spec.containers[0].securityContext.capabilities.drop[1]":     "CHOWN",
				"spec.containers[1].securityContext.capabilities.add[0]":      "FOWNER",
				"spec.containers[1].securityContext.capabilities.add[1]":      "SYSADMIN",
				"spec.containers[1].securityContext.capabilities.drop[0]":     "MKNOD",
				"spec.containers[1].securityContext.capabilities.drop[1]":     "CHOWN",
			},
		},
		{
			name: "determine default capabilities - multiple containers negative",
			input: `
{
	"kind":"Pod",
	"apiVersion":"v1",
	"metadata":{"name":"my-pod-name", "namespace":"my-pod-namespace"},
	"spec": {
		"containers":[{
			"name":"a",
			"image":"my-container-image",
			"securityContext": {
				"capabilities": {
					"add": [
						"FOWNER",
						"SYSADMIN"
					],
					"drop": [
						"MKNOD",
						"CHOWN"
					]
				}
			}
		},{
			"name":"b",
			"image":"my-container-image",
			"securityContext": {
				"capabilities": {
					"add": [
						"FOWNER"
					],
					"drop": [
						"CHOWN"
					]
				}
			}
		}]
	}
}
`,
			expectedKeys: map[string]string{
				"spec.containers[0].securityContext.capabilities.add[0]":  "FOWNER",
				"spec.containers[0].securityContext.capabilities.add[1]":  "SYSADMIN",
				"spec.containers[0].securityContext.capabilities.drop[0]": "MKNOD",
				"spec.containers[0].securityContext.capabilities.drop[1]": "CHOWN",
				"spec.containers[1].securityContext.capabilities.add[0]":  "FOWNER",
				"spec.containers[1].securityContext.capabilities.drop[0]": "CHOWN",
			},
			absentKeys: []string{
				"spec.securityContext.ContainerDefaults.capabilities",
				"spec.containers[1].securityContext.capabilities.add[1]",
				"spec.containers[1].securityContext.capabilities.drop[1]",
			},
		},
		// Default privileged tests
		{
			name: "determine default privileged - single container",
			input: `
{
	"kind":"Pod",
	"apiVersion":"v1",
	"metadata":{"name":"my-pod-name", "namespace":"my-pod-namespace"},
	"spec": {
		"containers":[{
			"name":"a",
			"image":"my-container-image",
			"securityContext": {
				"privileged": true
			}
		}]
	}
}
`,
			expectedKeys: map[string]string{
				"spec.securityContext.containerDefaults.privileged": "true",
				"spec.containers[0].securityContext.privileged":     "true",
			},
		},
		{
			name: "determine default privileged - multiple containers",
			input: `
{
	"kind":"Pod",
	"apiVersion":"v1",
	"metadata":{"name":"my-pod-name", "namespace":"my-pod-namespace"},
	"spec": {
		"containers":[{
			"name":"a",
			"image":"my-container-image",
			"securityContext": {
				"privileged": true
			}
		},{
			"name":"b",
			"image":"my-container-image",
			"securityContext": {
				"privileged": true
			}
		}]
	}
}
`,
			expectedKeys: map[string]string{
				"spec.securityContext.containerDefaults.privileged": "true",
				"spec.containers[0].securityContext.privileged":     "true",
				"spec.containers[1].securityContext.privileged":     "true",
			},
		},
		{
			name: "determine default privileged - multiple containers negative",
			input: `
{
	"kind":"Pod",
	"apiVersion":"v1",
	"metadata":{"name":"my-pod-name", "namespace":"my-pod-namespace"},
	"spec": {
		"containers":[{
			"name":"a",
			"image":"my-container-image",
			"securityContext": {
				"privileged": true
			}
		},{
			"name":"b",
			"image":"my-container-image"
		}]
	}
}
`,
			expectedKeys: map[string]string{
				"spec.containers[0].securityContext.privileged": "true",
			},
			absentKeys: []string{
				"spec.securityContext.containerDefaults.privileged",
				"spec.containers[1].securityContext.privileged",
			},
		},
		// Default SELinuxOptions tests
		{
			name: "determine default seLinuxOptions - single container",
			input: `
{
	"kind":"Pod",
	"apiVersion":"v1",
	"metadata":{"name":"my-pod-name", "namespace":"my-pod-namespace"},
	"spec": {
		"containers":[{
			"name":"a",
			"image":"my-container-image",
			"securityContext": {
				"seLinuxOptions": {
					"user": "user_u",
					"role": "role_r",
					"type": "type_t",
					"level": "s0:c1,c2"
				}
			}
		}]
	}
}
`,
			expectedKeys: map[string]string{
				"spec.securityContext.containerDefaults.seLinuxOptions.user":  "user_u",
				"spec.securityContext.containerDefaults.seLinuxOptions.role":  "role_r",
				"spec.securityContext.containerDefaults.seLinuxOptions.type":  "type_t",
				"spec.securityContext.containerDefaults.seLinuxOptions.level": "s0:c1,c2",
				"spec.containers[0].securityContext.seLinuxOptions.user":      "user_u",
				"spec.containers[0].securityContext.seLinuxOptions.role":      "role_r",
				"spec.containers[0].securityContext.seLinuxOptions.type":      "type_t",
				"spec.containers[0].securityContext.seLinuxOptions.level":     "s0:c1,c2",
			},
		},
		{
			name: "determine default seLinuxOptions - multiple containers",
			input: `
{
	"kind":"Pod",
	"apiVersion":"v1",
	"metadata":{"name":"my-pod-name", "namespace":"my-pod-namespace"},
	"spec": {
		"containers":[{
			"name":"a",
			"image":"my-container-image",
			"securityContext": {
				"seLinuxOptions": {
					"user": "user_u",
					"role": "role_r",
					"type": "type_t",
					"level": "s0:c1,c2"
				}
			}
		},{
			"name":"b",
			"image":"my-container-image",
			"securityContext": {
				"seLinuxOptions": {
					"user": "user_u",
					"role": "role_r",
					"type": "type_t",
					"level": "s0:c1,c2"
				}
			}
		}]
	}
}
`,
			expectedKeys: map[string]string{
				"spec.securityContext.containerDefaults.seLinuxOptions.user":  "user_u",
				"spec.securityContext.containerDefaults.seLinuxOptions.role":  "role_r",
				"spec.securityContext.containerDefaults.seLinuxOptions.type":  "type_t",
				"spec.securityContext.containerDefaults.seLinuxOptions.level": "s0:c1,c2",
				"spec.containers[0].securityContext.seLinuxOptions.user":      "user_u",
				"spec.containers[0].securityContext.seLinuxOptions.role":      "role_r",
				"spec.containers[0].securityContext.seLinuxOptions.type":      "type_t",
				"spec.containers[0].securityContext.seLinuxOptions.level":     "s0:c1,c2",
				"spec.containers[1].securityContext.seLinuxOptions.user":      "user_u",
				"spec.containers[1].securityContext.seLinuxOptions.role":      "role_r",
				"spec.containers[1].securityContext.seLinuxOptions.type":      "type_t",
				"spec.containers[1].securityContext.seLinuxOptions.level":     "s0:c1,c2",
			},
		},
		{
			name: "determine default seLinuxOptions - multiple containers negative",
			input: `
{
	"kind":"Pod",
	"apiVersion":"v1",
	"metadata":{"name":"my-pod-name", "namespace":"my-pod-namespace"},
	"spec": {
		"containers":[{
			"name":"a",
			"image":"my-container-image",
			"securityContext": {
				"seLinuxOptions": {
					"user": "user_u",
					"role": "role_r",
					"type": "type_t",
					"level": "s0:c1,c2"
				}
			}
		},{
			"name":"b",
			"image":"my-container-image",
			"securityContext": {
				"seLinuxOptions": {
					"user": "user_u",
					"role": "role_r",
					"type": "type_2_t",
					"level": "s0:c1,c2"
				}
			}
		}]
	}
}
`,
			expectedKeys: map[string]string{
				"spec.containers[0].securityContext.seLinuxOptions.user":  "user_u",
				"spec.containers[0].securityContext.seLinuxOptions.role":  "role_r",
				"spec.containers[0].securityContext.seLinuxOptions.type":  "type_t",
				"spec.containers[0].securityContext.seLinuxOptions.level": "s0:c1,c2",
				"spec.containers[1].securityContext.seLinuxOptions.user":  "user_u",
				"spec.containers[1].securityContext.seLinuxOptions.role":  "role_r",
				"spec.containers[1].securityContext.seLinuxOptions.type":  "type_2_t",
				"spec.containers[1].securityContext.seLinuxOptions.level": "s0:c1,c2",
			},
			absentKeys: []string{
				"spec.securityContext.containerDefaults.seLinuxOptions.user",
				"spec.securityContext.containerDefaults.seLinuxOptions.role",
				"spec.securityContext.containerDefaults.seLinuxOptions.type",
				"spec.securityContext.containerDefaults.seLinuxOptions.level",
			},
		},

		// Default runAsUser tests
		{
			name: "determine default runAsUser - single container",
			input: `
{
	"kind":"Pod",
	"apiVersion":"v1",
	"metadata":{"name":"my-pod-name", "namespace":"my-pod-namespace"},
	"spec": {
		"containers":[{
			"name":"a",
			"image":"my-container-image",
			"securityContext": {
				"runAsUser": 1000
			}
		}]
	}
}
`,
			expectedKeys: map[string]string{
				"spec.securityContext.containerDefaults.runAsUser": "1000",
				"spec.containers[0].securityContext.runAsUser":     "1000",
			},
		},
		{
			name: "determine default runAsUser - multiple containers",
			input: `
{
	"kind":"Pod",
	"apiVersion":"v1",
	"metadata":{"name":"my-pod-name", "namespace":"my-pod-namespace"},
	"spec": {
		"containers":[{
			"name":"a",
			"image":"my-container-image",
			"securityContext": {
				"runAsUser": 1000
			}
		},{
			"name":"b",
			"image":"my-container-image",
			"securityContext": {
				"runAsUser": 1000
			}
		}]
	}
}
`,
			expectedKeys: map[string]string{
				"spec.securityContext.containerDefaults.runAsUser": "1000",
				"spec.containers[0].securityContext.runAsUser":     "1000",
				"spec.containers[1].securityContext.runAsUser":     "1000",
			},
		},
		{
			name: "determine default runAsUser - multiple containers negative",
			input: `
{
	"kind":"Pod",
	"apiVersion":"v1",
	"metadata":{"name":"my-pod-name", "namespace":"my-pod-namespace"},
	"spec": {
		"containers":[{
			"name":"a",
			"image":"my-container-image",
			"securityContext": {
				"runAsUser": 1000
			}
		},{
			"name":"b",
			"image":"my-container-image",
			"securityContext": {
				"runAsUser": 1001
			}
		}]
	}
}
`,
			expectedKeys: map[string]string{
				"spec.containers[0].securityContext.runAsUser": "1000",
				"spec.containers[1].securityContext.runAsUser": "1001",
			},
			absentKeys: []string{
				"spec.securityContext.containerDefaults.runAsUser",
			},
		},
		// Default RunAsNonRoot tests
		{
			name: "determine default runAsNonRoot - single container",
			input: `
{
	"kind":"Pod",
	"apiVersion":"v1",
	"metadata":{"name":"my-pod-name", "namespace":"my-pod-namespace"},
	"spec": {
		"containers":[{
			"name":"a",
			"image":"my-container-image",
			"securityContext": {
				"runAsNonRoot": true
			}
		}]
	}
}
`,
			expectedKeys: map[string]string{
				"spec.securityContext.containerDefaults.runAsNonRoot": "true",
				"spec.containers[0].securityContext.runAsNonRoot":     "true",
			},
		},
		{
			name: "determine default runAsNonRoot - multiple containers",
			input: `
{
	"kind":"Pod",
	"apiVersion":"v1",
	"metadata":{"name":"my-pod-name", "namespace":"my-pod-namespace"},
	"spec": {
		"containers":[{
			"name":"a",
			"image":"my-container-image",
			"securityContext": {
				"runAsNonRoot": true
			}
		},{
			"name":"b",
			"image":"my-container-image",
			"securityContext": {
				"runAsNonRoot": true
			}
		}]
	}
}
`,
			expectedKeys: map[string]string{
				"spec.securityContext.containerDefaults.runAsNonRoot": "true",
				"spec.containers[0].securityContext.runAsNonRoot":     "true",
				"spec.containers[1].securityContext.runAsNonRoot":     "true",
			},
		},
		{
			name: "determine default runAsNonRoot - multiple containers negative",
			input: `
{
	"kind":"Pod",
	"apiVersion":"v1",
	"metadata":{"name":"my-pod-name", "namespace":"my-pod-namespace"},
	"spec": {
		"containers":[{
			"name":"a",
			"image":"my-container-image",
			"securityContext": {
				"runAsNonRoot": true
			}
		},{
			"name":"b",
			"image":"my-container-image",
			"securityContext": {
				"runAsNonRoot": false
			}
		}]
	}
}
`,
			expectedKeys: map[string]string{
				"spec.containers[0].securityContext.runAsNonRoot": "true",
			},
			absentKeys: []string{
				"spec.containers[1].securityContext.runAsNonRoot",
			},
		},
	}

	validator := func(obj runtime.Object) fielderrors.ValidationErrorList {
		return validation.ValidatePodSpec(&(obj.(*api.Pod).Spec))
	}

	for _, tc := range cases {
		t.Logf("Testing 1.0.0 backward compatibility for PodSpec.SecurityContext.ContainerDefaults: %v", tc.name)
		testutil.TestCompatibility(t, "v1", []byte(tc.input), validator, tc.expectedKeys, tc.absentKeys)
	}
}

func TestCompatibility_v1_PodSecurityContext_HostNetwork(t *testing.T) {
	cases := []struct {
		name  string
		input string
	}{
		{
			name: "deprecated = 1",
			input: `
{
	"kind":"Pod",
	"apiVersion":"v1",
	"metadata":{"name":"my-pod-name", "namespace":"my-pod-namespace"},
	"spec": {
		"hostNetwork": true,
		"containers":[{
			"name":"a",
			"image":"my-container-image"
		}]
	}
}
`,
		},
		{
			name: "new = 1",
			input: `
{
	"kind":"Pod",
	"apiVersion":"v1",
	"metadata":{"name":"my-pod-name", "namespace":"my-pod-namespace"},
	"spec": {
		"securityContext": {
			"hostNetwork": true
		},
		"containers":[{
			"name":"a",
			"image":"my-container-image"
		}]
	}
}
`,
		},
		{
			name: "deprecated = 1, new = 0",
			input: `
{
	"kind":"Pod",
	"apiVersion":"v1",
	"metadata":{"name":"my-pod-name", "namespace":"my-pod-namespace"},
	"spec": {
		"hostNetwork": false,
		"securityContext": {
			"hostNetwork": true
		},
		"containers":[{
			"name":"a",
			"image":"my-container-image"
		}]
	}
}
`,
		},
		{
			name: "deprecated = 1, new = 1",
			input: `
{
	"kind":"Pod",
	"apiVersion":"v1",
	"metadata":{"name":"my-pod-name", "namespace":"my-pod-namespace"},
	"spec": {
		"hostNetwork": true,
		"securityContext": {
			"hostNetwork": true
		},
		"containers":[{
			"name":"a",
			"image":"my-container-image"
		}]
	}
}
`,
		},
	}

	validator := func(obj runtime.Object) fielderrors.ValidationErrorList {
		return validation.ValidatePodSpec(&(obj.(*api.Pod).Spec))
	}

	expectedFields := map[string]string{
		"spec.securityContext.hostNetwork": "true",
		"spec.hostNetwork":                 "true",
	}

	for _, tc := range cases {
		t.Logf("Testing 1.0.0 backward compatibility for PodSpec.HostNetwork: %v", tc.name)
		testutil.TestCompatibility(t, "v1", []byte(tc.input), validator, expectedFields, nil)
	}
}
