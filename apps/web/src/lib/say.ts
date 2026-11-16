// The rules and the words for a message to a project's agent (`repose
// say`, DECISIONS I-1144, I-1146, I-1162; docs/interfaces/api.md
// "Messages for an agent"). A leaf module, as $lib/busyAgents.ts is,
// pinned against the Go source by say.test.ts.
//
// Why the words live here and not at the call site: the api passes the
// machine's own refusal through where it names the agent windows
// (internal/api/http/say.go, sayErr), and the machine's sentence names a
// CLI flag (internal/guestd/sample/say.go: "Name one with --agent").
// A browser must never read that, so every refusal this feature can get
// has a sentence of its own here.
import { ApiError } from '$lib/api/errors';
import type { Project, Say } from '$lib/api/types';

/** The longest message, internal/api/say/say.go's TextCap. */
export const TEXT_CAP = 4 << 10;

/**
 * The agent windows on a running machine, named by the window and falling
 * back to the agent, as busyAgents() does. Empty for a machine that is not
 * running and for one with no agent: nothing to speak to.
 *
 * The dashboard knows the windows, so it always sends one and the api's
 * `several_agents` refusal is unreachable from here.
 */
export function sayWindows(p: Pick<Project, 'state' | 'signals'> | undefined): string[] {
	if (!p || p.state !== 'running' || !p.signals) return [];
	const out: string[] = [];
	for (const a of p.signals.agents ?? []) {
		const name = a.window || a.agent;
		if (name) out.push(name);
	}
	return out;
}

/**
 * The dashboard's sentence for a refusal of POST /projects/:id/say, by
 * `detail.reason` (internal/api/http/say.go's sayErr; api.md). undefined
 * for anything else, which the call site reports the way it reports any
 * other api error.
 */
export function sayRefusal(err: unknown): string | undefined {
	if (!(err instanceof ApiError)) return undefined;
	switch (err.detail?.reason) {
		case 'text_too_long':
			return 'A message is at most 4 KB.';
		case 'not_running':
			return 'The machine is not running.';
		case 'machine_unreachable':
			return 'The machine did not take the message.';
		case 'no_agent':
			return 'That agent is no longer running on this machine.';
		case 'several_agents':
			return 'The machine runs more than one agent now. Reload the page.';
		default:
			return undefined;
	}
}

/**
 * What the machine did with the message, in one line: the answer's own
 * `state` and `replaced` (api.md "Messages for an agent"). Every state has
 * its line here, `delivered` included, so the words have one home.
 */
export function sayState(s: Pick<Say, 'state' | 'window' | 'replaced'>): string {
	const who = s.window || 'the agent';
	if (s.state === 'waiting') {
		return s.replaced
			? `${who} is mid-turn. It gets this message at the end of the turn, in place of the one that was waiting.`
			: `${who} is mid-turn. It gets the message when the turn ends.`;
	}
	if (s.state === 'replaced') return `It replaced the message waiting for ${who}.`;
	if (s.state === 'failed') return `The machine did not type it into ${who}.`;
	return s.replaced
		? `Delivered to ${who}, in place of the message that was waiting.`
		: `Delivered to ${who}.`;
}
