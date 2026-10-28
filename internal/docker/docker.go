// Package docker wraps the Docker Engine API. Marineris only ever talks to
// Docker through the socket proxy, which allow-lists the endpoints below; the
// raw Docker socket is never mounted into the Marineris container.
package docker

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/moby/moby/api/pkg/stdcopy"
	"github.com/moby/moby/api/types/container"
	"github.com/moby/moby/api/types/mount"
	"github.com/moby/moby/api/types/network"
	"github.com/moby/moby/client"

	"github.com/photonring/marineris-server/internal/validate"
)

// Labels used by Marineris.
const (
	// SystemLabel marks the containers of the Marineris stack itself. They
	// can be restarted but never stopped or removed from the UI, so the
	// admin cannot lock themselves out.
	SystemLabel  = "io.marineris.system"
	ProjectLabel = "com.docker.compose.project"
	ServiceLabel = "com.docker.compose.service"
)

// ErrProtected is returned for forbidden actions on system containers.
var ErrProtected = errors.New("docker: this is a Marineris system container; only restart is allowed")

// Client is a Docker API client.
type Client struct {
	api         *client.Client
	appsNetwork string
}

// New returns a client for the given Docker host (e.g. tcp://socket-proxy:2375).
func New(host, appsNetwork string) (*Client, error) {
	api, err := client.New(client.WithHost(host), client.WithTimeout(5*time.Minute))
	if err != nil {
		return nil, err
	}
	return &Client{api: api, appsNetwork: appsNetwork}, nil
}

// API exposes the underlying client.
func (c *Client) API() *client.Client { return c.api }

// Ping checks connectivity to the Docker API.
func (c *Client) Ping(ctx context.Context) error {
	_, err := c.api.Ping(ctx, client.PingOptions{})
	return err
}

// Container is a summary of a container for the UI.
type Container struct {
	ID       string   `json:"id"`
	Name     string   `json:"name"`
	Image    string   `json:"image"`
	State    string   `json:"state"`
	Status   string   `json:"status"`
	Health   string   `json:"health,omitempty"`
	Created  int64    `json:"created"`
	Ports    []int    `json:"ports"`
	Stack    string   `json:"stack,omitempty"`
	Service  string   `json:"service,omitempty"` // compose service
	Networks []string `json:"networks"`
	System   bool     `json:"system"`
	// Routable is true when Caddy can reach the container (it is attached
	// to the apps network).
	Routable bool `json:"routable"`
}

// List returns every container, running or not.
func (c *Client) List(ctx context.Context) ([]Container, error) {
	res, err := c.api.ContainerList(ctx, client.ContainerListOptions{All: true})
	if err != nil {
		return nil, err
	}
	out := make([]Container, 0, len(res.Items))
	for _, s := range res.Items {
		ct := Container{
			ID:      s.ID,
			Image:   s.Image,
			State:   string(s.State),
			Status:  s.Status,
			Created: s.Created,
			Stack:   s.Labels[ProjectLabel],
			Service: s.Labels[ServiceLabel],
			System:  s.Labels[SystemLabel] == "true",
		}
		if len(s.Names) > 0 {
			ct.Name = strings.TrimPrefix(s.Names[0], "/")
		}
		if s.Health != nil {
			ct.Health = string(s.Health.Status)
		}
		ports := map[int]bool{}
		for _, p := range s.Ports {
			if p.Type == "tcp" || p.Type == "" {
				ports[int(p.PrivatePort)] = true
			}
		}
		for p := range ports {
			ct.Ports = append(ct.Ports, p)
		}
		sort.Ints(ct.Ports)
		if s.NetworkSettings != nil {
			for n := range s.NetworkSettings.Networks {
				ct.Networks = append(ct.Networks, n)
				if n == c.appsNetwork {
					ct.Routable = true
				}
			}
			sort.Strings(ct.Networks)
		}
		if ct.Networks == nil {
			ct.Networks = []string{}
		}
		if ct.Ports == nil {
			ct.Ports = []int{}
		}
		out = append(out, ct)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

// Detail is the inspected state of a container. Environment variable values
// are redacted: they often contain credentials.
type Detail struct {
	Container
	Command       []string          `json:"command"`
	Env           []string          `json:"env"`
	Labels        map[string]string `json:"labels"`
	Mounts        []string          `json:"mounts"`
	RestartPolicy string            `json:"restart_policy"`
	ExposedPorts  []int             `json:"exposed_ports"`
	StartedAt     string            `json:"started_at"`
	ExitCode      int               `json:"exit_code"`
}

func checkRef(ref string) error {
	if err := validate.ContainerName(ref); err != nil {
		return fmt.Errorf("docker: invalid container reference: %w", err)
	}
	return nil
}

// Inspect returns details of a container.
func (c *Client) Inspect(ctx context.Context, ref string) (*Detail, error) {
	if err := checkRef(ref); err != nil {
		return nil, err
	}
	res, err := c.api.ContainerInspect(ctx, ref, client.ContainerInspectOptions{})
	if err != nil {
		return nil, err
	}
	in := res.Container
	d := &Detail{
		Container: Container{
			ID:       in.ID,
			Name:     strings.TrimPrefix(in.Name, "/"),
			Networks: []string{},
			Ports:    []int{},
		},
		Command:      []string{},
		Env:          []string{},
		Mounts:       []string{},
		ExposedPorts: []int{},
	}
	if in.Config != nil {
		d.Image = in.Config.Image
		d.Labels = in.Config.Labels
		d.Stack = in.Config.Labels[ProjectLabel]
		d.Service = in.Config.Labels[ServiceLabel]
		d.System = in.Config.Labels[SystemLabel] == "true"
		d.Command = append(append(d.Command, in.Config.Entrypoint...), in.Config.Cmd...)
		for _, e := range in.Config.Env {
			k, _, _ := strings.Cut(e, "=")
			d.Env = append(d.Env, k+"=••••")
		}
		for p := range in.Config.ExposedPorts {
			if p.Proto() == network.TCP {
				d.ExposedPorts = append(d.ExposedPorts, int(p.Num()))
			}
		}
		sort.Ints(d.ExposedPorts)
		d.Ports = d.ExposedPorts
	}
	if in.State != nil {
		d.State = string(in.State.Status)
		d.StartedAt = in.State.StartedAt
		d.ExitCode = in.State.ExitCode
		if in.State.Health != nil {
			d.Health = string(in.State.Health.Status)
		}
	}
	if in.HostConfig != nil {
		d.RestartPolicy = string(in.HostConfig.RestartPolicy.Name)
	}
	for _, m := range in.Mounts {
		src := m.Source
		if m.Type == mount.TypeVolume {
			src = m.Name
		}
		d.Mounts = append(d.Mounts, fmt.Sprintf("%s:%s (%s)", src, m.Destination, m.Type))
	}
	if in.NetworkSettings != nil {
		for n := range in.NetworkSettings.Networks {
			d.Networks = append(d.Networks, n)
			if n == c.appsNetwork {
				d.Routable = true
			}
		}
		sort.Strings(d.Networks)
	}
	return d, nil
}

func (c *Client) system(ctx context.Context, ref string) (bool, error) {
	res, err := c.api.ContainerInspect(ctx, ref, client.ContainerInspectOptions{})
	if err != nil {
		return false, err
	}
	return res.Container.Config != nil && res.Container.Config.Labels[SystemLabel] == "true", nil
}

// Action performs start, stop or restart on a container.
func (c *Client) Action(ctx context.Context, ref, action string) error {
	if err := checkRef(ref); err != nil {
		return err
	}
	switch action {
	case "start", "stop":
		sys, err := c.system(ctx, ref)
		if err != nil {
			return err
		}
		if sys && action == "stop" {
			return ErrProtected
		}
		if action == "start" {
			_, err = c.api.ContainerStart(ctx, ref, client.ContainerStartOptions{})
		} else {
			_, err = c.api.ContainerStop(ctx, ref, client.ContainerStopOptions{})
		}
		return err
	case "restart":
		_, err := c.api.ContainerRestart(ctx, ref, client.ContainerRestartOptions{})
		return err
	}
	return fmt.Errorf("docker: unknown action %q", action)
}

// Remove deletes a stopped, non-system container.
func (c *Client) Remove(ctx context.Context, ref string) error {
	if err := checkRef(ref); err != nil {
		return err
	}
	sys, err := c.system(ctx, ref)
	if err != nil {
		return err
	}
	if sys {
		return ErrProtected
	}
	_, err = c.api.ContainerRemove(ctx, ref, client.ContainerRemoveOptions{})
	return err
}

// MaxLogBytes caps the amount of log data returned in one call.
const MaxLogBytes = 2 << 20

// Logs returns the last tail lines of a container's output.
func (c *Client) Logs(ctx context.Context, ref string, tail int) (string, error) {
	if err := checkRef(ref); err != nil {
		return "", err
	}
	if tail <= 0 || tail > 5000 {
		tail = 500
	}
	rc, err := c.api.ContainerLogs(ctx, ref, client.ContainerLogsOptions{
		ShowStdout: true, ShowStderr: true, Timestamps: true, Tail: strconv.Itoa(tail),
	})
	if err != nil {
		return "", err
	}
	defer rc.Close()
	raw, err := io.ReadAll(io.LimitReader(rc, MaxLogBytes))
	if err != nil {
		return "", err
	}
	// Containers without a TTY multiplex stdout/stderr; demux when possible.
	var out bytes.Buffer
	if _, err := stdcopy.StdCopy(&out, &out, bytes.NewReader(raw)); err != nil {
		return string(raw), nil
	}
	return out.String(), nil
}

// ConnectToApps attaches a container to the apps network so Caddy can reach
// it. It is a no-op when already attached.
func (c *Client) ConnectToApps(ctx context.Context, ref string) error {
	if err := checkRef(ref); err != nil {
		return err
	}
	res, err := c.api.ContainerInspect(ctx, ref, client.ContainerInspectOptions{})
	if err != nil {
		return err
	}
	if res.Container.NetworkSettings != nil {
		if _, ok := res.Container.NetworkSettings.Networks[c.appsNetwork]; ok {
			return nil
		}
	}
	_, err = c.api.NetworkConnect(ctx, c.appsNetwork, client.NetworkConnectOptions{Container: ref})
	return err
}

// Network is a summary of a Docker network.
type Network struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Driver   string `json:"driver"`
	Scope    string `json:"scope"`
	Internal bool   `json:"internal"`
	Stack    string `json:"stack,omitempty"`
}

// Networks lists networks.
func (c *Client) Networks(ctx context.Context) ([]Network, error) {
	res, err := c.api.NetworkList(ctx, client.NetworkListOptions{})
	if err != nil {
		return nil, err
	}
	out := []Network{}
	for _, n := range res.Items {
		out = append(out, Network{ID: n.ID, Name: n.Name, Driver: n.Driver, Scope: n.Scope, Internal: n.Internal, Stack: n.Labels[ProjectLabel]})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

// Volume is a summary of a Docker volume.
type Volume struct {
	Name       string `json:"name"`
	Driver     string `json:"driver"`
	Mountpoint string `json:"mountpoint"`
	CreatedAt  string `json:"created_at"`
	Stack      string `json:"stack,omitempty"`
	System     bool   `json:"system"`
}

// Volumes lists volumes.
func (c *Client) Volumes(ctx context.Context) ([]Volume, error) {
	res, err := c.api.VolumeList(ctx, client.VolumeListOptions{})
	if err != nil {
		return nil, err
	}
	out := []Volume{}
	for _, v := range res.Items {
		out = append(out, Volume{Name: v.Name, Driver: v.Driver, Mountpoint: v.Mountpoint, CreatedAt: v.CreatedAt,
			Stack: v.Labels[ProjectLabel], System: v.Labels[SystemLabel] == "true"})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

// RemoveVolume deletes an unused, non-system volume.
func (c *Client) RemoveVolume(ctx context.Context, name string) error {
	if err := checkRef(name); err != nil {
		return err
	}
	vols, err := c.Volumes(ctx)
	if err != nil {
		return err
	}
	for _, v := range vols {
		if v.Name == name && v.System {
			return ErrProtected
		}
	}
	_, err = c.api.VolumeRemove(ctx, name, client.VolumeRemoveOptions{})
	return err
}

// RunSpec describes a short-lived helper container.
type RunSpec struct {
	Image string
	// User runs the container as uid:gid (never empty: helpers must not
	// run as root).
	User string
	Cmd  []string
	Env  []string
	// Binds are host:container[:ro] bind mounts.
	Binds   []string
	Timeout time.Duration
}

// Run creates a container from spec, waits for it to exit and returns its
// exit code and output. The container has no network, no capabilities and a
// read-only root filesystem, and is always removed.
func (c *Client) Run(ctx context.Context, spec RunSpec) (int64, string, error) {
	if spec.Timeout == 0 {
		spec.Timeout = 60 * time.Second
	}
	if spec.User == "" || strings.HasPrefix(spec.User, "0:") || spec.User == "0" {
		return 0, "", errors.New("docker: helper containers must run as a non-root user")
	}
	ctx, cancel := context.WithTimeout(ctx, spec.Timeout)
	defer cancel()
	created, err := c.api.ContainerCreate(ctx, client.ContainerCreateOptions{
		Config: &container.Config{
			Image:  spec.Image,
			User:   spec.User,
			Cmd:    spec.Cmd,
			Env:    spec.Env,
			Labels: map[string]string{"io.marineris.helper": "true"},
		},
		HostConfig: &container.HostConfig{
			Binds:          spec.Binds,
			NetworkMode:    "none",
			CapDrop:        []string{"ALL"},
			ReadonlyRootfs: true,
			SecurityOpt:    []string{"no-new-privileges:true"},
		},
	})
	if err != nil {
		return 0, "", err
	}
	defer func() {
		rctx, rcancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer rcancel()
		_, _ = c.api.ContainerRemove(rctx, created.ID, client.ContainerRemoveOptions{Force: true})
	}()
	wait := c.api.ContainerWait(ctx, created.ID, client.ContainerWaitOptions{Condition: container.WaitConditionNextExit})
	if _, err := c.api.ContainerStart(ctx, created.ID, client.ContainerStartOptions{}); err != nil {
		return 0, "", err
	}
	var code int64
	select {
	case res := <-wait.Result:
		code = res.StatusCode
	case err := <-wait.Error:
		return 0, "", err
	case <-ctx.Done():
		return 0, "", ctx.Err()
	}
	logs, _ := c.Logs(context.Background(), created.ID, 200)
	return code, logs, nil
}

// Hostname returns the host name reported by the Docker daemon.
func (c *Client) Hostname(ctx context.Context) string {
	res, err := c.api.Info(ctx, client.InfoOptions{})
	if err != nil {
		return ""
	}
	return res.Info.Name
}
