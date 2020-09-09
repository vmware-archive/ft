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
	RESTConfig *rest.Config
	Namespace  string
	PodName    string
}

// TODO:
// 1. refactor to replace K8sWebNodeInferredPostgresOpener.RESTConfig with an
//	adapter pattern like this
//	type k8sclient interface {
//		GetPod(string) (K8sPod, error)
//		GetSecretValue(string, string) (string, error)
//	}
// 2. stub out k8sclient to return suitable pods and secrets

func (kwnipo *K8sWebNodeInferredPostgresOpener) Open() (*sql.DB, error) {
	clientset, err := kubernetes.NewForConfig(kwnipo.RESTConfig)
	if err != nil {
		return nil, err
	}
	pod, err := clientset.CoreV1().
		Pods(kwnipo.Namespace).
		Get(context.Background(), kwnipo.PodName, metav1.GetOptions{})
	if err != nil {
		return nil, err
	}
	connectionString, err := ConnectionString(&K8sWebPod{pod})
	if err != nil {
		fmt.Println(pod.Spec.Containers)
		return nil, err
	}
	return sql.Open("postgres", connectionString)
}

type WebPod interface {
	Name() string
	PostgresParam(string) (string, error)
}

func ConnectionString(pod WebPod) (string, error) {
	conn := postgresConnection{}
	err := conn.Required("host", pod)
	if err != nil {
		return "", err
	}
	conn.Optional("port", pod)
	err = conn.Required("user", pod)
	if err != nil {
		return "", err
	}
	err = conn.Required("password", pod)
	if err != nil {
		return "", err
	}
	return conn.String(), nil
}

type postgresConnection struct {
	parts []string
}

func (pc *postgresConnection) Required(param string, pod WebPod) error {
	val, err := pod.PostgresParam(param)
	if err != nil {
		return err
	}
	pc.parts = append(pc.parts, fmt.Sprintf("%s=%s", param, val))
	return nil
}

func (pc *postgresConnection) Optional(param string, pod WebPod) {
	val, _ := pod.PostgresParam(param)
	if val == "" {
		return
	}
	pc.parts = append(pc.parts, fmt.Sprintf("%s=%s", param, val))
}

func (pc *postgresConnection) String() string {
	return strings.Join(append(pc.parts, "sslmode=disable"), " ")
}

type K8sWebPod struct {
	*corev1.Pod
}

func (wp *K8sWebPod) PostgresParam(param string) (string, error) {
	envVar := "CONCOURSE_POSTGRES_" + strings.ToUpper(param)
	container, err := findWebContainer(wp.Spec)
	if err != nil {
		return "", err
	}
	val := findEnvVar(envVar, container)
	if val == "" {
		return val, fmt.Errorf("container '%s' does not have '%s' specified",
			container.Name,
			envVar,
		)
	}
	return val, nil
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

func findEnvVar(name string, container corev1.Container) string {
	for _, envVar := range container.Env {
		if envVar.Name == name {
			return envVar.Value
		}
	}
	return ""
}
