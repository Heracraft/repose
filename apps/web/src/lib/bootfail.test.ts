import { describe, expect, it } from 'vitest';
import { bootFallbackReason } from './bootfail';

const le =
	"boot_failed: its new system did not boot, so it runs its previous one: the system it boots is missing from the machine's store (stage 1 found no stage 2 init); `repose logs --kind console` shows what the new one printed";

describe('bootFallbackReason', () => {
	it('is the sentence for a running project on its previous system', () => {
		expect(bootFallbackReason({ state: 'running', last_error: le })).toBe(
			le.slice('boot_failed: '.length)
		);
	});
	it('is the sentence for a running project whose new system a GC hid', () => {
		const hidden =
			"a nix garbage collection inside the machine hid parts of the new system, so it keeps its current one; `repose stop` then `repose start` repairs the machine's store and applies the new system";
		expect(
			bootFallbackReason({ state: 'running', last_error: 'store_path_hidden: ' + hidden })
		).toBe(hidden);
	});
	it('is empty for another reason, another state, or none', () => {
		expect(bootFallbackReason({ state: 'running', last_error: 'internal: switch failed' })).toBe(
			''
		);
		expect(bootFallbackReason({ state: 'error', last_error: le })).toBe('');
		expect(bootFallbackReason({ state: 'running', last_error: null })).toBe('');
	});
});
