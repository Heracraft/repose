<script lang="ts">
	import { onMount } from 'svelte';
	import { goto } from '$app/navigation';
	import { resolve } from '$app/paths';
	import { toast } from 'svelte-sonner';
	import { SIGN_IN_FAILED, adoptServerSession, handleSignInCallback } from '$lib/auth.svelte';
	import type { PageData } from './$types';

	let { data }: { data: PageData } = $props();

	onMount(async () => {
		try {
			if (data.adopt) {
				// A flow `GET /signin` started: the server did the exchange,
				// because it, and not this page, held the PKCE verifier
				// (DECISIONS I-878).
				await adoptServerSession(data.adopt.idToken, data.adopt.refreshToken);
				// eslint-disable-next-line svelte/no-navigation-without-resolve -- the path comes from the cookie, which only ever holds a same-origin path (lib/signin, safeReturnPath)
				await goto(data.adopt.returnTo);
			} else {
				await handleSignInCallback(location.href);
				await goto(resolve('/projects'));
			}
		} catch {
			toast.error(SIGN_IN_FAILED);
			await goto(resolve('/'));
		}
	});
</script>

<svelte:head>
	<title>Signing in… — repose</title>
</svelte:head>

<!-- A <main> like every other page's, so the layout's skip link has its
     target here too. A visitor can reach this page with scripts off now
     that sign-in starts at a link (DECISIONS I-878); they are signed in
     with Logto by this point, and nothing further can happen, so the
     waiting line is replaced rather than left spinning for ever.
     app.html marks a scripted page with html.js before the first paint. -->
<main id="main">
	<p class="signing p-16 text-center text-ink-muted" role="status">Signing in…</p>
	<p class="scriptless p-16 text-center text-ink-muted">The dashboard needs JavaScript.</p>
</main>

<style>
	/* One of the two, never both, and neither is in the accessibility
	   tree while it is the hidden one. A <noscript> would read better but
	   its children are only parsed when the parser itself had scripting
	   off, which is not the same condition. */
	:global(html:not(.js)) .signing {
		display: none;
	}
	:global(html.js) .scriptless {
		display: none;
	}
</style>
