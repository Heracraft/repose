<!--
  Settings → Hosts (DECISIONS I-1366): the machines of your own that run
  your projects. Add, list, remove, which is the whole of it and the same
  three `repose host` has (the dashboard parity rule).

  Add mints a single-use join token and shows the step that uses it once,
  as the API tokens tab shows a token once: the deployment's own installer
  line when it has recorded one, and `repose host add --join <bundle>`
  otherwise, which is every deployment today. Remove is the two-step the
  other tabs use, and a host still holding machines is refused by the api
  with their names.
-->
<script lang="ts">
	import { onMount, tick } from 'svelte';
	import { resolve } from '$app/paths';
	import { listHosts, createHost, deleteHost } from '$lib/api/client';
	import { toastApiError } from '$lib/api/toast';
	import { ApiError } from '$lib/api/errors';
	import { gb, relativeTime } from '$lib/format';
	import LoadState, { loadErrorText } from '$lib/components/LoadState.svelte';
	import CodeCopy from '$lib/components/CodeCopy.svelte';
	import { focusAfterRender, focusOnMount } from '$lib/focus';
	import type { OwnedHost, HostState } from '$lib/api/types';

	const NAME_RE = /^[a-z0-9][a-z0-9-]{0,31}$/;

	let hosts = $state<OwnedHost[] | undefined>(undefined);
	let loadError = $state<string | undefined>(undefined);
	let removing = $state<string | undefined>(undefined);
	let confirming = $state<string | undefined>(undefined);

	// The dialog: the name, then the one view of the join token.
	let dialog = $state<HTMLDialogElement | undefined>(undefined);
	let open = $state(false);
	let nameField = $state<HTMLInputElement | undefined>(undefined);
	let name = $state('');
	let saving = $state(false);
	let nameError = $state<string | undefined>(undefined);
	let made = $state<OwnedHost | undefined>(undefined);

	/**
	 * The hosts still yours. The api keeps a removed host's row for the
	 * history, and this list leaves it out, because a host the user
	 * removed is not hardware they have and `repose host ls` leaves it
	 * out too (19c): two surfaces, one answer.
	 */
	let live = $derived((hosts ?? []).filter((h) => h.state !== 'retired' && h.state !== 'lost'));

	async function load() {
		try {
			hosts = await listHosts();
			loadError = undefined;
		} catch (err) {
			if (hosts === undefined) loadError = loadErrorText(err, 'Could not load your hosts.');
			else toastApiError(err, 'Could not load your hosts.');
		}
	}

	onMount(() => {
		void load();
	});

	$effect(() => {
		if (!dialog) return;
		if (open && !dialog.open) {
			dialog.showModal();
			void tick().then(() => nameField?.focus());
		} else if (!open && dialog.open) {
			dialog.close();
			reset();
		}
	});

	function reset() {
		name = '';
		saving = false;
		nameError = undefined;
		made = undefined;
	}

	function close() {
		open = false;
		void focusAfterRender('host-add-open');
	}

	async function add() {
		if (saving) return;
		const n = name.trim();
		if (n && !NAME_RE.test(n)) {
			nameError = '1 to 32 of a-z, 0-9 and -, starting with a letter or digit.';
			return;
		}
		nameError = undefined;
		saving = true;
		try {
			made = await createHost(n ? { name: n } : {});
			await load();
			void tick().then(() => document.getElementById('host-done')?.focus());
		} catch (err) {
			if (err instanceof ApiError && err.detail?.reason === 'name_taken') {
				nameError = `You already have a host called ${n}.`;
			} else if (err instanceof ApiError && err.detail?.reason === 'name') {
				nameError = err.message;
			} else {
				// host_limit and own_hardware_plan_required are the api's
				// own sentences, which say what the plan includes.
				toastApiError(err, 'Could not add the host.');
			}
		} finally {
			saving = false;
		}
	}

	function keep(id: string) {
		confirming = undefined;
		void focusAfterRender(`host-remove-${id}`);
	}

	async function onRemove(h: OwnedHost) {
		if (removing) return;
		removing = h.id;
		try {
			await deleteHost(h.id);
			confirming = undefined;
			await load();
			void focusAfterRender('host-add-open');
		} catch (err) {
			// A host still holding machines is refused with their names,
			// which is the sentence to show.
			toastApiError(err, 'Could not remove the host.');
		} finally {
			removing = undefined;
		}
	}

	/**
	 * The word a user reads for a host's state, the same five
	 * `repose host ls` prints (19c's `hostStateWord`, and
	 * `internal/cli/hosts.go`'s): the two surfaces must not call one
	 * state two things. Lower case, as a project row's state is.
	 *
	 * This is a copy until 19c's `$lib/offlinehost` is on the branch,
	 * and then it is that import.
	 */
	function stateWord(s: HostState): string {
		switch (s) {
			case 'ready':
				return 'online';
			case 'registering':
				return 'waiting';
			case 'unreachable':
				return 'offline';
			case 'draining':
				return 'removing';
			default:
				return 'removed';
		}
	}

	function tone(s: HostState): 'on' | 'busy' | 'idle' {
		if (s === 'ready') return 'on';
		if (s === 'registering' || s === 'draining') return 'busy';
		return 'idle';
	}

	/** Its size, once it has told us; nothing before. */
	function size(h: OwnedHost): string {
		if (!h.mem_bytes) return '—';
		const cores = h.cores || h.vcpus;
		return cores ? `${gb(h.mem_bytes)} · ${cores} cores` : gb(h.mem_bytes);
	}

	function machines(h: OwnedHost): string {
		if (!h.projects) return 'None';
		const word = h.projects === 1 ? 'machine' : 'machines';
		return `${h.projects} ${word}, ${h.running_projects} running`;
	}

	function heard(h: OwnedHost): string {
		return h.last_heartbeat_at ? relativeTime(h.last_heartbeat_at) : '—';
	}

	/**
	 * The one step a user runs on the machine they are adding, from the
	 * answer to the mint (DECISIONS I-1366, I-1380). The same ladder
	 * `repose host add` prints, in the same order, because one answer
	 * read two ways is how the dialog came to show a value nobody could
	 * use:
	 *
	 * 1. `install.command`, the deployment's own installer line, run as
	 *    it is. Empty until an operator records one with `repose-admin
	 *    hosts install-command`, which no deployment has, so 2 is the
	 *    normal case rather than the edge case.
	 * 2. `install.argument`, the join bundle, in the command that takes
	 *    it. The CLI prints the argument bare because it pipes; nobody
	 *    pipes a dialog, so this shows the line to paste.
	 * 3. Nothing, for an api that sent neither.
	 *
	 * Never the bare `join_token`: it is an input to the api, not
	 * something a user runs, and a 43-character value that looks
	 * runnable is worse than no value, because the dialog is one-shot
	 * and remove-and-add hands out the same wrong thing again.
	 */
	let step = $derived(
		made?.install?.command
			? made.install.command
			: made?.install?.argument
				? `repose host add --join ${made.install.argument}`
				: ''
	);
</script>

<section class="hk-section" id="hosts" aria-labelledby="hosts-h">
	<div class="hk-section-head">
		<h2 id="hosts-h" class="hk-h2-app">
			{hosts && live.length > 0
				? `${live.length} ${live.length === 1 ? 'host' : 'hosts'}`
				: 'Your own hardware'}
		</h2>
		{#if hosts}
			<button
				type="button"
				id="host-add-open"
				class="hk-btn-secondary hk-btn-sm"
				aria-haspopup="dialog"
				onclick={() => (open = true)}>Add a host</button
			>
		{/if}
	</div>
	<p class="lede">
		A machine of your own runs your projects and nobody else's, and your projects go to it instead
		of to repose's servers.
		<a class="hk-link" href={resolve('/docs/[slug]', { slug: 'own-hardware' })}>How to add one</a>.
	</p>
	<LoadState
		status={hosts !== undefined ? 'ready' : loadError ? 'failed' : 'loading'}
		error={loadError}
		onretry={load}
		shape="rows"
	>
		{#if hosts && live.length === 0}
			<div class="hk-empty-row">
				<h3 class="hk-empty-title">No hosts of your own.</h3>
			</div>
		{:else if hosts}
			<table class="hk-table list" data-testid="own-hosts" aria-label="Your own hosts">
				<thead>
					<tr>
						<th scope="col">Name</th>
						<th scope="col" class="c-state">State</th>
						<th scope="col" class="c-size">Size</th>
						<th scope="col" class="c-machines">Machines</th>
						<th scope="col" class="c-when">Last heard</th>
						<th scope="col" class="c-act"><span class="hk-sr">Remove</span></th>
					</tr>
				</thead>
				<tbody>
					{#each live as h (h.id)}
						<tr class:open={confirming === h.id}>
							<td class="c-name">
								<span class="name">{h.name}</span>
								{#if h.arch}<span class="arch">{h.arch}</span>{/if}
								<!-- A host that has not registered has nothing to say but
								     its state: no size, no machines, no heartbeat. -->
								<span class="phone-meta">
									<span class="dot" data-tone={tone(h.state)}></span>{stateWord(h.state)}
									{#if h.last_heartbeat_at}<span class="nw">· heard {heard(h)}</span>{/if}
									{#if h.mem_bytes}<span class="nw">· {size(h)}</span>{/if}
									{#if h.projects}<span class="nw">· {machines(h)}</span>{/if}
								</span>
							</td>
							<td class="c-state"
								><span class="dot" data-tone={tone(h.state)}></span>{stateWord(h.state)}</td
							>
							<td class="c-size">{size(h)}</td>
							<td class="c-machines">{machines(h)}</td>
							<td class="c-when">{heard(h)}</td>
							<td class="c-act">
								{#if confirming !== h.id}
									<button
										type="button"
										id={`host-remove-${h.id}`}
										class="hk-btn-ghost hk-btn-sm hk-btn-ghost--touch"
										aria-label={`Remove ${h.name}…`}
										onclick={() => (confirming = h.id)}>Remove…</button
									>
								{/if}
							</td>
						</tr>
						{#if confirming === h.id}
							<tr class="confirm-row">
								<td colspan="6">
									<div class="hk-confirm" data-testid="confirm-remove-host">
										<p>
											Remove <code>{h.name}</code>? It stops taking your projects, and it
											joins again only with a new install.
										</p>
										<div class="hk-confirm-actions">
											<button
												type="button"
												class="hk-btn-danger hk-btn-sm"
												aria-label={`Remove ${h.name} now`}
												aria-disabled={removing === h.id ? 'true' : undefined}
												onclick={() => onRemove(h)}
												>{removing === h.id ? 'Removing…' : 'Remove'}</button
											>
											<button
												type="button"
												class="hk-btn-ghost hk-btn-sm"
												use:focusOnMount
												onclick={() => keep(h.id)}>Keep it</button
											>
										</div>
									</div>
								</td>
							</tr>
						{/if}
					{/each}
				</tbody>
			</table>
		{/if}
	</LoadState>
</section>

<dialog
	bind:this={dialog}
	class="hk-modal hdlg"
	aria-labelledby="hdlg-title"
	data-testid="host-dialog"
	oncancel={(e) => {
		e.preventDefault();
		close();
	}}
	onclick={(e) => {
		// A stray click must not lose the one view of the join token.
		if (e.target === dialog && !made) close();
	}}
>
	{#if open && !made}
		<form
			method="dialog"
			onsubmit={(e) => {
				e.preventDefault();
				void add();
			}}
		>
			<div class="hdlg-head"><h2 id="hdlg-title" class="hk-modal-title">Add a host</h2></div>
			<div class="hdlg-body">
				<div class="h-field">
					<label for="hdlg-name" class="hk-field-label">Name</label>
					<input
						id="hdlg-name"
						class="hk-input"
						bind:this={nameField}
						bind:value={name}
						autocomplete="off"
						spellcheck="false"
						placeholder="mac-mini"
						aria-invalid={nameError ? 'true' : undefined}
						aria-describedby={nameError ? 'hdlg-name-error' : 'hdlg-name-note'}
					/>
					{#if nameError}
						<p id="hdlg-name-error" class="hk-field-error">{nameError}</p>
					{:else}
						<p id="hdlg-name-note" class="hk-field-note">Leave it empty and it is named for you.</p>
					{/if}
				</div>
			</div>
			<div class="hk-modal-foot">
				<button type="button" class="hk-btn-ghost" onclick={close}>Cancel</button>
				<button type="submit" class="hk-btn" aria-disabled={saving ? 'true' : undefined}
					>{saving ? 'Adding…' : 'Add'}</button
				>
			</div>
		</form>
	{:else if open && made}
		<div class="hdlg-made">
			<div class="hdlg-head"><h2 id="hdlg-title" class="hk-modal-title">{made.name}</h2></div>
			<div class="hdlg-body">
				{#if step}
					<p class="hdlg-once">
						Run this on the machine you are adding. It is not shown again, and it works
						once.
					</p>
					<div data-testid="host-install">
						<CodeCopy command={step} what="the install step" />
					</div>
					<p class="hdlg-facts">
						<a class="hk-link" href={resolve('/docs/[slug]', { slug: 'own-hardware' })}
							>What this does</a
						>
					</p>
				{:else if made.join_token}
					<!-- An api older than I-1380 sends no install step at all.
					     The token is an input to the api and not something to
					     run, so it is never offered as one. -->
					<p class="hdlg-once">
						This is its join token. It is not shown again, and it works once.
					</p>
					<div data-testid="host-token">
						<CodeCopy command={made.join_token} what="the join token" />
					</div>
					<p class="hdlg-facts">
						<a class="hk-link" href={resolve('/docs/[slug]', { slug: 'own-hardware' })}
							>What to do with it</a
						>
					</p>
				{:else}
					<p class="hdlg-once">
						{made.name} was added, but this repose sent nothing to install it with.
					</p>
					<p class="hdlg-facts">
						<a class="hk-link" href={resolve('/docs/[slug]', { slug: 'own-hardware' })}
							>Adding your own hardware</a
						>
					</p>
				{/if}
			</div>
			<div class="hk-modal-foot">
				<button type="button" id="host-done" class="hk-btn" onclick={close}>Done</button>
			</div>
		</div>
	{/if}
</dialog>

<style>
	.lede {
		margin: -0.25rem 0 1.25rem;
		font-size: 0.875rem;
		color: var(--hk-text-3);
		text-wrap: pretty;
	}
	.list {
		border-bottom: 1px solid var(--hk-hairline);
	}
	.list td {
		vertical-align: middle;
	}
	.c-name {
		font-size: 0.875rem;
		font-weight: 500;
	}
	.c-name .arch {
		display: block;
		font-family: var(--hk-font-mono);
		font-size: 0.75rem;
		font-weight: 400;
		color: var(--hk-text-3);
	}
	.c-state,
	.c-size,
	.c-machines,
	.c-when {
		font-size: 0.8125rem;
		color: var(--hk-text-2);
		white-space: nowrap;
	}
	.c-when {
		width: 1%;
		font-variant-numeric: tabular-nums;
	}
	.c-act {
		width: 1%;
		text-align: right;
		white-space: nowrap;
	}
	.list td.c-act {
		padding-block: 0.375rem;
	}
	.c-act button {
		margin-right: -0.625rem;
	}
	/* The state dot, the same three tones the project rows use. */
	.dot {
		display: inline-block;
		width: 0.4375rem;
		height: 0.4375rem;
		margin-right: 0.4375rem;
		border-radius: 50%;
		background: var(--hk-text-3);
		vertical-align: 0.0625rem;
	}
	.dot[data-tone='on'] {
		background: var(--hk-ok);
	}
	.dot[data-tone='busy'] {
		background: var(--hk-warn);
	}
	/* Below 640 the row keeps its name and folds the rest under it, as
	   the API tokens row does: five columns of facts do not fit a phone
	   and a sideways scroll hides the Remove button. */
	.phone-meta {
		display: none;
	}
	@container dash (max-width: 639.98px) {
		.c-state,
		.c-size,
		.c-machines,
		.c-when {
			display: none;
		}
		.phone-meta {
			display: block;
			font-size: 0.75rem;
			font-weight: 400;
			color: var(--hk-text-3);
			text-wrap: pretty;
		}
		.nw {
			white-space: nowrap;
		}
	}
	.h-field {
		display: grid;
		gap: 0.375rem;
	}
	.hdlg-head,
	.hdlg-body {
		padding: 0 1.25rem;
	}
	.hdlg-head {
		padding-top: 1.25rem;
	}
	.hdlg-body {
		padding-top: 1rem;
		padding-bottom: 1.25rem;
	}
	.hdlg-once {
		margin: 0 0 0.75rem;
		font-size: 0.875rem;
		color: var(--hk-text-2);
		text-wrap: pretty;
	}
	.hdlg-facts {
		margin: 0.75rem 0 0;
		font-size: 0.8125rem;
		color: var(--hk-text-3);
	}
</style>
