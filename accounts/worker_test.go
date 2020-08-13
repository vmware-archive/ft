package accounts_test

import (
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
	"github.com/concourse/ctop/accounts"
	"github.com/concourse/ctop/accounts/accountsfakes"
	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
	"github.com/onsi/gomega/gbytes"
	"github.com/onsi/gomega/gstruct"
	"k8s.io/client-go/tools/remotecommand"
	runtimeapi "k8s.io/cri-api/pkg/apis/runtime/v1alpha2"
	cmdtesting "k8s.io/kubectl/pkg/cmd/testing"
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
			s, err := newTestStreamingServer(0)
			Expect(err).NotTo(HaveOccurred())
			s.fakeRuntime.portForwardFunc = func(
				pod string,
				port int32,
				conn io.ReadWriteCloser,
			) error {
				io.Copy(buf, conn)
				return nil
			}
			resp, err := s.GetPortForward(&runtimeapi.PortForwardRequest{
				PodSandboxId: "foo",
				Port:         []int32{7777},
			})
			Expect(err).NotTo(HaveOccurred())
			testURL, err := url.Parse(resp.Url)
			Expect(err).NotTo(HaveOccurred())
			k8sConn := new(accountsfakes.FakeK8sConnection)
			k8sConn.URLReturns(testURL, nil)
			k8sConn.RESTConfigReturns(cmdtesting.DefaultClientConfig())
			dialer := accounts.K8sGardenDialer{
				Conn: k8sConn,
			}

			conn, err := dialer.Dial()

			Expect(err).NotTo(HaveOccurred())
			conn.Write([]byte("hello world"))
			conn.Close()
			Eventually(buf).Should(gbytes.Say("hello world"))
		})
	})
})

// TODO we can probably use a counterfeiter fake for this

type fakeRuntime struct {
	execFunc        func(string, []string, io.Reader, io.WriteCloser, io.WriteCloser, bool, <-chan remotecommand.TerminalSize) error
	attachFunc      func(string, io.Reader, io.WriteCloser, io.WriteCloser, bool, <-chan remotecommand.TerminalSize) error
	portForwardFunc func(string, int32, io.ReadWriteCloser) error
}

func (f *fakeRuntime) Exec(containerID string, cmd []string, stdin io.Reader, stdout, stderr io.WriteCloser, tty bool, resize <-chan remotecommand.TerminalSize) error {
	return f.execFunc(containerID, cmd, stdin, stdout, stderr, tty, resize)
}

func (f *fakeRuntime) Attach(containerID string, stdin io.Reader, stdout, stderr io.WriteCloser, tty bool, resize <-chan remotecommand.TerminalSize) error {
	return f.attachFunc(containerID, stdin, stdout, stderr, tty, resize)
}

func (f *fakeRuntime) PortForward(podSandboxID string, port int32, stream io.ReadWriteCloser) error {
	return f.portForwardFunc(podSandboxID, port, stream)
}

type testStreamingServer struct {
	streaming.Server
	fakeRuntime    *fakeRuntime
	testHTTPServer *httptest.Server
}

func newTestStreamingServer(streamIdleTimeout time.Duration) (s *testStreamingServer, err error) {
	s = &testStreamingServer{}
	s.testHTTPServer = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// TODO we can probably make this smart enough to take a
		// request to /api/v1/namespaces/ns/pods/pod/portforward
		// and actually do a GetPortForward on the underlying
		// StreamingServer, so that logic doesn't need to live in
		// the body of the test.
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

	s.fakeRuntime = &fakeRuntime{}
	config := streaming.DefaultConfig
	config.BaseURL = testURL
	if streamIdleTimeout != 0 {
		config.StreamIdleTimeout = streamIdleTimeout
	}
	s.Server, err = streaming.NewServer(config, s.fakeRuntime)
	if err != nil {
		return nil, err
	}
	return s, nil
}
