package accounts

import (
	"net"
	"net/http"
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
	RESTConfig *rest.Config
	Namespace  string
	PodName    string
}

func (kgd *K8sGardenDialer) Dial() (net.Conn, error) {
	transport, upgrader, err := spdy.RoundTripperFor(kgd.RESTConfig)
	if err != nil {
		return nil, err
	}
	restClient, err := rest.RESTClientFor(kgd.RESTConfig)
	if err != nil {
		return nil, err
	}
	url := restClient.
		Post().
		Resource("pods").
		Namespace(kgd.Namespace).
		Name(kgd.PodName).
		SubResource("portforward").
		URL()
	if err != nil {
		return nil, err
	}
	streamConn, _, err := spdy.NewDialer(
		upgrader,
		&http.Client{Transport: transport},
		"POST",
		url,
	).Dial(portforward.PortForwardProtocolV1Name)
	if err != nil {
		return nil, err
	}
	headers := http.Header{}
	headers.Set(v1.StreamType, v1.StreamTypeData)
	headers.Set(v1.PortHeader, "7777")
	headers.Set(v1.PortForwardRequestIDHeader, "0")
	stream, err := streamConn.CreateStream(headers)
	if err != nil {
		return nil, err
	}

	headers.Set(v1.StreamType, v1.StreamTypeError)
	streamConn.CreateStream(headers)
	return &StreamConn{streamConn, stream}, nil
}

func RESTConfig() (*rest.Config, error) {
	restConfig, err := genericclioptions.
		NewConfigFlags(true).
		WithDeprecatedPasswordFlag().
		ToRESTConfig()
	if err != nil {
		return restConfig, err
	}
	restConfig.GroupVersion = &schema.GroupVersion{Group: "", Version: "v1"}
	restConfig.NegotiatedSerializer = scheme.Codecs.WithoutConversion()
	restConfig.APIPath = "/api"
	return restConfig, nil
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
