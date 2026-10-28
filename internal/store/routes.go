package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"strings"
	"time"
)

// Route kinds.
const (
	RouteContainer = "container"
	RouteExternal  = "external"
	RouteStatic    = "static"
	RouteSPA       = "spa"
)

// Route access policies.
const (
	AuthNone     = "none"     // public
	AuthAuthelia = "authelia" // forward-auth through Authelia (2FA)
	AuthAdmins   = "admins"   // like AuthAuthelia, restricted to the admins group
)

// UsesAuthelia reports whether a route access policy goes through Authelia.
func UsesAuthelia(auth string) bool { return auth == AuthAuthelia || auth == AuthAdmins }

// RouteTarget describes where a route sends traffic. Which fields apply
// depends on the route kind.
type RouteTarget struct {
	// container
	Container string `json:"container,omitempty"`
	Port      int    `json:"port,omitempty"`
	// container and external: upstream speaks HTTPS
	UpstreamTLS bool `json:"upstream_tls,omitempty"`
	// external: base URL, e.g. https://192.168.1.10:8443
	URL string `json:"url,omitempty"`
	// external with upstream_tls: skip certificate verification (self-signed
	// appliances). Off by default.
	TLSInsecureSkipVerify bool `json:"tls_insecure_skip_verify,omitempty"`
	// static / spa: folder name below the static root
	Folder string `json:"folder,omitempty"`
}

// Route is a reverse-proxy route served by Caddy.
type Route struct {
	ID         int64       `json:"id"`
	Name       string      `json:"name"`
	Host       string      `json:"host"`
	PathPrefix string      `json:"path_prefix"`
	Kind       string      `json:"kind"`
	Target     RouteTarget `json:"target"`
	Auth       string      `json:"auth"`
	// RateLimit overrides the default rate limit; nil means default.
	RateLimit *RateLimit `json:"rate_limit"`
	Enabled   bool       `json:"enabled"`
	CreatedAt time.Time  `json:"created_at"`
	UpdatedAt time.Time  `json:"updated_at"`
	Hub       HubEntry   `json:"hub"`
}

// HubEntry is how a route appears on the hub (app launcher).
type HubEntry struct {
	Visible  bool   `json:"visible"`
	Title    string `json:"title"`    // empty: the route name
	Subtitle string `json:"subtitle"` // optional
	HasIcon  bool   `json:"has_icon"` // read-only
}

const routeCols = `id, name, host, path_prefix, kind, target, auth, rate_limit, enabled, created_at, updated_at,
	hub_visible, hub_title, hub_subtitle, EXISTS(SELECT 1 FROM route_icons i WHERE i.route_id = routes.id)`

func scanRoute(row interface{ Scan(...any) error }) (*Route, error) {
	var r Route
	var target, rl string
	if err := row.Scan(&r.ID, &r.Name, &r.Host, &r.PathPrefix, &r.Kind, &target, &r.Auth, &rl, &r.Enabled, &r.CreatedAt, &r.UpdatedAt,
		&r.Hub.Visible, &r.Hub.Title, &r.Hub.Subtitle, &r.Hub.HasIcon); err != nil {
		return nil, notFound(err)
	}
	if err := json.Unmarshal([]byte(target), &r.Target); err != nil {
		return nil, err
	}
	if rl != "" && rl != "{}" && rl != "null" {
		r.RateLimit = new(RateLimit)
		if err := json.Unmarshal([]byte(rl), r.RateLimit); err != nil {
			return nil, err
		}
	}
	return &r, nil
}

func routeJSON(r *Route) (string, string, error) {
	t, err := json.Marshal(r.Target)
	if err != nil {
		return "", "", err
	}
	rl := []byte("{}")
	if r.RateLimit != nil {
		if rl, err = json.Marshal(r.RateLimit); err != nil {
			return "", "", err
		}
	}
	return string(t), string(rl), nil
}

// CreateRoute inserts a route.
func (s *Store) CreateRoute(ctx context.Context, r *Route) error {
	t, rl, err := routeJSON(r)
	if err != nil {
		return err
	}
	now := s.now()
	res, err := s.db.ExecContext(ctx,
		`INSERT INTO routes(name, host, path_prefix, kind, target, auth, rate_limit, enabled, created_at, updated_at, hub_visible, hub_title, hub_subtitle)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		r.Name, r.Host, r.PathPrefix, r.Kind, t, r.Auth, rl, r.Enabled, now, now, r.Hub.Visible, r.Hub.Title, r.Hub.Subtitle)
	if isUnique(err) {
		return ErrConflict
	}
	if err != nil {
		return err
	}
	r.ID, err = res.LastInsertId()
	r.CreatedAt, r.UpdatedAt = now, now
	return err
}

// UpdateRoute replaces a route.
func (s *Store) UpdateRoute(ctx context.Context, r *Route) error {
	t, rl, err := routeJSON(r)
	if err != nil {
		return err
	}
	r.UpdatedAt = s.now()
	return s.execOne(ctx,
		`UPDATE routes SET name = ?, host = ?, path_prefix = ?, kind = ?, target = ?, auth = ?, rate_limit = ?, enabled = ?, updated_at = ?,
		 hub_visible = ?, hub_title = ?, hub_subtitle = ? WHERE id = ?`,
		r.Name, r.Host, r.PathPrefix, r.Kind, t, r.Auth, rl, r.Enabled, r.UpdatedAt, r.Hub.Visible, r.Hub.Title, r.Hub.Subtitle, r.ID)
}

// GetRoute returns a route by ID.
func (s *Store) GetRoute(ctx context.Context, id int64) (*Route, error) {
	return scanRoute(s.db.QueryRowContext(ctx, `SELECT `+routeCols+` FROM routes WHERE id = ?`, id))
}

// ListRoutes returns all routes.
func (s *Store) ListRoutes(ctx context.Context) ([]*Route, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+routeCols+` FROM routes ORDER BY host, path_prefix`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*Route
	for rows.Next() {
		r, err := scanRoute(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// DeleteRoute deletes a route.
func (s *Store) DeleteRoute(ctx context.Context, id int64) error {
	return s.execOne(ctx, `DELETE FROM routes WHERE id = ?`, id)
}

// RouteIcon is the hub icon of a route.
type RouteIcon struct {
	ContentType string
	Data        []byte
	UpdatedAt   time.Time
}

// SetRouteIcon stores (or replaces) the icon of a route.
func (s *Store) SetRouteIcon(ctx context.Context, routeID int64, contentType string, data []byte) error {
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO route_icons(route_id, content_type, data, updated_at) VALUES (?, ?, ?, ?)
		 ON CONFLICT(route_id) DO UPDATE SET content_type = excluded.content_type, data = excluded.data, updated_at = excluded.updated_at`,
		routeID, contentType, data, s.now())
	if err != nil && strings.Contains(err.Error(), "FOREIGN KEY") {
		return ErrNotFound
	}
	return err
}

// GetRouteIcon returns the icon of a route.
func (s *Store) GetRouteIcon(ctx context.Context, routeID int64) (*RouteIcon, error) {
	var ic RouteIcon
	err := s.db.QueryRowContext(ctx, `SELECT content_type, data, updated_at FROM route_icons WHERE route_id = ?`, routeID).
		Scan(&ic.ContentType, &ic.Data, &ic.UpdatedAt)
	if err != nil {
		return nil, notFound(err)
	}
	return &ic, nil
}

// DeleteRouteIcon removes the icon of a route.
func (s *Store) DeleteRouteIcon(ctx context.Context, routeID int64) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM route_icons WHERE route_id = ?`, routeID)
	return err
}

// Stack is a docker-compose project managed by Marineris.
type Stack struct {
	Name       string     `json:"name"`
	Compose    string     `json:"compose"`
	Env        string     `json:"-"` // sealed .env contents
	UnsafeAck  bool       `json:"unsafe_ack"`
	CreatedAt  time.Time  `json:"created_at"`
	UpdatedAt  time.Time  `json:"updated_at"`
	DeployedAt *time.Time `json:"deployed_at"`
}

const stackCols = `name, compose, env, unsafe_ack, created_at, updated_at, deployed_at`

func scanStack(row interface{ Scan(...any) error }) (*Stack, error) {
	var st Stack
	var dep sql.NullTime
	if err := row.Scan(&st.Name, &st.Compose, &st.Env, &st.UnsafeAck, &st.CreatedAt, &st.UpdatedAt, &dep); err != nil {
		return nil, notFound(err)
	}
	st.DeployedAt = nullTime(dep)
	return &st, nil
}

// CreateStack inserts a stack.
func (s *Store) CreateStack(ctx context.Context, st *Stack) error {
	now := s.now()
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO stacks(name, compose, env, unsafe_ack, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?)`,
		st.Name, st.Compose, st.Env, st.UnsafeAck, now, now)
	if isUnique(err) {
		return ErrConflict
	}
	st.CreatedAt, st.UpdatedAt = now, now
	return err
}

// UpdateStack replaces the compose file, env and unsafe flag of a stack.
func (s *Store) UpdateStack(ctx context.Context, st *Stack) error {
	st.UpdatedAt = s.now()
	return s.execOne(ctx, `UPDATE stacks SET compose = ?, env = ?, unsafe_ack = ?, updated_at = ? WHERE name = ?`,
		st.Compose, st.Env, st.UnsafeAck, st.UpdatedAt, st.Name)
}

// MarkStackDeployed records a successful deployment.
func (s *Store) MarkStackDeployed(ctx context.Context, name string) error {
	return s.execOne(ctx, `UPDATE stacks SET deployed_at = ? WHERE name = ?`, s.now(), name)
}

// GetStack returns a stack by name.
func (s *Store) GetStack(ctx context.Context, name string) (*Stack, error) {
	return scanStack(s.db.QueryRowContext(ctx, `SELECT `+stackCols+` FROM stacks WHERE name = ?`, name))
}

// ListStacks returns all stacks.
func (s *Store) ListStacks(ctx context.Context) ([]*Stack, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+stackCols+` FROM stacks ORDER BY name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*Stack
	for rows.Next() {
		st, err := scanStack(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, st)
	}
	return out, rows.Err()
}

// DeleteStack deletes a stack record.
func (s *Store) DeleteStack(ctx context.Context, name string) error {
	return s.execOne(ctx, `DELETE FROM stacks WHERE name = ?`, name)
}
