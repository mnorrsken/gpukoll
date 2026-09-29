# Changelog

All notable changes to this project are documented here. Versions follow `vMAJOR.MINOR.PATCH`.

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
