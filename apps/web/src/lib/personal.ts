// The account's machine.nix on the dashboard (DECISIONS I-490): the
// sentence a save ends with, and the line an error names.
import type { PersonalChange } from '$lib/api/types';

function join(names: string[]): string {
	if (names.length <= 1) return names.join('');
	return `${names.slice(0, -1).join(', ')} and ${names[names.length - 1]}`;
}

/** "blog switches in place, docs at its next start." The CLI says the same (rebuildWhat). */
export function rebuildSummary(changes: PersonalChange[]): string {
	const running = changes.filter((c) => c.running).map((c) => c.slug);
	const stopped = changes.filter((c) => !c.running).map((c) => c.slug);
	const parts: string[] = [];
	if (running.length > 0) {
		parts.push(`${join(running)} ${running.length === 1 ? 'switches' : 'switch'} in place`);
	}
	if (stopped.length > 0) {
		const when = stopped.length === 1 ? 'at its next start' : 'at their next start';
		if (running.length > 0) parts.push(`${join(stopped)} ${when}`);
		else parts.push(`${join(stopped)} ${stopped.length === 1 ? 'switches' : 'switch'} ${when}`);
	}
	if (parts.length === 0) return 'Every new machine gets it.';
	return `${parts.join(', ')}.`;
}

/** The machine.nix line a message names (machine.nix:L:C), if any. */
export function machineNixLine(message: string | undefined): number | undefined {
	if (!message) return undefined;
	const m = /machine\.nix:(\d+)/.exec(message);
	return m ? Number(m[1]) : undefined;
}
