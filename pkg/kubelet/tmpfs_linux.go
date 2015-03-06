// +build linux

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

package kubelet

import (
	"fmt"
	"syscall"

	"github.com/docker/libcontainer/selinux"
)

const tmpfsMountFlags = uint(syscall.MS_NOEXEC | syscall.MS_NOSUID | syscall.MS_NODEV)

func (kl *kubelet) getTmpfsMountOptions() (string, error) {
	rootContext, err := selinux.GetFilecon(kl.getRootDir())
	if err != nil {
		return "", err	
	}

	return fmt.Sprintf("mode=0755,size=10g,rootcontext=\"%v\"", rootContext), nil
}
