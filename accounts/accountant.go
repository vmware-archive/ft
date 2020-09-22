package accounts

import (
	sq "github.com/Masterminds/squirrel"
)

type AccountantFactory func(Command) (Accountant, error)

var DefaultAccountantFactory = func(cmd Command) (Accountant, error) {
	var opener PostgresOpener
	if cmd.WebK8sNamespace != "" && cmd.WebK8sPod != "" {
		restConfig, err := RESTConfig()
		if err != nil {
			return nil, err
		}
		k8sClient := &k8sClient{
			RESTConfig: restConfig,
			Namespace:  cmd.WebK8sNamespace,
		}
		pod, err := k8sClient.GetPod(cmd.WebK8sPod)
		if err != nil {
			return nil, err
		}
		opener = &K8sWebNodeInferredPostgresOpener{
			WebPod:    &K8sWebPod{Pod: pod, Client: k8sClient},
			PodName:   cmd.WebK8sPod,
		}
	} else {
		opener = &StaticPostgresOpener{cmd.Postgres}
	}
	return &DBAccountant{Opener: opener}, nil
}

type DBAccountant struct {
	Opener PostgresOpener
}

func (da *DBAccountant) Account(containers []Container) ([]Sample, error) {
	conn, err := da.Opener.Open()
	if err != nil {
		return nil, err
	}
	defer conn.Close()

	samples := []Sample{}
	resourceSamples, err := resourceSamples(conn, containers)
	if err != nil {
		return nil, err
	}
	samples = append(samples, resourceSamples...)
	buildSamples, err := buildSamples(conn, containers)
	if err != nil {
		return nil, err
	}
	samples = append(samples, buildSamples...)

	return samples, nil
}

func filterHandles(containers []Container) sq.Eq {
	handles := []string{}
	for _, container := range containers {
		handles = append(handles, container.Handle)
	}
	return sq.Eq{"c.handle": handles}
}
