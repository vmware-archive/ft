package accounts_test

import (
	"errors"

	"github.com/concourse/ft/accounts"
	"github.com/concourse/ft/accounts/accountsfakes"
	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
	"github.com/onsi/gomega/gbytes"
)

//go:generate go run github.com/maxbrunsfeld/counterfeiter/v6 io.Writer

var noopValidator = func(accounts.Command) error {
	return nil
}
var noopAccountantFactory = func(accounts.Command) accounts.Accountant {
	return nil
}

var _ = Describe("Accounts", func() {
	Describe("#Execute", func() {
		It("handles worker errors", func() {
			buf := gbytes.NewBuffer()
			fakeWorker := new(accountsfakes.FakeWorker)
			fakeWorker.ContainersReturns([]accounts.Container{}, errors.New("pod not found"))

			returnCode := accounts.Execute(
				func(accounts.Command) (accounts.Worker, error) {
					return fakeWorker, nil
				},
				noopAccountantFactory,
				noopValidator,
				[]string{},
				buf,
			)

			Expect(returnCode).To(Equal(1))
			Expect(buf).To(gbytes.Say("worker error: pod not found\n"))
		})

		It("prints help text when `-h` is passed", func() {
			buf := gbytes.NewBuffer()
			returnCode := accounts.Execute(
				nil,
				noopAccountantFactory,
				noopValidator,
				[]string{"-h"},
				buf,
			)
			Expect(returnCode).To(Equal(0))
			Expect(buf).To(gbytes.Say("Usage"))
		})

		It("fails on flag parsing errors", func() {
			buf := gbytes.NewBuffer()
			returnCode := accounts.Execute(
				nil,
				noopAccountantFactory,
				noopValidator,
				[]string{"--invalid-flag"},
				buf,
			)
			Expect(returnCode).To(Equal(1))
			Expect(buf).To(gbytes.Say("invalid-flag"))
		})

		It("uses SSL flags to configure postgres connection", func() {
			fakeWorker := new(accountsfakes.FakeWorker)
			fakeWorker.ContainersReturns(nil, errors.New("no worker"))
			var cmd accounts.Command

			accounts.Execute(
				func(accounts.Command) (accounts.Worker, error) {
					return fakeWorker, nil
				},
				func(c accounts.Command) accounts.Accountant {
					cmd = c
					return nil
				},
				noopValidator,
				[]string{"--postgres-client-cert", "/path/to/cert"},
				gbytes.NewBuffer(),
			)

			Expect(cmd.Postgres.ClientCert.Path()).
				To(Equal("/path/to/cert"))
		})

		It("validates flags", func() {
			buf := gbytes.NewBuffer()
			returnCode := accounts.Execute(
				nil,
				nil,
				func(accounts.Command) error {
					return errors.New("invalid flags")
				},
				[]string{},
				buf,
			)
			Expect(returnCode).To(Equal(1))
			Expect(buf).To(gbytes.Say("invalid flags"))
		})

		It("fails on kubectl errors", func() {
			buf := gbytes.NewBuffer()
			returnCode := accounts.Execute(
				func(accounts.Command) (accounts.Worker, error) {
					return nil,
						errors.New("error loading config file")
				},
				noopAccountantFactory,
				noopValidator,
				[]string{},
				buf,
			)

			Expect(returnCode).To(Equal(1))
			Expect(buf).To(gbytes.Say("configuration error: error loading config file\n"))
		})

		It("fails on accountant errors", func() {
			buf := gbytes.NewBuffer()
			fakeWorker := new(accountsfakes.FakeWorker)
			container := accounts.Container{Handle: "abc123"}
			containers := []accounts.Container{container}
			fakeWorker.ContainersReturns(containers, nil)
			fakeAccountant := new(accountsfakes.FakeAccountant)
			fakeAccountant.AccountReturns(
				nil,
				errors.New("accountant error"),
			)

			returnCode := accounts.Execute(
				func(accounts.Command) (accounts.Worker, error) {
					return fakeWorker, nil
				}, func(accounts.Command) accounts.Accountant {
					return fakeAccountant
				},
				noopValidator,
				[]string{},
				buf,
			)

			Expect(returnCode).To(Equal(1))
			Expect(buf).To(gbytes.Say("accountant error: accountant error\n"))
		})

		It("fails on terminal io errors", func() {
			fakeWriter := new(accountsfakes.FakeWriter)
			fakeWriter.WriteReturns(0, errors.New("terminal io error"))
			fakeWorker := new(accountsfakes.FakeWorker)
			container := accounts.Container{Handle: "abc123"}
			containers := []accounts.Container{container}
			fakeWorker.ContainersReturns(containers, nil)
			fakeAccountant := new(accountsfakes.FakeAccountant)
			fakeAccountant.AccountReturns(
				[]accounts.Sample{
					accounts.Sample{
						Container: container,
					},
				},
				nil,
			)

			returnCode := accounts.Execute(
				func(accounts.Command) (accounts.Worker, error) {
					return fakeWorker, nil
				}, func(accounts.Command) accounts.Accountant {
					return fakeAccountant
				},
				noopValidator,
				[]string{},
				fakeWriter,
			)

			Expect(returnCode).To(Equal(1))
		})
	})
})
