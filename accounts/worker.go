package accounts

import (
	"net"
	"net/http"
	"net/url"
	"time"

	"code.cloudfoundry.org/garden/client/connection"
	"code.cloudfoundry.org/lager"
	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/util/httpstream"
	"k8s.io/cli-runtime/pkg/genericclioptions"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/portforward"
	"k8s.io/client-go/transport/spdy"
	"k8s.io/kubectl/pkg/scheme"
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
	streamConn, _, err := spdy.NewDialer(
		upgrader,
		&http.Client{Transport: transport},
		"POST",
		url,
	).Dial(portforward.PortForwardProtocolV1Name)
	// TODO why should this error? Test
	if err != nil {
		return nil, err
	}
	headers := http.Header{}
	headers.Set(v1.StreamType, v1.StreamTypeData)
	headers.Set(v1.PortHeader, "7777")
	headers.Set(v1.PortForwardRequestIDHeader, "0")
	stream, err := streamConn.CreateStream(headers)
	// TODO why should this error? Test
	if err != nil {
		return nil, err
	}

	headers.Set(v1.StreamType, v1.StreamTypeError)
	streamConn.CreateStream(headers)
	return &StreamConn{streamConn, stream}, nil
}

//go:generate go run github.com/maxbrunsfeld/counterfeiter/v6 . K8sConnection

type K8sConnection interface {
	RESTConfig() *rest.Config
	URL() (*url.URL, error)
}

type systemK8sConnection struct {
	restConfig *rest.Config
	namespace  string
	podName    string
}

func NewK8sConnection(namespace, podName string) (K8sConnection, error) {
	restConfig, err := genericclioptions.
		NewConfigFlags(true).
		WithDeprecatedPasswordFlag().
		ToRESTConfig()
	if err != nil {
		return nil, err
	}
	restConfig.GroupVersion = &schema.GroupVersion{Group: "", Version: "v1"}
	restConfig.NegotiatedSerializer = scheme.Codecs.WithoutConversion()
	restConfig.APIPath = "/api"
	return &systemK8sConnection{
		restConfig: restConfig,
		namespace:  namespace,
		podName:    podName,
	}, nil
}

func (kc *systemK8sConnection) RESTConfig() *rest.Config {
	return kc.restConfig
}

func (kc *systemK8sConnection) URL() (*url.URL, error) {
	restClient, err := rest.RESTClientFor(kc.restConfig)
	if err != nil {
		return nil, err
	}
	return restClient.
		Post().
		Resource("pods").
		Namespace(kc.namespace).
		Name(kc.podName).
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
	return sc.stream.Write(p)
}

func (sc *StreamConn) Read(p []byte) (n int, err error) {
	return sc.stream.Read(p)
}

func (sc *StreamConn) Close() error {
	return sc.conn.Close()
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
