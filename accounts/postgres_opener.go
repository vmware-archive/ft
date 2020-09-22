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
	WebPod    WebPod
	PodName   string
}

type K8sClient interface {
	GetPod(string) (*corev1.Pod, error)
	GetSecret(string, string) (string, error)
}

type k8sClient struct {
	RESTConfig *rest.Config
	Namespace  string
}

func (kc *k8sClient) GetPod(name string) (*corev1.Pod, error) {
	clientset, err := kubernetes.NewForConfig(kc.RESTConfig)
	if err != nil {
		return nil, err
	}
	return clientset.CoreV1().
		Pods(kc.Namespace).
		Get(context.Background(), name, metav1.GetOptions{})
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
	pgConn, err := kwnipo.Connection(kwnipo.WebPod)
	if err != nil {
		return nil, err
	}
	if pgConn.ShouldOverrideDefaultDialer() {
		return sql.OpenDB(pgConn), nil
	}
	return sql.Open("postgres", pgConn.String())
}

type WebPod interface {
	PostgresParam(string) (string, error)
	PostgresFile(string) (string, bool, error)
}

func (kwnipo *K8sWebNodeInferredPostgresOpener) Connection(
	pod WebPod,
) (*PostgresConnection, error) {
	conn := PostgresConnection{}
	var err error
	conn.host, err = conn.ReadParam("CONCOURSE_POSTGRES_HOST", pod)
	if err != nil {
		return nil, err
	}
	conn.port, _ = conn.ReadParam("CONCOURSE_POSTGRES_PORT", pod)
	conn.user, err = conn.ReadParam("CONCOURSE_POSTGRES_USER", pod)
	if err != nil {
		return nil, err
	}
	conn.password, err = conn.ReadParam("CONCOURSE_POSTGRES_PASSWORD", pod)
	if err != nil {
		return nil, err
	}
	conn.sslmode, err = conn.ReadParam("CONCOURSE_POSTGRES_SSLMODE", pod)
	if err != nil {
		conn.sslmode = "disable"
	}

	switch conn.sslmode {
	case "", "require", "verify-ca":
		conn.tlsConf.InsecureSkipVerify = true
	}
	err = conn.determineRootCert(pod)
	if err != nil {
		return nil, err
	}
	conn.tlsConf.Renegotiation = tls.RenegotiateFreelyAsClient
	sslcert, found, err := pod.PostgresFile("CONCOURSE_POSTGRES_CLIENT_CERT")
	if found && err != nil {
		return nil, err
	}
	sslkey, found, err := pod.PostgresFile("CONCOURSE_POSTGRES_CLIENT_KEY")
	if found && err != nil {
		return nil, err
	}
	if sslcert != "" && sslkey != "" {
		cert, err := tls.X509KeyPair([]byte(sslcert), []byte(sslkey))
		if err != nil {
			return nil, err
		}
		conn.tlsConf.Certificates = []tls.Certificate{cert}
	}

	return &conn, nil
}

func (pc *PostgresConnection) determineRootCert(pod WebPod) error {
	sslrootcert, found, err := pod.PostgresFile("CONCOURSE_POSTGRES_CA_CERT")
	if found {
		if err != nil {
			return err
		}
		pc.tlsConf.RootCAs = x509.NewCertPool()
		if !pc.tlsConf.RootCAs.AppendCertsFromPEM([]byte(sslrootcert)) {
			return errors.New("couldn't parse pem in sslrootcert")
		}
	}
	return nil
}

func (pc *PostgresConnection) Open(name string) (driver.Conn, error) {
	return pq.DialOpen(pc, name)
}

func (pc *PostgresConnection) Connect(_ context.Context) (driver.Conn, error) {
	return pq.DialOpen(pc, pc.String())
}

func (pc *PostgresConnection) Driver() driver.Driver {
	return pc
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
	host     string
	port     string
	user     string
	password string
	sslmode  string
	tlsConf  tls.Config
}

func (pc *PostgresConnection) ReadParam(paramName string, pod WebPod) (string, error) {
	return pod.PostgresParam(paramName)
}

func (pc *PostgresConnection) ShouldOverrideDefaultDialer() bool {
	return pc.tlsConf.RootCAs != nil || len(pc.tlsConf.Certificates) > 0
}

func (pc *PostgresConnection) String() string {
	parts := []string{"host=" + pc.host}
	if pc.port != "" {
		parts = append(parts, "port="+pc.port)
	}
	parts = append(parts, "user="+pc.user)
	parts = append(parts, "password="+pc.password)
	if pc.ShouldOverrideDefaultDialer() {
		parts = append(parts, "sslmode=disable")
	} else {
		parts = append(parts, "sslmode="+pc.sslmode)
	}
	return strings.Join(parts, " ")
}

type K8sWebPod struct {
	Pod    *corev1.Pod
	Client K8sClient
}

func (wp *K8sWebPod) PostgresParam(paramName string) (string, error) {
	container, err := findWebContainer(wp.Pod.Spec)
	if err != nil {
		return "", err
	}
	for _, envVar := range container.Env {
		if envVar.Name == paramName {
			return wp.getParam(envVar)
		}
	}
	return "",
		fmt.Errorf("container '%s' does not have '%s' specified",
			container.Name,
			paramName,
		)
}

func (wp *K8sWebPod) PostgresFile(paramName string) (string, bool, error) {
	container, err := findWebContainer(wp.Pod.Spec)
	if err != nil {
		return "", false, err
	}
	for _, envVar := range container.Env {
		if envVar.Name == paramName {
			secretName, key, err := wp.secretForParam(container, envVar.Value)
			if err != nil {
				return "", true, err
			}
			val, err := wp.Client.GetSecret(secretName, key)
			return val, true, err
		}
	}
	return "", false,
		fmt.Errorf("container '%s' does not have '%s' specified",
			container.Name,
			paramName,
		)
}

func (wp *K8sWebPod) secretForParam(container corev1.Container, paramValue string) (string, string, error) {
	// find the VolumeMount whose MountPath is a prefix for paramValue
	var volumeMount *corev1.VolumeMount
	for _, vm := range container.VolumeMounts {
		if strings.HasPrefix(paramValue, vm.MountPath) {
			volumeMount = &vm
		}
	}
	if volumeMount == nil {
		return "", "", fmt.Errorf(
			"container has no volume mounts matching '%s'",
			paramValue,
		)
	}
	var volume corev1.Volume
	// TODO: test when this fails -- an exotic k8s error?
	// find the Volume referenced by volumeMount
	for _, v := range wp.Pod.Spec.Volumes {
		if v.Name == volumeMount.Name {
			volume = v
		}
	}
	// TODO: test when this fails -- maybe filesystem paths don't get
	// constructed quite how you imagine? symlinks!?
	// find the item whose path matches the envVar
	var item corev1.KeyToPath
	for _, i := range volume.VolumeSource.Secret.Items {
		if paramValue == volumeMount.MountPath+"/"+i.Path {
			item = i
		}
	}
	secretName := volume.VolumeSource.Secret.SecretName
	key := item.Key
	return secretName, key, nil
}

func (wp *K8sWebPod) getParam(envVar corev1.EnvVar) (string, error) {
	if envVar.Value != "" {
		return envVar.Value, nil
	} else {
		secretKeyRef := envVar.ValueFrom.SecretKeyRef
		secretName := secretKeyRef.LocalObjectReference.Name
		key := secretKeyRef.Key
		return wp.Client.GetSecret(secretName, key)
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
