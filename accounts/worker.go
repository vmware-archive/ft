package accounts

import (
	"net"
	"net/http"
	"time"
	// "fmt"

	"code.cloudfoundry.org/lager"

	"k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/util/httpstream"
	"k8s.io/client-go/transport/spdy"

	"code.cloudfoundry.org/garden/client/connection"
	"k8s.io/client-go/tools/portforward"
	cmdutil "k8s.io/kubectl/pkg/cmd/util"
)

func NewLANWorker() Worker {
	return &LANWorker{}
}

type LANWorker struct {
}

func (lw *LANWorker) Containers(opts ...StatsOption) ([]Container, error) {
	handles, err := connection.New("tcp", "127.0.0.1:7777").List(nil)
	if err != nil {
		return nil, err
	}
	containers := []Container{}
	for _, handle := range handles {
		containers = append(containers, Container{Handle: handle})
	}
	return containers, nil
}

func NewK8sWorker(f cmdutil.Factory) Worker {
	return &K8sWorker{f: f}
}

type K8sWorker struct {
	f cmdutil.Factory
}

type StreamConn struct {
	httpstream.Stream
}

type StreamAddr struct {
}

func (sa *StreamAddr) Network() string {
	return "tcp"
}

func (sa *StreamAddr) String() string {
	return "127.0.0.1:7777"
}

func (sc *StreamConn) LocalAddr() net.Addr {
	return &StreamAddr{}
}

func (sc *StreamConn) RemoteAddr() net.Addr {
	return &StreamAddr{}
}

func (sc *StreamConn) SetDeadline(t time.Time) error {
	return nil
}

func (sc *StreamConn) SetReadDeadline(t time.Time) error {
	return nil
}

func (sc *StreamConn) SetWriteDeadline(t time.Time) error {
	return nil
}

func (kw *K8sWorker) Containers(opts ...StatsOption) ([]Container, error) {
	namespace := "ci"
	podName := "ci-worker-0"
	// TODO why should this error? Test
	restConfig, err := kw.f.ToRESTConfig()
	if err != nil {
		return nil, err
	}
	// TODO why should this error? Test
	transport, upgrader, err := spdy.RoundTripperFor(restConfig)
	if err != nil {
		return nil, err
	}
	// TODO why should this error? Test
	restClient, err := kw.f.RESTClient()
	if err != nil {
		return nil, err
	}
	dialer := spdy.NewDialer(
		upgrader,
		&http.Client{Transport: transport},
		"POST",
		restClient.
			Post().
			Resource("pods").
			Namespace(namespace).
			Name(podName).
			SubResource("portforward").
			URL(),
	)
	dialerFunc := func(network, address string) (net.Conn, error) {
		streamConn, _, err := dialer.Dial(portforward.PortForwardProtocolV1Name)
		// TODO why should this error? Test
		if err != nil {
			return nil, err
		}
		headers := http.Header{}
		headers.Set(v1.StreamType, v1.StreamTypeData)
		headers.Set(v1.PortHeader, "7777")

		// TODO do we need this:
		// headers.Set(v1.PortForwardRequestIDHeader, strconv.Itoa(requestID))

		stream, err := streamConn.CreateStream(headers)
		// TODO why should this error? Test
		if err != nil {
			return nil, err
		}
		return &StreamConn{stream}, nil
	}
	handles, err := connection.NewWithDialerAndLogger(
		dialerFunc,
		lager.NewLogger("garden-connection"),
	).List(nil)
	if err != nil {
		return nil, err
	}
	containers := []Container{}
	for _, handle := range handles {
		containers = append(containers, Container{Handle: handle})
	}
	return containers, nil
}
