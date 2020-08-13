module github.com/concourse/ctop

go 1.14

require (
	cloud.google.com/go v0.51.0 // indirect
	code.cloudfoundry.org/clock v0.0.0-20180518195852-02e53af36e6c
	code.cloudfoundry.org/garden v0.0.0-20181108172608-62470dc86365
	code.cloudfoundry.org/lager v2.0.0+incompatible
	github.com/Masterminds/squirrel v1.1.0
	github.com/concourse/baggageclaim v1.8.0
	github.com/concourse/concourse v1.6.1-0.20200811134233-6fdc412c6b5a
	github.com/concourse/flag v1.1.0
	github.com/concourse/retryhttp v1.0.2
	github.com/cppforlife/go-semi-semantic v0.0.0-20160921010311-576b6af77ae4
	github.com/fatih/color v1.7.0
	github.com/go-sql-driver/mysql v1.4.1 // indirect
	github.com/jessevdk/go-flags v1.4.0
	github.com/mattn/go-sqlite3 v1.11.0 // indirect
	github.com/maxbrunsfeld/counterfeiter/v6 v6.2.3
	github.com/onsi/ginkgo v1.13.0
	github.com/onsi/gomega v1.10.1
	github.com/patrickmn/go-cache v2.1.0+incompatible
	golang.org/x/oauth2 v0.0.0-20200107190931-bf48bf16ab8d // indirect
	google.golang.org/genproto v0.0.0-20200108215221-bd8f9a0ef82f // indirect
	k8s.io/api v0.18.6
	k8s.io/apimachinery v0.18.6
	k8s.io/cli-runtime v0.18.6
	k8s.io/client-go v11.0.0+incompatible
	k8s.io/cri-api v0.0.0
	k8s.io/kubectl v0.18.6
	k8s.io/kubernetes v1.18.6
)

replace k8s.io/client-go => k8s.io/client-go v0.18.6

replace k8s.io/api => k8s.io/api v0.18.6

replace k8s.io/apiextensions-apiserver => k8s.io/apiextensions-apiserver v0.18.6

replace k8s.io/apimachinery => k8s.io/apimachinery v0.18.7-rc.0

replace k8s.io/apiserver => k8s.io/apiserver v0.18.6

replace k8s.io/cli-runtime => k8s.io/cli-runtime v0.18.6

replace k8s.io/cloud-provider => k8s.io/cloud-provider v0.18.6

replace k8s.io/cluster-bootstrap => k8s.io/cluster-bootstrap v0.18.6

replace k8s.io/code-generator => k8s.io/code-generator v0.18.7-rc.0

replace k8s.io/component-base => k8s.io/component-base v0.18.6

replace k8s.io/cri-api => k8s.io/cri-api v0.18.7-rc.0

replace k8s.io/csi-translation-lib => k8s.io/csi-translation-lib v0.18.6

replace k8s.io/kube-aggregator => k8s.io/kube-aggregator v0.18.6

replace k8s.io/kube-controller-manager => k8s.io/kube-controller-manager v0.18.6

replace k8s.io/kube-proxy => k8s.io/kube-proxy v0.18.6

replace k8s.io/kube-scheduler => k8s.io/kube-scheduler v0.18.6

replace k8s.io/kubectl => k8s.io/kubectl v0.18.6

replace k8s.io/kubelet => k8s.io/kubelet v0.18.6

replace k8s.io/legacy-cloud-providers => k8s.io/legacy-cloud-providers v0.18.6

replace k8s.io/metrics => k8s.io/metrics v0.18.6

replace k8s.io/sample-apiserver => k8s.io/sample-apiserver v0.18.6

replace k8s.io/sample-cli-plugin => k8s.io/sample-cli-plugin v0.18.6

replace k8s.io/sample-controller => k8s.io/sample-controller v0.18.6
