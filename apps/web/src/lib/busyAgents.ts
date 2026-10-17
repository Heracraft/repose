// Whether stopping a machine would end an agent's turn, and the sentence
// that says so. `repose stop` asks before it does this and refuses
// without a terminal unless --yes (docs/features/stop-start-destroy.md,
// I-614); the dashboard did it on one click, from a Stop revealed on
// hover inside a row whose name link covers the whole row. The rule and
// the words are the CLI's: internal/cli/lifecycle.go's busyAgentList and
// joinNames, pinned by busyAgents.test.ts.
import type { Project } from '$lib/api/types';

/** The agent states that a stop would interrupt. */
const BUSY: Record<string, string> = {
	working: 'working',
	needs_input: 'needs input'
};

/**
 * "claude (working)", "codex-2 (needs input)" for each agent mid-turn or
 * waiting on an answer, named by its window and falling back to the
 * agent. Empty for a machine that is not running: nothing to interrupt.
 */
export function busyAgents(p: Pick<Project, 'state' | 'signals'> | undefined): string[] {
	if (!p || p.state !== 'running' || !p.signals) return [];
	const out: string[] = [];
	for (const a of p.signals.agents ?? []) {
		const state = BUSY[a.state];
		if (!state) continue;
		out.push(`${a.window || a.agent} (${state})`);
	}
	return out;
}

/** "a, b and c", as the CLI joins a list of names. */
export function joinNames(names: string[]): string {
	if (names.length === 0) return '';
	if (names.length === 1) return names[0];
	return `${names.slice(0, -1).join(', ')} and ${names[names.length - 1]}`;
}

/**
 * The warning before a stop that would interrupt an agent, in the CLI's
 * words: "api has claude (working). Stopping ends it." Empty when
 * nothing would be interrupted, which is the signal to stop without
 * asking.
 */
export function stopWarning(p: Pick<Project, 'slug' | 'state' | 'signals'> | undefined): string {
	const names = busyAgents(p);
	if (names.length === 0 || !p) return '';
	const ends = names.length === 1 ? 'Stopping ends it.' : 'Stopping ends them.';
	return `${p.slug} has ${joinNames(names)}. ${ends}`;
}
