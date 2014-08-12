// The build controller is responsible for running jobs for builds
// watching them, and updating build state.
package main

import (
	"flag"
	"time"

	"github.com/GoogleCloudPlatform/kubernetes/pkg/build"
	"github.com/GoogleCloudPlatform/kubernetes/pkg/client"
	"github.com/GoogleCloudPlatform/kubernetes/pkg/util"
	"github.com/golang/glog"
)

var (
	master             = flag.String("master", "", "The address of the Kubernetes API server")
	dockerBuilderImage = flag.String("docker_builder_image", "docker-builder", "Image to use when running 'docker build' builds")
	dockerRegistry     = flag.String("docker_registry", "", "The address of the Docker registry that hosts built images")
	stiBuilderImage    = flag.String("sti_builder_image", "sti-builder", "Image to use when running 'sti build' builds")
	timeout            = flag.Int("timeout", 120, "The timeout to enforce for builds") // TODO: better description
)

func main() {
	flag.Parse()
	util.InitLogs()
	defer util.FlushLogs()

	if len(*master) == 0 {
		glog.Fatal("usage: build-controller -master <master>")
	}

	buildController := build.MakeBuildController(client.New("http://"+*master, nil), *dockerBuilderImage, *dockerRegistry, *stiBuilderImage, *timeout)

	buildController.Run(10 * time.Second)
	select {}
}
