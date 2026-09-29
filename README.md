# gpukoll

A small web page that shows the GPU servers in a Kubernetes or OpenShift
cluster running the NVIDIA GPU Operator: how many GPUs there are, how many
are in use, and which servers are online.

- Each server is a frame. Each GPU is a block: green is available, orange
  (striped) is in use, grey means the server is offline. Block area follows
  GPU memory.
- No login. Every visitor is an anonymous, read-only guest.
- Light and dark mode (follows the OS, or pick one with the button top right).

## How it works

gpukoll polls the API server (default every 10s) and keeps the result in
memory, so the load does not grow with the number of viewers.

| What | Where it comes from |
|------|---------------------|
| GPU servers | nodes labelled `nvidia.com/gpu.present=true` or with `nvidia.com/gpu` capacity |
| GPU count | node capacity `nvidia.com/gpu`, falling back to label `nvidia.com/gpu.count` |
| Model, memory, driver | GPU Feature Discovery labels `nvidia.com/gpu.product`, `nvidia.com/gpu.memory`, `nvidia.com/cuda.driver-version.full` |
| GPUs in use | `nvidia.com/*` requests of pods on the node that have not finished |
| Online | node `Ready` condition is `True` |

MIG with the `mixed` strategy shows one block per MIG device
(`nvidia.com/mig-<profile>.count`). With time-slicing
(`nvidia.com/gpu.replicas`), a GPU counts as in use when any of its
slices is taken.

## Helm chart

```bash
helm install gpukoll oci://ghcr.io/mnorrsken/charts/gpukoll \
  --namespace gpukoll --create-namespace
```

The chart creates a ClusterRole with `get` and `list` on `nodes` and
`pods`, bound to the gpukoll service account. No Secret is needed.

On **OpenShift** a Route with edge TLS is created automatically (the chart
checks for `route.openshift.io/v1`). The pod sets no `runAsUser`, so it
runs under the default `restricted-v2` SCC. To set the hostname:

```bash
helm install gpukoll oci://ghcr.io/mnorrsken/charts/gpukoll \
  --namespace gpukoll --create-namespace \
  --set route.host=gpukoll.apps.example.com
```

On other clusters, enable the Ingress:

```yaml
ingress:
  enabled: true
  className: nginx
  hosts:
    - host: gpukoll.example.com
      paths:
        - path: /
          pathType: Prefix
```

Other values: `config.interval` (poll interval, default `10s`), `route.enabled`,
`rbac.create`, plus the usual `image`, `resources`, `nodeSelector`,
`tolerations`, `affinity`.

With `helm template`, pass `--api-versions route.openshift.io/v1/Route` to
render the Route.

## Development

```bash
make test
kubectl proxy &      # port 8001
make run             # http://localhost:8080
```

`make docker-build` builds the image, `make helm-lint` checks the chart.
