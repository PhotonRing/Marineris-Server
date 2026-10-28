package stacks

import (
	"bytes"
	"errors"
	"fmt"
	"regexp"
	"strings"

	"gopkg.in/yaml.v3"
)

// Labels a compose file can set on a service for Marineris.
const (
	// MainLabel ("true") marks the service whose icon represents the stack.
	MainLabel = "io.marineris.main"
	// IconLabel names the service's icon: a dashboard-icons name
	// ("jellyfin") or an https URL.
	IconLabel = "io.marineris.icon"
)

// EnvVar is one environment variable of a service. Null values (KEY with
// no value: taken from the environment) have Unset set.
type EnvVar struct {
	Key   string `json:"key"`
	Value string `json:"value"`
	Unset bool   `json:"unset,omitempty"`
}

// Mount is one entry of a service's volumes, as the stack page edits it.
type Mount struct {
	Type     string `json:"type"`   // "volume" or "bind"
	Source   string `json:"source"` // volume name (empty: anonymous) or host path
	Target   string `json:"target"` // path inside the container
	ReadOnly bool   `json:"read_only"`
	// External declares a volume that the stack does not own (an existing
	// Docker volume) when it is not declared in the file yet.
	External bool `json:"external,omitempty"`
}

// Service is the editable part of a service definition, read from the
// compose document as written (before interpolation).
type Service struct {
	Name        string   `json:"name"`
	Image       string   `json:"image"`
	Restart     string   `json:"restart"`
	Environment []EnvVar `json:"environment"`
	Volumes     []string `json:"volumes"`
	Mounts      []Mount  `json:"mounts"`
	Ports       []string `json:"ports"`
	Networks    []string `json:"networks"`
	Labels      []EnvVar `json:"labels"`
	// Fields written with the long (mapping) syntax cannot be edited as a
	// list of strings: the UI sends the user to the compose editor.
	LongSyntax []string `json:"long_syntax"`
	// Build is true for services built from a Dockerfile.
	Build bool `json:"build"`
}

// Main reports whether the service is marked as the stack's main service.
func (s Service) Main() bool { return labelValue(s.Labels, MainLabel) == "true" }

// Icon returns the icon label, if any.
func (s Service) Icon() string { return labelValue(s.Labels, IconLabel) }

func labelValue(labels []EnvVar, key string) string {
	for _, l := range labels {
		if l.Key == key {
			return l.Value
		}
	}
	return ""
}

// Services reads the services of a compose document, in file order.
func Services(composeYAML []byte) ([]Service, error) {
	_, services, err := parse(composeYAML)
	if err != nil {
		return nil, err
	}
	out := []Service{}
	for i := 0; i+1 < len(services.Content); i += 2 {
		name, def := services.Content[i].Value, services.Content[i+1]
		s := Service{Name: name, Environment: []EnvVar{}, Volumes: []string{}, Mounts: []Mount{}, Ports: []string{}, Networks: []string{}, Labels: []EnvVar{}, LongSyntax: []string{}}
		if def.Kind != yaml.MappingNode {
			out = append(out, s)
			continue
		}
		for j := 0; j+1 < len(def.Content); j += 2 {
			key, val := def.Content[j].Value, def.Content[j+1]
			switch key {
			case "image":
				s.Image = val.Value
			case "restart":
				s.Restart = val.Value
			case "build":
				s.Build = true
			case "environment", "labels":
				vars, long := keyValues(val)
				if long {
					s.LongSyntax = append(s.LongSyntax, key)
				}
				if key == "environment" {
					s.Environment = vars
				} else {
					s.Labels = vars
				}
			case "volumes", "ports":
				items, long := scalars(val)
				if long {
					s.LongSyntax = append(s.LongSyntax, key)
				}
				if key == "volumes" {
					s.Volumes = items
					s.Mounts = readMounts(val)
				} else {
					s.Ports = items
				}
			case "networks":
				switch val.Kind {
				case yaml.SequenceNode:
					s.Networks, _ = scalars(val)
				case yaml.MappingNode:
					for k := 0; k+1 < len(val.Content); k += 2 {
						s.Networks = append(s.Networks, val.Content[k].Value)
						if v := val.Content[k+1]; v.Kind != yaml.ScalarNode || v.Tag != "!!null" {
							if v.Kind != yaml.MappingNode || len(v.Content) > 0 {
								s.LongSyntax = appendOnce(s.LongSyntax, "networks")
							}
						}
					}
				}
			}
		}
		out = append(out, s)
	}
	return out, nil
}

// readMounts reads volumes entries in the short ("src:dst:ro") or long
// (mapping) syntax.
func readMounts(n *yaml.Node) []Mount {
	out := []Mount{}
	if n.Kind != yaml.SequenceNode {
		return out
	}
	for _, item := range n.Content {
		switch item.Kind {
		case yaml.ScalarNode:
			out = append(out, ParseMount(item.Value))
		case yaml.MappingNode:
			m := Mount{Type: "volume"}
			for i := 0; i+1 < len(item.Content); i += 2 {
				v := item.Content[i+1].Value
				switch item.Content[i].Value {
				case "type":
					m.Type = v
				case "source":
					m.Source = v
				case "target":
					m.Target = v
				case "read_only":
					m.ReadOnly = v == "true"
				}
			}
			out = append(out, m)
		}
	}
	return out
}

// ParseMount reads a short-syntax volumes entry.
func ParseMount(s string) Mount {
	parts := strings.Split(s, ":")
	m := Mount{Type: "volume"}
	switch len(parts) {
	case 1:
		m.Target = parts[0] // anonymous volume
		return m
	case 2:
		m.Source, m.Target = parts[0], parts[1]
	default:
		m.Source, m.Target = parts[0], parts[1]
		for _, opt := range strings.Split(parts[2], ",") {
			m.ReadOnly = m.ReadOnly || opt == "ro"
		}
	}
	if strings.HasPrefix(m.Source, "/") || strings.HasPrefix(m.Source, ".") || strings.HasPrefix(m.Source, "~") || strings.HasPrefix(m.Source, "$") {
		m.Type = "bind"
	}
	return m
}

var volumeNameRE = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_.-]{0,127}$`)

// shortSyntax writes a mount in the short syntax and checks it.
func (m Mount) shortSyntax() (string, error) {
	target := strings.TrimSpace(m.Target)
	src := strings.TrimSpace(m.Source)
	if !strings.HasPrefix(target, "/") || strings.ContainsAny(target, ": \t\n") {
		return "", fmt.Errorf("mounts: the container path %q must be absolute (/data), without ':' or spaces", target)
	}
	if strings.ContainsAny(src, ":\n") {
		return "", fmt.Errorf("mounts: the source %q cannot contain ':'", src)
	}
	switch m.Type {
	case "volume":
		if src == "" {
			if m.ReadOnly {
				return "", errors.New("mounts: an anonymous volume cannot be read-only")
			}
			return target, nil
		}
		if !volumeNameRE.MatchString(src) {
			return "", fmt.Errorf("mounts: invalid volume name %q", src)
		}
	case "bind":
		if !strings.HasPrefix(src, "/") && !strings.HasPrefix(src, "./") && !strings.HasPrefix(src, "../") && src != "." {
			return "", fmt.Errorf("mounts: the host path %q must be absolute (/srv/…) or relative to the stack (./data)", src)
		}
	default:
		return "", fmt.Errorf("mounts: unknown type %q (volume or bind)", m.Type)
	}
	out := src + ":" + target
	if m.ReadOnly {
		out += ":ro"
	}
	return out, nil
}

// TopVolumes returns the volumes declared at the top level of a compose
// document.
func TopVolumes(composeYAML []byte) []string {
	out := []string{}
	doc, _, err := parse(composeYAML)
	if err != nil {
		return out
	}
	if vols := mapGet(doc.Content[0], "volumes"); vols != nil && vols.Kind == yaml.MappingNode {
		for i := 0; i+1 < len(vols.Content); i += 2 {
			out = append(out, vols.Content[i].Value)
		}
	}
	return out
}

func appendOnce(list []string, v string) []string {
	for _, x := range list {
		if x == v {
			return list
		}
	}
	return append(list, v)
}

// keyValues reads environment/labels in either form ("K=v" list or map).
func keyValues(n *yaml.Node) ([]EnvVar, bool) {
	out := []EnvVar{}
	switch n.Kind {
	case yaml.MappingNode:
		for i := 0; i+1 < len(n.Content); i += 2 {
			v := n.Content[i+1]
			if v.Kind != yaml.ScalarNode {
				return out, true
			}
			out = append(out, EnvVar{Key: n.Content[i].Value, Value: v.Value, Unset: v.Tag == "!!null"})
		}
	case yaml.SequenceNode:
		for _, item := range n.Content {
			if item.Kind != yaml.ScalarNode {
				return out, true
			}
			k, v, ok := strings.Cut(item.Value, "=")
			out = append(out, EnvVar{Key: k, Value: v, Unset: !ok})
		}
	}
	return out, false
}

// scalars reads a list of strings; long is set when an item is a mapping.
func scalars(n *yaml.Node) ([]string, bool) {
	out := []string{}
	long := false
	if n.Kind != yaml.SequenceNode {
		return out, n.Kind != yaml.ScalarNode || n.Tag != "!!null"
	}
	for _, item := range n.Content {
		if item.Kind != yaml.ScalarNode {
			long = true
			continue
		}
		out = append(out, item.Value)
	}
	return out, long
}

func parse(composeYAML []byte) (*yaml.Node, *yaml.Node, error) {
	if len(composeYAML) > MaxComposeSize {
		return nil, nil, errors.New("compose file too large")
	}
	var doc yaml.Node
	if err := yaml.Unmarshal(composeYAML, &doc); err != nil {
		return nil, nil, fmt.Errorf("compose file: %w", err)
	}
	if doc.Kind != yaml.DocumentNode || len(doc.Content) == 0 || doc.Content[0].Kind != yaml.MappingNode {
		return nil, nil, errors.New("compose file: expected a mapping at the top level")
	}
	services := mapGet(doc.Content[0], "services")
	if services == nil || services.Kind != yaml.MappingNode {
		return nil, nil, errors.New("compose file: no services")
	}
	return &doc, services, nil
}

func mapGet(m *yaml.Node, key string) *yaml.Node {
	for i := 0; i+1 < len(m.Content); i += 2 {
		if m.Content[i].Value == key {
			return m.Content[i+1]
		}
	}
	return nil
}

// ServiceEdit lists the fields to change; nil fields are left untouched and
// empty values remove the key.
type ServiceEdit struct {
	Image       *string   `json:"image,omitempty"`
	Restart     *string   `json:"restart,omitempty"`
	Environment *[]EnvVar `json:"environment,omitempty"`
	Volumes     *[]string `json:"volumes,omitempty"`
	Mounts      *[]Mount  `json:"mounts,omitempty"`
	Labels      *[]EnvVar `json:"labels,omitempty"`
	Ports       *[]string `json:"ports,omitempty"`
	Networks    *[]string `json:"networks,omitempty"`
}

// EditService returns the compose document with one service changed. The
// rest of the document (other services, comments, key order) is kept.
func EditService(composeYAML []byte, service string, e ServiceEdit) ([]byte, error) {
	doc, services, err := parse(composeYAML)
	if err != nil {
		return nil, err
	}
	def := mapGet(services, service)
	if def == nil {
		return nil, fmt.Errorf("no service %q", service)
	}
	if def.Kind != yaml.MappingNode {
		*def = yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
	}
	str := func(key string, v *string) {
		if v == nil {
			return
		}
		if strings.TrimSpace(*v) == "" {
			mapDelete(def, key)
			return
		}
		mapSet(def, key, &yaml.Node{Kind: yaml.ScalarNode, Value: strings.TrimSpace(*v)})
	}
	list := func(key string, v *[]string) {
		if v == nil {
			return
		}
		seq := &yaml.Node{Kind: yaml.SequenceNode, Tag: "!!seq"}
		for _, item := range *v {
			if item = strings.TrimSpace(item); item != "" {
				seq.Content = append(seq.Content, &yaml.Node{Kind: yaml.ScalarNode, Value: item, Style: quoteIfNeeded(item)})
			}
		}
		if len(seq.Content) == 0 {
			mapDelete(def, key)
			return
		}
		mapSet(def, key, seq)
	}
	keyValues := func(key string, vars *[]EnvVar) error {
		if vars == nil {
			return nil
		}
		kv := &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
		seen := map[string]bool{}
		for _, v := range *vars {
			k := strings.TrimSpace(v.Key)
			if k == "" {
				continue
			}
			if strings.ContainsAny(k, "= \t\n:") {
				return fmt.Errorf("%s: invalid name %q", key, k)
			}
			if seen[k] {
				return fmt.Errorf("%s: %s is set twice", key, k)
			}
			seen[k] = true
			val := &yaml.Node{Kind: yaml.ScalarNode, Value: v.Value, Style: yaml.DoubleQuotedStyle}
			if v.Unset {
				val = &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!null", Value: ""}
			}
			kv.Content = append(kv.Content, &yaml.Node{Kind: yaml.ScalarNode, Value: k}, val)
		}
		if len(kv.Content) == 0 {
			mapDelete(def, key)
		} else {
			mapSet(def, key, kv)
		}
		return nil
	}
	str("image", e.Image)
	str("restart", e.Restart)
	if err := keyValues("environment", e.Environment); err != nil {
		return nil, err
	}
	if err := keyValues("labels", e.Labels); err != nil {
		return nil, err
	}
	if e.Mounts != nil {
		if e.Volumes != nil {
			return nil, errors.New("give mounts or volumes, not both")
		}
		vols := []string{}
		declared := map[string]bool{}
		for _, v := range TopVolumes(composeYAML) {
			declared[v] = true
		}
		top := mapGet(doc.Content[0], "volumes")
		for _, m := range *e.Mounts {
			s, err := m.shortSyntax()
			if err != nil {
				return nil, err
			}
			vols = append(vols, s)
			// A named volume must be declared at the top level.
			if src := strings.TrimSpace(m.Source); m.Type == "volume" && src != "" && !declared[src] {
				declared[src] = true
				if top == nil || top.Kind != yaml.MappingNode {
					top = &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
					mapSet(doc.Content[0], "volumes", top)
				}
				decl := &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map", Style: yaml.FlowStyle}
				if m.External {
					decl.Content = []*yaml.Node{{Kind: yaml.ScalarNode, Value: "external"}, {Kind: yaml.ScalarNode, Tag: "!!bool", Value: "true"}}
				}
				top.Content = append(top.Content, &yaml.Node{Kind: yaml.ScalarNode, Value: src}, decl)
			}
		}
		e.Volumes = &vols
	}
	list("volumes", e.Volumes)
	list("ports", e.Ports)
	list("networks", e.Networks)
	var buf bytes.Buffer
	enc := yaml.NewEncoder(&buf)
	enc.SetIndent(2)
	if err := enc.Encode(doc); err != nil {
		return nil, err
	}
	if err := enc.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// quoteIfNeeded quotes list items YAML would not read back as the same
// string, and port mappings ("80:80" is a base-60 number in YAML 1.1).
func quoteIfNeeded(s string) yaml.Style {
	var v any
	if err := yaml.Unmarshal([]byte(s), &v); err != nil || v != s || portLike.MatchString(s) {
		return yaml.DoubleQuotedStyle
	}
	return 0
}

var portLike = regexp.MustCompile(`^[0-9.:/a-z-]*[0-9]:[0-9]`)

func mapSet(m *yaml.Node, key string, val *yaml.Node) {
	for i := 0; i+1 < len(m.Content); i += 2 {
		if m.Content[i].Value == key {
			// Keep comments attached to the old value.
			val.HeadComment, val.LineComment, val.FootComment = m.Content[i+1].HeadComment, m.Content[i+1].LineComment, m.Content[i+1].FootComment
			m.Content[i+1] = val
			return
		}
	}
	m.Content = append(m.Content, &yaml.Node{Kind: yaml.ScalarNode, Value: key}, val)
}

func mapDelete(m *yaml.Node, key string) {
	for i := 0; i+1 < len(m.Content); i += 2 {
		if m.Content[i].Value == key {
			m.Content = append(m.Content[:i], m.Content[i+2:]...)
			return
		}
	}
}

// PinVolumeNames gives every volume the stack owns an explicit name equal to
// the one Docker already uses (<project>_<key>), so the stack keeps its data
// when its project name changes (rename). External volumes and volumes that
// already have a name are left alone.
func PinVolumeNames(composeYAML []byte, project string) ([]byte, error) {
	doc, _, err := parse(composeYAML)
	if err != nil {
		return nil, err
	}
	vols := mapGet(doc.Content[0], "volumes")
	if vols == nil || vols.Kind != yaml.MappingNode || len(vols.Content) == 0 {
		return composeYAML, nil
	}
	changed := false
	for i := 0; i+1 < len(vols.Content); i += 2 {
		key, decl := vols.Content[i].Value, vols.Content[i+1]
		if decl.Kind != yaml.MappingNode {
			*decl = yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
		}
		if mapGet(decl, "name") != nil {
			continue
		}
		if ext := mapGet(decl, "external"); ext != nil && ext.Value != "false" {
			continue
		}
		decl.Style = 0 // block style, for readability
		mapSet(decl, "name", &yaml.Node{Kind: yaml.ScalarNode, Value: project + "_" + key})
		changed = true
	}
	if !changed {
		return composeYAML, nil
	}
	var buf bytes.Buffer
	enc := yaml.NewEncoder(&buf)
	enc.SetIndent(2)
	if err := enc.Encode(doc); err != nil {
		return nil, err
	}
	if err := enc.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}
