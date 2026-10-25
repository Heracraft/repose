// The primitives gallery, for building and reviewing pages in `vite dev`
// only (Guide B 16): its figures are samples, so a production build answers
// 404 here and nothing links to it.
import { dev } from '$app/environment';
import { error } from '@sveltejs/kit';

export const prerender = false;

export function load() {
	if (!dev) error(404, 'Not found');
}
