<!--
  Make a project from the browser (DECISIONS I-1354, under I-1266).

  `POST /projects` is what the first `repose run` in a checkout calls, and
  nothing in it sends code to a machine: the CLI's own sync does that, and
  guestd sets `origin` on a checkout that is already there. So a project
  made here is an empty machine, and Repository is the remote this project
  is the machine for — what a later `repose run` in that checkout looks it
  up by — not a repository to clone. The field says so in those words,
  because a URL beside a Create button reads like a clone.

  Name, size and repository, then the lifetime, which is the only door to
  a temporary machine (`repose run --temp`) a browser has. The api refuses
  a remote beside a lifetime, so the repository field gives way to the one
  sentence that says why.

  The name rule and the duplicates are the api's: it holds the names and
  the remotes, so its sentence is the one shown rather than a second copy
  of the rule that could drift from it.
-->
<script lang="ts">
	import { resolve } from '$app/paths';
	import { createProject } from '$lib/api/client';
	import { ApiError, errorText } from '$lib/api/errors';
	import { toastApiError } from '$lib/api/toast';
	import { classMemoryGB, normalizeRemoteDisplay } from '$lib/format';
	import { withoutBillingURL } from '$lib/refusal';
	import { focusOnMount } from '$lib/focus';
	import type { Project } from '$lib/api/types';

	let {
		onsaved,
		oncancel
	}: {
		onsaved: (p: Project) => void;
		oncancel: () => void;
	} = $props();

	/** interfaces/README.md's classes, in the order the docs table has them. */
	const CLASSES = ['tiny', 'small', 'large', 'xl'];
	/** `repose run --temp`'s range, 10 minutes to 24 hours (I-347). */
	const LIVES: { value: number; label: string }[] = [
		{ value: 3600, label: '1 hour' },
		{ value: 4 * 3600, label: '4 hours' },
		{ value: 12 * 3600, label: '12 hours' },
		{ value: 24 * 3600, label: '24 hours' }
	];

	// This browser's zone, which is the laptop's zone for the user who is
	// at both: what the CLI sends from the laptop (I-198), read by the
	// machine's shells and its schedules. An engine that answers nothing
	// leaves the project to the account's zone.
	const zone = Intl.DateTimeFormat().resolvedOptions().timeZone || '';

	let name = $state('');
	// The api's own default, so a user who leaves it alone gets what
	// `repose run` gives them.
	let cls = $state('large');
	let repo = $state('');
	/** 0 keeps the machine; anything else makes it temporary. */
	let life = $state(0);
	let saving = $state(false);
	let error = $state<string | undefined>(undefined);
	/** A plan or capacity refusal: the api's sentence plus the plan's page. */
	let billingRefusal = $state(false);

	async function onsubmit(event: SubmitEvent) {
		event.preventDefault();
		if (saving) return;
		const n = name.trim();
		if (n === '') {
			error = 'Name is required.';
			return;
		}
		error = undefined;
		billingRefusal = false;
		saving = true;
		try {
			const p = await createProject({
				name: n,
				class: cls,
				// The lookup form of the remote, as the CLI sends it.
				...(life === 0 && repo.trim() !== ''
					? { remote_url: normalizeRemoteDisplay(repo) }
					: undefined),
				...(life === 0 ? undefined : { expires_in_s: life }),
				...(zone ? { tz: zone } : undefined)
			});
			onsaved(p);
		} catch (err) {
			// The api owns the name rule, the duplicate name, the duplicate
			// remote, the project cap and the plan, and each answer is a
			// whole sentence for the reader.
			if (err instanceof ApiError && err.code !== 'internal') {
				// payment_required ends by naming the billing page's URL; the
				// link beside it is this page's own (refusal.ts).
				billingRefusal = err.code === 'payment_required';
				const text = errorText(err, 'Could not create the project.');
				error = billingRefusal ? withoutBillingURL(text) : text;
			} else {
				toastApiError(err, 'Could not create the project.');
				error = 'Could not create it. Try again.';
			}
		} finally {
			saving = false;
		}
	}
</script>

<section class="hk-section" aria-labelledby="np-title">
	<div class="hk-section-head">
		<h2 id="np-title" class="hk-h2-app">New project</h2>
	</div>
	<form class="npform" {onsubmit} data-testid="new-project-form">
		<div class="hk-fields npfields">
			<div class="npfield">
				<label for="np-name" class="hk-field-label">Name</label>
				<input
					id="np-name"
					class="hk-input"
					autocomplete="off"
					spellcheck="false"
					placeholder="todo-app"
					aria-invalid={error ? 'true' : undefined}
					aria-describedby={error ? 'np-error' : undefined}
					bind:value={name}
					use:focusOnMount
				/>
			</div>
			<div class="npfield">
				<label for="np-class" class="hk-field-label">Size</label>
				<select id="np-class" class="hk-select" bind:value={cls}>
					{#each CLASSES as c (c)}
						<option value={c}>{c} · {classMemoryGB(c)} GB</option>
					{/each}
				</select>
			</div>
			<div class="npfield">
				<label for="np-life" class="hk-field-label">Keep it</label>
				<select id="np-life" class="hk-select" bind:value={life}>
					<option value={0}>Until I destroy it</option>
					{#each LIVES as l (l.value)}
						<option value={l.value}>{l.label}</option>
					{/each}
				</select>
			</div>
		</div>

		{#if life === 0}
			<div class="npfield">
				<label for="np-repo" class="hk-field-label">Repository</label>
				<input
					id="np-repo"
					class="hk-input npwide"
					autocomplete="off"
					spellcheck="false"
					placeholder="github.com/you/todo-app"
					bind:value={repo}
				/>
				<p class="nphelp">
					Optional, and not cloned: the remote this project is the machine for, so
					<code class="hk-mono">repose run</code> in that checkout finds it instead of making another
					machine.
				</p>
			</div>
		{:else}
			<p class="nphelp" data-testid="np-temp-note">
				A temporary machine is destroyed when its time is up, with no snapshot, and takes no
				repository.
			</p>
		{/if}

		{#if error}
			<p id="np-error" class="hk-field-error" data-testid="new-project-error">
				{error}
				{#if billingRefusal}<a href={resolve('/billing')}>See your plan</a>{/if}
			</p>
		{/if}

		<div class="hk-confirm-actions">
			<button type="submit" class="hk-btn hk-btn-sm" disabled={saving}
				>{saving ? 'Creating…' : 'Create'}</button
			>
			<button type="button" class="hk-btn-ghost hk-btn-sm" onclick={oncancel}>Cancel</button>
		</div>
	</form>
</section>

<style>
	.npform {
		margin-top: 1rem;
		display: flex;
		flex-direction: column;
		gap: 0.75rem;
	}
	/* The three short fields share a line from 640, as the trigger form's
	   do; below it each takes its own. */
	.npfields {
		display: grid;
		gap: 0.75rem;
		grid-template-columns: 1fr;
	}
	@media (min-width: 640px) {
		.npfields {
			grid-template-columns: repeat(auto-fit, minmax(10rem, 1fr));
		}
	}
	.npfield {
		display: flex;
		flex-direction: column;
		gap: 0.25rem;
		min-width: 0;
	}
	/* A remote is long; a name and a size are not. */
	.npwide {
		max-width: 28rem;
	}
	/* A line about one field stays over that field's half of the frame
	   rather than running its whole width. */
	.nphelp {
		margin: 0;
		max-width: 44rem;
		font-size: 0.8125rem;
		color: var(--hk-muted);
	}
</style>
