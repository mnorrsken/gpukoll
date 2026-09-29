// Package kube is a minimal read-only client for the Kubernetes API.
// It only lists nodes and endpoint slices, so it avoids pulling in client-go.
package kube

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"
)

const saDir = "/var/run/secrets/kubernetes.io/serviceaccount"

// Client talks to the Kubernetes API server.
type Client struct {
	base      string
	tokenFile string
	http      *http.Client
}

// InCluster returns a client that uses the pod's service account.
func InCluster() (*Client, error) {
	host, port := os.Getenv("KUBERNETES_SERVICE_HOST"), os.Getenv("KUBERNETES_SERVICE_PORT")
	if host == "" || port == "" {
		return nil, errors.New("not running in a cluster: KUBERNETES_SERVICE_HOST is not set (use -api with kubectl proxy)")
	}
	ca, err := os.ReadFile(saDir + "/ca.crt")
	if err != nil {
		return nil, fmt.Errorf("reading service account CA: %w", err)
	}
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(ca) {
		return nil, errors.New("no certificates in service account CA")
	}
	return &Client{
		base:      "https://" + net.JoinHostPort(host, port),
		tokenFile: saDir + "/token",
		http: &http.Client{
			Timeout: 30 * time.Second,
			Transport: &http.Transport{
				TLSClientConfig: &tls.Config{RootCAs: pool, MinVersion: tls.VersionTLS12},
			},
		},
	}, nil
}

// New returns a client for an API server that needs no credentials,
// such as `kubectl proxy`.
func New(base string) *Client {
	return &Client{
		base: strings.TrimRight(base, "/"),
		http: &http.Client{Timeout: 30 * time.Second},
	}
}

// Nodes lists all nodes.
func (c *Client) Nodes(ctx context.Context) ([]Node, error) {
	var l struct {
		Items []Node `json:"items"`
	}
	// resourceVersion=0 lets the API server answer from its watch cache.
	if err := c.get(ctx, "/api/v1/nodes?resourceVersion=0", &l); err != nil {
		return nil, err
	}
	return l.Items, nil
}

// EndpointSlices lists the EndpointSlices of a Service.
func (c *Client) EndpointSlices(ctx context.Context, namespace, service string) ([]EndpointSlice, error) {
	var l struct {
		Items []EndpointSlice `json:"items"`
	}
	q := url.Values{"labelSelector": {"kubernetes.io/service-name=" + service}}
	path := "/apis/discovery.k8s.io/v1/namespaces/" + url.PathEscape(namespace) + "/endpointslices?" + q.Encode()
	if err := c.get(ctx, path, &l); err != nil {
		return nil, err
	}
	return l.Items, nil
}

// Raw returns the body of a GET request to path, such as a pod proxy URL.
// The caller closes it.
func (c *Client) Raw(ctx context.Context, path string) (io.ReadCloser, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.base+path, nil)
	if err != nil {
		return nil, err
	}
	if c.tokenFile != "" {
		// Re-read on every call: projected tokens are rotated by the kubelet.
		tok, err := os.ReadFile(c.tokenFile)
		if err != nil {
			return nil, fmt.Errorf("reading service account token: %w", err)
		}
		req.Header.Set("Authorization", "Bearer "+strings.TrimSpace(string(tok)))
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("get %s: %w", path, err)
	}
	if resp.StatusCode != http.StatusOK {
		defer resp.Body.Close()
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return nil, fmt.Errorf("get %s: %s: %s", path, resp.Status, strings.TrimSpace(string(body)))
	}
	return resp.Body, nil
}

func (c *Client) get(ctx context.Context, path string, out any) error {
	body, err := c.Raw(ctx, path)
	if err != nil {
		return err
	}
	defer body.Close()
	if err := json.NewDecoder(body).Decode(out); err != nil {
		return fmt.Errorf("decoding %s: %w", path, err)
	}
	return nil
}
