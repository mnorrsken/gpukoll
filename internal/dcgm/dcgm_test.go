package dcgm

import (
	"reflect"
	"strings"
	"testing"
)

func TestParse(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  []Device
	}{
		{
			name: "whole gpus with and without pods",
			input: `# HELP DCGM_FI_DEV_GPU_UTIL GPU utilization (in %).
# TYPE DCGM_FI_DEV_GPU_UTIL gauge
DCGM_FI_DEV_GPU_UTIL{gpu="1",UUID="GPU-b",device="nvidia1",modelName="NVIDIA H100 80GB HBM3",Hostname="n1",container="",namespace="",pod=""} 0
DCGM_FI_DEV_GPU_UTIL{gpu="0",UUID="GPU-a",device="nvidia0",modelName="NVIDIA H100 80GB HBM3",Hostname="n1",container="main",namespace="ml",pod="train-0"} 97
DCGM_FI_DEV_FB_USED{gpu="0",UUID="GPU-a",device="nvidia0",Hostname="n1",container="main",namespace="ml",pod="train-0"} 40000
DCGM_FI_DEV_FB_USED{gpu="1",UUID="GPU-b",device="nvidia1",Hostname="n1"} 0
go_goroutines 12
`,
			want: []Device{
				{GPU: 0, Instance: -1, Used: true},
				{GPU: 1, Instance: -1},
			},
		},
		{
			name: "mig instances replace their parent gpu",
			input: `DCGM_FI_DEV_POWER_USAGE{gpu="0",UUID="GPU-a",Hostname="n1"} 80
DCGM_FI_DEV_FB_USED{gpu="0",UUID="GPU-a",GPU_I_PROFILE="3g.20gb",GPU_I_ID="2",Hostname="n1"} 0
DCGM_FI_DEV_FB_USED{gpu="0",UUID="GPU-a",GPU_I_PROFILE="1g.5gb",GPU_I_ID="7",Hostname="n1",pod="p",namespace="ns"} 100
DCGM_FI_DEV_FB_USED{gpu="1",UUID="GPU-b",Hostname="n1"} 0
`,
			want: []Device{
				{GPU: 0, Instance: 2, Profile: "3g.20gb"},
				{GPU: 0, Instance: 7, Profile: "1g.5gb", Used: true},
				{GPU: 1, Instance: -1},
			},
		},
		{
			name:  "escaped label values",
			input: `DCGM_FI_DEV_FB_USED{gpu="0",modelName="a \"quoted\", name\\",pod="x"} 1` + "\n",
			want:  []Device{{GPU: 0, Instance: -1, Used: true}},
		},
		{
			name:  "no dcgm series",
			input: "process_cpu_seconds_total 3\n",
			want:  []Device{},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := Parse(strings.NewReader(tt.input))
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("Parse() = %+v, want %+v", got, tt.want)
			}
		})
	}
}

func TestParseBadLabels(t *testing.T) {
	for _, in := range []string{
		`DCGM_FI_DEV_FB_USED{gpu="0" 1`,
		`DCGM_FI_DEV_FB_USED{gpu=0} 1`,
		`DCGM_FI_DEV_FB_USED{gpu="0} 1`,
	} {
		if _, err := Parse(strings.NewReader(in + "\n")); err == nil {
			t.Errorf("Parse(%q) returned no error", in)
		}
	}
}
