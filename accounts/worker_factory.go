package accounts

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
