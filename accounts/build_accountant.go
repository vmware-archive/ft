package accounts

import (
	"database/sql"
	"fmt"

	sq "github.com/Masterminds/squirrel"
	"github.com/concourse/concourse/atc/db"
)

type BuildWorkload struct {
	teamName      string
	pipelineName  string
	jobName       string
	buildName     string
	stepName      string
	containerType db.ContainerType
}

func (bw BuildWorkload) ToString() string {
	return fmt.Sprintf(
		"%s/%s/%s/%s/%s",
		bw.teamName,
		bw.pipelineName,
		bw.jobName,
		bw.buildName,
		bw.stepName,
	)
}

func buildSamples(
	conn *sql.DB,
	containers []Container,
) ([]Sample, error) {
	rows, err := sq.StatementBuilder.
		PlaceholderFormat(sq.Dollar).
		Select(
			"c.handle",
			"t.name",
			"c.meta_type",
			"c.meta_step_name",
			"c.meta_attempt",
			"c.meta_working_directory",
			"c.meta_process_user",
			"c.meta_pipeline_id",
			"c.meta_job_id",
			"c.meta_build_id",
			"c.meta_pipeline_name",
			"c.meta_job_name",
			"c.meta_build_name",
		).
		From("containers c").
		Join("teams t ON c.team_id = t.id").
		Where(sq.And{
			filterHandles(containers),
			sq.NotEq{"c.meta_type": db.ContainerTypeCheck},
		}).
		RunWith(conn).
		Query()
	if err != nil {
		return nil, err
	}

	workloads := map[string]BuildWorkload{}
	defer db.Close(rows)
	for rows.Next() {
		var metadata db.ContainerMetadata
		var handle, teamName string
		columns := append(
			[]interface{}{&handle, &teamName},
			metadata.ScanTargets()...,
		)
		err = rows.Scan(columns...)
		if err != nil {
			return nil, err
		}
		workloads[handle] = BuildWorkload{
			teamName:      teamName,
			pipelineName:  metadata.PipelineName,
			jobName:       metadata.JobName,
			buildName:     metadata.BuildName,
			stepName:      metadata.StepName,
			containerType: metadata.Type,
		}
	}

	var samples []Sample
	for _, container := range containers {
		if workload, ok := workloads[container.Handle]; ok {
			samples = append(samples, Sample{
				Container: container,
				Labels: Labels{
					Type:      workload.containerType,
					Workloads: []Workload{workload},
				},
			})
		}
	}

	return samples, nil
}
