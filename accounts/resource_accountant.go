package accounts

import (
	"database/sql"
	"fmt"

	sq "github.com/Masterminds/squirrel"
	"github.com/concourse/concourse/atc/db"
)

type ResourceWorkload struct {
	resourceName string
	pipelineName string
	teamName     string
}

func (rw ResourceWorkload) ToString() string {
	return fmt.Sprintf("%s/%s/%s", rw.teamName, rw.pipelineName, rw.resourceName)
}

func resourceSamples(
	conn *sql.DB,
	containers []Container,
) ([]Sample, error) {
	rows, err := sq.StatementBuilder.
		PlaceholderFormat(sq.Dollar).
		Select("c.handle", "r.name", "p.name", "t.name").
		From("containers c").
		Join("resource_config_check_sessions rccs on c.resource_config_check_session_id = rccs.id").
		Join("resources r on rccs.resource_config_id = r.resource_config_id").
		Join("pipelines p on r.pipeline_id = p.id").
		Join("teams t on p.team_id = t.id").
		Where(filterHandles(containers)).
		RunWith(conn).
		Query()
	if err != nil {
		return nil, err
	}

	workloads := map[string][]Workload{}
	defer db.Close(rows)
	for rows.Next() {
		resource := ResourceWorkload{}
		var handle string
		err = rows.Scan(
			&handle,
			&resource.resourceName,
			&resource.pipelineName,
			&resource.teamName,
		)
		if err != nil {
			return nil, err
		}
		workloads[handle] = append(workloads[handle], &resource)
	}

	var samples []Sample
	for _, container := range containers {
		if ws, ok := workloads[container.Handle]; ok {
			samples = append(samples, Sample{
				Container: container,
				Labels: Labels{
					Type:      db.ContainerTypeCheck,
					Workloads: ws,
				},
			})
		}
	}

	return samples, nil
}
