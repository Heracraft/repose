// Phrasings of help nobody asked for (DECISIONS I-485): reassurance,
// narrating the screen, telling the reader what comes next. The docs test
// and the copy test hold every page, screen and email to this list; the
// CLI's twin is TestCLIReassures in internal/cli. A grep catches only
// these; prose judgement stays with review (docs/CHECKLIST.md).
export const UNASKED: RegExp[] = [
	/\bnothing to type\b/i,
	/\bdon'?t worry\b/i,
	/\bno need to worry\b/i,
	/\bthat'?s it\b/i,
	/\bthat'?s all\b/i,
	/\byou'?re all set\b/i,
	/\byou can now\b/i,
	/\bsimply\b/i,
	/\bjust (run|type|open|click)\b/i,
	/\byou'?re looking at\b/i,
	/\bas (mentioned|noted|described) (above|earlier|before)\b/i,
	/\bin other words\b/i,
	/\bnext,? (read|see|head|go)\b/i,
	/^#+ next steps?\b/im,
	/\bin (just )?(a few )?seconds\b.*\bget started\b|\bget started in\b/i,
	/\bhappy (coding|hacking)\b/i
];

// unaskedIn lists each phrasing found in text, as "label: match".
export function unaskedIn(label: string, text: string): string[] {
	const hits: string[] = [];
	for (const re of UNASKED) {
		const m = text.match(re);
		if (m) hits.push(`${label}: "${m[0]}"`);
	}
	return hits;
}
