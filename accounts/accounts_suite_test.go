package accounts_test

import (
	"testing"

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
)

func TestAccounts(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Accounts Suite")
	suite.Run(t, &AccountsSuite{
		Assertions: require.New(t),
	})
	suite.Run(t, &AccountantSuite{
		Assertions: require.New(t),
	})
}
