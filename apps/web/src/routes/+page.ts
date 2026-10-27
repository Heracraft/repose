// The landing is prerendered into the served HTML, as the legal pages
// and the docs are: the headline, the lead and the plans paint from the
// first response instead of after the bundle runs, and a crawler or a
// browser with scripts off reads them. The terminal starts in onMount,
// and the sign-in buttons render signed out and swap in place once auth
// is known (+page.svelte, authAction), so nothing moves when the page
// hydrates (I-398). The rest of the app stays a client-rendered SPA
// (+layout.ts).
export const ssr = true;
export const prerender = true;
