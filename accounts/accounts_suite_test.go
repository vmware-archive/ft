package accounts_test

import (
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
)

func TestAccounts(t *testing.T) {
	suite.Run(t, &AccountsSuite{
		Assertions: require.New(t),
	})
	suite.Run(t, &AccountantSuite{
		Assertions: require.New(t),
	})
	suite.Run(t, &LANWorkerSuite{
		Assertions: require.New(t),
	})
	suite.Run(t, &K8sGardenDialerSuite{
		Assertions: require.New(t),
	})
	suite.Run(t, &PostgresOpenerSuite{
		Assertions: require.New(t),
	})
	suite.Run(t, &K8sWebPodSuite{
		Assertions: require.New(t),
	})
	suite.Run(t, &K8sClientSuite{
		Assertions: require.New(t),
	})
	suite.Run(t, &GardenConnectionSuite{
		Assertions: require.New(t),
	})
}
