// Package dcgm reads GPU usage from an NVIDIA DCGM exporter.
//
// The exporter must run with Kubernetes pod mapping on
// (DCGM_EXPORTER_KUBERNETES=true, the GPU Operator default). It then adds
// pod, namespace and container labels to the series of every GPU or MIG
// instance that is allocated to a pod.
package dcgm

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	neturl "net/url"
	"sort"
	"strconv"
	"strings"
)

// maxBody caps how much of a /metrics response is read.
const maxBody = 16 << 20

// Device is one GPU, or one MIG instance, reported by the exporter.
type Device struct {
	GPU      int    // GPU index on the node
	Instance int    // MIG GPU instance ID, -1 for a whole GPU
	Profile  string // MIG profile such as "1g.5gb"
	Used     bool   // allocated to a pod
	// Model and MemoryMiB are set for whole GPUs when the exporter reports
	// them (modelName label, DCGM_FI_DEV_FB_* series), so GPUs of different
	// kinds on one node can be told apart.
	Model     string
	MemoryMiB int
}

// MIG reports whether the device is a MIG instance.
func (d Device) MIG() bool { return d.Instance >= 0 }

// Scrape fetches and parses the exporter's metrics at url.
func Scrape(ctx context.Context, hc *http.Client, url string) ([]Device, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	resp, err := hc.Do(req)
	if err != nil {
		// Drop the *url.Error wrapper, which repeats the URL.
		if ue, ok := errors.AsType[*neturl.Error](err); ok {
			err = ue.Err
		}
		return nil, fmt.Errorf("scraping %s: %w", url, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("scraping %s: %s", url, resp.Status)
	}
	devs, err := Parse(io.LimitReader(resp.Body, maxBody))
	if err != nil {
		return nil, fmt.Errorf("parsing %s: %w", url, err)
	}
	return devs, nil
}

// Framebuffer series in MiB. Free + used + reserved is the GPU's memory.
var fbSeries = map[string]bool{
	"DCGM_FI_DEV_FB_FREE":     true,
	"DCGM_FI_DEV_FB_USED":     true,
	"DCGM_FI_DEV_FB_RESERVED": true,
}

// Parse reads Prometheus text exposition and returns the devices found in
// DCGM_FI_* series, sorted by GPU index and MIG instance. A GPU that has
// MIG instances is left out; its instances are returned instead.
func Parse(r io.Reader) ([]Device, error) {
	type key struct{ gpu, inst int }
	found := map[key]*Device{}
	migGPUs := map[int]bool{}
	// Framebuffer values per whole GPU and series, so a series repeated
	// with other labels is not counted twice.
	fb := map[int]map[string]float64{}

	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 64<<10), 1<<20)
	for sc.Scan() {
		line := sc.Text()
		if !strings.HasPrefix(line, "DCGM_FI_") {
			continue
		}
		labels, rest, err := parseLabels(line)
		if err != nil {
			return nil, err
		}
		gpu, err := strconv.Atoi(labels["gpu"])
		if err != nil {
			continue
		}
		k := key{gpu, -1}
		if id, ok := labels["GPU_I_ID"]; ok && id != "" {
			inst, err := strconv.Atoi(id)
			if err != nil {
				continue
			}
			k.inst = inst
			migGPUs[gpu] = true
		}
		d := found[k]
		if d == nil {
			d = &Device{GPU: k.gpu, Instance: k.inst}
			found[k] = d
		}
		if p := labels["GPU_I_PROFILE"]; p != "" {
			d.Profile = p
		}
		if labels["pod"] != "" {
			d.Used = true
		}
		if k.inst >= 0 {
			continue
		}
		if m := labels["modelName"]; m != "" {
			d.Model = m
		}
		if name := line[:strings.IndexAny(line, "{ \t")]; fbSeries[name] {
			if v, err := sampleValue(rest); err == nil {
				if fb[gpu] == nil {
					fb[gpu] = map[string]float64{}
				}
				fb[gpu][name] = v
			}
		}
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}

	devs := make([]Device, 0, len(found))
	for k, d := range found {
		if k.inst < 0 && migGPUs[k.gpu] {
			continue
		}
		if k.inst < 0 {
			var mib float64
			for _, v := range fb[k.gpu] {
				mib += v
			}
			d.MemoryMiB = int(mib)
		}
		devs = append(devs, *d)
	}
	sort.Slice(devs, func(i, j int) bool {
		if devs[i].GPU != devs[j].GPU {
			return devs[i].GPU < devs[j].GPU
		}
		return devs[i].Instance < devs[j].Instance
	})
	return devs, nil
}

// sampleValue returns the value of a sample from the text after its
// labels, such as ` 42` or ` 42 1700000000000`.
func sampleValue(rest string) (float64, error) {
	f := strings.Fields(rest)
	if len(f) == 0 {
		return 0, errors.New("no sample value")
	}
	return strconv.ParseFloat(f[0], 64)
}

// parseLabels returns the labels of one sample line such as
// `NAME{a="1",b="x\"y"} 42`, and the text after them.
func parseLabels(line string) (map[string]string, string, error) {
	labels := map[string]string{}
	i := strings.IndexAny(line, "{ \t")
	if i < 0 {
		return labels, "", nil
	}
	if line[i] != '{' {
		return labels, line[i:], nil
	}
	s := line[i+1:]
	for {
		s = strings.TrimLeft(s, " \t,")
		if s == "" {
			return nil, "", fmt.Errorf("unterminated labels in %q", line)
		}
		if s[0] == '}' {
			return labels, s[1:], nil
		}
		eq := strings.IndexByte(s, '=')
		if eq < 0 || eq+1 >= len(s) || s[eq+1] != '"' {
			return nil, "", fmt.Errorf("bad label in %q", line)
		}
		name := strings.TrimSpace(s[:eq])
		s = s[eq+2:]
		var b strings.Builder
		closed := false
		for j := 0; j < len(s); j++ {
			c := s[j]
			if c == '\\' && j+1 < len(s) {
				j++
				switch s[j] {
				case 'n':
					b.WriteByte('\n')
				default:
					b.WriteByte(s[j])
				}
				continue
			}
			if c == '"' {
				s = s[j+1:]
				closed = true
				break
			}
			b.WriteByte(c)
		}
		if !closed {
			return nil, "", fmt.Errorf("unterminated label value in %q", line)
		}
		labels[name] = b.String()
	}
}
