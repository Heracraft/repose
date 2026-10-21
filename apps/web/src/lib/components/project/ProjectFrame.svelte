<!--
  A project's frame, shared by its five tabs (DECISIONS I-841): the crumb,
  the name, the remote and the status line, Start or Stop, the tabs, and
  under them what Start or Stop has to say (the stop question, a refusal).
  It reads the project and polls it; the tab under it reads the project
  from the context and fetches only its own data. A project that is gone
  is the 404, with a Restore when it was destroyed.
-->
<script lang="ts">
	import type { Snippet } from 'svelte';
	import { onMount } from 'svelte';
	import { MediaQuery } from 'svelte/reactivity';
	import { page } from '$app/state';
	import { goto } from '$app/navigation';
	import { resolve } from '$app/paths';
	import { toast } from 'svelte-sonner';
	import { restoreProject, startProject, stopProject } from '$lib/api/client';
	import { ApiError } from '$lib/api/errors';
	import { toastApiError } from '$lib/api/toast';
	import { pollWhileVisible } from '$lib/poll';
	import { dateTime } from '$lib/format';
	import { title } from '$lib/title';
	import { stopWarning } from '$lib/busyAgents';
	import { focusOnMount } from '$lib/focus';
	import { withoutBillingURL } from '$lib/refusal';
	import PageShell from '$lib/components/PageShell.svelte';
	import ProjectTabs from '$lib/components/ProjectTabs.svelte';
	import { tabOf } from './tabs';
	import ErrorView from '$lib/components/ErrorView.svelte';
	import LoadState from '$lib/components/LoadState.svelte';
	import RestoreNameForm from '$lib/components/RestoreNameForm.svelte';
	import ProjectMeta from './ProjectMeta.svelte';
	import { notFound } from '$lib/notFound.svelte';
	import { ProjectState, provideProject } from './context.svelte';
	import type { PaymentRequiredReason, Project } from '$lib/api/types';

	let { id, children }: { id: string; children: Snippet } = $props();

	// svelte-ignore state_referenced_locally
	const ctx = provideProject(new ProjectState(id));
	/** Below 640px the header's action is a row of its own under the status line. */
	const phone = new MediaQuery('max-width: 639.98px');

	onMount(() => pollWhileVisible(ctx.refresh));

	let project = $derived(ctx.project);
	let tab = $derived(tabOf(page.url.pathname, id));

	/**
	 * The sentence `repose stop` asks before it ends an agent's turn, or ""
	 * when nothing would be interrupted (I-614).
	 */
	let stopEnds = $derived(stopWarning(project));
	let askingStop = $state(false);

	/** The button a step under way shows in the header, waiting. */
	const WAITING: Partial<Record<Project['state'], string>> = {
		creating: 'Creating…',
		building: 'Building…',
		starting: 'Starting…',
		stopping: 'Stopping…',
		restoring: 'Restoring…',
		destroying: 'Destroying…'
	};

	async function onStart() {
		if (ctx.opBusy) return;
		ctx.opBusy = 'start';
		ctx.startBanner = undefined;
		try {
			const { op_id } = await startProject(id);
			void ctx.waitForOp(op_id, () => {
				ctx.opBusy = undefined;
				void ctx.refresh();
			});
		} catch (err) {
			ctx.opBusy = undefined;
			if (err instanceof ApiError && err.code === 'payment_required') {
				await ctx.refuse(err);
				return;
			}
			if (err instanceof ApiError && err.code === 'capacity') {
				ctx.startBanner = err.code;
				return;
			}
			toastApiError(err, 'Could not start the project.');
		}
	}

	function onStopPressed() {
		if (ctx.opBusy) return;
		if (stopEnds) {
			askingStop = true;
			return;
		}
		void onStop();
	}

	let stopSnapshotFirst = $state(true);

	async function onStop() {
		if (ctx.opBusy) return;
		askingStop = false;
		ctx.opBusy = 'stop';
		try {
			const { op_id } = await stopProject(id, stopSnapshotFirst);
			void ctx.waitForOp(op_id, () => {
				ctx.opBusy = undefined;
				void ctx.refresh();
			});
		} catch (err) {
			ctx.opBusy = undefined;
			toastApiError(err, 'Could not stop the project.');
		}
	}

	/** The link a payment_required reason wants, on the billing page. */
	function refusalLink(reason?: PaymentRequiredReason): string {
		switch (reason) {
			case 'subscription_required':
				return 'Choose a plan';
			case 'plan_limit':
				return 'Upgrade';
			case 'disk_limit':
				return 'Change plan';
			case 'egress_limit':
				return 'See usage';
			case 'past_due':
				return 'Update card';
			case 'suspended':
				return 'Pay the invoice';
			default:
				return 'Billing';
		}
	}

	let goneError = $state<string | undefined>(undefined);
	let goneBusy = $state(false);

	async function restoreGone(name: string) {
		if (!ctx.gone) return;
		goneBusy = true;
		goneError = undefined;
		try {
			const res = await restoreProject({ project_id: ctx.gone.id, name });
			toast.success(
				`Restoring ${res.name} from its snapshot of ${dateTime(res.snapshot_created_at)}.`
			);
			await goto(resolve('/projects/[id]', { id: res.project_id }));
		} catch (err) {
			if (
				err instanceof ApiError &&
				err.code === 'conflict' &&
				err.detail?.reason === 'name_taken'
			) {
				goneError = `A project called ${String(err.detail?.name ?? name)} already exists; pick another name.`;
			} else if (err instanceof ApiError && err.code === 'invalid') {
				goneError = err.message;
			} else {
				toastApiError(err, 'Could not restore the project.');
			}
		} finally {
			goneBusy = false;
		}
	}

	// The rail marks no place current while this frame draws a 404.
	$effect(() => {
		if (!ctx.notFound) return;
		notFound.on = true;
		return () => {
			notFound.on = false;
		};
	});

	// The tab's name leads the title, then the project's: "Events ·
	// job-board · repose"; Overview is the project's name alone.
	let pageTitle = $derived(
		project
			? title(tab === 'Overview' ? project.name : `${tab} · ${project.name}`)
			: ctx.notFound
				? title('Not found')
				: title(tab === 'Overview' ? 'Project' : tab)
	);
	/** Config's Apply bar holds the page's primary there. */
	let headClass = $derived(ctx.holdPrimary ? 'hk-btn-secondary' : 'hk-btn');
	let hasNotes = $derived(askingStop || !!ctx.startBanner);
</script>

<svelte:head>
	<title>{pageTitle}</title>
</svelte:head>

<!-- One height for every state's actions (a check and Stop, or a lone
     Start), so the status line and the tabs never move with the state. -->
{#snippet headActions()}
	<div class="acts">
		{#if project?.state === 'stopped'}
			<button
				type="button"
				class={headClass}
				aria-disabled={ctx.opBusy ? 'true' : undefined}
				onclick={onStart}
			>
				{ctx.opBusy === 'start' ? 'Starting…' : 'Start'}
			</button>
		{:else if project?.state === 'error'}
			<!-- POST /start restarts a machine in error on its newest
			     configuration (lifecycle.md). -->
			<button
				type="button"
				class={headClass}
				aria-disabled={ctx.opBusy ? 'true' : undefined}
				onclick={onStart}
			>
				{ctx.opBusy === 'start' ? 'Restarting…' : 'Restart'}
			</button>
		{:else if project?.state === 'running'}
			<label class="hk-check snap-check">
				<input type="checkbox" bind:checked={stopSnapshotFirst} />
				Snapshot on stop
			</label>
			<button
				type="button"
				class="hk-btn-secondary"
				aria-disabled={ctx.opBusy ? 'true' : undefined}
				onclick={onStopPressed}
			>
				{ctx.opBusy === 'stop' ? 'Stopping…' : 'Stop'}
			</button>
		{:else if project && WAITING[project.state]}
			<!-- A step under way: the button that will come back, waiting. -->
			<button type="button" class="hk-btn-secondary" aria-disabled="true"
				>{WAITING[project.state]}</button
			>
		{/if}
	</div>
{/snippet}

<!-- The remote and the state under the title, then (on a phone) Start or
     Stop, then the tabs on the title's rule. -->
{#snippet tabs()}
	{#if project}<ProjectMeta {project} />{:else}<div class="meta-hold" aria-hidden="true"></div>{/if}
	{#if phone.current}<div class="phone-acts">{@render headActions()}</div>{/if}
	<ProjectTabs {id} />
{/snippet}

{#if ctx.notFound}
	<ErrorView status={404} title="Not found">
		{#if ctx.gone}
			<p class="gone">Destroyed {dateTime(ctx.gone.destroyed_at)}.</p>
			<div class="gone-form">
				<RestoreNameForm
					id={ctx.gone.id}
					bind:value={ctx.goneName}
					error={goneError}
					busy={goneBusy}
					autofocus={false}
					onsubmit={(n) => void restoreGone(n)}
				/>
			</div>
		{/if}
	</ErrorView>
{:else}
	<!-- The h1 stays a name while the project loads: "Loading…" as the
	     page's heading is what a screen reader announced as its title. -->
	<PageShell
		title={project?.name ?? 'Project'}
		crumbs={[{ label: 'Projects', href: resolve('/projects') }]}
		{tabs}
		action={phone.current ? undefined : headActions}
	>
		{#if hasNotes}
			<div class="notes">
				{#if askingStop}
					<!-- The CLI's words, and focus on the safe choice (I-614). -->
					<div class="hk-confirm" data-testid="confirm-stop">
						<p>{stopEnds}</p>
						<div class="hk-confirm-actions">
							<button type="button" class="hk-btn-danger hk-btn-sm" onclick={onStop}
								>Stop anyway</button
							>
							<button
								type="button"
								class="hk-btn-ghost hk-btn-sm"
								use:focusOnMount
								onclick={() => (askingStop = false)}>Keep it running</button
							>
						</div>
					</div>
				{/if}
				{#if ctx.startBanner === 'payment_required'}
					<div
						class="hk-banner hk-banner--err"
						role="alert"
						data-testid="refusal"
						data-reason={ctx.refusal?.reason ?? 'unknown'}
					>
						<p>
							{ctx.refusal
								? withoutBillingURL(ctx.refusal.message)
								: 'A plan is needed before a machine can start.'}
							<a href={resolve('/billing')}>{refusalLink(ctx.refusal?.reason)}</a>
						</p>
						{#if ctx.refusal && ctx.refusal.projects.length > 0}
							<div class="refusal-stops">
								{#each ctx.refusal.projects as named (named.id)}
									<button
										type="button"
										class="hk-btn-secondary hk-btn-sm"
										aria-disabled={ctx.opBusy ? 'true' : undefined}
										onclick={() => ctx.stopNamed(named)}>Stop {named.slug}</button
									>
								{/each}
							</div>
						{/if}
					</div>
				{:else if ctx.startBanner === 'capacity'}
					<div class="hk-banner" role="status">
						<p>No capacity right now.</p>
					</div>
				{/if}
			</div>
		{/if}
		{#if !project && ctx.loadFailed}
			<div class="min-h-screen">
				<LoadState status="failed" onretry={ctx.refresh} error={ctx.loadError} />
			</div>
		{:else}
			{#if !project}
				<div class="min-h-screen">
					<LoadState status="loading" onretry={ctx.refresh} />
				</div>
			{/if}
			<!-- The tab mounts with the frame, so its own reads go out with the
			     project's, but it is drawn only once the header is: what it
			     paints then is new, never moved (layout shift, I-664). -->
			<div hidden={!project} class="contents">
				{@render children()}
			</div>
		{/if}
	</PageShell>
{/if}

<style>
	.acts {
		display: flex;
		flex-wrap: wrap;
		align-items: baseline;
		gap: 0.5rem;
		min-height: 2.25rem;
	}
	/* The remote and the status line's height while the project loads, so
	   the tabs do not move down when they arrive. */
	.meta-hold {
		height: 3.25rem;
		margin: -0.875rem 0 1.25rem;
	}
	.phone-acts {
		margin: -0.25rem 0 1rem;
	}
	.snap-check {
		font-size: 0.8125rem;
		color: var(--hk-text-2);
		margin-right: 0.5rem;
	}
	.notes {
		display: grid;
		gap: 0.75rem;
		margin-bottom: 1.75rem;
	}
	.notes :global(.hk-banner) {
		margin: 0;
	}
	.refusal-stops {
		display: flex;
		flex-wrap: wrap;
		gap: 0.5rem;
	}
	.gone {
		margin: 0;
	}
	.gone-form {
		margin-top: 1rem;
		max-width: 40rem;
	}
</style>
