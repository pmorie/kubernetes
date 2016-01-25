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

package configmap

import (
	"fmt"
	"os"
	"path"

	"github.com/golang/glog"
	"k8s.io/kubernetes/pkg/api"
	"k8s.io/kubernetes/pkg/types"
	ioutil "k8s.io/kubernetes/pkg/util/io"
	"k8s.io/kubernetes/pkg/util/mount"
	"k8s.io/kubernetes/pkg/util/strings"
	"k8s.io/kubernetes/pkg/volume"
	volumeutil "k8s.io/kubernetes/pkg/volume/util"
)

// ProbeVolumePlugin is the entry point for plugin detection in a package.
func ProbeVolumePlugins() []volume.VolumePlugin {
	return []volume.VolumePlugin{&configMapPlugin{}}
}

const (
	configMapPluginName = "kubernetes.io/configmap"
)

// configMapPlugin implements the VolumePlugin interface.
type configMapPlugin struct {
	host volume.VolumeHost
}

var _ volume.VolumePlugin = &configMapPlugin{}

func (plugin *configMapPlugin) Init(host volume.VolumeHost) error {
	plugin.host = host
	return nil
}

func (plugin *configMapPlugin) Name() string {
	return configMapPluginName
}

func (plugin *configMapPlugin) CanSupport(spec *volume.Spec) bool {
	return spec.Volume != nil && spec.Volume.ConfigMap != nil
}

func (plugin *configMapPlugin) NewBuilder(spec *volume.Spec, pod *api.Pod, opts volume.VolumeOptions) (volume.Builder, error) {
	return &configMapVolumeBuilder{
		configMapVolume: &configMapVolume{spec.Name(), pod.UID, plugin, plugin.host.GetMounter(), plugin.host.GetWriter(), volume.MetricsNil{}},
		configMapName:   spec.Volume.ConfigMap.Name,
		pod:             *pod,
		opts:            &opts}, nil
}

func (plugin *configMapPlugin) NewCleaner(volName string, podUID types.UID) (volume.Cleaner, error) {
	return &configMapVolumeCleaner{&configMapVolume{volName, podUID, plugin, plugin.host.GetMounter(), plugin.host.GetWriter(), volume.MetricsNil{}}}, nil
}

type configMapVolume struct {
	volName string
	podUID  types.UID
	plugin  *configMapPlugin
	mounter mount.Interface
	writer  ioutil.Writer
	volume.MetricsNil
}

var _ volume.Volume = &configMapVolume{}

func (sv *configMapVolume) GetPath() string {
	return sv.plugin.host.GetPodVolumeDir(sv.podUID, strings.EscapeQualifiedNameForDisk(configMapPluginName), sv.volName)
}

// configMapVolumeBuilder handles retrieving secrets from the API server
// and placing them into the volume on the host.
type configMapVolumeBuilder struct {
	*configMapVolume

	configMapName string
	pod           api.Pod
	opts          *volume.VolumeOptions
}

var _ volume.Builder = &configMapVolumeBuilder{}

func (sv *configMapVolume) GetAttributes() volume.Attributes {
	return volume.Attributes{
		ReadOnly:        true,
		Managed:         true,
		SupportsSELinux: true,
	}
}

func (b *configMapVolumeBuilder) getMetaDir() string {
	return path.Join(b.plugin.host.GetPodPluginDir(b.podUID, strings.EscapeQualifiedNameForDisk(configMapPluginName)), b.volName)
}

// This is the spec for the volume that this plugin wraps.
var wrappedVolumeSpec = volume.Spec{
	Volume: &api.Volume{VolumeSource: api.VolumeSource{EmptyDir: &api.EmptyDirVolumeSource{Medium: api.StorageMediumMemory}}},
}

func (b *configMapVolumeBuilder) SetUp(fsGroup *int64) error {
	return b.SetUpAt(b.GetPath(), fsGroup)
}

func (b *configMapVolumeBuilder) SetUpAt(dir string, fsGroup *int64) error {
	notMnt, err := b.mounter.IsLikelyNotMountPoint(dir)
	// Getting an os.IsNotExist err from is a contingency; the directory
	// may not exist yet, in which case, setup should run.
	if err != nil && !os.IsNotExist(err) {
		return err
	}

	// If the readiness file is present for this volume and
	// the setup dir is a mountpoint, this volume is already ready.
	if volumeutil.IsReady(b.getMetaDir()) && !notMnt {
		return nil
	}

	glog.V(3).Infof("Setting up volume %v for pod %v at %v", b.volName, b.pod.UID, dir)

	// Wrap EmptyDir, let it do the setup.
	wrapped, err := b.plugin.host.NewWrapperBuilder(b.volName, wrappedVolumeSpec, &b.pod, *b.opts)
	if err != nil {
		return err
	}
	if err := wrapped.SetUpAt(dir, fsGroup); err != nil {
		return err
	}

	kubeClient := b.plugin.host.GetKubeClient()
	if kubeClient == nil {
		return fmt.Errorf("Cannot setup configMap volume %v because kube client is not configured", b.volName)
	}

	configMap, err := kubeClient.ConfigMaps(b.pod.Namespace).Get(b.configMapName)
	if err != nil {
		glog.Errorf("Couldn't get configMap %v/%v", b.pod.Namespace, b.configMapName)
		return err
	} else {
		totalBytes := totalBytes(configMap)
		glog.V(3).Infof("Received configMap %v/%v containing (%v) pieces of data, %v total bytes",
			b.pod.Namespace,
			b.configMapName,
			len(configMap.Data),
			totalBytes)
	}

	for name, data := range configMap.Data {
		hostFilePath := path.Join(dir, name)
		glog.V(3).Infof("Writing configMap data %v/%v/%v (%v bytes) to host file %v", b.pod.Namespace, b.configMapName, name, len(data), hostFilePath)
		err := b.writer.WriteFile(hostFilePath, []byte(data), 0444)
		if err != nil {
			glog.Errorf("Error writing configMap data to host path: %v, %v", hostFilePath, err)
			return err
		}
	}

	err = volume.SetVolumeOwnership(b, fsGroup)
	if err != nil {
		glog.Errorf("Error applying volume ownership settings for group: %v", fsGroup)
		return err
	}

	volumeutil.SetReady(b.getMetaDir())

	return nil
}

func totalBytes(configMap *api.ConfigMap) int {
	totalSize := 0
	for _, value := range configMap.Data {
		totalSize += len(value)
	}

	return totalSize
}

// configMapVolumeCleaner handles cleaning up configMap volumes.
type configMapVolumeCleaner struct {
	*configMapVolume
}

var _ volume.Cleaner = &configMapVolumeCleaner{}

func (c *configMapVolumeCleaner) TearDown() error {
	return c.TearDownAt(c.GetPath())
}

func (c *configMapVolumeCleaner) TearDownAt(dir string) error {
	glog.V(3).Infof("Tearing down volume %v for pod %v at %v", c.volName, c.podUID, dir)

	// Wrap EmptyDir, let it do the teardown.
	wrapped, err := c.plugin.host.NewWrapperCleaner(c.volName, wrappedVolumeSpec, c.podUID)
	if err != nil {
		return err
	}
	return wrapped.TearDownAt(dir)
}
