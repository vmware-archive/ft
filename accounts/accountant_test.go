package accounts_test

import (
	"context"
	"os"
	"time"

	"code.cloudfoundry.org/lager/lagertest"
	"github.com/concourse/concourse/atc"
	"github.com/concourse/concourse/atc/creds/credsfakes"
	"github.com/concourse/concourse/atc/db"
	"github.com/concourse/concourse/atc/db/lock"
	"github.com/concourse/concourse/atc/engine"
	"github.com/concourse/concourse/atc/engine/builder"
	"github.com/concourse/concourse/atc/lidar"
	"github.com/concourse/concourse/atc/metric"
	"github.com/concourse/concourse/atc/postgresrunner"
	"github.com/concourse/concourse/atc/resource"
	"github.com/concourse/concourse/atc/worker"
	"github.com/concourse/concourse/atc/worker/workerfakes"
	"github.com/concourse/workloads/accounts"
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
	})

	AfterEach(func() {
		postgresRunner.DropTestDB()

		dbProcess.Signal(os.Interrupt)
		err := <-dbProcess.Wait()
		Expect(err).NotTo(HaveOccurred())
	})

	createResources := func(rs atc.ResourceConfigs) {
		team, _ := teamFactory.CreateTeam(atc.Team{Name: "t"})
		team.SavePipeline("p", atc.Config{Resources: rs}, 0, false)
	}

	checkResources := func() {
		//        using a modified stepfactory
		//          using a fake pool
		//          using a fake worker client
		fakePool := new(workerfakes.FakePool)
		fakeClient := new(workerfakes.FakeClient)
		cpu := uint64(1024)
		mem := uint64(1024)
		defaultLimits := atc.ContainerLimits{
			CPU:    &cpu,
			Memory: &mem,
		}
		stepFactory := builder.NewStepFactory(
			fakePool,
			fakeClient,
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
		accountant := accounts.NewDBAccountant()
		samples, _ := accountant.Account([]accounts.Container{accounts.Container{Handle: "some-handle"}})
		Expect(samples[0].Workloads[0].ToString()).To(Equal("t/p/r"))
		Expect(samples[0].Workloads[1].ToString()).To(Equal("t/p/s"))
	})
})
