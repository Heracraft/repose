package gateway

// Authentication result strings. Every value except ResultOK is a reason of
// repose_gateway_auth_fail_total and of the auth_fail log event, and each is
// in obsmetrics.AuthFailReasons (docs/workstreams/10-observability.md §5,
// DECISIONS I-81). ResultOK is not a metric reason: an accepted connection
// is counted by repose_gateway_sessions_total when its relay opens.
const (
	ResultOK             = "ok"
	ResultNoCert         = "no_cert"
	ResultBadCA          = "bad_ca"
	ResultRevoked        = "revoked"
	ResultExpired        = "expired"
	ResultWrongPrincipal = "wrong_principal"
	ResultStopped        = "stopped"
	ResultRouteError     = "route_error"
	// ResultNoAddress: the project runs and its host is reachable, but
	// the api holds no address for the guest (DECISIONS I-721). Separate
	// from route_error because it is the api's record that is wrong, not
	// the host or the route lookup.
	ResultNoAddress   = "no_address"
	ResultRateLimited = "rate_limited"
	ResultNotFound    = "not_found"
	ResultBadLogin    = "bad_login"
	ResultBusy        = "busy"
)

// Hook ingest results (the `result` label of repose_gateway_hook_events_total).
const (
	HookForwarded   = "forwarded"
	HookRejected    = "rejected"
	HookRateLimited = "rate_limited"
	HookAPIError    = "api_error"
	HookNotFound    = "not_found"
)
