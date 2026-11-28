<!--
  04 No lock-in: one command (`repose attach`) from any frontend
  (Claude Desktop, Terminal, t3code, or any frontend) into a clean
  sandbox pane showing mixed agents/subscriptions and full machine control.
-->
<div class="am" data-testid="attach-mix">
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
				<span>Claude Desktop</span>
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
				<span>Terminal</span>
			</span>
			<a
				class="am-client am-client-link"
				href="https://t3.codes"
				target="_blank"
				rel="noopener noreferrer"><span>t3code ↗</span></a
			>
			<span class="am-client"><span>Any frontend</span></span>
		</div>
	</div>

	<div class="am-wire" aria-hidden="true">
		<span></span>
	</div>

	<div class="am-box" aria-hidden="true">
		<div class="am-box-h">
			<span class="am-dot"></span>
			<span class="am-mono">your-repo</span>
			<span class="am-sub-tag">root · your dotfiles</span>
		</div>
		<ul class="am-list">
			<li>
				<span class="am-agent-name">Claude Code</span>
				<span class="am-agent-role">primary driver</span>
				<span class="am-agent-src">Anthropic sub</span>
			</li>
			<li>
				<span class="am-agent-name">DeepSeek</span>
				<span class="am-agent-role">builder worker</span>
				<span class="am-agent-src">API key</span>
			</li>
			<li class="am-list-more">
				<span class="am-agent-name">Codex, opencode, or custom</span>
				<span class="am-agent-src">your choice</span>
			</li>
		</ul>
	</div>
</div>

<style>
	.am {
		display: grid;
		grid-template-columns: minmax(0, 1fr);
		gap: 1.25rem;
		align-items: center;
		padding-bottom: 2.5rem;
	}
	@media (min-width: 960px) {
		.am {
			grid-template-columns: minmax(0, 0.95fr) 3rem minmax(0, 1.35fr);
			gap: 0;
		}
	}
	.am-col {
		display: flex;
		flex-direction: column;
		gap: 0.625rem;
		min-width: 0;
	}
	.am-cmd-pill {
		display: flex;
		align-items: center;
		gap: 0.5rem;
		padding: 0.75rem 1rem;
		border: 1px solid var(--hk-border);
		border-radius: 6px;
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
		border-radius: 6px;
		background: var(--hk-surface);
		overflow: hidden;
		font-size: 0.8125rem;
		color: var(--hk-text-2);
	}
	.am-client {
		display: inline-flex;
		align-items: center;
		gap: 0.45rem;
		min-width: 0;
		padding: 0.7rem 0.85rem;
		border-right: 1px solid var(--hk-hairline);
		border-bottom: 1px solid var(--hk-hairline);
		text-decoration: none;
		color: inherit;
	}
	.am-client span {
		overflow: hidden;
		text-overflow: ellipsis;
		white-space: nowrap;
	}
	.am-client svg {
		flex: none;
		color: var(--hk-text-3);
	}
	.am-client:nth-child(2n) {
		border-right: 0;
	}
	.am-client:nth-last-child(-n + 2) {
		border-bottom: 0;
	}
	.am-client-link:hover {
		color: var(--hk-text);
		background: var(--hk-surface-2);
	}

	.am-wire {
		display: none;
	}
	@media (min-width: 960px) {
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

	/* Clean ruled machine pane for `your-repo` */
	.am-box {
		border: 1px solid var(--hk-border);
		border-radius: 6px;
		background: var(--hk-surface);
		overflow: hidden;
		min-width: 0;
	}
	.am-box-h {
		display: flex;
		align-items: center;
		gap: 0.5rem;
		padding: 0.65rem 1rem;
		border-bottom: 1px solid var(--hk-hairline);
		background: var(--hk-bg);
		font-size: 0.75rem;
	}
	.am-dot {
		width: 7px;
		height: 7px;
		border-radius: 50%;
		background: var(--pic-accent);
		flex: none;
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
	.am-list {
		margin: 0;
		padding: 0;
		list-style: none;
	}
	.am-list li {
		display: grid;
		grid-template-columns: minmax(0, 1.1fr) minmax(0, 1fr) auto;
		align-items: center;
		gap: 0.75rem;
		padding: 0.8rem 1rem;
		border-bottom: 1px solid var(--hk-hairline);
		font-size: 0.8125rem;
	}
	.am-list li:last-child {
		border-bottom: 0;
	}
	.am-list li.am-list-more {
		grid-template-columns: minmax(0, 1fr) auto;
		color: var(--hk-text-3);
	}
	.am-agent-name {
		font-weight: 500;
		color: var(--hk-text);
		white-space: nowrap;
		overflow: hidden;
		text-overflow: ellipsis;
	}
	.am-list-more .am-agent-name {
		font-weight: 400;
		color: var(--hk-text-2);
	}
	.am-agent-role {
		font-size: 0.75rem;
		color: var(--hk-text-3);
		white-space: nowrap;
		overflow: hidden;
		text-overflow: ellipsis;
	}
	.am-agent-src {
		font-family: var(--hk-font-mono);
		font-size: 0.6875rem;
		color: var(--hk-text-3);
		white-space: nowrap;
	}
	@media (max-width: 639.98px) {
		.am-list li {
			grid-template-columns: minmax(0, 1fr) auto;
		}
		.am-agent-role {
			display: none;
		}
	}
</style>
