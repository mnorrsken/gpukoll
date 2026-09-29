// Package server polls the cluster and serves the web UI and its JSON API.
package server

import (
	"context"
	"embed"
	"encoding/json"
	"fmt"
	"io/fs"
	"log/slog"
	"net/http"
	"sync"
	"time"

	"github.com/mnorrsken/gpukoll/internal/gpu"
	"github.com/mnorrsken/gpukoll/internal/kube"
	"github.com/mnorrsken/gpukoll/internal/version"
)

//go:embed web
var webFS embed.FS

// Source lists nodes and active pods.
type Source interface {
	Nodes(ctx context.Context) ([]kube.Node, error)
	ActivePods(ctx context.Context) ([]kube.Pod, error)
}

// Server keeps the latest snapshot in memory. Viewers read the cached copy,
// so the API server load does not grow with the number of open browsers.
type Server struct {
	src      Source
	interval time.Duration

	mu   sync.RWMutex
	snap *gpu.Snapshot
	err  error
}

// New returns a server that polls src every interval.
func New(src Source, interval time.Duration) *Server {
	return &Server{src: src, interval: interval}
}

// Run polls until ctx is done.
func (s *Server) Run(ctx context.Context) {
	t := time.NewTicker(s.interval)
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
	ctx, cancel := context.WithTimeout(ctx, s.interval+20*time.Second)
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
	pods, err := s.src.ActivePods(ctx)
	if err != nil {
		return gpu.Snapshot{}, fmt.Errorf("listing pods: %w", err)
	}
	return gpu.Build(nodes, pods, time.Now().UTC()), nil
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
		RefreshSeconds: max(1, int(s.interval.Seconds())),
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
