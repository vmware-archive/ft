package accounts

import "github.com/concourse/concourse/atc/db"

type Sample struct {
	Container Container
	Labels    Labels
}

type Labels struct {
	Type      db.ContainerType
	Workloads []Workload
}

//go:generate go run github.com/maxbrunsfeld/counterfeiter/v6 . Accountant

type Accountant interface {
	Account([]Container) ([]Sample, error)
}

type Container struct {
	Handle string
	Stats  Stats
}

type Stats struct {
}

// a Workload is a description of a concourse core concept that corresponds to
// a container. i.e. team/pipeline/job/build/step or team/pipeline/resource.
// Roughly speaking this is what the fly hijack codebase refers to as a
// container fingerprint
// = a reason for a container's existence.

type Workload interface {
	ToString() string
}

//go:generate go run github.com/maxbrunsfeld/counterfeiter/v6 . Worker

type Worker interface {
	Containers(...StatsOption) ([]Container, error)
}

type StatsOption func()

func Account(w Worker, a Accountant) ([]Sample, error) {
	containers, err := w.Containers()
	if err != nil {
		return nil, err
	}
	return a.Account(containers)
}
