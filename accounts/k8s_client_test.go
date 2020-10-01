package accounts_test

import (
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

type K8sClientSuite struct {
	suite.Suite
	*require.Assertions
}

func (s *K8sClientSuite) fakeAPI(path string, obj runtime.Object) *httptest.Server {
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

func (s *K8sClientSuite) TestGetPodFindsPod() {
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
						Value: "example.com",
					},
				},
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
	webPod := &accounts.K8sWebPod{Pod: pod}
	host, err := webPod.ValueFromEnvVar("CONCOURSE_POSTGRES_HOST")
	s.NoError(err)
	s.Equal(host, "example.com")
}

func (s *K8sClientSuite) TestGetSecretLooksUpSecrets() {
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
