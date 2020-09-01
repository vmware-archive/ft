package accounts

import (
	"fmt"
	"io"
	"strings"

	"github.com/concourse/concourse/atc/db"
	"github.com/concourse/concourse/fly/ui"
	"github.com/fatih/color"
	"github.com/jessevdk/go-flags"
	"github.com/spf13/cobra"
)

func Execute(workerFactory WorkerFactory, accountantFactory AccountantFactory, args []string, stdout io.Writer) int {
	cmd, err := parseArgs(args)
	if err != nil {
		fmt.Fprintln(stdout, err.Error())
		return flagErrorReturnCode(err)
	}
	worker, err := workerFactory(cmd)
	if err != nil {
		fmt.Fprintf(stdout, "configuration error: %s\n", err.Error())
		return 1
	}
	accountant := accountantFactory(cmd)
	containers, err := worker.Containers()
	if err != nil {
		fmt.Fprintf(stdout, "worker error: %s\n", err.Error())
		return 1
	}
	samples, err := accountant.Account(containers)
	if err != nil {
		fmt.Fprintf(stdout, "accountant error: %s\n", err.Error())
		return 1
	}
	err = printSamples(stdout, samples)
	if err != nil {
		return 1
	}
	return 0
}

func flagErrorReturnCode(err error) int {
	ourErr, ok := err.(*flags.Error)
	if ok && ourErr.Type == flags.ErrHelp {
		return 0
	}
	return 1
}

func parseArgs(args []string) (Command, error) {
	ftCmd := Command{}
	// create cobra.Command
	cobraCmd := &cobra.Command{
		Use:   "ft",
		Short: "ft is an operator observability tool for concourse",
	}
	var postgresCaCert, postgresClientCert, postgresClientKey string
	// define flags
	cobraCmd.PersistentFlags().StringVar(&ftCmd.K8sNamespace, "k8s-namespace", "", "Kubernetes namespace containing the worker pod to query")
	cobraCmd.PersistentFlags().StringVar(&ftCmd.K8sPod, "k8s-pod", "", "Name of the worker pod to query")
	cobraCmd.PersistentFlags().StringVar(&ftCmd.Postgres.Host, "postgres-host", "127.0.0.1", "The postgres host to connect to")
	cobraCmd.PersistentFlags().Uint16Var(&ftCmd.Postgres.Port, "postgres-port", 5432, "The postgres port to connect to") // TODO uint16
	cobraCmd.PersistentFlags().StringVar(&ftCmd.Postgres.User, "postgres-user", "", "The postgres user to sign in as")
	cobraCmd.PersistentFlags().StringVar(&ftCmd.Postgres.Database, "postgres-database", "atc", "The postgres database to connect to")
	cobraCmd.PersistentFlags().StringVar(&ftCmd.Postgres.Password, "postgres-password", "", "The postgres user's password")
	cobraCmd.PersistentFlags().StringVar(&ftCmd.Postgres.SSLMode, "postgres-sslmode", "disable", "Whether or not to use SSL when connecting to postgres") // TODO choices in cobra - disable, require, verify-ca, verify-full
	cobraCmd.PersistentFlags().StringVar(&postgresCaCert, "postgres-ca-cert", "", "CA cert file location, to verify when connecting to postgres with SSL") // TODO flag.File
	cobraCmd.PersistentFlags().StringVar(&postgresClientCert, "postgres-client-cert", "", "Client cert file location, to use when connecting to postgres with SSL") // TODO flag.File
	cobraCmd.PersistentFlags().StringVar(&postgresClientKey, "postgres-client-key", "", "Client key file location, to use when connecting to postgres with SSL") // TODO flag.File

	// call Execute on cobra.Command
	// populate our own Command struct from there

	// parser := flags.NewParser(&cmd, flags.HelpFlag|flags.PassDoubleDash)
	// parser.NamespaceDelimiter = "-"
	// _, err := parser.ParseArgs(args)
	return ftCmd, nil
}

func printSamples(writer io.Writer, samples []Sample) error {
	data := []ui.TableRow{}
	for _, sample := range samples {
		workloads := []string{}
		for _, w := range sample.Labels.Workloads {
			workloads = append(workloads, w.ToString())
		}
		data = append(data, ui.TableRow{
			ui.TableCell{Contents: sample.Container.Handle},
			ui.TableCell{Contents: string(sample.Labels.Type)},
			ui.TableCell{Contents: strings.Join(workloads, ",")},
		})
	}
	table := ui.Table{
		Headers: ui.TableRow{
			ui.TableCell{
				Contents: "handle",
				Color:    color.New(color.Bold),
			},
			ui.TableCell{
				Contents: "type",
				Color:    color.New(color.Bold),
			},
			ui.TableCell{
				Contents: "workloads",
				Color:    color.New(color.Bold),
			},
		},
		Data: data,
	}
	return table.Render(writer, true)
}

type Sample struct {
	Container Container
	Labels    Labels
}

type Labels struct {
	Type      db.ContainerType
	Workloads []Workload
}

//go:generate go run github.com/maxbrunsfeld/counterfeiter/v6 . Accountant

type Accountant interface {
	Account([]Container) ([]Sample, error)
}

type Container struct {
	Handle string
	Stats  Stats
}

type Stats struct {
}

// a Workload is a description of a concourse core concept that corresponds to
// a container. i.e. team/pipeline/job/build/step or team/pipeline/resource.
// Roughly speaking this is what the fly hijack codebase refers to as a
// container fingerprint
// = a reason for a container's existence.

type Workload interface {
	ToString() string
}

//go:generate go run github.com/maxbrunsfeld/counterfeiter/v6 . Worker

type Worker interface {
	Containers(...StatsOption) ([]Container, error)
}

type StatsOption func()
