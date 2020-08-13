package accounts_test

import (
	"context"
	"database/sql"
	"fmt"
	"io"
	"os"
	"strconv"
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
	"github.com/concourse/ft/accounts"
	"github.com/concourse/flag"
	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
	gocache "github.com/patrickmn/go-cache"
)

var _ = Describe("DBAccountant", func() {
	var (
		dbConn        db.Conn
		lockConn      *sql.DB
		lockFactory   lock.LockFactory
		teamFactory   db.TeamFactory
		workerFactory db.WorkerFactory
		team          db.Team
	)

	testDBName := func() string {
		return "testdb" + strconv.Itoa(GinkgoParallelNode())
	}

	dbHost := func() string {
		if val, exists := os.LookupEnv("DB_HOST"); exists {
			return val
		}
		return "127.0.0.1"
	}

	dataSource := func() string {
		return fmt.Sprintf(
			"host=%s user=postgres password=password sslmode=disable port=5432",
			dbHost(),
		)
	}

	dropTestDB := func() error {
		conn, err := sql.Open("postgres", dataSource())
		defer conn.Close()
		Expect(err).NotTo(HaveOccurred())
		_, err = conn.Exec("DROP DATABASE " + testDBName())
		return err
	}

	createTestDB := func() error {
		conn, err := sql.Open("postgres", dataSource())
		defer conn.Close()
		Expect(err).NotTo(HaveOccurred())
		_, err = conn.Exec("CREATE DATABASE " + testDBName())
		return err
	}

	BeforeEach(func() {
		if createTestDB() != nil {
			Expect(dropTestDB()).To(Succeed())
			Expect(createTestDB()).To(Succeed())
		}

		datasourceName := fmt.Sprintf("host=%s user=postgres password=password dbname=%s sslmode=disable port=5432", dbHost(), testDBName())
		var err error
		dbConn, err = db.Open(
			lagertest.NewTestLogger("postgres"),
			"postgres",
			datasourceName,
			nil,
			nil,
			"postgresrunner",
			nil,
		)
		Expect(err).NotTo(HaveOccurred())
		lockConn, err = sql.Open("postgres", datasourceName)
		Expect(err).NotTo(HaveOccurred())
		lockFactory = lock.NewLockFactory(
			lockConn,
			metric.LogLockAcquired,
			metric.LogLockReleased,
		)
		teamFactory = db.NewTeamFactory(dbConn, lockFactory)
		workerFactory = db.NewWorkerFactory(dbConn)
		team, _ = teamFactory.CreateDefaultTeamIfNotExists()
	})

	AfterEach(func() {
		dbConn.Close()
		lockConn.Close()
		dropTestDB()
	})

	registerWorker := func() {
		workerFactory.SaveWorker(atc.Worker{
			Platform: "linux",
			Version:  "0.0.0-dev",
			Name:     "worker",
			ResourceTypes: []atc.WorkerResourceType{{
				Type: "git",
			}},
		}, 10*time.Second)
	}

	createResources := func(rs atc.ResourceConfigs) {
		plan := []atc.Step{}
		for _, r := range rs {
			plan = append(plan, atc.Step{Config: &atc.GetStep{Name: r.Name}})
		}
		_, _, err := team.SavePipeline(
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
		Expect(err).NotTo(HaveOccurred())
	}

	testEngine := func(gclient gclient.Client, bclient baggageclaim.Client) engine.Engine {
		compressionLib := compression.NewGzipCompression()

		workerProvider := testWorkerProvider(
			dbConn,
			lockFactory,
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
			teamFactory,
			db.NewBuildFactory(dbConn, lockFactory, 24*time.Hour, 24*time.Hour),
			db.NewResourceCacheFactory(dbConn, lockFactory),
			db.NewResourceConfigFactory(dbConn, lockFactory),
			defaultLimits,
			worker.NewVolumeLocalityPlacementStrategy(),
			lockFactory,
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

	checkResources := func() {
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

		engine := testEngine(fakeGClient, fakeBaggageclaimClient)
		checkFactory := db.NewCheckFactory(
			dbConn,
			lockFactory,
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
				CheckableCounter:         db.NewCheckableCounter(dbConn),
			},
		).Run(context.TODO())
	}

	It("accounts for resource check containers", func() {
		atc.EnableGlobalResources = true
		// register a worker with "git" resource type
		registerWorker()
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
		createResources(resources)
		checkResources()
		accountant := accounts.NewDBAccountant(flag.PostgresConfig{
			Host:     dbHost(),
			Port:     5432,
			User:     "postgres",
			Password: "password",
			Database: testDBName(),
			SSLMode:  "disable",
		})
		Eventually(team.Containers).ShouldNot(BeEmpty())
		containers := []accounts.Container{}
		dbContainers, _ := team.Containers()
		for _, container := range dbContainers {
			containers = append(containers, accounts.Container{Handle: container.Handle()})
		}
		samples, err := accountant.Account(containers)
		Expect(err).NotTo(HaveOccurred())
		workloadStrings := []string{}
		for _, workload := range samples[0].Labels.Workloads {
			workloadStrings = append(workloadStrings, workload.ToString())
		}
		Expect(workloadStrings).To(ContainElements("main/p/r", "main/p/s"))
		Expect(samples[0].Labels.Type).To(Equal(db.ContainerTypeCheck))
	})

	createJob := func(jobConfig atc.JobConfig) db.Job {
		pipeline, _, err := team.SavePipeline(
			"p",
			atc.Config{
				Jobs: atc.JobConfigs{
					jobConfig,
				},
			},
			0,
			false,
		)
		Expect(err).NotTo(HaveOccurred())
		job, _, err := pipeline.Job(jobConfig.Name)
		Expect(err).NotTo(HaveOccurred())
		return job
	}

	It("accounts for job build containers", func() {
		// register a worker with "git" resource type
		registerWorker()
		job := createJob(atc.JobConfig{
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
				dbConn,
				100,
				gocache.New(10*time.Second, 10*time.Second),
			),
		)

		scheduler.NewRunner(
			lagertest.NewTestLogger("scheduler"),
			db.NewJobFactory(dbConn, lockFactory),
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
		fakeGClient.CreateReturns(fakeGClientContainer, nil)
		fakeBaggageclaimClient := new(baggageclaimfakes.FakeClient)
		fakeBaggageclaimVolume := new(baggageclaimfakes.FakeVolume)
		fakeBaggageclaimVolume.PathReturns("/path/to/fake/volume")
		fakeBaggageclaimClient.LookupVolumeReturns(fakeBaggageclaimVolume, true, nil)

		dbBuildFactory := db.NewBuildFactory(
			dbConn,
			lockFactory,
			5*time.Minute,
			120*time.Hour,
		)
		Eventually(dbBuildFactory.GetAllStartedBuilds).ShouldNot(BeEmpty())
		builds.NewTracker(
			dbBuildFactory,
			testEngine(fakeGClient, fakeBaggageclaimClient),
		).Run(
			lagerctx.NewContext(
				context.TODO(),
				lagertest.NewTestLogger("build-tracker"),
			),
		)

		// TODO wait for build to complete?

		accountant := accounts.NewDBAccountant(flag.PostgresConfig{
			Host:     dbHost(),
			Port:     5432,
			User:     "postgres",
			Password: "password",
			Database: testDBName(),
			SSLMode:  "disable",
		})
		Eventually(team.Containers).ShouldNot(BeEmpty())
		containers := []accounts.Container{}
		dbContainers, _ := team.Containers()
		for _, container := range dbContainers {
			containers = append(containers, accounts.Container{Handle: container.Handle()})
		}
		samples, err := accountant.Account(containers)
		Expect(err).NotTo(HaveOccurred())
		workloadStrings := []string{}
		for _, workload := range samples[0].Labels.Workloads {
			workloadStrings = append(workloadStrings, workload.ToString())
		}
		Expect(workloadStrings).To(ConsistOf("main/p/some-job/1/task"))
	})
})
