package main

import (
	"github.com/concourse/workloads/accounts"
	"github.com/concourse/flag"
	flags "github.com/jessevdk/go-flags"
)

func main() {
	postgresConfig := flag.PostgresConfig{}
	parser := flags.NewParser(&postgresConfig, flags.HelpFlag|flags.PassDoubleDash)
	_, err := parser.Parse()
	if err != nil {
		panic(err)
	}
	// TODO connect to a real worker
	var worker accounts.Worker = nil
	accountant := accounts.NewDBAccountant(postgresConfig)
	accounts.Account(worker, accountant)
	// print the samples
}
