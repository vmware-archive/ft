package accounts

import (
	"github.com/concourse/flag"
)

type Command struct {
	Postgres     flag.PostgresConfig `group:"PostgreSQL Configuration" namespace:"postgres"`
	K8sNamespace string              `long:"k8s-namespace"`
	K8sPod       string              `long:"k8s-pod"`
}

//go:generate go run github.com/maxbrunsfeld/counterfeiter/v6 . WorkerFactory

type WorkerFactory interface {
	CreateWorker(Command) (Worker, error)
}

type workerFactory struct{}

var DefaultWorkerFactory WorkerFactory = &workerFactory{}

func (wf *workerFactory) CreateWorker(cmd Command) (Worker, error) {
	var dialer GardenDialer
	if cmd.K8sNamespace != "" && cmd.K8sPod != "" {
		restConfig, err := RESTConfig()
		if err != nil {
			return nil, err
		}
		dialer = &K8sGardenDialer{
			RESTConfig: restConfig,
			Namespace:  cmd.K8sNamespace,
			PodName:    cmd.K8sPod,
		}
	} else {
		dialer = &LANGardenDialer{}
	}
	return &GardenWorker{
		Dialer: dialer,
	}, nil
}
