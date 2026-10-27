import { redirect } from '@sveltejs/kit';
import { resolve } from '$app/paths';
import type { PageLoad } from './$types';

// A project's secrets are a section of its Config tab now (DECISIONS
// I-841). Docs and bookmarks still link here; a client-side load, since
// the dashboard has no server render (ssr = false), so the fragment
// survives.
export const load: PageLoad = ({ params }) => {
	redirect(307, `${resolve('/projects/[id]/config', { id: params.id })}#secrets`);
};
