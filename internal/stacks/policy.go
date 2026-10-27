// Package stacks manages docker-compose stacks: safe loading, a security
// policy check, and deployment through the Docker Compose SDK.
package stacks

import (
	"fmt"
	"path/filepath"
	"sort"
	"strings"

	"github.com/compose-spec/compose-go/v2/types"
	"gopkg.in/yaml.v3"
)

// Violation is a policy finding for a stack.
type Violation struct {
	Service string `json:"service,omitempty"`
	Rule    string `json:"rule"`
	Message string `json:"message"`
	// Overridable findings may be accepted explicitly by the admin
	// (unsafe_ack, with step-up authentication). The others are always
	// refused because the socket proxy would reject them anyway or because
	// they would hand the host to the stack.
	Overridable bool `json:"overridable"`
}

// Policy configures the checks.
type Policy struct {
	// StackDir is the stack's own directory on the host.
	StackDir string
	// AllowedBindRoots are host directories bind mounts may come from, in
	// addition to StackDir. Must match the socket proxy's allow-list.
	AllowedBindRoots []string
	// ReservedNames are container names used by the Marineris stack.
	ReservedNames []string
}

// Capabilities that effectively grant control of the host.
var dangerousCaps = map[string]bool{
	"ALL": true, "SYS_ADMIN": true, "SYS_MODULE": true, "SYS_PTRACE": true, "SYS_RAWIO": true,
	"SYS_BOOT": true, "SYS_TIME": true, "NET_ADMIN": true, "DAC_READ_SEARCH": true, "DAC_OVERRIDE": true,
	"BPF": true, "PERFMON": true, "MAC_ADMIN": true, "MAC_OVERRIDE": true, "SYSLOG": true,
}

// Host paths that may never be bind mounted, even inside an allowed root.
var forbiddenPaths = []string{
	"/", "/etc", "/root", "/home", "/boot", "/dev", "/proc", "/sys", "/run", "/var/run",
	"/var/lib/docker", "/var/lib/containerd", "/usr", "/bin", "/sbin", "/lib", "/lib64",
}

var allowedTopLevel = map[string]bool{
	"services": true, "networks": true, "volumes": true, "secrets": true, "configs": true,
	"name": true, "version": true,
}

// PreScan inspects the raw compose document before it is loaded. It refuses
// constructs that make the loader read files outside the stack directory
// (include, extends from another file) and unknown top-level keys.
func PreScan(raw []byte) []Violation {
	var doc map[string]any
	if err := yaml.Unmarshal(raw, &doc); err != nil {
		return []Violation{{Rule: "syntax", Message: "invalid YAML: " + err.Error()}}
	}
	if doc == nil {
		return []Violation{{Rule: "syntax", Message: "the compose file is empty"}}
	}
	var out []Violation
	keys := make([]string, 0, len(doc))
	for k := range doc {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		if strings.HasPrefix(k, "x-") {
			continue
		}
		if k == "include" {
			out = append(out, Violation{Rule: "include", Message: "'include' is not supported: put everything in one compose file"})
			continue
		}
		if !allowedTopLevel[k] {
			out = append(out, Violation{Rule: "syntax", Message: fmt.Sprintf("unknown top-level key %q", k)})
		}
	}
	services, _ := doc["services"].(map[string]any)
	if len(services) == 0 {
		out = append(out, Violation{Rule: "syntax", Message: "the compose file defines no services"})
	}
	for name, raw := range services {
		svc, _ := raw.(map[string]any)
		if ext, ok := svc["extends"].(map[string]any); ok {
			if _, hasFile := ext["file"]; hasFile {
				out = append(out, Violation{Service: name, Rule: "extends", Message: "'extends' from another file is not supported"})
			}
		}
		// The loader reads these files while loading; refuse paths that
		// could point outside the stack directory before that happens.
		for _, key := range []string{"env_file", "label_file"} {
			for _, p := range filePaths(svc[key]) {
				if !relativeInside(p) {
					out = append(out, Violation{Service: name, Rule: key, Message: fmt.Sprintf("%s %q must be a relative path inside the stack directory", key, p)})
				}
			}
		}
	}
	for _, section := range []string{"secrets", "configs"} {
		entries, _ := doc[section].(map[string]any)
		for name, raw := range entries {
			e, _ := raw.(map[string]any)
			if f, ok := e["file"].(string); ok && !relativeInside(f) {
				out = append(out, Violation{Rule: section, Message: fmt.Sprintf("%s %q: file must be a relative path inside the stack directory", section, name)})
			}
		}
	}
	return out
}

// filePaths extracts paths from a string, a list of strings or a list of
// {path: ...} objects.
func filePaths(v any) []string {
	switch x := v.(type) {
	case string:
		return []string{x}
	case []any:
		var out []string
		for _, e := range x {
			switch y := e.(type) {
			case string:
				out = append(out, y)
			case map[string]any:
				if p, ok := y["path"].(string); ok {
					out = append(out, p)
				}
			}
		}
		return out
	}
	return nil
}

func relativeInside(p string) bool {
	if p == "" || filepath.IsAbs(p) || strings.Contains(p, "$") || strings.HasPrefix(p, "~") {
		return false
	}
	c := filepath.Clean(p)
	return c != ".." && !strings.HasPrefix(c, "../")
}

// Check applies the security policy to a loaded project.
func Check(p *types.Project, pol Policy) []Violation {
	var out []Violation
	add := func(svc, rule, msg string, overridable bool) {
		out = append(out, Violation{Service: svc, Rule: rule, Message: msg, Overridable: overridable})
	}
	reserved := map[string]bool{}
	for _, n := range pol.ReservedNames {
		reserved[n] = true
	}
	names := make([]string, 0, len(p.Services))
	for n := range p.Services {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, name := range names {
		s := p.Services[name]
		if s.Build != nil {
			add(name, "build", "building images is not allowed; use a published image", false)
		}
		if s.Image == "" {
			add(name, "image", "an image is required", false)
		}
		if reserved[s.ContainerName] {
			add(name, "container_name", fmt.Sprintf("container name %q is reserved for Marineris", s.ContainerName), false)
		}
		if s.Privileged {
			add(name, "privileged", "privileged mode gives the container full control of the host", true)
		}
		for field, v := range map[string]string{"network_mode": s.NetworkMode, "pid": s.Pid, "ipc": s.Ipc, "uts": s.Uts, "userns_mode": s.UserNSMode, "cgroup": s.Cgroup} {
			if v == "host" {
				add(name, field, fmt.Sprintf("%s: host shares a host namespace with the container", field), true)
			}
		}
		if strings.HasPrefix(s.NetworkMode, "container:") || strings.HasPrefix(s.Pid, "container:") || strings.HasPrefix(s.Ipc, "container:") {
			add(name, "namespace", "joining another container's namespaces is not allowed", true)
		}
		if len(s.VolumesFrom) > 0 {
			add(name, "volumes_from", "volumes_from can expose other containers' data", true)
		}
		for _, c := range s.CapAdd {
			cc := strings.TrimPrefix(strings.ToUpper(c), "CAP_")
			if dangerousCaps[cc] {
				add(name, "cap_add", fmt.Sprintf("capability %s grants control over the host", cc), true)
			}
		}
		if len(s.Devices) > 0 {
			add(name, "devices", "direct access to host devices", true)
		}
		for _, o := range s.SecurityOpt {
			lo := strings.ToLower(o)
			if strings.Contains(lo, "unconfined") || strings.Contains(lo, "label=disable") || strings.Contains(lo, "label:disable") {
				add(name, "security_opt", fmt.Sprintf("security option %q disables a protection", o), true)
			}
		}
		if s.Runtime != "" && s.Runtime != "runc" {
			add(name, "runtime", fmt.Sprintf("custom runtime %q", s.Runtime), true)
		}
		for _, v := range s.Volumes {
			if v.Type != types.VolumeTypeBind {
				continue
			}
			if msg := checkBind(v.Source, pol); msg != "" {
				add(name, "bind_mount", msg, false)
			}
		}
		for _, ef := range s.EnvFiles {
			if !within(ef.Path, pol.StackDir) {
				add(name, "env_file", fmt.Sprintf("env_file %q must be inside the stack directory", ef.Path), false)
			}
		}
		for _, lf := range s.LabelFiles {
			if !within(lf, pol.StackDir) {
				add(name, "label_file", fmt.Sprintf("label_file %q must be inside the stack directory", lf), false)
			}
		}
	}
	for n, sec := range p.Secrets {
		if sec.File != "" && !within(sec.File, pol.StackDir) {
			add("", "secrets", fmt.Sprintf("secret %q file must be inside the stack directory", n), false)
		}
	}
	for n, cfg := range p.Configs {
		if cfg.File != "" && !within(cfg.File, pol.StackDir) {
			add("", "configs", fmt.Sprintf("config %q file must be inside the stack directory", n), false)
		}
	}
	return out
}

func checkBind(src string, pol Policy) string {
	if !filepath.IsAbs(src) {
		return fmt.Sprintf("bind mount %q is not an absolute path", src)
	}
	clean := filepath.Clean(src)
	for _, f := range forbiddenPaths {
		if clean == f {
			return fmt.Sprintf("bind mount of %s is never allowed", clean)
		}
	}
	if strings.HasSuffix(clean, "docker.sock") || strings.Contains(clean, "/docker.sock") {
		return "mounting the Docker socket is never allowed"
	}
	if within(clean, pol.StackDir) {
		return ""
	}
	for _, root := range pol.AllowedBindRoots {
		if within(clean, root) {
			return ""
		}
	}
	return fmt.Sprintf("bind mount %s is outside the stack directory and the allowed roots (%s)", clean, strings.Join(pol.AllowedBindRoots, ", "))
}

// within reports whether p is root or inside root (lexically, after Clean).
func within(p, root string) bool {
	if root == "" || !filepath.IsAbs(p) {
		return false
	}
	p, root = filepath.Clean(p), filepath.Clean(root)
	return p == root || strings.HasPrefix(p, root+string(filepath.Separator))
}

// Blocking returns the violations that prevent deployment given whether the
// admin acknowledged the overridable ones.
func Blocking(vs []Violation, unsafeAck bool) []Violation {
	var out []Violation
	for _, v := range vs {
		if !v.Overridable || !unsafeAck {
			out = append(out, v)
		}
	}
	return out
}
