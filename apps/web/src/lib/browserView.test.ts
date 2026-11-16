import { describe, expect, it } from 'vitest';
import { VIEW_RETRIES, viewEnded, viewStreamUrl } from './browserView';

// The stream's URL is the api's origin and the path the mint answered
// with, as ws(s): PUBLIC_API_URL carries /v1 and so does stream_path, so
// the path replaces it rather than being appended (api.md).
describe('viewStreamUrl', () => {
	it('puts the mint path on the api origin, over wss', () => {
		expect(viewStreamUrl('https://api.repose.run/v1', '/v1/browser/stream')).toBe(
			'wss://api.repose.run/v1/browser/stream'
		);
	});
	it('keeps a development api on ws and its port', () => {
		expect(viewStreamUrl('http://127.0.0.1:8080/v1', '/v1/browser/stream')).toBe(
			'ws://127.0.0.1:8080/v1/browser/stream'
		);
	});
	it('is empty without an api url, which the page shows as a refusal', () => {
		expect(viewStreamUrl('', '/v1/browser/stream')).toBe('');
		expect(viewStreamUrl('https://api.repose.run/v1', '')).toBe('');
	});
});

// A close the api explained is not reconnected to, because every connect
// mints and a mint is where a frozen machine wakes (I-1216).
describe('viewEnded', () => {
	it('reconnects after a transport error and after the guest closed', () => {
		expect(viewEnded(undefined)).toEqual({ retry: true });
		expect(viewEnded('')).toEqual({ retry: true });
		expect(viewEnded('closed')).toEqual({ retry: true });
	});

	it('stops with a line after idle, four hours and the byte budget', () => {
		for (const reason of ['idle', 'cap', 'budget']) {
			const end = viewEnded(reason);
			expect(end.retry, reason).toBe(false);
			expect(end.note, reason).toBeTruthy();
		}
		expect(viewEnded('idle').note).toContain('two minutes');
		expect(viewEnded('budget').note).toContain('2 GB');
	});

	it('stops on any other word the api may add', () => {
		expect(viewEnded('too_large')).toEqual({ retry: false, note: 'The view stopped.' });
		expect(viewEnded('error')).toEqual({ retry: false, note: 'The view stopped.' });
	});

	it('waits 1, 2 then 4 seconds, and no fourth time', () => {
		expect([...VIEW_RETRIES]).toEqual([1000, 2000, 4000]);
	});
});
