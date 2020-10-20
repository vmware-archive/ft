package accounts

import (
	"fmt"
	"io"
	"strings"

	"github.com/c2h5oh/datasize"
	"github.com/concourse/concourse/atc/db"
	"github.com/concourse/concourse/fly/ui"
	"github.com/concourse/flag"
	"github.com/fatih/color"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

type Command struct {
	Postgres        flag.PostgresConfig
	K8sNamespace    string
	K8sPod          string
	WebK8sNamespace string
	WebK8sPod       string
}

func Execute(
	workerFactory WorkerFactory,
	accountantFactory AccountantFactory,
	validator func(Command) error,
	args []string,
	stdout io.Writer,
) int {
	cmd, err := parseArgs(args, stdout)
	if err == pflag.ErrHelp {
		return 0
	}
	if err != nil {
		fmt.Fprintln(stdout, err.Error())
		return 1
	}
	err = validator(cmd)
	if err != nil {
		fmt.Fprintln(stdout, err.Error())
		return 1
	}
	worker, err := workerFactory(cmd)
	if err != nil {
		fmt.Fprintf(stdout, "configuration error: %s\n", err.Error())
		return 1
	}
	accountant, err := accountantFactory(cmd)
	if err != nil {
		fmt.Fprintf(stdout, "configuration error: %s\n", err.Error())
		return 1
	}
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

func parseArgs(args []string, out io.Writer) (Command, error) {
	ftCmd := Command{}
	cobraCmd := &cobra.Command{
		Use:   "ft",
		Short: "ft is an operator observability tool for concourse",
	}
	var postgresCaCert, postgresClientCert, postgresClientKey string
	cobraCmd.PersistentFlags().StringVar(&ftCmd.K8sNamespace, "k8s-namespace", "", "Kubernetes namespace containing the worker pod to query")
	cobraCmd.PersistentFlags().StringVar(&ftCmd.K8sPod, "k8s-pod", "", "Name of the worker pod to query")
	cobraCmd.PersistentFlags().StringVar(&ftCmd.WebK8sNamespace, "web-k8s-namespace", "", "Kubernetes namespace containing the web pod to inpect for connection information")
	cobraCmd.PersistentFlags().StringVar(&ftCmd.WebK8sPod, "web-k8s-pod", "", "Name of the web pod to inspect for connection information")
	cobraCmd.PersistentFlags().StringVar(&ftCmd.Postgres.Host, "postgres-host", "127.0.0.1", "The postgres host to connect to")
	cobraCmd.PersistentFlags().Uint16Var(&ftCmd.Postgres.Port, "postgres-port", 5432, "The postgres port to connect to")
	cobraCmd.PersistentFlags().StringVar(&ftCmd.Postgres.User, "postgres-user", "", "The postgres user to sign in as")
	cobraCmd.PersistentFlags().StringVar(&ftCmd.Postgres.Database, "postgres-database", "atc", "The postgres database to connect to")
	cobraCmd.PersistentFlags().StringVar(&ftCmd.Postgres.Password, "postgres-password", "", "The postgres user's password")
	cobraCmd.PersistentFlags().StringVar(&ftCmd.Postgres.SSLMode, "postgres-sslmode", "disable", "Whether or not to use SSL when connecting to postgres") // TODO choices in cobra - disable, require, verify-ca, verify-full
	cobraCmd.PersistentFlags().StringVar(&postgresCaCert, "postgres-ca-cert", "", "CA cert file location, to verify when connecting to postgres with SSL")
	cobraCmd.PersistentFlags().StringVar(&postgresClientCert, "postgres-client-cert", "", "Client cert file location, to use when connecting to postgres with SSL")
	cobraCmd.PersistentFlags().StringVar(&postgresClientKey, "postgres-client-key", "", "Client key file location, to use when connecting to postgres with SSL")

	cobraCmd.SetOut(out)
	cobraCmd.InitDefaultHelpFlag()
	err := cobraCmd.ParseFlags(args)
	if helpVal, _ := cobraCmd.Flags().GetBool("help"); helpVal {
		cobraCmd.HelpFunc()(cobraCmd, args)
		cobraCmd.Println(cobraCmd.UsageString())
		return ftCmd, pflag.ErrHelp
	}
	ftCmd.Postgres.CACert = flag.File(postgresCaCert)
	ftCmd.Postgres.ClientCert = flag.File(postgresClientCert)
	ftCmd.Postgres.ClientKey = flag.File(postgresClientKey)
	return ftCmd, err
}

func printSamples(writer io.Writer, samples []Sample) error {
	data := []ui.TableRow{}
	for _, sample := range samples {
		workloads := []string{}
		for _, w := range sample.Labels.Workloads {
			workloads = append(workloads, w.ToString())
		}
		data = append(data, ui.TableRow{
			ui.TableCell{Contents: strings.Join(workloads, ",")},
			ui.TableCell{Contents: string(sample.Labels.Type)},
			ui.TableCell{Contents: humanReadable(sample.Container.Stats.Memory)},
			ui.TableCell{Contents: sample.Container.Handle},
		})
	}
	table := ui.Table{
		Headers: ui.TableRow{
			ui.TableCell{
				Contents: "workloads",
				Color:    color.New(color.Bold),
			},
			ui.TableCell{
				Contents: "type",
				Color:    color.New(color.Bold),
			},
			ui.TableCell{
				Contents: "memory",
				Color:    color.New(color.Bold),
			},
			ui.TableCell{
				Contents: "handle",
				Color:    color.New(color.Bold),
			},
		},
		Data: data,
	}
	return table.Render(writer, true)
}

func humanReadable(bytes uint64) string {
	return datasize.ByteSize(bytes).HumanReadable()
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
	Memory uint64
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
