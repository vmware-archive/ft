package accounts

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io/ioutil"
	"os"
	"strings"

	corev1 "k8s.io/api/core/v1"

	"github.com/concourse/flag"
	"github.com/jessevdk/go-flags"
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

type WebNodeInferredPostgresOpener struct {
	WebNode     WebNode
	FileTracker FileTracker
}

type K8sClient interface {
	GetPod(string) (*corev1.Pod, error)
	GetSecret(string, string) (string, error)
}

type k8sClient struct {
	RESTConfig *rest.Config
	Namespace  string
}

func (kc *k8sClient) GetPod(name string) (*corev1.Pod, error) {
	clientset, err := kubernetes.NewForConfig(kc.RESTConfig)
	if err != nil {
		return nil, err
	}
	return clientset.CoreV1().
		Pods(kc.Namespace).
		Get(context.Background(), name, metav1.GetOptions{})
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

type FileTracker interface {
	Write(string) (string, error)
	Clear()
}

type TmpfsTracker struct {
	filenames []string
}

func (tt *TmpfsTracker) Write(contents string) (string, error) {
	tmpfile, err := ioutil.TempFile("", "")
	if err != nil {
		return "", err
	}
	_, err = tmpfile.Write([]byte(contents))
	if err != nil {
		return "", err
	}
	err = tmpfile.Close()
	if err != nil {
		return "", err
	}
	tt.filenames = append(tt.filenames, tmpfile.Name())
	return tmpfile.Name(), nil
}

func (tt *TmpfsTracker) Clear() {
	for _, file := range tt.filenames {
		os.Remove(file)
	}
	tt.filenames = nil
}

func (tt *TmpfsTracker) Count() int {
	return len(tt.filenames)
}

func (wnipo *WebNodeInferredPostgresOpener) Open() (*sql.DB, error) {
	postgresConfig, err := wnipo.PostgresConfig()
	if err != nil {
		return nil, err
	}
	defer wnipo.FileTracker.Clear()
	db, err := sql.Open("postgres", postgresConfig.ConnectionString())
	if err != nil {
		return nil, err
	}
	err = db.Ping()
	if err != nil {
		return nil, err
	}
	return db, nil
}

type WebNode interface {
	PostgresParamNames() ([]string, error)
	ValueFromEnvVar(string) (string, error)
	FileContentsFromEnvVar(string) (string, error)
}

func isFile(paramName string) bool {
	switch paramName {
	case "CONCOURSE_POSTGRES_CA_CERT",
		"CONCOURSE_POSTGRES_CLIENT_CERT",
		"CONCOURSE_POSTGRES_CLIENT_KEY":
		return true
	default:
		return false
	}
}

func toFlagName(paramName string) string {
	return "--" + strings.Replace(
		strings.ToLower(
			strings.TrimPrefix(paramName, "CONCOURSE_POSTGRES_"),
		),
		"_", "-", -1,
	)
}

func (wnipo *WebNodeInferredPostgresOpener) PostgresConfig() (flag.PostgresConfig, error) {
	postgresConfig := flag.PostgresConfig{}
	args := []string{}
	paramNames, _ := wnipo.WebNode.PostgresParamNames()
	for _, postgresParam := range paramNames {
		value, err := wnipo.toFlagValue(postgresParam)
		if err != nil {
			return postgresConfig, err
		}
		args = append(args, toFlagName(postgresParam), value)
	}
	flags.ParseArgs(&postgresConfig, args)
	return postgresConfig, nil
}

func (wnipo *WebNodeInferredPostgresOpener) toFlagValue(
	paramName string,
) (string, error) {
	var value string
	if isFile(paramName) {
		contents, err := wnipo.WebNode.FileContentsFromEnvVar(paramName)
		if err != nil {
			return "", err
		}
		value, err = wnipo.FileTracker.Write(contents)
		if err != nil {
			return "", err
		}
	} else {
		var err error
		value, err = wnipo.WebNode.ValueFromEnvVar(paramName)
		if err != nil {
			return "", err
		}
	}
	return value, nil
}

func tempfileFromString(contents string) (string, error) {
	tmpfile, err := ioutil.TempFile("", "")
	if err != nil {
		return "", err
	}
	_, err = tmpfile.Write([]byte(contents))
	if err != nil {
		return "", err
	}
	err = tmpfile.Close()
	if err != nil {
		return "", err
	}
	return tmpfile.Name(), nil
}

type K8sWebPod struct {
	Pod    *corev1.Pod
	Client K8sClient
}

func (wp *K8sWebPod) PostgresParamNames() ([]string, error) {
	container, err := findWebContainer(wp.Pod.Spec)
	if err != nil {
		return nil, err
	}
	names := []string{}
	for _, envVar := range container.Env {
		if strings.HasPrefix(envVar.Name, "CONCOURSE_POSTGRES_") {
			names = append(names, envVar.Name)
		}
	}
	return names, nil
}

func (wp *K8sWebPod) ValueFromEnvVar(paramName string) (string, error) {
	container, err := findWebContainer(wp.Pod.Spec)
	if err != nil {
		return "", err
	}
	for _, envVar := range container.Env {
		if envVar.Name == paramName {
			return wp.getParam(envVar)
		}
	}
	return "",
		fmt.Errorf("container '%s' does not have '%s' specified",
			container.Name,
			paramName,
		)
}

func (wp *K8sWebPod) FileContentsFromEnvVar(paramName string) (string, error) {
	container, err := findWebContainer(wp.Pod.Spec)
	if err != nil {
		return "", err
	}
	for _, envVar := range container.Env {
		if envVar.Name == paramName {
			secretName, key, err := wp.secretForParam(container, envVar.Value)
			if err != nil {
				return "", err
			}
			return wp.Client.GetSecret(secretName, key)
		}
	}
	return "", nil
}

func (wp *K8sWebPod) secretForParam(container corev1.Container, paramValue string) (string, string, error) {
	// find the VolumeMount whose MountPath is a prefix for paramValue
	var volumeMount *corev1.VolumeMount
	for _, vm := range container.VolumeMounts {
		if strings.HasPrefix(paramValue, vm.MountPath) {
			volumeMount = &vm
			break
		}
	}
	if volumeMount == nil {
		return "", "", fmt.Errorf(
			"container has no volume mounts matching '%s'",
			paramValue,
		)
	}
	var volume *corev1.Volume
	for _, v := range wp.Pod.Spec.Volumes {
		if v.Name == volumeMount.Name {
			volume = &v
			break
		}
	}
	if volume == nil {
		return "", "", fmt.Errorf(
			"pod has no volume named '%s'",
			volumeMount.Name,
		)
	}
	// TODO: test when this fails -- maybe filesystem paths don't get
	// constructed quite how you imagine? symlinks!?
	// find the item whose path matches the envVar
	var item corev1.KeyToPath
	for _, i := range volume.VolumeSource.Secret.Items {
		if paramValue == volumeMount.MountPath+"/"+i.Path {
			item = i
		}
	}
	secretName := volume.VolumeSource.Secret.SecretName
	key := item.Key
	return secretName, key, nil
}

func (wp *K8sWebPod) getParam(envVar corev1.EnvVar) (string, error) {
	if envVar.Value != "" {
		return envVar.Value, nil
	} else {
		secretKeyRef := envVar.ValueFrom.SecretKeyRef
		secretName := secretKeyRef.LocalObjectReference.Name
		key := secretKeyRef.Key
		return wp.Client.GetSecret(secretName, key)
	}
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
