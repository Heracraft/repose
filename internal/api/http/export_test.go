package httpapi

import "strings"

// SetHerdrMinBase stands in for the herdrMinBase a release sets, for the
// test's lifetime; the returned func puts the old value back.
func SetHerdrMinBase(v string) (restore func()) {
	old := minBase.Load()
	minBase.Store(&v)
	return func() { minBase.Store(old) }
}

// TokenPolicyGaps lists the user routes behind `authed` that neither
// tokenRouteScope nor tokenNever names (DECISIONS I-1316).
func (s *Server) TokenPolicyGaps() []string {
	// Registered without authed: a signed link, a cookie ticket, the
	// public count, Polar's webhook, and the internal mTLS routes.
	open := map[string]bool{"GET /v1/browser/stream": true, "GET /v1/public/seats": true, "POST /v1/billing/webhook": true,
		"GET /v1/notify/unsubscribe": true, "POST /v1/notify/unsubscribe": true, "GET /v1/questions/reply": true, "POST /v1/questions/reply": true}
	var gaps []string
	for _, p := range s.routes {
		if open[p] || strings.Contains(p, "/v1/internal/") || !strings.Contains(p, "/v1/") {
			continue
		}
		if _, ok := tokenRouteScope[p]; ok {
			continue
		}
		if _, ok := tokenNever[p]; ok {
			continue
		}
		gaps = append(gaps, p)
	}
	return gaps
}
