// The one table of a run's reasons and the words every surface prints
// for it, from internal/triggerrun/triggerrun.go (DECISIONS I-1256).
// The CLI, the api and hostd read the Go copy; this is the same table
// for the browser, and apps/web/src/lib/triggers.test.ts holds the two
// to each other, because the first forked copies were already missing
// `clock_late` by the time they were written.
export const TRIGGER_REASONS: Record<string, string> = {
	off: 'the trigger is off',
	stopped: 'the machine was stopped',
	busy: 'the machine was busy',
	error: 'the machine is in error',
	route_gone: 'the route was removed',
	payload_too_large: 'the event was over 1 MiB',
	too_many_fires: 'more than 60 fires this hour',
	duplicate: 'a duplicate of an event already seen',
	gated: 'the gate refused it',
	gate_timeout: 'the gate took more than 10 seconds',
	concurrency: 'a run is working',
	plan_limit: "your plan's memory is in use",
	prompt_missing: 'the prompt file is missing',
	prompt_too_large: 'the prompt file is over 64 KB',
	no_agent: 'the agent is not installed on this machine',
	not_logged_in: 'the agent is not logged in',
	no_permission_mode: 'the agent stopped at a permission prompt',
	timeout: 'killed at the timeout',
	guest_restart: "the machine's agent supervisor restarted",
	machine_stopped: 'the machine stopped',
	clock_late: 'the fire was late and was not caught up',
	unreachable: 'the machine did not take the fire',
	payload_gone: "that run's payload is no longer kept",
	dry_run: 'the gate ran and no agent was started',
	herdr: 'this machine runs under herdr, and a trigger starts a tmux window'
};

/**
 * The words a surface prints for a reason. A `gated` reason may carry
 * the gate's own last line after a colon, which is kept; a word this
 * build does not know (a newer api) is printed as it came.
 */
export function reasonSentence(reason?: string): string {
	if (!reason) return '';
	if (TRIGGER_REASONS[reason]) return TRIGGER_REASONS[reason];
	if (reason.startsWith('gated: ')) return `${TRIGGER_REASONS.gated}: ${reason.slice(7)}`;
	return reason;
}
