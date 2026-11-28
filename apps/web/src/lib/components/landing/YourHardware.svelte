<!--
  03 Your hardware: tight 2-column band — zero paragraphs, zero repeated rows.
  Left: three one-line facts (each said once).
  Right: 3-bar visual showing both your Mac mini and repose Cloud finishing
  well ahead of a typical throttled cloud sandbox.
-->
<div class="yh" data-testid="your-hardware">
	<ul class="yh-list">
		<li>
			<strong>Your own hardware is already fast enough.</strong>
			<span>Linux server or Mac mini — free for up to 2 hosts, no per-minute meter.</span>
		</li>
		<li>
			<strong>Reach it from anywhere.</strong>
			<span>No port forwarding, no static IP, no VPN to configure.</span>
		</li>
		<li>
			<strong>Real physical cores, either way.</strong>
			<span>On your own box, or on repose Cloud when you don't want to manage hardware.</span>
		</li>
	</ul>

	<div class="yh-bars" aria-hidden="true">
		<p class="yh-cap">Build speed</p>
		<div class="yh-bar-row">
			<div class="yh-bar-meta">
				<span class="yh-who yh-who--own">Your Mac mini</span>
				<span class="yh-cue yh-cue--own">Fastest</span>
			</div>
			<span class="yh-track"><span class="yh-fill yh-fill--own"></span></span>
		</div>
		<div class="yh-bar-row">
			<div class="yh-bar-meta">
				<span class="yh-who yh-who--own">repose Cloud</span>
				<span class="yh-cue yh-cue--cloud">Fast</span>
			</div>
			<span class="yh-track"><span class="yh-fill yh-fill--cloud"></span></span>
		</div>
		<div class="yh-bar-row">
			<div class="yh-bar-meta">
				<span class="yh-who">Average cloud sandbox</span>
				<span class="yh-cue">Throttled</span>
			</div>
			<span class="yh-track"><span class="yh-fill"></span></span>
		</div>
	</div>
</div>

<style>
	.yh {
		display: grid;
		grid-template-columns: minmax(0, 1fr);
		margin: 0 calc(-1 * var(--edge));
		border-top: 1px solid var(--rule-in);
	}
	@media (min-width: 960px) {
		.yh {
			grid-template-columns: minmax(0, 1fr) minmax(0, 1fr);
		}
	}
	.yh-list {
		margin: 0;
		padding: 0;
		list-style: none;
	}
	.yh-list li {
		display: flex;
		flex-direction: column;
		gap: 0.25rem;
		padding: 1.25rem var(--edge);
		border-bottom: 1px solid var(--hk-hairline);
		font-size: 0.875rem;
		line-height: 1.5;
		color: var(--hk-text-3);
	}
	.yh-list li:last-child {
		border-bottom: 0;
	}
	.yh-list strong {
		font-weight: 500;
		color: var(--hk-text);
	}
	.yh-bars {
		display: flex;
		flex-direction: column;
		justify-content: center;
		gap: 1.125rem;
		padding: 1.5rem var(--edge) 2rem;
		border-top: 1px solid var(--rule-in);
	}
	@media (min-width: 960px) {
		.yh-list li {
			padding: 1.375rem var(--in) 1.375rem var(--edge);
		}
		.yh-bars {
			padding: 1.5rem var(--edge) 1.5rem var(--in);
			border-top: 0;
			border-left: 1px solid var(--rule-in);
		}
	}
	.yh-cap {
		margin: 0;
		font-family: var(--hk-font-mono);
		font-size: 0.6875rem;
		letter-spacing: 0.05em;
		text-transform: uppercase;
		color: var(--hk-text-3);
	}
	.yh-bar-row {
		display: flex;
		flex-direction: column;
		gap: 0.4rem;
	}
	.yh-bar-meta {
		display: flex;
		align-items: baseline;
		justify-content: space-between;
		gap: 0.5rem;
	}
	.yh-who {
		font-size: 0.8125rem;
		color: var(--hk-text-3);
	}
	.yh-who--own {
		color: var(--hk-text);
		font-weight: 500;
	}
	.yh-cue {
		font-family: var(--hk-font-mono);
		font-size: 0.6875rem;
		color: var(--hk-text-3);
	}
	.yh-cue--own {
		color: var(--pic-accent);
	}
	.yh-cue--cloud {
		color: var(--pic-accent);
		opacity: 0.78;
	}
	.yh-track {
		display: block;
		height: 0.5rem;
		border-radius: 1px;
		background: var(--hk-surface-2);
		overflow: hidden;
	}
	.yh-fill {
		display: block;
		width: 34%;
		height: 100%;
		border-radius: 1px;
		background: var(--hk-border-strong);
	}
	.yh-fill--own {
		width: 92%;
		background: var(--pic-accent);
	}
	.yh-fill--cloud {
		width: 78%;
		background: var(--pic-accent);
		opacity: 0.72;
	}
</style>
