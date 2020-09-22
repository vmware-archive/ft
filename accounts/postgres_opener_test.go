package accounts_test

import (
	"bytes"
	"crypto/rand"
	"crypto/rsa"
	"crypto/tls"
	"crypto/x509"
	"encoding/binary"
	"encoding/pem"
	"errors"
	"io"
	"math/big"
	"net"
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

func (tkc *testk8sClient) GetPod(name string) (*corev1.Pod, error) {
	return nil, nil
}

func (tkc *testk8sClient) GetSecret(name, key string) (string, error) {
	return tkc.secrets[name][key], nil
}

func (s *PostgresOpenerSuite) fakePostgres(tlsConf *tls.Config) (string, net.Listener) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	s.NoError(err)
	_, port, err := net.SplitHostPort(ln.Addr().String())
	s.NoError(err)
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				continue
			}
			// if client expects SSL, magic SSL number
			buf := make([]byte, 8)
			conn.Read(buf)
			header := make([]byte, 4)
			binary.BigEndian.PutUint32(header, uint32(8))
			body := make([]byte, 4)
			binary.BigEndian.PutUint32(body, uint32(80877103))
			if bytes.Equal(buf, append(header, body...)) {
				conn.Write([]byte{'S'})
				conn = tls.Server(conn, tlsConf)
			}
			// tell the client SSL is enabled, if they asked
			// upgrade connection tls.Server(conn,config)
			// read until you see two null chars, which means
			// the initial connection message is over
			for {
				buf := make([]byte, 1)
				conn.Read(buf)
				if buf[0] == '\000' {
					conn.Read(buf)
					if buf[0] == '\000' {
						break
					}
				}
			}
			// tell that you're ready for a query
			size := make([]byte, 4)
			binary.BigEndian.PutUint32(size, uint32(5))
			header = append([]byte{'Z'}, size...)
			conn.Write(append(header, 'I'))
			// read the simple query ";" that pq uses to ping;
			// it happens to be 7 bytes long
			buf = make([]byte, 7)
			conn.Read(buf)
			// I think 'I' means no results
			size = make([]byte, 4)
			binary.BigEndian.PutUint32(size, uint32(4))
			conn.Write(append([]byte{'I'}, size...))
			// Then 'Z' means done
			size = make([]byte, 4)
			binary.BigEndian.PutUint32(size, uint32(5))
			header = append([]byte{'Z'}, size...)
			conn.Write(append(header, 'I'))
		}
	}()
	return port, ln
}

func (s *PostgresOpenerSuite) TestInfersPostgresConnectionFromWebNode() {
	port, pg := s.fakePostgres(nil)
	defer pg.Close()
	opener := &accounts.K8sWebNodeInferredPostgresOpener{
		WebPod: &testWebPod{
			name:     "helm-release-web",
			host:     "127.0.0.1",
			port:     port,
			user:     "postgres",
			password: "password",
		},
		PodName: "pod-name",
	}

	db, err := opener.Open()
	s.NoError(err)
	err = db.Ping()
	s.NoError(err)
}

func (s *PostgresOpenerSuite) generateKeyPair() ([]byte, []byte) {
	priv, err := rsa.GenerateKey(rand.Reader, 4096)
	s.NoError(err)

	serialNumberLimit := new(big.Int).Lsh(big.NewInt(1), 128)
	serialNumber, err := rand.Int(rand.Reader, serialNumberLimit)
	s.NoError(err)

	template := x509.Certificate{
		SerialNumber: serialNumber,
	}

	derBytes, err := x509.CreateCertificate(rand.Reader, &template, &template, &priv.PublicKey, priv)
	s.NoError(err)
	certBlock := pem.EncodeToMemory(&pem.Block{
		Type:  "CERTIFICATE",
		Bytes: derBytes,
	})
	keyBlock := pem.EncodeToMemory(&pem.Block{
		Type:  "PRIVATE KEY",
		Bytes: x509.MarshalPKCS1PrivateKey(priv),
	})
	return certBlock, keyBlock
}

func (s *PostgresOpenerSuite) TestInfersTLSConfigFromWebNode() {
	certBlock, keyBlock := s.generateKeyPair()
	cert, err := tls.X509KeyPair(certBlock, keyBlock)
	s.NoError(err)
	tlsConf := &tls.Config{Certificates: []tls.Certificate{cert}}
	port, pg := s.fakePostgres(tlsConf)
	defer pg.Close()
	opener := &accounts.K8sWebNodeInferredPostgresOpener{
		WebPod: &testWebPod{
			name:        "helm-release-web",
			host:        "127.0.0.1",
			port:        port,
			user:        "postgres",
			password:    "password",
			sslmode:     "verify-ca",
			sslrootcert: string(certBlock),
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
	host, err := webPod.PostgresParam("CONCOURSE_POSTGRES_HOST")
	s.NoError(err)
	s.Equal(host, "example.com")
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
		Pod: &corev1.Pod{
			Spec: corev1.PodSpec{
				Containers: []corev1.Container{
					{
						Name: "helm-release-web",
						Env:  env,
					},
				},
			},
		},
		Client: &testk8sClient{
			secrets: map[string]map[string]string{
				secretName: map[string]string{
					secretKey: "username",
				},
			},
		},
	}

	userParam, err := pod.PostgresParam("CONCOURSE_POSTGRES_USER")
	s.NoError(err)
	s.Equal(userParam, "username")
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
		Pod: &corev1.Pod{
			Spec: corev1.PodSpec{
				Containers: []corev1.Container{container},
				Volumes:    []corev1.Volume{volume},
			},
		},
		Client: &testk8sClient{
			secrets: map[string]map[string]string{
				secretName: map[string]string{
					secretKey: "ssl cert",
				},
			},
		},
	}

	fileContents, _, err := pod.PostgresFile("CONCOURSE_POSTGRES_CA_CERT")
	s.NoError(err)
	s.Equal(fileContents, "ssl cert")
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
		Pod: &corev1.Pod{
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
		Pod: &corev1.Pod{
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
		Pod: &corev1.Pod{
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
		Pod: &corev1.Pod{
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
	_, err := pod.PostgresParam("PARAM")
	s.EqualError(
		err,
		"container 'web' does not have 'PARAM' specified",
	)
}

type testWebPod struct {
	name        string
	host        string
	port        string
	user        string
	password    string
	sslmode     string
	sslrootcert string
	sslkey      string
	sslcert     string
}

func (twp *testWebPod) PostgresParam(param string) (string, error) {
	var val string
	switch param {
	case "CONCOURSE_POSTGRES_HOST":
		val = twp.host
	case "CONCOURSE_POSTGRES_PORT":
		val = twp.port
	case "CONCOURSE_POSTGRES_USER":
		val = twp.user
	case "CONCOURSE_POSTGRES_PASSWORD":
		val = twp.password
	case "CONCOURSE_POSTGRES_SSLMODE":
		val = twp.sslmode
	}
	if val == "" {
		return "", errors.New("foobar")
	}
	return val, nil
}

func (twp *testWebPod) PostgresFile(param string) (string, bool, error) {
	var val string
	switch param {
	case "CONCOURSE_POSTGRES_CA_CERT":
		val = twp.sslrootcert
	case "CONCOURSE_POSTGRES_CLIENT_CERT":
		val = twp.sslcert
	case "CONCOURSE_POSTGRES_CLIENT_KEY":
		val = twp.sslkey
	}
	if val != "" {
		return val, true, nil
	}
	return "", false, errors.New("foobar")
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
		WebPod:    pod,
		PodName:   "pod-name",
	}
	_, err := opener.Connection(pod)
	s.Error(err)
}

func (s *PostgresOpenerSuite) TestFailsWithMissingUser() {
	pod := &testWebPod{
		name:     "web",
		host:     "host",
		password: "password",
	}
	opener := &accounts.K8sWebNodeInferredPostgresOpener{
		WebPod:    pod,
		PodName:   "pod-name",
	}
	_, err := opener.Connection(pod)
	s.Error(err)
}

func (s *PostgresOpenerSuite) TestFailsWithMissingPassword() {
	pod := &testWebPod{
		name: "web",
		host: "host",
		user: "user",
	}
	opener := &accounts.K8sWebNodeInferredPostgresOpener{
		WebPod:    pod,
		PodName:   "pod-name",
	}
	_, err := opener.Connection(pod)
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
		WebPod:    pod,
		PodName:   "pod-name",
	}
	connection, err := opener.Connection(pod)
	s.NoError(err)
	s.Equal(
		connection.String(),
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
		WebPod:    pod,
		PodName:   "pod-name",
	}
	connection, err := opener.Connection(pod)
	s.NoError(err)
	s.Equal(
		connection.String(),
		"host=1.2.3.4 user=postgres password=password sslmode=verify-ca",
	)
}

func (s *PostgresOpenerSuite) TestFailsWhenRootCertLookupErrors() {
	container := corev1.Container{
		Name: "helm-release-web",
		Env: []corev1.EnvVar{
			{
				Name:  "CONCOURSE_POSTGRES_HOST",
				Value: "example.com",
			},
			{
				Name:  "CONCOURSE_POSTGRES_USER",
				Value: "postgres",
			},
			{
				Name:  "CONCOURSE_POSTGRES_PASSWORD",
				Value: "password",
			},
			{
				Name:  "CONCOURSE_POSTGRES_SSLMODE",
				Value: "verify-ca",
			},
			{
				Name:  "CONCOURSE_POSTGRES_CA_CERT",
				Value: "/postgres-keys/ca.cert",
			},
		},
	}
	pod := &accounts.K8sWebPod{
		Pod: &corev1.Pod{
			Spec: corev1.PodSpec{
				Containers: []corev1.Container{container},
			},
		},
	}

	opener := &accounts.K8sWebNodeInferredPostgresOpener{
		WebPod:    pod,
		PodName:   "pod-name",
	}

	_, err := opener.Connection(pod)
	s.EqualError(
		err,
		"container has no volume mounts matching '/postgres-keys/ca.cert'",
	)
}

func (s *PostgresOpenerSuite) TestFailsWhenClientCertLookupErrors() {
	container := corev1.Container{
		Name: "helm-release-web",
		Env: []corev1.EnvVar{
			{
				Name:  "CONCOURSE_POSTGRES_HOST",
				Value: "example.com",
			},
			{
				Name:  "CONCOURSE_POSTGRES_USER",
				Value: "postgres",
			},
			{
				Name:  "CONCOURSE_POSTGRES_PASSWORD",
				Value: "password",
			},
			{
				Name:  "CONCOURSE_POSTGRES_SSLMODE",
				Value: "verify-ca",
			},
			{
				Name:  "CONCOURSE_POSTGRES_CLIENT_CERT",
				Value: "/postgres-keys/client.cert",
			},
		},
	}
	pod := &accounts.K8sWebPod{
		Pod: &corev1.Pod{
			Spec: corev1.PodSpec{
				Containers: []corev1.Container{container},
			},
		},
	}

	opener := &accounts.K8sWebNodeInferredPostgresOpener{
		WebPod:    pod,
		PodName:   "pod-name",
	}

	_, err := opener.Connection(pod)
	s.EqualError(
		err,
		"container has no volume mounts matching '/postgres-keys/client.cert'",
	)
}

func (s *PostgresOpenerSuite) TestFailsWhenClientKeyLookupErrors() {
	container := corev1.Container{
		Name: "helm-release-web",
		Env: []corev1.EnvVar{
			{
				Name:  "CONCOURSE_POSTGRES_HOST",
				Value: "example.com",
			},
			{
				Name:  "CONCOURSE_POSTGRES_USER",
				Value: "postgres",
			},
			{
				Name:  "CONCOURSE_POSTGRES_PASSWORD",
				Value: "password",
			},
			{
				Name:  "CONCOURSE_POSTGRES_SSLMODE",
				Value: "verify-ca",
			},
			{
				Name:  "CONCOURSE_POSTGRES_CLIENT_KEY",
				Value: "/postgres-keys/client.key",
			},
		},
	}
	pod := &accounts.K8sWebPod{
		Pod: &corev1.Pod{
			Spec: corev1.PodSpec{
				Containers: []corev1.Container{container},
			},
		},
	}

	opener := &accounts.K8sWebNodeInferredPostgresOpener{
		WebPod:    pod,
		PodName:   "pod-name",
	}

	_, err := opener.Connection(pod)
	s.EqualError(
		err,
		"container has no volume mounts matching '/postgres-keys/client.key'",
	)
}

func (s *PostgresOpenerSuite) TestReadsClientTLSWithoutRootCert() {
	certBlock, keyBlock := s.generateKeyPair()
	cert, err := tls.X509KeyPair(certBlock, keyBlock)
	s.NoError(err)
	tlsConf := &tls.Config{Certificates: []tls.Certificate{cert}}
	port, pg := s.fakePostgres(tlsConf)
	defer pg.Close()
	pod := &testWebPod{
		name:        "web",
		host:        "127.0.0.1",
		port:        port,
		user:        "postgres",
		password:    "password",
		sslmode:     "verify-ca",
		sslkey:      string(keyBlock),
		sslcert:     string(certBlock),
		sslrootcert: string(certBlock),
	}
	opener := &accounts.K8sWebNodeInferredPostgresOpener{
		WebPod:    pod,
		PodName:   "pod-name",
	}
	db, err := opener.Open()
	s.NoError(err)
	err = db.Ping()
	s.NoError(err)
}
