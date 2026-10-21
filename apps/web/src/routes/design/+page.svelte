<script lang="ts">
	// Every shared primitive (spec 1.6), in the order of that table, with
	// sample strings from repose's own screens. Dev only (+page.ts).
	import { toast } from 'svelte-sonner';
	import PageShell from '$lib/components/PageShell.svelte';
	import ThemeSwitch from '$lib/components/ThemeSwitch.svelte';
	import StateDot from '$lib/components/StateDot.svelte';
	import Meter from '$lib/components/Meter.svelte';
	import CodeCopy from '$lib/components/CodeCopy.svelte';
	import ConfirmType from '$lib/components/ConfirmType.svelte';
	import LoadState from '$lib/components/LoadState.svelte';
	import CodeBlock from '$lib/hk/CodeBlock.svelte';
	import CommandList from '$lib/hk/CommandList.svelte';
	import CopyButton from '$lib/hk/CopyButton.svelte';
	import Segmented from '$lib/hk/Segmented.svelte';
	import { INSTALL_COMMAND } from '$lib/links';
	import type { GuestState } from '$lib/api/types';

	const states: GuestState[] = ['running', 'creating', 'starting', 'stopping', 'stopped', 'error'];
	let range = $state<'hour' | 'day' | 'week'>('day');
	let usage = $state('day');
	let tab = $state<'menu' | 'nix'>('menu');
	let confirming = $state(false);
	let agree = $state(true);
	const SESSION = [
		'$ repose run',
		'Starting todo-app (large, 4 cores, 16 GB).',
		'Ready in 16s.'
	].join('\n');
</script>

<svelte:head><title>Design · repose</title></svelte:head>

<PageShell title="Design" lede="Every shared primitive, light and dark. Dev only.">
	{#snippet action()}<ThemeSwitch />{/snippet}

	<section class="hk-section">
		<div class="hk-section-head"><h2 class="hk-h2-app">Buttons</h2></div>
		<div class="g-row">
			<button type="button" class="hk-btn">Start</button>
			<button type="button" class="hk-btn-secondary">Stop</button>
			<button type="button" class="hk-btn-ghost">Cancel</button>
			<button type="button" class="hk-btn-danger">Destroy…</button>
			<button type="button" class="hk-btn-text-danger">Cancel plan</button>
		</div>
		<div class="g-row">
			<button type="button" class="hk-btn hk-btn-lg">Get started</button>
			<button type="button" class="hk-btn hk-btn-sm">Create snapshot</button>
			<button type="button" class="hk-btn-secondary hk-btn-sm">Resize…</button>
			<button type="button" class="hk-btn-ghost hk-btn-sm">Show all 14</button>
		</div>
		<div class="g-row">
			<button type="button" class="hk-btn" aria-disabled="true">Starting…</button>
			<button type="button" class="hk-btn-secondary" aria-disabled="true">Stopping…</button>
			<button type="button" class="hk-btn-secondary" disabled>Save</button>
			<button type="button" class="hk-btn-danger" disabled>Destroy todo-app</button>
			<button type="button" class="hk-btn-danger hk-btn-danger--armed">Destroy todo-app</button>
		</div>
	</section>

	<section class="hk-section">
		<div class="hk-section-head"><h2 class="hk-h2-app">Badges</h2></div>
		<div class="g-row">
			<span class="hk-badge hk-badge--warn hk-num">3</span>
			<span class="hk-badge hk-badge--outline">temporary</span>
			<span class="hk-badge hk-badge--outline">Current</span>
			<span class="hk-badge hk-badge--outline">refunded</span>
			<span class="hk-badge hk-badge--mono">small</span>
		</div>
	</section>

	<section class="hk-section">
		<div class="hk-section-head"><h2 class="hk-h2-app">State dots</h2></div>
		<div class="g-row">
			{#each states as s (s)}<StateDot state={s} />{/each}
			<StateDot state="running" frozen />
			<span class="inline-flex items-center gap-2"
				><span class="hk-dot hk-dot--warn" aria-hidden="true"></span>needs input</span
			>
		</div>
	</section>

	<section class="hk-section">
		<div class="hk-section-head"><h2 class="hk-h2-app">Banners</h2></div>
		<div class="hk-banner" role="status">
			Over by 50 GB: $2.50 on the next invoice at $0.05 a GB.
		</div>
		<div class="hk-banner hk-banner--err" role="alert">
			The last payment failed. <button type="button" class="hk-banner-action">Update card</button>
		</div>
		<div class="hk-banner hk-banner--ok" role="status">Applied.</div>
		<div class="hk-banner hk-banner--info" role="status">Billing is not switched on yet.</div>
		<div class="outage-sample">Cannot reach the API. Retrying…</div>
	</section>

	<section class="hk-section">
		<div class="hk-section-head"><h2 class="hk-h2-app">Inputs</h2></div>
		<div class="hk-fields">
			<div>
				<label class="hk-field-label" for="d-name">Name</label>
				<input id="d-name" class="hk-input" value="OPENAI_API_KEY" />
			</div>
			<div>
				<label class="hk-field-label" for="d-search">Search projects</label>
				<input id="d-search" class="hk-input hk-input--search" type="search" />
			</div>
			<div>
				<label class="hk-field-label" for="d-grow">Grow to</label>
				<select id="d-grow" class="hk-select"><option>160 GB</option><option>320 GB</option></select
				>
			</div>
			<div>
				<label class="hk-field-label" for="d-value">Value</label>
				<textarea id="d-value" class="hk-textarea" aria-invalid="true" aria-describedby="d-err"
				></textarea>
				<p id="d-err" class="hk-field-error">Secret values are limited to 64 KB.</p>
			</div>
			<label class="hk-check"
				><input type="checkbox" bind:checked={agree} /> Email notifications</label
			>
		</div>
	</section>

	<section class="hk-section">
		<div class="hk-section-head"><h2 class="hk-h2-app">Joined controls</h2></div>
		<div class="stack">
			<Segmented
				options={[
					{ value: 'hour', label: 'Hour' },
					{ value: 'day', label: 'Day' },
					{ value: 'week', label: 'Week' }
				]}
				bind:value={range}
				name="d-range"
				label="Range"
			/>
			<nav aria-label="Filter projects" class="hk-seg--links">
				<a href="#all" aria-current="page">All</a>
				<a href="#running">Running</a>
				<a href="#stopped">Stopped</a>
			</nav>
			<div class="hk-seg--buttons" role="group" aria-label="Usage range">
				{#each ['hour', 'day', 'week'] as r (r)}
					<button type="button" aria-pressed={usage === r} onclick={() => (usage = r)}
						>{r[0].toUpperCase() + r.slice(1)}</button
					>
				{/each}
			</div>
			<div class="hk-tablist" role="tablist" aria-label="Editor">
				<button
					type="button"
					role="tab"
					aria-selected={tab === 'menu'}
					onclick={() => (tab = 'menu')}>Menu</button
				>
				<button type="button" role="tab" aria-selected={tab === 'nix'} onclick={() => (tab = 'nix')}
					>Nix</button
				>
			</div>
		</div>
	</section>

	<section class="hk-section">
		<div class="hk-section-head"><h2 class="hk-h2-app">Page tabs</h2></div>
		<nav aria-label="Project" class="hk-tabs tabs-sample">
			<a href="#overview" aria-current="page">Overview</a>
			<a href="#config">Config</a>
			<a href="#secrets">Secrets</a>
		</nav>
	</section>

	<section class="hk-section">
		<div class="hk-section-head">
			<h2 class="hk-h2-app">Table</h2>
			<span>10 of 35 shown</span>
		</div>
		<div class="hk-table-wrap" role="region" aria-label="Snapshots">
			<table class="hk-table hk-table--hover">
				<thead>
					<tr><th>Snapshot</th><th>Taken</th><th class="hk-num-col">Size</th><th></th></tr>
				</thead>
				<tbody>
					<tr>
						<td><a class="hk-link" href="#s1">nightly</a></td>
						<td>2026-10-20 23:06</td>
						<td class="hk-num-col">3.4 GB</td>
						<td class="hk-num-col"
							><button type="button" class="hk-btn-ghost hk-btn-sm">Restore…</button></td
						>
					</tr>
					<tr>
						<td><a class="hk-link" href="#s2">before-upgrade</a></td>
						<td>just now</td>
						<td class="hk-num-col">812 MB</td>
						<td class="hk-num-col"
							><button type="button" class="hk-btn-ghost hk-btn-sm">Restore…</button></td
						>
					</tr>
				</tbody>
			</table>
		</div>
	</section>

	<section class="hk-section">
		<div class="hk-section-head"><h2 class="hk-h2-app">Facts</h2></div>
		<dl class="hk-facts">
			<div>
				<dt>Project</dt>
				<dd class="hk-mono">todo-app</dd>
			</div>
			<div>
				<dt>Remote</dt>
				<dd>github.com/heracraft/todo-app</dd>
			</div>
			<div>
				<dt>Created</dt>
				<dd>2026-10-20 23:06</dd>
			</div>
		</dl>
		<dl class="hk-facts hk-facts--row facts-gap">
			<div>
				<dt>Size</dt>
				<dd>large</dd>
			</div>
			<div>
				<dt>Memory</dt>
				<dd>16 GB</dd>
			</div>
			<div>
				<dt>Uptime</dt>
				<dd>3h 12m</dd>
			</div>
		</dl>
	</section>

	<section class="hk-section">
		<div class="hk-section-head"><h2 class="hk-h2-app">Stats</h2></div>
		<div class="hk-stats" style="--cols: 3">
			<div class="hk-stat">
				<div class="hk-stat-label">Memory running</div>
				<div class="hk-stat-value">24<span class="hk-stat-unit">of 32 GB</span></div>
			</div>
			<div class="hk-stat">
				<div class="hk-stat-label">Agents needing input</div>
				<div class="hk-stat-value">2</div>
			</div>
			<div class="hk-stat">
				<div class="hk-stat-label">Disk held</div>
				<div class="hk-stat-value">112<span class="hk-stat-unit">GB</span></div>
			</div>
		</div>
	</section>

	<section class="hk-section">
		<div class="hk-section-head"><h2 class="hk-h2-app">Meters</h2></div>
		<div class="meters">
			<Meter label="Running now" used={24} limit={32} />
			<Meter label="Disk held" used={88} limit={100} />
			<Meter label="Egress this period" used={112.4} limit={100} />
			<Meter label="Memory" used={0} limit={8} />
		</div>
	</section>

	<section class="hk-section">
		<div class="hk-section-head"><h2 class="hk-h2-app">Code</h2></div>
		<div class="stack">
			<CodeBlock title="Terminal" code={SESSION} />
			<CommandList
				label="Connect"
				commands={[
					{
						command: 'repose run',
						what: 'repose run, from the checkout',
						note: 'from the checkout'
					},
					{ command: 'ssh todo-app.repose', what: 'the ssh command' },
					{ command: 'repose code todo-app', what: 'the repose code command' }
				]}
			/>
			<CodeCopy command={INSTALL_COMMAND} what="the install command" prompt />
			<div class="g-row">
				<CopyButton text="repose run" label="Copy" ariaLabel="Copy repose run" />
				<CopyButton text="repose run" ariaLabel="Copy repose run" size="sm" />
				<CopyButton text={INSTALL_COMMAND} label="Copy install command" size="lg" />
			</div>
		</div>
	</section>

	<section class="hk-section">
		<div class="hk-section-head"><h2 class="hk-h2-app">Empty row</h2></div>
		<div class="hk-empty-row">
			<h2 class="hk-empty-title">No secrets yet</h2>
			<button type="button" class="hk-btn hk-btn-sm">Add a secret</button>
		</div>
	</section>

	<section class="hk-section">
		<div class="hk-section-head"><h2 class="hk-h2-app">Panel</h2></div>
		<div class="hk-panel panel-sample">
			<p class="hk-eyebrow">Plus</p>
			<p class="price">$89 <span class="hk-stat-unit">a month</span></p>
			<p class="hk-muted">16 GB running at once</p>
		</div>
	</section>

	<section class="hk-section">
		<div class="hk-section-head"><h2 class="hk-h2-app">Inline confirm</h2></div>
		{#if confirming}
			<div class="hk-confirm">
				<p><code>todo-app</code> has claude (working). Stopping ends it.</p>
				<div class="hk-confirm-actions">
					<button type="button" class="hk-btn-danger hk-btn-sm" onclick={() => (confirming = false)}
						>Stop anyway</button
					>
					<button type="button" class="hk-btn-ghost hk-btn-sm" onclick={() => (confirming = false)}
						>Keep it running</button
					>
				</div>
			</div>
		{:else}
			<button type="button" class="hk-btn-secondary hk-btn-sm" onclick={() => (confirming = true)}
				>Stop</button
			>
		{/if}
		<div class="confirm-type">
			<ConfirmType word="todo-app" label="Destroy todo-app" onconfirm={() => {}} />
		</div>
	</section>

	<section class="hk-section">
		<div class="hk-section-head"><h2 class="hk-h2-app">Loading</h2></div>
		<LoadState status="loading" shape="rows" onretry={() => {}} />
		<div class="facts-gap">
			<LoadState status="failed" error="Cannot reach the API." onretry={() => {}} />
		</div>
	</section>

	<section class="hk-section">
		<div class="hk-section-head"><h2 class="hk-h2-app">Toasts</h2></div>
		<div class="g-row">
			<button
				type="button"
				class="hk-btn-secondary hk-btn-sm"
				onclick={() => toast.success('Timezone set to Europe/Berlin.')}>Success</button
			>
			<button
				type="button"
				class="hk-btn-secondary hk-btn-sm"
				onclick={() => toast.error('Could not stop todo-app: the host is not answering.')}
				>Error</button
			>
		</div>
	</section>

	<section class="hk-section">
		<div class="hk-section-head"><h2 class="hk-h2-app">Text</h2></div>
		<p class="hk-eyebrow">For solo founders and their agents</p>
		<h3 class="hk-h2">Your dev environment, replicated in the cloud</h3>
		<p class="hk-lead">
			<strong>Your code, tools and logins sit on a machine of its own,</strong> where agents run with
			full permissions and nobody approves commands one by one.
		</p>
		<p class="body">
			Run <code>repose run</code> in your checkout. The <a class="hk-link" href="#docs">docs</a> have
			every command.
		</p>
		<a class="hk-more" href="#docs">All agents</a>
		<p class="hk-label">Updated 2026-10-20 23:06</p>
	</section>

	<section class="hk-section">
		<div class="hk-section-head"><h2 class="hk-h2-app">Bottom bar</h2></div>
		<div class="hk-bottom-bar bar-sample">
			<span class="hk-muted">Unsaved changes</span>
			<button type="button" class="hk-btn">Apply</button>
		</div>
	</section>
</PageShell>

<style>
	.g-row {
		display: flex;
		flex-wrap: wrap;
		align-items: center;
		gap: 0.75rem 1rem;
		margin-bottom: 1rem;
	}
	.stack {
		display: grid;
		gap: 1.25rem;
		justify-items: start;
	}
	.stack > :global(.hk-code) {
		width: 100%;
	}
	.meters {
		display: grid;
		grid-template-columns: repeat(auto-fit, minmax(14rem, 1fr));
		gap: 1.5rem 2rem;
	}
	.facts-gap {
		margin-top: 1.5rem;
	}
	.tabs-sample {
		border-bottom: 1px solid var(--hk-hairline);
	}
	.panel-sample {
		max-width: 20rem;
		padding: 1.25rem;
	}
	.price {
		margin: 0.5rem 0 0.25rem;
		font-size: 1.5rem;
		font-weight: 500;
	}
	.confirm-type {
		margin-top: 1.5rem;
		max-width: 40rem;
	}
	.outage-sample {
		padding: 0.5rem;
		background: var(--hk-err-wash);
		color: var(--hk-err);
		font-size: 0.8125rem;
		font-weight: 500;
		text-align: center;
	}
	.body {
		max-width: 40rem;
		color: var(--hk-text-2);
	}
	.bar-sample {
		position: static;
		margin-top: 0;
	}
</style>
