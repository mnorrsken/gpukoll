// Command gpukoll serves a web page showing GPU servers and GPU usage in a
// Kubernetes cluster that runs the NVIDIA GPU Operator.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/mnorrsken/gpukoll/internal/kube"
	"github.com/mnorrsken/gpukoll/internal/server"
	"github.com/mnorrsken/gpukoll/internal/version"
)

func main() {
	listen := flag.String("listen", ":8080", "HTTP listen address")
	api := flag.String("api", "", "Kubernetes API URL without auth, e.g. http://127.0.0.1:8001 from `kubectl proxy`; empty uses the in-cluster service account")
	interval := flag.Duration("interval", 10*time.Second, "how often to poll the cluster")
	showVersion := flag.Bool("version", false, "print version and exit")
	flag.Parse()

	if *showVersion {
		fmt.Printf("gpukoll %s (%s)\n", version.Version, version.Commit)
		return
	}
	if err := run(*listen, *api, *interval); err != nil {
		slog.Error("gpukoll failed", "err", err)
		os.Exit(1)
	}
}

func run(listen, api string, interval time.Duration) error {
	if interval <= 0 {
		return errors.New("-interval must be positive")
	}
	var client *kube.Client
	if api != "" {
		client = kube.New(api)
	} else {
		var err error
		if client, err = kube.InCluster(); err != nil {
			return err
		}
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	srv := server.New(client, interval)
	go srv.Run(ctx)

	hs := &http.Server{
		Addr:              listen,
		Handler:           srv.Handler(),
		ReadHeaderTimeout: 10 * time.Second,
	}
	go func() {
		<-ctx.Done()
		shutdown, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		hs.Shutdown(shutdown)
	}()

	slog.Info("gpukoll starting", "version", version.Version, "listen", listen, "interval", interval)
	if err := hs.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return fmt.Errorf("serving http: %w", err)
	}
	return nil
}
