<!--
  03 Your hardware (DECISIONS I-1674): what ships today for a box you own,
  an x86 box or a Mac. Every line is the public docs' and true of both
  (own-hardware.md, I-1626 to I-1660; ssh-and-editors.md): `repose host
  add` run on the box, `repose run` placing new sandboxes there, sandboxes
  that share the box's memory with no size, and SSH through repose's
  gateway with no inbound port. Not said, because it is not true of both
  or not without a condition: "one command" (an x86 box also runs the
  installer `repose host add` prints), a start "while 1 GB is free" (a
  Mac also limits sandboxes by cores), memory "handed back" (an idle one
  returns part of it, its file cache, after five minutes). The figures are diagrams of those rules, not
  measurements: they carry no number the docs do not. Static.
-->
<script lang="ts">
	/* The memory bar: three sandboxes and the free rest. A diagram of
	   sharing (no sizes), so the widths are drawn, not measured; the names
	   are short so each fits under its part. */
	const SHARE = [
		{ name: 'web-app', w: 38 },
		{ name: 'api', w: 22 },
		{ name: 'docs', w: 12 }
	];
</script>

<ul class="yh" data-testid="your-hardware">
	<li>
		<div class="yh-copy">
			<p class="label num"><span class="n">3.1 </span>Setup</p>
			<h3 class="item-title">Add a box you already have</h3>
			<p class="copy">
				Run <code>repose host add</code> on an x86 box or a Mac you own. Once it is added,
				<code>repose run</code> puts new sandboxes on it.
			</p>
		</div>
		<div class="yh-fig" aria-hidden="true">
			<p class="yh-cmd"><span class="yh-c">repose host add</span><span>on the box</span></p>
			<p class="yh-cmd"><span class="yh-c">repose run</span><span>in any repo</span></p>
		</div>
	</li>
	<li>
		<div class="yh-copy">
			<p class="label num"><span class="n">3.2 </span>Memory</p>
			<h3 class="item-title">Sandboxes share its memory</h3>
			<p class="copy">
				A sandbox there has no size. They share the box's memory, and <code>repose ls</code> shows what
				each one uses.
			</p>
		</div>
		<div class="yh-fig" aria-hidden="true">
			<div class="yh-mem">
				{#each SHARE as s (s.name)}<span class="yh-seg" style:width="{s.w}%"></span>{/each}
				<span class="yh-free"></span>
			</div>
			<div class="yh-names">
				{#each SHARE as s (s.name)}<span style:width="{s.w}%">{s.name}</span>{/each}
				<span class="yh-free-name">free</span>
			</div>
		</div>
	</li>
	<li>
		<div class="yh-copy">
			<p class="label num"><span class="n">3.3 </span>Reach</p>
			<h3 class="item-title">Reach it from anywhere</h3>
			<p class="copy">
				The box dials out to repose, so nothing opens on your router. After <code>repose login</code
				>
				on any laptop, <code>ssh your-project.repose</code> reaches it.
			</p>
		</div>
		<div class="yh-fig" aria-hidden="true">
			<div class="yh-path">
				<span class="yh-node">your laptop</span>
				<span class="yh-wire"><span class="yh-wl">ssh</span></span>
				<span class="yh-node yh-hub">repose</span>
				<span class="yh-wire yh-in"><span class="yh-wl">dials out</span></span>
				<span class="yh-node">your box</span>
			</div>
		</div>
	</li>
</ul>

<style>
	.yh {
		display: grid;
		grid-template-columns: minmax(0, 1fr);
		margin: 0 calc(-1 * var(--edge));
		padding: 0;
		list-style: none;
		border-top: 1px solid var(--rule-in);
	}
	.yh > li {
		display: flex;
		flex-direction: column;
		min-width: 0;
		padding: 1.375rem var(--edge) 1.75rem;
	}
	.yh > li + li {
		border-top: 1px solid var(--rule-in);
	}
	.yh-copy > :global(.num) {
		margin-bottom: 0.375rem;
	}
	.yh-copy :global(.item-title) {
		text-wrap: balance;
	}
	.yh-copy :global(.copy) {
		margin-top: 0.25rem;
	}
	.yh-fig {
		padding-top: 1.25rem;
	}
	@media (min-width: 1024px) {
		.yh {
			grid-template-columns: repeat(3, minmax(0, 1fr));
		}
		.yh > li {
			display: grid;
			grid-row: span 2;
			grid-template-rows: subgrid;
			padding: 1.5rem var(--in) 1.75rem;
		}
		.yh > li:first-child {
			padding-left: var(--edge);
		}
		.yh > li:last-child {
			padding-right: var(--edge);
		}
		.yh > li + li {
			border-top: 0;
			border-left: 1px solid var(--rule-in);
		}
	}

	/* 3.1: the two commands in the page's reference treatment. */
	.yh-cmd {
		display: flex;
		justify-content: space-between;
		gap: 1rem;
		margin: 0;
		padding: 0.5rem 0;
		border-bottom: 1px solid var(--hk-hairline);
		font-size: 0.8125rem;
		line-height: 1.25rem;
		color: var(--hk-text-3);
	}
	.yh-cmd:first-child {
		border-top: 1px solid var(--hk-hairline);
	}
	.yh-c {
		font-family: var(--hk-font-mono);
		color: var(--hk-text);
	}

	/* 3.2: one bar, the box's memory, shared. */
	.yh-mem {
		display: flex;
		gap: 2px;
		height: 0.625rem;
	}
	.yh-seg {
		flex: none;
		border-radius: 1px;
		background: var(--pic-accent);
	}
	.yh-seg + .yh-seg {
		opacity: 0.7;
	}
	.yh-seg + .yh-seg + .yh-seg {
		opacity: 0.45;
	}
	.yh-free {
		flex: 1;
		border: 1px solid var(--hk-border-strong);
		border-radius: 1px;
	}
	.yh-names {
		display: flex;
		gap: 2px;
		margin-top: 0.5rem;
		font-family: var(--hk-font-mono);
		font-size: 0.6875rem;
		line-height: 1rem;
		color: var(--hk-text-3);
	}
	.yh-names > span {
		flex: none;
		overflow: hidden;
		text-overflow: clip;
		white-space: nowrap;
	}
	.yh-names > .yh-free-name {
		flex: 1;
	}

	/* 3.3: laptop to repose, the box dialling out to it. */
	.yh-path {
		display: flex;
		align-items: center;
		padding-top: 0.875rem;
	}
	.yh-node {
		flex: none;
		padding: 0.25rem 0.5rem;
		border: 1px solid var(--hk-border);
		border-radius: 6px;
		background: var(--hk-surface);
		font-size: 0.75rem;
		line-height: 1rem;
		color: var(--hk-text-2);
		white-space: nowrap;
	}
	.yh-hub {
		font-family: var(--hk-font-mono);
		color: var(--hk-text);
	}
	.yh-wire {
		position: relative;
		flex: 1;
		min-width: 1.5rem;
		height: 1px;
		background: var(--hk-border-strong);
	}
	/* The arrowhead points at repose: right on the laptop's wire, left on
	   the box's, since the box opens the connection. */
	.yh-wire::after {
		content: '';
		position: absolute;
		top: -3px;
		right: 0;
		border-block: 3.5px solid transparent;
		border-left: 5px solid var(--hk-border-strong);
	}
	.yh-in::after {
		right: auto;
		left: 0;
		border-left: 0;
		border-right: 5px solid var(--hk-border-strong);
	}
	.yh-wl {
		position: absolute;
		bottom: 0.375rem;
		left: 50%;
		transform: translateX(-50%);
		font-family: var(--hk-font-mono);
		font-size: 0.6875rem;
		line-height: 1rem;
		color: var(--hk-text-3);
		white-space: nowrap;
	}
</style>
