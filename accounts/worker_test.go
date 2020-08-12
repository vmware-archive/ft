package accounts_test

import (
	// "fmt"
	"fmt"
	"net"
	"net/http"
	// "net/http"

	"code.cloudfoundry.org/garden"
	"code.cloudfoundry.org/garden/gardenfakes"
	"code.cloudfoundry.org/garden/server"
	"code.cloudfoundry.org/lager/lagertest"
	"github.com/concourse/ctop/accounts"
	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
	"github.com/onsi/gomega/gstruct"

	// corev1 "k8s.io/api/core/v1"
	// metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	restclient "k8s.io/client-go/rest"
	"k8s.io/client-go/rest/fake"
	"k8s.io/kubectl/pkg/scheme"

	cmdtesting "k8s.io/kubectl/pkg/cmd/testing"
)

var _ = Describe("Worker", func() {
	Describe("LANWorker", func() {
		var (
			gardenServer *server.GardenServer
			backend      *gardenfakes.FakeBackend
			listener     net.Listener
		)
		BeforeEach(func() {
			backend = new(gardenfakes.FakeBackend)
			logger := lagertest.NewTestLogger("test")
			gardenServer = server.New(
				"tcp",
				"127.0.0.1:7777",
				0,
				backend,
				logger,
			)
			listener, _ = net.Listen("tcp", "127.0.0.1:7777")
			go gardenServer.Serve(listener)
		})
		AfterEach(func() {
			gardenServer.Stop()
			listener.Close()
		})
		It("lists containers", func() {
			fakeContainer := new(gardenfakes.FakeContainer)
			fakeContainer.HandleReturns("container-handle")
			backend.ContainersReturns([]garden.Container{fakeContainer}, nil)

			worker := accounts.NewLANWorker()
			containers, err := worker.Containers()

			Expect(err).NotTo(HaveOccurred())
			Expect(containers).To(ConsistOf(
				gstruct.MatchFields(gstruct.IgnoreExtras, gstruct.Fields{
					"Handle": Equal("container-handle"),
				}),
			))
		})
	})
	Describe("K8sWorker", func() {
		It("lists containers", func() {
			// create the fake cmdutil.Factory
			tf := cmdtesting.NewTestFactory().WithNamespace("test")
			defer tf.Cleanup()
			ns := scheme.Codecs.WithoutConversion()
			tf.Client = &fake.RESTClient{
				GroupVersion:         schema.GroupVersion{Group: "", Version: "v1"},
				NegotiatedSerializer: ns,
			}
			tf.ClientConfigVal = &restclient.Config{APIPath: "/api", ContentConfig: restclient.ContentConfig{NegotiatedSerializer: scheme.Codecs, GroupVersion: &schema.GroupVersion{Version: "v1"}}}

			// pass it into NewK8sWorker
			worker := accounts.NewK8sWorker(tf)
			// grab containers -- what kind of stubbing will be required?
			containers, err := worker.Containers()

			Expect(err).NotTo(HaveOccurred())
			Expect(containers).To(ConsistOf(
				gstruct.MatchFields(gstruct.IgnoreExtras, gstruct.Fields{
					"Handle": Equal("container-handle"),
				}),
			))
		})
	})
})
