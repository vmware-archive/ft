package accounts

import (
	"context"
	"database/sql"
	"fmt"

	sq "github.com/Masterminds/squirrel"
	"github.com/concourse/flag"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
)

type AccountantFactory func(Command) Accountant

var DefaultAccountantFactory = func(cmd Command) Accountant {
	return &DBAccountant{Opener: &StaticPostgresOpener{cmd.Postgres}}
}

type DBAccountant struct {
	Opener PostgresOpener
}

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
	// connectionFinder func(corev1.Pod) string
	// TODO: ^ drive this collaborator with unit tests
}

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
	// find the 'web' container
	envVars := pod.Spec.Containers[0].Env
	// find the relevant postgres config
	// find host env var
	host := findEnvVar("CONCOURSE_POSTGRES_HOST", envVars)
	// find port env var
	port := findEnvVar("CONCOURSE_POSTGRES_PORT", envVars)
	// open a connection
	connectionString := fmt.Sprintf(
		"host=%s port=%s sslmode=disable user=postgres password=password",
		host,
		port,
	)
	return sql.Open("postgres", connectionString)
}

func findEnvVar(name string, envVars []corev1.EnvVar) string {
	for _, envVar := range envVars {
		if envVar.Name == name {
			return envVar.Value
		}
	}
	return ""
}

func (da *DBAccountant) Account(containers []Container) ([]Sample, error) {
	conn, err := da.Opener.Open()
	if err != nil {
		return nil, err
	}
	defer conn.Close()

	samples := []Sample{}
	resourceSamples, err := resourceSamples(conn, containers)
	if err != nil {
		return nil, err
	}
	samples = append(samples, resourceSamples...)
	buildSamples, err := buildSamples(conn, containers)
	if err != nil {
		return nil, err
	}
	samples = append(samples, buildSamples...)

	return samples, nil
}

func filterHandles(containers []Container) sq.Eq {
	handles := []string{}
	for _, container := range containers {
		handles = append(handles, container.Handle)
	}
	return sq.Eq{"c.handle": handles}
}
