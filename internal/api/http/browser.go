package httpapi

import (
	"bufio"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"io"
	"log/slog"
	"net"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/heracraft/repose/internal/api/edge"
	"github.com/heracraft/repose/internal/api/metrics"
	"github.com/heracraft/repose/internal/api/resting"
	"github.com/heracraft/repose/internal/api/store"
	"github.com/heracraft/repose/internal/db"
	"github.com/heracraft/repose/internal/obs"
	"github.com/heracraft/repose/internal/wsframe"
)

// The dashboard's browser view (DECISIONS I-1216, amending I-292): two
// routes, a mint and a stream.
//
//	POST /v1/projects/{id}/browser/view  -> a ticket, as a cookie
//	GET  /v1/browser/stream              -> the desktop, as a WebSocket
//
// The mint is an ordinary authenticated route: it checks ownership
// through the single-owner lookup every project route uses, refuses a
// machine that is not running, wakes one that rests (at most once per
// project per ten minutes, from a row that survives an api restart), and
// sets an HttpOnly, Secure, SameSite=Strict cookie scoped to the stream
// path. The ticket is never in the response body, so no script, no
// extension and no devtools export can read it, and the body carries no
// password of any kind.
//
// The stream route carries **no project id**. The ticket names the
// project, and the handler derives project_id, login and guest_ip from
// the ticket's row alone: there is no path id to resolve, so there is
// nothing for a caller to point at someone else's machine. That is the
// whole reason the route has the shape it has.
//
// The api is on the byte path, so the api owns the byte budget (on the
// ticket row, so a reconnect cannot start it again), the idle close and
// the hard session cap. The edge owns the handshake with the guest and
// the concurrency counters, and the guest's VNC password never leaves it.

const (
	// viewTicketTTL is short because a ticket authenticates a *connect*
	// and not a session: the page mints again on every reconnect through
	// the authenticated POST, so expiry is this credential's revocation
	// and nothing has to be revoked by hand when a machine stops.
	viewTicketTTL = 15 * time.Minute
	// viewWakeWindow is one wake per project per ten minutes, and only
	// from the mint route (I-1114's `webhook_starts` precedent).
	viewWakeWindow = 10 * time.Minute
	// viewCookie is the ticket's cookie, scoped to the stream path.
	viewCookie = "repose_view"
	// viewStreamPath is the one route the cookie is sent to.
	viewStreamPath = "/v1/browser/stream"
	// ViewBudgetBytes is the bytes one ticket may carry before the stream
	// closes. It is the only bound on a rooted guest using the view as
	// edge bandwidth, and it is not metered as the tenant's egress: the
	// frames never cross a guest's uplink, so nothing could meter them
	// (nix/hosts/nftables.nix `guest_meter`). Exported because it is the
	// figure /docs quotes and a test plants a row just under.
	ViewBudgetBytes = 2 << 30
	// viewFlush writes the spend to the ticket row every this many bytes,
	// and once more at the close.
	viewFlush = 16 << 20
	// viewIdle closes a stream with no frame in either direction. Shorter
	// than the guest's own 30-minute viewer stop on purpose, so a closed
	// laptop stops costing frames long before the guest stops drawing
	// them. Deps.ViewIdle overrides it (tests).
	viewIdle = 120 * time.Second
	// viewSessionCap is the hard cap on one stream, well inside the
	// ticket's own life plus a long watch.
	viewSessionCap = 4 * time.Hour
	// viewMessageMax bounds one WebSocket message from the page. A
	// pointer or key event is a handful of bytes; this is for a cut-text
	// the page does not send and a client that lies about its lengths.
	viewMessageMax = 1 << 20
	// viewChunk is how much of the guest's stream goes into one frame.
	viewChunk = 32 << 10
	// ViewMintRate caps mints per user per hour, over the general
	// per-minute buckets `authed` already applies.
	ViewMintRate = 10
)

// mintBrowserView is POST /v1/projects/{id}/browser/view.
func (s *Server) mintBrowserView(w http.ResponseWriter, r *http.Request) error {
	if s.d.ViewRelay == nil {
		return errf("unavailable", "the browser view is not configured on this api")
	}
	ctx := r.Context()
	// Every mint sweeps the dead rows, so the table needs no janitor job.
	if err := store.DeleteExpiredViewTickets(ctx, s.d.Pool); err != nil {
		return err
	}
	p, err := s.userProject(r)
	if err != nil {
		return err
	}
	u := userFrom(ctx)
	if p.State != "running" {
		// No implicit start: `repose stop` is a deliberate act that frees
		// plan memory, and a start takes 12 to 14 s (I-1114's reasoning).
		return withDetail(errf("conflict", "%s is %s", p.Slug, p.State), map[string]any{"state": p.State})
	}
	if p.GuestIP == nil {
		return withDetail(errf("conflict", "%s is running but repose has no address for it", p.Slug), map[string]any{"state": p.State})
	}
	// A machine that rests has its vCPUs stopped, so it cannot draw a
	// frame. The wake is here and nowhere else on this path.
	wake := ""
	if p.RestingSince != nil {
		last, ok, err := store.LastViewWake(ctx, s.d.Pool, p.ID)
		if err != nil {
			return err
		}
		if !ok || time.Since(last) >= viewWakeWindow {
			waker := s.userWake()
			if waker == nil {
				return errf("internal", "waking is not configured on this api")
			}
			if _, err := waker.Wake(ctx, p.ID, resting.ReasonView); err != nil {
				return wakeFailed(p.Slug, err)
			}
			if err := store.MarkViewWake(ctx, s.d.Pool, p.ID); err != nil {
				return err
			}
			wake = resting.ReasonView
			p.RestingSince = nil
		}
	}
	ticket, hash, err := newViewTicket()
	if err != nil {
		return err
	}
	expires := time.Now().Add(viewTicketTTL)
	ticketID, err := store.InsertViewTicket(ctx, s.d.Pool, hash, p.ID, u.ID, expires)
	if err != nil {
		return err
	}
	http.SetCookie(w, &http.Cookie{
		Name:     viewCookie,
		Value:    ticket,
		Path:     viewStreamPath,
		MaxAge:   int(viewTicketTTL.Seconds()),
		HttpOnly: true,
		Secure:   true,
		SameSite: http.SameSiteStrictMode,
	})
	attrs := []any{"event", obs.EventBrowserViewOpen, "project_id", p.ID.String(), "user_id", u.ID.String(),
		"ticket_id", ticketID.String(), "source_prefix", sourcePrefix(r)}
	if wake != "" {
		attrs = append(attrs, "wake", wake)
	}
	obs.Logger(ctx, s.d.Log).Info("browser view opened", attrs...)
	// No ticket and no password in the body: the ticket is in the cookie
	// and the password never leaves the edge.
	writeJSON(w, http.StatusOK, map[string]any{
		"expires_at":  expires.UTC(),
		"stream_path": viewStreamPath,
		"resting":     p.RestingSince != nil,
	})
	return nil
}

// newViewTicket is 256 random bits, base64url with no padding (a padded
// value is not a valid cookie value), and its sha256, which is the only
// form the database holds.
func newViewTicket() (ticket string, hash []byte, err error) {
	var b [32]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", nil, err
	}
	ticket = base64.RawURLEncoding.EncodeToString(b[:])
	sum := sha256.Sum256([]byte(ticket))
	return ticket, sum[:], nil
}

// browserStream is GET /v1/browser/stream: the page's WebSocket, the
// edge's relay, and the bytes between them.
func (s *Server) browserStream(w http.ResponseWriter, r *http.Request) error {
	if s.d.ViewRelay == nil {
		return errf("unavailable", "the browser view is not configured on this api")
	}
	// The upgrade headers first: a plain GET must not start a desktop.
	if !strings.EqualFold(r.Header.Get("Upgrade"), "websocket") {
		return errf("invalid", "this route takes a websocket upgrade")
	}
	ctx := r.Context()
	prefix := sourcePrefix(r)
	switch code := s.viewConns.begin(prefix); code {
	case "":
	case "forbidden":
		return errf("forbidden", "too many refused tickets from your address; try again later")
	case "rate_limited":
		return errf("rate_limited", "too many connections from your address")
	default:
		return errf("view_capacity", "the view is at capacity; try again shortly")
	}
	defer s.viewConns.done()

	// The cookie is ambient, so the upgrade also checks the browser's
	// Origin against the dashboard's own. A client that sends no Origin
	// is not a browser and cannot be made to send the cookie by a page.
	if origin := r.Header.Get("Origin"); origin != "" && !s.viewOriginAllowed(origin) {
		s.viewConns.checked(prefix)
		return errf("forbidden", "that origin may not open a view")
	}
	cookie, err := r.Cookie(viewCookie)
	if err != nil || cookie.Value == "" {
		s.viewConns.checked(prefix)
		s.viewConns.fail(prefix)
		return errf("view_expired", "mint a view ticket first")
	}
	sum := sha256.Sum256([]byte(cookie.Value))
	ticket, err := store.GetViewTicket(ctx, s.d.Pool, sum[:])
	if err != nil {
		s.viewConns.checked(prefix)
		if errors.Is(err, db.ErrNotFound) {
			// Expiry is this credential's revocation, so an unknown hash
			// and an expired row are one answer; the page mints again.
			s.viewConns.fail(prefix)
			return errf("view_expired", "the view ticket has expired; mint another")
		}
		return err
	}
	s.viewConns.checked(prefix)

	// Everything from here comes from the ticket's row. There is no path
	// id, and GetProject is never reached with anything a caller chose.
	p, err := store.GetProject(ctx, s.d.Pool, ticket.ProjectID)
	if err != nil {
		return err
	}
	if p.DestroyedAt != nil || p.UserID != ticket.UserID {
		return errf("view_expired", "the view ticket is no longer valid")
	}
	// A suspended account may read itself and its billing and nothing
	// else, which a live ticket must not be a way around.
	u, err := store.GetUser(ctx, s.d.Pool, ticket.UserID)
	if err != nil {
		return err
	}
	if u.SuspendedAt != nil {
		return errf("view_expired", "the view ticket is no longer valid")
	}
	if p.State != "running" || p.GuestIP == nil {
		return withDetail(errf("conflict", "%s is %s", p.Slug, p.State), map[string]any{"state": p.State})
	}
	if ticket.Bytes >= ViewBudgetBytes {
		return errf("view_budget", "this view has carried all it may; mint another")
	}

	conn, err := s.d.ViewRelay.Dial(ctx, p.ID.String(), p.Slug+"."+u.Handle, p.GuestIP.String())
	if err != nil {
		s.viewMetric("refused")
		if code := edge.Code(err); code != "" {
			obs.Logger(ctx, s.d.Log).Warn("view relay refused", "event", "route_fail", "reason", code,
				"project_id", p.ID.String(), "user_id", u.ID.String(), "ticket_id", ticket.ID.String())
			switch code {
			case "view_busy":
				return errf("view_busy", "%s already has a view open", p.Slug)
			case "view_capacity":
				return errf("view_capacity", "the view is at capacity; try again shortly")
			}
			return withDetail(errf("conflict", "%s's desktop did not answer", p.Slug), map[string]any{"reason": code})
		}
		obs.Logger(ctx, s.d.Log).Warn("view relay unreachable", "event", "route_fail", "reason", "view_relay",
			"project_id", p.ID.String(), "ticket_id", ticket.ID.String(), "err", err.Error())
		return errf("unavailable", "the view could not be opened; try again shortly")
	}
	defer func() { _ = conn.Close() }()

	// The relay answered, so the stream exists: upgrade and pipe. Every
	// refusal above this line is an ordinary error envelope the page can
	// read; after it, the page learns the outcome from the close frame.
	ws, brw, err := wsframe.Accept(w, r)
	if err != nil {
		return errf("invalid", "this route takes a websocket upgrade")
	}
	defer func() { _ = ws.Close() }()

	v := &viewStream{
		log:    obs.Logger(ctx, s.d.Log),
		m:      s.d.Metrics,
		pool:   s.d.Pool,
		hash:   sum[:],
		ticket: ticket,
		user:   u,
		prefix: prefix,
		idle:   s.viewIdleWindow(),
		ws:     ws,
		br:     brw.Reader,
		guest:  conn,
	}
	v.run(ctx)
	return nil
}

// viewOriginAllowed is the dashboard's own origin, and nothing else. With
// none configured the check is off, which is what a test api and a
// development api without DASHBOARD_URL get.
func (s *Server) viewOriginAllowed(origin string) bool {
	if len(s.d.ViewOrigins) == 0 {
		return true
	}
	for _, o := range s.d.ViewOrigins {
		if strings.EqualFold(origin, o) {
			return true
		}
	}
	return false
}

// viewIdleWindow is viewIdle, or what the deployment set (tests).
func (s *Server) viewIdleWindow() time.Duration {
	if s.d.ViewIdle > 0 {
		return s.d.ViewIdle
	}
	return viewIdle
}

func (s *Server) viewMetric(outcome string) {
	if s.d.Metrics != nil {
		s.d.Metrics.BrowserViewsTotal.WithLabelValues(outcome).Inc()
	}
}

// viewStream is one open view: the page's WebSocket on one side, the
// edge's relay on the other, and the three bounds the api owns.
type viewStream struct {
	log    *slog.Logger
	m      *metrics.M
	pool   *db.Pool
	hash   []byte
	ticket *store.ViewTicket
	user   *store.User
	prefix string

	ws    net.Conn
	br    *bufio.Reader
	guest net.Conn
	// idle is the window with no frame in either direction that closes
	// the stream.
	idle time.Duration

	// flushed is how much of in+out has reached the ticket row. Written
	// by the guest-to-page goroutine while the stream runs and by run
	// after both have ended, never by two at once.
	flushed  int64
	in       atomic.Int64
	out      atomic.Int64
	lastSeen atomic.Int64 // unix nanoseconds

	// wmu keeps a frame whole when two goroutines write at once.
	wmu     sync.Mutex
	once    sync.Once
	outcome string
}

// write sends one frame. Both goroutines write to the socket -- the
// reader answers a ping, the copier sends the guest's bytes, and end
// sends the close frame -- and a frame has to reach the page whole.
func (v *viewStream) write(frame []byte) error {
	v.wmu.Lock()
	defer v.wmu.Unlock()
	_, err := v.ws.Write(frame)
	return err
}

// end records the first outcome, tells the page why in a close frame, and
// closes both sides, which is what ends every copy goroutine.
//
// The close frame is what lets the page tell an idle or budget close --
// which it must not reconnect after, or it would mint again and wake
// again -- from a transport error, which it may retry. Without it every
// close looks the same from a browser.
func (v *viewStream) end(outcome string) {
	v.once.Do(func() {
		v.outcome = outcome
		// 1000 is a normal closure; the reason is one of the closed set
		// of outcome words and never anything a tenant typed.
		reason := append([]byte{0x03, 0xe8}, outcome...)
		_ = v.write(wsframe.Encode(wsframe.OpClose, reason, false))
		_ = v.guest.Close()
		_ = v.ws.Close()
	})
}

func (v *viewStream) touch() { v.lastSeen.Store(time.Now().UnixNano()) }

// run pipes until either side ends or one of the api's bounds fires, then
// writes the close line and the ticket's spend.
func (v *viewStream) run(ctx context.Context) {
	start := time.Now()
	v.touch()
	done := make(chan struct{})
	var wg sync.WaitGroup
	wg.Add(2)
	// The page to the guest: whole WebSocket messages, each bounded, and
	// nothing read for its content.
	go func() {
		defer wg.Done()
		for {
			op, payload, _, err := wsframe.ReadMessage(v.br, viewMessageMax, func(f wsframe.Frame) error {
				switch f.Op {
				case wsframe.OpPing:
					return v.write(wsframe.Encode(wsframe.OpPong, f.Payload, false))
				case wsframe.OpClose:
					return io.EOF
				}
				return nil
			})
			if err != nil {
				if errors.Is(err, wsframe.ErrTooLarge) {
					v.end("too_large")
					return
				}
				v.end("closed")
				return
			}
			if op == wsframe.OpClose {
				v.end("closed")
				return
			}
			v.touch()
			v.in.Add(int64(len(payload)))
			if _, err := v.guest.Write(payload); err != nil {
				v.end("closed")
				return
			}
		}
	}()
	// The guest to the page: binary frames, and the byte budget.
	go func() {
		defer wg.Done()
		buf := make([]byte, viewChunk)
		var sinceFlush int64
		for {
			n, err := v.guest.Read(buf)
			if n > 0 {
				v.touch()
				v.out.Add(int64(n))
				sinceFlush += int64(n)
				if werr := v.write(wsframe.Encode(wsframe.OpBinary, buf[:n], false)); werr != nil {
					v.end("closed")
					return
				}
				if sinceFlush >= viewFlush {
					spent, ferr := v.flush(ctx, sinceFlush)
					sinceFlush = 0
					if ferr == nil && spent >= ViewBudgetBytes {
						v.end("budget")
						return
					}
				}
			}
			if err != nil {
				if errors.Is(err, io.EOF) {
					v.end("closed")
				} else {
					v.end("error")
				}
				return
			}
		}
	}()
	// The idle close and the session cap. A still desktop sends nothing,
	// which is the case this is for: the tab is open and nobody is there.
	go func() {
		tick := v.idle / 4
		if tick > 5*time.Second {
			tick = 5 * time.Second
		}
		if tick < 50*time.Millisecond {
			tick = 50 * time.Millisecond
		}
		t := time.NewTicker(tick)
		defer t.Stop()
		for {
			select {
			case <-done:
				return
			case <-ctx.Done():
				v.end("closed")
				return
			case now := <-t.C:
				if now.Sub(time.Unix(0, v.lastSeen.Load())) >= v.idle {
					v.end("idle")
					return
				}
				if now.Sub(start) >= viewSessionCap {
					v.end("cap")
					return
				}
			}
		}
	}()
	wg.Wait()
	close(done)
	v.end("closed")

	// The last of the spend, whatever ended the stream, so a reconnect
	// sees the bytes this one carried.
	if left := v.out.Load() + v.in.Load() - v.flushed; left > 0 {
		_, _ = v.flush(ctx, left)
	}
	if v.m != nil {
		v.m.BrowserViewsTotal.WithLabelValues(v.outcome).Inc()
	}
	v.log.Info("browser view closed",
		"event", obs.EventBrowserViewClose,
		"project_id", v.ticket.ProjectID.String(),
		"user_id", v.ticket.UserID.String(),
		"ticket_id", v.ticket.ID.String(),
		"outcome", v.outcome,
		"ms", time.Since(start).Milliseconds(),
		"bytes_in", v.in.Load(),
		"bytes_out", v.out.Load(),
		"source_prefix", v.prefix,
	)
}

// flush adds n bytes to the ticket's spend and returns the new total. The
// budget lives on the row because the attack it bounds is reconnecting.
func (v *viewStream) flush(ctx context.Context, n int64) (int64, error) {
	// The request's context is cancelled the moment the stream ends, so
	// the write that records the spend gets its own.
	fctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	spent, err := store.TouchViewTicket(fctx, v.pool, v.hash, n)
	if err != nil {
		v.log.Warn("view spend not recorded", "event", "request_error", "ticket_id", v.ticket.ID.String(), "err", err.Error())
		return 0, err
	}
	v.flushed += n
	return spent, nil
}
