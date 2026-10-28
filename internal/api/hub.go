package api

import (
	"context"
	"errors"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/photonring/marineris-server/internal/docker"
	"github.com/photonring/marineris-server/internal/icons"
	"github.com/photonring/marineris-server/internal/stacks"
	"github.com/photonring/marineris-server/internal/store"
)

var validWidgets = map[string]bool{
	"clock": true, "cpu": true, "memory": true, "disk": true, "network": true, "uptime": true,
}

type hubTile struct {
	ID       int64  `json:"id"`
	Title    string `json:"title"`
	Subtitle string `json:"subtitle"`
	URL      string `json:"url"`
	Icon     string `json:"icon,omitempty"` // same-origin URL, cache-busted
	Auth     string `json:"auth"`
}

// iconResolver finds where a route's hub icon comes from: its own icon,
// else the stack or service it links to, else (for a route to a stack's
// container) its service's icon. Icons of services are only their own:
// they never come from routes.
type iconResolver struct {
	s          *Server
	ctx        context.Context
	versions   map[string]time.Time // service icons by "stack/service"
	containers []docker.Container
	listed     bool
	mains      map[string]string // stack → main service
}

func (s *Server) newIconResolver(ctx context.Context) (*iconResolver, error) {
	v, err := s.Store.ImageVersions(ctx, store.ImageService)
	if err != nil {
		return nil, err
	}
	return &iconResolver{s: s, ctx: ctx, versions: v, mains: map[string]string{}}, nil
}

func (ir *iconResolver) list() []docker.Container {
	if !ir.listed {
		ir.containers, ir.listed = ir.s.listContainers(ir.ctx), true
	}
	return ir.containers
}

// serviceKey turns a link ("media" or "media/jellyfin") into a service key.
func (ir *iconResolver) serviceKey(link string) string {
	if strings.Contains(link, "/") {
		return link
	}
	main, ok := ir.mains[link]
	if !ok {
		if st, err := ir.s.Store.GetStack(ir.ctx, link); err == nil {
			if sum, err := ir.s.stackServices(ir.ctx, st, ir.list()); err == nil {
				main = sum.Main
			}
		}
		ir.mains[link] = main
	}
	return link + "/" + main
}

// resolve returns the service whose icon the route shows (empty with own
// set when the route has its own icon) and a version for cache busting.
func (ir *iconResolver) resolve(rt *store.Route) (service string, own bool, version time.Time) {
	if rt.Hub.HasIcon {
		return "", true, rt.UpdatedAt
	}
	link := rt.Hub.IconLink
	if link == "" && rt.Kind == store.RouteContainer {
		for _, c := range ir.list() {
			if c.Name == rt.Target.Container && c.Stack != "" && c.Service != "" {
				link = c.Stack + "/" + c.Service
			}
		}
	}
	if link == "" {
		return "", false, time.Time{}
	}
	key := ir.serviceKey(link)
	if t, ok := ir.versions[key]; ok {
		return key, false, t
	}
	return "", false, time.Time{}
}

// url is the hub icon address of a route, empty without an icon.
func (ir *iconResolver) url(rt *store.Route) string {
	service, own, version := ir.resolve(rt)
	if !own && service == "" {
		return ""
	}
	return "/api/hub/icons/" + strconv.FormatInt(rt.ID, 10) + "?v=" + strconv.FormatInt(version.UnixNano(), 36)
}

// handleHub returns the launcher tiles: every enabled, visible route.
func (s *Server) handleHub(w http.ResponseWriter, r *http.Request, rc *reqCtx) error {
	routes, err := s.Store.ListRoutes(r.Context())
	if err != nil {
		return err
	}
	ir, err := s.newIconResolver(r.Context())
	if err != nil {
		return err
	}
	tiles := []hubTile{}
	for _, rt := range routes {
		if !rt.Enabled || !rt.Hub.Visible {
			continue
		}
		t := hubTile{ID: rt.ID, Title: rt.Hub.Title, Subtitle: rt.Hub.Subtitle, URL: "https://" + rt.Host + rt.PathPrefix + "/", Auth: rt.Auth}
		if t.Title == "" {
			t.Title = rt.Name
		}
		t.Icon = ir.url(rt)
		tiles = append(tiles, t)
	}
	sort.SliceStable(tiles, func(i, j int) bool { return strings.ToLower(tiles[i].Title) < strings.ToLower(tiles[j].Title) })
	st := rc.settings
	return ok(w, map[string]any{
		"title":      st.HubTitle,
		"widgets":    st.HubWidgets,
		"background": st.HubBackground,
		"tiles":      tiles,
	})
}

func (s *Server) handleHubIcon(w http.ResponseWriter, r *http.Request, rc *reqCtx) error {
	id, err := idParam(r)
	if err != nil {
		return err
	}
	ctx := r.Context()
	rt, err := s.Store.GetRoute(ctx, id)
	if err != nil {
		return err
	}
	ir, err := s.newIconResolver(ctx)
	if err != nil {
		return err
	}
	service, own, _ := ir.resolve(rt)
	switch {
	case own:
		ic, err := s.Store.GetRouteIcon(ctx, id)
		if err != nil {
			return err
		}
		serveImage(w, &store.Image{ContentType: ic.ContentType, Data: ic.Data})
	case service != "":
		im, err := s.Store.GetImage(ctx, store.ImageService, service)
		if err != nil {
			return err
		}
		serveImage(w, im)
	default:
		return store.ErrNotFound
	}
	return nil
}

// handleRouteIconSet gives a route an icon of its own (an upload, an https
// URL or a dashboard-icons name) or links it to a stack's or a service's
// icon, which it then follows.
func (s *Server) handleRouteIconSet(w http.ResponseWriter, r *http.Request, rc *reqCtx) error {
	id, err := idParam(r)
	if err != nil {
		return err
	}
	var req struct {
		Data string `json:"data"` // base64
		URL  string `json:"url"`
		Name string `json:"name"` // dashboard-icons slug
		// The icon of a stack (its main service) or of one of its services.
		Stack   string `json:"stack"`
		Service string `json:"service"`
	}
	if err := decode(r, &req); err != nil {
		return err
	}
	ctx := r.Context()
	rt, err := s.Store.GetRoute(ctx, id)
	if err != nil {
		return err
	}
	var data []byte
	var ct string
	switch {
	case req.Stack != "":
		link, err := s.iconLink(ctx, req.Stack, req.Service)
		if err != nil {
			return err
		}
		// A link replaces the route's own icon.
		if err := s.Store.DeleteRouteIcon(ctx, id); err != nil {
			return err
		}
		if err := s.Store.SetRouteIconLink(ctx, id, link); err != nil {
			return err
		}
		s.audit(r, rc, "route.icon", rt.Name, true, "linked to "+link)
		return ok(w, map[string]any{"ok": true, "link": link})
	case req.Data != "":
		if ct, data, err = decodeImage(req.Data); err != nil {
			return err
		}
	case req.URL != "" || req.Name != "":
		u := strings.TrimSpace(req.URL)
		if req.Name != "" {
			if u, err = icons.SlugURL(strings.TrimSpace(req.Name)); err != nil {
				return fieldError("name", err.Error())
			}
		}
		if s.Icons == nil {
			return &Error{Status: http.StatusServiceUnavailable, Code: "unavailable", Message: "icon download is disabled"}
		}
		fctx, cancel := context.WithTimeout(ctx, 20*time.Second)
		defer cancel()
		if ct, data, err = s.Icons.Fetch(fctx, u); err != nil {
			return &Error{Status: http.StatusBadGateway, Code: "icon_fetch", Message: err.Error()}
		}
	default:
		return badRequest("give data, url or name")
	}
	if err := s.Store.SetRouteIcon(ctx, id, ct, data); err != nil {
		return err
	}
	// An icon of its own replaces a link (and bumps updated_at, so the
	// hub's cache-busting URL changes).
	if err := s.Store.SetRouteIconLink(ctx, id, ""); err != nil {
		return err
	}
	s.audit(r, rc, "route.icon", rt.Name, true, ct)
	return ok(w, map[string]any{"ok": true, "content_type": ct})
}

func (s *Server) handleRouteIconDelete(w http.ResponseWriter, r *http.Request, rc *reqCtx) error {
	id, err := idParam(r)
	if err != nil {
		return err
	}
	ctx := r.Context()
	rt, err := s.Store.GetRoute(ctx, id)
	if err != nil {
		return err
	}
	// Back to the automatic icon: the service's, for a stack's container.
	if err := s.Store.DeleteRouteIcon(ctx, id); err != nil {
		return err
	}
	if err := s.Store.SetRouteIconLink(ctx, id, ""); err != nil {
		return err
	}
	s.audit(r, rc, "route.icon", rt.Name, true, "automatic")
	return ok(w, map[string]bool{"ok": true})
}

// iconLink checks a stack (and service) a route icon can link to.
func (s *Server) iconLink(ctx context.Context, stack, service string) (string, error) {
	st, err := s.Store.GetStack(ctx, stack)
	if errors.Is(err, store.ErrNotFound) {
		return "", fieldError("stack", "no such stack")
	}
	if err != nil {
		return "", err
	}
	if service == "" {
		return st.Name, nil
	}
	defs, err := stacks.Services([]byte(st.Compose))
	if err != nil {
		return "", badRequest(err.Error())
	}
	for _, d := range defs {
		if d.Name == service {
			return st.Name + "/" + service, nil
		}
	}
	return "", fieldError("service", "no such service in this stack")
}

func (s *Server) handleSystemStats(w http.ResponseWriter, r *http.Request, rc *reqCtx) error {
	if s.Stats == nil {
		return &Error{Status: http.StatusServiceUnavailable, Code: "unavailable", Message: "metrics are not available"}
	}
	return ok(w, s.Stats.Snapshot())
}
