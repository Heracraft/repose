// A dashboard page's <title> (DECISIONS I-841): "Projects · repose", and
// "(2) Projects · repose" while agents wait on two questions, so a
// background tab says so. Read inside a template or $derived, the prefix
// follows the shared count.
import { pendingQuestions } from './questions.svelte';

/** `page` is the page's own part: "Projects", "Events · job-board", "Secrets · Settings". */
export function title(page: string): string {
	const n = pendingQuestions.count;
	return `${n > 0 ? `(${n}) ` : ''}${page} · repose`;
}
