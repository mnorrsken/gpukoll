// Package gpu turns Kubernetes nodes and pods into a GPU inventory.
//
// GPU details come from the labels that NVIDIA GPU Feature Discovery (part of
// the GPU Operator) puts on each node. Those describe one kind of GPU per
// node, so the model and memory the DCGM exporter reports for each GPU take
// precedence. Usage comes from the node's DCGM exporter: a GPU is in use
// when it is allocated to a pod. A node is online when its Ready condition
// is True, and cordoned when it is marked unschedulable.
package gpu

import (
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/mnorrsken/gpukoll/internal/dcgm"
	"github.com/mnorrsken/gpukoll/internal/kube"
)

const (
	labelPresent  = "nvidia.com/gpu.present"
	labelCount    = "nvidia.com/gpu.count"
	labelMemory   = "nvidia.com/gpu.memory"
	labelProduct  = "nvidia.com/gpu.product"
	labelReplicas = "nvidia.com/gpu.replicas"
	labelDriver   = "nvidia.com/cuda.driver-version.full"
	labelMIG      = "nvidia.com/mig.strategy"

	resGPU       = "nvidia.com/gpu"
	resGPUShared = "nvidia.com/gpu.shared"
	migPrefix    = "nvidia.com/mig-"
)

// Snapshot is the state of all GPU servers at one point in time.
type Snapshot struct {
	UpdatedAt time.Time `json:"updatedAt"`
	Summary   Summary   `json:"summary"`
	Servers   []Server  `json:"servers"`
}

// Summary counts GPUs over all servers. GPUs on offline servers, and on
// servers whose usage is unknown, are counted in Total and Offline or
// Unknown only.
type Summary struct {
	Total         int `json:"total"`
	Used          int `json:"used"`
	Available     int `json:"available"`
	Offline       int `json:"offline"`
	Unknown       int `json:"unknown"`
	Servers       int `json:"servers"`
	ServersOnline int `json:"serversOnline"`
}

// Server is one GPU node.
type Server struct {
	Name     string    `json:"name"`
	Online   bool      `json:"online"`
	Cordoned bool      `json:"cordoned"`
	Status   string    `json:"status"`
	Since    time.Time `json:"since,omitzero"`
	Product  string    `json:"product,omitempty"`
	Driver   string    `json:"driver,omitempty"`
	Total    int       `json:"total"`
	Used     int       `json:"used"`
	// UsageKnown is false when the node's DCGM exporter could not be read;
	// UsageError then says why.
	UsageKnown bool   `json:"usageKnown"`
	UsageError string `json:"usageError,omitempty"`
	GPUs       []GPU  `json:"gpus"`
}

// GPU is one schedulable GPU or MIG device.
type GPU struct {
	Product   string `json:"product,omitempty"`
	MemoryMiB int    `json:"memoryMiB,omitempty"`
	MIG       bool   `json:"mig,omitempty"`
	Used      bool   `json:"used"`
}

// group is a set of identical devices exposed as one extended resource.
type group struct {
	product   string
	count     int
	memoryMiB int
	// migProfile is set for the MIG instances of one profile (strategy
	// "mixed"). Otherwise the group is nvidia.com/gpu.
	migProfile string
	// mixed is set on nvidia.com/gpu when the node uses the "mixed" MIG
	// strategy; MIG instances then have groups of their own.
	mixed bool
}

func (g group) mig() bool {
	return g.migProfile != "" || strings.Contains(g.product, "-MIG-")
}

// devices returns the DCGM devices that belong to the group.
func (g group) devices(all []dcgm.Device) []dcgm.Device {
	var whole, mig []dcgm.Device
	for _, d := range all {
		switch {
		case !d.MIG():
			whole = append(whole, d)
		case g.migProfile == "" || d.Profile == g.migProfile:
			mig = append(mig, d)
		}
	}
	if g.migProfile != "" {
		return mig
	}
	// nvidia.com/gpu is whole GPUs, or MIG instances when the node runs MIG
	// with the "single" strategy. The nvidia.com/mig.strategy label can't
	// tell these apart: the GPU Operator sets it to "single" on every GPU
	// node, MIG or not. So go by what the exporter reports.
	if len(whole) > 0 || g.mixed {
		return whole
	}
	return mig
}

// Usage is what a node's DCGM exporter reported.
type Usage struct {
	Devices []dcgm.Device
	Err     error
}

// Build computes a snapshot from nodes and the DCGM usage of each node,
// keyed by node name. A node missing from usage has unknown usage.
func Build(nodes []kube.Node, usage map[string]Usage, now time.Time) Snapshot {
	snap := Snapshot{UpdatedAt: now, Servers: []Server{}}
	for _, n := range nodes {
		groups := nodeGroups(n)
		if len(groups) == 0 && n.Metadata.Labels[labelPresent] != "true" {
			continue
		}
		s := Server{
			Name:     n.Metadata.Name,
			Cordoned: n.Spec.Unschedulable,
			Status:   "Unknown",
			Product:  n.Metadata.Labels[labelProduct],
			Driver:   n.Metadata.Labels[labelDriver],
			GPUs:     []GPU{},
		}
		for _, c := range n.Status.Conditions {
			if c.Type == "Ready" {
				s.Online = c.Status == "True"
				s.Since = c.LastTransitionTime
				if s.Online {
					s.Status = "Ready"
				} else {
					s.Status = "NotReady"
					if c.Reason != "" {
						s.Status = c.Reason
					}
				}
			}
		}
		u, ok := usage[s.Name]
		switch {
		case !s.Online:
		case !ok:
			s.UsageError = "no ready DCGM exporter on this node"
		case u.Err != nil:
			s.UsageError = u.Err.Error()
		default:
			s.UsageKnown = true
		}
		for _, g := range groups {
			devs := g.devices(u.Devices)
			for i := range g.count {
				used := s.UsageKnown && i < len(devs) && devs[i].Used
				if used {
					s.Used++
				}
				gp := GPU{
					Product:   g.product,
					MemoryMiB: g.memoryMiB,
					MIG:       g.mig(),
					Used:      used,
				}
				// The exporter's model and memory hold even when usage is
				// not trusted, as for a node that just went NotReady.
				if i < len(devs) && !devs[i].MIG() {
					if devs[i].Model != "" {
						gp.Product = devs[i].Model
					}
					if devs[i].MemoryMiB > 0 {
						gp.MemoryMiB = devs[i].MemoryMiB
					}
				}
				s.GPUs = append(s.GPUs, gp)
			}
			s.Total += g.count
		}

		snap.Summary.Servers++
		snap.Summary.Total += s.Total
		if s.Online {
			snap.Summary.ServersOnline++
		}
		switch {
		case !s.Online:
			snap.Summary.Offline += s.Total
		case !s.UsageKnown:
			snap.Summary.Unknown += s.Total
		default:
			snap.Summary.Used += s.Used
			snap.Summary.Available += s.Total - s.Used
		}
		snap.Servers = append(snap.Servers, s)
	}
	sort.Slice(snap.Servers, func(i, j int) bool { return snap.Servers[i].Name < snap.Servers[j].Name })
	return snap
}

// nodeGroups returns the GPU device groups on a node. Counts come from the
// node capacity (what the device plugin advertises) and fall back to the
// GPU Feature Discovery labels, so a node whose device plugin is down, or
// whose kubelet stopped reporting, still shows its GPUs.
func nodeGroups(n kube.Node) []group {
	l := n.Metadata.Labels
	capacity := n.Status.Capacity

	replicas := atoi(l[labelReplicas])
	if replicas < 1 {
		replicas = 1
	}
	res := resGPU
	if _, ok := capacity[resGPU]; !ok {
		if _, ok := capacity[resGPUShared]; ok {
			res = resGPUShared
		}
	}
	var gs []group
	count := atoi(capacity[res]) / replicas
	if count == 0 {
		count = atoi(l[labelCount])
	}
	if count > 0 {
		gs = append(gs, group{
			product:   l[labelProduct],
			count:     count,
			memoryMiB: atoi(l[labelMemory]),
			mixed:     l[labelMIG] == "mixed",
		})
	}

	// With the "mixed" MIG strategy each MIG profile is its own resource,
	// labelled nvidia.com/mig-<profile>.count and .memory.
	if l[labelMIG] == "mixed" {
		var profiles []string
		for k := range l {
			if p, ok := strings.CutPrefix(k, migPrefix); ok && strings.HasSuffix(p, ".count") {
				profiles = append(profiles, strings.TrimSuffix(p, ".count"))
			}
		}
		sort.Strings(profiles)
		for _, p := range profiles {
			res := migPrefix + p
			count := atoi(capacity[res])
			if count == 0 {
				count = atoi(l[res+".count"])
			}
			if count == 0 {
				continue
			}
			gs = append(gs, group{
				product:    strings.TrimSpace(l[labelProduct] + " MIG " + p),
				count:      count,
				memoryMiB:  atoi(l[res+".memory"]),
				migProfile: p,
			})
		}
	}
	return gs
}

func atoi(s string) int {
	n, err := strconv.Atoi(strings.TrimSpace(s))
	if err != nil || n < 0 {
		return 0
	}
	return n
}
