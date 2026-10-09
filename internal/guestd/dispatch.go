package guestd

import (
	"context"
	"errors"
	"time"

	guestdv1 "github.com/heracraft/repose/internal/gen/guestd/v1"
	"github.com/heracraft/repose/internal/guestd/exec"
	"github.com/heracraft/repose/internal/guestd/questions"
	"github.com/heracraft/repose/internal/guestd/sysdep"
	"github.com/heracraft/repose/internal/guestd/system"
	"github.com/heracraft/repose/internal/vsockrpc"
)

// WindowSaveTimeout bounds repose-tmux-save before the poweroff.
const WindowSaveTimeout = 5 * time.Second

// ShutdownFlush is how long the poweroff waits after the response is written,
// so hostd sees the answer before the guest goes away.
const ShutdownFlush = 500 * time.Millisecond

// dispatch runs one request in its own goroutine under its own deadline and
// writes the response. Every request named in
// docs/interfaces/vsock-guestd.md has a case here.
func (s *Server) dispatch(ctx context.Context, conn *vsockrpc.Conn, id string, req *guestdv1.Request) {
	ctx, cancel := context.WithTimeout(ctx, s.deadlineFor(req))
	defer cancel()

	resp := s.handle(ctx, req)
	env := &guestdv1.Envelope{RequestId: id, Body: &guestdv1.Envelope_Response{Response: resp}}
	if err := conn.Send(env); err != nil {
		s.log.Warn("could not write a response", "event", "ready", "reason", "write_error")
	}
}

// deadlineFor is the per-request-family deadline of
// docs/workstreams/04-guestd.md §5.
func (s *Server) deadlineFor(req *guestdv1.Request) time.Duration {
	switch r := req.GetReq().(type) {
	case *guestdv1.Request_Switch:
		d := s.cfg.SwitchTimeout
		if d <= 0 {
			d = system.DefaultTimeout
		}
		return d + time.Minute
	case *guestdv1.Request_Exec:
		d := time.Duration(r.Exec.GetTimeoutS()) * time.Second
		if d <= 0 {
			d = exec.DefaultTimeout
		}
		if d > exec.MaxTimeout {
			d = exec.MaxTimeout
		}
		return d + 10*time.Second
	default:
		return DefaultDeadline
	}
}

func (s *Server) handle(ctx context.Context, req *guestdv1.Request) *guestdv1.Response {
	switch r := req.GetReq().(type) {
	case *guestdv1.Request_Ping:
		return ok(&guestdv1.Response_Ping{Ping: &guestdv1.PingResult{
			Version: ProtocolVersion,
			UptimeS: s.uptimeSeconds(),
			BootId:  s.bootID,
		}})

	case *guestdv1.Request_Freeze:
		return empty(s.freeze.Freeze())

	case *guestdv1.Request_Thaw:
		return empty(s.freeze.Thaw())

	case *guestdv1.Request_Switch:
		res, err := s.system.Switch(ctx, r.Switch.GetSystemClosure(), r.Switch.GetForceReboot(), r.Switch.GetRegistration())
		if err != nil {
			// The output is returned even on failure: it is the only thing
			// that tells the user why their config did not apply.
			if res != nil {
				return withResult(err, &guestdv1.Response_Switch{Switch: res})
			}
			return fail(err)
		}
		return ok(&guestdv1.Response_Switch{Switch: res})

	case *guestdv1.Request_GrowFs:
		res, err := s.fs.Grow(ctx)
		if err != nil {
			return fail(err)
		}
		return ok(&guestdv1.Response_GrowFs{GrowFs: res})

	case *guestdv1.Request_WriteSecrets:
		return empty(s.secrets.Write(ctx, r.WriteSecrets.GetSecrets()))

	case *guestdv1.Request_SetPrincipals:
		return empty(s.ssh.Set(ctx, r.SetPrincipals.GetPrincipals()))

	case *guestdv1.Request_SetupProject:
		return empty(s.project.Setup(ctx, r.SetupProject))

	case *guestdv1.Request_Sample:
		res, err := s.sampler.Sample(ctx)
		if err != nil {
			return fail(err)
		}
		return ok(&guestdv1.Response_Sample{Sample: res})

	case *guestdv1.Request_Exec:
		res, err := s.exec.Exec(ctx, r.Exec)
		if err != nil {
			if res != nil {
				return withResult(err, &guestdv1.Response_Exec{Exec: res})
			}
			return fail(err)
		}
		return ok(&guestdv1.Response_Exec{Exec: res})

	case *guestdv1.Request_Shutdown:
		return empty(s.shutdown(ctx, r.Shutdown.GetTimeoutS()))

	case *guestdv1.Request_RegisterPaths:
		return empty(s.system.RegisterPaths(ctx, r.RegisterPaths.GetRegistration()))

	case *guestdv1.Request_AnswerQuestion:
		return empty(s.answerQuestion(r.AnswerQuestion))

	default:
		// An unknown request from a newer hostd. Saying so by code is what
		// lets hostd degrade per request (docs/workstreams/04-guestd.md §8).
		return fail(sysdep.Invalid("guestd protocol version %s does not know this request", ProtocolVersion))
	}
}

// answerQuestion closes a question a repose-ask is waiting on. The answer
// is tenant text; the log line carries the id and the status only.
func (s *Server) answerQuestion(a *guestdv1.AnswerQuestion) error {
	err := s.asks.Answer(a.GetQuestionId(), a.GetStatus(), a.GetAnswer())
	switch {
	case errors.Is(err, questions.ErrNotFound):
		return sysdep.NotFound("no question %s in this guest", a.GetQuestionId())
	case errors.Is(err, questions.ErrInvalid):
		return sysdep.Invalid("%v", err)
	case err != nil:
		return err
	}
	s.log.Info("agent question closed", "event", "agent_question", "question_id", a.GetQuestionId(), "status", a.GetStatus())
	return nil
}

// shutdown powers the guest off after the response has been flushed.
func (s *Server) shutdown(ctx context.Context, timeoutS uint32) error {
	timeout := time.Duration(timeoutS) * time.Second
	if timeout <= 0 {
		timeout = 30 * time.Second
	}
	if err := s.freeze.Close(); err != nil {
		return err
	}
	s.log.Info("shutting down", "event", "shutdown", "timeout_ms", timeout.Milliseconds())

	runner := s.cfg.Runner
	if runner == nil {
		runner = sysdep.ExecRunner{}
	}
	go func() {
		time.Sleep(ShutdownFlush)
		// The tmux session's agent windows, recorded while every agent
		// still runs, come back at the next start (DECISIONS I-636). A
		// base without the script, or a herdr project, records nothing.
		saveCtx, cancelSave := context.WithTimeout(context.Background(), WindowSaveTimeout)
		if res, err := runner.Run(saveCtx, sysdep.RunSpec{
			Argv:      []string{"repose-tmux-save"},
			User:      "dev",
			Env:       sysdep.DevEnv(s.paths, "dev"),
			MaxOutput: 4 << 10,
		}); err != nil || res.ExitCode != 0 {
			s.log.Warn("agent windows not recorded", "event", "shutdown")
		}
		cancelSave()
		poweroffCtx, cancel := context.WithTimeout(context.Background(), timeout)
		defer cancel()
		if _, err := runner.Run(poweroffCtx, sysdep.RunSpec{
			Argv:      []string{"systemctl", "poweroff"},
			MaxOutput: 4 << 10,
			Env:       sysdep.DevEnv(s.paths, "root"),
		}); err != nil {
			s.log.Error("poweroff failed", "event", "shutdown")
		}
	}()
	_ = ctx
	return nil
}

func ok(result any) *guestdv1.Response {
	resp := &guestdv1.Response{Ok: true}
	assign(resp, result)
	return resp
}

func empty(err error) *guestdv1.Response {
	if err != nil {
		return fail(err)
	}
	return &guestdv1.Response{Ok: true}
}

func fail(err error) *guestdv1.Response {
	return &guestdv1.Response{
		Ok:    false,
		Error: &guestdv1.Error{Code: sysdep.CodeOf(err), Message: err.Error()},
	}
}

func withResult(err error, result any) *guestdv1.Response {
	resp := fail(err)
	assign(resp, result)
	return resp
}

func assign(resp *guestdv1.Response, result any) {
	switch r := result.(type) {
	case *guestdv1.Response_Ping:
		resp.Result = r
	case *guestdv1.Response_Switch:
		resp.Result = r
	case *guestdv1.Response_GrowFs:
		resp.Result = r
	case *guestdv1.Response_Sample:
		resp.Result = r
	case *guestdv1.Response_Exec:
		resp.Result = r
	}
}
