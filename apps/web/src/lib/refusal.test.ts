import { describe, expect, it } from 'vitest';
import { withoutBillingURL } from './refusal';

const url = 'https://repose.herakraft.co/billing';

describe('withoutBillingURL', () => {
	it.each([
		[
			`Your Solo plan runs 8 GB at once and todo-app is using it. Stop it, or upgrade at ${url}.`,
			'Your Solo plan runs 8 GB at once and todo-app is using it. Stop it.'
		],
		[
			`Your Solo plan runs 8 GB at once and api and web are using it. Stop one, or upgrade at ${url}.`,
			'Your Solo plan runs 8 GB at once and api and web are using it. Stop one.'
		],
		[
			`Your Solo plan runs 8 GB at once and an xl machine needs 16 GB. Upgrade to Plus at ${url}.`,
			'Your Solo plan runs 8 GB at once and an xl machine needs 16 GB.'
		],
		[`Your Solo plan runs 8 GB at once. Upgrade at ${url}.`, 'Your Solo plan runs 8 GB at once.'],
		[`Choose a plan at ${url} first.`, 'Choose a plan first.'],
		[
			`Your last payment failed. Update your card at ${url} to start machines again.`,
			'Your last payment failed. Update your card to start machines again.'
		],
		['No URL here.', 'No URL here.']
	])('%s', (input, want) => {
		expect(withoutBillingURL(input)).toBe(want);
	});
});
