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

  Restart (I-1690) is `repose host restart` for a Mac that parks its
  machines (`can_restart`): every machine is saved with its memory, the
  Mac restarts, and they run again at the next login. A machine that
  cannot be parked stops it, and the refusal names it beside Restart
  anyway, which restarts with those machines losing their memory.
-->
<script lang="ts">
	import { onMount, tick } from 'svelte';
	import { resolve } from '$app/paths';
	import { listHosts, createHost, deleteHost, restartHost, getHostOp } from '$lib/api/client';
	import { toastApiError } from '$lib/api/toast';
	import { ApiError, errorText } from '$lib/api/errors';
	import { gb, heldText, relativeTime } from '$lib/format';
	import LoadState, { loadErrorText } from '$lib/components/LoadState.svelte';
	import CodeCopy from '$lib/components/CodeCopy.svelte';
	import { focusAfterRender, focusOnMount } from '$lib/focus';
	import type { OwnedHost, HostState } from '$lib/api/types';
	import { projectsStore } from '$lib/projects.svelte';
	import { notAnsweringSince } from '$lib/answering';
	import { pollUntilDone } from '$lib/poll';

	const NAME_RE = /^[a-z0-9][a-z0-9-]{0,31}$/;
	/** What a Mac downloads and the free disk `repose host add` asks for
	 * (I-1684): repose installed (49 MB unpacked, review-mac), the
	 * erofs base the first machine fetches, and the 10 GB refusal. The
	 * api's install answer carries none of them, so they are
	 * own-hardware.md's figures. */
	const MAC_DOWNLOAD = 'about 50 MB';
	const MAC_BASE = 'about 3 GB';
	const MAC_FREE_DISK = '10 GB';
	/** What takes repose off an x86 box again (I-1590); `repose host rm` prints it too. */
	const X86_UNINSTALL =
		'curl -fsSL https://repose.run/install-host.sh | sudo bash -s -- --uninstall';

	let hosts = $state<OwnedHost[] | undefined>(undefined);
	let loadError = $state<string | undefined>(undefined);
	let removing = $state<string | undefined>(undefined);
	let confirming = $state<string | undefined>(undefined);
	/** The host whose Restart is being confirmed, run, or answered. */
	let restartFor = $state<string | undefined>(undefined);
	let restarting = $state(false);
	/** The refusal of the last restart: the machines it could not park. */
	let restartRefusal = $state<string | undefined>(undefined);
	/** What a restart that went ahead said, until the list is read again. */
	let restarted = $state<Record<string, string>>({});

	// The dialog: the name, then the one view of the join token.
	let dialog = $state<HTMLDialogElement | undefined>(undefined);
	let open = $state(false);
	let nameField = $state<HTMLInputElement | undefined>(undefined);
	let name = $state('');
	let saving = $state(false);
	let nameError = $state<string | undefined>(undefined);
	/** A refusal that is not about the name: the plan's limit, no plan. */
	let addError = $state<string | undefined>(undefined);
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
		addError = undefined;
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
		addError = undefined;
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
				// In the dialog, never behind it (ENG-29's sibling,
				// ENG-25). A native dialog opened with showModal() sits
				// in the browser's top layer, which no z-index reaches,
				// so a toast raised while it is open renders under the
				// backdrop: the customer pressed Add, nothing appeared
				// to happen, and the reason — then the plan's one-host limit
				// — was behind the thing they were looking at. The api's
				// sentences say what the plan includes, so they are the
				// ones shown, beside the button that was pressed.
				addError = errorText(err, 'Could not add the host.');
			}
		} finally {
			saving = false;
		}
	}

	function openRestart(id: string) {
		confirming = undefined;
		restartRefusal = undefined;
		restartFor = id;
	}

	function keepRunning(id: string) {
		restartFor = undefined;
		restartRefusal = undefined;
		void focusAfterRender(`host-restart-${id}`);
	}

	/** Waits for a host op every 2s until it ends. */
	function waitForHostOp(hostID: string, opID: string) {
		return new Promise<Awaited<ReturnType<typeof getHostOp>>>((settle, fail) => {
			pollUntilDone(async () => {
				try {
					const op = await getHostOp(hostID, opID);
					if (op.state === 'done' || op.state === 'error') {
						settle(op);
						return true;
					}
					return false;
				} catch (err) {
					fail(err);
					return true;
				}
			});
		});
	}

	async function onRestart(h: OwnedHost, force: boolean) {
		if (restarting) return;
		restarting = true;
		try {
			const { op_id } = await restartHost(h.id, force);
			const op = await waitForHostOp(h.id, op_id);
			if (op.state === 'error') {
				const e = op.error;
				restartRefusal = typeof e === 'string' ? e : (e?.message ?? `${h.name} did not restart.`);
				return;
			}
			const n = op.result?.parked ?? 0;
			const lost = op.result?.not_parked ?? [];
			restarted = {
				...restarted,
				[h.id]:
					`Parked ${n} ${n === 1 ? 'machine' : 'machines'}; ${h.name} is restarting.` +
					(lost.length
						? ` ${lost.join(', ')} ${lost.length === 1 ? 'starts' : 'start'} fresh.`
						: '')
			};
			restartFor = undefined;
			restartRefusal = undefined;
			await load();
		} catch (err) {
			restartRefusal = errorText(err, `Could not restart ${h.name}.`);
		} finally {
			restarting = false;
		}
	}

	/** What a Restart does to this host's machines, in one sentence. */
	function restartWarning(h: OwnedHost): string {
		// The Mac's own apps may be quit without asking to save (hostd
		// falls back to shutdown -r, I-1690): a loss nobody can undo, so
		// the question says it (review-dash P1 #1).
		const apps =
			" Save your work in the Mac's own apps first: it may restart without waiting for them.";
		if (!h.running_projects) return `No machine is running on it.${apps}`;
		if (h.running_projects === 1)
			return `Its running machine is parked with its memory, then the Mac restarts; the machine runs again when you log in to the Mac.${apps}`;
		return `Its ${h.running_projects} running machines are parked with their memory, then the Mac restarts; they run again when you log in to the Mac.${apps}`;
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

	/** What kind of machine it is, in words a customer uses: an aarch64
	 * host is a Mac (review-dash P3 #12). */
	function archWord(arch?: string): string {
		if (arch === 'aarch64') return 'Mac';
		if (arch === 'x86_64') return 'x86';
		return arch ?? '';
	}

	/** The state word, with a restart through repose shown as such
	 * (I-1690 amendment, review-dash P2 #2). */
	function hostWord(h: OwnedHost): string {
		if (h.state === 'ready' && h.restarting_since) return 'restarting';
		return stateWord(h.state);
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
		// Running machines that stopped answering are named apart, so the
		// host does not read healthy while they hang (pass2-dash N5,
		// I-1658); the projects list the rail already holds says which.
		const silent = (projectsStore.list ?? []).filter(
			(p) => p.host_id === h.id && !!notAnsweringSince(p)
		).length;
		// Frozen ones are named too: on a Mac they rest with their memory
		// saved and given back (review-dash P2 #4).
		const frozen = (projectsStore.list ?? []).filter(
			(p) => p.host_id === h.id && p.state === 'running' && !!p.resting
		).length;
		// On a Mac, what macOS gives up for its machines together, in the
		// CLI's word (I-1712).
		const mac = (projectsStore.list ?? []).filter(
			(p) => p.host_id === h.id && p.state === 'running' && !!p.vm
		);
		const held = mac.length
			? `, ${heldText(mac.reduce((n, p) => n + (p.mem_used_bytes ?? 0), 0))}`
			: '';
		return `${h.projects} ${word}, ${h.running_projects} running${frozen ? `, ${frozen} frozen` : ''}${silent ? `, ${silent} not answering` : ''}${held}`;
	}

	// What the confirmation says. A host that is answering cannot be
	// removed while machines are on it: the api refuses that. A host that
	// has stopped answering can be, which is the only way out of the
	// wedge in ENG-119, and it is the one path where removing a host
	// loses machines -- so the question says how many and that their
	// disks went with it, in the same words `repose host rm` uses
	// (DECISIONS I-1420).
	function removeWarning(h: OwnedHost): string {
		if (h.projects && (h.state === 'unreachable' || h.state === 'lost')) {
			const what = h.projects === 1 ? 'the machine on it' : `the ${h.projects} machines on it`;
			const disks = h.projects === 1 ? 'its disk is' : 'their disks are';
			return `It is not answering, and ${what} cannot be recovered: ${disks} on that host and repose has no copy.${snapshotsGo(h)}`;
		}
		// An answering host still holding machines is refused by the
		// api; the question names them first (review-dash P2 #5).
		const on = (projectsStore.list ?? []).filter((p) => p.host_id === h.id).map((p) => p.name);
		if (on.length)
			return `It still holds ${on.join(', ')}, and a host is removed only once its machines are: remove ${on.length === 1 ? 'it' : 'them'} first.${snapshotsGo(h)}`;
		return `It stops taking your projects, and it joins again only with a new install.${snapshotsGo(h)}`;
	}

	/** What removing it deletes besides machines: the snapshots kept on its
	 * disk, counted (I-1546). */
	function snapshotsGo(h: OwnedHost): string {
		const n = h.snapshots ?? 0;
		if (n === 0) return '';
		return n === 1
			? ' This deletes the 1 snapshot kept on it; repose has no copy.'
			: ` This deletes the ${n} snapshots kept on it; repose has no copy.`;
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

	/**
	 * The same argument for an x86 box, which runs the host installer
	 * instead of the CLI (I-1490; own-hardware.md "Adding it"). Shown only
	 * when the api sent the argument, since the line is nothing without it.
	 * The argument goes through a file so sudo's log keeps no copy
	 * (ENG-206, I-1592); the same line `repose host add` prints.
	 */
	let x86Step = $derived(
		made?.install?.argument
			? `printf '%s\\n' ${made.install.argument} > repose-join && curl -fsSL https://repose.run/install-host.sh | sudo bash -s -- --join-file repose-join`
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
								{#if archWord(h.arch)}<span class="arch">{archWord(h.arch)}</span>{/if}
								<!-- A host that has not registered has nothing to say but
								     its state: no size, no machines, no heartbeat. -->
								<!-- One fact a line, so a wrap never starts a line with
								     a separator (review-dash 9, I-1654). -->
								<span class="phone-meta">
									<span class="pm-line"
										><span class="dot" data-tone={tone(h.state)}></span>{hostWord(
											h
										)}{#if h.last_heartbeat_at}<span class="nw">&nbsp;· heard {heard(h)}</span
											>{/if}</span
									>
									{#if h.mem_bytes}<span class="pm-line">{size(h)}</span>{/if}
									{#if h.projects}<span class="pm-line">{machines(h)}</span>{/if}
								</span>
							</td>
							<td class="c-state"
								><span class="dot" data-tone={tone(h.state)}></span>{hostWord(h)}</td
							>
							<td class="c-size">{size(h)}</td>
							<td class="c-machines">{machines(h)}</td>
							<td class="c-when">{heard(h)}</td>
							<td class="c-act">
								{#if h.can_restart && h.state === 'ready' && !h.restarting_since && restartFor !== h.id && confirming !== h.id}
									<button
										type="button"
										id={`host-restart-${h.id}`}
										class="hk-btn-ghost hk-btn-sm hk-btn-ghost--touch"
										aria-label={`Restart ${h.name}…`}
										onclick={() => openRestart(h.id)}>Restart…</button
									>
								{/if}
								{#if confirming !== h.id && restartFor !== h.id}
									<button
										type="button"
										id={`host-remove-${h.id}`}
										class="hk-btn-ghost hk-btn-sm hk-btn-ghost--touch"
										aria-label={`Remove ${h.name}…`}
										onclick={() => {
											restartFor = undefined;
											confirming = h.id;
										}}>Remove…</button
									>
								{/if}
							</td>
						</tr>
						{#if restarted[h.id] && restartFor !== h.id}
							<tr class="confirm-row">
								<td colspan="6">
									<p class="restarted" role="status" data-testid="host-restarted">
										{restarted[h.id]}
									</p>
								</td>
							</tr>
						{/if}
						{#if restartFor === h.id}
							<tr class="confirm-row">
								<td colspan="6">
									<div class="hk-confirm" data-testid="confirm-restart-host">
										{#if restartRefusal}
											<p role="alert" data-testid="host-restart-refused">{restartRefusal}</p>
										{:else}
											<p>Restart <strong>{h.name}</strong>? {restartWarning(h)}</p>
										{/if}
										<div class="hk-confirm-actions">
											<button
												type="button"
												class={restartRefusal ? 'hk-btn-danger hk-btn-sm' : 'hk-btn hk-btn-sm'}
												aria-label={restartRefusal
													? `Restart ${h.name} anyway`
													: `Restart ${h.name} now`}
												aria-disabled={restarting ? 'true' : undefined}
												onclick={() => onRestart(h, !!restartRefusal)}
												>{restarting
													? 'Restarting…'
													: restartRefusal
														? 'Restart anyway'
														: 'Restart'}</button
											>
											<button
												type="button"
												class="hk-btn-ghost hk-btn-sm"
												use:focusOnMount
												onclick={() => keepRunning(h.id)}>Cancel</button
											>
										</div>
									</div>
								</td>
							</tr>
						{/if}
						{#if confirming === h.id}
							<tr class="confirm-row">
								<td colspan="6">
									<div class="hk-confirm" data-testid="confirm-remove-host">
										<p>
											Remove <strong>{h.name}</strong>? {removeWarning(h)}
										</p>
										{#if h.arch === 'x86_64'}
											<!-- An x86 box keeps hostd and its disk until the installer's
											     --uninstall runs there (ENG-204, I-1590). -->
											<p data-testid="host-uninstall-x86">
												Repose keeps running on the box until you run this there as root:
											</p>
											<CodeCopy command={X86_UNINSTALL} what="the uninstall step" wrap />
										{:else if h.arch === 'aarch64'}
											<!-- A Mac keeps hostd, its launchd daemons and the machines'
											     disks until `repose host rm --local` runs on it
											     (I-1684; review-dash P2 #5). -->
											<p data-testid="host-uninstall-mac">
												Repose keeps running on the Mac, with its disks, until you run this there:
											</p>
											<CodeCopy
												command="repose host rm --local"
												what="the Mac's uninstall step"
												wrap
											/>
										{/if}
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
		// Escape must not lose the one view of the join step either
		// (review-dash P3 #6): with it shown, only Done closes.
		if (!made) close();
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
			{#if addError}
				<p class="hdlg-refusal hk-field-error" role="alert" data-testid="host-add-error">
					{addError}
				</p>
			{/if}
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
						Run this on the machine you are adding. It is not shown again, and it works once.
					</p>
					{#if x86Step}<h3 class="hdlg-where">On a Mac</h3>{/if}
					{#if step.startsWith('repose ')}
						<!-- The step is a repose command (review-dash P2 #3), whether
						     the api composed it or this page did (review-dash-2 P2 2:
						     the live api sends the command, and the line was gone). -->
						<p class="hdlg-facts hdlg-first" data-testid="host-mac-cli">
							The Mac needs the repose CLI and <code>repose login</code> first:
							<a class="hk-link" href={resolve('/docs/[slug]', { slug: 'cli' })}>Install</a>.
						</p>
					{/if}
					<div data-testid="host-install">
						<CodeCopy command={step} what="the install step" wrap />
					</div>
					{#if x86Step}
						<p class="hdlg-facts" data-testid="host-mac-size">
							Repose takes {MAC_DOWNLOAD} on the Mac, and the first machine downloads {MAC_BASE} more;
							it needs {MAC_FREE_DISK} of free disk.
						</p>
						<h3 class="hdlg-where">On an x86 box with Ubuntu 24.04</h3>
						<div data-testid="host-install-x86">
							<CodeCopy command={x86Step} what="the x86 install step" wrap />
						</div>
						<!-- What the installer takes (ops/install-host-x86.sh): a file by
						     default, or a whole empty disk it refuses to format when it
						     holds anything (council finding 9). -->
						<p class="hdlg-facts" data-testid="host-x86-disk">
							It keeps your machines' disks in /var/lib/repose/guests.img, or with --device takes
							over a whole empty disk.
						</p>
					{/if}
					<p class="hdlg-facts">
						<a class="hk-link" href={resolve('/docs/[slug]', { slug: 'own-hardware' })}
							>What this does</a
						>
					</p>
				{:else if made.join_token}
					<!-- An api older than I-1380 sends no install step at all.
					     The token is an input to the api and not something to
					     run, so it is never offered as one. -->
					<p class="hdlg-once">This is its join token. It is not shown again, and it works once.</p>
					<div data-testid="host-token">
						<CodeCopy command={made.join_token} what="the join token" wrap />
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
		overflow-wrap: anywhere;
	}
	.c-name .name {
		display: block;
		overflow-wrap: anywhere;
	}
	.c-name .arch {
		display: block;
		font-family: var(--hk-font-mono);
		font-size: 0.75rem;
		font-weight: 400;
		color: var(--hk-text-3);
		overflow-wrap: anywhere;
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
	.c-act button + button {
		margin-left: 0.625rem;
	}
	.restarted {
		margin: 0;
		font-size: 0.8125rem;
		color: var(--hk-text-2);
		text-wrap: pretty;
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
		/* Keep long unbroken host names or arch strings inside the first
		   column so the table never overflows a 390px phone or pushes Remove
		   off the right edge (ENG-88). */
		.list .c-name {
			width: 100%;
			max-width: 0;
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
		.pm-line {
			display: block;
		}
	}
	@media (forced-colors: active) {
		.c-act button {
			margin-inline: 0;
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
	/* A refusal the api sent, between the field and the buttons: the
	   dialog is in the top layer and a toast is not, so this is the only
	   place a reason can be seen while the dialog is open (ENG-25). */
	.hdlg-refusal {
		margin: 0;
		padding: 0 1.25rem 0.25rem;
		line-height: 1.25rem;
		text-wrap: pretty;
	}
	.hdlg-where {
		margin: 1rem 0 0.375rem;
		font-size: 0.8125rem;
		font-weight: 500;
		color: var(--hk-text-2);
	}
	.hdlg-where:first-of-type {
		margin-top: 0;
	}
	.hdlg-facts {
		margin: 0.75rem 0 0;
		font-size: 0.8125rem;
		color: var(--hk-text-3);
	}
	.hdlg-first {
		margin: 0 0 0.5rem;
	}
</style>
