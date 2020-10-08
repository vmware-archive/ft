package accounts_test

import (
	"net"

	"code.cloudfoundry.org/garden"
	"code.cloudfoundry.org/garden/gardenfakes"
	"code.cloudfoundry.org/garden/server"
	"code.cloudfoundry.org/lager/lagertest"
	"github.com/concourse/ft/accounts"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
)

type GardenConnectionSuite struct {
	suite.Suite
	*require.Assertions
	gardenServer *server.GardenServer
	backend      *gardenfakes.FakeBackend
	listener     net.Listener
}

func (s *GardenConnectionSuite) SetupTest() {
	s.backend = new(gardenfakes.FakeBackend)
	s.gardenServer = server.New(
		"tcp",
		"127.0.0.1:7777",
		0,
		s.backend,
		lagertest.NewTestLogger("test"),
	)
	s.listener, _ = net.Listen("tcp", "127.0.0.1:7777")
	go s.gardenServer.Serve(s.listener)
}

func (s *GardenConnectionSuite) TearDownTest() {
	s.gardenServer.Stop()
	s.listener.Close()
}

func (s *LANWorkerSuite) TestGetsAllMetrics() {
	metricsEntry := garden.ContainerMetricsEntry{
		Metrics: garden.Metrics{
			MemoryStat: garden.ContainerMemoryStat{
				TotalActiveAnon: 123,
			},
		},
		Err: nil,
	}
	s.backend.BulkMetricsReturns(map[string]garden.ContainerMetricsEntry{
		"container-handle": metricsEntry,
	}, nil)

	connection := accounts.GardenConnection{
		Dialer: &accounts.LANGardenDialer{},
	}
	metricsEntries, err := connection.AllMetrics()

	s.NoError(err)
	s.Len(metricsEntries, 1)
	s.Contains(metricsEntries, "container-handle")
	s.Equal(metricsEntry, metricsEntries["container-handle"])
}

// a container "exists" if it appears in the output from List
// BulkMetrics fails if you ask about nonexistent containers
// BulkMetrics actually returns metrics for the containers you asked about
