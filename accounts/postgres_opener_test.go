package accounts_test

import (
	"errors"
	"io"
	"io/ioutil"
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

type testk8sClient struct {
	pod     accounts.WebPod
	secrets map[string]map[string]string
}

func (tkc *testk8sClient) GetPod(name string) (accounts.WebPod, error) {
	return tkc.pod, nil
}

func (tkc *testk8sClient) GetSecret(name, key string) (string, error) {
	return tkc.secrets[name][key], nil
}

func (s *PostgresOpenerSuite) TestInfersPostgresConnectionFromWebNode() {
	opener := &accounts.K8sWebNodeInferredPostgresOpener{
		K8sClient: &testk8sClient{
			pod: &testWebPod{
				name:     "helm-release-web",
				host:     dbHost(),
				port:     "5432",
				user:     "postgres",
				password: "password",
			},
		},
		PodName: "pod-name",
	}

	db, err := opener.Open()
	s.NoError(err)
	err = db.Ping()
	s.NoError(err)
}

func (s *PostgresOpenerSuite) fakeAPI(path string, obj runtime.Object) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch p, m := r.URL.Path, r.Method; {
		case p == path && m == "GET":
			body := cmdtesting.ObjBody(
				scheme.Codecs.LegacyCodec(scheme.Scheme.PrioritizedVersionsAllGroups()...),
				obj,
			)
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
}

// TODO split out a separate k8s-related suite
func (s *PostgresOpenerSuite) TestGetPodFindsPod() {
	namespace := "namespace"
	podName := "pod-name"
	podSpec := corev1.PodSpec{
		RestartPolicy: corev1.RestartPolicyAlways,
		DNSPolicy:     corev1.DNSClusterFirst,
		Containers: []corev1.Container{
			{
				Name: "helm-release-web",
			},
		},
	}
	fakeAPI := s.fakeAPI(
		"/api/v1/namespaces/"+namespace+"/pods/"+podName,
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
		},
	)
	defer fakeAPI.Close()
	restConfig := &restclient.Config{
		Host:    fakeAPI.URL,
		APIPath: "/api",
		ContentConfig: restclient.ContentConfig{
			NegotiatedSerializer: scheme.Codecs,
			ContentType:          runtime.ContentTypeJSON,
			GroupVersion:         &corev1.SchemeGroupVersion,
		},
	}
	client := accounts.NewK8sClient(restConfig, namespace)

	pod, err := client.GetPod(podName)
	s.NoError(err)
	s.Equal(pod.Name(), podName)
}

func (s *PostgresOpenerSuite) TestWebPodPostgresParamLooksUpSecret() {
	secretName := "secret-name"
	secretKey := "postgresql-user"
	secretKeyRef := &corev1.SecretKeySelector{
		LocalObjectReference: corev1.LocalObjectReference{
			Name: secretName,
		},
		Key: secretKey,
	}
	env := []corev1.EnvVar{
		{
			Name: "CONCOURSE_POSTGRES_USER",
			ValueFrom: &corev1.EnvVarSource{
				SecretKeyRef: secretKeyRef,
			},
		},
	}
	pod := &accounts.K8sWebPod{
		&corev1.Pod{
			Spec: corev1.PodSpec{
				Containers: []corev1.Container{
					{
						Name: "helm-release-web",
						Env:  env,
					},
				},
			},
		},
	}

	userParam, err := pod.PostgresParam("user")
	s.NoError(err)
	userParamValue, err := userParam(&testk8sClient{
		secrets: map[string]map[string]string{
			secretName: map[string]string{
				secretKey: "username",
			},
		},
	})
	s.Equal(userParamValue, "username")
}

func (s *PostgresOpenerSuite) TestWebPodPostgresParamGetsCertFromSecret() {
	secretName := "secret-name"
	secretKey := "postgresql-ca-cert"
	volumeName := "keys-volume"
	container := corev1.Container{
		Name: "helm-release-web",
		Env: []corev1.EnvVar{
			{
				Name:  "CONCOURSE_POSTGRES_CA_CERT",
				Value: "/postgres-keys/ca.cert",
			},
		},
		VolumeMounts: []corev1.VolumeMount{
			{
				Name:      volumeName,
				MountPath: "/postgres-keys",
			},
		},
	}
	volume := corev1.Volume{Name: volumeName,
		VolumeSource: corev1.VolumeSource{
			Secret: &corev1.SecretVolumeSource{
				SecretName: secretName,
				Items: []corev1.KeyToPath{
					{
						Key:  secretKey,
						Path: "ca.cert",
					},
				},
			},
		},
	}
	pod := &accounts.K8sWebPod{
		&corev1.Pod{
			Spec: corev1.PodSpec{
				Containers: []corev1.Container{container},
				Volumes:    []corev1.Volume{volume},
			},
		},
	}

	caCertParam, err := pod.PostgresParam("sslrootcert")
	s.NoError(err)
	caCertParamValue, err := caCertParam(&testk8sClient{
		secrets: map[string]map[string]string{
			secretName: map[string]string{
				secretKey: "ssl cert",
			},
		},
	})
	contents, err := ioutil.ReadFile(caCertParamValue)
	s.NoError(err)
	s.Equal(string(contents), "ssl cert")
}

func (s *PostgresOpenerSuite) TestK8sClientLooksUpSecrets() {
	namespace := "namespace"
	secretName := "secret-name"
	fakeAPI := s.fakeAPI(
		"/api/v1/namespaces/"+namespace+"/secrets/"+secretName,
		&corev1.Secret{
			Data: map[string][]byte{
				"postgresql-user": []byte("user"),
			},
		},
	)
	defer fakeAPI.Close()
	restConfig := &restclient.Config{
		Host:    fakeAPI.URL,
		APIPath: "/api",
		ContentConfig: restclient.ContentConfig{
			NegotiatedSerializer: scheme.Codecs,
			ContentType:          runtime.ContentTypeJSON,
			GroupVersion:         &corev1.SchemeGroupVersion,
		},
	}
	client := accounts.NewK8sClient(restConfig, namespace)
	val, err := client.GetSecret(secretName, "postgresql-user")
	s.NoError(err)
	s.Equal(val, "user")
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
	sslmode  string
}

func (twp *testWebPod) PostgresParam(param string) (accounts.Parameter, error) {
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
	case "sslmode":
		val = twp.sslmode
	}
	if val == "" {
		return nil, errors.New("foobar")
	}
	return func(accounts.K8sClient) (string, error) { return val, nil }, nil
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
	opener := &accounts.K8sWebNodeInferredPostgresOpener{
		K8sClient: &testk8sClient{pod: pod},
		PodName:   "pod-name",
	}
	_, err := opener.ConnectionString(pod)
	s.Error(err)
}

func (s *PostgresOpenerSuite) TestFailsWithMissingUser() {
	pod := &testWebPod{
		name:     "web",
		host:     "host",
		password: "password",
	}
	opener := &accounts.K8sWebNodeInferredPostgresOpener{
		K8sClient: &testk8sClient{pod: pod},
		PodName:   "pod-name",
	}
	_, err := opener.ConnectionString(pod)
	s.Error(err)
}

func (s *PostgresOpenerSuite) TestFailsWithMissingPassword() {
	pod := &testWebPod{
		name: "web",
		host: "host",
		user: "user",
	}
	opener := &accounts.K8sWebNodeInferredPostgresOpener{
		K8sClient: &testk8sClient{pod: pod},
		PodName:   "pod-name",
	}
	_, err := opener.ConnectionString(pod)
	s.Error(err)
}

func (s *PostgresOpenerSuite) TestOmitsPortWhenUnspecified() {
	pod := &testWebPod{
		name:     "web",
		host:     "1.2.3.4",
		user:     "postgres",
		password: "password",
	}
	opener := &accounts.K8sWebNodeInferredPostgresOpener{
		K8sClient: &testk8sClient{pod: pod},
		PodName:   "pod-name",
	}
	connectionString, err := opener.ConnectionString(pod)
	s.NoError(err)
	s.Equal(
		connectionString,
		"host=1.2.3.4 user=postgres password=password sslmode=disable",
	)
}

func (s *PostgresOpenerSuite) TestReadsSSLMode() {
	pod := &testWebPod{
		name:     "web",
		host:     "1.2.3.4",
		user:     "postgres",
		password: "password",
		sslmode:  "verify-ca",
	}
	opener := &accounts.K8sWebNodeInferredPostgresOpener{
		K8sClient: &testk8sClient{pod: pod},
		PodName:   "pod-name",
	}
	connectionString, err := opener.ConnectionString(pod)
	s.NoError(err)
	s.Equal(
		connectionString,
		"host=1.2.3.4 user=postgres password=password sslmode=verify-ca",
	)
}
