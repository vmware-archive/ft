package accounts

import (
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"time"

	"code.cloudfoundry.org/lager"

	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/util/httpstream"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/transport/spdy"

	"code.cloudfoundry.org/garden/client/connection"
	"k8s.io/client-go/tools/portforward"
)

type GardenWorker struct {
	Dialer GardenDialer
}

func (gw *GardenWorker) Containers(opts ...StatsOption) ([]Container, error) {
	handles, err := connection.NewWithDialerAndLogger(
		func(string, string) (net.Conn, error) {
			return gw.Dialer.Dial()
		},
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

type GardenDialer interface {
	Dial() (net.Conn, error)
}

type LANGardenDialer struct{}

func (lgd *LANGardenDialer) Dial() (net.Conn, error) {
	return net.Dial("tcp", "127.0.0.1:7777")
}

type K8sGardenDialer struct {
	Conn K8sConnection
}

func (kgd *K8sGardenDialer) Dial() (net.Conn, error) {
	// TODO why should this error? Test
	transport, upgrader, err := spdy.RoundTripperFor(kgd.Conn.RESTConfig())
	if err != nil {
		return nil, err
	}
	// TODO why should this error? Test
	url, err := kgd.Conn.URL()
	if err != nil {
		return nil, err
	}
	dialer := spdy.NewDialer(
		upgrader,
		&http.Client{Transport: transport},
		"POST",
		url,
	)
	streamConn, _, err := dialer.Dial(portforward.PortForwardProtocolV1Name)
	// TODO why should this error? Test
	if err != nil {
		return nil, err
	}
	headers := http.Header{}
	headers.Set(v1.StreamType, v1.StreamTypeData)
	headers.Set(v1.PortHeader, "7777")

	// TODO do we need this:
	headers.Set(v1.PortForwardRequestIDHeader, strconv.Itoa(0))

	stream, err := streamConn.CreateStream(headers)
	headers.Set(v1.StreamType, v1.StreamTypeError)
	errorStream, err := streamConn.CreateStream(headers)
	// TODO why should this error? Test
	if err != nil {
		return nil, err
	}
	go io.Copy(errorStream, os.Stdout)
	return &StreamConn{streamConn, stream}, nil
}

//go:generate go run github.com/maxbrunsfeld/counterfeiter/v6 . K8sConnection

type K8sConnection interface {
	RESTConfig() *rest.Config
	URL() (*url.URL, error)
}

type systemK8sConnection struct {
	restConfig *rest.Config
}

func NewK8sConnection(restConfig *rest.Config) K8sConnection {
	return &systemK8sConnection{restConfig}
}

func (kc *systemK8sConnection) RESTConfig() *rest.Config {
	return kc.restConfig
}

func (kc *systemK8sConnection) URL() (*url.URL, error) {
	namespace := "ci"
	podName := "ci-worker-0"
	restClient, err := rest.RESTClientFor(kc.restConfig)
	if err != nil {
		return nil, err
	}
	return restClient.
		Post().
		Resource("pods").
		Namespace(namespace).
		Name(podName).
		SubResource("portforward").
		URL(), nil
}

type StreamConn struct {
	conn   httpstream.Connection
	stream httpstream.Stream
}

type StreamAddr struct {
}

func (sa *StreamAddr) Network() string {
	return "tcp"
}

func (sa *StreamAddr) String() string {
	return "127.0.0.1:7777"
}

func (sc *StreamConn) Write(p []byte) (n int, err error) {
	fmt.Println("Write", string(p))
	return sc.stream.Write(p)
}

func (sc *StreamConn) Read(p []byte) (n int, err error) {
	fmt.Println("Read", string(p))
	return sc.stream.Read(p)
}

func (sc *StreamConn) Close() error {
	fmt.Println("Close")
	return sc.conn.Close()
}

func (sc *StreamConn) LocalAddr() net.Addr {
	fmt.Println("LocalAddr")
	return &StreamAddr{}
}

func (sc *StreamConn) RemoteAddr() net.Addr {
	fmt.Println("RemoteAddr")
	return &StreamAddr{}
}

func (sc *StreamConn) SetDeadline(t time.Time) error {
	fmt.Println("SetDeadline", t)
	return nil
}

func (sc *StreamConn) SetReadDeadline(t time.Time) error {
	fmt.Println("SetReadDeadline", t)
	return nil
}

func (sc *StreamConn) SetWriteDeadline(t time.Time) error {
	fmt.Println("SetReadDeadline", t)
	return nil
}
