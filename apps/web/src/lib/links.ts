// The links every header and footer carries, named once so the header,
// the footer and the landing cannot point at two different places
// (DECISIONS I-655).
import { resolve } from '$app/paths';

/** The source, on GitHub. */
export const SOURCE_URL = 'https://github.com/Heracraft/repose';

/** The public feedback board, linked from every page (owner's ask, I-655). */
export const FEEDBACK_URL = 'https://repose.fider.io';

/**
 * Where a user writes when something went wrong that they cannot undo
 * themselves — today only a plan that was paid for and never arrived
 * (I-767). Named here so the product never grows a second support
 * address: CLAUDE.md, "Names are fixed". The feedback board above is
 * for feedback and stays where it is.
 */
export const SUPPORT_EMAIL = 'support@herakraft.co';
export const SUPPORT_MAILTO = `mailto:${SUPPORT_EMAIL}`;

/** The landing's pricing section, from any page. */
export function pricingHref(): string {
	return `${resolve('/')}#pricing`;
}
