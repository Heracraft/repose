import adapter from '@sveltejs/adapter-node';
import { vitePreprocess } from '@sveltejs/vite-plugin-svelte';

/** @type {import('@sveltejs/kit').Config} */
const config = {
	preprocess: vitePreprocess(),
	kit: {
		// adapter-node, not adapter-auto: the Dockerfile (5.6) runs `node
		// build` directly, which adapter-auto cannot target ahead of time.
		adapter: adapter(),
		// The one Content-Security-Policy the app has, set here because no
		// layer in front of it (Coolify's Traefik, the edge) sets one. Its
		// point is script-src and frame-src: the only third-party script is
		// Paddle.js on the billing page and the only frame is Paddle's
		// checkout overlay (DECISIONS I-289). SvelteKit nonces its own
		// inline start script (mode auto; hashes on the prerendered legal
		// pages). connect-src cannot name the api and Logto, which are
		// runtime PUBLIC_* values, so it allows https and the loopback the
		// Playwright fixtures listen on. Svelte writes style attributes, so
		// style-src keeps 'unsafe-inline'; it also names the hosts Paddle.js
		// loads its overlay stylesheet from (DECISIONS I-600). The fonts are
		// served from static/fonts (DECISIONS I-371), so style-src and
		// font-src name no font CDN.
		csp: {
			mode: 'auto',
			directives: {
				'default-src': ['self'],
				// The hash is app.html's one inline script, which sets html.js
				// before the first paint (DECISIONS I-398); editing that script
				// changes it.
				'script-src': [
					'self',
					'sha256-aL3Pv6ygSyucrLwgv7XagHcNUqYEpcwpRZTImp8W7/g=',
					'https://cdn.paddle.com',
					'https://*.paddle.com'
				],
				'style-src': [
					'self',
					'unsafe-inline',
					'https://cdn.paddle.com',
					'https://sandbox-cdn.paddle.com'
				],
				'font-src': ['self'],
				'img-src': ['self', 'data:', 'https:'],
				'connect-src': [
					'self',
					'https:',
					'wss:',
					'http://127.0.0.1:*',
					'http://localhost:*',
					'ws://127.0.0.1:*',
					'ws://localhost:*'
				],
				'frame-src': [
					'https://*.paddle.com',
					'https://buy.paddle.com',
					'https://sandbox-buy.paddle.com'
				],
				'object-src': ['none'],
				'base-uri': ['self']
			}
		}
	}
};

export default config;
