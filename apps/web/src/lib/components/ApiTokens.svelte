<!--
  Settings → API tokens (DECISIONS I-1314..I-1322): the tokens an agent or a
  script calls the api with. The list shows everything but the token; the
  dialog makes one and shows it once, with a copy button, and the page
  never sees it again. Delete is the two-step the Secrets tab uses.
-->
<script lang="ts">
	import { onMount, tick } from 'svelte';
	import { resolve } from '$app/paths';
	import { toast } from 'svelte-sonner';
	import { listTokens, createToken, deleteToken } from '$lib/api/client';
	import { toastApiError } from '$lib/api/toast';
	import { ApiError } from '$lib/api/errors';
	import { dateOnly, relativeTime } from '$lib/format';
	import LoadState, { loadErrorText } from '$lib/components/LoadState.svelte';
	import CodeCopy from '$lib/components/CodeCopy.svelte';
	import { focusAfterRender, focusOnMount } from '$lib/focus';
	import { projectsStore } from '$lib/projects.svelte';
	import { TOKEN_SCOPES, type ApiToken, type TokenScope } from '$lib/api/types';

	const NAME_RE = /^[a-z0-9][a-z0-9-]{0,31}$/;
	const EXPIRIES = [
		{ days: 7, label: '7 days' },
		{ days: 30, label: '30 days' },
		{ days: 90, label: '90 days' },
		{ days: 365, label: '365 days' }
	];

	let tokens = $state<ApiToken[] | undefined>(undefined);
	let loadError = $state<string | undefined>(undefined);
	let deleting = $state<string | undefined>(undefined);
	let confirming = $state<string | undefined>(undefined);

	// The dialog: the form, then the one view of the token.
	let dialog = $state<HTMLDialogElement | undefined>(undefined);
	let open = $state(false);
	let nameField = $state<HTMLInputElement | undefined>(undefined);
	let name = $state('');
	let scopes = $state<TokenScope[]>(['read']);
	let projectId = $state('');
	let days = $state(30);
	let saving = $state(false);
	let nameError = $state<string | undefined>(undefined);
	let scopeError = $state<string | undefined>(undefined);
	let made = $state<ApiToken | undefined>(undefined);

	let live = $derived((tokens ?? []).filter((t) => !t.revoked_at));
	let projects = $derived(projectsStore.list ?? []);

	async function load() {
		try {
			tokens = await listTokens();
			loadError = undefined;
		} catch (err) {
			if (tokens === undefined) loadError = loadErrorText(err, 'Could not load tokens.');
			else toastApiError(err, 'Could not load tokens.');
		}
	}

	onMount(() => {
		void load();
		void projectsStore.refresh();
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
		scopes = ['read'];
		projectId = '';
		days = 30;
		saving = false;
		nameError = undefined;
		scopeError = undefined;
		made = undefined;
	}

	function close() {
		open = false;
		void focusAfterRender('token-create-open');
	}

	function toggle(s: TokenScope, on: boolean) {
		scopes = on ? [...scopes, s] : scopes.filter((x) => x !== s);
		if (scopes.length) scopeError = undefined;
	}

	async function create() {
		if (saving) return;
		const n = name.trim();
		nameError = NAME_RE.test(n)
			? undefined
			: '1 to 32 of a-z, 0-9 and -, starting with a letter or digit.';
		scopeError = scopes.length ? undefined : 'Pick at least one.';
		if (nameError || scopeError) return;
		saving = true;
		try {
			const ordered = TOKEN_SCOPES.map((s) => s.name).filter((s) => scopes.includes(s));
			made = await createToken({
				name: n,
				scopes: ordered,
				project_id: projectId || undefined,
				expires_in_days: days
			});
			await load();
			void tick().then(() => document.getElementById('token-done')?.focus());
		} catch (err) {
			if (err instanceof ApiError && err.code === 'conflict') {
				nameError = `You already have a token named ${n}.`;
			} else if (err instanceof ApiError && err.code === 'invalid') {
				if (err.detail?.reason === 'token_limit') toast.error(err.message);
				else nameError = err.message;
			} else {
				toastApiError(err, 'Could not make the token.');
			}
		} finally {
			saving = false;
		}
	}

	function keep(n: string) {
		confirming = undefined;
		void focusAfterRender(`token-delete-${n}`);
	}

	async function onDelete(n: string) {
		if (deleting) return;
		deleting = n;
		const names = live.map((t) => t.name);
		const at = names.indexOf(n);
		const after = [names[at + 1], names[at - 1]].filter(Boolean).map((m) => `token-delete-${m}`);
		try {
			await deleteToken(n);
			confirming = undefined;
			await load();
			void focusAfterRender(...after, 'token-create-open');
		} catch (err) {
			toastApiError(err, 'Could not revoke the token.');
		} finally {
			deleting = undefined;
		}
	}

	function reach(t: ApiToken): string {
		return t.project ? (t.project.slug ?? t.project.id) : 'All projects';
	}

	/** The end as a date, or what ended it first. */
	function ends(t: ApiToken): string {
		if (t.revoked_at) return `Revoked ${relativeTime(t.revoked_at)}`;
		if (new Date(t.expires_at).getTime() <= Date.now())
			return `Expired ${relativeTime(t.expires_at)}`;
		return dateOnly(t.expires_at);
	}

	function used(t: ApiToken): string {
		return t.last_used_at ? relativeTime(t.last_used_at) : 'Never';
	}
</script>

<section class="hk-section" id="tokens" aria-labelledby="tokens-h">
	<div class="hk-section-head">
		<h2 id="tokens-h" class="hk-h2-app">
			{tokens && live.length > 0
				? `${live.length} ${live.length === 1 ? 'token' : 'tokens'}`
				: 'API tokens'}
		</h2>
		{#if tokens}
			<button
				type="button"
				id="token-create-open"
				class="hk-btn-secondary hk-btn-sm"
				aria-haspopup="dialog"
				onclick={() => (open = true)}>Create token</button
			>
		{/if}
	</div>
	<p class="lede">
		An agent or a script calls the api as you, with the scopes you pick, until the token expires.
		<a class="hk-link" href={resolve('/docs/[slug]', { slug: 'api' })}>How to use one</a>.
	</p>
	<LoadState
		status={tokens !== undefined ? 'ready' : loadError ? 'failed' : 'loading'}
		error={loadError}
		onretry={load}
		shape="rows"
	>
		{#if tokens && tokens.length === 0}
			<div class="hk-empty-row">
				<h3 class="hk-empty-title">No API tokens.</h3>
			</div>
		{:else if tokens}
			<table class="hk-table list" data-testid="api-tokens" aria-label="API tokens">
				<thead>
					<tr>
						<th scope="col">Name</th>
						<th scope="col" class="c-scopes">Scopes</th>
						<th scope="col" class="c-reach">Project</th>
						<th scope="col" class="c-when">Expires</th>
						<th scope="col" class="c-when">Last used</th>
						<th scope="col" class="c-act"><span class="hk-sr">Revoke</span></th>
					</tr>
				</thead>
				<tbody>
					{#each tokens as t (t.id)}
						<tr class:open={confirming === t.name} class:gone={!!t.revoked_at}>
							<td class="c-name">
								<span class="name">{t.name}</span>
								<span class="prefix">{t.prefix}…</span>
								<span class="phone-meta"
									>{t.scopes.join(', ')} · {reach(t)} <span class="nw">· {ends(t)}</span></span
								>
							</td>
							<td class="c-scopes">{t.scopes.join(', ')}</td>
							<td class="c-reach">{reach(t)}</td>
							<td class="c-when">{ends(t)}</td>
							<td class="c-when">{used(t)}</td>
							<td class="c-act">
								{#if !t.revoked_at && confirming !== t.name}
									<button
										type="button"
										id={`token-delete-${t.name}`}
										class="hk-btn-ghost hk-btn-sm hk-btn-ghost--touch"
										aria-label={`Revoke ${t.name}…`}
										onclick={() => (confirming = t.name)}>Revoke…</button
									>
								{/if}
							</td>
						</tr>
						{#if confirming === t.name}
							<tr class="confirm-row">
								<td colspan="6">
									<div class="hk-confirm" data-testid="confirm-revoke-token">
										<p>Revoke <code>{t.name}</code>? Every call with it is refused from now on.</p>
										<div class="hk-confirm-actions">
											<button
												type="button"
												class="hk-btn-danger hk-btn-sm"
												aria-label={`Revoke ${t.name} now`}
												aria-disabled={deleting === t.name ? 'true' : undefined}
												onclick={() => onDelete(t.name)}
												>{deleting === t.name ? 'Revoking…' : 'Revoke'}</button
											>
											<button
												type="button"
												class="hk-btn-ghost hk-btn-sm"
												use:focusOnMount
												onclick={() => keep(t.name)}>Keep it</button
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
	class="hk-modal tdlg"
	aria-labelledby="tdlg-title"
	data-testid="token-dialog"
	oncancel={(e) => {
		e.preventDefault();
		close();
	}}
	onclick={(e) => {
		// A stray click must not lose the one view of the token; Escape and Done close it.
		if (e.target === dialog && !made) close();
	}}
>
	{#if open && !made}
		<form
			method="dialog"
			onsubmit={(e) => {
				e.preventDefault();
				void create();
			}}
		>
			<div class="tdlg-head"><h2 id="tdlg-title" class="hk-modal-title">Create a token</h2></div>
			<div class="tdlg-body">
				<div class="field">
					<label for="tdlg-name" class="hk-field-label">Name</label>
					<input
						id="tdlg-name"
						class="hk-input"
						bind:this={nameField}
						bind:value={name}
						autocomplete="off"
						spellcheck="false"
						placeholder="ci"
						aria-invalid={nameError ? 'true' : undefined}
						aria-describedby={nameError ? 'tdlg-name-error' : undefined}
					/>
					{#if nameError}<p id="tdlg-name-error" class="hk-field-error">{nameError}</p>{/if}
				</div>
				<fieldset
					class="field scopes"
					aria-describedby={scopeError ? 'tdlg-scope-error' : undefined}
				>
					<legend class="hk-field-label">Scopes</legend>
					{#each TOKEN_SCOPES as s (s.name)}
						<label class="hk-check scope">
							<input
								type="checkbox"
								checked={scopes.includes(s.name)}
								onchange={(e) => toggle(s.name, e.currentTarget.checked)}
							/>
							<span
								><span class="scope-name">{s.name}</span>
								<span class="scope-means">{s.means}</span></span
							>
						</label>
					{/each}
					{#if scopeError}<p id="tdlg-scope-error" class="hk-field-error">{scopeError}</p>{/if}
				</fieldset>
				<div class="two">
					<div class="field">
						<label for="tdlg-project" class="hk-field-label">Project</label>
						<select id="tdlg-project" class="hk-select" bind:value={projectId}>
							<option value="">All projects</option>
							{#each projects as p (p.id)}
								<option value={p.id}>{p.slug}</option>
							{/each}
						</select>
					</div>
					<div class="field">
						<label for="tdlg-expires" class="hk-field-label">Expires in</label>
						<select id="tdlg-expires" class="hk-select" bind:value={days}>
							{#each EXPIRIES as x (x.days)}
								<option value={x.days}>{x.label}</option>
							{/each}
						</select>
					</div>
				</div>
			</div>
			<div class="hk-modal-foot">
				<button type="button" class="hk-btn-ghost" onclick={close}>Cancel</button>
				<button type="submit" class="hk-btn" aria-disabled={saving ? 'true' : undefined}
					>{saving ? 'Creating…' : 'Create'}</button
				>
			</div>
		</form>
	{:else if open && made}
		<div class="tdlg-made">
			<div class="tdlg-head"><h2 id="tdlg-title" class="hk-modal-title">{made.name}</h2></div>
			<div class="tdlg-body">
				<p class="tdlg-once">Copy it now. It is not shown again.</p>
				<div data-testid="token-value">
					<CodeCopy command={made.token ?? ''} what="the token" />
				</div>
				<p class="tdlg-facts">
					{made.scopes.join(', ')} · {reach(made)} · until {dateOnly(made.expires_at)}
				</p>
			</div>
			<div class="hk-modal-foot">
				<button type="button" id="token-done" class="hk-btn" onclick={close}>Done</button>
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
	.c-name .prefix {
		display: block;
		font-family: var(--hk-font-mono);
		font-size: 0.75rem;
		font-weight: 400;
		color: var(--hk-text-3);
	}
	.c-scopes,
	.c-reach,
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
	tr.gone td {
		color: var(--hk-text-3);
	}
	tr.open td {
		border-bottom: 0;
	}
	.confirm-row td {
		padding-top: 0;
		border-top: 0;
	}
	.phone-meta {
		display: none;
	}
	@media (max-width: 767.98px) {
		.list thead .c-scopes,
		.list td.c-scopes,
		.list thead .c-reach,
		.list td.c-reach,
		.list thead .c-when,
		.list td.c-when {
			display: none;
		}
		.phone-meta {
			display: block;
			margin-top: 0.125rem;
			font-size: 0.75rem;
			font-weight: 400;
			color: var(--hk-text-3);
		}
	}

	/* The dialog: the secret dialog's box, head and body. */
	.tdlg {
		max-width: 30rem;
	}
	.tdlg-head {
		padding: 1.25rem 1.25rem 0;
	}
	.tdlg-body {
		display: grid;
		gap: 1rem;
		padding: 0.75rem 1.25rem 0.25rem;
	}
	.field {
		display: grid;
		gap: 0.375rem;
		min-width: 0;
	}
	fieldset.field {
		margin: 0;
		padding: 0;
		border: 0;
	}
	.scopes legend {
		margin-bottom: 0.375rem;
	}
	.scope {
		align-items: baseline;
		padding-block: 0.125rem;
	}
	/* The name is a column from 480px, as `--help` prints it. */
	.scope > span {
		display: grid;
		grid-template-columns: 4.5rem 1fr;
		column-gap: 0.5rem;
	}
	@media (max-width: 479.98px) {
		.scope > span {
			display: inline;
		}
	}
	.scope-name {
		font-family: var(--hk-font-mono);
		font-size: 0.8125rem;
	}
	.scope-means {
		font-size: 0.8125rem;
		color: var(--hk-text-3);
	}
	.two {
		display: grid;
		grid-template-columns: 1fr 1fr;
		gap: 1rem;
	}
	@media (max-width: 479.98px) {
		.two {
			grid-template-columns: 1fr;
		}
	}
	.tdlg-once {
		margin: 0;
		font-size: 0.875rem;
		color: var(--hk-text-2);
	}
	.tdlg-facts {
		margin: 0;
		font-size: 0.8125rem;
		color: var(--hk-text-3);
	}
	.nw {
		white-space: nowrap;
	}
	@media (forced-colors: active) {
		.c-act button {
			margin-inline: 0;
		}
	}
</style>
