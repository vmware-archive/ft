package accounts_test

import (
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"

	"code.cloudfoundry.org/garden"
	"code.cloudfoundry.org/garden/gardenfakes"
	"code.cloudfoundry.org/garden/server"
	"code.cloudfoundry.org/lager/lagertest"
	"github.com/concourse/ft/accounts"
	"github.com/concourse/ft/accounts/accountsfakes"
	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
	"github.com/onsi/gomega/gbytes"
	"github.com/onsi/gomega/gstruct"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/runtime"
	restclient "k8s.io/client-go/rest"
	runtimeapi "k8s.io/cri-api/pkg/apis/runtime/v1alpha2"
	"k8s.io/kubectl/pkg/scheme"
	"k8s.io/kubernetes/pkg/kubelet/server/streaming"
)

var _ = Describe("Worker", func() {
	Describe("LANWorker", func() {
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

			worker := accounts.GardenWorker{
				Dialer: &accounts.LANGardenDialer{},
			}
			containers, err := worker.Containers()

			Expect(err).NotTo(HaveOccurred())
			Expect(containers).To(ConsistOf(
				gstruct.MatchFields(gstruct.IgnoreExtras, gstruct.Fields{
					"Handle": Equal("container-handle"),
				}),
			))
		})
	})

	Describe("K8sGardenDialer", func() {
		It("successfully connects to a forwarded port", func() {
			buf := gbytes.NewBuffer()
			s, err := newTestStreamingServer()
			Expect(err).NotTo(HaveOccurred())
			s.fakeRuntime.PortForwardCalls(func(
				pod string,
				port int32,
				conn io.ReadWriteCloser,
			) error {
				io.Copy(buf, conn)
				return nil
			})

			dialer := &accounts.K8sGardenDialer{
				RESTConfig: &restclient.Config{
					Host:    s.testHTTPServer.URL,
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

			Expect(err).NotTo(HaveOccurred())
			conn.Write([]byte("hello world"))
			conn.Close()
			Eventually(buf).Should(gbytes.Say("hello world"))
		})
	})
})

//go:generate go run github.com/maxbrunsfeld/counterfeiter/v6 k8s.io/kubernetes/pkg/kubelet/server/streaming.Runtime

type testStreamingServer struct {
	streaming.Server
	fakeRuntime    *accountsfakes.FakeRuntime
	testHTTPServer *httptest.Server
}

func newTestStreamingServer() (s *testStreamingServer, err error) {
	s = &testStreamingServer{}
	s.testHTTPServer = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp, err := s.GetPortForward(&runtimeapi.PortForwardRequest{
			PodSandboxId: "foo",
			Port:         []int32{7777},
		})
		Expect(err).NotTo(HaveOccurred())
		testURL, err := url.Parse(resp.Url)
		Expect(err).NotTo(HaveOccurred())
		r.URL = testURL
		s.ServeHTTP(w, r)
	}))
	defer func() {
		if err != nil {
			s.testHTTPServer.Close()
		}
	}()

	testURL, err := url.Parse(s.testHTTPServer.URL)
	if err != nil {
		return nil, err
	}

	s.fakeRuntime = new(accountsfakes.FakeRuntime)
	config := streaming.DefaultConfig
	config.BaseURL = testURL
	s.Server, err = streaming.NewServer(config, s.fakeRuntime)
	if err != nil {
		return nil, err
	}
	return s, nil
}
