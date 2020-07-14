package accounts_test

import (
	"time"

	"code.cloudfoundry.org/clock"
	"code.cloudfoundry.org/lager"
	"github.com/concourse/baggageclaim"
	"github.com/concourse/concourse/atc/compression"
	"github.com/concourse/concourse/atc/db"
	"github.com/concourse/concourse/atc/db/lock"
	"github.com/concourse/concourse/atc/policy"
	"github.com/concourse/concourse/atc/resource"
	"github.com/concourse/concourse/atc/worker"
	"github.com/concourse/concourse/atc/worker/gclient"
	"github.com/concourse/concourse/atc/worker/image"
	"github.com/concourse/retryhttp"
	"github.com/cppforlife/go-semi-semantic/version"
)

func testWorkerProvider(
	dbConn db.Conn,
	lockFactory lock.LockFactory,
	compressionLib compression.Compression,
	gclient gclient.Client,
	bclient baggageclaim.Client,
) worker.WorkerProvider {
	dbResourceCacheFactory := db.NewResourceCacheFactory(dbConn, lockFactory)
	dbResourceConfigFactory := db.NewResourceConfigFactory(dbConn, lockFactory)
	resourceFetcher := worker.NewFetcher(
		clock.NewClock(),
		lockFactory,
		worker.NewFetchSourceFactory(dbResourceCacheFactory),
	)
	workerVersion, _ := version.NewVersionFromString("0.0.0-dev")
	return &TestWorkerProvider{
		gclient,
		bclient,
		lockFactory,
		retryhttp.NewExponentialBackOffFactory(5 * time.Minute),
		resourceFetcher,
		image.NewImageFactory(
			image.NewImageResourceFetcherFactory(
				resource.NewResourceFactory(),
				dbResourceCacheFactory,
				dbResourceConfigFactory,
				resourceFetcher,
			),
			compressionLib,
		),
		dbResourceCacheFactory,
		dbResourceConfigFactory,
		db.NewWorkerBaseResourceTypeFactory(dbConn),
		db.NewTaskCacheFactory(dbConn),
		db.NewWorkerTaskCacheFactory(dbConn),
		db.NewVolumeRepository(dbConn),
		db.NewTeamFactory(dbConn, lockFactory),
		db.NewWorkerFactory(dbConn),
		workerVersion,
		10 * time.Minute,
		5 * time.Minute,
		nil,
	}
}

type TestWorkerProvider struct {
	gclient                           gclient.Client
	bclient                           baggageclaim.Client
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
	volumeClient := worker.NewVolumeClient(
		provider.bclient,
		savedWorker,
		clock.NewClock(),
		provider.lockFactory,
		provider.dbVolumeRepository,
		provider.dbWorkerBaseResourceTypeFactory,
		provider.dbTaskCacheFactory,
		provider.dbWorkerTaskCacheFactory,
	)
	return worker.NewGardenWorker(
		provider.gclient,
		provider.dbVolumeRepository,
		volumeClient,
		provider.imageFactory,
		provider.resourceFetcher,
		provider.dbTeamFactory,
		savedWorker,
		provider.dbResourceCacheFactory,
		buildContainersCount,
		provider.policyChecker,
	)
}
