# Changelog

All notable changes to this project are documented here. Versions follow `vMAJOR.MINOR.PATCH`.

## Unreleased

### Added
- **Mixed GPU kinds on one node** — GPU Feature Discovery labels describe only one GPU kind per node, so gpukoll now reads each whole GPU's model (`modelName` label) and memory (`DCGM_FI_DEV_FB_FREE` + `FB_USED` + `FB_RESERVED`) from the DCGM exporter. Labels are the fallback when the exporter has no data for a GPU. MIG devices still use labels. The server line lists each kind, e.g. "2× NVIDIA H100 80GB HBM3 · 80 GB · 2× NVIDIA L4 · 22 GB". `FB_RESERVED` is not in the exporter's default counters in older versions; without it, memory reads slightly low.
- **Cordoned nodes** — a node with `spec.unschedulable: true` (`kubectl cordon`) shows a "Cordoned" pill next to Online/Offline. The JSON API has a new `cordoned` boolean per server. Summary counts do not change: free GPUs on a cordoned server still count as available. No new RBAC.

## v0.3.0

### Added
- **Optional NetworkPolicy** — `networkPolicy.enabled=true` adds a NetworkPolicy in `dcgm.namespace` that lets gpukoll reach the DCGM exporter pods on TCP 9400. Off by default: only enable it when that namespace already has policies selecting the exporter pods, since a new policy would otherwise block other scrapers such as Prometheus. `networkPolicy.exporterPodLabels` and `networkPolicy.exporterPort` set the exporter pod labels and port.
- **Screenshot** — README shows gpukoll with demo data.
- **Dependabot** — weekly updates for Go modules, GitHub Actions and the Docker base images.

## v0.2.1

### Fixed
- **In-use GPUs shown as available** — the GPU Operator labels every GPU node `nvidia.com/mig.strategy=single`, even without MIG, and gpukoll then only looked for MIG instances. gpukoll now uses whole GPUs when the DCGM exporter reports any and falls back to MIG instances only when there are none.

## v0.2.0

### Changed
- **GPU usage from DCGM** — usage now comes from the NVIDIA DCGM exporter instead of pod resource requests. A GPU is in use when its metrics carry a `pod` label. This needs `DCGM_EXPORTER_KUBERNETES=true`, which is the GPU Operator default. The UI marks the exact GPUs in use instead of the first N.
- **Smaller RBAC** — the ClusterRole now only has `list` on nodes (no pods, no `get`). A new Role in the DCGM exporter namespace allows `list` on `endpointslices` (`discovery.k8s.io`). gpukoll can no longer read pod specs.

### Added
- **DCGM settings** — chart values `dcgm.namespace` (default `nvidia-gpu-operator`, use `gpu-operator` for the upstream chart) and `dcgm.service` (default `nvidia-dcgm-exporter`), and flags `-dcgm-namespace` and `-dcgm-service`.
- **Usage unknown state** — when a server is up but its exporter cannot be scraped, its blocks are dashed and the reason is shown. Those GPUs count as unknown (not available, not in use).
- **MIG single strategy** — MIG instances are now mapped to blocks for the "single" strategy too (mixed was already supported).
- **Local dev proxy** — `-dcgm-proxy` flag scrapes through the API server pod proxy. `make run` uses it.

### Notes
- On upgrade, set `dcgm.namespace` if the GPU Operator is not in `nvidia-gpu-operator`.
- A NetworkPolicy in that namespace must allow ingress from gpukoll on TCP 9400.

## v0.1.0

### Added
- **GPU dashboard** — web UI that lists GPU servers, read from NVIDIA GPU Operator / GPU Feature Discovery node labels.
- **GPU blocks** — one block per GPU, grouped per server and sized by GPU memory. Green means available, orange striped means in use, grey means the server is offline.
- **Summary tiles** — available, in use, total and servers online.
- **Status detection** — in-use count comes from `nvidia.com/*` pod requests; a server is online when its node Ready condition is true.
- **MIG and time-slicing** — MIG mixed strategy and time-slicing are supported.
- **Open access** — anonymous, read-only, no login.
- **Themes** — light, dark and system theme.
- **Helm chart** — `oci://ghcr.io/mnorrsken/charts/gpukoll` with a ClusterRole (get/list on nodes and pods), an OpenShift Route created automatically when `route.openshift.io/v1` exists, `restricted-v2` SCC compatibility, and an optional Ingress.
- **Container image** — multi-arch image at `ghcr.io/mnorrsken/gpukoll`.
