package gpu

import (
	"testing"
	"time"

	"github.com/mnorrsken/gpukoll/internal/kube"
)

func node(t *testing.T, name string, ready string, labels, capacity map[string]string) kube.Node {
	t.Helper()
	var n kube.Node
	n.Metadata.Name = name
	n.Metadata.Labels = labels
	n.Status.Capacity = capacity
	n.Status.Conditions = []kube.Condition{{Type: "Ready", Status: ready, Reason: "KubeletReady"}}
	return n
}

func pod(t *testing.T, nodeName, phase string, requests ...map[string]string) kube.Pod {
	t.Helper()
	var p kube.Pod
	p.Spec.NodeName = nodeName
	p.Status.Phase = phase
	for _, r := range requests {
		var c kube.Container
		c.Resources.Limits = r
		p.Spec.Containers = append(p.Spec.Containers, c)
	}
	return p
}

func TestBuild(t *testing.T) {
	a100 := map[string]string{
		labelPresent: "true",
		labelCount:   "8",
		labelMemory:  "81920",
		labelProduct: "NVIDIA-A100-SXM4-80GB",
	}
	tests := []struct {
		name        string
		nodes       []kube.Node
		pods        []kube.Pod
		wantServer  []int // total, used per server
		wantSummary Summary
	}{
		{
			name: "online node with pods",
			nodes: []kube.Node{
				node(t, "gpu1", "True", a100, map[string]string{resGPU: "8"}),
			},
			pods: []kube.Pod{
				pod(t, "gpu1", "Running", map[string]string{resGPU: "2"}, map[string]string{"cpu": "1"}),
				pod(t, "gpu1", "Pending", map[string]string{resGPU: "1"}),
				pod(t, "gpu1", "Succeeded", map[string]string{resGPU: "4"}),
				pod(t, "", "Pending", map[string]string{resGPU: "4"}),
			},
			wantServer:  []int{8, 3},
			wantSummary: Summary{Total: 8, Used: 3, Available: 5, Servers: 1, ServersOnline: 1},
		},
		{
			name: "offline node counts as offline",
			nodes: []kube.Node{
				node(t, "gpu1", "True", a100, map[string]string{resGPU: "8"}),
				node(t, "gpu2", "Unknown", a100, map[string]string{resGPU: "8"}),
			},
			pods: []kube.Pod{
				pod(t, "gpu2", "Running", map[string]string{resGPU: "8"}),
			},
			wantServer:  []int{8, 0, 8, 0},
			wantSummary: Summary{Total: 16, Used: 0, Available: 8, Offline: 8, Servers: 2, ServersOnline: 1},
		},
		{
			name: "label count used when device plugin is down",
			nodes: []kube.Node{
				node(t, "gpu1", "True", a100, nil),
			},
			wantServer:  []int{8, 0},
			wantSummary: Summary{Total: 8, Available: 8, Servers: 1, ServersOnline: 1},
		},
		{
			name: "non gpu node is skipped",
			nodes: []kube.Node{
				node(t, "cpu1", "True", map[string]string{"kubernetes.io/os": "linux"}, map[string]string{"cpu": "32"}),
			},
			wantSummary: Summary{},
		},
		{
			name: "time slicing rounds up to physical gpus",
			nodes: []kube.Node{
				node(t, "gpu1", "True", map[string]string{labelCount: "2", labelReplicas: "4"}, map[string]string{resGPU: "8"}),
			},
			pods: []kube.Pod{
				pod(t, "gpu1", "Running", map[string]string{resGPU: "1"}),
				pod(t, "gpu1", "Running", map[string]string{resGPU: "4"}),
			},
			wantServer:  []int{2, 2},
			wantSummary: Summary{Total: 2, Used: 2, Servers: 1, ServersOnline: 1},
		},
		{
			name: "mixed mig strategy",
			nodes: []kube.Node{
				node(t, "gpu1", "True", map[string]string{
					labelPresent:                   "true",
					labelMIG:                       "mixed",
					labelProduct:                   "NVIDIA-A100-SXM4-40GB",
					"nvidia.com/mig-1g.5gb.count":  "7",
					"nvidia.com/mig-1g.5gb.memory": "4864",
					"nvidia.com/mig-3g.20gb.count": "2",
				}, map[string]string{resGPU: "1", "nvidia.com/mig-1g.5gb": "7", "nvidia.com/mig-3g.20gb": "2"}),
			},
			pods: []kube.Pod{
				pod(t, "gpu1", "Running", map[string]string{"nvidia.com/mig-1g.5gb": "3"}),
			},
			wantServer:  []int{10, 3},
			wantSummary: Summary{Total: 10, Used: 3, Available: 7, Servers: 1, ServersOnline: 1},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			snap := Build(tt.nodes, tt.pods, time.Unix(0, 0))
			if snap.Summary != tt.wantSummary {
				t.Errorf("summary = %+v, want %+v", snap.Summary, tt.wantSummary)
			}
			var got []int
			for _, s := range snap.Servers {
				got = append(got, s.Total, s.Used)
				used := 0
				for _, g := range s.GPUs {
					if g.Used {
						used++
					}
				}
				if used != s.Used || len(s.GPUs) != s.Total {
					t.Errorf("%s: %d blocks with %d used, want %d with %d", s.Name, len(s.GPUs), used, s.Total, s.Used)
				}
			}
			if len(got) != len(tt.wantServer) {
				t.Fatalf("servers = %v, want %v", got, tt.wantServer)
			}
			for i := range got {
				if got[i] != tt.wantServer[i] {
					t.Fatalf("servers = %v, want %v", got, tt.wantServer)
				}
			}
		})
	}
}

func TestPodRequestsInitContainer(t *testing.T) {
	p := pod(t, "n", "Running", map[string]string{resGPU: "1"})
	var ic kube.Container
	ic.Resources.Requests = map[string]string{resGPU: "2"}
	p.Spec.InitContainers = []kube.Container{ic}
	if got := podRequests(p)[resGPU]; got != 2 {
		t.Errorf("podRequests = %d, want 2", got)
	}
}
