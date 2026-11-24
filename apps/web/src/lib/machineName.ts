// The name a new machine is offered (DECISIONS I-1602): the repository's
// own name when one is typed, as `repose run` names a machine after its
// checkout, else two plain words, as Codespaces and Railway name theirs.
// The user can type over it; the api still holds the rule and the
// duplicates, so this only has to be a name it usually takes.

const FIRST = [
	'amber',
	'brisk',
	'calm',
	'clear',
	'cool',
	'crisp',
	'dry',
	'early',
	'even',
	'fair',
	'fresh',
	'glad',
	'grey',
	'just',
	'keen',
	'late',
	'light',
	'long',
	'mild',
	'neat',
	'north',
	'plain',
	'quick',
	'quiet',
	'rapid',
	'ready',
	'round',
	'sharp',
	'short',
	'slow',
	'small',
	'snug',
	'solid',
	'south',
	'spare',
	'steady',
	'still',
	'swift',
	'tidy',
	'warm'
];
const SECOND = [
	'alder',
	'aspen',
	'basin',
	'birch',
	'brook',
	'cairn',
	'cedar',
	'cliff',
	'cove',
	'creek',
	'delta',
	'dune',
	'fern',
	'field',
	'fjord',
	'grove',
	'harbor',
	'heath',
	'hill',
	'inlet',
	'lake',
	'larch',
	'ledge',
	'marsh',
	'meadow',
	'mesa',
	'moor',
	'oak',
	'pine',
	'pond',
	'reef',
	'ridge',
	'river',
	'shore',
	'slope',
	'spruce',
	'stone',
	'tarn',
	'vale',
	'willow'
];

/** Two words, never one of `taken` (the account's names, any case). */
export function wordName(taken: Iterable<string>, random: () => number = Math.random): string {
	const have = new Set([...taken].map((t) => t.toLowerCase()));
	for (let i = 0; i < 50; i++) {
		const n = `${FIRST[Math.floor(random() * FIRST.length)]}-${SECOND[Math.floor(random() * SECOND.length)]}`;
		if (!have.has(n)) return n;
	}
	return `machine-${Math.floor(random() * 9000 + 1000)}`;
}

/**
 * The repository's last path segment, in the characters the api's name
 * rule takes ([A-Za-z0-9._-]{1,64}), lowercased; '' when there is none.
 * `https://github.com/you/Todo-App.git` gives `todo-app`.
 */
export function repoName(remote: string): string {
	const path = remote
		.trim()
		.replace(/\.git\/?$/i, '')
		.replace(/\/+$/, '');
	const last = path.split(/[/:]/).pop() ?? '';
	return last
		.toLowerCase()
		.replace(/[^a-z0-9._-]+/g, '-')
		.replace(/^-+|-+$/g, '')
		.slice(0, 64);
}
