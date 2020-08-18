package accounts

import (
	"database/sql"

	sq "github.com/Masterminds/squirrel"

	"github.com/concourse/flag"
)

type AccountantFactory func(Command) Accountant

var DefaultAccountantFactory = func(cmd Command) Accountant {
	return &DBAccountant{PostgresConfig: cmd.Postgres}
}

type DBAccountant struct {
	PostgresConfig flag.PostgresConfig
}

func (da *DBAccountant) Account(containers []Container) ([]Sample, error) {
	conn, err := sql.Open("postgres", da.PostgresConfig.ConnectionString())
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
