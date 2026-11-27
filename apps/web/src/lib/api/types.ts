import type { AgentLink } from '$lib/agent-link';
// Shapes copied from docs/interfaces/api.md. This file is the thin typed
// client's contract; a field added there without a matching field here is a
// bug in this file, not a reason to read the response as `any`.

export type SizeClass = 'tiny' | 'small' | 'large' | 'xl';

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

/** A plan id. `own` is the Own hardware plan: any number of hosts the user owns, and
 *  none of repose's memory, disk or egress (I-1364). */
export type PlanId = 'solo' | 'plus' | 'pro' | 'own';

/** The subscription's own status, Polar's words (docs/interfaces/api.md GET /billing). */
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
	| 'billing_disabled'
	// A Nix file sent to an Ubuntu machine, and a config change made over a
	// newer revision than the client loaded (api.md, I-1462, I-1528).
	| 'nix_config_retired'
	| 'menu_stale'
	// The browser view's own refusals (DECISIONS I-1216, api.md).
	| 'view_expired'
	| 'view_busy'
	| 'view_budget'
	| 'view_capacity'
	| 'unavailable';

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
		/** How many hosts of their own the account may register (I-1364);
		 *  absent from an api older than it. */
		hosts?: number;
	};
	notify?: {
		email: boolean;
		ntfy_url: string | null;
		/** The Google Chat channel (I-692): the webhook's space and when
		 * Google refused it. The webhook URL itself is never sent back. */
		gchat?: { space: string; broken_at: string | null } | null;
		/**
		 * How long a report waits while you are typing in the machine,
		 * in seconds; 0 is off (I-1282). Absent from an api before it.
		 */
		hold_seconds?: number;
	};
	/**
	 * The account's GitHub answer (I-1340, I-1342). Absent from an api
	 * before it, which means the laptops copy the gh login as they
	 * always have.
	 */
	github?: {
		/** Whether a laptop may copy this account's GitHub CLI login
		 * into its machines. It can only subtract: a laptop that has
		 * turned the copy off stays off whatever this says. */
		login_copy: boolean;
		/** What GitHub said about the account's GH_TOKEN when it was
		 * stored, null when it said nothing or the secret is gone. */
		token_expires_at: string | null;
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
	/** What Polar charges at next_billed_at, in cents (I-497). */
	next_charge_cents?: number | null;
	/** When the introductory price ends; null for none or not yet fixed (I-497). */
	intro_until?: string | null;
}

/** GET /billing's usage: this period's, or the last 30 days without a plan. */
export interface Usage {
	running_gb: number;
	memory_gb: number;
	/** What the projects' volumes hold, the figure the plan's disk counts
	 *  (I-585); absent from an api older than that. */
	disk_held_gb?: number;
	/** The same figure rounded up to a GB, kept one release (I-585). */
	disk_allocated_gb: number;
	disk_gb: number;
	egress_gb: number;
	egress_included_gb: number;
	overage_cents: number;
	projects: number;
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
	/** How many hosts of their own the plan allows: 1 on Own hardware, 0 on
	 *  the hosted plans (I-1364). A plan with hosts > 0 sells none of
	 *  repose's memory, disk or egress, so those three read 0 and mean
	 *  "the plan caps nothing"; the host's own memory and disk do. */
	hosts?: number;
	/** Whether this plan's seats are free for this user right now. */
	available: boolean;
	/** The introductory price a month and for how many months, Solo's founding
	 *  price; 0 for none (I-497, I-672). */
	intro_price_cents?: number;
	intro_months?: number;
	/** The egress allowance a month while the introductory offer runs; 0 keeps
	 *  egress_gb, as Solo's founding price does (I-497, I-672). */
	intro_egress_gb?: number;
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
	/** Places left at Solo's founding price; null when there is no offer (I-672). */
	founding_left: number | null;
}

/** GET /billing (docs/interfaces/api.md "Usage and billing", I-289, I-290). */
export interface Billing {
	subscription: Subscription | null;
	usage: Usage;
	plans: Plan[];
	/** Whether this user's checkout of a plan with an introductory price gets it (I-497). */
	intro_eligible?: boolean;
	seats: Seats;
	waitlist: WaitlistPlace | null;
}

/** POST /billing/checkout's answer: Polar's hosted checkout, where the browser goes (I-604). */
export interface Checkout {
	url: string;
}

export interface AgentSignal {
	agent: string;
	window: string;
	state: string;
	/**
	 * An opaque id for the agent, never a process number, or its terminal (I-1676). An agent outside any
	 * terminal repose can see (Claude Desktop's) has an id and an empty
	 * window and terminal. An older api sends neither.
	 */
	agent_id?: string;
	/** `tmux:<window>`, `herdr:<key>`, or empty for no terminal. */
	terminal?: string;
}

/** Whether an agent runs outside any terminal repose can see (I-1676). */
export function noTerminal(a: AgentSignal): boolean {
	return !a.window && !!a.agent_id;
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
	/** The name the account gave the host it is on, for a host of its own
	 * only (I-1392); a host repose runs has host_id alone. */
	host_name?: string;
	/** False for a machine on a host of the account's own: it shares that
	 * host's memory and cores and has no size, so `class` says nothing
	 * (I-1630, I-1632). Absent from an older api, which means sized. */
	sized?: boolean;
	/** Memory the running machine uses, as it sees it, from the newest
	 * sample (I-1632); absent when it is not running or not sampled yet. */
	mem_used_bytes?: number;
	guest_ip?: string;
	agent_default: string;
	hold_base_updates: boolean;
	base_version: string;
	config_revision_id: string;
	volume_bytes: number;
	/** The volume's allocated blocks, which keep a deleted file's blocks
	 * until the daily fstrim; not shown (I-567, I-585). */
	disk_used_bytes?: number;
	/** The guest's root filesystem from the newest sample (I-567); absent
	 * from a guest older than it or one that did not answer. */
	root_used_bytes?: number;
	root_size_bytes?: number;
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
	/** Set while the machine is frozen: nobody is using it, so its cores
	 * are stopped and every process is held (I-818). A marker on
	 * `running`, never a state of its own; the word a reader sees is
	 * "frozen" (I-683). */
	resting?: {
		since: string;
		/** On a Mac, a machine resting as a save that the locked Mac cannot
		 * read: it wakes once its user unlocks it (I-1682). */
		waits_for?: 'unlock' | 'login';
	} | null;
	/** Set while a running machine's guestd has not answered for about
	 * three minutes (I-1652): hung, its memory full or its kernel stuck.
	 * A Start restarts it, which loses what is in its memory. */
	not_answering?: { since: string } | null;
	/** Set while a machine on a Mac without the appliance is parked and
	 * waits for the Mac's user to log in: its memory is kept and it runs
	 * again at the login (I-1672). Never with not_answering. */
	parked?: { since: string; waits_for: string } | null;
	/** Set while the project's create waits for a host of the account's
	 * own to finish installing (I-1700): the machine starts there once it
	 * registers. `provider` is `mac` for a Mac added from that Mac. */
	waiting_for_host?: { name: string; provider: string } | null;
	/** On a running machine of a host that sizes each start (a Mac), the
	 * size its VM got and its size with the host to itself (I-1680). */
	vm?: {
		vcpus: number;
		mem_bytes: number;
		full_vcpus: number;
		full_mem_bytes: number;
		sized_down: boolean;
	} | null;
	/** The machine is never frozen while idle (I-818, I-826); the switch
	 * is on its Config page. Absent from an older api, which means off. */
	keep_awake?: boolean;
	/** Set while the project is temporary (`repose run --temp`, I-347): it is
	 * destroyed with no snapshot once this has passed. */
	expires_at?: string;
	/** The account's machine.nix is kept off this machine (I-490). */
	personal_opt_out?: boolean;
	/** What runs the machine's terminals from its next start (I-502).
	 * Absent from an older api, which means tmux. */
	multiplexer?: 'tmux' | 'herdr';
	/** Where the machine goes when it is placed (I-1366): `own` is a
	 * host this account owns when it owns any and the hosted fleet when
	 * it owns none, `hosted` the fleet whatever it owns. Absent from an
	 * older api, which means own. */
	placement?: Placement;
	/** What the machine boots, for life (I-1462): `nix`, or `erofs`, the
	 * Ubuntu base configured by the menu alone. Absent from an older api,
	 * which means nix. */
	guest_runtime?: GuestRuntime;
	/** erofs only: the newer base the machine boots at its next start
	 * (I-1462); null otherwise. */
	pending_base_version?: string | null;
	/** The checkout's name under /home/dev as the CLI last found it
	 * (I-1610): "" when the machine has none, null until a CLI has said.
	 * Absent from an older api, which means unknown. */
	checkout?: string | null;
}

export type GuestRuntime = 'nix' | 'erofs';

export type Placement = 'own' | 'hosted';

/**
 * One host the account owns, of GET /hosts (DECISIONS I-1366): a machine
 * of their own that runs their projects and nobody else's. `mem_bytes`,
 * `vcpus` and `cores` are 0 and `arch`, `last_heartbeat_at` and
 * `registered_at` are absent until it registers. `join_token` and
 * `install` come on the POST answer alone.
 */
export interface OwnedHost {
	id: string;
	name: string;
	state: HostState;
	provider?: string;
	arch?: string;
	mem_bytes: number;
	vcpus: number;
	cores: number;
	/** The account's live projects placed on it. */
	projects: number;
	/** Those of them in a state that holds memory. */
	running_projects: number;
	/** What a host whose hostd shares it last reported free (I-1654); a
	 * machine starts there at 1 GB free (I-1630). */
	free_mem_bytes?: number;
	/** On a Mac, the machines its cores run at once (I-1640, I-1654). */
	running_limit?: number;
	/** Snapshots kept on its disk, which removing it deletes (I-1542). */
	snapshots?: number;
	/** A Mac that parks its machines before a restart, so Restart keeps
	 * their memory (I-1690). */
	can_restart?: boolean;
	last_heartbeat_at?: string;
	registered_at?: string;
	created_at: string;
	/** The POST answer alone carries these three. */
	join_token?: string;
	join_token_expires_at?: string;
	/**
	 * What to do with the host (DECISIONS I-1366, I-1380). `command` is
	 * the whole line to run on the machine, from the deployment's
	 * `host_install_command`, and is `""` until an operator records one
	 * — which is every deployment today, so the fallback is the normal
	 * case. `argument` is the join bundle `repose host add --join`
	 * takes: the token and, where the deployment has a hub, the
	 * WireGuard material the machine needs before it can reach the api
	 * at all. The bare `join_token` is neither of these and is an input
	 * to the api rather than something a user runs.
	 */
	install?: { command: string; argument?: string; url: string };
}

/** hosts.state: `registering` is minted and never used, `unreachable` is
 * 90 seconds without a heartbeat, which for a machine at home is
 * switched off. */
export type HostState = 'registering' | 'ready' | 'draining' | 'unreachable' | 'retired' | 'lost';

/**
 * POST /projects/:id/browser/view (DECISIONS I-1216): the answer to a
 * mint. The ticket itself is in the cookie the answer sets, and there is
 * no password in the body on this path: the guest's own never leaves the
 * edge. `resting` is a machine whose cores are stopped that this mint did
 * not wake, because one was woken for it inside the last ten minutes.
 */
export interface ViewTicket {
	expires_at: string;
	stream_path: string;
	resting: boolean;
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
	/** Its snapshot went with a removed host of the account's own (I-1542). */
	gone_with_host?: string | null;
	snapshot: Snapshot;
	/** False for one that ran on a host of the account's own, where a
	 * machine has no size (I-1654). Absent from an older api: sized. */
	sized?: boolean;
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

/** A host op (GET /hosts/:id/ops/:op_id, I-1690): a restart's answer. */
export interface HostOp {
	state: 'pending' | 'running' | 'done' | 'error';
	error?: { code: string; message: string } | string;
	result?: { parked?: number; not_parked?: string[]; restarting?: boolean; park_ms?: number };
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

/** One delivery a provider made to a webhook route (DECISIONS I-1110). */
export interface WebhookDelivery {
	at: string;
	status: number;
	outcome: string;
	duration_ms: number;
	bytes_in: number;
	bytes_out: number;
}

/** A project's public webhook route. `url` carries the route's token. */
export interface WebhookRoute {
	route_id: string;
	name: string;
	port: number;
	path: string;
	allow_start: boolean;
	/** The trigger that owns this route, absent for a route of the user's own (I-1250). */
	trigger?: string;
	url: string;
	created_at: string;
	updated_at: string;
	last_delivery?: WebhookDelivery;
	deliveries?: WebhookDelivery[];
}

// Triggers (DECISIONS I-1248..I-1262): an event or a time starts an
// agent session on the machine. `prompt_path` is a path in the machine;
// the prompt's text and every payload stay there and never reach the
// api, which is why neither is in these shapes.
export interface TriggerRun {
	run_id: string;
	trigger_id?: string;
	trigger: string;
	source: 'route' | 'clock' | 'hand';
	state: 'queued' | 'working' | 'done' | 'failed' | 'timeout' | 'skipped';
	reason?: string;
	summary?: string;
	/** The agent's `--link URL --label TEXT` (I-931), as an event's. */
	link?: AgentLink;
	window?: string;
	dedupe_key?: string;
	payload_bytes: number;
	payload_kept: boolean;
	/** Above 1 on a row that stands for several refusals in one hour. */
	skips?: number;
	started_machine: boolean;
	started_at?: string;
	finished_at?: string;
	created_at: string;
}

export interface Trigger {
	trigger_id: string;
	name: string;
	kind: 'route' | 'schedule';
	route?: { name: string; url: string };
	spec?: string;
	zone?: string;
	next_at?: string;
	prompt_path: string;
	workdir: string;
	agent?: string;
	timeout_s: number;
	concurrency: number;
	gate?: string;
	dedupe?: string;
	notify: 'failed' | 'never';
	enabled: boolean;
	allow_start: boolean;
	stop_after: boolean;
	catch_up: boolean;
	created_at: string;
	updated_at: string;
	last_run?: TriggerRun;
	runs?: TriggerRun[];
}

export interface SecretMeta {
	name: string;
	created_at: string;
	updated_at: string;
}

/** One row of GET /me/tokens (DECISIONS I-1314): everything but the token. */
export interface ApiToken {
	id: string;
	name: string;
	prefix: string;
	scopes: TokenScope[];
	project: { id: string; slug: string | null } | null;
	expires_at: string;
	created_at: string;
	last_used_at: string | null;
	revoked_at: string | null;
	/** Set on the create answer only. */
	token?: string;
}

export type TokenScope = 'read' | 'projects' | 'config' | 'secrets' | 'triggers' | 'agents';

/** The six scopes and one line each, the api's tokens.Means word for word. */
export const TOKEN_SCOPES: { name: TokenScope; means: string }[] = [
	{
		name: 'read',
		means: "every project's status, events, questions, triggers and settings"
	},
	{ name: 'projects', means: 'create, start, stop, resize, snapshot, restore and remove projects' },
	{ name: 'config', means: "change machine.nix and a project's config" },
	{ name: 'secrets', means: 'set and remove secrets (never read a value)' },
	{ name: 'triggers', means: 'make, change, fire and remove triggers and webhook routes' },
	{ name: 'agents', means: 'message an agent and answer its questions' }
];

/** POST /me/tokens. */
export interface CreateTokenRequest {
	name: string;
	scopes: TokenScope[];
	project_id?: string;
	expires_in_days?: number;
}

/** PUT /me/secrets/:name: the running machines that got it now, and the
 * projects whose own secret of that name wins (I-697). */
export interface AccountSecretPut {
	name: string;
	pushed: number;
	overridden_by: string[];
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
	/** The account's own host it was kept on when that host was removed,
	 * which deleted it (I-1542); null otherwise and from an older api. */
	gone_with_host?: string | null;
}

export interface ProjectEvent {
	id: string;
	ts: string;
	kind: string;
	agent?: string;
	/** The tmux window the agent ran in, when the event names one. */
	window?: string;
	summary: string;
	/** Who raised it: `api` for repose's own notices, `host` or `http`
	 * for a machine's (api.md's events route). */
	source?: string;
	/** The agent's `--link URL --label TEXT` (I-931). */
	link?: AgentLink;
	/**
	 * Each channel's outcome: a send time, `held` while the presence hold
	 * has it (I-689), `dropped: resolved`, or a failure. The row says so
	 * only when nothing went out (I-1280).
	 */
	delivered?: Record<string, string>;
}

/** One page of events and, from an api with I-662, how many match in all. */
export interface EventsPage {
	events: ProjectEvent[];
	total?: number;
}

/** A repose-ask question (docs/interfaces/api.md "Questions", I-245). */
export type QuestionState = 'pending' | 'answered' | 'cancelled' | 'expired' | 'no_channel';

export type QuestionVia = 'dashboard' | 'cli' | 'ntfy' | 'email' | 'gchat' | 'terminal' | 'agent';

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
	/**
	 * Where it was answered or closed: `terminal` is an answer typed to the
	 * agent itself (state answered, answer null), `agent` a withdrawal by
	 * the agent (state cancelled), I-690; `gchat` is Google Chat, I-692.
	 */
	answered_via: QuestionVia | null;
	created_at: string;
	expires_at: string;
	answered_at: string | null;
	/**
	 * The named secret the question asks for (`repose-ask --secret`, I-929).
	 * Its answer is the value, stored as that secret, never in `answer`.
	 */
	secret?: string;
	/** The agent's `--link URL --label TEXT` (I-931). */
	link?: AgentLink;
}

/**
 * A message the owner typed for a project's agent (docs/interfaces/api.md
 * "Messages for an agent", DECISIONS I-1144). `text` is tenant content:
 * shown to the owner, typed into the agent's terminal, never logged.
 * `window` is the agent window the machine resolved it to, as `repose ps`
 * names it. `delivered` means the text is in the agent's terminal now;
 * `waiting` means the agent was mid-turn and the machine types it in at
 * the end of the turn. One message waits per window, so a second while
 * one waits answers `replaced: true` and the earlier row moves to
 * `replaced`.
 */
export interface Say {
	id: string;
	project_id: string;
	window: string;
	text: string;
	sender?: string;
	state: 'delivered' | 'waiting' | 'replaced' | 'failed';
	replaced?: boolean;
	created_at: string;
}

export interface UsageRow {
	guest_hours: Partial<Record<SizeClass, number>>;
	gb_months: number;
	egress_gb: number;
	cost_cents: number;
}

/** One element of GET /billing/invoices: a Polar order (I-604). */
export interface Invoice {
	id: string;
	number?: string | null;
	/** Polar's order status: `paid`, `pending`, `refunded`, `partially_refunded`. */
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
