<!--
  A project's triggers, on its Triggers tab (DECISIONS I-1248..I-1262,
  I-1360): every way the outside reaches the machine, with what answers
  it. A row is a trigger (a webhook or a schedule starts an agent with a
  prompt) or a webhook route that forwards to the app on a port in the
  machine, which I-1110 put under Webhooks on Config and I-1360 moved
  here: the URL, the token, the rates and the start are the same door,
  and the only difference is who receives the delivery. A row opens into
  its runs, or for a forwarding route its URL and last delivery.

  Run fires it now. A fire is not an op: it never takes ctx.opBusy, so
  Stop, Start and a snapshot stay available while an agent runs (I-1258).
  The button's label changes and it disables; there is no spinner, which
  is the house rule.

  The four inline buttons are Run, Edit, On/Off and Remove, the same on
  every row, and the one destructive action is last so it never moves.
  Four short ghost buttons measure 236px at 390, where the row is a
  block and they have a line to themselves; I-1258 said three because
  the fourth it weighed was Copy, in a row that also drew the target.
  A URL is never drawn, on a trigger's row or a forwarding route's: it
  holds the route's token, a dashboard on a shared screen is a screen,
  and the user who needs the characters copies them. Copy puts it on
  the clipboard.

  Add and Edit open the same form (TriggerForm.svelte). I-1258 said the
  tab would have no Add, because making a trigger means writing a prompt
  into the machine and a browser cannot reach one; I-1266 gave the api a
  `prompt` it carries to the machine and forgets, so it can.
-->
<script lang="ts">
	import { onMount } from 'svelte';
	import { toast } from 'svelte-sonner';
	import {
		listTriggers,
		listTriggerRuns,
		putTrigger,
		deleteTrigger,
		fireTrigger,
		listWebhooks,
		rotateWebhook,
		deleteWebhook
	} from '$lib/api/client';
	import TriggerForm, { type Saved } from './TriggerForm.svelte';
	import { toastApiError, PollFailure, PollGroup } from '$lib/api/toast';
	import CopyButton from '$lib/hk/CopyButton.svelte';
	import LoadState, { loadErrorText } from '$lib/components/LoadState.svelte';
	import { focusAfterRender, focusOnMount } from '$lib/focus';
	import { agentLink } from '$lib/agent-link';
	import { pollWhileVisible } from '$lib/poll';
	import { ageShort, dateTime } from '$lib/format';
	import { reasonSentence } from '$lib/triggers';
	import type { Project, Trigger, TriggerRun, WebhookRoute } from '$lib/api/types';

	let { project }: { project: Project } = $props();
	const projectId = $derived(project.id);

	let triggers = $state<Trigger[] | undefined>(undefined);
	/** The project's webhook routes; the ones no trigger owns are rows here. */
	let routes = $state<WebhookRoute[] | undefined>(undefined);
	const forwards = $derived((routes ?? []).filter((r) => !r.trigger));
	/** Every door: the triggers, then the routes that forward to the app. */
	const rows = $derived.by(() => {
		if (!triggers) return undefined;
		return [
			...triggers.map((t) => ({ key: `t-${t.trigger_id}`, name: t.name, trigger: t })),
			...forwards.map((r) => ({ key: `r-${r.route_id}`, name: r.name, route: r }))
		] as Row[];
	});
	let loadFailed = $state(false);
	let loadError = $state<string | undefined>(undefined);
	/** The trigger whose runs are open, and those runs. */
	let open = $state<string | undefined>(undefined);
	let runs = $state<TriggerRun[] | undefined>(undefined);
	/** The row whose Remove… or Rotate… was pressed once. */
	let confirming = $state<{ key: string; what: 'remove' | 'rotate' } | undefined>(undefined);
	/** The row whose Run, on/off, rotate or remove is in flight. */
	let busy = $state<string | undefined>(undefined);
	/** The form that is open: a new trigger, or the one being changed. */
	let form = $state<{ trigger?: Trigger; route?: WebhookRoute } | undefined>(undefined);

	type Row = { key: string; name: string; trigger?: Trigger; route?: WebhookRoute };

	const pollFailures = new PollGroup();
	const failure = new PollFailure('Could not load the triggers.', pollFailures);

	async function load(quiet = false) {
		try {
			[triggers, routes] = await Promise.all([listTriggers(projectId), listWebhooks(projectId)]);
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

	async function onFire(name: string, dryRun = false) {
		busy = name;
		try {
			const run = await fireTrigger(projectId, name, dryRun);
			if (run.state === 'skipped') {
				toast.message(`${name}: ${reasonSentence(run.reason)}`);
			}
			await load();
			if (open === name) runs = await listTriggerRuns(projectId, name);
		} catch (err) {
			toastApiError(err, `Could not run ${name}.`);
		} finally {
			busy = undefined;
		}
	}

	/** Add and Edit open the same form; Edit opens the row it is on. */
	function openForm(row?: Row) {
		form = { trigger: row?.trigger, route: row?.route };
	}

	async function onSaved(saved: Saved, created: boolean) {
		form = undefined;
		await load();
		const name = saved.kind === 'trigger' ? saved.trigger.name : saved.route.name;
		if (created) {
			// What `repose triggers add` and `repose webhooks add` print: a
			// URL is a copy away in the row's expansion, which opens, and a
			// schedule says the zone it stored and the next fire, because a
			// user has to see that 07:00 is their 07:00 (I-1252).
			if (saved.kind === 'trigger' && saved.trigger.kind === 'schedule') {
				const t = saved.trigger;
				toast.success(`${t.name}: ${t.spec} ${t.zone}, next ${next(t) || 'when it is on'}`);
			} else {
				toast.success(`${name} has a URL. Copy it into the provider's dashboard.`);
			}
			open = name;
			runs = saved.kind === 'trigger' ? await listTriggerRuns(projectId, name).catch(() => []) : [];
		} else {
			toast.success(`${name} saved.`);
		}
		void focusAfterRender('trigger-add-open', `open-${name}`);
	}

	/** A forwarding route's token, replaced at once; the new URL goes on the clipboard. */
	async function onRotate(r: WebhookRoute) {
		busy = r.name;
		try {
			const rotated = await rotateWebhook(projectId, r.name);
			confirming = undefined;
			await load();
			await navigator.clipboard.writeText(rotated.url).catch(() => undefined);
			toast.success(`Rotated ${r.name}; the new URL is on your clipboard.`);
			void focusAfterRender(`rotate-${r.name}`);
		} catch (err) {
			toastApiError(err, `Could not rotate ${r.name}.`);
		} finally {
			busy = undefined;
		}
	}

	async function onRemoveRoute(r: WebhookRoute) {
		busy = r.name;
		try {
			await deleteWebhook(projectId, r.name);
			confirming = undefined;
			if (open === r.name) open = undefined;
			await load();
			toast.success(`${r.name} removed.`);
			const next = rows?.[0]?.name;
			if (next) void focusAfterRender(`open-${next}`);
		} catch (err) {
			toastApiError(err, `Could not remove ${r.name}.`);
		} finally {
			busy = undefined;
		}
	}

	/** The last delivery of a forwarding route, in `repose webhooks`' status words. */
	function lastDelivery(r: WebhookRoute): string {
		const d = r.last_delivery;
		if (!d) return 'never';
		if (d.outcome === 'delivered' || d.outcome === 'woke') return `${d.status}`;
		return `${d.status} ${d.outcome}`;
	}

	function targetOf(r: WebhookRoute): string {
		return `:${r.port}${r.path === '/' ? '/' : r.path}`;
	}

	/** Who answers the door: an agent, or the app on a port in the machine. */
	function receiver(row: Row): string {
		return row.route ? 'your app' : 'an agent';
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
			// The row that asked is gone, so focus lands on the next
			// row's own control rather than on <body> (WCAG 2.4.3).
			const next = rows?.[0]?.name;
			if (next) void focusAfterRender(`open-${next}`);
		} catch (err) {
			toastApiError(err, `Could not remove ${name}.`);
		} finally {
			busy = undefined;
		}
	}

	/** Keep, which puts focus back on the button that opened the confirm. */
	function keep(name: string, what: 'remove' | 'rotate' = 'remove'): void {
		confirming = undefined;
		void focusAfterRender(`${what}-${name}`);
	}

	/**
	 * The door. A webhook is `webhook`; a route the CLI named apart from
	 * its trigger (`--route recruiting`) says which, because that is the
	 * name in `repose webhooks`. A schedule is its spec.
	 */
	function door(row: Row): string {
		const t = row.trigger;
		if (!t) return 'webhook';
		if (t.kind !== 'route') return t.spec ?? '';
		const r = t.route?.name;
		return r && r !== t.name ? `webhook ${r}` : 'webhook';
	}

	/**
	 * The next fire, in the trigger's own zone. `add` prints the zone it
	 * stored so a user sees that 07:00 is their 07:00 (I-1252), and a
	 * dashboard drawing the same fire in the reader's browser zone undid
	 * that: a `weekdays 07:00 Europe/Berlin` trigger read `06:00
	 * tomorrow` in London. A trigger that is off has no next fire.
	 */
	function next(t: Trigger): string {
		if (t.kind !== 'schedule' || !t.enabled || !t.next_at) return '';
		const zone = t.zone || 'UTC';
		const at = new Date(t.next_at);
		const hhmm = at.toLocaleTimeString('en-GB', {
			hour: '2-digit',
			minute: '2-digit',
			hourCycle: 'h23',
			timeZone: zone
		});
		const day = (d: Date) => d.toLocaleDateString('en-CA', { timeZone: zone });
		if (day(at) === day(new Date())) return `${hhmm} today`;
		if (day(at) === day(new Date(Date.now() + 86_400_000))) return `${hhmm} tomorrow`;
		return `${hhmm} ${at.toLocaleDateString('en-GB', { weekday: 'short', timeZone: zone })}`;
	}

	/**
	 * The zone a schedule's times are in, shown beside the spec where it
	 * is not the reader's own: `07:00` means nothing without it.
	 */
	function zoneNote(t?: Trigger): string {
		if (!t || t.kind !== 'schedule' || !t.zone) return '';
		const here = Intl.DateTimeFormat().resolvedOptions().timeZone;
		return t.zone === here ? '' : t.zone;
	}

	function lastRun(t: Trigger): string {
		if (!t.last_run) return 'never';
		return t.last_run.state;
	}

	/** What a run says for itself: the agent's own words where it sent
	 *  any, else the reason, with the count where the row stands for
	 *  several refusals in its hour. */
	function why(r: TriggerRun): string {
		const base = r.summary || reasonSentence(r.reason);
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

	/**
	 * A run's link, through the same check an agent event's goes
	 * through (agent-link.ts): http or https, no credentials in it, and
	 * the host shown beside the label, because the words come from the
	 * agent and the host is what says where they lead (I-931).
	 */
	function runLink(r: TriggerRun) {
		return agentLink(r.link ?? undefined);
	}

	/** The dot's class, so a state reads at a glance: a failure is the
	 *  one worth finding on the page. */
	function dot(state?: string): string {
		switch (state) {
			case 'done':
				return 'hk-dot hk-dot--ok';
			case 'failed':
			case 'timeout':
				return 'hk-dot hk-dot--err';
			case 'skipped':
				return 'hk-dot hk-dot--warn';
			case 'working':
				return 'hk-dot hk-dot--live';
			default:
				return 'hk-dot';
		}
	}
</script>

<section class="hk-section" id="triggers" aria-labelledby="triggers-title">
	<div class="hk-section-head">
		<h2 id="triggers-title" class="hk-h2-app">
			{rows && rows.length > 0
				? `${rows.length} ${rows.length === 1 ? 'trigger' : 'triggers'}`
				: 'Triggers'}
		</h2>
		{#if triggers && !form}
			<button
				type="button"
				id="trigger-add-open"
				class="hk-btn-secondary hk-btn-sm"
				onclick={() => openForm()}>Add trigger</button
			>
		{/if}
	</div>

	<LoadState
		status={triggers !== undefined ? 'ready' : loadFailed ? 'failed' : 'loading'}
		onretry={() => load()}
		error={loadError}
		shape="rows"
	>
		{#if rows && rows.length === 0 && !form}
			<div class="hk-empty-row tempty">
				<h3 class="hk-empty-title">No triggers yet</h3>
				<span
					>A trigger starts an agent when a webhook arrives or at a time you choose, or hands the
					webhook to your app.</span
				>
			</div>
		{:else if rows && rows.length > 0}
			<div class="hk-table-wrap">
				<table class="hk-table ttable">
					<thead>
						<tr>
							<th scope="col">Name</th>
							<th scope="col" class="hidden whitespace-nowrap sm:table-cell">Fires on</th>
							<th scope="col" class="hidden whitespace-nowrap md:table-cell">Then</th>
							<th scope="col" class="w-px whitespace-nowrap">Last</th>
							<th scope="col" class="hidden w-px whitespace-nowrap md:table-cell">Next</th>
							<th scope="col" class="w-px"><span class="hk-sr">Actions</span></th>
						</tr>
					</thead>
					<tbody>
						{#each rows as row (row.key)}
							{@const t = row.trigger}
							{@const r = row.route}
							<tr class:off={t && !t.enabled}>
								<td class="tname">
									<button
										type="button"
										id={`open-${row.name}`}
										class="trunsbtn"
										aria-expanded={open === row.name}
										onclick={() => toggleRuns(row.name)}
										>{row.name}{#if t && !t.enabled}<span class="toff">off</span>{/if}</button
									>
									<!-- The door, where the Fires on column is not shown: a list
									     of names alone does not say which is a webhook and which
									     is a schedule, which is the first thing to know. -->
									<span class="tdoor"
										>{door(row)}{#if zoneNote(t)}<span class="tzone">{zoneNote(t)}</span>{/if}<span
											class="tzone">{receiver(row)}</span
										></span
									>
								</td>
								<td class="hidden whitespace-nowrap sm:table-cell"
									>{door(row)}{#if zoneNote(t)}<span class="tzone">{zoneNote(t)}</span>{/if}</td
								>
								<!-- Who answers it: an agent, with its prompt where the width
								     holds it, or the app, with the port it listens on. -->
								<td class="tthen hidden md:table-cell">
									{receiver(row)}
									{#if r}
										<span class="hk-mono tdetail">{targetOf(r)}</span>
									{:else if t}
										<span class="hk-mono tdetail hidden 2xl:inline" title={t.prompt_path}
											>{t.prompt_path}</span
										>
									{/if}
								</td>
								<td class="tlast whitespace-nowrap">
									{#if t}
										<span class={dot(t.last_run?.state)}></span>{lastRun(t)}
										{#if t.last_run}<span class="twhen" title={dateTime(t.last_run.created_at)}
												>{ageShort(t.last_run.created_at)}</span
											>{/if}
									{:else if r}
										<span
											class={r.last_delivery
												? r.last_delivery.status < 300
													? 'hk-dot hk-dot--ok'
													: 'hk-dot hk-dot--err'
												: 'hk-dot'}
										></span>{lastDelivery(r)}
										{#if r.last_delivery}<span class="twhen" title={dateTime(r.last_delivery.at)}
												>{ageShort(r.last_delivery.at)}</span
											>{/if}
									{/if}
								</td>
								<td class="hidden whitespace-nowrap md:table-cell">{t ? next(t) : ''}</td>
								<td class="tact">
									{#if confirming?.key !== row.key}
										<div class="tact-row">
											{#if t}
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
													id={`edit-${t.name}`}
													class="hk-btn-ghost hk-btn-sm"
													aria-label={`Change ${t.name}`}
													onclick={() => openForm(row)}>Edit</button
												>
												<button
													type="button"
													id={`toggle-${t.name}`}
													class="hk-btn-ghost hk-btn-sm"
													aria-label={t.enabled ? `Turn ${t.name} off` : `Turn ${t.name} on`}
													disabled={busy === t.name}
													onclick={() => onToggle(t)}>{t.enabled ? 'Off' : 'On'}</button
												>
												<button
													type="button"
													id={`remove-${t.name}`}
													class="hk-btn-ghost hk-btn-sm"
													aria-label={`Remove ${t.name}`}
													onclick={() => (confirming = { key: row.key, what: 'remove' })}
													>Remove…</button
												>
											{:else if r}
												<!-- A forwarding route has no run and no off: a provider
												     delivers or it does not. Its four are Copy, Edit,
												     Rotate and Remove, the last still last. -->
												<CopyButton
													text={r.url}
													label="Copy"
													ariaLabel={`Copy the URL of ${r.name}`}
													size="sm"
												/>
												<button
													type="button"
													id={`edit-${r.name}`}
													class="hk-btn-ghost hk-btn-sm"
													aria-label={`Change ${r.name}`}
													onclick={() => openForm(row)}>Edit</button
												>
												<button
													type="button"
													id={`rotate-${r.name}`}
													class="hk-btn-ghost hk-btn-sm"
													aria-label={`Rotate the token of ${r.name}`}
													onclick={() => (confirming = { key: row.key, what: 'rotate' })}
													>Rotate…</button
												>
												<button
													type="button"
													id={`remove-${r.name}`}
													class="hk-btn-ghost hk-btn-sm"
													aria-label={`Remove ${r.name}`}
													onclick={() => (confirming = { key: row.key, what: 'remove' })}
													>Remove…</button
												>
											{/if}
										</div>
									{/if}
								</td>
							</tr>
							{#if confirming?.key === row.key && t}
								<tr class="confirm-row">
									<td colspan="6" data-testid="confirm-remove-trigger">
										<div class="hk-confirm">
											<p>
												Remove <span class="hk-mono">{t.name}</span>?
												{#if t.route}Its URL answers 410 for 30 days.
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
													use:focusOnMount
													onclick={() => keep(t.name)}>Keep</button
												>
											</div>
										</div>
									</td>
								</tr>
							{:else if confirming?.key === row.key && r}
								{@const what = confirming.what}
								<tr class="confirm-row">
									<td colspan="6" data-testid={`confirm-${what}-webhook`}>
										<div class="hk-confirm">
											{#if what === 'rotate'}
												<p>
													Rotate <span class="hk-mono">{r.name}</span>? The URL in the provider's
													dashboard stops working at once and answers 410 for 30 days. The new URL
													goes on your clipboard.
												</p>
											{:else}
												<p>
													Remove <span class="hk-mono">{r.name}</span>? Its URL answers 410 for 30
													days, then 404.
												</p>
											{/if}
											<div class="hk-confirm-actions">
												<button
													type="button"
													class={what === 'remove'
														? 'hk-btn-danger hk-btn-sm'
														: 'hk-btn-secondary hk-btn-sm'}
													disabled={busy === r.name}
													onclick={() => (what === 'rotate' ? onRotate(r) : onRemoveRoute(r))}
													>{busy === r.name
														? what === 'rotate'
															? 'Rotating…'
															: 'Removing…'
														: what === 'rotate'
															? 'Rotate'
															: 'Remove'}</button
												>
												<button
													type="button"
													class="hk-btn-secondary hk-btn-sm"
													use:focusOnMount
													onclick={() => keep(r.name, what)}>Keep</button
												>
											</div>
										</div>
									</td>
								</tr>
							{/if}
							{#if open === row.name && r}
								<tr class="runs-row">
									<td colspan="6">
										<p class="turl">
											<span
												>Forwards to <span class="hk-mono rname">{targetOf(r)}</span>. A delivery
												{r.allow_start ? 'starts' : 'does not start'} a stopped machine.</span
											>
											<CopyButton
												text={r.url}
												label="Copy URL"
												ariaLabel={`Copy the URL of ${r.name}`}
												size="sm"
											/>
										</p>
										{#if r.last_delivery}
											<p class="tnone">
												Last delivery {lastDelivery(r)}, {ageShort(r.last_delivery.at)}.
											</p>
										{:else}
											<p class="tnone">{r.name} has had no delivery.</p>
										{/if}
									</td>
								</tr>
							{/if}
							{#if open === row.name && t}
								<tr class="runs-row">
									<td colspan="6">
										<p class="turl">
											{#if t.kind === 'schedule' && t.enabled && t.next_at}
												<span class="md:hidden">next {next(t)}</span>
											{/if}
											<span class="2xl:hidden">{t.prompt_path}</span>
											<!-- A dry run takes the admission decision and runs the
											     gate and starts no agent, which is how a gate is
											     written (I-1254). -->
											<button
												type="button"
												id={`dry-${t.name}`}
												class="hk-btn-ghost hk-btn-sm"
												aria-label={`Dry run ${t.name}`}
												disabled={busy === t.name}
												onclick={() => onFire(t.name, true)}>Dry run</button
											>
										</p>
										{#if t.route}
											<p class="turl">
												<span
													>{t.route.name === t.name
														? 'Its URL'
														: `Its URL, the route ${t.route.name}`}</span
												>
												<CopyButton
													text={t.route.url}
													label="Copy URL"
													ariaLabel={`Copy the URL of ${t.name}`}
													size="sm"
												/>
											</p>
										{/if}
										{#if runs === undefined}
											<p class="tnone">Loading…</p>
										{:else if runs.length === 0}
											<p class="tnone">{t.name} has not run.</p>
										{:else}
											<ul class="truns">
												{#each runs as run (run.run_id)}
													<li>
														<span class={dot(run.state)}></span>
														<span class="trstate">{run.state}</span>
														<!-- The door this run came in by, the word the CLI's
														     SOURCE column prints: a hand fire and a schedule's
														     fire are otherwise the same row. -->
														<span class="trsrc">{run.source}</span>
														<span class="trwhen" title={dateTime(run.created_at)}
															>{ageShort(run.created_at)}</span
														>
														<span class="trtook">{took(run)}</span>
														<span class="trwhy">{why(run)}</span>
														{#if runLink(run)}
															{@const l = runLink(run)!}
															<!-- eslint-disable svelte/no-navigation-without-resolve -- the agent's own address, checked by agentLink -->
															<a
																class="hk-btn-ghost hk-btn-sm"
																href={l.href}
																target="_blank"
																rel="noopener noreferrer">{l.text}</a
															>
															<!-- eslint-enable svelte/no-navigation-without-resolve -->
														{/if}
														{#if run.window}<span class="hk-mono trwin">{run.window}</span>{/if}
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

		{#if form}
			<TriggerForm
				{project}
				trigger={form.trigger}
				route={form.route}
				onsaved={onSaved}
				oncancel={() => (form = undefined)}
			/>
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
		border-radius: var(--hk-radius-xs);
	}
	/* The door under the name, at the widths where the On column is not
	   drawn. The media query is the component's own, as EventRow's is: a
	   scoped rule outranks Tailwind's `sm:hidden`
	   (the scoping class makes it one class more specific), so the
	   utility drew the door twice at 1440. */
	.tdoor {
		display: block;
		font-size: 0.75rem;
		font-weight: 400;
		color: var(--hk-muted);
	}
	@media (min-width: 640px) {
		.tdoor {
			display: none;
		}
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
	.twhen,
	.tzone {
		margin-inline-start: 0.5rem;
		font-size: 0.75rem;
		color: var(--hk-muted);
	}
	.twhen {
		margin-inline-start: 0.5rem;
	}
	/* The dot and its word, with the gap every other dotted line on
	   these tabs has (MachineCard's disk-full, EventRow's kind). */
	.tlast :global(.hk-dot),
	.truns :global(.hk-dot) {
		margin-inline-end: 0.375rem;
	}
	.tact {
		white-space: nowrap;
	}
	.tact-row {
		display: flex;
		gap: 0.25rem;
		justify-content: flex-end;
	}
	/* The receiver and, after it, the one value that may be long: the
	   prompt path or the port and path. It truncates; its whole value is
	   in the row's expansion. */
	.tthen {
		max-width: 22rem;
		white-space: nowrap;
	}
	.tdetail {
		display: inline-block;
		max-width: 14rem;
		margin-inline-start: 0.375rem;
		overflow: hidden;
		text-overflow: ellipsis;
		vertical-align: bottom;
		font-size: 0.8125rem;
		color: var(--hk-muted);
	}
	.tthen .tdetail.hidden {
		display: none;
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
		overflow-wrap: anywhere;
	}
	.trstate {
		min-width: 4.5rem;
	}
	.trsrc,
	.trwhen,
	.trtook,
	.trwin {
		color: var(--hk-muted);
	}
	.trsrc {
		min-width: 2.75rem;
	}
	.trwhy {
		flex: 1 1 12rem;
	}
	/* A route trigger's own line in its expansion: the route's name and
	   the button that puts the URL on the clipboard. The URL is never
	   drawn; the reason is in the comment at the top of this file. */
	.turl .rname {
		font-weight: 500;
		color: var(--hk-text);
	}
	.turl {
		display: flex;
		flex-wrap: wrap;
		align-items: center;
		gap: 0.5rem 0.75rem;
		margin: 0 0 0.5rem;
		font-size: 0.8125rem;
		color: var(--hk-muted);
	}
	@media (max-width: 639.98px) {
		.ttable thead {
			display: none;
		}
		.ttable tbody tr {
			display: block;
			padding-block: 0.625rem;
			border-top: 1px solid var(--hk-hairline);
		}
		.ttable tbody tr:first-child {
			border-top: 0;
		}
		.ttable tbody td {
			padding: 0;
			border-top: 0;
		}
		/* Only these three: the door, the prompt and the next fire carry
		   Tailwind's `hidden`, and a `display` here would outrank it. */
		.ttable tbody td.tname,
		.ttable tbody td.tlast,
		.ttable tbody td.tact {
			display: block;
		}
		.ttable td.tname {
			overflow-wrap: break-word;
		}
		.ttable td.tlast {
			margin-top: 0.125rem;
		}
		.ttable td.tact {
			margin-top: 0.5rem;
			padding-block: 0;
		}
		.tact-row {
			justify-content: flex-start;
		}
		.runs-row > td,
		.confirm-row td {
			padding-top: 0.5rem;
		}
	}

	.tnone {
		margin: 0;
		font-size: 0.8125rem;
		color: var(--hk-muted);
	}
</style>
