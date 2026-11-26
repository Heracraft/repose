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
	"io"
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
	cliOldest := flag.String("cli-oldest", "", "send this as the oldest CLI release answered as documented (X-Repose-CLI-Oldest) on every answer")
	configDelay := flag.Duration("config-delay", 0, "keep a PUT /config op running this long, streaming its log line by line (0: done at first read)")
	defaultRuntime := flag.String("default-runtime", "nix", "the guest runtime a new machine gets, nix or erofs (settings.default_guest_runtime)")
	gatewayHost := flag.String("gateway-host", "", "the gateway POST /certs names (default 127.0.0.1, at a port nothing listens on)")
	listen := flag.String("listen", "", "the address the api listens on (default 127.0.0.1 on a random port)")
	gatewayPort := flag.Int("gateway-port", 0, "the gateway port POST /certs names (default api.FakeGatewayPort)")
	flag.Parse()
	if *defaultRuntime != "nix" && *defaultRuntime != "erofs" {
		fmt.Fprintln(os.Stderr, "fakeapi: -default-runtime is nix or erofs")
		os.Exit(2)
	}

	f := api.New(api.Options{Listen: *listen, Billing: *billing, CLILatest: *cliLatest, CLIOldest: *cliOldest, ConfigDelay: *configDelay,
		GatewayHost: *gatewayHost, GatewayPort: *gatewayPort})
	f.SetDefaultGuestRuntime(*defaultRuntime)
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
//	POST /not-answering {"project_id","minutes"} -> f.SetNotAnswering: guestd silent that long (I-1652)
//	POST /refuse-create {"message"}           -> f.RefuseNextCreate: the host refuses the next create
//	                 for room and the project is gone (I-1648)
//	POST /billing    api.BillingState as JSON -> f.SetBillingState: {"mode": off|none|trial|active|past_due|suspended|exempt,
//	                 "plan", "scheduled_plan", "cancelled", "seats": {total, held, waiting},
//	                 "waitlist": {position, invited, hold_hours}, "egress_gb", "invoices",
//	                 "own_for_sale" (the Own hardware plan is sold here, I-1364)}; every field optional
//	GET  /polar/checkout/{id} -> f.CompleteCheckout, then 303 to the dashboard's /billing?checkout=done
//	                 (Polar's hosted checkout, paid, and its webhook; every checkout's URL points here)
//	POST /question   {"project_id","agent","text","options","timeout_s","secret","link","link_label"} -> f.AddQuestion,
//	                 or f.AddSecretQuestion with "secret" (I-929)
//	POST /question/close {"question_id","state","reason"} -> f.CloseQuestion: the guest closes it;
//	                 reason terminal answers it in the terminal, withdrawn withdraws it (I-690)
//	POST /events     {"project_id","count","kinds","agents","windows","summaries","spacing_minutes","delivered"}: count events
//	                 spacing_minutes apart (default 1) ending now, oldest first. kinds, agents, windows and
//	                 summaries are cycled when given; by default every event is an agent_message with no
//	                 agent and the summaries are e000.., oldest first (I-414, I-662). The project's creation
//	                 moves to before the first of them (f.BackdateCreation), so it stays the oldest event
//	GET  /say        -> f.Says: the messages the fake has taken, for a test to assert what was sent
//	POST /runtime    {"default": nix|erofs} -> f.SetDefaultGuestRuntime: the runtime every new machine gets (I-1462)
//	POST /pending-base {"project_id","version"} -> f.SetPendingBase: the base the next start boots; "" clears it (I-1464)
//	POST /desktop    {"on": bool, "copy": "text"} -> f.SetDesktop: the browser view's stream route
//	                 serves a static grey screen over RFB instead of refusing, so a test can reach
//	                 the watching state and its clipboard panel; "copy" is handed to the page as the
//	                 machine's own cut-text. GET /desktop -> f.DesktopPastes, what Send sent (I-1216)
//	POST /say        {"agents": ["claude","codex-2"], "state": delivered|waiting, "replaced": bool,
//	                 "reply": "text", "reply_ms": 1500}
//	                 -> f.SetSayAgents, f.SetSayState and f.SetSayReply: which agents the machines run,
//	                 what a `repose say` is answered with (I-1144), and the turn-ended event a
//	                 delivered message gets back after reply_ms (I-1604; empty reply: none)
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
	// {project_id, minutes}: the project's guestd has not answered since
	// that many minutes ago; minutes < 0 clears it (DECISIONS I-1652).
	mux.HandleFunc("POST /not-answering", func(w http.ResponseWriter, r *http.Request) {
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
		f.SetNotAnswering(body.ProjectID, since)
	})
	// The hosts a user owns (DECISIONS I-1366). A fake host never
	// registers by itself, because nothing here runs hostd, so the
	// dashboard's test asks for one that has:
	// {name} adds a registered, answering host and answers its id;
	// {id, quiet_minutes} marks one offline that long ago;
	// {project_id, host_id} places a machine on it;
	// {state_host, state} moves the host of that name to a state, which
	// is how a test gets one that is still `registering` (waiting);
	// {limit} is the plan's host count, -1 to let it be the default.
	mux.HandleFunc("POST /owned-host", func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Name         string `json:"name"`
			ID           string `json:"id"`
			QuietMinutes int    `json:"quiet_minutes"`
			ProjectID    string `json:"project_id"`
			HostID       string `json:"host_id"`
			Limit        *int   `json:"limit"`
			InstallLine  string `json:"install_line"`
			SetInstall   bool   `json:"set_install"`
			StateHost    string `json:"state_host"`
			State        string `json:"state"`
			Arch         string `json:"arch"`
			// FreeBytes, with state_host, is the free memory that host
			// reports (I-1630).
			FreeBytes *int64 `json:"free_bytes"`
			// OldHostd, with state_host: that host's hostd does not
			// share, so it is placed by class (I-1630).
			OldHostd *bool `json:"old_hostd"`
			// MemUsedBytes, with project_id, is what that running
			// machine reports in use (I-1632).
			MemUsedBytes int64 `json:"mem_used_bytes"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		if body.Limit != nil {
			f.SetHostLimit(*body.Limit)
		}
		if body.SetInstall {
			f.SetHostInstall(body.InstallLine)
		}
		if body.ID != "" && body.QuietMinutes > 0 {
			f.SetOwnedHostOffline(body.ID, time.Duration(body.QuietMinutes)*time.Minute)
		}
		if body.ProjectID != "" && body.HostID != "" {
			f.SetProjectHost(body.ProjectID, body.HostID)
		}
		if body.StateHost != "" && body.State != "" {
			f.SetOwnedHostState(body.StateHost, body.State)
		}
		if body.StateHost != "" && body.OldHostd != nil {
			f.SetOwnedHostOldHostd(body.StateHost, *body.OldHostd)
		}
		if body.StateHost != "" && body.FreeBytes != nil {
			f.SetOwnedHostFree(body.StateHost, *body.FreeBytes)
		}
		if body.ProjectID != "" && body.MemUsedBytes > 0 {
			f.SetMemUsed(body.ProjectID, body.MemUsedBytes)
		}
		id := ""
		if body.Name != "" {
			id = f.AddOwnedHost(body.Name)
		}
		if body.Arch != "" {
			target := body.ID
			if target == "" {
				target = id
			}
			if target != "" {
				f.SetOwnedHostArch(target, body.Arch)
			}
		}
		_ = json.NewEncoder(w).Encode(map[string]string{"id": id})
	})
	// {project_id, used_bytes, size_bytes}: the root filesystem the Disk
	// column reads (I-567). The create leaves every machine at 1 GiB
	// used, the narrowest figure that column draws, so a test about how
	// wide it has to be needs this (ENG-24).
	mux.HandleFunc("POST /disk", func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			ProjectID string `json:"project_id"`
			UsedBytes int64  `json:"used_bytes"`
			SizeBytes int64  `json:"size_bytes"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		if err := f.SetProjectDisk(body.ProjectID, body.UsedBytes, body.SizeBytes); err != nil {
			http.Error(w, err.Error(), http.StatusNotFound)
		}
	})
	// {default}: the guest runtime a new machine gets, nix or erofs, for
	// every account (settings.default_guest_runtime, I-1462).
	mux.HandleFunc("POST /runtime", func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Default string `json:"default"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		if body.Default != "nix" && body.Default != "erofs" {
			http.Error(w, "default is nix or erofs", http.StatusBadRequest)
			return
		}
		f.SetDefaultGuestRuntime(body.Default)
	})
	// {project_id, version}: the base the project's next start boots, as
	// the base bump sets it (I-1464); "" clears it.
	mux.HandleFunc("POST /pending-base", func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			ProjectID string `json:"project_id"`
			Version   string `json:"version"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		if err := f.SetPendingBase(body.ProjectID, body.Version); err != nil {
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
			Secret    string   `json:"secret"`
			Link      string   `json:"link"`
			LinkLabel string   `json:"link_label"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		var q api.Question
		var err error
		if body.Secret != "" {
			q, err = f.AddSecretQuestion(body.ProjectID, body.Agent, body.Secret, body.Text, time.Duration(body.TimeoutS)*time.Second)
		} else {
			q, err = f.AddQuestion(body.ProjectID, body.Agent, body.Text, body.Options, time.Duration(body.TimeoutS)*time.Second)
		}
		if err == nil && body.Link != "" {
			f.LinkLast(body.ProjectID, q.ID, body.Link, body.LinkLabel)
			q.Link = map[string]string{"url": body.Link}
			if body.LinkLabel != "" {
				q.Link["label"] = body.LinkLabel
			}
		}
		if err != nil {
			http.Error(w, err.Error(), http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(q)
	})
	mux.HandleFunc("POST /question/close", func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			QuestionID string `json:"question_id"`
			State      string `json:"state"`
			Reason     string `json:"reason"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		q, err := f.CloseQuestion(body.QuestionID, body.State, body.Reason)
		if err != nil {
			http.Error(w, err.Error(), http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(q)
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
			// Link and LinkLabel go on the newest of them (I-931), and
			// Delivered likewise (I-1280).
			Link      string            `json:"link"`
			LinkLabel string            `json:"link_label"`
			Delivered map[string]string `json:"delivered"`
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
		f.LinkLast(body.ProjectID, "", body.Link, body.LinkLabel)
		f.DeliveredLast(body.ProjectID, body.Delivered)
		w.WriteHeader(http.StatusNoContent)
	})
	// The messages the fake took, oldest first: a test asserts the window
	// the dashboard sent to, not only the line it printed.
	mux.HandleFunc("GET /say", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(f.Says())
	})
	// `repose say` (DECISIONS I-1144): which agent windows the fake's
	// machines run, and what it answers a message with.
	// The browser view's fake desktop (DECISIONS I-1216): off unless a
	// test asks, because the view's reconnect cases need a stream that
	// refuses.
	mux.HandleFunc("GET /desktop", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(f.DesktopPastes())
	})
	mux.HandleFunc("POST /desktop", func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			On   bool   `json:"on"`
			Copy string `json:"copy"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		f.SetDesktop(body.On, body.Copy)
		w.WriteHeader(http.StatusNoContent)
	})
	// A trigger's runs (DECISIONS I-1256): the dashboard's tab reads
	// outcomes, and a test needs some without a machine.
	mux.HandleFunc("POST /trigger-run", func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			ProjectID    string `json:"project_id"`
			Trigger      string `json:"trigger"`
			State        string `json:"state"`
			Reason       string `json:"reason"`
			Summary      string `json:"summary"`
			Source       string `json:"source"`
			Window       string `json:"window"`
			PayloadBytes int64  `json:"payload_bytes"`
			Skips        int    `json:"skips"`
			AgoMinutes   int    `json:"ago_minutes"`
			TookSeconds  int    `json:"took_seconds"`
			// The agent's own `--link URL --label TEXT` (I-931).
			LinkURL   string `json:"link_url"`
			LinkLabel string `json:"link_label"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		started := time.Now().Add(-time.Duration(max(body.AgoMinutes, 1)) * time.Minute)
		finished := started.Add(time.Duration(max(body.TookSeconds, 1)) * time.Second)
		run := api.FakeTriggerRun{
			State: body.State, Reason: body.Reason, Summary: body.Summary,
			Source: body.Source, Window: body.Window, PayloadBytes: body.PayloadBytes,
			Skips: body.Skips, StartedAt: &started, FinishedAt: &finished, CreatedAt: started,
		}
		if body.LinkURL != "" {
			run.Link = map[string]string{"url": body.LinkURL}
			if body.LinkLabel != "" {
				run.Link["label"] = body.LinkLabel
			}
		}
		f.AddTriggerRun(body.ProjectID, body.Trigger, run)
		w.WriteHeader(http.StatusNoContent)
	})
	// What the machine was given as a trigger's prompt (I-1266): the
	// api carries the text and keeps none of it, so this is the only
	// place a test can read what the guest would hold.
	mux.HandleFunc("GET /trigger-prompt", func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		_, _ = io.WriteString(w, f.TriggerPrompt(q.Get("project_id"), q.Get("trigger")))
	})
	mux.HandleFunc("POST /say", func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Agents   *[]string `json:"agents"`
			State    string    `json:"state"`
			Replaced bool      `json:"replaced"`
			// Reply is what the agent answers a delivered message with,
			// ReplyMS how long it takes; a preview's stand-in for an
			// agent (I-1604). Empty turns it off.
			Reply   string `json:"reply"`
			ReplyMS int    `json:"reply_ms"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		if body.Agents != nil {
			f.SetSayAgents(*body.Agents...)
		}
		f.SetSayState(body.State, body.Replaced)
		f.SetSayReply(body.Reply, time.Duration(body.ReplyMS)*time.Millisecond)
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
	mux.HandleFunc("POST /refuse-create", func(w http.ResponseWriter, r *http.Request) {
		var body struct{ Message string }
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		f.RefuseNextCreate(body.Message)
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
