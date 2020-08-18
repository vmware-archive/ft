package accounts

import (
	"github.com/concourse/flag"
)

type Command struct {
	Postgres     flag.PostgresConfig `group:"PostgreSQL Configuration" namespace:"postgres"`
	K8sNamespace string              `long:"k8s-namespace"`
	K8sPod       string              `long:"k8s-pod"`
}

type WorkerFactory func(Command) (Worker, error)

var DefaultWorkerFactory WorkerFactory = func(cmd Command) (Worker, error) {
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
