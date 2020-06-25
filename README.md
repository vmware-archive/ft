* one binary that does it all - speaks kubernetes and talks to the worker, also speaks postgres and talks to the database and gives the whole container accounting story
* two binaries - one speaks postgres, the other 

prometheus exporter or operator CLI?
bosh release - http API? -- run on a different port

fly containers --workloads/--details/--pipelines

1. upload a binary to the worker, run it and capture stdout
1. port-forward to the worker and run the binary locally

# port-forwarding smart version

workeradapter - bosh ssh --opts "-L 7777:localhost:7777"/k8s port-forward <ns> <pod> 7777

workloads [--bosh-worker <deployment/vm>|--k8s-worker <ns/pod>|--lan-worker <address>] --postgres-config ...

bosh or kubectl cli must be installed and auth is handled out-of-band

reuse postgres flags fron github.com/concourse/flag

print data to stdout

docker-compose up -d
workloads --lan-worker localhost:7777
abd123 - team/pipeline/resource, other-team/other-pipeline/other-resource
aed892 - team/pipeline/job/build/name/step
ad0732 - N/A

```
type Worker
    = Bosh Deployment VMID
    | K8s Namespace PodName
    | LAN Address

type Workload
    = Check Team Pipeline Resource
    | Build Team Pipeline Job Build Step
```

```
type Worker interface {
	Containers(*StatsOption...) ([]Container, error)
}

type Container struct {
	Handle string
	Stats
}

type Sample struct {
	Container Container
	Workloads []Workload
}

type Account []Sample

type Accountant interface {
	Account([]Container) (Account, error)
}

type Workload interface {
	toString() string
}

dbResourceAccountant -> dbResourceWorkload
dbBuildAccountant -> dbBuildWorkload

func Samples(w Worker, a Accountant) (Account, error) {
	a.Account(w.Containers())
}

func main() {
	var w Worker
	var a Accountant
	// parse flags
	account := accounts.Account(w,s)
	fmt.Println(account)
}
```

how to account for a container?
-> how to account for a check container?
-> how to find the resource config check session for a container?

