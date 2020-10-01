package accounts_test

import (
	"github.com/concourse/ft/accounts"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
	corev1 "k8s.io/api/core/v1"
)

type K8sWebPodSuite struct {
	suite.Suite
	*require.Assertions
}

type testk8sClient struct {
	secrets map[string]map[string]string
}

func (tkc *testk8sClient) GetPod(name string) (*corev1.Pod, error) {
	return nil, nil
}

func (tkc *testk8sClient) GetSecret(name, key string) (string, error) {
	return tkc.secrets[name][key], nil
}

func (s *K8sWebPodSuite) TestValueFromEnvVarLooksUpSecret() {
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

	userParam, err := pod.ValueFromEnvVar("CONCOURSE_POSTGRES_USER")
	s.NoError(err)
	s.Equal(userParam, "username")
}

func (s *K8sWebPodSuite) TestFileContentsFromEnvVarGetsCertFromSecret() {
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
			{
				Name:      "some-other-volume",
				MountPath: "/some/other/path",
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
				Volumes: []corev1.Volume{
					volume,
					{Name: "another-volume"},
				},
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

	fileContents, err := pod.FileContentsFromEnvVar("CONCOURSE_POSTGRES_CA_CERT")
	s.NoError(err)
	s.Equal(fileContents, "ssl cert")
}

func (s *K8sWebPodSuite) TestValueFromEnvVarFailsWithNoContainers() {
	pod := &accounts.K8sWebPod{
		Pod: &corev1.Pod{
			Spec: corev1.PodSpec{
				Containers: []corev1.Container{},
			},
		},
	}
	_, err := pod.ValueFromEnvVar("param")
	s.EqualError(err, "could not find a 'web' container")
}

func (s *K8sWebPodSuite) TestValueFromEnvVarFailsWithoutWebContainer() {
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
	_, err := pod.ValueFromEnvVar("param")
	s.EqualError(err, "could not find a 'web' container")
}

func (s *K8sWebPodSuite) TestValueFromEnvVarFailsWithMultipleWebContainers() {
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
	_, err := pod.ValueFromEnvVar("param")
	s.EqualError(
		err,
		"found multiple 'web' containers",
	)
}

func (s *K8sWebPodSuite) TestValueFromEnvVarFailsWithMissingParam() {
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
	_, err := pod.ValueFromEnvVar("PARAM")
	s.EqualError(
		err,
		"container 'web' does not have 'PARAM' specified",
	)
}

func (s *K8sWebPodSuite) TestFileContentsFromEnvVarFailsWithoutMatchingMount() {
	container := corev1.Container{
		Name: "helm-release-web",
		Env: []corev1.EnvVar{
			{
				Name:  "SOME_FILE",
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

	_, err := pod.FileContentsFromEnvVar("SOME_FILE")
	s.EqualError(
		err,
		"container has no volume mounts matching '/postgres-keys/client.key'",
	)
}

func (s *K8sWebPodSuite) TestFileContentsFromEnvVarFailsWithoutMatchingVolume() {
	container := corev1.Container{
		Name: "helm-release-web",
		Env: []corev1.EnvVar{
			{
				Name:  "SOME_FILE",
				Value: "/postgres-keys/client.key",
			},
		},
		VolumeMounts: []corev1.VolumeMount{
			{
				Name:      "nonexistent-volume",
				MountPath: "/postgres-keys",
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

	_, err := pod.FileContentsFromEnvVar("SOME_FILE")
	s.EqualError(
		err,
		"pod has no volume named 'nonexistent-volume'",
	)
}

func (s *K8sWebPodSuite) TestPostgresParamNamesReturnsEmptyWithNoEnvVars() {
	container := corev1.Container{
		Name: "helm-release-web",
		Env:  []corev1.EnvVar{},
	}
	pod := &accounts.K8sWebPod{
		Pod: &corev1.Pod{
			Spec: corev1.PodSpec{
				Containers: []corev1.Container{container},
			},
		},
	}
	paramNames, _ := pod.PostgresParamNames()
	s.Equal([]string{}, paramNames)
}

func (s *K8sWebPodSuite) TestPostgresParamNamesReturnsEnvVars() {
	container := corev1.Container{
		Name: "helm-release-web",
		Env: []corev1.EnvVar{
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
	paramNames, _ := pod.PostgresParamNames()
	s.Equal([]string{"CONCOURSE_POSTGRES_CA_CERT"}, paramNames)
}

func (s *K8sWebPodSuite) TestPostgresParamNamesFailsWithNoContainers() {
	pod := &accounts.K8sWebPod{
		Pod: &corev1.Pod{
			Spec: corev1.PodSpec{
				Containers: []corev1.Container{},
			},
		},
	}
	_, err := pod.PostgresParamNames()
	s.EqualError(err, "could not find a 'web' container")
}

func (s *K8sWebPodSuite) TestPostgresParamNamesReturnsEnvVarsWithPrefix() {
	container := corev1.Container{
		Name: "helm-release-web",
		Env: []corev1.EnvVar{
			{
				Name:  "CONCOURSE_POSTGRES_CA_CERT",
				Value: "/postgres-keys/ca.cert",
			},
			{
				Name:  "FOO_BAR",
				Value: "baz",
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
	paramNames, _ := pod.PostgresParamNames()
	s.Equal([]string{"CONCOURSE_POSTGRES_CA_CERT"}, paramNames)
}
