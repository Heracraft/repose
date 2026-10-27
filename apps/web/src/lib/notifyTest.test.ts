import { describe, expect, it } from 'vitest';
import { testReport } from './notifyTest';

describe('testReport', () => {
	it('names every channel that got the test', () => {
		expect(testReport({ email: 'ok' })).toEqual({
			tone: 'ok',
			text: 'Test notification sent by email.'
		});
		expect(testReport({ email: 'ok', ntfy: 'ok', gchat: 'ok' }).text).toBe(
			'Test notification sent by email, ntfy and Google Chat.'
		);
	});
	it('says which channel refused it and why, once', () => {
		expect(testReport({ email: 'ok', ntfy: 'error', ntfy_error: '404 Not Found.' })).toEqual({
			tone: 'partly',
			text: 'Test notification sent by email. Not sent by ntfy (404 Not Found).'
		});
	});
	it('says when nothing got it', () => {
		expect(testReport({ email: 'error', gchat: 'error', gchat_error: 'webhook deleted' })).toEqual({
			tone: 'failed',
			text: 'Test notification not sent by email or Google Chat (webhook deleted).'
		});
		expect(testReport({}).text).toBe('No channel is on.');
	});
});
