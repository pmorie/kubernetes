package deploy

import (
	"errors"
	"fmt"
	"time"

	"github.com/GoogleCloudPlatform/kubernetes/pkg/api"
	"github.com/GoogleCloudPlatform/kubernetes/pkg/client"
	"github.com/GoogleCloudPlatform/kubernetes/pkg/util"
	"github.com/golang/glog"
)

// DeploymentController is responsible for executing Deployment objects stored in etcd
type DeploymentController struct {
	kubeClient       client.Interface
	syncTime         <-chan time.Time
	deploymentRunner DeploymentRunner
}

// DeploymentRunner is an interface that knows how to run deployments and was
// created as an interface to allow testing.
type DeploymentRunner interface {
	// run a job for the specified deployment
	run(build api.Deployment) error
}

// DefaultDeploymentRunner is the default implementation of DeploymentRunner interface.
type DefaultDeploymentRunner struct {
	kubeClient  client.Interface
	environment map[string]string
}

// MakeDeploymentController creates a new DeploymentController.
func MakeDeploymentController(kubeClient client.Interface, initialEnvironment map[string]string) *DeploymentController {
	dc := &DeploymentController{
		kubeClient: kubeClient,
		deploymentRunner: &DefaultDeploymentRunner{
			kubeClient:  kubeClient,
			environment: initialEnvironment,
		},
	}
	return dc
}

// Run begins watching and syncing.
func (dc *DeploymentController) Run(period time.Duration) {
	dc.syncTime = time.Tick(period)
	go util.Forever(func() { dc.synchronize() }, period)
}

// The main sync loop. Iterates over current builds and delegates syncing.
func (dc *DeploymentController) synchronize() {
	deployments, err := dc.kubeClient.ListDeployments(nil)
	if err != nil {
		glog.Errorf("Synchronization error: %v (%#v)", err, err)
		return
	}
	for _, deployment := range deployments.Items {
		err = dc.syncDeployment(deployment)
		if err != nil {
			glog.Errorf("Error synchronizing: %#v", err)
		}
	}
}

// Sync loop brings the deployment state into sync with its corresponding job.
func (dc *DeploymentController) syncDeployment(deployment api.Deployment) error {
	glog.Infof("Syncing deployment state for deployment ID %s", deployment.ID)
	glog.Infof("Deployment details: %#v", deployment)
	if deployment.Status == api.DeploymentNew {
		return dc.deploymentRunner.run(deployment)
	} else if deployment.Status == api.DeploymentPending && len(deployment.JobID) == 0 {
		return nil
	} else if deployment.Status == api.DeploymentComplete {
		return nil
	}

	glog.Infof("Retrieving info for job ID %s for deployment ID %s", deployment.JobID, deployment.ID)
	job, err := dc.kubeClient.GetJob(deployment.JobID)
	if err != nil {
		return err
	}

	update := false

	glog.Infof("Status for job ID %s: %s", job.ID, job.Status)
	switch job.Status {
	case api.JobRunning:
		switch deployment.Status {
		case api.DeploymentNew, api.DeploymentPending:
			glog.Infof("Setting deployment state to running")
			deployment.Status = api.DeploymentRunning
			update = true
		case api.DeploymentComplete:
			return errors.New("Illegal state transition")
		}
	case api.JobPending:
		switch deployment.Status {
		case api.DeploymentNew:
			deployment.Status = api.DeploymentPending
			update = true
		case api.DeploymentComplete:
			return errors.New("Illegal state transition")
		}
	case api.JobComplete:
		if deployment.Status == api.DeploymentComplete {
			glog.Infof("Deployment status is already complete - no-op")
			return nil
		}

		// TODO: better way of evaluating job completion
		glog.Infof("Setting deployment state to complete")
		deployment.Status = api.DeploymentComplete
		deployment.Success = job.Success
		update = true
	}

	if update {
		_, err := dc.kubeClient.UpdateDeployment(deployment)
		if err != nil {
			return err
		}
	}

	return nil
}

func (r *DefaultDeploymentRunner) run(deployment api.Deployment) error {
	var envVars []api.EnvVar
	for k, v := range r.environment {
		envVars = append(envVars, api.EnvVar{Name: k, Value: v})
	}
	for k, v := range deployment.Context {
		envVars = append(envVars, api.EnvVar{Name: k, Value: v})
	}

	job := api.Job{
		Pod: api.Pod{
			Labels: map[string]string{},
			DesiredState: api.PodState{
				Manifest: api.ContainerManifest{
					Version: "v1beta1",
					Containers: []api.Container{
						{
							Name:          "deployment-job",
							Image:         deployment.DeploymentImage,
							Privileged:    true,
							RestartPolicy: "runOnce",
							Env:           envVars,
						},
					},
				},
			},
		},
	}

	glog.Infof("Creating job for deployment %s", deployment.ID)
	job, err := r.kubeClient.CreateJob(job)

	if err != nil {
		glog.Errorf("Error creating job for deployment ID %s: %s\n", deployment.ID, err.Error())

		deployment.Status = api.DeploymentComplete
		deployment.Success = false

		_, err := r.kubeClient.UpdateDeployment(deployment)
		if err != nil {
			return fmt.Errorf("Couldn't update deployment: %#v : %s", deployment, err.Error())
		}

		//TODO should this return nil or some error?
		return nil
	}

	glog.Infof("Setting deployment state to pending, job id = %s", job.ID)
	deployment.Status = api.DeploymentPending
	deployment.JobID = job.ID

	_, err = r.kubeClient.UpdateDeployment(deployment)
	if err != nil {
		return fmt.Errorf("Couldn't update Job: %#v : %s", job, err.Error())
	}

	return nil
}
