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

	conn, err := sql.Open("postgres", da.postgresConfig.ConnectionString())
	if err != nil {
		return nil, err
	}
	defer conn.Close()

	workloads := map[string][]Workload{}
	err = insertResourceWorkloads(workloads, conn, handles)
	if err != nil {
		return nil, err
	}
	err = insertBuildWorkloads(workloads, conn, handles)
	if err != nil {
		return nil, err
	}

	samples := []Sample{}
	for _, c := range containers {
		samples = append(
			samples,
			Sample{Container: c, Workloads: workloads[c.Handle]},
		)
	}
	return samples, nil
}

func insertResourceWorkloads(
	workloads map[string][]Workload,
	conn *sql.DB,
	handles []string,
) error {
	rows, err := sq.StatementBuilder.
		PlaceholderFormat(sq.Dollar).
		Select("c.handle", "r.name", "p.name", "t.name").
		From("containers c").
		Join("resource_config_check_sessions rccs on c.resource_config_check_session_id = rccs.id").
		Join("resources r on rccs.resource_config_id = r.resource_config_id").
		Join("pipelines p on r.pipeline_id = p.id").
		Join("teams t on p.team_id = t.id").
		Where(sq.Eq{"c.handle": handles}).
		RunWith(conn).
		Query()
	if err != nil {
		return err
	}

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
			return err
		}
		workloads[handle] = append(workloads[handle], &resource)
	}

	return nil
}

type ResourceWorkload struct {
	resourceName string
	pipelineName string
	teamName     string
}

func (rw ResourceWorkload) ToString() string {
	return fmt.Sprintf("%s/%s/%s", rw.teamName, rw.pipelineName, rw.resourceName)
}

func insertBuildWorkloads(
	workloads map[string][]Workload,
	conn *sql.DB,
	handles []string,
) error {
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
			sq.Eq{"c.handle": handles},
			sq.NotEq{"c.meta_type": db.ContainerTypeCheck},
		}).
		RunWith(conn).
		Query()
	if err != nil {
		return err
	}

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
			return err
		}
		workloads[handle] = append(workloads[handle], &BuildWorkload{
			teamName:     teamName,
			pipelineName: metadata.PipelineName,
			jobName:      metadata.JobName,
			buildName:    metadata.BuildName,
			stepName:     metadata.StepName,
		})
	}

	return nil
}

type BuildWorkload struct {
	teamName     string
	pipelineName string
	jobName      string
	buildName    string
	stepName     string
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
