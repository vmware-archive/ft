package main

import (
	"io"
	"os"
	"strings"

	_ "k8s.io/client-go/plugin/pkg/client/auth"

	"github.com/concourse/concourse/fly/ui"
	"github.com/concourse/flag"
	"github.com/concourse/ft/accounts"
	"github.com/fatih/color"
	flags "github.com/jessevdk/go-flags"
)

type Command struct {
	Postgres     flag.PostgresConfig `group:"PostgreSQL Configuration" namespace:"postgres"`
	K8sNamespace string              `long:"k8s-namespace"`
	K8sPod       string              `long:"k8s-pod"`
}

func main() {
	cmd := Command{}
	parser := flags.NewParser(&cmd, flags.HelpFlag|flags.PassDoubleDash)
	parser.NamespaceDelimiter = "-"
	_, err := parser.Parse()
	if err != nil {
		panic(err)
	}
	var dialer accounts.GardenDialer
	if cmd.K8sNamespace != "" && cmd.K8sPod != "" {
		restConfig, err := accounts.RESTConfig()
		if err != nil {
			panic(err)
		}
		dialer = &accounts.K8sGardenDialer{
			RESTConfig: restConfig,
			Namespace:  cmd.K8sNamespace,
			PodName:    cmd.K8sPod,
		}
	} else {
		dialer = &accounts.LANGardenDialer{}
	}
	worker := &accounts.GardenWorker{
		Dialer: dialer,
	}
	accountant := accounts.NewDBAccountant(cmd.Postgres)
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
