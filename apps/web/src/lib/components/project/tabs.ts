// A project's five tabs (DECISIONS I-841), in order, with the path each
// adds to /projects/<id>. Pure, so the rule is tested without a browser.

export const PROJECT_TABS = [
	['Overview', ''],
	['Events', '/events'],
	['Usage', '/usage'],
	['Snapshots', '/snapshots'],
	['Config', '/config']
] as const;

export type ProjectTab = (typeof PROJECT_TABS)[number][0];

/** The tab a pathname under /projects/<id> is on; Overview for the project's own path. */
export function tabOf(pathname: string, id: string): ProjectTab {
	const base = `/projects/${id}`;
	if (!pathname.startsWith(base)) return 'Overview';
	const rest = pathname.slice(base.length).replace(/\/$/, '');
	return (
		PROJECT_TABS.find(([, p]) => p !== '' && (rest === p || rest.startsWith(`${p}/`)))?.[0] ??
		'Overview'
	);
}
