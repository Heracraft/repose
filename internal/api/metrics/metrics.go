// Package metrics defines the repose_api_* families from
// docs/workstreams/10-observability.md §5 and 05-control-plane-api.md
// §5.15. Labels are bounded enums only; project and guest ids never
// appear as labels.
package metrics

import (
	"github.com/prometheus/client_golang/prometheus"

	"github.com/heracraft/repose/internal/obs"
	obsmetrics "github.com/heracraft/repose/internal/obs/metrics"
)

// M holds every api metric.
type M struct {
	RequestsTotal                *prometheus.CounterVec
	RequestDuration              *prometheus.HistogramVec
	Hosts                        *prometheus.GaugeVec
	Projects                     *prometheus.GaugeVec
	ScheduleTotal                *prometheus.CounterVec
	CertsIssuedTotal             prometheus.Counter
	CertsRevokedTotal            prometheus.Counter
	RollupLagSeconds             prometheus.Gauge
	RollupDuration               prometheus.Histogram
	NotifyTotal                  *prometheus.CounterVec
	NotifyDeliveryLatencySeconds prometheus.Histogram
	OutboxDepth                  prometheus.Gauge
	OutboxLagSeconds             prometheus.Gauge
	// BillingWebhookTotal counts Paddle webhook deliveries by event kind and
	// result; PaddleWebhookRejected reads the bad_signature series.
	BillingWebhookTotal *prometheus.CounterVec
	// BillingOverageChargesTotal counts egress overage lines sent to Paddle
	// by result; OverageChargeFailed reads the error series.
	BillingOverageChargesTotal *prometheus.CounterVec
	// BillingGateRefusedTotal counts payment_required refusals by reason.
	BillingGateRefusedTotal *prometheus.CounterVec
	// BillingSubscriptions counts subscription webhooks by plan and status,
	// which is the dashboard's view of the plan mix.
	BillingSubscriptions *prometheus.CounterVec
	// BillingStopsTotal counts machines the api stopped for billing, by
	// reason (past_due, ended, egress); BillingStopped alerts on it.
	BillingStopsTotal  *prometheus.CounterVec
	SnapshotAgeSeconds prometheus.Gauge
	GRPCStreams        prometheus.Gauge
	OpsTotal           *prometheus.CounterVec
	OpsOpen            *prometheus.GaugeVec
	BuildDuration      *prometheus.HistogramVec
	SecretsOpsTotal    *prometheus.CounterVec
	CommandsTotal      *prometheus.CounterVec
	SamplesTotal       prometheus.Counter
	EventsTotal        *prometheus.CounterVec
	HostWarningsTotal  *prometheus.CounterVec
	// HostReportsRefused counts what a host reported that the api dropped
	// (I-445..I-447): foreign_guest (a guest of a project placed on
	// another host, or on none), project_cap (a project past its hourly
	// cap on stored guest events), bad_snapshot (a snapshot path outside
	// the project's prefix).
	HostReportsRefused *prometheus.CounterVec
	// SamplesFailed counts guests whose sample row was not stored as sent:
	// guest_fields (stored with the host-measured fields only), insert
	// (nothing stored).
	SamplesFailed       *prometheus.CounterVec
	EgressAlertProjects prometheus.Gauge
	// PartitionDropFailTotal is the input of the PartitionDropFail alert
	// (ops/alerts.yaml): the sample tables keep partitions past retention, so
	// Postgres grows and nothing else breaks.
	PartitionDropFailTotal prometheus.Counter
	BillingGapMinutes      prometheus.Counter
	KeyVaultErrorsTotal    prometheus.Counter
	// AbuseStopsTotal counts guests the api stopped by itself, by kind
	// (miner: DECISIONS I-239); MinerStopped alerts on any increase.
	AbuseStopsTotal *prometheus.CounterVec
	// AbuseHeldProjects is the projects whose start is refused until an
	// operator runs `repose-admin abuse clear`.
	AbuseHeldProjects prometheus.Gauge
	// AbuseBusyUnattendedProjects is the BusyUnattended alert's input:
	// projects at full CPU on every vCPU for six hours with no session, no
	// tmux client and no agent (I-239), recomputed from meter_samples.
	AbuseBusyUnattendedProjects prometheus.Gauge
	// SeatsTotal and SeatsHeld are the fleet's seats (DECISIONS I-290):
	// the 8 GB blocks of the ready hosts (or SEATS_TOTAL) and those held
	// by live subscriptions and unexpired invitations.
	SeatsTotal prometheus.Gauge
	SeatsHeld  prometheus.Gauge
	// WaitlistWaiting is the users holding a place on the seats waitlist
	// (DECISIONS I-269, I-290), set by the invite tick; a queue that
	// grows is a host to add (RUNBOOK "Waitlist growing").
	WaitlistWaiting prometheus.Gauge
	// WaitlistJoinedTotal counts users put on the waitlist by a refused
	// checkout or POST /billing/waitlist; WaitlistInvitedTotal the
	// invitations the tick sent, WaitlistConvertedTotal the invited users
	// whose subscription arrived, WaitlistExpiredTotal the holds that ran
	// out.
	WaitlistJoinedTotal    prometheus.Counter
	WaitlistInvitedTotal   prometheus.Counter
	WaitlistConvertedTotal prometheus.Counter
	WaitlistExpiredTotal   prometheus.Counter
}

// New registers every family on reg.
func New(reg prometheus.Registerer) *M {
	m := &M{
		RequestsTotal:                prometheus.NewCounterVec(prometheus.CounterOpts{Name: "repose_api_requests_total", Help: "HTTP requests by route, method and status."}, []string{"route", "method", "status"}),
		RequestDuration:              prometheus.NewHistogramVec(prometheus.HistogramOpts{Name: "repose_api_request_duration_seconds", Help: "HTTP request latency by route.", Buckets: prometheus.DefBuckets}, []string{"route"}),
		Hosts:                        prometheus.NewGaugeVec(prometheus.GaugeOpts{Name: "repose_api_hosts", Help: "Hosts by state."}, []string{"state"}),
		Projects:                     prometheus.NewGaugeVec(prometheus.GaugeOpts{Name: "repose_api_projects", Help: "Projects by state and class."}, []string{"state", "class"}),
		ScheduleTotal:                prometheus.NewCounterVec(prometheus.CounterOpts{Name: "repose_api_schedule_total", Help: "Placement attempts by result."}, []string{"result"}),
		CertsIssuedTotal:             prometheus.NewCounter(prometheus.CounterOpts{Name: "repose_api_certs_issued_total", Help: "User certificates issued."}),
		CertsRevokedTotal:            prometheus.NewCounter(prometheus.CounterOpts{Name: "repose_api_certs_revoked_total", Help: "Certificates revoked."}),
		RollupLagSeconds:             prometheus.NewGauge(prometheus.GaugeOpts{Name: "repose_api_rollup_lag_seconds", Help: "Age of the newest rolled-up hour."}),
		RollupDuration:               prometheus.NewHistogram(prometheus.HistogramOpts{Name: "repose_api_rollup_duration_seconds", Help: "Hourly rollup duration.", Buckets: prometheus.ExponentialBuckets(0.01, 4, 8)}),
		NotifyTotal:                  prometheus.NewCounterVec(prometheus.CounterOpts{Name: "repose_api_notify_total", Help: "Notification deliveries by channel and result."}, []string{"channel", "result"}),
		NotifyDeliveryLatencySeconds: prometheus.NewHistogram(prometheus.HistogramOpts{Name: "repose_api_notify_delivery_latency_seconds", Help: "Event timestamp to delivered timestamp, successful deliveries only.", Buckets: prometheus.ExponentialBuckets(1, 2, 12)}),
		OutboxDepth:                  prometheus.NewGauge(prometheus.GaugeOpts{Name: "repose_api_outbox_depth", Help: "Undelivered outbox rows."}),
		OutboxLagSeconds:             prometheus.NewGauge(prometheus.GaugeOpts{Name: "repose_api_outbox_lag_seconds", Help: "Age of the oldest undelivered outbox row."}),
		BillingWebhookTotal:          prometheus.NewCounterVec(prometheus.CounterOpts{Name: "repose_api_billing_webhook_total", Help: "Paddle webhook deliveries by event kind and result."}, []string{"kind", "result"}),
		BillingOverageChargesTotal:   prometheus.NewCounterVec(prometheus.CounterOpts{Name: "repose_api_billing_overage_charges_total", Help: "Egress overage lines sent to Paddle by result."}, []string{"result"}),
		BillingGateRefusedTotal:      prometheus.NewCounterVec(prometheus.CounterOpts{Name: "repose_api_billing_gate_refused_total", Help: "Compute refused with payment_required, by reason."}, []string{"reason"}),
		BillingSubscriptions:         prometheus.NewCounterVec(prometheus.CounterOpts{Name: "repose_api_billing_subscriptions_total", Help: "Subscription webhooks applied, by plan and status."}, []string{"plan", "status"}),
		BillingStopsTotal:            prometheus.NewCounterVec(prometheus.CounterOpts{Name: "repose_api_billing_stops_total", Help: "Machines the api stopped for billing, by reason."}, []string{"reason"}),
		SnapshotAgeSeconds:           prometheus.NewGauge(prometheus.GaugeOpts{Name: "repose_api_snapshot_age_seconds", Help: "Oldest newest-snapshot age over running projects."}),
		GRPCStreams:                  prometheus.NewGauge(prometheus.GaugeOpts{Name: "repose_api_grpc_streams", Help: "Connected host streams."}),
		OpsTotal:                     prometheus.NewCounterVec(prometheus.CounterOpts{Name: "repose_api_ops_total", Help: "Ops finished by kind and state."}, []string{"kind", "state"}),
		OpsOpen:                      prometheus.NewGaugeVec(prometheus.GaugeOpts{Name: "repose_api_ops_open", Help: "Ops pending or running by kind."}, []string{"kind"}),
		BuildDuration:                prometheus.NewHistogramVec(prometheus.HistogramOpts{Name: "repose_api_build_duration_seconds", Help: "Build op duration by result.", Buckets: prometheus.ExponentialBuckets(1, 2, 12)}, []string{"result"}),
		SecretsOpsTotal:              prometheus.NewCounterVec(prometheus.CounterOpts{Name: "repose_api_secrets_ops_total", Help: "Secret operations by op."}, []string{"op"}),
		CommandsTotal:                prometheus.NewCounterVec(prometheus.CounterOpts{Name: "repose_api_commands_total", Help: "hostd commands by kind and result."}, []string{"kind", "result"}),
		SamplesTotal:                 prometheus.NewCounter(prometheus.CounterOpts{Name: "repose_api_samples_total", Help: "Sample messages ingested."}),
		EventsTotal:                  prometheus.NewCounterVec(prometheus.CounterOpts{Name: "repose_api_events_total", Help: "Events ingested by kind."}, []string{"kind"}),
		HostWarningsTotal:            prometheus.NewCounterVec(prometheus.CounterOpts{Name: "repose_api_host_warnings_total", Help: "Host warnings by kind."}, []string{"kind"}),
		HostReportsRefused:           prometheus.NewCounterVec(prometheus.CounterOpts{Name: "repose_api_host_reports_refused_total", Help: "Host events, Hello entries and samples the api dropped, by reason."}, []string{"reason"}),
		SamplesFailed:                prometheus.NewCounterVec(prometheus.CounterOpts{Name: "repose_api_samples_failed_total", Help: "Guests whose sample row was not stored as sent, by reason."}, []string{"reason"}),
		PartitionDropFailTotal:       prometheus.NewCounter(prometheus.CounterOpts{Name: "repose_api_partition_drop_fail_total", Help: "Partition maintenance runs that failed (docs/workstreams/10-observability.md §6)."}),
		EgressAlertProjects:          prometheus.NewGauge(prometheus.GaugeOpts{Name: "repose_api_egress_alert_projects", Help: "Projects over 1 TB egress in 24 h."}),
		BillingGapMinutes:            prometheus.NewCounter(prometheus.CounterOpts{Name: "repose_api_billing_gap_minutes_total", Help: "Minutes a running project had no sample."}),
		KeyVaultErrorsTotal:          prometheus.NewCounter(prometheus.CounterOpts{Name: "repose_api_keyvault_errors_total", Help: "Key Vault failures."}),
		AbuseStopsTotal:              prometheus.NewCounterVec(prometheus.CounterOpts{Name: "repose_api_abuse_stops_total", Help: "Guests the api stopped for abuse, by kind."}, []string{"kind"}),
		AbuseHeldProjects:            prometheus.NewGauge(prometheus.GaugeOpts{Name: "repose_api_abuse_held_projects", Help: "Projects whose start is refused until repose-admin abuse clear."}),
		AbuseBusyUnattendedProjects:  prometheus.NewGauge(prometheus.GaugeOpts{Name: "repose_api_abuse_busy_unattended_projects", Help: "Projects at full CPU on every vCPU for 6 h with no session, tmux client or agent."}),
		SeatsTotal:                   prometheus.NewGauge(prometheus.GaugeOpts{Name: "repose_api_seats_total", Help: "Seats on the fleet: 8 GB blocks of the ready hosts, or SEATS_TOTAL."}),
		SeatsHeld:                    prometheus.NewGauge(prometheus.GaugeOpts{Name: "repose_api_seats_held", Help: "Seats held by live subscriptions and unexpired waitlist invitations."}),
		WaitlistWaiting:              prometheus.NewGauge(prometheus.GaugeOpts{Name: "repose_api_waitlist_waiting", Help: "Users waiting on the seats waitlist."}),
		WaitlistJoinedTotal:          prometheus.NewCounter(prometheus.CounterOpts{Name: "repose_api_waitlist_joined_total", Help: "Users put on the seats waitlist."}),
		WaitlistInvitedTotal:         prometheus.NewCounter(prometheus.CounterOpts{Name: "repose_api_waitlist_invited_total", Help: "Waitlist invitations the api sent."}),
		WaitlistConvertedTotal:       prometheus.NewCounter(prometheus.CounterOpts{Name: "repose_api_waitlist_converted_total", Help: "Invited users whose subscription arrived."}),
		WaitlistExpiredTotal:         prometheus.NewCounter(prometheus.CounterOpts{Name: "repose_api_waitlist_expired_total", Help: "Waitlist invitations whose 72-hour hold ran out."}),
	}
	// The alert on stops reads increase(); a series that exists from start
	// is what lets the first stop register as one.
	m.AbuseStopsTotal.WithLabelValues("miner")
	for _, r := range []string{"past_due", "ended", "egress"} {
		m.BillingStopsTotal.WithLabelValues(r)
	}
	m.BillingWebhookTotal.WithLabelValues("none", "bad_signature")
	m.BillingOverageChargesTotal.WithLabelValues("ok")
	m.BillingOverageChargesTotal.WithLabelValues("error")
	for _, r := range RefusedReasons {
		m.HostReportsRefused.WithLabelValues(r)
	}
	for _, r := range []string{"guest_fields", "insert"} {
		m.SamplesFailed.WithLabelValues(r)
	}
	reg.MustRegister(m.RequestsTotal, m.RequestDuration, m.Hosts, m.Projects, m.ScheduleTotal, m.CertsIssuedTotal, m.CertsRevokedTotal,
		m.RollupLagSeconds, m.RollupDuration, m.NotifyTotal, m.NotifyDeliveryLatencySeconds, m.OutboxDepth, m.OutboxLagSeconds, m.BillingWebhookTotal, m.BillingOverageChargesTotal, m.BillingGateRefusedTotal, m.BillingSubscriptions, m.BillingStopsTotal, m.SnapshotAgeSeconds,
		m.GRPCStreams, m.OpsTotal, m.OpsOpen, m.BuildDuration, m.SecretsOpsTotal, m.CommandsTotal, m.SamplesTotal, m.EventsTotal,
		m.HostWarningsTotal, m.HostReportsRefused, m.SamplesFailed, m.EgressAlertProjects, m.BillingGapMinutes, m.KeyVaultErrorsTotal,
		m.PartitionDropFailTotal, m.AbuseStopsTotal, m.AbuseHeldProjects, m.AbuseBusyUnattendedProjects,
		m.SeatsTotal, m.SeatsHeld, m.WaitlistWaiting, m.WaitlistJoinedTotal, m.WaitlistInvitedTotal, m.WaitlistConvertedTotal, m.WaitlistExpiredTotal)
	return m
}

// RefusedReasons is the reason enum of repose_api_host_reports_refused_total.
var RefusedReasons = []string{"foreign_guest", "project_cap", "bad_snapshot"}

// NewNop returns metrics on a private registry (tests). It is the checked
// registry of internal/obs/metrics, so a series that breaks the naming rules
// of docs/workstreams/10-observability.md §5 fails a unit test rather than a
// deployment.
func NewNop() *M { return New(obsmetrics.New(obs.ComponentAPI)) }
