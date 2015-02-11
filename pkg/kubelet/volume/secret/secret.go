package secret

import (
	"fmt"
	"io/ioutil"
	"os"
	"path"

	"github.com/GoogleCloudPlatform/kubernetes/pkg/api"
	"github.com/GoogleCloudPlatform/kubernetes/pkg/kubelet/volume"
	"github.com/GoogleCloudPlatform/kubernetes/pkg/types"
	"github.com/golang/glog"
)

// ProbeVolumePlugin is the entry point for plugin detection in a package
func ProbeVolumePlugins() []volume.Plugin {
	return []volume.Plugin{&secretPlugin{}}
}

const (
	secretPluginName = "kubernetes.io/secret"
)

// secretPlugin implements the VolumePlugin interface
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
	if spec.Source.Secret != nil {
		return true
	}

	return false
}

func (plugin *secretPlugin) NewBuilder(spec *api.Volume, podUID types.UID) (volume.Builder, error) {
	return plugin.newBuilderInternal(spec, podUID)
}

func (plugin *secretPlugin) newBuilderInternal(spec *api.Volume, podUID types.UID) (volume.Builder, error) {
	return &secretVolume{spec.Name, podUID, plugin, &spec.Source.Secret.Target}, nil
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
	hostPath := sv.GetPath()
	err := os.MkdirAll(hostPath, 0750)
	if err != nil {
		return err
	}

	kubeClient := sv.plugin.host.GetKubeClient()
	if kubeClient == nil {
		return fmt.Errorf("Cannot setup secret volume %v because kube client is not configured", sv)
	}

	secret, err := kubeClient.Secrets(sv.secretRef.Namespace).Get(sv.secretRef.Name)
	if err != nil {
		glog.Errorf("Couldn't get secret %v/%v", sv.secretRef.Namespace, sv.secretRef.Name)
		return err
	}

	for name, data := range secret.Data {
		hostFilePath := path.Join(hostPath, name)
		err := ioutil.WriteFile(hostFilePath, data, 0644)
		if err != nil {
			return err
		}
	}

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
