# CronHPA

Cron Horizontal Pod Autoscaler(CronHPA) enables us to auto scale workloads(those support `scale` subresource, e.g. deployment, statefulset) periodically using [crontab](https://en.wikipedia.org/wiki/Cron) scheme.


`CronHPA` example:

```
apiVersion: extensions.tkestack.io/v1
kind: CronHPA
metadata:
  name: example-cron-hpa
spec:
  scaleTargetRef:
    apiVersion: apps/v1
    kind: Deployment
    name: demo-deployment
  crons:
    - schedule: "0 23 * * 5"  // Set replicas to 60 every Friday 23:00
      targetReplicas: 60
    - schedule: "0 23 * * 7"  // Set replicas to 30 every Sunday 23:00
      targetReplicas: 30
```

More design ideas could be found at [design.md](./design.md).

## Build

``` sh
$ make build
or
$ go build -o bin/cron-hpa-controller .
```

## Run

```sh
# assumes you have a working kubeconfig, not required if operating in-cluster
# It will create CRD `CronHPA` by default.
$ bin/cron-hpa-controller --master=127.0.0.1:8080 --v=5 --stderrthreshold=0   // Assume 127.0.0.1:8080 is k8s master ip:port
or
$ bin/cron-hpa-controller --kubeconfig=$HOME/.kube/config --v=5 --stderrthreshold=0

# create a custom resource of type cron-hpa
$ kubectl create -f artifacts/examples/example-cron-hpa.yaml

# check pods created through the custom resource
$ kubectl get cronhpa
```

## Cleanup

You can clean up the created CustomResourceDefinition with:

    $ kubectl delete crd cronhpas.extensions.tkestack.io
