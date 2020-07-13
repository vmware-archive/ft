package accounts_test

import (
	"context"
	"os"
	"time"

	"code.cloudfoundry.org/clock"
	"code.cloudfoundry.org/lager"
	"code.cloudfoundry.org/lager/lagerctx"
	"code.cloudfoundry.org/lager/lagertest"
	"github.com/concourse/concourse/atc"
	"github.com/concourse/concourse/atc/compression"
	"github.com/concourse/concourse/atc/creds/credsfakes"
	"github.com/concourse/concourse/atc/db"
	"github.com/concourse/concourse/atc/db/lock"
	"github.com/concourse/concourse/atc/engine"
	"github.com/concourse/concourse/atc/engine/builder"
	"github.com/concourse/concourse/atc/lidar"
	"github.com/concourse/concourse/atc/metric"
	"github.com/concourse/concourse/atc/policy"
	"github.com/concourse/concourse/atc/postgresrunner"
	"github.com/concourse/concourse/atc/resource"
	"github.com/concourse/concourse/atc/worker"
	"github.com/concourse/concourse/atc/worker/gclient/gclientfakes"
	"github.com/concourse/concourse/atc/worker/image"
	"github.com/concourse/concourse/atc/worker/workerfakes"
	"github.com/concourse/flag"
	"github.com/concourse/retryhttp"
	"github.com/concourse/workloads/accounts"
	"github.com/cppforlife/go-semi-semantic/version"
	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
	"github.com/tedsuo/ifrit"
)

var _ = FDescribe("DBAccountant", func() {
	var (
		postgresRunner postgresrunner.Runner
		dbProcess      ifrit.Process
		dbConn         db.Conn
		lockFactory    lock.LockFactory
		teamFactory    db.TeamFactory
		workerFactory  db.WorkerFactory
	)

	BeforeEach(func() {
		postgresRunner = postgresrunner.Runner{
			Port: 5433 + GinkgoParallelNode(),
		}
		dbProcess = ifrit.Invoke(postgresRunner)
		postgresRunner.CreateTestDB()
		dbConn = postgresRunner.OpenConn()
		lockFactory = lock.NewLockFactory(postgresRunner.OpenSingleton(), metric.LogLockAcquired, metric.LogLockReleased)
		teamFactory = db.NewTeamFactory(dbConn, lockFactory)
		workerFactory = db.NewWorkerFactory(dbConn)
	})

	AfterEach(func() {
		postgresRunner.DropTestDB()

		dbProcess.Signal(os.Interrupt)
		err := <-dbProcess.Wait()
		Expect(err).NotTo(HaveOccurred())
	})

	registerWorker := func(w atc.Worker) {
		workerFactory.SaveWorker(w, 10*time.Second)
	}

	createResources := func(rs atc.ResourceConfigs) {
		team, _ := teamFactory.CreateTeam(atc.Team{Name: "t"})
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

	checkResources := func() {
		//        using a modified stepfactory
		//          using a fake pool
		//          using a fake worker client
		dbVolumeRepository := db.NewVolumeRepository(dbConn)
		fakeGClient := new(gclientfakes.FakeClient)
		fakeVolumeClient := new(workerfakes.FakeVolumeClient)
		// TODO stub volume client to create base resource type
		resourceFactory := resource.NewResourceFactory()
		dbResourceCacheFactory := db.NewResourceCacheFactory(dbConn, lockFactory)
		dbResourceConfigFactory := db.NewResourceConfigFactory(dbConn, lockFactory)
		fetchSourceFactory := worker.NewFetchSourceFactory(dbResourceCacheFactory)
		resourceFetcher := worker.NewFetcher(clock.NewClock(), lockFactory, fetchSourceFactory)
		imageResourceFetcherFactory := image.NewImageResourceFetcherFactory(
			resourceFactory,
			dbResourceCacheFactory,
			dbResourceConfigFactory,
			resourceFetcher,
		)
		compressionLib := compression.NewGzipCompression()
		imageFactory := image.NewImageFactory(imageResourceFetcherFactory, compressionLib)
		dbWorkerBaseResourceTypeFactory := db.NewWorkerBaseResourceTypeFactory(dbConn)
		dbWorkerTaskCacheFactory := db.NewWorkerTaskCacheFactory(dbConn)
		dbTaskCacheFactory := db.NewTaskCacheFactory(dbConn)
		dbWorkerFactory := db.NewWorkerFactory(dbConn)
		workerVersion, _ := version.NewVersionFromString("0.0.0-dev")
		// real provider
		testProvider := &TestWorkerProvider{
			fakeGClient,
			fakeVolumeClient,
			lockFactory,
			retryhttp.NewExponentialBackOffFactory(5 * time.Minute),
			resourceFetcher,
			imageFactory,
			dbResourceCacheFactory,
			dbResourceConfigFactory,
			dbWorkerBaseResourceTypeFactory,
			dbTaskCacheFactory,
			dbWorkerTaskCacheFactory,
			dbVolumeRepository,
			teamFactory,
			dbWorkerFactory,
			workerVersion,
			10 * time.Minute,
			5 * time.Minute,
			nil,
		}
		pool := worker.NewPool(testProvider)
		workerClient := worker.NewClient(
			pool,
			testProvider,
			compressionLib,
			10*time.Second,
			10*time.Second,
		)
		cpu := uint64(1024)
		mem := uint64(1024)
		defaultLimits := atc.ContainerLimits{
			CPU:    &cpu,
			Memory: &mem,
		}
		stepFactory := builder.NewStepFactory(
			pool,
			workerClient,
			resourceFactory,
			teamFactory,
			db.NewBuildFactory(dbConn, lockFactory, 24*time.Hour, 24*time.Hour),
			dbResourceCacheFactory,
			db.NewResourceConfigFactory(dbConn, lockFactory),
			defaultLimits,
			worker.NewVolumeLocalityPlacementStrategy(),
			lockFactory,
			false,
		)
		// insert checks -- maybe using a modified scanner?
		// "run" checks -- using a modified checker
		//    using a modified engine
		//      using a modified stepbuilder
		//        using a fake secrets & varsourcepool
		fakeSecrets := new(credsfakes.FakeSecrets)
		fakeVarSourcePool := new(credsfakes.FakeVarSourcePool)
		engine := engine.NewEngine(
			builder.NewStepBuilder(
				stepFactory,
				builder.NewDelegateFactory(),
				"external-url",
				fakeSecrets,
				fakeVarSourcePool,
				false,
			),
		)

		checkFactory := db.NewCheckFactory(dbConn, lockFactory, fakeSecrets, fakeVarSourcePool, 1*time.Hour)
		logger := lagertest.NewTestLogger("test")

		// insert checks
		lidar.NewScanner(
			logger,
			checkFactory,
			fakeSecrets,
			1*time.Hour,
			10*time.Second,
			1*time.Minute,
		).Run(lagerctx.NewContext(context.Background(), logger))
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
		).Run(lagerctx.NewContext(context.Background(), logger))
	}

	It("accounts for resource check containers", func() {
		atc.EnableGlobalResources = true
		// register a worker with "git" resource type
		registerWorker(atc.Worker{
			Version: "0.0.0-dev",
			ResourceTypes: []atc.WorkerResourceType{{
				Type: "git",
			}},
		})
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
			Host:     "127.0.0.1",
			Port:     5433 + uint16(GinkgoParallelNode()),
			User:     "postgres",
			Database: "testdb",
			SSLMode:  "disable",
		})
		samples, err := accountant.Account([]accounts.Container{accounts.Container{Handle: "some-handle"}})
		Expect(err).NotTo(HaveOccurred())
		Expect(samples[0].Workloads[0].ToString()).To(Equal("t/p/r"))
		Expect(samples[0].Workloads[1].ToString()).To(Equal("t/p/s"))
	})
})

type TestWorkerProvider struct {
	FakeGClient                       *gclientfakes.FakeClient
	FakeVolumeClient                  *workerfakes.FakeVolumeClient
	lockFactory                       lock.LockFactory
	retryBackOffFactory               retryhttp.BackOffFactory
	resourceFetcher                   worker.Fetcher
	imageFactory                      worker.ImageFactory
	dbResourceCacheFactory            db.ResourceCacheFactory
	dbResourceConfigFactory           db.ResourceConfigFactory
	dbWorkerBaseResourceTypeFactory   db.WorkerBaseResourceTypeFactory
	dbTaskCacheFactory                db.TaskCacheFactory
	dbWorkerTaskCacheFactory          db.WorkerTaskCacheFactory
	dbVolumeRepository                db.VolumeRepository
	dbTeamFactory                     db.TeamFactory
	dbWorkerFactory                   db.WorkerFactory
	workerVersion                     version.Version
	baggageclaimResponseHeaderTimeout time.Duration
	gardenRequestTimeout              time.Duration
	policyChecker                     *policy.Checker
}

func (provider *TestWorkerProvider) RunningWorkers(logger lager.Logger) ([]worker.Worker, error) {
	savedWorkers, err := provider.dbWorkerFactory.Workers()
	if err != nil {
		return nil, err
	}

	buildContainersCountPerWorker, err := provider.dbWorkerFactory.BuildContainersCountPerWorker()
	if err != nil {
		return nil, err
	}

	workers := []worker.Worker{}

	for _, savedWorker := range savedWorkers {
		if savedWorker.State() != db.WorkerStateRunning {
			continue
		}

		workerLog := logger.Session("running-worker")
		worker := provider.NewGardenWorker(
			workerLog,
			savedWorker,
			buildContainersCountPerWorker[savedWorker.Name()],
		)
		if !worker.IsVersionCompatible(workerLog, provider.workerVersion) {
			continue
		}

		workers = append(workers, worker)
	}

	return workers, nil
}

func (provider *TestWorkerProvider) FindWorkersForContainerByOwner(
	logger lager.Logger,
	owner db.ContainerOwner,
) ([]worker.Worker, error) {
	logger = logger.Session("worker-for-container")
	dbWorkers, err := provider.dbWorkerFactory.FindWorkersForContainerByOwner(owner)
	if err != nil {
		return nil, err
	}

	var workers []worker.Worker
	for _, w := range dbWorkers {
		worker := provider.NewGardenWorker(logger, w, 0)
		if worker.IsVersionCompatible(logger, provider.workerVersion) {
			workers = append(workers, worker)
		}
	}

	return workers, nil
}

func (provider *TestWorkerProvider) FindWorkerForContainer(
	logger lager.Logger,
	teamID int,
	handle string,
) (worker.Worker, bool, error) {
	logger = logger.Session("worker-for-container")
	team := provider.dbTeamFactory.GetByID(teamID)

	dbWorker, found, err := team.FindWorkerForContainer(handle)
	if err != nil {
		return nil, false, err
	}

	if !found {
		return nil, false, nil
	}

	worker := provider.NewGardenWorker(logger, dbWorker, 0)
	if !worker.IsVersionCompatible(logger, provider.workerVersion) {
		return nil, false, nil
	}
	return worker, true, err
}

func (provider *TestWorkerProvider) FindWorkerForVolume(
	logger lager.Logger,
	teamID int,
	handle string,
) (worker.Worker, bool, error) {
	logger = logger.Session("worker-for-volume")
	team := provider.dbTeamFactory.GetByID(teamID)

	dbWorker, found, err := team.FindWorkerForVolume(handle)
	if err != nil {
		return nil, false, err
	}

	if !found {
		return nil, false, nil
	}

	worker := provider.NewGardenWorker(logger, dbWorker, 0)
	if !worker.IsVersionCompatible(logger, provider.workerVersion) {
		return nil, false, nil
	}
	return worker, true, err
}
func (provider *TestWorkerProvider) NewGardenWorker(logger lager.Logger, savedWorker db.Worker, buildContainersCount int) worker.Worker {
	// modified gardenworker with fake garden/baggageclaim
	return worker.NewGardenWorker(
		provider.FakeGClient,
		provider.dbVolumeRepository,
		provider.FakeVolumeClient,
		provider.imageFactory,
		provider.resourceFetcher,
		provider.dbTeamFactory,
		savedWorker,
		provider.dbResourceCacheFactory,
		buildContainersCount,
		provider.policyChecker,
	)
}
