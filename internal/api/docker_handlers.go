package api

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/photonring/marineris-server/internal/stacks"
	"github.com/photonring/marineris-server/internal/store"
	"github.com/photonring/marineris-server/internal/validate"
)

func containerRef(r *http.Request) (string, error) {
	ref := r.PathValue("id")
	if err := validate.ContainerName(ref); err != nil {
		return "", badRequest("invalid container reference")
	}
	return ref, nil
}

func (s *Server) handleContainerList(w http.ResponseWriter, r *http.Request, rc *reqCtx) error {
	if err := s.requireDocker(); err != nil {
		return err
	}
	list, err := s.Docker.List(r.Context())
	if err != nil {
		return dockerErr(err)
	}
	return ok(w, list)
}

func (s *Server) handleContainerGet(w http.ResponseWriter, r *http.Request, rc *reqCtx) error {
	if err := s.requireDocker(); err != nil {
		return err
	}
	ref, err := containerRef(r)
	if err != nil {
		return err
	}
	d, err := s.Docker.Inspect(r.Context(), ref)
	if err != nil {
		return dockerErr(err)
	}
	return ok(w, d)
}

func (s *Server) handleContainerLogs(w http.ResponseWriter, r *http.Request, rc *reqCtx) error {
	if err := s.requireDocker(); err != nil {
		return err
	}
	ref, err := containerRef(r)
	if err != nil {
		return err
	}
	tail, _ := strconv.Atoi(r.URL.Query().Get("tail"))
	logs, err := s.Docker.Logs(r.Context(), ref, tail)
	if err != nil {
		return dockerErr(err)
	}
	return ok(w, map[string]string{"logs": logs})
}

func (s *Server) handleContainerAction(action string) func(http.ResponseWriter, *http.Request, *reqCtx) error {
	return func(w http.ResponseWriter, r *http.Request, rc *reqCtx) error {
		if err := s.requireDocker(); err != nil {
			return err
		}
		ref, err := containerRef(r)
		if err != nil {
			return err
		}
		err = s.Docker.Action(r.Context(), ref, action)
		s.audit(r, rc, "container."+action, ref, err == nil, "")
		if err != nil {
			return dockerErr(err)
		}
		return ok(w, map[string]bool{"ok": true})
	}
}

func (s *Server) handleContainerRemove(w http.ResponseWriter, r *http.Request, rc *reqCtx) error {
	if err := s.requireDocker(); err != nil {
		return err
	}
	ref, err := containerRef(r)
	if err != nil {
		return err
	}
	err = s.Docker.Remove(r.Context(), ref)
	s.audit(r, rc, "container.remove", ref, err == nil, "")
	if err != nil {
		return dockerErr(err)
	}
	return ok(w, map[string]bool{"ok": true})
}

// dockerErr turns Docker API errors into user-facing errors. Messages from
// the Docker daemon (and the socket proxy) are shown: they carry no secrets
// and are needed to understand failures.
func dockerErr(err error) error {
	var ae *Error
	if errors.As(err, &ae) {
		return err
	}
	msg := err.Error()
	switch {
	case strings.Contains(msg, "No such container") || strings.Contains(msg, "not found"):
		return &Error{Status: http.StatusNotFound, Code: "not_found", Message: msg}
	case strings.Contains(msg, "protected") || strings.Contains(msg, "system container"):
		return err
	}
	return &Error{Status: http.StatusBadGateway, Code: "docker", Message: msg}
}

// --- Stacks ----------------------------------------------------------------

type stackView struct {
	*store.Stack
	Env string `json:"env"`
}

func (s *Server) stackEnv(st *store.Stack) (string, error) {
	if st.Env == "" {
		return "", nil
	}
	b, err := s.Box.Open(st.Env, "stack-env:"+st.Name)
	return string(b), err
}

func (s *Server) sealEnv(name, env string) (string, error) {
	if env == "" {
		return "", nil
	}
	return s.Box.Seal([]byte(env), "stack-env:"+name)
}

func (s *Server) handleStackList(w http.ResponseWriter, r *http.Request, rc *reqCtx) error {
	list, err := s.Store.ListStacks(r.Context())
	if err != nil {
		return err
	}
	out := []map[string]any{}
	cs := s.listContainers(r.Context())
	running := map[string]int{}
	total := map[string]int{}
	for _, c := range cs {
		if c.Stack != "" {
			total[c.Stack]++
			if c.State == "running" {
				running[c.Stack]++
			}
		}
	}
	for _, st := range list {
		item := map[string]any{
			"name": st.Name, "created_at": st.CreatedAt, "updated_at": st.UpdatedAt, "deployed_at": st.DeployedAt,
			"unsafe_ack": st.UnsafeAck, "containers": total[st.Name], "running": running[st.Name],
		}
		// A stack whose file cannot be read is still listed, without details.
		if sum, err := s.stackServices(r.Context(), st, cs); err == nil {
			item["state"], item["main"], item["icon"], item["services"] = sum.State, sum.Main, sum.Icon, len(sum.Services)
		} else {
			var ae *Error
			if !errors.As(err, &ae) {
				return err
			}
		}
		out = append(out, item)
	}
	return ok(w, out)
}

type stackRequest struct {
	Name      string `json:"name"`
	Compose   string `json:"compose"`
	Env       string `json:"env"`
	UnsafeAck bool   `json:"unsafe_ack"`
}

func (s *Server) requireStacks() error {
	if s.Stacks == nil {
		return &Error{Status: http.StatusServiceUnavailable, Code: "docker_unavailable", Message: "stack management is not available"}
	}
	return nil
}

// checkUnsafe requires a step-up before a stack may opt into unsafe settings.
func (s *Server) checkUnsafe(rc *reqCtx, req *stackRequest, prev *store.Stack) error {
	if req.UnsafeAck && (prev == nil || !prev.UnsafeAck) && !s.Sessions.RecentlyAuthenticated(rc.session, StepUpWindow) {
		return &Error{Status: http.StatusForbidden, Code: "reauth_required", Message: "confirm your identity to allow unsafe settings"}
	}
	return nil
}

func (s *Server) handleStackCheck(w http.ResponseWriter, r *http.Request, rc *reqCtx) error {
	if err := s.requireStacks(); err != nil {
		return err
	}
	var req stackRequest
	if err := decode(r, &req); err != nil {
		return err
	}
	if req.Name == "" {
		req.Name = "check"
	}
	vs, err := s.Stacks.Check(r.Context(), req.Name, []byte(req.Compose), []byte(req.Env))
	if err != nil {
		return badRequest(err.Error())
	}
	return ok(w, map[string]any{"violations": vs, "blocking": nonNilV(stacks.Blocking(vs, req.UnsafeAck))})
}

func nonNilV(v []stacks.Violation) []stacks.Violation {
	if v == nil {
		return []stacks.Violation{}
	}
	return v
}

func (s *Server) handleStackCreate(w http.ResponseWriter, r *http.Request, rc *reqCtx) error {
	if err := s.requireStacks(); err != nil {
		return err
	}
	var req stackRequest
	if err := decode(r, &req); err != nil {
		return err
	}
	if err := validate.StackName(req.Name); err != nil {
		return fieldError("name", err.Error())
	}
	if err := s.checkUnsafe(rc, &req, nil); err != nil {
		return err
	}
	ctx := r.Context()
	if _, err := s.Stacks.Check(ctx, req.Name, []byte(req.Compose), []byte(req.Env)); err != nil {
		return badRequest(err.Error())
	}
	env, err := s.sealEnv(req.Name, req.Env)
	if err != nil {
		return err
	}
	st := &store.Stack{Name: req.Name, Compose: req.Compose, Env: env, UnsafeAck: req.UnsafeAck}
	if err := s.Store.CreateStack(ctx, st); err != nil {
		return err
	}
	s.audit(r, rc, "stack.create", st.Name, true, "")
	return ok(w, stackView{Stack: st, Env: req.Env})
}

func (s *Server) handleStackGet(w http.ResponseWriter, r *http.Request, rc *reqCtx) error {
	st, err := s.Store.GetStack(r.Context(), r.PathValue("name"))
	if err != nil {
		return err
	}
	env, err := s.stackEnv(st)
	if err != nil {
		return err
	}
	return ok(w, stackView{Stack: st, Env: env})
}

func (s *Server) handleStackUpdate(w http.ResponseWriter, r *http.Request, rc *reqCtx) error {
	if err := s.requireStacks(); err != nil {
		return err
	}
	var req stackRequest
	if err := decode(r, &req); err != nil {
		return err
	}
	ctx := r.Context()
	st, err := s.Store.GetStack(ctx, r.PathValue("name"))
	if err != nil {
		return err
	}
	if err := s.checkUnsafe(rc, &req, st); err != nil {
		return err
	}
	if _, err := s.Stacks.Check(ctx, st.Name, []byte(req.Compose), []byte(req.Env)); err != nil {
		return badRequest(err.Error())
	}
	env, err := s.sealEnv(st.Name, req.Env)
	if err != nil {
		return err
	}
	st.Compose, st.Env, st.UnsafeAck = req.Compose, env, req.UnsafeAck
	if err := s.Store.UpdateStack(ctx, st); err != nil {
		return err
	}
	s.audit(r, rc, "stack.update", st.Name, true, "")
	return ok(w, stackView{Stack: st, Env: req.Env})
}

func (s *Server) handleStackDeploy(w http.ResponseWriter, r *http.Request, rc *reqCtx) error {
	if err := s.requireStacks(); err != nil {
		return err
	}
	ctx := context.WithoutCancel(r.Context())
	st, err := s.Store.GetStack(ctx, r.PathValue("name"))
	if err != nil {
		return err
	}
	env, err := s.stackEnv(st)
	if err != nil {
		return err
	}
	err = s.Stacks.Deploy(ctx, st.Name, []byte(st.Compose), []byte(env), st.UnsafeAck)
	s.audit(r, rc, "stack.deploy", st.Name, err == nil, errString(err))
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
	return ok(w, map[string]bool{"ok": true})
}

func (s *Server) handleStackDown(w http.ResponseWriter, r *http.Request, rc *reqCtx) error {
	if err := s.requireStacks(); err != nil {
		return err
	}
	name := r.PathValue("name")
	if _, err := s.Store.GetStack(r.Context(), name); err != nil {
		return err
	}
	err := s.Stacks.Down(context.WithoutCancel(r.Context()), name, false)
	s.audit(r, rc, "stack.down", name, err == nil, errString(err))
	if err != nil {
		return dockerErr(err)
	}
	return ok(w, map[string]bool{"ok": true})
}

func (s *Server) handleStackDelete(w http.ResponseWriter, r *http.Request, rc *reqCtx) error {
	if err := s.requireStacks(); err != nil {
		return err
	}
	name := r.PathValue("name")
	ctx := context.WithoutCancel(r.Context())
	if _, err := s.Store.GetStack(ctx, name); err != nil {
		return err
	}
	removeVolumes := r.URL.Query().Get("volumes") == "true"
	if err := s.Stacks.Down(ctx, name, removeVolumes); err != nil {
		return dockerErr(err)
	}
	if err := s.Stacks.Remove(name); err != nil {
		return err
	}
	if err := s.Store.DeleteStack(ctx, name); err != nil {
		return err
	}
	if err := s.Store.DeleteImagesWithPrefix(ctx, store.ImageService, name+"/"); err != nil {
		return err
	}
	s.audit(r, rc, "stack.delete", name, true, "volumes removed: "+strconv.FormatBool(removeVolumes))
	return ok(w, map[string]bool{"ok": true})
}

// --- Volumes & networks ----------------------------------------------------

func (s *Server) handleVolumeList(w http.ResponseWriter, r *http.Request, rc *reqCtx) error {
	if err := s.requireDocker(); err != nil {
		return err
	}
	v, err := s.Docker.Volumes(r.Context())
	if err != nil {
		return dockerErr(err)
	}
	return ok(w, v)
}

func (s *Server) handleVolumeDelete(w http.ResponseWriter, r *http.Request, rc *reqCtx) error {
	if err := s.requireDocker(); err != nil {
		return err
	}
	name := r.PathValue("name")
	if err := validate.ContainerName(name); err != nil {
		return badRequest("invalid volume name")
	}
	err := s.Docker.RemoveVolume(r.Context(), name)
	s.audit(r, rc, "volume.remove", name, err == nil, errString(err))
	if err != nil {
		return dockerErr(err)
	}
	return ok(w, map[string]bool{"ok": true})
}

func (s *Server) handleNetworkList(w http.ResponseWriter, r *http.Request, rc *reqCtx) error {
	if err := s.requireDocker(); err != nil {
		return err
	}
	n, err := s.Docker.Networks(r.Context())
	if err != nil {
		return dockerErr(err)
	}
	return ok(w, n)
}

func errString(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}
