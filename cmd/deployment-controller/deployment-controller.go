// The deployment controller is responsible for monitoring and starting
// new deployments
//
package main

import (
	"flag"
	"time"

	"github.com/GoogleCloudPlatform/kubernetes/pkg/client"
	"github.com/GoogleCloudPlatform/kubernetes/pkg/deploy"
	"github.com/GoogleCloudPlatform/kubernetes/pkg/util"
	"github.com/golang/glog"
)

var (
	master = flag.String("master", "", "The address of the Kubernetes API server")
)

func main() {
	flag.Parse()
	util.InitLogs()
	defer util.FlushLogs()

	if len(*master) == 0 {
		glog.Fatal("usage: deployment-controller -master <master>")
	}

	env := map[string]string{
		"KUBERNETES_MASTER": "http://" + *master,
	}
	deploymentController := deploy.MakeDeploymentController(client.New("http://"+*master, nil), env)
	deploymentController.Run(10 * time.Second)
	select {}
}
