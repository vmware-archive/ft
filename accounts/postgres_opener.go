package accounts

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"database/sql"
	"database/sql/driver"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
	"strings"
	"time"

	corev1 "k8s.io/api/core/v1"

	"github.com/concourse/flag"
	"github.com/lib/pq"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
)

type PostgresOpener interface {
	Open() (*sql.DB, error)
}

type StaticPostgresOpener struct {
	flag.PostgresConfig
}

func (spo *StaticPostgresOpener) Open() (*sql.DB, error) {
	return sql.Open("postgres", spo.ConnectionString())
}

type K8sWebNodeInferredPostgresOpener struct {
	K8sClient K8sClient
	PodName   string
}

type K8sClient interface {
	GetPod(string) (WebPod, error)
	GetSecret(string, string) (string, error)
}

type k8sClient struct {
	RESTConfig *rest.Config
	Namespace  string
}

func (kc *k8sClient) GetPod(name string) (WebPod, error) {
	clientset, err := kubernetes.NewForConfig(kc.RESTConfig)
	if err != nil {
		return nil, err
	}
	pod, err := clientset.CoreV1().
		Pods(kc.Namespace).
		Get(context.Background(), name, metav1.GetOptions{})
	if err != nil {
		return nil, err
	}
	return &K8sWebPod{pod}, nil
}

func (kc *k8sClient) GetSecret(name, key string) (string, error) {
	clientset, err := kubernetes.NewForConfig(kc.RESTConfig)
	if err != nil {
		return "", err
	}
	secret, err := clientset.CoreV1().
		Secrets(kc.Namespace).
		Get(context.Background(), name, metav1.GetOptions{})
	if err != nil {
		return "", err
	}
	return string(secret.Data[key]), nil
}

func NewK8sClient(restConfig *rest.Config, namespace string) K8sClient {
	return &k8sClient{
		RESTConfig: restConfig,
		Namespace:  namespace,
	}
}

func (kwnipo *K8sWebNodeInferredPostgresOpener) Open() (*sql.DB, error) {
	pod, err := kwnipo.K8sClient.GetPod(kwnipo.PodName)
	if err != nil {
		return nil, err
	}
	pgConn, err := kwnipo.Connection(pod)
	if err != nil {
		return nil, err
	}
	if pgConn.UsesTls() {
		sql.Register("postgrestls", &pgConn)
		return sql.Open("postgrestls", pgConn.String())
	}
	return sql.Open("postgres", pgConn.String())
}

type WebPod interface {
	PostgresParam(string) (Parameter, error)
	PostgresFile(string) (Parameter, error)
}

func (kwnipo *K8sWebNodeInferredPostgresOpener) Connection(
	pod WebPod,
) (PostgresConnection, error) {
	conn := PostgresConnection{client: kwnipo.K8sClient}
	var err error
	conn.host, err = conn.ReadParam("CONCOURSE_POSTGRES_HOST", pod)
	if err != nil {
		return PostgresConnection{}, err
	}
	conn.port, _ = conn.ReadParam("CONCOURSE_POSTGRES_PORT", pod)
	conn.user, err = conn.ReadParam("CONCOURSE_POSTGRES_USER", pod)
	if err != nil {
		return PostgresConnection{}, err
	}
	conn.password, err = conn.ReadParam("CONCOURSE_POSTGRES_PASSWORD", pod)
	if err != nil {
		return PostgresConnection{}, err
	}
	conn.sslmode, err = conn.ReadParam("CONCOURSE_POSTGRES_SSLMODE", pod)
	if err != nil {
		conn.sslmode = "disable"
	}

	switch conn.sslmode {
	case "", "require", "verify-ca":
		conn.tlsConf.InsecureSkipVerify = true
	}
	sslcert, err := conn.ReadFile("CONCOURSE_POSTGRES_CLIENT_CERT", pod)
	if err != nil {
		return conn, nil
	}
	sslkey, err := conn.ReadFile("CONCOURSE_POSTGRES_CLIENT_KEY", pod)
	if err != nil {
		return conn, nil
	}
	if sslcert != "" && sslkey != "" {
		cert, err := tls.X509KeyPair([]byte(sslcert), []byte(sslkey))
		if err != nil {
			return PostgresConnection{}, err
		}
		conn.tlsConf.Certificates = []tls.Certificate{cert}
	}
	sslrootcert, err := conn.ReadFile("CONCOURSE_POSTGRES_CA_CERT", pod)
	if err != nil {
		return conn, nil
	}
	if sslrootcert != "" {
		conn.tlsConf.RootCAs = x509.NewCertPool()
		if !conn.tlsConf.RootCAs.AppendCertsFromPEM([]byte(sslrootcert)) {
			return PostgresConnection{},
				errors.New("couldn't parse pem in sslrootcert")
		}
	}
	conn.tlsConf.Renegotiation = tls.RenegotiateFreelyAsClient
	conn.sslmode = "disable"

	return conn, nil
}

func (pc *PostgresConnection) Open(name string) (driver.Conn, error) {
	return pq.DialOpen(pc, name)
}

func (pc *PostgresConnection) dialer() *net.Dialer {
	return &net.Dialer{}
}

func (pc *PostgresConnection) Dial(network, address string) (net.Conn, error) {
	conn, err := pc.dialer().Dial(network, address)
	if err != nil {
		return nil, err
	}
	return pc.upgrade(conn)
}
func (pc *PostgresConnection) DialTimeout(network, address string, timeout time.Duration) (net.Conn, error) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	conn, err := pc.dialer().DialContext(ctx, network, address)
	if err != nil {
		return nil, err
	}
	return pc.upgrade(conn)
}
func (pc *PostgresConnection) DialContext(ctx context.Context, network, address string) (net.Conn, error) {
	conn, err := pc.dialer().DialContext(ctx, network, address)
	if err != nil {
		return nil, err
	}
	return pc.upgrade(conn)
}

func (pc *PostgresConnection) upgrade(conn net.Conn) (net.Conn, error) {
	// startup packet
	header := make([]byte, 4)
	binary.BigEndian.PutUint32(header, uint32(8))
	body := make([]byte, 4)
	binary.BigEndian.PutUint32(body, uint32(80877103))
	_, err := conn.Write(append(header, body...))
	if err != nil {
		return nil, err
	}

	// read the response, check if it starts with 'S'
	buf := make([]byte, 1)
	_, err = io.ReadFull(conn, buf)
	if err != nil {
		return nil, err
	}
	if buf[0] != 'S' {
		return nil, errors.New("SSL is not enabled on the server")
	}

	// create the tls.Client using conn
	return tls.Client(conn, &pc.tlsConf), nil
}

type PostgresConnection struct {
	client     K8sClient
	host       string
	port       string
	user       string
	password   string
	sslmode    string
	tlsConf    tls.Config
}

func (pc *PostgresConnection) ReadParam(paramName string, pod WebPod) (string, error) {
	param, err := pod.PostgresParam(paramName)
	if err != nil {
		return "", err
	}
	return param(pc.client)
}

func (pc *PostgresConnection) ReadFile(paramName string, pod WebPod) (string, error) {
	param, err := pod.PostgresFile(paramName)
	if err != nil {
		return "", err
	}
	return param(pc.client)
}

func (pc *PostgresConnection) UsesTls() bool {
	return pc.tlsConf.RootCAs != nil || len(pc.tlsConf.Certificates) > 0
}

func (pc *PostgresConnection) String() string {
	parts := []string{"host=" + pc.host}
	if pc.port != "" {
		parts = append(parts, "port="+pc.port)
	}
	parts = append(parts, "user="+pc.user)
	parts = append(parts, "password="+pc.password)
	parts = append(parts, "sslmode="+pc.sslmode)
	return strings.Join(parts, " ")
}

type K8sWebPod struct {
	*corev1.Pod
}

type Parameter func(K8sClient) (string, error)

func (wp *K8sWebPod) PostgresParam(paramName string) (Parameter, error) {
	container, err := findWebContainer(wp.Spec)
	if err != nil {
		return nil, err
	}
	var param Parameter
	for _, envVar := range container.Env {
		if envVar.Name == paramName {
			param = wp.getParam(envVar)
		}
	}
	if param == nil {
		return param,
			fmt.Errorf("container '%s' does not have '%s' specified",
				container.Name,
				paramName,
			)
	}
	return param, nil
}

func (wp *K8sWebPod) PostgresFile(paramName string) (Parameter, error) {
	container, err := findWebContainer(wp.Spec)
	if err != nil {
		return nil, err
	}
	var param Parameter
	for _, envVar := range container.Env {
		if envVar.Name == paramName {
			// find the VolumeMount whose MountPath is a prefix for envVar.Value
			var volumeMount corev1.VolumeMount
			for _, vm := range container.VolumeMounts {
				if strings.HasPrefix(envVar.Value, vm.MountPath) {
					volumeMount = vm
				}
			}
			var volume corev1.Volume
			// find the Volume referenced by volumeMount
			for _, v := range wp.Spec.Volumes {
				if v.Name == volumeMount.Name {
					volume = v
				}
			}
			// find the item whose path matches the envVar
			var item corev1.KeyToPath
			for _, i := range volume.VolumeSource.Secret.Items {
				if envVar.Value == volumeMount.MountPath+"/"+i.Path {
					item = i
				}
			}
			secretName := volume.VolumeSource.Secret.SecretName
			key := item.Key
			param = func(client K8sClient) (string, error) {
				return client.GetSecret(secretName, key)
			}
		}
	}
	if param == nil {
		return param,
			fmt.Errorf("container '%s' does not have '%s' specified",
				container.Name,
				paramName,
			)
	}
	return param, nil
}

func (wp *K8sWebPod) getParam(envVar corev1.EnvVar) Parameter {
	if envVar.Value != "" {
		val := envVar.Value
		return func(K8sClient) (string, error) {
			return val, nil
		}
	} else {
		secretKeyRef := envVar.ValueFrom.SecretKeyRef
		secretName := secretKeyRef.LocalObjectReference.Name
		key := secretKeyRef.Key
		return func(client K8sClient) (string, error) {
			return client.GetSecret(secretName, key)
		}
	}
}

func findWebContainer(spec corev1.PodSpec) (corev1.Container, error) {
	var (
		container corev1.Container
		found     bool
	)
	for _, c := range spec.Containers {
		if strings.Contains(c.Name, "web") {
			if found {
				return container,
					errors.New("found multiple 'web' containers")
			}
			container = c
			found = true
		}
	}
	if found {
		return container, nil
	}
	return container, errors.New("could not find a 'web' container")
}