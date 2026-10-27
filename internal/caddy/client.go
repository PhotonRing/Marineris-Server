package caddy

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"time"
)

// Client talks to Caddy's admin API.
type Client struct {
	http  *http.Client
	base  string
	probe string
}

// NewClient returns a client for the admin endpoint, given either as
// unix:///path/to/socket or http://host:port. probeAddr (host:port) is the
// plain-HTTP health listener rendered into the config; it may be empty.
func NewClient(admin, probeAddr string) (*Client, error) {
	tr := &http.Transport{
		MaxIdleConns:          4,
		IdleConnTimeout:       30 * time.Second,
		ResponseHeaderTimeout: 60 * time.Second,
		Proxy:                 nil, // never send admin traffic through a proxy
	}
	base := admin
	switch {
	case strings.HasPrefix(admin, "unix://"):
		sock := strings.TrimPrefix(admin, "unix://")
		tr.DialContext = func(ctx context.Context, _, _ string) (net.Conn, error) {
			var d net.Dialer
			return d.DialContext(ctx, "unix", sock)
		}
		base = "http://caddy-admin"
	case strings.HasPrefix(admin, "http://"):
	default:
		return nil, fmt.Errorf("caddy: unsupported admin address %q", admin)
	}
	c := &Client{http: &http.Client{Transport: tr, Timeout: 90 * time.Second}, base: strings.TrimSuffix(base, "/")}
	if probeAddr != "" {
		c.probe = "http://" + probeAddr + "/"
	}
	return c, nil
}

// Load replaces Caddy's running configuration. Caddy applies a new config
// atomically: if it fails to load, the previous config keeps running and
// an error is returned.
func (c *Client) Load(ctx context.Context, cfg []byte) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.base+"/load", bytes.NewReader(cfg))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	// Caddy refuses to reload an identical config unless asked; we always
	// want the load (and its validation) to happen.
	req.Header.Set("Cache-Control", "must-revalidate")
	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("caddy: admin API unreachable: %w", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
	if resp.StatusCode/100 != 2 {
		return &LoadError{Status: resp.StatusCode, Message: strings.TrimSpace(string(body))}
	}
	return nil
}

// LoadError is returned when Caddy rejects a configuration.
type LoadError struct {
	Status  int
	Message string
}

func (e *LoadError) Error() string {
	return fmt.Sprintf("caddy rejected the configuration (HTTP %d): %s", e.Status, e.Message)
}

// Config returns Caddy's running configuration.
func (c *Client) Config(ctx context.Context) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.base+"/config/", nil)
	if err != nil {
		return nil, err
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("caddy: admin API unreachable: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("caddy: GET /config/ returned %d", resp.StatusCode)
	}
	return io.ReadAll(io.LimitReader(resp.Body, 16<<20))
}

// Healthy checks that the admin API answers and, when configured, that the
// HTTP app serves the probe listener (proving the new config is running).
func (c *Client) Healthy(ctx context.Context) error {
	if _, err := c.Config(ctx); err != nil {
		return err
	}
	if c.probe == "" {
		return nil
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.probe, nil)
	if err != nil {
		return err
	}
	probe := &http.Client{Timeout: 5 * time.Second, Transport: &http.Transport{Proxy: nil}}
	resp, err := probe.Do(req)
	if err != nil {
		return fmt.Errorf("caddy: probe listener unreachable: %w", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 64))
	if resp.StatusCode != http.StatusOK || string(body) != "ok" {
		return errors.New("caddy: probe listener returned an unexpected response")
	}
	return nil
}
