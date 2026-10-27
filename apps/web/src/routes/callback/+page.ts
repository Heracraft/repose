// The rest of the app is a client-rendered SPA (+layout.ts), but this one
// page is rendered on the server so that +page.server.ts runs on the
// request Logto redirects to: that is the request carrying the
// authorization code and the httpOnly cookie a server-started flow needs
// (DECISIONS I-878). The landing page overrides ssr the same way.
export const ssr = true;
