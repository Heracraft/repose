import { describe, expect, it } from 'vitest';
import { machineNixLine, rebuildSummary } from './personal';

const ch = (slug: string, running: boolean) => ({
	project_id: slug,
	slug,
	revision_id: 'r',
	running
});

describe('rebuildSummary', () => {
	it('names running and stopped machines the way the CLI does', () => {
		expect(rebuildSummary([])).toBe('Every new machine gets it.');
		expect(rebuildSummary([ch('blog', true)])).toBe('blog switches in place.');
		expect(rebuildSummary([ch('blog', true), ch('api', true), ch('docs', false)])).toBe(
			'blog and api switch in place, docs at its next start.'
		);
		expect(rebuildSummary([ch('docs', false)])).toBe('docs switches at its next start.');
		expect(rebuildSummary([ch('a', false), ch('b', false)])).toBe(
			'a and b switch at their next start.'
		);
	});
});

describe('machineNixLine', () => {
	it('reads the line from a machine.nix location', () => {
		expect(machineNixLine("syntax error at machine.nix:2:3, unexpected ']'")).toBe(2);
		expect(machineNixLine('error at fragment.nix:4:1')).toBeUndefined();
		expect(machineNixLine(undefined)).toBeUndefined();
	});
});
