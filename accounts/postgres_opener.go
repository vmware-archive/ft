package accounts

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io/ioutil"
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
	connectionString, err := kwnipo.ConnectionString(pod)
	if err != nil {
		return nil, err
	}
	return sql.Open("postgres", connectionString)
}

type WebPod interface {
	Name() string
	PostgresParam(string) (Parameter, error)
}

func (kwnipo *K8sWebNodeInferredPostgresOpener) ConnectionString(
	pod WebPod,
) (string, error) {
	conn := postgresConnection{client: kwnipo.K8sClient}
	err := conn.Append("host", pod)
	if err != nil {
		return "", err
	}
	conn.Append("port", pod)
	err = conn.Append("user", pod)
	if err != nil {
		return "", err
	}
	err = conn.Append("password", pod)
	if err != nil {
		return "", err
	}
	conn.Append("sslrootcert", pod)
	conn.Append("sslkey", pod)
	conn.Append("sslcert", pod)
	err = conn.Append("sslmode", pod)
	if err != nil {
		conn.parts = append(conn.parts, "sslmode=disable")
	}
	return conn.String(), nil
}

type postgresConnection struct {
	client K8sClient
	parts  []string
}

func (pc *postgresConnection) Append(paramName string, pod WebPod) error {
	param, err := pod.PostgresParam(paramName)
	if err != nil {
		return err
	}
	val, err := param(pc.client)
	if err != nil {
		return err
	}
	pc.parts = append(pc.parts, fmt.Sprintf("%s=%s", paramName, val))
	return nil
}

func (pc *postgresConnection) String() string {
	return strings.Join(pc.parts, " ")
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
	envVarName := envVarName(paramName)
	var param Parameter
	for _, envVar := range container.Env {
		if envVar.Name == envVarName {
			param = wp.getParam(paramName, container, envVar)
		}
	}
	if param == nil {
		return param,
			fmt.Errorf("container '%s' does not have '%s' specified",
				container.Name,
				envVarName,
			)
	}
	return param, nil
}

func (wp *K8sWebPod) getParam(
	paramName string,
	container corev1.Container,
	envVar corev1.EnvVar,
) Parameter {
	switch paramName {
	case "sslrootcert", "sslcert", "sslkey":
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
		return func(client K8sClient) (string, error) {
			contents, err := client.GetSecret(secretName, key)
			if err != nil {
				return "", err
			}
			tmpfile, err := ioutil.TempFile("", paramName)
			if err != nil {
				return "", err
			}
			defer tmpfile.Close()
			_, err = tmpfile.Write([]byte(contents))
			if err != nil {
				return "", err
			}
			return tmpfile.Name(), nil
		}

	default:
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
}

func envVarName(paramName string) string {
	switch paramName {
	case "sslrootcert":
		return "CONCOURSE_POSTGRES_CA_CERT"
	case "sslcert":
		return "CONCOURSE_POSTGRES_CLIENT_CERT"
	case "sslkey":
		return "CONCOURSE_POSTGRES_CLIENT_KEY"
	default:
		return "CONCOURSE_POSTGRES_" + strings.ToUpper(paramName)
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

func (wp *K8sWebPod) Name() string {
	return wp.Pod.Name
}
