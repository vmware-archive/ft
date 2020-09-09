package accounts_test

import (
	"bytes"
	"errors"

	"github.com/concourse/ft/accounts"
	"github.com/concourse/ft/accounts/accountsfakes"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
)

type AccountsSuite struct {
	suite.Suite
	*require.Assertions
}

//go:generate go run github.com/maxbrunsfeld/counterfeiter/v6 io.Writer

var noopValidator = func(accounts.Command) error {
	return nil
}
var noopWorkerFactory = func(accounts.Command) (accounts.Worker, error) {
	return nil, nil
}
var noopAccountantFactory = func(accounts.Command) (accounts.Accountant, error) {
	return nil, nil
}

func (s *AccountsSuite) TestHandlesWorkerErrors() {
	buf := bytes.NewBuffer([]byte{})
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

	s.Equal(returnCode, 1)
	s.Contains(buf.String(), "worker error: pod not found\n")
}

func (s *AccountsSuite) TestPrintsUsageWhenHelpFlagIsPassed() {
	buf := bytes.NewBuffer([]byte{})
	returnCode := accounts.Execute(
		noopWorkerFactory,
		noopAccountantFactory,
		noopValidator,
		[]string{"-h"},
		buf,
	)
	s.Equal(returnCode, 0)
	s.Contains(buf.String(), "Usage")
}

func (s *AccountsSuite) TestFailsOnFlagParsingErrors() {
	buf := bytes.NewBuffer([]byte{})
	returnCode := accounts.Execute(
		noopWorkerFactory,
		noopAccountantFactory,
		noopValidator,
		[]string{"--invalid-flag"},
		buf,
	)
	s.Equal(returnCode, 1)
	s.Contains(buf.String(), "invalid-flag")
}

func (s *AccountsSuite) TestUsesSSLFlagsToConfigurePostgresConnection() {
	fakeWorker := new(accountsfakes.FakeWorker)
	fakeWorker.ContainersReturns(nil, errors.New("no worker"))
	var cmd accounts.Command

	accounts.Execute(
		func(accounts.Command) (accounts.Worker, error) {
			return fakeWorker, nil
		},
		func(c accounts.Command) (accounts.Accountant, error) {
			cmd = c
			return nil, nil
		},
		noopValidator,
		[]string{"--postgres-client-cert", "/path/to/cert"},
		bytes.NewBuffer([]byte{}),
	)

	s.Equal(cmd.Postgres.ClientCert.Path(), "/path/to/cert")
}

func (s *AccountsSuite) TestRunsValidatorAgainstFlags() {
	buf := bytes.NewBuffer([]byte{})

	returnCode := accounts.Execute(
		noopWorkerFactory,
		noopAccountantFactory,
		func(accounts.Command) error {
			return errors.New("invalid flags")
		},
		[]string{},
		buf,
	)

	s.Equal(returnCode, 1)
	s.Contains(buf.String(), "invalid flags")
}

func (s *AccountsSuite) TestFailsOnKubectlErrors() {
	buf := bytes.NewBuffer([]byte{})

	returnCode := accounts.Execute(
		func(accounts.Command) (accounts.Worker, error) {
			return nil, errors.New("error loading config file")
		},
		noopAccountantFactory,
		noopValidator,
		[]string{},
		buf,
	)

	s.Equal(returnCode, 1)
	s.Contains(buf.String(), "configuration error: error loading config file\n")
}

func (s *AccountsSuite) TestFailsOnAccountantFactoryKubectlErrors() {
	buf := bytes.NewBuffer([]byte{})

	returnCode := accounts.Execute(
		noopWorkerFactory,
		func(accounts.Command) (accounts.Accountant, error) {
			return nil, errors.New("error loading config file")
		},
		noopValidator,
		[]string{},
		buf,
	)

	s.Equal(returnCode, 1)
	s.Contains(buf.String(), "configuration error: error loading config file\n")
}

func (s *AccountsSuite) TestFailsOnAccountantErrors() {
	buf := bytes.NewBuffer([]byte{})
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
		}, func(accounts.Command) (accounts.Accountant, error) {
			return fakeAccountant, nil
		},
		noopValidator,
		[]string{},
		buf,
	)

	s.Equal(returnCode, 1)
	s.Contains(buf.String(), "accountant error: accountant error\n")
}

func (s *AccountsSuite) TestFailsOnTerminalIOErrors() {
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
		}, func(accounts.Command) (accounts.Accountant, error) {
			return fakeAccountant, nil
		},
		noopValidator,
		[]string{},
		fakeWriter,
	)

	s.Equal(returnCode, 1)
}
