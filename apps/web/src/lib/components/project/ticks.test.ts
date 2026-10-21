import { describe, expect, it } from 'vitest';
import { splitTicks } from './ticks';

describe('splitTicks', () => {
	it('turns backticked commands into code parts', () => {
		expect(splitTicks('Run `repose start` to try again.')).toEqual([
			{ text: 'Run ', code: false },
			{ text: 'repose start', code: true },
			{ text: ' to try again.', code: false }
		]);
	});

	it('leaves a sentence with no backticks whole', () => {
		expect(splitTicks('the machine did not boot.')).toEqual([
			{ text: 'the machine did not boot.', code: false }
		]);
	});

	it('keeps an unpaired backtick as text', () => {
		expect(splitTicks('a `b` c `d')).toEqual([
			{ text: 'a ', code: false },
			{ text: 'b', code: true },
			{ text: ' c `d', code: false }
		]);
	});

	it('starts with code when the sentence does', () => {
		expect(splitTicks('`repose keep` keeps it')).toEqual([
			{ text: 'repose keep', code: true },
			{ text: ' keeps it', code: false }
		]);
	});
});
