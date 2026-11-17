<!--
  A project's triggers, on its Triggers tab (DECISIONS I-1248..I-1262):
  the door each one fires on, its prompt, whether it is on, its last run
  and the next fire. A row opens into its runs.

  Run fires it now. A fire is not an op: it never takes ctx.opBusy, so
  Stop, Start and a snapshot stay available while an agent runs (I-1258).
  The button's label changes and it disables; there is no spinner, which
  is the house rule.

  Copy puts a route trigger's URL on the clipboard and draws none of it:
  the URL holds the route's token, and the reason is in
  ProjectWebhooks.svelte in better words.
-->
<script lang="ts">
	import { onMount } from 'svelte';
	import { toast } from 'svelte-sonner';
	import {
		listTriggers,
		listTriggerRuns,
		putTrigger,
		deleteTrigger,
		fireTrigger
	} from '$lib/api/client';
	import { toastApiError, PollFailure, PollGroup } from '$lib/api/toast';
	import CopyButton from '$lib/hk/CopyButton.svelte';
	import LoadState, { loadErrorText } from '$lib/components/LoadState.svelte';
	import { pollWhileVisible } from '$lib/poll';
	import { dateTime } from '$lib/format';
	import type { Trigger, TriggerRun } from '$lib/api/types';

	let { projectId }: { projectId: string } = $props();

	let triggers = $state<Trigger[] | undefined>(undefined);
	let loadFailed = $state(false);
	let loadError = $state<string | undefined>(undefined);
	/** The trigger whose runs are open, and those runs. */
	let open = $state<string | undefined>(undefined);
	let runs = $state<TriggerRun[] | undefined>(undefined);
	/** The trigger whose Remove… was pressed once. */
	let confirming = $state<string | undefined>(undefined);
	/** The trigger whose Run or on/off is in flight. */
	let busy = $state<string | undefined>(undefined);

	const pollFailures = new PollGroup();
	const failure = new PollFailure('Could not load the triggers.', pollFailures);

	async function load(quiet = false) {
		try {
			triggers = await listTriggers(projectId);
			loadFailed = false;
			if (open) runs = await listTriggerRuns(projectId, open);
			failure.ok();
		} catch (err) {
			if (triggers === undefined) {
				loadFailed = true;
				loadError = loadErrorText(err, 'Could not load the triggers.');
			} else if (quiet) {
				failure.fail(err);
			} else {
				toastApiError(err, 'Could not load the triggers.');
			}
		}
	}

	onMount(() => {
		void load();
		// The layout's own cadence: 10s in view, 60s hidden. A run that is
		// working changes state on its own, so the list is the thing that
		// polls and a fire does not hold a faster loop open.
		return pollWhileVisible(() => load(true));
	});

	async function toggleRuns(name: string) {
		if (open === name) {
			open = undefined;
			runs = undefined;
			return;
		}
		open = name;
		runs = undefined;
		try {
			runs = await listTriggerRuns(projectId, name);
		} catch (err) {
			toastApiError(err, `Could not load ${name}'s runs.`);
			open = undefined;
		}
	}

	async function onFire(name: string) {
		busy = name;
		try {
			const run = await fireTrigger(projectId, name);
			if (run.state === 'skipped') {
				toast.message(`${name}: ${reasonText(run.reason)}`);
			}
			await load();
			if (open === name) runs = await listTriggerRuns(projectId, name);
		} catch (err) {
			toastApiError(err, `Could not run ${name}.`);
		} finally {
			busy = undefined;
		}
	}

	async function onToggle(t: Trigger) {
		busy = t.name;
		try {
			await putTrigger(projectId, { name: t.name, enabled: !t.enabled });
			await load();
		} catch (err) {
			toastApiError(err, `Could not turn ${t.name} ${t.enabled ? 'off' : 'on'}.`);
		} finally {
			busy = undefined;
		}
	}

	async function onRemove(name: string) {
		busy = name;
		try {
			await deleteTrigger(projectId, name);
			confirming = undefined;
			if (open === name) open = undefined;
			await load();
			toast.success(`${name} removed.`);
		} catch (err) {
			toastApiError(err, `Could not remove ${name}.`);
		} finally {
			busy = undefined;
		}
	}

	/** The door, as the CLI's ON column prints it. */
	function door(t: Trigger): string {
		return t.kind === 'route' ? `${t.route?.name ?? ''} (route)` : (t.spec ?? '');
	}

	/** The next fire, in the reader's own zone, which is the clock they
	 *  are looking at. A trigger that is off has no next fire. */
	function next(t: Trigger): string {
		if (t.kind !== 'schedule' || !t.enabled || !t.next_at) return '';
		return dateTime(t.next_at);
	}

	function lastRun(t: Trigger): string {
		if (!t.last_run) return 'never';
		return t.last_run.state;
	}

	/** The one table of reasons (features/triggers.md §4.1): every surface
	 *  prints the same words. `gated` carries the gate's own line. */
	const REASONS: Record<string, string> = {
		off: 'the trigger is off',
		stopped: 'the machine was stopped',
		busy: 'the machine was busy',
		error: 'the machine is in error',
		route_gone: 'the route was removed',
		payload_too_large: 'the event was over 1 MiB',
		too_many_fires: 'more than 60 fires this hour',
		duplicate: 'a duplicate of an event already seen',
		gated: 'the gate refused it',
		gate_timeout: 'the gate took more than 10 seconds',
		concurrency: 'a run is working',
		plan_limit: "your plan's memory is in use",
		prompt_missing: 'the prompt file is missing',
		prompt_too_large: 'the prompt file is over 64 KB',
		no_agent: 'the agent is not installed on this machine',
		not_logged_in: 'the agent is not logged in',
		no_permission_mode: 'the agent stopped at a permission prompt',
		timeout: 'killed at the timeout',
		guest_restart: "the machine's agent supervisor restarted",
		machine_stopped: 'the machine stopped',
		unreachable: 'the machine did not take the fire',
		payload_gone: "that run's payload is no longer kept",
		dry_run: 'the gate ran and no agent was started'
	};

	function reasonText(reason?: string): string {
		if (!reason) return '';
		if (REASONS[reason]) return REASONS[reason];
		if (reason.startsWith('gated: ')) return `the gate refused it: ${reason.slice(7)}`;
		return reason;
	}

	/** What a run says for itself: the agent's own words where it sent
	 *  any, else the reason, with the count where the row stands for
	 *  several refusals in its hour. */
	function why(r: TriggerRun): string {
		const base = r.summary || reasonText(r.reason);
		if (r.skips && r.skips > 1) return `${base} (${r.skips} events)`;
		return base;
	}

	function took(r: TriggerRun): string {
		if (!r.started_at || !r.finished_at) return '';
		const ms = Date.parse(r.finished_at) - Date.parse(r.started_at);
		if (!Number.isFinite(ms) || ms < 0) return '';
		const s = Math.round(ms / 1000);
		if (s < 60) return `${s}s`;
		if (s < 3600) return `${Math.floor(s / 60)}m${String(s % 60).padStart(2, '0')}s`;
		return `${Math.floor(s / 3600)}h${String(Math.floor((s % 3600) / 60)).padStart(2, '0')}m`;
	}

	/** The dot's class, so a state reads at a glance: a failure is the
	 *  one worth finding on the page. */
	function dot(state?: string): string {
		switch (state) {
			case 'done':
				return 'hk-dot hk-dot--ok';
			case 'failed':
			case 'timeout':
				return 'hk-dot hk-dot--bad';
			case 'working':
			case 'queued':
				return 'hk-dot hk-dot--busy';
			default:
				return 'hk-dot';
		}
	}
</script>

<section class="hk-section" id="triggers" aria-labelledby="triggers-title">
	<div class="hk-section-head">
		<h2 id="triggers-title" class="hk-h2-app">
			{triggers && triggers.length > 0
				? `${triggers.length} ${triggers.length === 1 ? 'trigger' : 'triggers'}`
				: 'Triggers'}
		</h2>
	</div>

	<LoadState
		status={triggers !== undefined ? 'ready' : loadFailed ? 'failed' : 'loading'}
		onretry={() => load()}
		error={loadError}
		shape="rows"
	>
		{#if triggers && triggers.length === 0}
			<div class="hk-empty-row tempty">
				<h3 class="hk-empty-title">No triggers yet</h3>
				<span
					>A trigger starts an agent when a webhook arrives or at a time you choose. <code
						class="hk-mono">repose triggers add</code
					> makes one.</span
				>
			</div>
		{:else if triggers && triggers.length > 0}
			<div class="hk-table-wrap">
				<table class="hk-table ttable">
					<thead>
						<tr>
							<th scope="col">Name</th>
							<th scope="col" class="hidden whitespace-nowrap sm:table-cell">On</th>
							<th scope="col" class="hidden whitespace-nowrap lg:table-cell">Prompt</th>
							<th scope="col" class="w-px whitespace-nowrap">Last run</th>
							<th scope="col" class="hidden w-px whitespace-nowrap md:table-cell">Next</th>
							<th scope="col" class="w-px"><span class="hk-sr">Actions</span></th>
						</tr>
					</thead>
					<tbody>
						{#each triggers as t (t.trigger_id)}
							<tr class:off={!t.enabled}>
								<td class="tname">
									<button
										type="button"
										class="trunsbtn"
										aria-expanded={open === t.name}
										onclick={() => toggleRuns(t.name)}
										>{t.name}{#if !t.enabled}<span class="toff">off</span>{/if}</button
									>
									<span class="tsub block sm:hidden">{door(t)}</span>
								</td>
								<td class="hidden whitespace-nowrap sm:table-cell">{door(t)}</td>
								<td class="hk-mono hidden whitespace-nowrap lg:table-cell">{t.prompt_path}</td>
								<td class="whitespace-nowrap">
									<span class={dot(t.last_run?.state)}></span>{lastRun(t)}
									{#if t.last_run}<span class="twhen">{dateTime(t.last_run.created_at)}</span>{/if}
								</td>
								<td class="hidden whitespace-nowrap md:table-cell">{next(t)}</td>
								<td class="tact">
									{#if confirming !== t.name}
										<div class="tact-row">
											<button
												type="button"
												id={`fire-${t.name}`}
												class="hk-btn-ghost hk-btn-sm"
												aria-label={`Run ${t.name} now`}
												aria-busy={busy === t.name}
												disabled={busy === t.name}
												onclick={() => onFire(t.name)}
												>{busy === t.name ? 'Running…' : 'Run'}</button
											>
											<button
												type="button"
												id={`toggle-${t.name}`}
												class="hk-btn-ghost hk-btn-sm"
												aria-label={t.enabled ? `Turn ${t.name} off` : `Turn ${t.name} on`}
												disabled={busy === t.name}
												onclick={() => onToggle(t)}>{t.enabled ? 'Off' : 'On'}</button
											>
											{#if t.route}
												<CopyButton
													text={t.route.url}
													label="Copy"
													ariaLabel={`Copy the URL of ${t.name}`}
													size="sm"
												/>
											{:else}
												<button
													type="button"
													id={`remove-${t.name}`}
													class="hk-btn-ghost hk-btn-sm"
													aria-label={`Remove ${t.name}`}
													onclick={() => (confirming = t.name)}>Remove…</button
												>
											{/if}
										</div>
									{/if}
								</td>
							</tr>
							{#if confirming === t.name}
								<tr class="confirm-row">
									<td colspan="6" data-testid="confirm-remove-trigger">
										<div class="hk-confirm">
											<p>
												Remove <span class="hk-mono">{t.name}</span>?
												{#if t.route}Its route goes with it, and that URL answers 410 for 30 days.
												{/if}Its prompt file stays on the machine.
											</p>
											<div class="hk-confirm-actions">
												<button
													type="button"
													class="hk-btn-danger hk-btn-sm"
													disabled={busy === t.name}
													onclick={() => onRemove(t.name)}
													>{busy === t.name ? 'Removing…' : 'Remove'}</button
												>
												<button
													type="button"
													class="hk-btn-secondary hk-btn-sm"
													onclick={() => (confirming = undefined)}>Keep</button
												>
											</div>
										</div>
									</td>
								</tr>
							{/if}
							{#if open === t.name}
								<tr class="runs-row">
									<td colspan="6">
										{#if runs === undefined}
											<p class="tnone">Loading…</p>
										{:else if runs.length === 0}
											<p class="tnone">{t.name} has not run.</p>
										{:else}
											<ul class="truns">
												{#each runs as r (r.run_id)}
													<li>
														<span class={dot(r.state)}></span>
														<span class="trstate">{r.state}</span>
														<span class="trwhen">{dateTime(r.created_at)}</span>
														<span class="trtook">{took(r)}</span>
														<span class="trwhy">{why(r)}</span>
														{#if r.link}
															<a class="hk-btn-ghost hk-btn-sm" href={r.link.url}
																>{r.link.label ?? 'Open'}</a
															>
														{/if}
														{#if r.window}<span class="hk-mono trwin">{r.window}</span>{/if}
													</li>
												{/each}
											</ul>
										{/if}
									</td>
								</tr>
							{/if}
						{/each}
					</tbody>
				</table>
			</div>
		{/if}
	</LoadState>
</section>

<style>
	.tname {
		font-weight: 500;
	}
	/* The name opens the runs, so it is the row's own control. */
	.trunsbtn {
		all: unset;
		cursor: pointer;
		font: inherit;
		color: inherit;
	}
	.trunsbtn:focus-visible {
		outline: 2px solid var(--hk-ring);
		outline-offset: 2px;
		border-radius: 2px;
	}
	.toff {
		margin-inline-start: 0.5rem;
		font-size: 0.75rem;
		font-weight: 400;
		color: var(--hk-muted);
	}
	tr.off .tname {
		color: var(--hk-muted);
	}
	/* Under the name on a narrow screen, where the On column is hidden:
	   three buttons and a door side by side broke a 390 row. */
	.tsub,
	.twhen {
		font-size: 0.75rem;
		color: var(--hk-muted);
	}
	.twhen {
		margin-inline-start: 0.5rem;
	}
	.tact-row {
		display: flex;
		gap: 0.25rem;
		justify-content: flex-end;
	}
	.tempty span {
		color: var(--hk-muted);
	}
	.runs-row > td {
		padding-block: 0.5rem;
	}
	.truns {
		display: grid;
		gap: 0.25rem;
		margin: 0;
		padding: 0;
		list-style: none;
	}
	.truns li {
		display: flex;
		flex-wrap: wrap;
		align-items: baseline;
		gap: 0.5rem;
		font-size: 0.8125rem;
	}
	.trstate {
		min-width: 4.5rem;
	}
	.trwhen,
	.trtook,
	.trwin {
		color: var(--hk-muted);
	}
	.trwhy {
		flex: 1 1 12rem;
	}
	.tnone {
		margin: 0;
		font-size: 0.8125rem;
		color: var(--hk-muted);
	}
</style>
