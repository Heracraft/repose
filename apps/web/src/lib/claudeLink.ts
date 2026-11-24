/**
 * Claude Desktop's link to a new session on an SSH host
 * (code.claude.com/docs/en/desktop, "Open an SSH session from a link";
 * DECISIONS I-1606). `ssh_host` is the project's alias in the user's own
 * ssh config, which names the user and the certificate; `ssh_folder` is
 * the checkout the CLI last told the api about (I-1610), the home when
 * the machine has none, and `~` while nobody has said. The link carries
 * no prompt, user, token or project id. The CLI builds the same link
 * (internal/cli/code.go, claudeDesktopLink).
 */

/** internal/checkoutname.Valid: "" or one folder name of these characters. */
const CHECKOUT = /^[A-Za-z0-9._-]{1,100}$/;

export function checkoutFolder(checkout: string | null | undefined): string {
	if (checkout === '') return '/home/dev';
	if (
		typeof checkout !== 'string' ||
		!CHECKOUT.test(checkout) ||
		checkout === '.' ||
		checkout === '..'
	)
		return '~';
	return `/home/dev/${checkout}`;
}

export function claudeLink(slug: string, checkout: string | null | undefined): string {
	// Every character of both values is one a query value carries as it is.
	return `claude://code/new?ssh_host=${encodeURIComponent(`${slug}.repose`)}&ssh_folder=${encodeURIComponent(checkoutFolder(checkout)).replaceAll('%2F', '/')}`;
}
