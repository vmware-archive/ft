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
	"math/big"
	"net"
	"time"

	"github.com/concourse/ft/accounts"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
	corev1 "k8s.io/api/core/v1"
)

type PostgresOpenerSuite struct {
	suite.Suite
	*require.Assertions
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

type testWebNode struct {
	name        string
	host        string
	port        string
	user        string
	password    string
	sslmode     string
	sslrootcert string
	sslkey      string
	sslcert     string
	valueError  bool
}

func (twp *testWebNode) PostgresParamNames() ([]string, error) {
	names := []string{}
	if twp.host != "" {
		names = append(names, "CONCOURSE_POSTGRES_HOST")
	}
	if twp.port != "" {
		names = append(names, "CONCOURSE_POSTGRES_PORT")
	}
	if twp.user != "" {
		names = append(names, "CONCOURSE_POSTGRES_USER")
	}
	if twp.password != "" {
		names = append(names, "CONCOURSE_POSTGRES_PASSWORD")
	}
	if twp.sslmode != "" {
		names = append(names, "CONCOURSE_POSTGRES_SSLMODE")
	}
	if twp.sslrootcert != "" {
		names = append(names, "CONCOURSE_POSTGRES_CA_CERT")
	}
	if twp.sslcert != "" {
		names = append(names, "CONCOURSE_POSTGRES_CLIENT_CERT")
	}
	if twp.sslkey != "" {
		names = append(names, "CONCOURSE_POSTGRES_CLIENT_KEY")
	}
	return names, nil
}

func (twp *testWebNode) ValueFromEnvVar(param string) (string, error) {
	if twp.valueError {
		return "", errors.New("foobar")
	}
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

func (twp *testWebNode) FileContentsFromEnvVar(param string) (string, error) {
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
		return val, nil
	}
	return "", nil
}

func (s *PostgresOpenerSuite) TestInfersPostgresConnectionFromWebNode() {
	port, pg := s.fakePostgres(nil)
	defer pg.Close()
	opener := &accounts.WebNodeInferredPostgresOpener{
		WebNode: &testWebNode{
			name:     "helm-release-web",
			host:     "127.0.0.1",
			port:     port,
			user:     "postgres",
			password: "password",
		},
		FileTracker: &accounts.TmpfsTracker{},
	}

	_, err := opener.Open()
	s.NoError(err)
}

func (s *PostgresOpenerSuite) generateKeyPair() ([]byte, []byte) {
	priv, err := rsa.GenerateKey(rand.Reader, 4096)
	s.NoError(err)

	notBefore := time.Now()
	notAfter := notBefore.Add(365 * 24 * time.Hour)

	serialNumberLimit := new(big.Int).Lsh(big.NewInt(1), 128)
	serialNumber, err := rand.Int(rand.Reader, serialNumberLimit)
	s.NoError(err)

	template := x509.Certificate{
		SerialNumber: serialNumber,
		NotBefore:    notBefore,
		NotAfter:     notAfter,
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
	opener := &accounts.WebNodeInferredPostgresOpener{
		WebNode: &testWebNode{
			name:        "helm-release-web",
			host:        "127.0.0.1",
			port:        port,
			user:        "postgres",
			password:    "password",
			sslmode:     "verify-ca",
			sslrootcert: string(certBlock),
		},
		FileTracker: &accounts.TmpfsTracker{},
	}

	_, err = opener.Open()
	s.NoError(err)
}

func (s *PostgresOpenerSuite) TestCleansUpFiles() {
	certBlock, keyBlock := s.generateKeyPair()
	cert, err := tls.X509KeyPair(certBlock, keyBlock)
	s.NoError(err)
	tlsConf := &tls.Config{Certificates: []tls.Certificate{cert}}
	port, pg := s.fakePostgres(tlsConf)
	defer pg.Close()
	tracker := &accounts.TmpfsTracker{}
	opener := &accounts.WebNodeInferredPostgresOpener{
		WebNode: &testWebNode{
			name:        "helm-release-web",
			host:        "127.0.0.1",
			port:        port,
			user:        "postgres",
			password:    "password",
			sslmode:     "verify-ca",
			sslrootcert: string(certBlock),
		},
		FileTracker: tracker,
	}

	_, err = opener.Open()
	s.Equal(0, tracker.Count(), "tempfiles must be deleted")
}

func (s *PostgresOpenerSuite) TestUsesDefaultPortWhenUnspecified() {
	pod := &testWebNode{
		name:     "web",
		host:     "1.2.3.4",
		user:     "postgres",
		password: "password",
	}
	opener := &accounts.WebNodeInferredPostgresOpener{
		WebNode: pod,
	}
	postgresConfig, err := opener.PostgresConfig()
	s.NoError(err)
	s.Contains(
		postgresConfig.ConnectionString(),
		"port=5432",
	)
}

func (s *PostgresOpenerSuite) TestReadsSSLMode() {
	pod := &testWebNode{
		name:     "web",
		host:     "1.2.3.4",
		user:     "postgres",
		password: "password",
		sslmode:  "verify-ca",
	}
	opener := &accounts.WebNodeInferredPostgresOpener{
		WebNode: pod,
	}
	postgresConfig, err := opener.PostgresConfig()
	s.NoError(err)
	s.Contains(
		postgresConfig.ConnectionString(),
		"sslmode='verify-ca'",
	)
}

func (s *PostgresOpenerSuite) TestFailsWhenValueLookupErrors() {
	pod := &testWebNode{
		host:       "1.2.3.4",
		valueError: true,
	}
	opener := &accounts.WebNodeInferredPostgresOpener{
		WebNode: pod,
	}

	_, err := opener.PostgresConfig()

	s.EqualError(err, "foobar")
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

	opener := &accounts.WebNodeInferredPostgresOpener{
		WebNode: pod,
	}

	_, err := opener.PostgresConfig()
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

	opener := &accounts.WebNodeInferredPostgresOpener{
		WebNode: pod,
	}

	_, err := opener.PostgresConfig()
	s.EqualError(
		err,
		"container has no volume mounts matching '/postgres-keys/client.cert'",
	)
}

func (s *PostgresOpenerSuite) TestInfersClientTLSConfigFromWebNode() {
	certBlock, keyBlock := s.generateKeyPair()
	cert, err := tls.X509KeyPair(certBlock, keyBlock)
	s.NoError(err)
	tlsConf := &tls.Config{Certificates: []tls.Certificate{cert}}
	port, pg := s.fakePostgres(tlsConf)
	defer pg.Close()
	pod := &testWebNode{
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
	opener := &accounts.WebNodeInferredPostgresOpener{
		WebNode:     pod,
		FileTracker: &accounts.TmpfsTracker{},
	}
	_, err = opener.Open()
	s.NoError(err)
}
