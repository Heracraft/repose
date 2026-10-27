import tailwindcss from '@tailwindcss/vite';
import { sveltekit } from '@sveltejs/kit/vite';
import { defineConfig } from 'vitest/config';

export default defineConfig({
	plugins: [tailwindcss(), sveltekit()],
	// Under Vitest, resolve Svelte's browser build, so a component test can
	// mount() into jsdom (ErrorView.test); the app's own build is unchanged.
	resolve: process.env.VITEST ? { conditions: ['browser'] } : undefined,
	ssr: {
		// Bundle every package into the server build. The runtime image holds
		// build/ and no node_modules (Dockerfile), so a package left external
		// failed at its first on-request render: /docs/<unknown slug>
		// answered 500, "Cannot find package 'marked'".
		noExternal: true
	},
	server: {
		// This dev box is reached over Tailscale, not localhost. Sign-in needs
		// a secure context (PKCE uses crypto.subtle), so `just web` puts the
		// dev server behind `tailscale serve` on the machine's ts.net name.
		host: '0.0.0.0',
		allowedHosts: ['.ts.net']
	},
	preview: {
		host: '0.0.0.0'
	},
	test: {
		// Playwright owns everything under tests/ (its own *.spec.ts files);
		// Vitest only sees the co-located unit tests under src/.
		include: ['src/**/*.test.ts'],
		environment: 'jsdom'
	}
});
