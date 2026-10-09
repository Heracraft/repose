package obs

import "sort"

// Every log event named in docs/workstreams/10-observability.md §5, as
// constants so a call site cannot misspell one and so the coverage test can
// find the ones nobody emits. "Events are intentional, not fmt.Sprintf
// residue": a component may emit events beyond this list (hostd's own §5.14
// list is longer), but it must emit all of its own.
const (
	// hostd
	EventGuestCreate   = "guest_create"
	EventGuestStart    = "guest_start"
	EventGuestStop     = "guest_stop"
	EventGuestDestroy  = "guest_destroy"
	EventGuestState    = "guest_state"
	EventBuildStart    = "build_start"
	EventBuildDone     = "build_done"
	EventBuildFail     = "build_fail"
	EventSwitchDone    = "switch_done"
	EventSnapshotStart = "snapshot_start"
	EventSnapshotDone  = "snapshot_done"
	EventSnapshotFail  = "snapshot_fail"
	// EventRestoreDone carries the download bytes and the write and fsck
	// times, so a slow restore says where it went (I-403).
	EventRestoreDone      = "restore_done"
	EventRestoreFail      = "restore_fail"
	EventStreamConnect    = "stream_connect"
	EventStreamDisconnect = "stream_disconnect"
	EventGuestdLost       = "guestd_lost"
	EventGuestdRegained   = "guestd_regained"
	EventPoolWarning      = "pool_warning"
	EventStoreWarning     = "store_warning"

	// guestd
	EventReady         = "ready"
	EventFreeze        = "freeze"
	EventThaw          = "thaw"
	EventFreezeTimeout = "freeze_timeout"
	EventSwitch        = "switch"
	EventAgentEvent    = "agent_event"
	EventAgentState    = "agent_state"
	// EventOOMPriority is guestd re-applying oom_score_adj (I-200); it
	// carries counts only.
	EventOOMPriority    = "oom_priority"
	EventHookBadPayload = "hook_bad_payload"
	EventGrowFs         = "grow_fs"
	EventWriteSecrets   = "write_secrets"
	EventSetPrincipals  = "set_principals"
	EventSetupProject   = "setup_project"
	EventSample         = "sample"
	EventExec           = "exec"
	EventShutdown       = "shutdown"
	EventWarning        = "warning"

	// api
	EventRequest       = "request"
	EventCertIssue     = "cert_issue"
	EventCertRevoke    = "cert_revoke"
	EventSchedule      = "schedule"
	EventScheduleFail  = "schedule_fail"
	EventScheduleWait  = "schedule_wait"
	EventCommandSend   = "command_send"
	EventCommandResult = "command_result"
	EventRollupDone    = "rollup_done"
	// Billing (workstream 09, DECISIONS I-289). billing_gap is the failure
	// mode "sample gap for a running guest": the minutes are under-billed,
	// never estimated, and the gap is visible rather than silent. The
	// Polar lines carry user_id, kind, reason, plan and result only.
	EventBillingGap         = "billing_gap"
	EventBillingWebhook     = "webhook_received"
	EventOverageCharged     = "overage_charged"
	EventBillingGateRefused = "gate_refused"
	EventBillingStopped     = "billing_stopped"
	EventBillingEnforce     = "billing_enforce"
	// EventBillingDiskOverPlan is a user whose projects came to hold more
	// than the plan's disk (DECISIONS I-585), logged once per period
	// with the disk_over_plan email.
	EventBillingDiskOverPlan = "disk_over_plan"
	EventNotifySend          = "notify_send"
	EventNotifyFail          = "notify_fail"
	EventAdminAction         = "admin_action"
	// EventPartitionDropFail is the §6 failure mode: the meter_samples or
	// proc_samples partition drop did not run, so disk grows and nothing
	// else breaks.
	EventPartitionDropFail = "partition_drop_fail"

	// gateway
	EventSessionOpen  = "session_open"
	EventSessionClose = "session_close"
	EventAuthFail     = "auth_fail"
	EventRouteFail    = "route_fail"
	EventDialFail     = "dial_fail"
)

// RequiredEvents is the per-component list §5 calls "the events each
// component must emit". The coverage test in events_test.go checks a
// component's list against the source of the repository.
var RequiredEvents = map[Component][]string{
	ComponentHostd: {
		EventGuestCreate, EventGuestStart, EventGuestStop, EventGuestDestroy,
		EventGuestState, EventBuildStart, EventBuildDone, EventBuildFail,
		EventSwitchDone, EventSnapshotStart, EventSnapshotDone, EventSnapshotFail,
		EventRestoreDone, EventRestoreFail, EventStreamConnect, EventStreamDisconnect, EventGuestdLost,
		EventGuestdRegained, EventPoolWarning, EventStoreWarning,
	},
	ComponentGuestd: {
		EventReady, EventFreeze, EventThaw, EventFreezeTimeout, EventSwitch,
		EventAgentEvent, EventAgentState, EventHookBadPayload, EventGrowFs,
		EventWriteSecrets, EventSetPrincipals, EventSetupProject, EventSample,
		EventExec, EventShutdown, EventWarning,
	},
	ComponentAPI: {
		EventRequest, EventCertIssue, EventCertRevoke, EventSchedule,
		EventScheduleFail, EventScheduleWait, EventCommandSend, EventCommandResult, EventRollupDone,
		EventBillingWebhook, EventNotifySend, EventNotifyFail, EventPartitionDropFail,
	},
	ComponentGateway: {
		EventSessionOpen, EventSessionClose, EventAuthFail, EventRouteFail,
		EventDialFail,
	},
	// The CLI logs only to ~/.config/repose/cli.log at debug level with
	// --verbose and ships nothing from a laptop, so it has no required
	// events. hostdev and repose-hook are dev and helper binaries.
	//
	// admin_action is the admin CLI's, not the api's: §5 put it on the api's
	// list expecting admin actions to arrive as api calls, and workstream 05
	// built repose-admin against Postgres directly (DECISIONS I-59), so the
	// line is written where the audit_log row is.
	ComponentCLI:     {},
	ComponentAdmin:   {EventAdminAction},
	ComponentHostdev: {},
	ComponentHook:    {},
}

// PendingEvents are events in §5 whose producer does not exist yet, with the
// workstream that owes each one. The coverage test reports them instead of
// failing, so that a missing producer is visible without blocking the
// workstreams that are built.
//
// It is empty: workstream 09 built POST /billing/webhook, which is the last
// §5 event that had no producer.
var PendingEvents = map[string]string{}

// AllRequiredEvents is every event in RequiredEvents, sorted and deduped.
func AllRequiredEvents() []string {
	seen := map[string]bool{}
	var out []string
	for _, evs := range RequiredEvents {
		for _, e := range evs {
			if !seen[e] {
				seen[e] = true
				out = append(out, e)
			}
		}
	}
	sort.Strings(out)
	return out
}
