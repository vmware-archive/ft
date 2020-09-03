package accounts_test

import (
	"bytes"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"time"

	"code.cloudfoundry.org/garden"
	"code.cloudfoundry.org/garden/gardenfakes"
	"code.cloudfoundry.org/garden/server"
	"code.cloudfoundry.org/lager/lagertest"
	"github.com/concourse/ft/accounts"
	"github.com/concourse/ft/accounts/accountsfakes"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/runtime"
	restclient "k8s.io/client-go/rest"
	runtimeapi "k8s.io/cri-api/pkg/apis/runtime/v1alpha2"
	"k8s.io/kubectl/pkg/scheme"
	"k8s.io/kubernetes/pkg/kubelet/server/streaming"
)

type LANWorkerSuite struct {
	suite.Suite
	*require.Assertions
	gardenServer *server.GardenServer
	backend      *gardenfakes.FakeBackend
	listener     net.Listener
}

func (s *LANWorkerSuite) SetupTest() {
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

func (s *LANWorkerSuite) TearDownTest() {
	s.gardenServer.Stop()
	s.listener.Close()
}

func (s *LANWorkerSuite) TestLANWorkerListsContainers() {
	fakeContainer := new(gardenfakes.FakeContainer)
	fakeContainer.HandleReturns("container-handle")
	s.backend.ContainersReturns([]garden.Container{fakeContainer}, nil)

	worker := accounts.GardenWorker{
		Dialer: &accounts.LANGardenDialer{},
	}
	containers, err := worker.Containers()

	s.NoError(err)
	s.Len(containers, 1)
	s.Equal(containers[0].Handle, "container-handle")
}

type K8sGardenDialerSuite struct {
	suite.Suite
	*require.Assertions
}

func (s *K8sGardenDialerSuite) TestConnectsToForwardedPort() {
	buf := bytes.NewBuffer([]byte{})
	streamingServer, err := s.newTestStreamingServer()
	s.NoError(err)
	streamingServer.fakeRuntime.PortForwardCalls(func(
		pod string,
		port int32,
		conn io.ReadWriteCloser,
	) error {
		io.Copy(buf, conn)
		return nil
	})

	dialer := &accounts.K8sGardenDialer{
		RESTConfig: &restclient.Config{
			Host:    streamingServer.testHTTPServer.URL,
			APIPath: "/api",
			ContentConfig: restclient.ContentConfig{
				NegotiatedSerializer: scheme.Codecs,
				ContentType:          runtime.ContentTypeJSON,
				GroupVersion:         &corev1.SchemeGroupVersion,
			},
		},
		Namespace: "some-namespace",
		PodName:   "some-pod",
	}
	conn, err := dialer.Dial()

	s.NoError(err)
	conn.Write([]byte("hello world"))
	conn.Close()
	s.Eventually(
		func() bool {
			return buf.String() == "hello world"
		},
		time.Second,
		100*time.Millisecond,
	)
}

//go:generate go run github.com/maxbrunsfeld/counterfeiter/v6 k8s.io/kubernetes/pkg/kubelet/server/streaming.Runtime

type testStreamingServer struct {
	streaming.Server
	fakeRuntime    *accountsfakes.FakeRuntime
	testHTTPServer *httptest.Server
}

func (s *K8sGardenDialerSuite) newTestStreamingServer() (streamingServer *testStreamingServer, err error) {
	streamingServer = &testStreamingServer{}
	streamingServer.testHTTPServer = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp, err := streamingServer.GetPortForward(&runtimeapi.PortForwardRequest{
			PodSandboxId: "foo",
			Port:         []int32{7777},
		})
		s.NoError(err)
		testURL, err := url.Parse(resp.Url)
		s.NoError(err)
		r.URL = testURL
		streamingServer.ServeHTTP(w, r)
	}))
	defer func() {
		if err != nil {
			streamingServer.testHTTPServer.Close()
		}
	}()

	testURL, err := url.Parse(streamingServer.testHTTPServer.URL)
	if err != nil {
		return nil, err
	}

	streamingServer.fakeRuntime = new(accountsfakes.FakeRuntime)
	config := streaming.DefaultConfig
	config.BaseURL = testURL
	streamingServer.Server, err = streaming.NewServer(config, streamingServer.fakeRuntime)
	if err != nil {
		return nil, err
	}
	return streamingServer, nil
}
