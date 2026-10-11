// The links every header and footer carries, named once so the header,
// the footer and the landing cannot point at two different places
// (DECISIONS I-655).
import { resolve } from '$app/paths';

/** The source, on GitHub. */
export const SOURCE_URL = 'https://github.com/Heracraft/repose';

/** The public feedback board, linked from every page (owner's ask, I-655). */
export const FEEDBACK_URL = 'https://repose.fider.io';

/** The landing's pricing section, from any page. */
export function pricingHref(): string {
	return `${resolve('/')}#pricing`;
}
