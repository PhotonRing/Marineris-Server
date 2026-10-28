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

// Service is the editable part of a service definition, read from the
// compose document as written (before interpolation).
type Service struct {
	Name        string   `json:"name"`
	Image       string   `json:"image"`
	Restart     string   `json:"restart"`
	Environment []EnvVar `json:"environment"`
	Volumes     []string `json:"volumes"`
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
		s := Service{Name: name, Environment: []EnvVar{}, Volumes: []string{}, Ports: []string{}, Networks: []string{}, Labels: []EnvVar{}, LongSyntax: []string{}}
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
	str("image", e.Image)
	str("restart", e.Restart)
	if e.Environment != nil {
		env := &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
		seen := map[string]bool{}
		for _, v := range *e.Environment {
			k := strings.TrimSpace(v.Key)
			if k == "" {
				continue
			}
			if strings.ContainsAny(k, "= \t\n") {
				return nil, fmt.Errorf("environment: invalid variable name %q", k)
			}
			if seen[k] {
				return nil, fmt.Errorf("environment: %s is set twice", k)
			}
			seen[k] = true
			val := &yaml.Node{Kind: yaml.ScalarNode, Value: v.Value, Style: yaml.DoubleQuotedStyle}
			if v.Unset {
				val = &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!null", Value: ""}
			}
			env.Content = append(env.Content, &yaml.Node{Kind: yaml.ScalarNode, Value: k}, val)
		}
		if len(env.Content) == 0 {
			mapDelete(def, "environment")
		} else {
			mapSet(def, "environment", env)
		}
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
