// Command fakeapi runs internal/fakes/api as a standalone process on a
// random localhost port, for the dashboard's Playwright suite to spawn
// (docs/workstreams/08-dashboard.md §7: "Playwright end to end against
// internal/fakes/api (Go, run as a test fixture on a port)"). A second,
// separate listener exposes the fake's error switch over HTTP, since
// internal/fakes/api.Fake.Fail/FailNext/Unfail are Go-only methods and a
// Playwright spec has no other way to call them.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/heracraft/repose/internal/fakes/api"
)

func main() {
	billing := flag.Bool("billing", false, "start with billing on and the account active on Solo, instead of billing off (503 billing_disabled, an exempt account)")
	cliLatest := flag.String("cli-latest", "", "send this as the newest CLI release (X-Repose-CLI-Latest) on every answer")
	flag.Parse()

	f := api.New(api.Options{Billing: *billing, CLILatest: *cliLatest})
	defer f.Close()

	admin, err := newAdminServer(f)
	if err != nil {
		fmt.Fprintln(os.Stderr, "fakeapi:", err)
		os.Exit(1)
	}
	defer func() { _ = admin.Close() }()

	// The two lines of stdout this process ever prints, so a harness's
	// line-reader never has to guess which line is which.
	fmt.Printf("FAKEAPI_URL=%s/v1\n", f.URL())
	fmt.Printf("FAKEAPI_ADMIN_URL=%s\n", admin.URL)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	<-ctx.Done()
}

type adminServer struct {
	*http.Server
	URL string
}

func (a *adminServer) Close() error { return a.Server.Close() }

// newAdminServer starts the switch on 127.0.0.1:0. Routes:
//
//	POST /fail       {"method","path","code"} -> f.Fail
//	POST /fail-next  {"method","path","code"} -> f.FailNext
//	POST /unfail     {"method","path"}        -> f.Unfail
//	POST /billing    api.BillingState as JSON -> f.SetBillingState: {"mode": off|none|trial|active|past_due|suspended|exempt,
//	                 "plan", "scheduled_plan", "cancelled", "seats": {total, held, waiting},
//	                 "waitlist": {position, invited, hold_hours}, "egress_gb", "invoices"}; every field optional
//	GET  /polar/checkout/{id} -> f.CompleteCheckout, then 303 to the dashboard's /billing?checkout=done
//	                 (Polar's hosted checkout, paid, and its webhook; every checkout's URL points here)
//	POST /question   {"project_id","agent","text","options","timeout_s"} -> f.AddQuestion
//	POST /question/close {"id","reason"} -> f.CloseQuestion: the guest closes a question
//	                 (I-690; reason terminal|withdrawn|interrupted|asker_gone)
//	POST /events     {"project_id","count","kinds","agents","windows","summaries","spacing_minutes"}: count events
//	                 spacing_minutes apart (default 1) ending now, oldest first. kinds, agents, windows and
//	                 summaries are cycled when given; by default every event is an agent_message with no
//	                 agent and the summaries are e000.., oldest first (I-414, I-662). The project's creation
//	                 moves to before the first of them (f.BackdateCreation), so it stays the oldest event
//	POST /agents     {"project_id","agents": [{agent, window, state}],"ssh_sessions","tmux_clients"}
//	                 -> f.SetAgents, and f.SetSessions when either count is given
//
// Every answer carries permissive CORS headers, so a Playwright test can
// call the switch from the page itself.
func newAdminServer(f *api.Fake) (*adminServer, error) {
	type req struct{ Method, Path, Code string }
	decode := func(w http.ResponseWriter, r *http.Request) (req, bool) {
		var body req
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return req{}, false
		}
		return body, true
	}
	mux := http.NewServeMux()
	mux.HandleFunc("POST /fail", func(w http.ResponseWriter, r *http.Request) {
		if body, ok := decode(w, r); ok {
			f.Fail(body.Method+" "+body.Path, body.Code)
		}
	})
	mux.HandleFunc("POST /billing", func(w http.ResponseWriter, r *http.Request) {
		var body api.BillingState
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		if err := f.SetBillingState(body); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
		}
	})
	// {project_id, minutes}: the project is frozen since that many minutes
	// ago; minutes < 0 clears it (the mock of DECISIONS I-683).
	mux.HandleFunc("POST /resting", func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			ProjectID string `json:"project_id"`
			Minutes   int    `json:"minutes"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		var since *time.Time
		if body.Minutes >= 0 {
			t := time.Now().Add(-time.Duration(body.Minutes) * time.Minute)
			since = &t
		}
		if err := f.SetResting(body.ProjectID, since); err != nil {
			http.Error(w, err.Error(), http.StatusNotFound)
		}
	})
	mux.HandleFunc("GET /polar/checkout/{id}", func(w http.ResponseWriter, r *http.Request) {
		back, err := f.CompleteCheckout(r.PathValue("id"))
		if err != nil {
			http.Error(w, err.Error(), http.StatusNotFound)
			return
		}
		if back == "" {
			_, _ = fmt.Fprintln(w, "checkout complete")
			return
		}
		http.Redirect(w, r, back, http.StatusSeeOther)
	})
	mux.HandleFunc("POST /question", func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			ProjectID string   `json:"project_id"`
			Agent     string   `json:"agent"`
			Text      string   `json:"text"`
			Options   []string `json:"options"`
			TimeoutS  int      `json:"timeout_s"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		q, err := f.AddQuestion(body.ProjectID, body.Agent, body.Text, body.Options, time.Duration(body.TimeoutS)*time.Second)
		if err != nil {
			http.Error(w, err.Error(), http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(q)
	})
	mux.HandleFunc("POST /question/close", func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			ID     string `json:"id"`
			Reason string `json:"reason"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		if err := f.CloseQuestion(body.ID, body.Reason); err != nil {
			http.Error(w, err.Error(), http.StatusConflict)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	})
	// count events on a project, spacing_minutes apart ending now, oldest
	// first; the lists are cycled (see the routes above).
	mux.HandleFunc("POST /events", func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			ProjectID      string   `json:"project_id"`
			Count          int      `json:"count"`
			Kinds          []string `json:"kinds"`
			Agents         []string `json:"agents"`
			Windows        []string `json:"windows"`
			Summaries      []string `json:"summaries"`
			SpacingMinutes int      `json:"spacing_minutes"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		spacing := time.Duration(max(body.SpacingMinutes, 1)) * time.Minute
		pick := func(list []string, i int, def string) string {
			if len(list) == 0 {
				return def
			}
			return list[i%len(list)]
		}
		now := time.Now()
		f.BackdateCreation(body.ProjectID, now.Add(time.Duration(-body.Count-1)*spacing).Truncate(time.Second))
		for i := 0; i < body.Count; i++ {
			f.AddEventAs(body.ProjectID, now.Add(time.Duration(i-body.Count)*spacing),
				pick(body.Kinds, i, "agent_message"), pick(body.Agents, i, ""), pick(body.Windows, i, ""),
				pick(body.Summaries, i, fmt.Sprintf("e%03d", i)))
		}
		w.WriteHeader(http.StatusNoContent)
	})
	mux.HandleFunc("POST /agents", func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			ProjectID   string            `json:"project_id"`
			Agents      []api.AgentSignal `json:"agents"`
			SSHSessions *int              `json:"ssh_sessions"`
			TmuxClients *int              `json:"tmux_clients"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		if body.Agents == nil {
			body.Agents = []api.AgentSignal{}
		}
		f.SetAgents(body.ProjectID, body.Agents)
		if body.SSHSessions != nil || body.TmuxClients != nil {
			f.SetSessions(body.ProjectID, deref(body.SSHSessions), deref(body.TmuxClients))
		}
		w.WriteHeader(http.StatusNoContent)
	})
	mux.HandleFunc("POST /fail-next", func(w http.ResponseWriter, r *http.Request) {
		if body, ok := decode(w, r); ok {
			f.FailNext(body.Method, body.Path, body.Code)
		}
	})
	mux.HandleFunc("POST /unfail", func(w http.ResponseWriter, r *http.Request) {
		if body, ok := decode(w, r); ok {
			f.Unfail(body.Method + " " + body.Path)
		}
	})
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, fmt.Errorf("fakeapi: admin listener: %w", err)
	}
	cors := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type")
		w.Header().Set("Access-Control-Expose-Headers", "Repose-Event-Count, X-Request-Id")
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		mux.ServeHTTP(w, r)
	})
	srv := &http.Server{Handler: cors, ReadHeaderTimeout: 5 * time.Second}
	go func() { _ = srv.Serve(ln) }()
	url := "http://" + ln.Addr().String()
	f.SetCheckoutBase(url + "/polar/checkout/")
	return &adminServer{Server: srv, URL: url}, nil
}

func deref(p *int) int {
	if p == nil {
		return 0
	}
	return *p
}
