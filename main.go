package main

import (
	"io"
	"os"
	"strings"

	"github.com/concourse/concourse/fly/ui"
	"github.com/concourse/flag"
	"github.com/concourse/ctop/accounts"
	"github.com/fatih/color"
	flags "github.com/jessevdk/go-flags"
)

func main() {
	postgresConfig := flag.PostgresConfig{}
	parser := flags.NewParser(&postgresConfig, flags.HelpFlag|flags.PassDoubleDash)
	_, err := parser.Parse()
	if err != nil {
		panic(err)
	}
	worker := accounts.NewLANWorker()
	accountant := accounts.NewDBAccountant(postgresConfig)
	samples, err := accounts.Account(worker, accountant)
	if err != nil {
		panic(err)
	}
	err = printSamples(os.Stdout, samples)
	if err != nil {
		panic(err)
	}
}

func printSamples(writer io.Writer, samples []accounts.Sample) error {
	data := []ui.TableRow{}
	for _, sample := range samples {
		workloads := []string{}
		for _, w := range sample.Workloads {
			workloads = append(workloads, w.ToString())
		}
		data = append(data, ui.TableRow{
			ui.TableCell{Contents: sample.Container.Handle},
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
				Contents: "workloads",
				Color:    color.New(color.Bold),
			},
		},
		Data: data,
	}
	return table.Render(writer, true)
}
