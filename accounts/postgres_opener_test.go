package accounts_test

import (
	"errors"
	"io"
	"net/http"
	"net/http/httptest"

	"github.com/concourse/ft/accounts"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	restclient "k8s.io/client-go/rest"
	cmdtesting "k8s.io/kubectl/pkg/cmd/testing"
	"k8s.io/kubectl/pkg/scheme"
)

type PostgresOpenerSuite struct {
	suite.Suite
	*require.Assertions
}

func (s *PostgresOpenerSuite) TestInfersHostFromWebNode() {
	// stub k8s client such that web node has
	// 	CONCOURSE_POSTGRES_HOST=<local listener>
	namespace := "namespace"
	podName := "pod-name"
	podSpec := corev1.PodSpec{
		RestartPolicy: corev1.RestartPolicyAlways,
		DNSPolicy:     corev1.DNSClusterFirst,
		Containers: []corev1.Container{
			{
				Name: "helm-release-web",
				Env: []corev1.EnvVar{
					{
						Name:  "CONCOURSE_POSTGRES_HOST",
						Value: dbHost(),
					},
					{
						Name:  "CONCOURSE_POSTGRES_PORT",
						Value: "5432",
					},
					{
						Name:  "CONCOURSE_POSTGRES_USER",
						Value: "postgres",
					},
					{
						Name:  "CONCOURSE_POSTGRES_PASSWORD",
						Value: "password",
					},
				},
			},
		},
	}

	// construct K8sWebNodeInferredPostgresOpener
	//	with above k8s client
	fakeAPI := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch p, m := r.URL.Path, r.Method; {
		case p == "/api/v1/namespaces/"+namespace+"/pods/"+podName && m == "GET":
			body := cmdtesting.ObjBody(
				scheme.Codecs.LegacyCodec(scheme.Scheme.PrioritizedVersionsAllGroups()...),
				&corev1.Pod{
					ObjectMeta: metav1.ObjectMeta{
						Name:            podName,
						Namespace:       namespace,
						ResourceVersion: "10",
					},
					Spec: podSpec,
					Status: corev1.PodStatus{
						Phase: corev1.PodRunning,
					},
				})
			for k, vals := range cmdtesting.DefaultHeader() {
				for _, v := range vals {
					w.Header().Add(k, v)
				}
			}
			io.Copy(w, body)
		default:
			s.Failf("unexpected request", "%#v\n%#v", r.URL, r)
		}
	}))
	defer fakeAPI.Close()
	opener := &accounts.K8sWebNodeInferredPostgresOpener{
		RESTConfig: &restclient.Config{
			Host:    fakeAPI.URL,
			APIPath: "/api",
			ContentConfig: restclient.ContentConfig{
				NegotiatedSerializer: scheme.Codecs,
				ContentType:          runtime.ContentTypeJSON,
				GroupVersion:         &corev1.SchemeGroupVersion,
			},
		},
		Namespace: namespace,
		PodName:   podName,
	}
	// Call Open() on the opener
	db, err := opener.Open()
	s.NoError(err)
	err = db.Ping()
	s.NoError(err)
}

func (s *PostgresOpenerSuite) TestWebPodPostgresParamFailsWithNoContainers() {
	pod := &accounts.K8sWebPod{
		&corev1.Pod{
			Spec: corev1.PodSpec{
				Containers: []corev1.Container{},
			},
		},
	}
	_, err := pod.PostgresParam("param")
	s.EqualError(err, "could not find a 'web' container")
}

func (s *PostgresOpenerSuite) TestWebPodPostgresParamFailsWithoutWebContainer() {
	pod := &accounts.K8sWebPod{
		&corev1.Pod{
			Spec: corev1.PodSpec{
				Containers: []corev1.Container{
					{
						Name: "not-the-right-container",
					},
				},
			},
		},
	}
	_, err := pod.PostgresParam("param")
	s.EqualError(err, "could not find a 'web' container")
}

func (s *PostgresOpenerSuite) TestWebPodPostgresParamFailsWithMultipleWebContainers() {
	pod := &accounts.K8sWebPod{
		&corev1.Pod{
			Spec: corev1.PodSpec{
				Containers: []corev1.Container{
					{
						Name: "web",
					},
					{
						Name: "also-web",
					},
				},
			},
		},
	}
	_, err := pod.PostgresParam("param")
	s.EqualError(
		err,
		"found multiple 'web' containers",
	)
}

func (s *PostgresOpenerSuite) TestWebPodPostgresParamFailsWithMissingParam() {
	pod := &accounts.K8sWebPod{
		&corev1.Pod{
			Spec: corev1.PodSpec{
				Containers: []corev1.Container{
					{
						Name: "not-the-right-container",
					},
					{
						Name: "web",
					},
				},
			},
		},
	}
	_, err := pod.PostgresParam("param")
	s.EqualError(
		err,
		"container 'web' does not have 'CONCOURSE_POSTGRES_PARAM' specified",
	)
}

type testWebPod struct {
	name     string
	host     string
	port     string
	user     string
	password string
}

func (twp *testWebPod) PostgresParam(param string) (string, error) {
	var val string
	switch param {
	case "host":
		val = twp.host
	case "port":
		val = twp.port
	case "user":
		val = twp.user
	case "password":
		val = twp.password
	}
	if val == "" {
		return val, errors.New("foobar")
	}
	return val, nil
}

func (twp *testWebPod) Name() string {
	return twp.name
}

func (s *PostgresOpenerSuite) TestFailsWithMissingHost() {
	pod := &testWebPod{
		name:     "web",
		user:     "user",
		password: "password",
	}
	_, err := accounts.ConnectionString(pod)
	s.Error(err)
}

func (s *PostgresOpenerSuite) TestFailsWithMissingUser() {
	pod := &testWebPod{
		name:     "web",
		host:     "host",
		password: "password",
	}
	_, err := accounts.ConnectionString(pod)
	s.Error(err)
}

func (s *PostgresOpenerSuite) TestFailsWithMissingPassword() {
	pod := &testWebPod{
		name: "web",
		host: "host",
		user: "user",
	}
	_, err := accounts.ConnectionString(pod)
	s.Error(err)
}

func (s *PostgresOpenerSuite) TestOmitsPortWhenUnspecified() {
	pod := &testWebPod{
		name:     "web",
		host:     "1.2.3.4",
		user:     "postgres",
		password: "password",
	}
	connectionString, err := accounts.ConnectionString(pod)
	s.NoError(err)
	s.Equal(
		connectionString,
		"host=1.2.3.4 user=postgres password=password sslmode=disable",
	)
}
