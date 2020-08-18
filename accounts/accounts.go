package accounts

import (
	"fmt"
	"io"
	"strings"

	"github.com/concourse/concourse/atc/db"
	"github.com/concourse/concourse/fly/ui"
	"github.com/fatih/color"
	"github.com/jessevdk/go-flags"
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
	cmd := Command{}
	parser := flags.NewParser(&cmd, flags.HelpFlag|flags.PassDoubleDash)
	parser.NamespaceDelimiter = "-"
	_, err := parser.ParseArgs(args)
	return cmd, err
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
