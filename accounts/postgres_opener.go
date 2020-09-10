package accounts

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
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
	return strings.Join(append(pc.parts, "sslmode=disable"), " ")
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
	envVarName := "CONCOURSE_POSTGRES_" + strings.ToUpper(paramName)
	var param Parameter
	for _, envVar := range container.Env {
		if envVar.Name == envVarName {
			if envVar.Value != "" {
				val := envVar.Value
				param = func(K8sClient) (string, error) {
					return val, nil
				}
			} else {
				secretName := envVar.ValueFrom.SecretKeyRef.LocalObjectReference.Name
				key := envVar.ValueFrom.SecretKeyRef.Key
				param = func(client K8sClient) (string, error) {
					return client.GetSecret(secretName, key)
				}
			}
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
