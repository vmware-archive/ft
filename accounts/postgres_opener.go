package accounts

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io/ioutil"
	"os"
	"strings"

	corev1 "k8s.io/api/core/v1"

	"github.com/concourse/flag"
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
	WebPod  WebPod
	PodName string
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
	defer pgConn.CleanupTempFiles()
	db, err := sql.Open("postgres", pgConn.String())
	if err != nil {
		return nil, err
	}
	err = db.Ping()
	if err != nil {
		return nil, err
	}
	return db, nil
}

// k8s API impl vs CRI exec impl
// corev1.Pod       spdyconn
type WebPod interface { // TODO rename WebNode -- should not be k8s-specific
	PostgresParam(string) (string, error)
	PostgresFile(string) (string, error)
}

func (kwnipo *K8sWebNodeInferredPostgresOpener) Connection(
	pod WebPod,
) (*PostgresConnection, error) {
	conn := PostgresConnection{}
	var err error
	conn.host, err = pod.PostgresParam("CONCOURSE_POSTGRES_HOST")
	if err != nil {
		return nil, err
	}
	// TODO why isn't there error handling here? (hint: there is a default)
	conn.port, _ = pod.PostgresParam("CONCOURSE_POSTGRES_PORT")
	conn.user, err = pod.PostgresParam("CONCOURSE_POSTGRES_USER")
	if err != nil {
		return nil, err
	}
	conn.password, err = pod.PostgresParam("CONCOURSE_POSTGRES_PASSWORD")
	if err != nil {
		return nil, err
	}
	conn.sslmode, err = pod.PostgresParam("CONCOURSE_POSTGRES_SSLMODE")
	if err != nil {
		conn.sslmode = "disable"
	}

	err = conn.determineRootCert(pod)
	if err != nil {
		return nil, err
	}
	sslcert, err := pod.PostgresFile("CONCOURSE_POSTGRES_CLIENT_CERT")
	if err != nil {
		return nil, err
	}
	sslkey, err := pod.PostgresFile("CONCOURSE_POSTGRES_CLIENT_KEY")
	if err != nil {
		return nil, err
	}
	if sslcert != "" && sslkey != "" {
		conn.sslcert, err = tempfileFromString(sslcert)
		if err != nil {
			return nil, err
		}
		conn.sslkey, err = tempfileFromString(sslkey)
		if err != nil {
			return nil, err
		}
	}

	return &conn, nil
}

func (pc *PostgresConnection) determineRootCert(pod WebPod) error {
	sslrootcert, err := pod.PostgresFile("CONCOURSE_POSTGRES_CA_CERT")
	if err != nil {
		return err
	}
	if sslrootcert != "" {
		pc.sslrootcert, err = tempfileFromString(sslrootcert)
		if err != nil {
			return err
		}
	}
	return nil
}

func tempfileFromString(contents string) (string, error) {
	tmpfile, err := ioutil.TempFile("", "")
	if err != nil {
		return "", err
	}
	_, err = tmpfile.Write([]byte(contents))
	if err != nil {
		return "", err
	}
	err = tmpfile.Close()
	if err != nil {
		return "", err
	}
	return tmpfile.Name(), nil
}

func (pc *PostgresConnection) CleanupTempFiles() {
	if pc.sslrootcert != "" {
		os.Remove(pc.sslrootcert)
	}
	if pc.sslcert != "" {
		os.Remove(pc.sslcert)
	}
	if pc.sslkey != "" {
		os.Remove(pc.sslkey)
	}
}

// TODO what if the database is different from the user
// TODO what about connection timeout

type PostgresConnection struct {
	host        string
	port        string
	user        string
	password    string
	sslmode     string
	sslrootcert string
	sslcert     string
	sslkey      string
}

func (pc *PostgresConnection) String() string {
	parts := []string{"host=" + pc.host}
	if pc.port != "" {
		parts = append(parts, "port="+pc.port)
	}
	parts = append(parts, "user="+pc.user)
	parts = append(parts, "password="+pc.password)
	parts = append(parts, "sslmode="+pc.sslmode)
	if pc.sslrootcert != "" {
		parts = append(parts, "sslrootcert="+pc.sslrootcert)
	}
	if pc.sslcert != "" {
		parts = append(parts, "sslcert="+pc.sslcert)
	}
	if pc.sslkey != "" {
		parts = append(parts, "sslkey="+pc.sslkey)
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

func (wp *K8sWebPod) PostgresFile(paramName string) (string, error) {
	container, err := findWebContainer(wp.Pod.Spec)
	if err != nil {
		return "", err
	}
	for _, envVar := range container.Env {
		if envVar.Name == paramName {
			secretName, key, err := wp.secretForParam(container, envVar.Value)
			if err != nil {
				return "", err
			}
			return wp.Client.GetSecret(secretName, key)
		}
	}
	return "", nil
}

func (wp *K8sWebPod) secretForParam(container corev1.Container, paramValue string) (string, string, error) {
	// find the VolumeMount whose MountPath is a prefix for paramValue
	var volumeMount *corev1.VolumeMount
	for _, vm := range container.VolumeMounts {
		if strings.HasPrefix(paramValue, vm.MountPath) {
			volumeMount = &vm
			break
		}
	}
	if volumeMount == nil {
		return "", "", fmt.Errorf(
			"container has no volume mounts matching '%s'",
			paramValue,
		)
	}
	var volume *corev1.Volume
	for _, v := range wp.Pod.Spec.Volumes {
		if v.Name == volumeMount.Name {
			volume = &v
			break
		}
	}
	if volume == nil {
		return "", "", fmt.Errorf(
			"pod has no volume named '%s'",
			volumeMount.Name,
		)
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
