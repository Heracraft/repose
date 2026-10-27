// An api sentence quotes a command in backticks ("run `repose start`").
// The dashboard shows the command as inline code and never the backticks
// (I-837): the parts alternate between text and code, an unpaired
// backtick staying as text.

export interface TickPart {
	text: string;
	code: boolean;
}

export function splitTicks(s: string): TickPart[] {
	const parts = s.split('`');
	// An even count of pieces means an odd count of backticks: the last
	// one has no partner and stays in the text.
	if (parts.length % 2 === 0) {
		const tail = parts.splice(parts.length - 2, 2).join('`');
		parts.push(tail);
	}
	return parts
		.map((text, i) => ({ text, code: i % 2 === 1 }))
		.filter((p) => p.text !== '' || p.code);
}
