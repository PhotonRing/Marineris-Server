package api

import (
	"context"
	"encoding/base64"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/photonring/marineris-server/internal/icons"
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

// handleHub returns the launcher tiles: every enabled, visible route.
func (s *Server) handleHub(w http.ResponseWriter, r *http.Request, rc *reqCtx) error {
	routes, err := s.Store.ListRoutes(r.Context())
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
		if rt.Hub.HasIcon {
			t.Icon = "/api/hub/icons/" + strconv.FormatInt(rt.ID, 10) + "?v=" + strconv.FormatInt(rt.UpdatedAt.Unix(), 10)
		}
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
	ic, err := s.Store.GetRouteIcon(r.Context(), id)
	if err != nil {
		return err
	}
	h := w.Header()
	h.Set("Content-Type", ic.ContentType)
	h.Set("Cache-Control", "private, max-age=86400")
	// SVGs can carry scripts: never let them run, even when opened directly.
	h.Set("Content-Security-Policy", "default-src 'none'; style-src 'unsafe-inline'; sandbox")
	_, _ = w.Write(ic.Data) //nolint:gosec // sniffed image with a fixed type and a sandboxing CSP
	return nil
}

// handleRouteIconSet sets a route's icon from an upload (base64), an https
// URL or a dashboard-icons name.
func (s *Server) handleRouteIconSet(w http.ResponseWriter, r *http.Request, rc *reqCtx) error {
	id, err := idParam(r)
	if err != nil {
		return err
	}
	var req struct {
		Data string `json:"data"` // base64
		URL  string `json:"url"`
		Name string `json:"name"` // dashboard-icons slug
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
	case req.Data != "":
		data, err = base64.StdEncoding.DecodeString(req.Data)
		if err != nil {
			return fieldError("data", "must be base64")
		}
		if ct, err = icons.Detect(data); err != nil {
			return fieldError("data", err.Error())
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
	// Bump updated_at so the hub's cache-busting URL changes.
	if err := s.Store.UpdateRoute(ctx, rt); err != nil {
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
	if err := s.Store.DeleteRouteIcon(ctx, id); err != nil {
		return err
	}
	if err := s.Store.UpdateRoute(ctx, rt); err != nil {
		return err
	}
	return ok(w, map[string]bool{"ok": true})
}

func (s *Server) handleSystemStats(w http.ResponseWriter, r *http.Request, rc *reqCtx) error {
	if s.Stats == nil {
		return &Error{Status: http.StatusServiceUnavailable, Code: "unavailable", Message: "metrics are not available"}
	}
	return ok(w, s.Stats.Snapshot())
}
