import type { Project } from './api/types';

/**
 * The api's sentence for a running project whose new system did not boot,
 * so that it runs its previous one (DECISIONS I-590), from last_error
 * "boot_failed: its new system did not boot, so it runs its previous one: ...",
 * or whose switch was refused because a nix garbage collection inside it
 * hid the new system (I-589), "store_path_hidden: ...".
 * Empty for any other project.
 */
export function bootFallbackReason(p: Pick<Project, 'state' | 'last_error'>): string {
	if (p.state !== 'running' || !p.last_error) return '';
	for (const prefix of ['boot_failed: ', 'store_path_hidden: ']) {
		if (p.last_error.startsWith(prefix)) return p.last_error.slice(prefix.length);
	}
	return '';
}
