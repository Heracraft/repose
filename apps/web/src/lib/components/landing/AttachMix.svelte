<!--
  05 No lock-in: one command to attach from the frontends developers actually
  use (Claude Code Desktop, Terminal, temu3code, or any frontend) into a
  sandbox mixing agents and subscriptions with full root control.
  Consistent --hk-border / --hk-hairline framing (no extra glows).
-->
<div class="am-stage" data-testid="attach-mix">
	<div class="am">
		<div class="am-col">
			<div class="am-cmd-pill">
				<span class="am-prompt">$</span>
				<code>repose attach</code>
			</div>
			<div class="am-clients">
				<span class="am-client">
					<svg
						viewBox="0 0 16 16"
						width="13"
						height="13"
						fill="none"
						stroke="currentColor"
						stroke-width="1.4"
						stroke-linejoin="round"
						aria-hidden="true"
					>
						<rect x="2" y="2.75" width="12" height="10.5" rx="1.5" />
						<path d="M2 5.75h12" />
					</svg>
					Claude Code Desktop
				</span>
				<span class="am-client">
					<svg
						viewBox="0 0 16 16"
						width="13"
						height="13"
						fill="none"
						stroke="currentColor"
						stroke-width="1.4"
						stroke-linecap="round"
						stroke-linejoin="round"
						aria-hidden="true"
					>
						<path d="M3.5 5l3 3-3 3M8 11h4.5" />
					</svg>
					Terminal
				</span>
				<a
					class="am-client am-client-link"
					href="https://t3.codes"
					target="_blank"
					rel="noopener noreferrer">t3code ↗</a
				>
				<span class="am-client">Any frontend</span>
			</div>
		</div>

		<div class="am-wire" aria-hidden="true">
			<span></span>
		</div>

		<div class="am-col" aria-hidden="true">
			<div class="am-box">
				<div class="am-box-h">
					<span class="am-dot"></span>
					<span class="am-mono">your-repo</span>
					<span class="am-sub-tag">full root · your dotfiles</span>
				</div>
				<div class="am-agents">
					<div class="am-agent">
						<span class="am-role">Primary driver</span>
						<span class="am-name">Claude Code</span>
					</div>
					<span class="am-plus">+</span>
					<div class="am-agent">
						<span class="am-role">Builder worker</span>
						<span class="am-name">DeepSeek</span>
					</div>
					<span class="am-plus">+</span>
					<div class="am-agent am-agent--any">
						<span class="am-role">Any model</span>
						<span class="am-name">Codex / custom</span>
					</div>
				</div>
				<div class="am-dots">
					<code>.zshrc</code>
					<code>.gitconfig</code>
					<code>~/.claude/</code>
					<code>your scripts</code>
				</div>
			</div>
		</div>
	</div>
</div>

<style>
	.am-stage {
		padding: 1.25rem;
		border: 1px solid var(--hk-hairline);
		border-radius: 12px;
		background: var(--hk-bg);
		margin-bottom: 2.25rem;
	}
	.am {
		display: grid;
		grid-template-columns: minmax(0, 1fr);
		gap: 1rem;
		align-items: center;
	}
	@media (min-width: 900px) {
		.am {
			grid-template-columns: minmax(0, 0.92fr) 3rem minmax(0, 1.38fr);
			gap: 0;
		}
	}
	.am-col {
		display: flex;
		flex-direction: column;
		gap: 0.625rem;
	}
	.am-cmd-pill {
		display: flex;
		align-items: center;
		gap: 0.5rem;
		padding: 0.75rem 1rem;
		border: 1px solid var(--hk-border);
		border-radius: 9px;
		background: var(--hk-surface);
		font-family: var(--hk-font-mono);
		font-size: 0.875rem;
		color: var(--hk-text);
	}
	.am-prompt {
		color: var(--pic-accent);
	}
	.am-clients {
		display: grid;
		grid-template-columns: repeat(2, minmax(0, 1fr));
		border: 1px solid var(--hk-border);
		border-radius: 9px;
		background: var(--hk-surface);
		overflow: hidden;
		font-size: 0.8125rem;
		color: var(--hk-text-2);
	}
	.am-clients > :is(span, a) {
		display: inline-flex;
		align-items: center;
		gap: 0.45rem;
		padding: 0.65rem 0.875rem;
		border-right: 1px solid var(--hk-hairline);
		border-bottom: 1px solid var(--hk-hairline);
		text-decoration: none;
		color: inherit;
	}
	.am-client svg {
		flex: none;
		color: var(--hk-text-3);
	}
	.am-clients > :is(span, a):nth-child(2n) {
		border-right: 0;
	}
	.am-clients > :is(span, a):nth-last-child(-n + 2) {
		border-bottom: 0;
	}
	.am-client-link:hover {
		color: var(--hk-text);
		background: var(--hk-surface-2);
	}
	.am-wire {
		display: none;
	}
	@media (min-width: 900px) {
		.am-wire {
			display: flex;
			align-items: center;
		}
		.am-wire span {
			position: relative;
			width: 100%;
			height: 1px;
			background: var(--hk-border-strong);
		}
		.am-wire span::after {
			content: '';
			position: absolute;
			top: -3.5px;
			right: 0;
			border-block: 4px solid transparent;
			border-left: 6px solid var(--hk-border-strong);
		}
	}
	.am-box {
		border: 1px solid var(--hk-border);
		border-radius: 10px;
		background: var(--hk-surface);
		padding: 1rem 1.125rem;
	}
	.am-box-h {
		display: flex;
		align-items: center;
		gap: 0.5rem;
		padding-bottom: 0.75rem;
		margin-bottom: 0.875rem;
		border-bottom: 1px solid var(--hk-hairline);
		font-size: 0.75rem;
	}
	.am-dot {
		width: 7px;
		height: 7px;
		border-radius: 50%;
		background: var(--pic-accent);
	}
	.am-mono {
		font-family: var(--hk-font-mono);
		color: var(--hk-text);
	}
	.am-sub-tag {
		margin-left: auto;
		font-family: var(--hk-font-mono);
		font-size: 0.6875rem;
		color: var(--hk-text-3);
	}
	.am-agents {
		display: flex;
		flex-wrap: wrap;
		align-items: center;
		gap: 0.5rem;
		margin-bottom: 0.875rem;
	}
	.am-agent {
		flex: 1 1 7.5rem;
		display: flex;
		flex-direction: column;
		gap: 0.125rem;
		padding: 0.625rem 0.75rem;
		border: 1px solid var(--hk-hairline);
		border-radius: 7px;
		background: var(--hk-bg);
	}
	.am-agent--any {
		border-style: dashed;
	}
	.am-role {
		font-family: var(--hk-font-mono);
		font-size: 0.6875rem;
		color: var(--hk-text-3);
	}
	.am-name {
		font-size: 0.875rem;
		font-weight: 500;
		color: var(--hk-text);
	}
	.am-plus {
		font-family: var(--hk-font-mono);
		font-size: 0.875rem;
		color: var(--hk-text-4);
	}
	.am-dots {
		display: flex;
		flex-wrap: wrap;
		gap: 0.45rem;
		padding-top: 0.75rem;
		border-top: 1px solid var(--hk-hairline);
	}
	.am-dots code {
		padding: 0.15rem 0.45rem;
		border: 1px solid var(--hk-hairline);
		border-radius: 4px;
		background: var(--hk-bg);
		font-family: var(--hk-font-mono);
		font-size: 0.6875rem;
		color: var(--hk-text-3);
	}
</style>
