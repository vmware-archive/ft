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
