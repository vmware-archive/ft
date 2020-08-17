package main

import (
	"os"

	_ "k8s.io/client-go/plugin/pkg/client/auth"

	"github.com/concourse/ft/accounts"
)

func main() {
	returnCode := accounts.Execute(
		accounts.DefaultWorkerFactory,
		os.Args[1:],
		os.Stdout,
	)
	os.Exit(returnCode)
}
