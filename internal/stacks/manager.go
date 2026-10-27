package stacks

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/compose-spec/compose-go/v2/loader"
	"github.com/compose-spec/compose-go/v2/types"
	"github.com/docker/cli/cli/command"
	"github.com/docker/cli/cli/flags"
	"github.com/docker/compose/v5/pkg/api"
	"github.com/docker/compose/v5/pkg/compose"

	"github.com/photonring/marineris-server/internal/secrets"
	"github.com/photonring/marineris-server/internal/validate"
)

// MaxComposeSize bounds compose and .env documents.
const MaxComposeSize = 256 << 10

// PolicyError reports blocking policy violations.
type PolicyError struct{ Violations []Violation }

func (e *PolicyError) Error() string {
	msgs := make([]string, 0, len(e.Violations))
	for _, v := range e.Violations {
		if v.Service != "" {
			msgs = append(msgs, v.Service+": "+v.Message)
		} else {
			msgs = append(msgs, v.Message)
		}
	}
	return "stack refused by policy: " + strings.Join(msgs, "; ")
}

// Manager loads, checks and deploys stacks.
type Manager struct {
	root          string
	dockerHost    string
	bindRoots     []string
	reservedNames []string

	mu      sync.Mutex
	service api.Compose
}

// NewManager returns a manager storing stacks under root (which must have
// the same absolute path on the host and in the Marineris container).
func NewManager(root, dockerHost string, bindRoots, reservedNames []string) *Manager {
	return &Manager{root: root, dockerHost: dockerHost, bindRoots: bindRoots, reservedNames: reservedNames}
}

// Dir returns the directory of a stack.
func (m *Manager) Dir(name string) string { return filepath.Join(m.root, name) }

func (m *Manager) policy(name string) Policy {
	return Policy{StackDir: m.Dir(name), AllowedBindRoots: m.bindRoots, ReservedNames: m.reservedNames}
}

// Load parses a compose document for stack name without touching Docker.
// Interpolation only sees the stack's own .env content: never the
// environment of the Marineris process.
func (m *Manager) Load(ctx context.Context, name string, composeYAML, env []byte) (*types.Project, []Violation, error) {
	if err := validate.StackName(name); err != nil {
		return nil, nil, fmt.Errorf("stack name %w", err)
	}
	if len(composeYAML) > MaxComposeSize || len(env) > MaxComposeSize {
		return nil, nil, errors.New("compose file or .env too large")
	}
	if vs := PreScan(composeYAML); len(vs) > 0 {
		return nil, vs, nil
	}
	envMap, err := parseEnv(env)
	if err != nil {
		return nil, nil, err
	}
	dir := m.Dir(name)
	cd := types.ConfigDetails{
		WorkingDir:  dir,
		ConfigFiles: []types.ConfigFile{{Filename: filepath.Join(dir, "compose.yaml"), Content: composeYAML}},
		Environment: envMap,
	}
	project, lerr := loadFromDetails(ctx, cd, name)
	if lerr != nil {
		// An invalid document is a finding for the user, not a server error.
		return nil, []Violation{{Rule: "syntax", Message: lerr.Error()}}, nil //nolint:nilerr // reported as a violation
	}
	for svcName, s := range project.Services {
		s.CustomLabels = map[string]string{
			api.ProjectLabel:     project.Name,
			api.ServiceLabel:     svcName,
			api.VersionLabel:     api.ComposeVersion,
			api.WorkingDirLabel:  project.WorkingDir,
			api.ConfigFilesLabel: filepath.Join(dir, "compose.yaml"),
			api.OneoffLabel:      "False",
		}
		project.Services[svcName] = s
	}
	return project, Check(project, m.policy(name)), nil
}

// Write stores the compose file and .env of a stack on disk (the .env with
// mode 0600), so that relative bind mounts and env files resolve.
func (m *Manager) Write(name string, composeYAML, env []byte) error {
	if err := validate.StackName(name); err != nil {
		return err
	}
	dir := m.Dir(name)
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return err
	}
	if err := secrets.WriteFileAtomic(filepath.Join(dir, "compose.yaml"), composeYAML, 0o640, false); err != nil {
		return err
	}
	return secrets.WriteFileAtomic(filepath.Join(dir, ".env"), env, 0o600, false)
}

func (m *Manager) compose() (api.Compose, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.service != nil {
		return m.service, nil
	}
	// The docker CLI library reads its config from DOCKER_CONFIG; point it
	// at an empty private directory so no host credentials are involved.
	cfgDir, err := os.MkdirTemp("", "marineris-docker-")
	if err != nil {
		return nil, err
	}
	_ = os.Setenv("DOCKER_CONFIG", cfgDir)
	dockerCli, err := command.NewDockerCli(command.WithCombinedStreams(&bytes.Buffer{}))
	if err != nil {
		return nil, err
	}
	if err := dockerCli.Initialize(&flags.ClientOptions{Hosts: []string{m.dockerHost}}); err != nil {
		return nil, err
	}
	svc, err := compose.NewComposeService(dockerCli)
	if err != nil {
		return nil, err
	}
	m.service = svc
	return svc, nil
}

// Deploy loads, checks and starts (or updates) a stack. unsafeAck accepts
// overridable violations.
func (m *Manager) Deploy(ctx context.Context, name string, composeYAML, env []byte, unsafeAck bool) error {
	project, vs, err := m.Load(ctx, name, composeYAML, env)
	if err != nil {
		return err
	}
	if blocking := Blocking(vs, unsafeAck); len(blocking) > 0 {
		return &PolicyError{Violations: blocking}
	}
	if err := m.Write(name, composeYAML, env); err != nil {
		return err
	}
	svc, err := m.compose()
	if err != nil {
		return err
	}
	return svc.Up(ctx, project, api.UpOptions{
		Create: api.CreateOptions{
			RemoveOrphans:        true,
			Recreate:             api.RecreateDiverged,
			RecreateDependencies: api.RecreateDiverged,
			QuietPull:            true,
		},
		Start: api.StartOptions{Project: project},
	})
}

// Down stops and removes a stack's containers and networks. Volumes are kept
// unless removeVolumes is set.
func (m *Manager) Down(ctx context.Context, name string, removeVolumes bool) error {
	if err := validate.StackName(name); err != nil {
		return err
	}
	svc, err := m.compose()
	if err != nil {
		return err
	}
	return svc.Down(ctx, name, api.DownOptions{RemoveOrphans: true, Volumes: removeVolumes})
}

// Remove deletes the stack directory from disk.
func (m *Manager) Remove(name string) error {
	if err := validate.StackName(name); err != nil {
		return err
	}
	return os.RemoveAll(m.Dir(name))
}

// loadFromDetails loads a project from memory. Remote resources and
// include/extends files are disabled (PreScan also refuses them).
func loadFromDetails(ctx context.Context, cd types.ConfigDetails, name string) (*types.Project, error) {
	return loader.LoadWithContext(ctx, cd, func(o *loader.Options) {
		o.SetProjectName(name, true)
		o.ResolvePaths = true
		o.SkipInclude = true
		o.ResourceLoaders = nil
		o.Profiles = []string{"*"}
	})
}

func parseEnv(env []byte) (map[string]string, error) {
	out := map[string]string{}
	for i, line := range strings.Split(string(env), "\n") {
		line = strings.TrimSpace(strings.TrimSuffix(line, "\r"))
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		k, v, ok := strings.Cut(line, "=")
		k = strings.TrimSpace(strings.TrimPrefix(k, "export "))
		if !ok || k == "" || strings.ContainsAny(k, " \t\"'$") {
			return nil, fmt.Errorf(".env line %d: expected KEY=value", i+1)
		}
		v = strings.TrimSpace(v)
		if len(v) >= 2 && (v[0] == '"' && v[len(v)-1] == '"' || v[0] == '\'' && v[len(v)-1] == '\'') {
			v = v[1 : len(v)-1]
		}
		out[k] = v
	}
	return out, nil
}

// Check loads a stack and returns its policy findings without deploying.
func (m *Manager) Check(ctx context.Context, name string, composeYAML, env []byte) ([]Violation, error) {
	_, vs, err := m.Load(ctx, name, composeYAML, env)
	if vs == nil {
		vs = []Violation{}
	}
	return vs, err
}
