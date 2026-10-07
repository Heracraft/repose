import type { Project } from './api/types';

/**
 * The api's sentence for a running project whose new system did not boot,
 * so that it runs its previous one (DECISIONS I-590), from last_error
 * "boot_failed: its new system did not boot, so it runs its previous one: ...".
 * Empty for any other project.
 */
export function bootFallbackReason(p: Pick<Project, 'state' | 'last_error'>): string {
	if (p.state !== 'running' || !p.last_error) return '';
	const prefix = 'boot_failed: ';
	return p.last_error.startsWith(prefix) ? p.last_error.slice(prefix.length) : '';
}
