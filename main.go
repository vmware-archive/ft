package main

import (
	"io"
	"os"
	"strings"

	"k8s.io/cli-runtime/pkg/genericclioptions"
	_ "k8s.io/client-go/plugin/pkg/client/auth"
	cmdutil "k8s.io/kubectl/pkg/cmd/util"

	"github.com/concourse/concourse/fly/ui"
	"github.com/concourse/ctop/accounts"
	"github.com/concourse/flag"
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
	// worker := accounts.NewLANWorker()
	kubeConfigFlags := genericclioptions.NewConfigFlags(true).WithDeprecatedPasswordFlag()
	f := cmdutil.NewFactory(kubeConfigFlags)
	restConfig, err := f.ToRESTConfig()
	if err != nil {
		panic(err)
	}
	worker := &accounts.GardenWorker{
		Dialer: &accounts.K8sGardenDialer{
			Conn: accounts.NewK8sConnection(restConfig),
		},
	}
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
