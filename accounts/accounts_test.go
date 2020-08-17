package accounts_test

import (
	"errors"

	"github.com/concourse/ft/accounts"
	"github.com/concourse/ft/accounts/accountsfakes"
	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
	"github.com/onsi/gomega/gbytes"
)

var _ = Describe("Accounts", func() {
	Describe("#Execute", func() {
		It("handles worker errors", func() {
			buf := gbytes.NewBuffer()
			fakeWorker := new(accountsfakes.FakeWorker)
			fakeWorker.ContainersReturns([]accounts.Container{}, errors.New("worker error"))
			fakeWorkerFactory := new(accountsfakes.FakeWorkerFactory)
			fakeWorkerFactory.CreateWorkerReturns(fakeWorker, nil)

			returnCode := accounts.Execute(fakeWorkerFactory, []string{}, buf)

			Expect(returnCode).To(Equal(1))
			Expect(buf).To(gbytes.Say("worker error"))
		})
	})

	Describe("#Account", func() {
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
})
