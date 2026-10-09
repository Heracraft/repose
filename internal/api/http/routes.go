package httpapi

import (
	"net/http"
)

// registerUserRoutes lists every user route of docs/interfaces/api.md.
func (s *Server) registerUserRoutes() {
	a := func(h handler) handler { return s.authed(h, false) }
	m := s.user
	// Users
	s.route(m, "GET /v1/me", a(s.getMe))
	s.route(m, "PATCH /v1/me", a(s.patchMe))
	s.route(m, "DELETE /v1/me", a(s.deleteMe))
	s.route(m, "POST /v1/me/notify-test", a(s.notifyTest))
	// The personal layer, machine.nix (DECISIONS I-490)
	s.route(m, "GET /v1/me/config", a(s.getPersonal))
	s.route(m, "PUT /v1/me/config", a(s.limited(s.cfg, s.putPersonal)))
	s.route(m, "GET /v1/me/config/revisions", a(s.listPersonal))
	// Projects
	s.route(m, "GET /v1/projects", a(s.listProjects))
	s.route(m, "POST /v1/projects", a(s.createProject))
	s.route(m, "GET /v1/projects/destroyed", a(s.listDestroyed))
	s.route(m, "POST /v1/projects/restore", a(s.restoreByName))
	s.route(m, "GET /v1/projects/{id}", a(s.getProject))
	s.route(m, "PATCH /v1/projects/{id}", a(s.patchProject))
	s.route(m, "DELETE /v1/projects/{id}", a(s.destroyProject))
	s.route(m, "POST /v1/projects/{id}/start", a(s.startProject))
	s.route(m, "POST /v1/projects/{id}/stop", a(s.stopProject))
	s.route(m, "GET /v1/projects/{id}/ops/{op_id}", a(s.getOp))
	s.route(m, "GET /v1/projects/{id}/ops/{op_id}/log", s.authed(s.opLog, true))
	s.route(m, "POST /v1/projects/{id}/resize", a(s.resizeProject))
	s.route(m, "GET /v1/projects/{id}/route", a(s.projectRoute))
	s.route(m, "GET /v1/projects/{id}/samples", a(s.projectSamples))
	// Config
	s.route(m, "GET /v1/projects/{id}/config", a(s.getConfig))
	s.route(m, "PUT /v1/projects/{id}/config", a(s.limited(s.cfg, s.putConfig)))
	s.route(m, "GET /v1/projects/{id}/config/revisions", a(s.listRevisions))
	s.route(m, "POST /v1/projects/{id}/config/revisions/{rev}/apply", a(s.applyRevision))
	s.route(m, "GET /v1/catalog", a(s.catalog))
	// Certificates
	s.route(m, "POST /v1/certs", a(s.limited(s.certs, s.issueCert)))
	s.route(m, "POST /v1/certs/revoke", a(s.revokeCert))
	// Secrets
	s.route(m, "GET /v1/projects/{id}/secrets", a(s.listSecrets))
	s.route(m, "PUT /v1/projects/{id}/secrets/{name}", a(s.putSecret))
	s.route(m, "DELETE /v1/projects/{id}/secrets/{name}", a(s.deleteSecret))
	// Snapshots
	s.route(m, "GET /v1/projects/{id}/snapshots", a(s.listSnapshots))
	s.route(m, "POST /v1/projects/{id}/snapshots", a(s.createSnapshot))
	s.route(m, "POST /v1/projects/{id}/snapshots/{sid}/restore", a(s.restoreSnapshot))
	s.route(m, "POST /v1/projects/{id}/fork", a(s.forkProject))
	// Events and logs
	s.route(m, "GET /v1/projects/{id}/events", a(s.listEvents))
	s.route(m, "GET /v1/projects/{id}/logs", a(s.projectLogs))
	// Questions from repose-ask (DECISIONS I-245)
	s.route(m, "GET /v1/questions", a(s.listQuestions))
	s.route(m, "GET /v1/projects/{id}/questions", a(s.listProjectQuestions))
	s.route(m, "POST /v1/projects/{id}/questions/{qid}/answer", a(s.answerQuestion))
	s.route(m, "POST /v1/projects/{id}/questions/{qid}/cancel", a(s.cancelQuestion))
	// Usage and billing (DECISIONS I-289). POST /v1/billing/waitlist and
	// GET /v1/public/seats are the seats workstream's (I-290).
	s.route(m, "GET /v1/usage", a(s.usage))
	s.route(m, "GET /v1/billing", a(s.billingOverview))
	s.route(m, "POST /v1/billing/checkout", a(s.billingCheckout))
	s.route(m, "POST /v1/billing/plan", a(s.billingPlan))
	s.route(m, "POST /v1/billing/cancel", a(s.billingCancel))
	s.route(m, "POST /v1/billing/resume", a(s.billingResume))
	s.route(m, "POST /v1/billing/portal", a(s.billingPortal))
	s.route(m, "GET /v1/billing/invoices", a(s.billingInvoices))
	// The seats waitlist (DECISIONS I-290); /public/seats is the landing
	// page's count and needs no token.
	s.route(m, "POST /v1/billing/waitlist", a(s.billingWaitlist))
	s.route(m, "GET /v1/public/seats", s.publicSeats)
	// Polar authenticates itself with the Standard Webhooks headers, so the
	// webhook carries no bearer token.
	s.route(m, "POST /v1/billing/webhook", s.billingWebhook)
}

// registerInternalRoutes lists the gateway routes; the listener's mTLS
// is the authentication.
func (s *Server) registerInternalRoutes() {
	m := s.internal
	s.route(m, "GET /v1/internal/route", s.internalRoute)
	s.route(m, "GET /v1/internal/revoked", s.internalRevoked)
	s.route(m, "GET /v1/internal/ca", s.internalCA)
	s.route(m, "POST /v1/internal/sessions", s.internalSessions)
	s.route(m, "GET /v1/internal/hosts", s.internalHosts)
	s.route(m, "POST /v1/internal/gateway-certs", s.internalGatewayCerts)
	s.route(m, "POST /v1/internal/events", s.internalEvents)
	m.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) { s.healthz(w, r) })
}
