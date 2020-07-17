package accounts_test

import (
	"errors"

	"github.com/concourse/ctop/accounts"
	"github.com/concourse/ctop/accounts/accountsfakes"
	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
)

var _ = Describe("Account", func() {
	var (
		worker     *accountsfakes.FakeWorker
		accountant *accountsfakes.FakeAccountant
	)

	BeforeEach(func() {
		worker = new(accountsfakes.FakeWorker)
		accountant = new(accountsfakes.FakeAccountant)
	})

	It("accounts the workloads for each container", func() {
		container := accounts.Container{Handle: "abc123"}
		containers := []accounts.Container{container}
		worker.ContainersReturns(containers, nil)

		accounts.Account(worker, accountant)

		Expect(accountant.AccountArgsForCall(0)).To(Equal(containers))
	})

	It("surfaces worker errors", func() {
		worker.ContainersReturns(nil, errors.New("HTTP error"))

		_, err := accounts.Account(worker, accountant)

		Expect(err).NotTo(BeNil())
	})
})
