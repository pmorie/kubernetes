package secret

import (
	"os"

	"github.com/GoogleCloudPlatform/kubernetes/pkg/api"
	"github.com/GoogleCloudPlatform/kubernetes/pkg/kubelet/volume"
	"github.com/GoogleCloudPlatform/kubernetes/pkg/types"
)

// This is the primary entrypoint for volume plugins.
func ProbeVolumePlugins() []volume.Plugin {
	return []volume.Plugin{&secretPlugin{}}
}

const (
	secretPluginName = "kubernetes.io/secret"
)

type secretPlugin struct {
	host volume.Host
}

func (plugin *secretPlugin) Init(host volume.Host) {
	plugin.host = host
}

func (plugin *secretPlugin) Name() string {
	return secretPluginName
}

func (plugin *secretPlugin) CanSupport(spec *api.Volume) bool {
	if spec.Source.SecretSource != nil {
		return true
	}

	return false
}

func (plugin *secretPlugin) NewBuilder(spec *api.Volume, podUID types.UID) (volume.Builder, error) {
	return plugin.newBuilderInternal(spec, podUID)
}

func (plugin *secretPlugin) newBuilderInternal(spec *api.Volume, podUID types.UID) (volume.Builder, error) {
	return &secretVolume{spec.Name, podUID, plugin, &spec.Source.SecretSource.Target}, nil
}

func (plugin *secretPlugin) NewCleaner(volName string, podUID types.UID) (volume.Cleaner, error) {
	return plugin.newCleanerInternal(volName, podUID)
}

func (plugin *secretPlugin) newCleanerInternal(volName string, podUID types.UID) (volume.Cleaner, error) {
	return &secretVolume{volName, podUID, plugin, nil}, nil
}

type secretVolume struct {
	volName   string
	podUID    types.UID
	plugin    *secretPlugin
	secretRef *api.ObjectReference
}

func (sv *secretVolume) SetUp() error {
	path := sv.GetPath()
	err := os.MkdirAll(path, 0750)
	if err != nil {
		return err
	}

	// TODO: fetch secret data from API server

	return nil
}

func (sv *secretVolume) GetPath() string {
	return sv.plugin.host.GetPodVolumeDir(sv.podUID, volume.EscapePluginName(secretPluginName), sv.volName)
}

func (sv *secretVolume) TearDown() error {
	tmpDir, err := volume.RenameDirectory(sv.GetPath(), sv.volName+".deleting~")
	if err != nil {
		return err
	}
	err = os.RemoveAll(tmpDir)
	if err != nil {
		return err
	}
	return nil
}
