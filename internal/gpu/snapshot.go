// Package gpu turns Kubernetes nodes and pods into a GPU inventory.
//
// GPU details come from the labels that NVIDIA GPU Feature Discovery (part of
// the GPU Operator) puts on each node. Usage comes from the GPU resource
// requests of pods scheduled on the node. A node is online when its Ready
// condition is True.
package gpu

import (
	"sort"
	"strconv"
	"strings"
	"time"

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

// Summary counts GPUs over all servers. GPUs on offline servers are
// counted in Total and Offline only.
type Summary struct {
	Total         int `json:"total"`
	Used          int `json:"used"`
	Available     int `json:"available"`
	Offline       int `json:"offline"`
	Servers       int `json:"servers"`
	ServersOnline int `json:"serversOnline"`
}

// Server is one GPU node.
type Server struct {
	Name    string    `json:"name"`
	Online  bool      `json:"online"`
	Status  string    `json:"status"`
	Since   time.Time `json:"since,omitzero"`
	Product string    `json:"product,omitempty"`
	Driver  string    `json:"driver,omitempty"`
	Total   int       `json:"total"`
	Used    int       `json:"used"`
	GPUs    []GPU     `json:"gpus"`
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
	resource  string
	product   string
	count     int
	memoryMiB int
	replicas  int
	mig       bool
}

// Build computes a snapshot from nodes and the pods running on them.
func Build(nodes []kube.Node, pods []kube.Pod, now time.Time) Snapshot {
	requested := map[string]map[string]int{} // node -> resource -> count
	for _, p := range pods {
		if p.Spec.NodeName == "" || p.Status.Phase == "Succeeded" || p.Status.Phase == "Failed" {
			continue
		}
		for res, n := range podRequests(p) {
			if requested[p.Spec.NodeName] == nil {
				requested[p.Spec.NodeName] = map[string]int{}
			}
			requested[p.Spec.NodeName][res] += n
		}
	}

	snap := Snapshot{UpdatedAt: now, Servers: []Server{}}
	for _, n := range nodes {
		groups := nodeGroups(n)
		if len(groups) == 0 && n.Metadata.Labels[labelPresent] != "true" {
			continue
		}
		s := Server{
			Name:    n.Metadata.Name,
			Status:  "Unknown",
			Product: n.Metadata.Labels[labelProduct],
			Driver:  n.Metadata.Labels[labelDriver],
			GPUs:    []GPU{},
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
		for _, g := range groups {
			used := ceilDiv(requested[s.Name][g.resource], g.replicas)
			for i := range g.count {
				s.GPUs = append(s.GPUs, GPU{
					Product:   g.product,
					MemoryMiB: g.memoryMiB,
					MIG:       g.mig,
					Used:      s.Online && i < used,
				})
			}
			if s.Online {
				s.Used += min(used, g.count)
			}
			s.Total += g.count
		}

		snap.Summary.Servers++
		snap.Summary.Total += s.Total
		if s.Online {
			snap.Summary.ServersOnline++
			snap.Summary.Used += s.Used
			snap.Summary.Available += s.Total - s.Used
		} else {
			snap.Summary.Offline += s.Total
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
			resource:  res,
			product:   l[labelProduct],
			count:     count,
			memoryMiB: atoi(l[labelMemory]),
			replicas:  replicas,
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
				resource:  res,
				product:   strings.TrimSpace(l[labelProduct] + " MIG " + p),
				count:     count,
				memoryMiB: atoi(l[res+".memory"]),
				replicas:  1,
				mig:       true,
			})
		}
	}
	return gs
}

// podRequests returns the NVIDIA resources a pod holds, the same way the
// scheduler counts them: the larger of the sum over containers and the
// largest init container.
func podRequests(p kube.Pod) map[string]int {
	sum := map[string]int{}
	for _, c := range p.Spec.Containers {
		for res, n := range containerRequests(c) {
			sum[res] += n
		}
	}
	for _, c := range p.Spec.InitContainers {
		for res, n := range containerRequests(c) {
			sum[res] = max(sum[res], n)
		}
	}
	return sum
}

func containerRequests(c kube.Container) map[string]int {
	out := map[string]int{}
	// Extended resources may set only limits; requests then default to them.
	for _, m := range []map[string]string{c.Resources.Limits, c.Resources.Requests} {
		for res, q := range m {
			if strings.HasPrefix(res, "nvidia.com/") {
				out[res] = atoi(q)
			}
		}
	}
	return out
}

func atoi(s string) int {
	n, err := strconv.Atoi(strings.TrimSpace(s))
	if err != nil || n < 0 {
		return 0
	}
	return n
}

func ceilDiv(a, b int) int {
	return (a + b - 1) / b
}
