<!--
  New project on /projects (DECISIONS I-1602, I-1612, amending I-1354): one field
  and one button. The name is filled in and selected, so Create, or
  Enter, makes the machine at once; typing replaces it. Everything else
  has the default `repose run` gives and is shown as one line under the
  field, which opens Options: size, how long to keep it, where it runs
  (only for an account that owns a host, I-1390, I-1584) and the
  repository. A machine that runs on a host of the account's own has no
  size (I-1632), so Size is there only for repose's servers.

  `POST /projects` is what the first `repose run` in a checkout calls,
  and nothing in it sends code to a machine: a project made here is an
  empty machine, and Repository is the remote a later `repose run` in
  that checkout looks it up by, not a repository to clone (I-1354). A
  typed repository names the machine after itself while the name is
  still the one offered. The api refuses a remote beside a lifetime, so
  the field gives way to the one sentence that says why.

  The api holds the name rule, the duplicates, the plan and placement;
  its sentence is the one shown, the form keeps what was typed, and a
  refusal opens Options, where every other choice is.
-->
<script lang="ts">
	import { resolve } from '$app/paths';
	import { onMount } from 'svelte';
	import { createProject, listHosts } from '$lib/api/client';
	import { ApiError, errorText } from '$lib/api/errors';
	import { toastApiError } from '$lib/api/toast';
	import { classMemoryGB, classVCPUs, normalizeRemoteDisplay } from '$lib/format';
	import { hostStateWord } from '$lib/offlinehost';
	import { withoutBillingURL } from '$lib/refusal';
	import { repoName, wordName } from '$lib/machineName';
	import { defaultClass, fits, freeGB, type PlanRoom } from '$lib/planRoom';
	import type { OwnedHost, Placement, Project } from '$lib/api/types';

	let {
		taken = [],
		size,
		named,
		room,
		onstop,
		onsaved,
		oncancel
	}: {
		/** The account's project names, so the name offered is a free one. */
		taken?: string[];
		/** A size or a name from the page's URL (`/projects?new&size=small`). */
		size?: string;
		named?: string;
		/** The plan's memory and what holds it, where the plan meters it (I-1612). */
		room?: PlanRoom;
		/** Stops a machine that holds the plan's memory, as its row's Stop does. */
		onstop?: (id: string) => void;
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
	// at both: what the CLI sends from the laptop (I-198).
	const zone = Intl.DateTimeFormat().resolvedOptions().timeZone || '';

	// svelte-ignore state_referenced_locally
	const offered = named?.trim() || wordName(taken);
	let name = $state(offered);
	/** The name is still the one offered, so a repository may rename it. */
	// svelte-ignore state_referenced_locally
	let nameIsOurs = $state(!named);
	// The size the user chose (Options, or the link's `size`). Until they
	// choose, it is the api's own default, which `repose run` gets, or
	// the largest class the plan has room for now when that one would be
	// refused (I-1612). It follows the plan as machines stop and start.
	// svelte-ignore state_referenced_locally
	let chosen = $state<string | undefined>(size && CLASSES.includes(size) ? size : undefined);
	let cls = $derived(chosen ?? defaultClass(room));
	let free = $derived(freeGB(room));
	/** Nothing fits beside what runs now: the form says so before Enter. */
	let full = $derived(!!room && !fits(room, 'tiny'));
	let repo = $state('');
	/** 0 keeps the machine; anything else makes it temporary. */
	let life = $state(0);
	let hosts = $state<OwnedHost[]>([]);
	/** `own` is the api's default; `hosted` is repose's servers; a host's id names that host (I-1584). */
	let runsOn = $state<string>('own');
	let options = $state(false);
	let saving = $state(false);
	let error = $state<string | undefined>(undefined);
	let billingRefusal = $state(false);
	let nameField = $state<HTMLInputElement | undefined>();

	onMount(async () => {
		nameField?.focus();
		nameField?.select();
		// A side read: an account that owns no host gets the form without
		// Runs on, and so does one whose /hosts did not answer.
		try {
			const all = await listHosts({ passive: true });
			hosts = all.filter((h) => h.state !== 'retired' && h.state !== 'lost');
		} catch {
			hosts = [];
		}
	});

	function placementBody(v: string): { placement: Placement; host?: string } {
		if (v === 'own' || v === 'hosted') return { placement: v };
		return { placement: 'own', host: v };
	}
	function hostLabel(h: OwnedHost): string {
		return h.state === 'ready' ? h.name : `${h.name} · ${hostStateWord(h.state)}`;
	}
	function sizeLabel(c: string): string {
		const cores = classVCPUs(c);
		return `${c} · ${cores} ${cores === 1 ? 'core' : 'cores'} · ${classMemoryGB(c)} GB`;
	}

	/** It goes to a host of the account's own, where a machine has no
	 * size and shares the host's memory (I-1630, I-1632): no Size field,
	 * no size in the summary, and the plan's memory does not apply. */
	let onOwnHost = $derived(hosts.length > 0 && runsOn !== 'hosted');

	/** Where it runs, in the summary's words; '' for an account with no host. */
	let whereWords = $derived.by(() => {
		if (hosts.length === 0) return '';
		if (runsOn === 'hosted') return "on repose's servers";
		if (runsOn === 'own')
			return hosts.length === 1 ? `on ${hostLabel(hosts[0])}` : 'on any of your hosts';
		const h = hosts.find((x) => x.id === runsOn);
		return h ? `on ${hostLabel(h)}` : '';
	});
	let summary = $derived(
		[
			onOwnHost ? '' : sizeLabel(cls),
			room && !onOwnHost ? `${free} GB of your plan's ${room.limitGB} GB free` : '',
			whereWords,
			life === 0
				? 'kept until you destroy it'
				: `destroyed after ${LIVES.find((l) => l.value === life)?.label}`,
			life === 0 && repo.trim() ? normalizeRemoteDisplay(repo) : ''
		]
			.filter(Boolean)
			.join(' · ')
	);

	function onRepo(v: string) {
		repo = v;
		if (nameIsOurs) name = repoName(v) || offered;
	}

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
				...(zone ? { tz: zone } : undefined),
				...(hosts.length === 0 ? undefined : placementBody(runsOn))
			});
			onsaved(p);
		} catch (err) {
			if (err instanceof ApiError && err.code !== 'internal') {
				billingRefusal = err.code === 'payment_required';
				const text = errorText(err, 'Could not create the machine.');
				error = billingRefusal ? withoutBillingURL(text) : text;
				// Size, place and lifetime are what most refusals are about,
				// so the choices are in view next to the sentence.
				options = true;
			} else {
				toastApiError(err, 'Could not create the machine.');
				error = 'Could not create it. Try again.';
			}
		} finally {
			saving = false;
		}
	}

	function onkeydown(e: KeyboardEvent) {
		if (e.key === 'Escape') {
			e.preventDefault();
			oncancel();
		}
	}
</script>

<section class="hk-section" aria-labelledby="nm-title">
	<h2 id="nm-title" class="hk-sr">New project</h2>
	<!-- svelte-ignore a11y_no_noninteractive_element_interactions -->
	<form class="nmform" {onsubmit} {onkeydown} data-testid="new-machine-form">
		<div class="nmrow">
			<label for="nm-name" class="hk-field-label nmlabel">Name</label>
			<input
				id="nm-name"
				class="hk-input nmname"
				autocomplete="off"
				spellcheck="false"
				aria-invalid={error ? 'true' : undefined}
				aria-describedby={error ? 'nm-error' : 'nm-summary'}
				bind:this={nameField}
				bind:value={name}
				oninput={() => (nameIsOurs = false)}
			/>
			<div class="nmacts">
				<button type="submit" class="hk-btn" aria-disabled={saving ? 'true' : undefined}
					>{saving ? 'Creating…' : 'Create'}</button
				>
				<button type="button" class="hk-btn-ghost" onclick={oncancel}>Cancel</button>
			</div>
		</div>

		<div class="nmsummary">
			<button
				type="button"
				class="nmtoggle"
				aria-expanded={options}
				aria-controls="nm-options"
				onclick={() => (options = !options)}
			>
				<span id="nm-summary" data-testid="nm-summary">{summary}</span>
				<span class="nmmore">{options ? 'Hide options' : 'Options'}</span>
			</button>
		</div>

		{#if room && !error && !onOwnHost && !fits(room, cls)}
			<!-- The api would refuse this size by the plan (I-1612): said
			     before Enter, with the way out beside it. -->
			<div class="nmroom" data-testid="nm-room">
				<p>
					{full
						? `Your plan's ${room.limitGB} GB is in use. Stop a machine to make one here.`
						: `A ${cls} machine needs ${classMemoryGB(cls)} GB; ${free} GB is free.`}
				</p>
				{#if full && onstop}
					<div class="nmstops">
						{#each room.holders as h (h.id)}
							<button type="button" class="hk-btn-secondary hk-btn-sm" onclick={() => onstop(h.id)}
								>Stop {h.name}</button
							>
						{/each}
					</div>
				{/if}
			</div>
		{/if}

		{#if error}
			<p id="nm-error" class="hk-field-error" data-testid="new-machine-error">
				{error}
				{#if billingRefusal}<a href={resolve('/billing')}>See your plan</a>{/if}
			</p>
		{/if}

		{#if options}
			<div id="nm-options" class="nmoptions">
				{#if !onOwnHost}
					<div class="nmfield">
						<label for="nm-size" class="hk-field-label">Size</label>
						<select id="nm-size" class="hk-select" bind:value={() => cls, (v) => (chosen = v)}>
							{#each CLASSES as c (c)}
								<option value={c}>{sizeLabel(c)}</option>
							{/each}
						</select>
					</div>
				{/if}
				<div class="nmfield">
					<label for="nm-life" class="hk-field-label">Keep it</label>
					<select id="nm-life" class="hk-select" bind:value={life}>
						<option value={0}>Until I destroy it</option>
						{#each LIVES as l (l.value)}
							<option value={l.value}>{l.label}</option>
						{/each}
					</select>
				</div>
				{#if hosts.length > 0}
					<div class="nmfield">
						<label for="nm-host" class="hk-field-label">Runs on</label>
						<select id="nm-host" class="hk-select" bind:value={runsOn}>
							{#if hosts.length === 1}
								<option value="own">{hostLabel(hosts[0])}</option>
							{:else}
								<option value="own">Any of your hosts</option>
								{#each hosts as h (h.id)}
									<option value={h.id}>{hostLabel(h)}</option>
								{/each}
							{/if}
							<option value="hosted">repose's servers</option>
						</select>
					</div>
				{/if}
				{#if life === 0}
					<div class="nmfield nmwide">
						<label for="nm-repo" class="hk-field-label">Repository</label>
						<input
							id="nm-repo"
							class="hk-input"
							autocomplete="off"
							spellcheck="false"
							placeholder="github.com/you/todo-app"
							aria-describedby="nm-repo-help"
							value={repo}
							oninput={(e) => onRepo(e.currentTarget.value)}
						/>
						<p id="nm-repo-help" class="nmhelp">
							Not cloned. <code class="hk-mono">repose run</code> in that checkout uses this machine.
						</p>
					</div>
				{:else}
					<p class="nmhelp nmwide" data-testid="nm-temp-note">
						A temporary machine is destroyed with no snapshot, and takes no repository.
					</p>
				{/if}
			</div>
		{/if}
	</form>
</section>

<style>
	.nmform {
		display: flex;
		flex-direction: column;
		gap: 0.5rem;
	}
	.nmrow {
		display: grid;
		grid-template-columns: 1fr;
		gap: 0.5rem;
		align-items: end;
	}
	.nmlabel {
		grid-column: 1 / -1;
		margin-bottom: -0.25rem;
	}
	.nmname {
		font-family: var(--hk-font-mono);
	}
	.nmacts {
		display: flex;
		gap: 0.5rem;
	}
	@media (min-width: 640px) {
		.nmrow {
			grid-template-columns: minmax(0, 24rem) auto;
		}
	}
	.nmsummary {
		display: flex;
	}
	/* The defaults as one line, which is also the way to change them. */
	.nmtoggle {
		display: inline-flex;
		flex-wrap: wrap;
		align-items: baseline;
		gap: 0.25rem 0.75rem;
		padding: 0.25rem 0;
		border: 0;
		background: none;
		color: var(--hk-text-3);
		font: inherit;
		font-size: 0.8125rem;
		text-align: left;
		cursor: pointer;
	}
	.nmtoggle:hover .nmmore {
		text-decoration: underline;
		text-underline-offset: 3px;
	}
	.nmmore {
		color: var(--hk-accent);
	}
	.nmoptions {
		display: grid;
		gap: 0.75rem;
		grid-template-columns: 1fr;
		margin-top: 0.25rem;
	}
	@media (min-width: 640px) {
		.nmoptions {
			grid-template-columns: repeat(auto-fill, minmax(12rem, 1fr));
			max-width: 48rem;
		}
	}
	.nmfield {
		display: flex;
		flex-direction: column;
		gap: 0.25rem;
		min-width: 0;
	}
	.nmwide {
		grid-column: 1 / -1;
		max-width: 28rem;
	}
	.nmroom {
		display: flex;
		flex-direction: column;
		gap: 0.5rem;
		font-size: 0.875rem;
		color: var(--hk-text-2);
	}
	.nmroom p {
		margin: 0;
	}
	.nmstops {
		display: flex;
		flex-wrap: wrap;
		gap: 0.5rem;
	}
	.nmhelp {
		margin: 0;
		font-size: 0.8125rem;
		color: var(--hk-text-3);
	}
	@media (pointer: coarse), (max-width: 639.98px) {
		.nmtoggle {
			min-height: 2.75rem;
		}
	}
</style>
