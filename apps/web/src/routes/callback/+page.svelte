<script lang="ts">
	import { onMount } from 'svelte';
	import { goto } from '$app/navigation';
	import { resolve } from '$app/paths';
	import { toast } from 'svelte-sonner';
	import {
		SIGN_IN_FAILED,
		adoptServerSession,
		handleSignInCallback,
		takeReturnPath
	} from '$lib/auth.svelte';
	import type { PageData } from './$types';

	let { data }: { data: PageData } = $props();

	onMount(async () => {
		try {
			if (data.adopt) {
				// A flow `GET /signin` started: the server did the exchange,
				// because it, and not this page, held the PKCE verifier, and
				// the page to come back to rode in the same cookie
				// (DECISIONS I-878).
				await adoptServerSession(data.adopt.idToken, data.adopt.refreshToken);
				await goto(resolve(data.adopt.returnTo));
			} else {
				await handleSignInCallback(location.href);
				await goto(resolve(takeReturnPath() ?? '/projects'));
			}
		} catch {
			toast.error(SIGN_IN_FAILED);
			await goto(resolve('/'));
		}
	});
</script>

<svelte:head>
	<title>Signing in · repose</title>
</svelte:head>

<!-- A <main> like every other page's, so the layout's skip link has its
     target here too; on the frame where the page it hands over to starts.
     A visitor can reach this page with scripts off now that sign-in starts
     at a link (DECISIONS I-878). They are signed in with Logto by then and
     nothing further can happen, so the waiting line is replaced rather
     than left spinning for ever; app.html marks a scripted page with
     html.js before the first paint (I-398). A <noscript> would read better
     but its children are only parsed when the parser itself had scripting
     off, which is not the same condition. -->
<main id="main" tabindex="-1" class="frame callback">
	<p class="signing" role="status">Signing in…</p>
	<p class="scriptless">The dashboard needs JavaScript.</p>
</main>

<style>
	.callback {
		padding-top: 2.25rem;
	}
	p {
		margin: 0;
		font-size: 0.9375rem;
		color: var(--hk-text-3);
	}
	:global(html:not(.js)) .signing {
		display: none;
	}
	:global(html.js) .scriptless {
		display: none;
	}
</style>
