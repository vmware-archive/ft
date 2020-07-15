package accounts

import (
	"code.cloudfoundry.org/garden/client/connection"
)

func NewLANWorker() Worker {
	return &LANWorker{}
}

type LANWorker struct {
}

func (lw *LANWorker) Containers(opts ...StatsOption) ([]Container, error) {
	handles, err := connection.New("tcp", "127.0.0.1:7777").List(nil)
	if err != nil {
		return nil, err
	}
	containers := []Container{}
	for _, handle := range handles {
		containers = append(containers, Container{Handle: handle})
	}
	return containers, nil
}
