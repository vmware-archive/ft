package accounts

import (
	"database/sql"
	"fmt"

	sq "github.com/Masterminds/squirrel"
	"github.com/concourse/atc/db"
	"github.com/concourse/flag"
)

func NewDBAccountant(pgc flag.PostgresConfig) Accountant {
	return &DBAccountant{postgresConfig: pgc}
}

type DBAccountant struct {
	postgresConfig flag.PostgresConfig
}

func (da *DBAccountant) Account(containers []Container) ([]Sample, error) {
	handles := []string{}
	for _, container := range containers {
		handles = append(handles, container.Handle)
	}

	dbConn, err := sql.Open("postgres", da.postgresConfig.ConnectionString())
	if err != nil {
		return nil, err
	}

	rows, err := sq.StatementBuilder.
		PlaceholderFormat(sq.Dollar).
		Select("c.handle", "r.name", "p.name", "t.name").
		From("containers c").
		Join("resource_config_check_sessions rccs on c.resource_config_check_session_id = rccs.id").
		Join("resources r on rccs.resource_config_id = r.resource_config_id").
		Join("pipelines p on r.pipeline_id = p.id").
		Join("teams t on p.team_id = t.id").
		Where(sq.Eq{"c.handle": handles}).
		RunWith(dbConn).
		Query()
	if err != nil {
		return nil, err
	}

	workloadMap := map[string][]Workload{}

	defer db.Close(rows)
	for rows.Next() {
		resource := ResourceWorkload{}
		var handle string
		rows.Scan(&handle, &resource.resourceName, &resource.pipelineName, &resource.teamName)
		workloadMap[handle] = append(workloadMap[handle], resource)
	}

	samples := []Sample{}
	for _, c := range containers {
		samples = append(samples, Sample{Container: c, Workloads: workloadMap[c.Handle]})
	}
	return samples, nil
}

type ResourceWorkload struct {
	resourceName string
	pipelineName string
	teamName     string
}

func (rw ResourceWorkload) ToString() string {
	return fmt.Sprintf("%s/%s/%s", rw.teamName, rw.pipelineName, rw.resourceName)
}
