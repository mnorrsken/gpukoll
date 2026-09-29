package gpu

import (
	"errors"
	"testing"
	"time"

	"github.com/mnorrsken/gpukoll/internal/dcgm"
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

// gpus returns whole-GPU devices; used lists the indexes allocated to pods.
func gpus(t *testing.T, n int, used ...int) Usage {
	t.Helper()
	var u Usage
	for i := range n {
		u.Devices = append(u.Devices, dcgm.Device{GPU: i, Instance: -1})
	}
	for _, i := range used {
		u.Devices[i].Used = true
	}
	return u
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
		usage       map[string]Usage
		wantUsed    map[string][]bool // per server, per block
		wantSummary Summary
	}{
		{
			name:        "used gpus follow dcgm indexes",
			nodes:       []kube.Node{node(t, "gpu1", "True", a100, map[string]string{resGPU: "8"})},
			usage:       map[string]Usage{"gpu1": gpus(t, 8, 1, 6)},
			wantUsed:    map[string][]bool{"gpu1": {false, true, false, false, false, false, true, false}},
			wantSummary: Summary{Total: 8, Used: 2, Available: 6, Servers: 1, ServersOnline: 1},
		},
		{
			name: "offline node is grey even with stale usage",
			nodes: []kube.Node{
				node(t, "gpu1", "True", a100, map[string]string{resGPU: "8"}),
				node(t, "gpu2", "Unknown", a100, map[string]string{resGPU: "8"}),
			},
			usage: map[string]Usage{"gpu1": gpus(t, 8), "gpu2": gpus(t, 8, 0, 1)},
			wantUsed: map[string][]bool{
				"gpu1": make([]bool, 8),
				"gpu2": make([]bool, 8),
			},
			wantSummary: Summary{Total: 16, Available: 8, Offline: 8, Servers: 2, ServersOnline: 1},
		},
		{
			name: "missing or failing exporter means unknown usage",
			nodes: []kube.Node{
				node(t, "gpu1", "True", a100, map[string]string{resGPU: "8"}),
				node(t, "gpu2", "True", a100, map[string]string{resGPU: "8"}),
			},
			usage:       map[string]Usage{"gpu2": {Err: errors.New("connection refused")}},
			wantUsed:    map[string][]bool{"gpu1": make([]bool, 8), "gpu2": make([]bool, 8)},
			wantSummary: Summary{Total: 16, Unknown: 16, Servers: 2, ServersOnline: 2},
		},
		{
			name:        "label count used when device plugin is down",
			nodes:       []kube.Node{node(t, "gpu1", "True", a100, nil)},
			usage:       map[string]Usage{"gpu1": gpus(t, 8, 0)},
			wantUsed:    map[string][]bool{"gpu1": {true, false, false, false, false, false, false, false}},
			wantSummary: Summary{Total: 8, Used: 1, Available: 7, Servers: 1, ServersOnline: 1},
		},
		{
			name:        "non gpu node is skipped",
			nodes:       []kube.Node{node(t, "cpu1", "True", map[string]string{"kubernetes.io/os": "linux"}, map[string]string{"cpu": "32"})},
			wantSummary: Summary{},
		},
		{
			// The GPU Operator labels every GPU node with strategy "single",
			// MIG or not.
			name:        "single strategy label on a non mig node",
			nodes:       []kube.Node{node(t, "gpu1", "True", map[string]string{labelCount: "2", labelMIG: "single"}, map[string]string{resGPU: "2"})},
			usage:       map[string]Usage{"gpu1": gpus(t, 2, 0, 1)},
			wantUsed:    map[string][]bool{"gpu1": {true, true}},
			wantSummary: Summary{Total: 2, Used: 2, Servers: 1, ServersOnline: 1},
		},
		{
			name:        "time slicing shows physical gpus",
			nodes:       []kube.Node{node(t, "gpu1", "True", map[string]string{labelCount: "2", labelReplicas: "4"}, map[string]string{resGPU: "8"})},
			usage:       map[string]Usage{"gpu1": gpus(t, 2, 1)},
			wantUsed:    map[string][]bool{"gpu1": {false, true}},
			wantSummary: Summary{Total: 2, Used: 1, Available: 1, Servers: 1, ServersOnline: 1},
		},
		{
			name: "mixed mig strategy",
			nodes: []kube.Node{node(t, "gpu1", "True", map[string]string{
				labelPresent:                   "true",
				labelMIG:                       "mixed",
				labelProduct:                   "NVIDIA-A100-SXM4-40GB",
				"nvidia.com/mig-1g.5gb.count":  "2",
				"nvidia.com/mig-1g.5gb.memory": "4864",
				"nvidia.com/mig-3g.20gb.count": "1",
			}, map[string]string{resGPU: "1", "nvidia.com/mig-1g.5gb": "2", "nvidia.com/mig-3g.20gb": "1"})},
			usage: map[string]Usage{"gpu1": {Devices: []dcgm.Device{
				{GPU: 0, Instance: 1, Profile: "3g.20gb", Used: true},
				{GPU: 0, Instance: 7, Profile: "1g.5gb"},
				{GPU: 0, Instance: 8, Profile: "1g.5gb", Used: true},
				{GPU: 1, Instance: -1},
			}}},
			// whole GPU first, then MIG profiles in name order
			wantUsed:    map[string][]bool{"gpu1": {false, false, true, true}},
			wantSummary: Summary{Total: 4, Used: 2, Available: 2, Servers: 1, ServersOnline: 1},
		},
		{
			name: "single mig strategy",
			nodes: []kube.Node{node(t, "gpu1", "True", map[string]string{
				labelMIG:     "single",
				labelCount:   "3",
				labelProduct: "A100-SXM4-40GB-MIG-1g.5gb",
			}, nil)},
			usage: map[string]Usage{"gpu1": {Devices: []dcgm.Device{
				{GPU: 0, Instance: 7, Profile: "1g.5gb"},
				{GPU: 0, Instance: 8, Profile: "1g.5gb", Used: true},
				{GPU: 0, Instance: 9, Profile: "1g.5gb"},
			}}},
			wantUsed:    map[string][]bool{"gpu1": {false, true, false}},
			wantSummary: Summary{Total: 3, Used: 1, Available: 2, Servers: 1, ServersOnline: 1},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			snap := Build(tt.nodes, tt.usage, time.Unix(0, 0))
			if snap.Summary != tt.wantSummary {
				t.Errorf("summary = %+v, want %+v", snap.Summary, tt.wantSummary)
			}
			if len(snap.Servers) != len(tt.wantUsed) {
				t.Fatalf("got %d servers, want %d", len(snap.Servers), len(tt.wantUsed))
			}
			for _, s := range snap.Servers {
				want := tt.wantUsed[s.Name]
				if len(s.GPUs) != len(want) || len(s.GPUs) != s.Total {
					t.Fatalf("%s: %d blocks, total %d, want %d", s.Name, len(s.GPUs), s.Total, len(want))
				}
				used := 0
				for i, g := range s.GPUs {
					if g.Used != want[i] {
						t.Errorf("%s: block %d used = %v, want %v", s.Name, i, g.Used, want[i])
					}
					if g.Used {
						used++
					}
				}
				if used != s.Used {
					t.Errorf("%s: Used = %d, blocks say %d", s.Name, s.Used, used)
				}
			}
		})
	}
}

func TestBuildUsageError(t *testing.T) {
	nodes := []kube.Node{node(t, "gpu1", "True", map[string]string{labelCount: "1"}, nil)}
	snap := Build(nodes, map[string]Usage{"gpu1": {Err: errors.New("boom")}}, time.Unix(0, 0))
	s := snap.Servers[0]
	if s.UsageKnown || s.UsageError != "boom" {
		t.Errorf("UsageKnown = %v, UsageError = %q", s.UsageKnown, s.UsageError)
	}
}
