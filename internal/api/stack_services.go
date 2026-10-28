package api

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/photonring/marineris-server/internal/docker"
	"github.com/photonring/marineris-server/internal/icons"
	"github.com/photonring/marineris-server/internal/stacks"
	"github.com/photonring/marineris-server/internal/store"
	"github.com/photonring/marineris-server/internal/validate"
)

// Status of a service or a stack, from its containers.
const (
	stateRunning     = "running"
	stateStarting    = "starting"
	stateUnhealthy   = "unhealthy"
	statePartial     = "partial"
	stateStopped     = "stopped"
	stateNotDeployed = "not_deployed"
)

// containersState sums up the state of a set of containers.
func containersState(cs []docker.Container) string {
	if len(cs) == 0 {
		return stateNotDeployed
	}
	running := 0
	state := stateRunning
	for _, c := range cs {
		if c.State == "running" {
			running++
		}
		switch {
		case c.Health == "unhealthy":
			state = stateUnhealthy
		case c.Health == "starting" || c.State == "restarting":
			if state == stateRunning {
				state = stateStarting
			}
		}
	}
	switch {
	case running == 0:
		return stateStopped
	case running < len(cs) && state == stateRunning:
		return statePartial
	}
	return state
}

type serviceView struct {
	stacks.Service
	Main       bool               `json:"main"`
	Icon       string             `json:"icon"`
	IconSource string             `json:"icon_source"` // "" or "custom"
	State      string             `json:"state"`
	Containers []docker.Container `json:"containers"`
}

type stackSummary struct {
	Main     string        `json:"main"`
	Icon     string        `json:"icon"`
	State    string        `json:"state"`
	Services []serviceView `json:"services"`
	// For the mounts editor: volumes declared in the file, and where bind
	// mounts may come from besides the stack's folder.
	Volumes   []string `json:"volumes"`
	BindRoots []string `json:"bind_roots"`
	// PendingChanges: the file was saved after the last deployment.
	PendingChanges bool `json:"pending_changes"`
}

// stackServices describes the services of a stack: definition, containers,
// status and icon. The main service (whose icon is the stack's) is the one
// labelled io.marineris.main, else the first one published by a route, else
// the first one of the file.
func (s *Server) stackServices(ctx context.Context, st *store.Stack, containers []docker.Container) (stackSummary, error) {
	sum := stackSummary{Services: []serviceView{}, Volumes: stacks.TopVolumes([]byte(st.Compose)), BindRoots: []string{},
		PendingChanges: st.DeployedAt != nil && st.UpdatedAt.After(*st.DeployedAt)}
	if s.Stacks != nil {
		sum.BindRoots = s.Stacks.BindRoots()
	}
	defs, err := stacks.Services([]byte(st.Compose))
	if err != nil {
		return sum, badRequest(err.Error())
	}
	iconVersions, err := s.Store.ImageVersions(ctx, store.ImageService)
	if err != nil {
		return sum, err
	}
	routes, err := s.Store.ListRoutes(ctx)
	if err != nil {
		return sum, err
	}
	routed := map[string]bool{} // containers published by a route
	for _, rt := range routes {
		if rt.Kind == store.RouteContainer {
			routed[rt.Target.Container] = true
		}
	}
	firstRouted := ""
	for _, def := range defs {
		v := serviceView{Service: def, Containers: []docker.Container{}}
		for _, c := range containers {
			if c.Stack == st.Name && c.Service == def.Name {
				v.Containers = append(v.Containers, c)
			}
		}
		v.State = containersState(v.Containers)
		key := st.Name + "/" + def.Name
		if t, ok := iconVersions[key]; ok {
			v.Icon = "/api/stacks/" + st.Name + "/icons/" + def.Name + "?v=" + strconv.FormatInt(t.UnixNano(), 36)
			v.IconSource = "custom"
		}
		for _, c := range v.Containers {
			if routed[c.Name] && firstRouted == "" {
				firstRouted = def.Name
			}
		}
		if def.Main() && sum.Main == "" {
			sum.Main = def.Name
		}
		sum.Services = append(sum.Services, v)
	}
	if sum.Main == "" {
		sum.Main = firstRouted
	}
	if sum.Main == "" && len(defs) > 0 {
		sum.Main = defs[0].Name
	}
	var all []docker.Container
	for i := range sum.Services {
		v := &sum.Services[i]
		v.Main = v.Name == sum.Main
		if v.Main {
			sum.Icon = v.Icon
		}
		all = append(all, v.Containers...)
	}
	sum.State = containersState(all)
	if sum.State == stateRunning || sum.State == stateStarting {
		// Services without any container (not started yet) make it partial.
		for _, v := range sum.Services {
			if len(v.Containers) == 0 {
				sum.State = statePartial
			}
		}
	}
	return sum, nil
}

func (s *Server) listContainers(ctx context.Context) []docker.Container {
	if s.Docker == nil {
		return nil
	}
	cs, err := s.Docker.List(ctx)
	if err != nil {
		return nil
	}
	return cs
}

func (s *Server) handleStackServices(w http.ResponseWriter, r *http.Request, rc *reqCtx) error {
	ctx := r.Context()
	st, err := s.Store.GetStack(ctx, r.PathValue("name"))
	if err != nil {
		return err
	}
	sum, err := s.stackServices(ctx, st, s.listContainers(ctx))
	if err != nil {
		return err
	}
	return ok(w, sum)
}

// lookupService returns a stack and checks that it defines the service.
func (s *Server) lookupService(ctx context.Context, r *http.Request) (*store.Stack, string, error) {
	st, err := s.Store.GetStack(ctx, r.PathValue("name"))
	if err != nil {
		return nil, "", err
	}
	name := r.PathValue("service")
	defs, err := stacks.Services([]byte(st.Compose))
	if err != nil {
		return nil, "", badRequest(err.Error())
	}
	for _, d := range defs {
		if d.Name == name {
			return st, name, nil
		}
	}
	return nil, "", &Error{Status: http.StatusNotFound, Code: "not_found", Message: "no such service in this stack"}
}

// handleServiceUpdate changes one service of a stack's compose file (the
// fields the UI's tabs edit) and, with deploy, recreates that service.
func (s *Server) handleServiceUpdate(w http.ResponseWriter, r *http.Request, rc *reqCtx) error {
	if err := s.requireStacks(); err != nil {
		return err
	}
	var req struct {
		stacks.ServiceEdit
		Deploy bool `json:"deploy"`
	}
	if err := decode(r, &req); err != nil {
		return err
	}
	ctx := context.WithoutCancel(r.Context())
	st, svc, err := s.lookupService(ctx, r)
	if err != nil {
		return err
	}
	compose, err := stacks.EditService([]byte(st.Compose), svc, req.ServiceEdit)
	if err != nil {
		return badRequest(err.Error())
	}
	env, err := s.stackEnv(st)
	if err != nil {
		return err
	}
	vs, err := s.Stacks.Check(ctx, st.Name, compose, []byte(env))
	if err != nil {
		return badRequest(err.Error())
	}
	if blocking := stacks.Blocking(vs, st.UnsafeAck); len(blocking) > 0 {
		return &stacks.PolicyError{Violations: blocking}
	}
	st.Compose = string(compose)
	if err := s.Store.UpdateStack(ctx, st); err != nil {
		return err
	}
	s.audit(r, rc, "stack.service.update", st.Name+"/"+svc, true, "")
	if req.Deploy {
		if err := s.recreate(r, rc, st, env, []string{svc}, false); err != nil {
			return err
		}
	}
	return ok(w, map[string]any{"ok": true, "compose": st.Compose})
}

func (s *Server) recreate(r *http.Request, rc *reqCtx, st *store.Stack, env string, services []string, pull bool) error {
	ctx := context.WithoutCancel(r.Context())
	err := s.Stacks.Recreate(ctx, st.Name, []byte(st.Compose), []byte(env), st.UnsafeAck, services, pull)
	action, target := "stack.recreate", st.Name
	if pull {
		action = "stack.update"
	}
	if len(services) == 1 {
		target += "/" + services[0]
	}
	s.audit(r, rc, action, target, err == nil, errString(err))
	if err != nil {
		var pe *stacks.PolicyError
		if errors.As(err, &pe) {
			return err
		}
		return dockerErr(err)
	}
	if err := s.Store.MarkStackDeployed(ctx, st.Name); err != nil {
		return err
	}
	s.fetchServiceIcons(st)
	return nil
}

// handleServiceAction starts, stops or restarts the containers of a service,
// or recreates it ("recreate", "update" = pull the image first).
func (s *Server) handleServiceAction(action string) func(http.ResponseWriter, *http.Request, *reqCtx) error {
	return func(w http.ResponseWriter, r *http.Request, rc *reqCtx) error {
		if err := s.requireStacks(); err != nil {
			return err
		}
		if err := s.requireDocker(); err != nil {
			return err
		}
		ctx := context.WithoutCancel(r.Context())
		st, svc, err := s.lookupService(ctx, r)
		if err != nil {
			return err
		}
		if action == "recreate" || action == "update" {
			env, err := s.stackEnv(st)
			if err != nil {
				return err
			}
			if err := s.recreate(r, rc, st, env, []string{svc}, action == "update"); err != nil {
				return err
			}
			return ok(w, map[string]bool{"ok": true})
		}
		cs, err := s.Docker.List(ctx)
		if err != nil {
			return dockerErr(err)
		}
		n := 0
		for _, c := range cs {
			if c.Stack != st.Name || c.Service != svc {
				continue
			}
			n++
			err := s.Docker.Action(ctx, c.Name, action)
			s.audit(r, rc, "container."+action, c.Name, err == nil, errString(err))
			if err != nil {
				return dockerErr(err)
			}
		}
		if n == 0 {
			return &Error{Status: http.StatusConflict, Code: "not_deployed", Message: "the service has no container: deploy the stack first"}
		}
		return ok(w, map[string]bool{"ok": true})
	}
}

// handleStackRestart restarts every container of a stack.
func (s *Server) handleStackRestart(w http.ResponseWriter, r *http.Request, rc *reqCtx) error {
	if err := s.requireDocker(); err != nil {
		return err
	}
	ctx := context.WithoutCancel(r.Context())
	st, err := s.Store.GetStack(ctx, r.PathValue("name"))
	if err != nil {
		return err
	}
	cs, err := s.Docker.List(ctx)
	if err != nil {
		return dockerErr(err)
	}
	for _, c := range cs {
		if c.Stack == st.Name {
			if err := s.Docker.Action(ctx, c.Name, "restart"); err != nil {
				s.audit(r, rc, "stack.restart", st.Name, false, err.Error())
				return dockerErr(err)
			}
		}
	}
	s.audit(r, rc, "stack.restart", st.Name, true, "")
	return ok(w, map[string]bool{"ok": true})
}

// handleStackPull updates a stack: pulls the images of every service and
// recreates all of them (which also applies the saved definition).
func (s *Server) handleStackPull(w http.ResponseWriter, r *http.Request, rc *reqCtx) error {
	if err := s.requireStacks(); err != nil {
		return err
	}
	st, err := s.Store.GetStack(r.Context(), r.PathValue("name"))
	if err != nil {
		return err
	}
	env, err := s.stackEnv(st)
	if err != nil {
		return err
	}
	if err := s.recreate(r, rc, st, env, nil, true); err != nil {
		return err
	}
	return ok(w, map[string]bool{"ok": true})
}

// --- Service icons -----------------------------------------------------------

func (s *Server) handleServiceIcon(w http.ResponseWriter, r *http.Request, rc *reqCtx) error {
	ctx := r.Context()
	st, svc, err := s.lookupService(ctx, r)
	if err != nil {
		return err
	}
	im, err := s.Store.GetImage(ctx, store.ImageService, st.Name+"/"+svc)
	if err != nil {
		return err
	}
	serveImage(w, im)
	return nil
}

func (s *Server) handleServiceIconSet(w http.ResponseWriter, r *http.Request, rc *reqCtx) error {
	var req struct {
		Data string `json:"data"` // base64
		URL  string `json:"url"`
		Name string `json:"name"` // dashboard-icons slug
	}
	if err := decode(r, &req); err != nil {
		return err
	}
	ctx := r.Context()
	st, svc, err := s.lookupService(ctx, r)
	if err != nil {
		return err
	}
	var ct string
	var data []byte
	switch {
	case req.Data != "":
		if ct, data, err = decodeImage(req.Data); err != nil {
			return err
		}
	case req.URL != "" || req.Name != "":
		ref := req.URL
		if req.Name != "" {
			ref = req.Name
		}
		if ct, data, err = s.fetchIcon(ctx, ref); err != nil {
			return err
		}
	default:
		return badRequest("give data, url or name")
	}
	if err := s.Store.SetImage(ctx, store.ImageService, st.Name+"/"+svc, ct, data); err != nil {
		return err
	}
	s.audit(r, rc, "stack.service.icon", st.Name+"/"+svc, true, ct)
	return ok(w, map[string]any{"ok": true, "content_type": ct})
}

func (s *Server) handleServiceIconDelete(w http.ResponseWriter, r *http.Request, rc *reqCtx) error {
	ctx := r.Context()
	st, svc, err := s.lookupService(ctx, r)
	if err != nil {
		return err
	}
	if err := s.Store.DeleteImage(ctx, store.ImageService, st.Name+"/"+svc); err != nil {
		return err
	}
	return ok(w, map[string]bool{"ok": true})
}

// fetchIcon downloads an icon from an https URL or a dashboard-icons name.
func (s *Server) fetchIcon(ctx context.Context, ref string) (string, []byte, error) {
	ref = strings.TrimSpace(ref)
	if s.Icons == nil {
		return "", nil, &Error{Status: http.StatusServiceUnavailable, Code: "unavailable", Message: "icon download is disabled"}
	}
	u := ref
	if !strings.Contains(ref, "://") {
		var err error
		if u, err = icons.SlugURL(ref); err != nil {
			return "", nil, fieldError("name", err.Error())
		}
	}
	fctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	ct, data, err := s.Icons.Fetch(fctx, u)
	if err != nil {
		return "", nil, &Error{Status: http.StatusBadGateway, Code: "icon_fetch", Message: err.Error()}
	}
	return ct, data, nil
}

// fetchServiceIcons gives an icon to the services of a stack that have none,
// in the background: the one named by their io.marineris.icon label, else
// the dashboard-icons icon named after their image ("jellyfin/jellyfin" →
// "jellyfin"). Failures are ignored: the UI shows initials instead.
func (s *Server) fetchServiceIcons(st *store.Stack) {
	if s.Icons == nil {
		return
	}
	defs, err := stacks.Services([]byte(st.Compose))
	if err != nil {
		return
	}
	s.bg.Add(1)
	go func() {
		defer s.bg.Done()
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
		defer cancel()
		have, err := s.Store.ImageVersions(ctx, store.ImageService)
		if err != nil {
			return
		}
		for _, d := range defs {
			key := st.Name + "/" + d.Name
			if _, ok := have[key]; ok {
				continue
			}
			ref := d.Icon()
			if ref == "" {
				ref = icons.SuggestSlug(d.Image)
			}
			if ref == "" {
				continue
			}
			ct, data, err := s.fetchIcon(ctx, ref)
			if err != nil {
				s.Log.Debug("no icon for service", "service", key, "icon", ref, "err", err)
				continue
			}
			if err := s.Store.SetImage(ctx, store.ImageService, key, ct, data); err != nil {
				s.Log.Warn("storing a service icon failed", "service", key, "err", err)
			}
		}
	}()
}

// handleStackRename renames a stack. A deployed stack is taken down and
// deployed again under its new name; its volumes keep their data (their
// current names are written into the file) and routes to its containers
// follow the new container names.
func (s *Server) handleStackRename(w http.ResponseWriter, r *http.Request, rc *reqCtx) error {
	if err := s.requireStacks(); err != nil {
		return err
	}
	var req struct {
		Name string `json:"name"`
	}
	if err := decode(r, &req); err != nil {
		return err
	}
	ctx := context.WithoutCancel(r.Context())
	st, err := s.Store.GetStack(ctx, r.PathValue("name"))
	if err != nil {
		return err
	}
	oldName, newName := st.Name, strings.TrimSpace(req.Name)
	if err := validate.StackName(newName); err != nil {
		return fieldError("name", err.Error())
	}
	if newName == oldName {
		return ok(w, map[string]string{"name": newName})
	}
	if _, err := s.Store.GetStack(ctx, newName); err == nil {
		return &Error{Status: http.StatusConflict, Code: "conflict", Message: "a stack named " + newName + " already exists"}
	}
	env, err := s.stackEnv(st)
	if err != nil {
		return err
	}
	cs := s.listContainers(ctx)
	var own []docker.Container
	for _, c := range cs {
		if c.Stack == oldName {
			own = append(own, c)
		}
	}
	deployed := len(own) > 0
	compose := []byte(st.Compose)
	if deployed {
		if compose, err = stacks.PinVolumeNames(compose, oldName); err != nil {
			return badRequest(err.Error())
		}
	}
	vs, err := s.Stacks.Check(ctx, newName, compose, []byte(env))
	if err != nil {
		return badRequest(err.Error())
	}
	if blocking := stacks.Blocking(vs, st.UnsafeAck); len(blocking) > 0 {
		return &stacks.PolicyError{Violations: blocking}
	}
	sealed, err := s.sealEnv(newName, env)
	if err != nil {
		return err
	}
	if deployed {
		if err := s.Stacks.Down(ctx, oldName, false); err != nil {
			return dockerErr(err)
		}
	}
	next := *st
	next.Name, next.Compose, next.Env = newName, string(compose), sealed
	if err := s.Store.RenameStack(ctx, oldName, &next); err != nil {
		return err
	}
	if err := s.Stacks.Remove(oldName); err != nil {
		s.Log.Warn("removing the old stack folder failed", "stack", oldName, "err", err)
	}
	s.audit(r, rc, "stack.rename", oldName, true, "renamed to "+newName)
	// Route icons linked to the stack or its services follow it.
	if links, err := s.Store.ListRoutes(ctx); err == nil {
		for _, rt := range links {
			if l := rt.Hub.IconLink; l == oldName || strings.HasPrefix(l, oldName+"/") {
				if err := s.Store.SetRouteIconLink(ctx, rt.ID, newName+strings.TrimPrefix(l, oldName)); err != nil {
					return err
				}
			}
		}
	}
	if !deployed {
		return ok(w, map[string]string{"name": newName})
	}

	if err := s.Stacks.Deploy(ctx, newName, compose, []byte(env), next.UnsafeAck); err != nil {
		s.audit(r, rc, "stack.deploy", newName, false, err.Error())
		return dockerErr(err)
	}
	if err := s.Store.MarkStackDeployed(ctx, newName); err != nil {
		return err
	}
	s.fetchServiceIcons(&next)
	// Containers without a fixed container_name are called <stack>-<service>-<n>.
	renamed := map[string]string{}
	for _, c := range own {
		if rest, ok := strings.CutPrefix(c.Name, oldName+"-"); ok {
			renamed[c.Name] = newName + "-" + rest
		}
	}
	routes, err := s.Store.ListRoutes(ctx)
	if err != nil {
		return err
	}
	changed := false
	for _, rt := range routes {
		to, ok := renamed[rt.Target.Container]
		if rt.Kind != store.RouteContainer || !ok {
			continue
		}
		rt.Target.Container = to
		if err := s.Store.UpdateRoute(ctx, rt); err != nil {
			return err
		}
		changed = true
	}
	for _, c := range own {
		if to, ok := renamed[c.Name]; ok && c.Routable && s.Docker != nil {
			if err := s.Docker.ConnectToApps(ctx, to); err != nil {
				// Logged by its previous name (from Docker); the new one is
				// derived from the request.
				s.Log.Warn("reattaching a renamed container to the apps network failed", "previous_name", c.Name, "err", err)
			}
		}
	}
	if changed {
		if err := s.commit(ctx, rc, "stack "+oldName+" renamed to "+newName, affectsCaddy, nil); err != nil {
			return err
		}
	}
	return ok(w, map[string]string{"name": newName})
}

// handleStackStart starts the stopped containers of a stack.
func (s *Server) handleStackStart(w http.ResponseWriter, r *http.Request, rc *reqCtx) error {
	if err := s.requireDocker(); err != nil {
		return err
	}
	ctx := context.WithoutCancel(r.Context())
	st, err := s.Store.GetStack(ctx, r.PathValue("name"))
	if err != nil {
		return err
	}
	cs, err := s.Docker.List(ctx)
	if err != nil {
		return dockerErr(err)
	}
	n := 0
	for _, c := range cs {
		if c.Stack == st.Name && c.State != "running" {
			n++
			if err := s.Docker.Action(ctx, c.Name, "start"); err != nil {
				s.audit(r, rc, "stack.start", st.Name, false, err.Error())
				return dockerErr(err)
			}
		}
	}
	s.audit(r, rc, "stack.start", st.Name, true, strconv.Itoa(n)+" containers started")
	return ok(w, map[string]bool{"ok": true})
}

// stackIcon is an icon a route can reuse: a stack's (its main service's) or
// a service's own.
type stackIcon struct {
	Stack   string `json:"stack"`
	Service string `json:"service,omitempty"`
	Icon    string `json:"icon"`
}

// handleStackIcons lists the icons a route can link to: those of the
// services, and of the stacks (their main service's).
func (s *Server) handleStackIcons(w http.ResponseWriter, r *http.Request, rc *reqCtx) error {
	ctx := r.Context()
	list, err := s.Store.ListStacks(ctx)
	if err != nil {
		return err
	}
	cs := s.listContainers(ctx)
	out := []stackIcon{}
	for _, st := range list {
		sum, err := s.stackServices(ctx, st, cs)
		if err != nil {
			continue
		}
		for _, v := range sum.Services {
			if v.IconSource != "custom" {
				continue
			}
			if v.Main {
				out = append(out, stackIcon{Stack: st.Name, Icon: v.Icon})
			}
			out = append(out, stackIcon{Stack: st.Name, Service: v.Name, Icon: v.Icon})
		}
	}
	return ok(w, out)
}

// MoveRouteIconsToServices runs once (Marineris 0.1.3): before, a route kept
// its own copy of its app's icon and a service without an icon showed its
// route's. Now services own their icons and routes follow them, so the icon
// of a route to a stack's container moves to that service when the service
// has none, and the route follows it. Routes whose service already has an
// icon keep theirs (a deliberate, different icon). It needs Docker to know
// which service a container belongs to: when Docker is not reachable yet it
// does nothing and is tried again later.
func (s *Server) MoveRouteIconsToServices(ctx context.Context) (bool, error) {
	const task = "route-icons-to-services"
	if done, err := s.Store.Done(ctx, task); err != nil || done {
		return done, err
	}
	if s.Docker == nil {
		return false, nil
	}
	cs, err := s.Docker.List(ctx)
	if err != nil {
		return false, nil //nolint:nilerr // Docker not reachable yet: try later
	}
	byName := map[string]docker.Container{}
	for _, c := range cs {
		byName[c.Name] = c
	}
	have, err := s.Store.ImageVersions(ctx, store.ImageService)
	if err != nil {
		return false, err
	}
	routes, err := s.Store.ListRoutes(ctx)
	if err != nil {
		return false, err
	}
	for _, rt := range routes {
		c, ok := byName[rt.Target.Container]
		if rt.Kind != store.RouteContainer || !rt.Hub.HasIcon || !ok || c.Stack == "" || c.Service == "" {
			continue
		}
		key := c.Stack + "/" + c.Service
		if _, hasIcon := have[key]; hasIcon {
			continue
		}
		if _, err := s.Store.GetStack(ctx, c.Stack); err != nil {
			continue // not a stack managed by Marineris
		}
		ic, err := s.Store.GetRouteIcon(ctx, rt.ID)
		if err != nil {
			return false, err
		}
		if err := s.Store.SetImage(ctx, store.ImageService, key, ic.ContentType, ic.Data); err != nil {
			return false, err
		}
		if err := s.Store.DeleteRouteIcon(ctx, rt.ID); err != nil {
			return false, err
		}
		if err := s.Store.SetRouteIconLink(ctx, rt.ID, ""); err != nil {
			return false, err
		}
		have[key] = time.Now()
		s.Log.Info("route icon moved to its service", "route", rt.Name, "service", key)
	}
	return true, s.Store.MarkDone(ctx, task)
}
