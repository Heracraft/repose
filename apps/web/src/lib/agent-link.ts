// The link an agent attached with `--link URL --label TEXT` (DECISIONS
// I-931), as the dashboard draws it: the label and the host, so a label
// the agent wrote cannot say the link goes somewhere it does not. The api
// checked it (internal/msglink); this keeps to the same rule again, since
// an href is drawn from it.

export interface AgentLink {
	url: string;
	label?: string;
}

/** The host a link names: with its port, the loopback addresses as localhost. */
export function linkHost(url: URL): string {
	const h = url.hostname.replace(/^\[|\]$/g, '');
	const loop = h === 'localhost' || h === '::1' || /^127\./.test(h);
	const host = loop ? 'localhost' : h;
	return url.port ? `${host}:${url.port}` : host;
}

/** The link's href and text, or undefined for one that is not http or https. */
export function agentLink(l: AgentLink | undefined): { href: string; text: string } | undefined {
	if (!l?.url) return undefined;
	let u: URL;
	try {
		u = new URL(l.url);
	} catch {
		return undefined;
	}
	if ((u.protocol !== 'http:' && u.protocol !== 'https:') || u.username || u.password)
		return undefined;
	const host = linkHost(u);
	return { href: u.href, text: l.label ? `${l.label} · ${host}` : `Open ${host}` };
}
