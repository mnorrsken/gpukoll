// Package server polls the cluster and serves the web UI and its JSON API.
package server

import (
	"context"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"sync"
	"time"

	"github.com/mnorrsken/gpukoll/internal/dcgm"
	"github.com/mnorrsken/gpukoll/internal/gpu"
	"github.com/mnorrsken/gpukoll/internal/kube"
	"github.com/mnorrsken/gpukoll/internal/version"
)

//go:embed web
var webFS embed.FS

// Source reads from the Kubernetes API.
type Source interface {
	Nodes(ctx context.Context) ([]kube.Node, error)
	EndpointSlices(ctx context.Context, namespace, service string) ([]kube.EndpointSlice, error)
	Raw(ctx context.Context, path string) (io.ReadCloser, error)
}

// Config controls polling.
type Config struct {
	Interval time.Duration
	// Namespace and Service of the NVIDIA DCGM exporter.
	DCGMNamespace string
	DCGMService   string
	// DCGMProxy scrapes the exporters through the API server's pod proxy
	// instead of connecting to pod IPs, for running outside the cluster.
	DCGMProxy bool
}

// Server keeps the latest snapshot in memory. Viewers read the cached copy,
// so the API server load does not grow with the number of open browsers.
type Server struct {
	src    Source
	cfg    Config
	scrape *http.Client

	mu   sync.RWMutex
	snap *gpu.Snapshot
	err  error
}

// New returns a server that polls src every cfg.Interval.
func New(src Source, cfg Config) *Server {
	return &Server{
		src: src,
		cfg: cfg,
		// No proxy from the environment: exporters are reached on pod IPs.
		scrape: &http.Client{Timeout: 5 * time.Second, Transport: &http.Transport{}},
	}
}

// Run polls until ctx is done.
func (s *Server) Run(ctx context.Context) {
	t := time.NewTicker(s.cfg.Interval)
	defer t.Stop()
	for {
		s.poll(ctx)
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

func (s *Server) poll(ctx context.Context) {
	ctx, cancel := context.WithTimeout(ctx, s.cfg.Interval+20*time.Second)
	defer cancel()
	snap, err := s.collect(ctx)
	s.mu.Lock()
	defer s.mu.Unlock()
	if err != nil {
		if s.err == nil {
			slog.Error("polling cluster", "err", err)
		}
		s.err = err
		return
	}
	if s.err != nil {
		slog.Info("polling cluster recovered")
	}
	s.snap, s.err = &snap, nil
}

func (s *Server) collect(ctx context.Context) (gpu.Snapshot, error) {
	nodes, err := s.src.Nodes(ctx)
	if err != nil {
		return gpu.Snapshot{}, fmt.Errorf("listing nodes: %w", err)
	}
	return gpu.Build(nodes, s.usage(ctx, nodes), time.Now().UTC()), nil
}

// exporter is where one node's DCGM exporter can be scraped.
type exporter struct {
	addr string // pod IP
	pod  string // pod name, for the API server proxy
	port int
}

// usage scrapes the DCGM exporter of every node in parallel. Failures are
// reported per node, so one broken exporter only greys out its own server.
func (s *Server) usage(ctx context.Context, nodes []kube.Node) map[string]gpu.Usage {
	out := map[string]gpu.Usage{}
	slices, err := s.src.EndpointSlices(ctx, s.cfg.DCGMNamespace, s.cfg.DCGMService)
	if err != nil {
		err = fmt.Errorf("listing DCGM exporter endpoints in %s: %w", s.cfg.DCGMNamespace, err)
		for _, n := range nodes {
			out[n.Metadata.Name] = gpu.Usage{Err: err}
		}
		return out
	}

	exporters := map[string]exporter{}
	for _, sl := range slices {
		if len(sl.Ports) == 0 {
			continue
		}
		port := sl.Ports[0].Port
		for _, p := range sl.Ports {
			if p.Name == "gpu-metrics" {
				port = p.Port
			}
		}
		for _, ep := range sl.Endpoints {
			ready := ep.Conditions.Ready == nil || *ep.Conditions.Ready
			if !ready || ep.NodeName == "" || len(ep.Addresses) == 0 {
				continue
			}
			e := exporter{addr: ep.Addresses[0], port: port}
			if ep.TargetRef != nil && ep.TargetRef.Kind == "Pod" {
				e.pod = ep.TargetRef.Name
			}
			exporters[ep.NodeName] = e
		}
	}

	var (
		mu sync.Mutex
		wg sync.WaitGroup
	)
	for nodeName, e := range exporters {
		wg.Go(func() {
			devs, err := s.scrapeOne(ctx, e)
			mu.Lock()
			out[nodeName] = gpu.Usage{Devices: devs, Err: err}
			mu.Unlock()
		})
	}
	wg.Wait()
	return out
}

func (s *Server) scrapeOne(ctx context.Context, e exporter) ([]dcgm.Device, error) {
	if !s.cfg.DCGMProxy {
		return dcgm.Scrape(ctx, s.scrape, "http://"+net.JoinHostPort(e.addr, strconv.Itoa(e.port))+"/metrics")
	}
	if e.pod == "" {
		return nil, errors.New("exporter endpoint has no pod reference")
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	path := fmt.Sprintf("/api/v1/namespaces/%s/pods/%s:%d/proxy/metrics",
		url.PathEscape(s.cfg.DCGMNamespace), url.PathEscape(e.pod), e.port)
	body, err := s.src.Raw(ctx, path)
	if err != nil {
		return nil, err
	}
	defer body.Close()
	return dcgm.Parse(io.LimitReader(body, 16<<20))
}

// Handler returns the HTTP handler. There is no login: the data is
// read-only and every visitor is anonymous.
func (s *Server) Handler() http.Handler {
	static, err := fs.Sub(webFS, "web")
	if err != nil {
		panic(err)
	}
	mux := http.NewServeMux()
	mux.Handle("GET /", http.FileServerFS(static))
	mux.HandleFunc("GET /api/gpus", s.handleGPUs)
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.Write([]byte("ok\n"))
	})
	return securityHeaders(mux)
}

type apiResponse struct {
	*gpu.Snapshot
	RefreshSeconds int    `json:"refreshSeconds"`
	Version        string `json:"version"`
	Error          string `json:"error,omitempty"`
}

func (s *Server) handleGPUs(w http.ResponseWriter, _ *http.Request) {
	s.mu.RLock()
	resp := apiResponse{
		Snapshot:       s.snap,
		RefreshSeconds: max(1, int(s.cfg.Interval.Seconds())),
		Version:        version.Version,
	}
	if s.err != nil {
		resp.Error = s.err.Error()
	}
	s.mu.RUnlock()

	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	if resp.Snapshot == nil {
		if resp.Error == "" {
			resp.Error = "waiting for first poll"
		}
		w.WriteHeader(http.StatusServiceUnavailable)
	}
	json.NewEncoder(w).Encode(resp)
}

func securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("Content-Security-Policy", "default-src 'self'; style-src 'self' 'unsafe-inline'; img-src 'self' data:; frame-ancestors 'none'")
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("Referrer-Policy", "no-referrer")
		next.ServeHTTP(w, r)
	})
}
