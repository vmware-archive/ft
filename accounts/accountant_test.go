package accounts_test

import (
	"context"
	"database/sql"
	"fmt"
	"io"
	"os"
	"time"

	"code.cloudfoundry.org/garden"
	"code.cloudfoundry.org/garden/gardenfakes"
	"code.cloudfoundry.org/lager/lagerctx"
	"code.cloudfoundry.org/lager/lagertest"
	"github.com/concourse/baggageclaim"
	"github.com/concourse/baggageclaim/baggageclaimfakes"
	"github.com/concourse/concourse/atc"
	"github.com/concourse/concourse/atc/builds"
	"github.com/concourse/concourse/atc/compression"
	"github.com/concourse/concourse/atc/creds/credsfakes"
	"github.com/concourse/concourse/atc/db"
	"github.com/concourse/concourse/atc/db/lock"
	"github.com/concourse/concourse/atc/engine"
	"github.com/concourse/concourse/atc/engine/builder"
	"github.com/concourse/concourse/atc/lidar"
	"github.com/concourse/concourse/atc/metric"
	"github.com/concourse/concourse/atc/resource"
	"github.com/concourse/concourse/atc/scheduler"
	"github.com/concourse/concourse/atc/scheduler/algorithm"
	"github.com/concourse/concourse/atc/worker"
	"github.com/concourse/concourse/atc/worker/gclient"
	"github.com/concourse/concourse/atc/worker/gclient/gclientfakes"
	"github.com/concourse/flag"
	"github.com/concourse/ft/accounts"
	gocache "github.com/patrickmn/go-cache"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
)

type AccountantSuite struct {
	suite.Suite
	*require.Assertions
	dbConn        db.Conn
	lockConn      *sql.DB
	lockFactory   lock.LockFactory
	teamFactory   db.TeamFactory
	workerFactory db.WorkerFactory
	team          db.Team
}

func testDBName() string {
	return "testdb"
}

func dbHost() string {
	if val, exists := os.LookupEnv("DB_HOST"); exists {
		return val
	}
	return "127.0.0.1"
}

func dataSource() string {
	return fmt.Sprintf(
		"host=%s user=postgres password=password sslmode=disable port=5432",
		dbHost(),
	)
}

func (s *AccountantSuite) dropTestDB() error {
	conn, err := sql.Open("postgres", dataSource())
	defer conn.Close()
	s.NoError(err)
	_, err = conn.Exec("DROP DATABASE " + testDBName())
	return err
}

func (s *AccountantSuite) createTestDB() error {
	conn, err := sql.Open("postgres", dataSource())
	defer conn.Close()
	s.NoError(err)
	_, err = conn.Exec("CREATE DATABASE " + testDBName())
	return err
}

func (s *AccountantSuite) SetupTest() {
	if s.createTestDB() != nil {
		s.NoError(s.dropTestDB())
		s.NoError(s.createTestDB())
	}

	datasourceName := fmt.Sprintf("host=%s user=postgres password=password dbname=%s sslmode=disable port=5432", dbHost(), testDBName())
	var err error
	s.dbConn, err = db.Open(
		lagertest.NewTestLogger("postgres"),
		"postgres",
		datasourceName,
		nil,
		nil,
		"postgresrunner",
		nil,
	)
	s.NoError(err)
	s.lockConn, err = sql.Open("postgres", datasourceName)
	s.NoError(err)
	s.lockFactory = lock.NewLockFactory(
		s.lockConn,
		metric.LogLockAcquired,
		metric.LogLockReleased,
	)
	s.teamFactory = db.NewTeamFactory(s.dbConn, s.lockFactory)
	s.workerFactory = db.NewWorkerFactory(s.dbConn)
	s.team, _ = s.teamFactory.CreateDefaultTeamIfNotExists()
}

func (s *AccountantSuite) TearDownTest() {
	s.dbConn.Close()
	s.lockConn.Close()
	s.dropTestDB()
}

func (s *AccountantSuite) registerWorker() {
	s.workerFactory.SaveWorker(atc.Worker{
		Platform: "linux",
		Version:  "0.0.0-dev",
		Name:     "worker",
		ResourceTypes: []atc.WorkerResourceType{{
			Type: "git",
		}},
	}, 10*time.Second)
}

func (s *AccountantSuite) createResources(rs atc.ResourceConfigs) {
	plan := []atc.Step{}
	for _, r := range rs {
		plan = append(plan, atc.Step{Config: &atc.GetStep{Name: r.Name}})
	}
	_, _, err := s.team.SavePipeline(
		"p",
		atc.Config{
			Resources: rs,
			Jobs: atc.JobConfigs{
				{
					Name:         "some-job",
					PlanSequence: plan,
				},
			},
		},
		0,
		false,
	)
	s.NoError(err)
}

func (s *AccountantSuite) testEngine(gclient gclient.Client, bclient baggageclaim.Client) engine.Engine {
	compressionLib := compression.NewGzipCompression()

	workerProvider := testWorkerProvider(
		s.dbConn,
		s.lockFactory,
		compressionLib,
		gclient,
		bclient,
	)
	pool := worker.NewPool(workerProvider)
	cpu := uint64(1024)
	mem := uint64(1024)
	defaultLimits := atc.ContainerLimits{
		CPU:    &cpu,
		Memory: &mem,
	}
	stepFactory := builder.NewStepFactory(
		pool,
		worker.NewClient(
			pool,
			workerProvider,
			compressionLib,
			10*time.Second,
			10*time.Second,
		),
		resource.NewResourceFactory(),
		s.teamFactory,
		db.NewBuildFactory(s.dbConn, s.lockFactory, 24*time.Hour, 24*time.Hour),
		db.NewResourceCacheFactory(s.dbConn, s.lockFactory),
		db.NewResourceConfigFactory(s.dbConn, s.lockFactory),
		defaultLimits,
		worker.NewVolumeLocalityPlacementStrategy(),
		s.lockFactory,
		false,
	)
	return engine.NewEngine(
		builder.NewStepBuilder(
			stepFactory,
			builder.NewDelegateFactory(),
			"external-url",
			new(credsfakes.FakeSecrets),
			new(credsfakes.FakeVarSourcePool),
			false,
		),
	)
}

func (s *AccountantSuite) checkResources() {
	fakeGClient := new(gclientfakes.FakeClient)
	fakeGClientContainer := new(gclientfakes.FakeContainer)
	fakeGClientContainer.RunStub = func(ctx context.Context, ps garden.ProcessSpec, pi garden.ProcessIO) (garden.Process, error) {
		fakeProcess := new(gardenfakes.FakeProcess)
		fakeProcess.WaitStub = func() (int, error) {
			io.WriteString(pi.Stdout, "[]")
			return 0, nil
		}
		return fakeProcess, nil
	}
	fakeGClient.CreateReturns(fakeGClientContainer, nil)
	fakeBaggageclaimClient := new(baggageclaimfakes.FakeClient)
	fakeBaggageclaimVolume := new(baggageclaimfakes.FakeVolume)
	fakeBaggageclaimVolume.PathReturns("/path/to/fake/volume")
	fakeBaggageclaimClient.LookupVolumeReturns(fakeBaggageclaimVolume, true, nil)

	engine := s.testEngine(fakeGClient, fakeBaggageclaimClient)
	checkFactory := db.NewCheckFactory(
		s.dbConn,
		s.lockFactory,
		new(credsfakes.FakeSecrets),
		new(credsfakes.FakeVarSourcePool),
		1*time.Hour,
	)
	logger := lagertest.NewTestLogger("test")

	// insert checks
	lidar.NewScanner(
		logger,
		checkFactory,
		new(credsfakes.FakeSecrets),
		1*time.Hour,
		10*time.Second,
		1*time.Minute,
	).Run(context.TODO())
	// run the checks
	lidar.NewChecker(
		logger,
		checkFactory,
		engine,
		lidar.CheckRateCalculator{
			MaxChecksPerSecond:       -1,
			ResourceCheckingInterval: 10 * time.Second,
			CheckableCounter:         db.NewCheckableCounter(s.dbConn),
		},
	).Run(context.TODO())
}

func (s *AccountantSuite) createJob(jobConfig atc.JobConfig) db.Job {
	pipeline, _, err := s.team.SavePipeline(
		"p",
		atc.Config{
			Jobs: atc.JobConfigs{
				jobConfig,
			},
		},
		0,
		false,
	)
	s.NoError(err)
	job, _, err := pipeline.Job(jobConfig.Name)
	s.NoError(err)
	return job
}

func (s *AccountantSuite) TestAccountsForResourceCheckContainers() {
	atc.EnableGlobalResources = true
	// register a worker with "git" resource type
	s.registerWorker()
	resources := atc.ResourceConfigs{
		{
			Name:   "r",
			Type:   "git",
			Source: atc.Source{"some": "repository"},
		},
		{
			Name:   "s",
			Type:   "git",
			Source: atc.Source{"some": "repository"},
		},
	}
	s.createResources(resources)
	s.checkResources()
	accountant := &accounts.DBAccountant{
		PostgresConfig: flag.PostgresConfig{
			Host:     dbHost(),
			Port:     5432,
			User:     "postgres",
			Password: "password",
			Database: testDBName(),
			SSLMode:  "disable",
		},
	}
	s.Eventually(
		func() bool {
			cs, _ := s.team.Containers()
			return len(cs) > 0
		},
		time.Second,
		100*time.Millisecond,
	)
	containers := []accounts.Container{}
	dbContainers, _ := s.team.Containers()
	for _, container := range dbContainers {
		containers = append(containers, accounts.Container{Handle: container.Handle()})
	}
	samples, err := accountant.Account(containers)
	s.NoError(err)
	workloadStrings := []string{}
	for _, workload := range samples[0].Labels.Workloads {
		workloadStrings = append(workloadStrings, workload.ToString())
	}
	s.Contains(workloadStrings, "main/p/r")
	s.Contains(workloadStrings, "main/p/s")
	s.Equal(samples[0].Labels.Type, db.ContainerTypeCheck)
}

func (s *AccountantSuite) TestAccountsForJobBuildContainers() {
	// register a worker with "git" resource type
	s.registerWorker()
	job := s.createJob(atc.JobConfig{
		Name: "some-job",
		PlanSequence: []atc.Step{
			{Config: &atc.TaskStep{
				Name: "task",
				Config: &atc.TaskConfig{
					Platform: "linux",
					Run: atc.TaskRunConfig{
						Path: "foo",
					},
				},
			}},
		},
	})
	job.CreateBuild()
	alg := algorithm.New(
		db.NewVersionsDB(
			s.dbConn,
			100,
			gocache.New(10*time.Second, 10*time.Second),
		),
	)

	scheduler.NewRunner(
		lagertest.NewTestLogger("scheduler"),
		db.NewJobFactory(s.dbConn, s.lockFactory),
		&scheduler.Scheduler{
			Algorithm: alg,
			BuildStarter: scheduler.NewBuildStarter(
				builds.NewPlanner(
					atc.NewPlanFactory(time.Now().Unix()),
				),
				alg),
		},
		32,
	).Run(context.TODO())

	fakeGClient := new(gclientfakes.FakeClient)
	fakeGClientContainer := new(gclientfakes.FakeContainer)
	fakeGClientContainer.RunStub = func(ctx context.Context, ps garden.ProcessSpec, pi garden.ProcessIO) (garden.Process, error) {
		fakeProcess := new(gardenfakes.FakeProcess)
		fakeProcess.WaitStub = func() (int, error) {
			io.WriteString(pi.Stdout, "[]")
			return 0, nil
		}
		return fakeProcess, nil
	}
	fakeGClientContainer.AttachStub = func(ctx context.Context, foo string, pi garden.ProcessIO) (garden.Process, error) {
		fakeProcess := new(gardenfakes.FakeProcess)
		fakeProcess.WaitStub = func() (int, error) {
			io.WriteString(pi.Stdout, "[]")
			return 0, nil
		}
		return fakeProcess, nil
	}
	fakeGClient.CreateReturns(fakeGClientContainer, nil)
	fakeBaggageclaimClient := new(baggageclaimfakes.FakeClient)
	fakeBaggageclaimVolume := new(baggageclaimfakes.FakeVolume)
	fakeBaggageclaimVolume.PathReturns("/path/to/fake/volume")
	fakeBaggageclaimClient.LookupVolumeReturns(fakeBaggageclaimVolume, true, nil)

	dbBuildFactory := db.NewBuildFactory(
		s.dbConn,
		s.lockFactory,
		5*time.Minute,
		120*time.Hour,
	)
	s.Eventually(
		func() bool {
			bs, _ := dbBuildFactory.GetAllStartedBuilds()
			return len(bs) > 0
		},
		time.Second,
		100*time.Millisecond,
	)
	builds.NewTracker(
		dbBuildFactory,
		s.testEngine(fakeGClient, fakeBaggageclaimClient),
	).Run(
		lagerctx.NewContext(
			context.TODO(),
			lagertest.NewTestLogger("build-tracker"),
		),
	)

	// TODO wait for build to complete?

	accountant := &accounts.DBAccountant{
		PostgresConfig: flag.PostgresConfig{
			Host:     dbHost(),
			Port:     5432,
			User:     "postgres",
			Password: "password",
			Database: testDBName(),
			SSLMode:  "disable",
		},
	}
	s.Eventually(
		func() bool {
			cs, _ := s.team.Containers()
			return len(cs) > 0
		},
		time.Second,
		100*time.Millisecond,
	)
	containers := []accounts.Container{}
	dbContainers, _ := s.team.Containers()
	for _, container := range dbContainers {
		containers = append(containers, accounts.Container{Handle: container.Handle()})
	}
	samples, err := accountant.Account(containers)
	s.NoError(err)
	workloadStrings := []string{}
	for _, workload := range samples[0].Labels.Workloads {
		workloadStrings = append(workloadStrings, workload.ToString())
	}
	s.Equal(workloadStrings, []string{"main/p/some-job/1/task"})
}
