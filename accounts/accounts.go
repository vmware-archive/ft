package accounts

//go:generate go run github.com/maxbrunsfeld/counterfeiter/v6 . Accountant

type Accountant interface {
	Account([]Container) ([]Sample, error)
}

type Sample struct {
	Container Container
	Workloads []Workload
}

type Container struct {
	Handle string
	Stats Stats
}

type Stats struct {
}

type Workload interface {
	toString() string
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
