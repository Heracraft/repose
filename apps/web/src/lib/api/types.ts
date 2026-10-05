// Shapes copied from docs/interfaces/api.md. This file is the thin typed
// client's contract; a field added there without a matching field here is a
// bug in this file, not a reason to read the response as `any`.

export type SizeClass = 'small' | 'large' | 'xl';

export type GuestState =
	| 'creating'
	| 'building'
	| 'starting'
	| 'running'
	| 'stopping'
	| 'stopped'
	| 'restoring'
	| 'destroying'
	| 'destroyed'
	| 'error';

/** GET /me's billing.status: a projection of the subscription (I-289).
 * `none` is an account with no plan yet, `trial` a trialing one. */
export type BillingStatus = 'none' | 'trial' | 'active' | 'past_due' | 'suspended' | 'exempt';

export type PlanId = 'solo' | 'plus' | 'pro';

/** The subscription's own status, Paddle's words (docs/interfaces/api.md GET /billing). */
export type SubscriptionStatus = 'trialing' | 'active' | 'past_due' | 'paused' | 'canceled';

/** `detail.reason` of a `payment_required` refusal (api.md "Usage and billing"). */
export type PaymentRequiredReason =
	| 'subscription_required'
	| 'plan_limit'
	| 'disk_limit'
	| 'egress_limit'
	| 'past_due'
	| 'suspended';

export type ErrorCode =
	| 'unauthenticated'
	| 'forbidden'
	| 'not_found'
	| 'invalid'
	| 'conflict'
	| 'payment_required'
	| 'capacity'
	| 'waitlisted'
	| 'rate_limited'
	| 'internal'
	| 'billing_disabled';

export interface ApiErrorBody {
	code: ErrorCode;
	message: string;
	detail?: Record<string, unknown>;
}

export interface Me {
	id: string;
	handle: string;
	email: string;
	/** null for an account that signed up with email and never linked GitHub. */
	github_login: string | null;
	tz: string;
	created_at: string;
	billing: {
		status: BillingStatus;
		plan: PlanId | null;
		seats: number;
		period_end: string | null;
		trial_end: string | null;
		cancel_at: string | null;
		/** Whether a subscription exists; kept one release (I-289). */
		has_card: boolean;
		/** Always 0 since I-289; kept one release. */
		trial_credit_cents: number;
	};
	limits: {
		projects: number;
		xl: number;
		memory_gb: number;
		disk_gb: number;
		egress_gb: number;
	};
	notify?: {
		email: boolean;
		ntfy_url: string | null;
	};
	/** The place on the waitlist while the user holds one (I-269, I-290). */
	waitlist?: WaitlistPlace | null;
}

/** The user's place on the waitlist (I-290): invited_at and hold_until are
 * set once a seat is held for them, for 72 hours. */
export interface WaitlistPlace {
	position: number;
	joined_at: string;
	invited_at?: string | null;
	hold_until?: string | null;
}

/** GET /billing's subscription, null without one. */
export interface Subscription {
	plan: PlanId;
	status: SubscriptionStatus;
	seats: number;
	period_start: string;
	period_end: string;
	next_billed_at: string | null;
	trial_end: string | null;
	cancel_at: string | null;
	scheduled_plan: PlanId | null;
}

/** GET /billing's usage: this period's, or the last 30 days without a plan. */
export interface Usage {
	running_gb: number;
	memory_gb: number;
	disk_allocated_gb: number;
	disk_gb: number;
	egress_gb: number;
	egress_included_gb: number;
	overage_cents: number;
	projects: number;
	project_limit: number;
}

/** One of GET /billing's plans (docs/PRICING.md). */
export interface Plan {
	id: PlanId;
	name: string;
	price_cents: number;
	currency: string;
	trial_days: number;
	seats: number;
	memory_gb: number;
	disk_gb: number;
	egress_gb: number;
	project_limit: number;
	/** Whether this plan's seats are free for this user right now. */
	available: boolean;
}

export interface Seats {
	total: number;
	held: number;
	free: number;
	waiting: number;
}

/** GET /public/seats, no auth: the landing page's count. */
export interface PublicSeats {
	total: number;
	free: number;
	waiting: number;
}

/** GET /billing (docs/interfaces/api.md "Usage and billing", I-289, I-290). */
export interface Billing {
	subscription: Subscription | null;
	usage: Usage;
	plans: Plan[];
	seats: Seats;
	waitlist: WaitlistPlace | null;
	paddle: {
		/** `fake` is internal/fakes/api: the dashboard uses window.__reposePaddleStub instead of Paddle.js. */
		environment: 'sandbox' | 'live' | 'fake';
		client_token: string;
	};
}

/** POST /billing/checkout's answer: what Paddle.js opens. */
export interface Checkout {
	transaction_id: string;
	client_token: string;
	environment: 'sandbox' | 'live' | 'fake';
}

export interface AgentSignal {
	agent: string;
	window: string;
	state: string;
}

export interface Signals {
	ssh_sessions: number;
	tmux_clients: number;
	agents: AgentSignal[];
	/** False when the newest sample found the environment's agent not answering (I-157). */
	guestd_ok?: boolean;
}

export interface Project {
	id: string;
	name: string;
	slug: string;
	remote_url: string;
	class: SizeClass;
	state: GuestState;
	host_id?: string;
	guest_ip?: string;
	agent_default: string;
	hold_base_updates: boolean;
	base_version: string;
	config_revision_id: string;
	volume_bytes: number;
	disk_used_bytes?: number;
	created_at: string;
	started_at?: string;
	signals?: Signals;
	cost_today_cents: number;
	cost_month_cents: number;
	last_snapshot_at?: string;
	/** The last failed op's "code: sentence" (I-159); null once an op succeeds. */
	last_error?: string | null;
	host_unreachable?: boolean;
	/** Set while it has run a day with no SSH session and no agent working
	 * (I-262). hourly_cents is 0 since plans (I-289) and is not shown. */
	idle?: { since: string; hourly_cents: number };
	/** Set while the project is temporary (`repose run --temp`, I-347): it is
	 * destroyed with no snapshot once this has passed. */
	expires_at?: string;
	/** The account's machine.nix is kept off this machine (I-490). */
	personal_opt_out?: boolean;
}

/** GET /projects/destroyed (I-167): a destroyed project that can still be restored. */
export interface DestroyedProject {
	id: string;
	name: string;
	slug: string;
	class: SizeClass;
	remote_url?: string | null;
	volume_bytes: number;
	destroyed_at: string;
	/** Whether a restore can take the old name (no live project holds it). */
	name_free: boolean;
	restorable_until?: string | null;
	snapshot: Snapshot;
}

/** POST /projects/restore's answer (I-167). */
export interface RestoreResult {
	op_id: string;
	project_id: string;
	name: string;
	slug: string;
	snapshot_id: string;
	snapshot_created_at: string;
	from_project_id: string;
}

export interface OpStatus {
	state: 'pending' | 'running' | 'done' | 'error';
	error?: string;
	log_url?: string;
}

/** One menu item (docs/interfaces/api.md "MenuSelection"): a catalog entry
 * with its enum options, or any nixpkgs package by attribute path
 * (DECISIONS I-220). */
export type MenuItem = { id: string; options?: Record<string, string> } | { package: string };

export type MenuSelection = MenuItem[];

export interface Config {
	revision_id: string;
	fragment: string;
	menu?: MenuSelection | null;
	base_version: string;
	applied_at?: string;
	/** The machine.nix text the active revision carries, '' for none (I-490). */
	personal?: string;
}

/** GET /me/config: the account's machine.nix (I-490). */
export interface PersonalConfig {
	revision_id: string | null;
	fragment: string;
	created_at: string | null;
	source: 'cli' | 'dashboard' | null;
	opted_out?: string[];
}

/** One project a machine.nix save reached. */
export interface PersonalChange {
	project_id: string;
	slug: string;
	revision_id: string;
	op_id?: string;
	merged?: boolean;
	running: boolean;
}

export interface PutPersonalResult extends PersonalConfig {
	projects: PersonalChange[];
	unchanged?: boolean;
}

export interface Revision {
	revision_id: string;
	created_at: string;
	status: 'building' | 'applied' | 'failed';
	error?: string;
}

export interface CatalogOption {
	id: string;
	type: string;
	values: string[];
	default: string;
}

export interface CatalogItem {
	id: string;
	label: string;
	group: string;
	kind: 'service' | 'package' | 'agent' | 'runtime';
	description: string;
	options?: CatalogOption[];
}

export interface SecretMeta {
	name: string;
	created_at: string;
	updated_at: string;
}

/** One bucket of GET /projects/:id/samples (api.md, I-492, I-493). */
export interface SamplePoint {
	ts: string;
	/** Share of the class's vCPUs used, 0 to 1. */
	cpu: number;
	/** Memory in use as the guest sees it; null from a guest older than I-493. */
	mem_used_bytes: number | null;
	/** Share of the time a task in the guest waited for a vCPU; null as above. */
	cpu_pressure: number | null;
	/** Share of the time the machine waited for a host CPU. */
	host_cpu_wait: number;
	disk_used_bytes: number;
}

export type SampleWindow = '1h' | '24h' | '7d';

export interface Samples {
	window: SampleWindow;
	step_s: number;
	vcpus: number;
	memory_bytes: number;
	points: SamplePoint[];
	procs: { comm: string; cpu_s: number; rss_max_bytes: number }[];
}

export interface Snapshot {
	id: string;
	created_at: string;
	bytes: number;
	reason: string;
	expires_at?: string | null;
}

export interface ProjectEvent {
	id: string;
	ts: string;
	kind: string;
	agent?: string;
	summary: string;
}

/** A repose-ask question (docs/interfaces/api.md "Questions", I-245). */
export type QuestionState = 'pending' | 'answered' | 'cancelled' | 'expired' | 'no_channel';

export interface Question {
	id: string;
	project_id: string;
	project: string;
	agent: string;
	window?: string;
	text: string;
	options: string[];
	state: QuestionState;
	answer: string | null;
	answered_via: 'dashboard' | 'cli' | 'ntfy' | 'email' | null;
	created_at: string;
	expires_at: string;
	answered_at: string | null;
}

export interface UsageRow {
	guest_hours: Partial<Record<SizeClass, number>>;
	gb_months: number;
	egress_gb: number;
	cost_cents: number;
}

/** One element of GET /billing/invoices: a Paddle transaction (I-289). */
export interface Invoice {
	id: string;
	number?: string | null;
	status: string;
	currency: string;
	amount_cents: number;
	subtotal_cents: number;
	tax_cents: number;
	created_at: string;
	period_start: string;
	period_end: string;
	hosted_url?: string | null;
	pdf_url?: string | null;
}

export interface Route {
	host_id: string;
	guest_ip: string;
	state: GuestState;
}
