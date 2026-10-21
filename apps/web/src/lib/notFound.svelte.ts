// A page drawn as a 404 inside a layout that rendered (a project that does
// not exist, or was destroyed) is not a SvelteKit error, so page.error stays
// null there. The page raises this flag while it draws its ErrorView, and the
// rail marks no place current, as on a routed 404 (DECISIONS I-841).
export const notFound = $state({ on: false });
