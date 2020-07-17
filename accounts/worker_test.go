package accounts_test

import (
	"net"

	"code.cloudfoundry.org/garden"
	"code.cloudfoundry.org/garden/gardenfakes"
	"code.cloudfoundry.org/garden/server"
	"code.cloudfoundry.org/lager/lagertest"
	"github.com/concourse/ctop/accounts"
	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
	"github.com/onsi/gomega/gstruct"
)

var _ = Describe("LANWorker", func() {
	var (
		gardenServer *server.GardenServer
		backend      *gardenfakes.FakeBackend
		listener     net.Listener
	)
	BeforeEach(func() {
		backend = new(gardenfakes.FakeBackend)
		logger := lagertest.NewTestLogger("test")
		gardenServer = server.New(
			"tcp",
			"127.0.0.1:7777",
			0,
			backend,
			logger,
		)
		listener, _ = net.Listen("tcp", "127.0.0.1:7777")
		go gardenServer.Serve(listener)
	})
	AfterEach(func() {
		gardenServer.Stop()
		listener.Close()
	})
	It("lists containers", func() {
		fakeContainer := new(gardenfakes.FakeContainer)
		fakeContainer.HandleReturns("container-handle")
		backend.ContainersReturns([]garden.Container{fakeContainer}, nil)

		worker := accounts.NewLANWorker()
		containers, err := worker.Containers()

		Expect(err).NotTo(HaveOccurred())
		Expect(containers).To(ConsistOf(
			gstruct.MatchFields(gstruct.IgnoreExtras, gstruct.Fields{
				"Handle": Equal("container-handle"),
			}),
		))
	})
})
