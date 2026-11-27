<!--
  Start agent (DECISIONS I-1702): an agent on the running machine, in a
  window of its own in its tmux session and its checkout, as `repose run`
  starts one from a laptop. The project's default agent is chosen; Remote
  Control, Claude Code's own, turns on the machine's switch first so the
  session shows in the Claude app. A window of that agent already open is
  left alone and the line says so. Under the agents, or alone under an
  Agents heading when none runs.
-->
<script lang="ts">
	import { getOp, startAgent } from '$lib/api/client';
	import { ApiError, errorText } from '$lib/api/errors';
	import { pollUntilDone } from '$lib/poll';
	import { AGENTS, agentName } from '$lib/agents';

	let {
		projectId,
		defaultAgent,
		heading,
		onstarted
	}: {
		projectId: string;
		defaultAgent: string;
		/** No agent runs, so this is the Agents section itself. */
		heading: boolean;
		onstarted: () => void;
	} = $props();

	// svelte-ignore state_referenced_locally
	let agent = $state(AGENTS.some((a) => a.id === defaultAgent) ? defaultAgent : 'claude');
	let remoteControl = $state(false);
	let busy = $state(false);
	let line = $state('');
	let failed = $state(false);

	async function onsubmit(e: SubmitEvent) {
		e.preventDefault();
		if (busy) return;
		busy = true;
		line = '';
		failed = false;
		try {
			const { op_id } = await startAgent(projectId, {
				agent,
				...(agent === 'claude' && remoteControl ? { remote_control: true } : undefined)
			});
			await new Promise<void>((settle) =>
				pollUntilDone(async () => {
					const op = await getOp(projectId, op_id);
					if (op.state === 'done') {
						if (op.result?.existed) line = `${agentName(agent)} is already running here.`;
						settle();
						return true;
					}
					if (op.state === 'error') {
						line = op.error ?? `${agentName(agent)} did not start.`;
						failed = true;
						settle();
						return true;
					}
					return false;
				})
			);
			onstarted();
		} catch (err) {
			failed = true;
			line = errorText(err, `${agentName(agent)} did not start.`);
			if (!(err instanceof ApiError)) line = `${agentName(agent)} did not start. Try again.`;
		} finally {
			busy = false;
		}
	}
</script>

<section
	class={heading ? 'hk-section' : 'sa-under'}
	aria-labelledby={heading ? 'start-agent-title' : undefined}
	aria-label={heading ? undefined : 'Start agent'}
	data-testid="start-agent"
>
	{#if heading}
		<div class="hk-section-head">
			<h2 id="start-agent-title" class="hk-h2-app">Agents</h2>
		</div>
		<p class="sa-none">No agent is running.</p>
	{/if}
	<form class="sa-form" {onsubmit}>
		<label class="hk-sr" for="sa-agent">Agent</label>
		<select id="sa-agent" class="hk-select" bind:value={agent} disabled={busy}>
			{#each AGENTS as a (a.id)}
				<option value={a.id}>{a.name}</option>
			{/each}
		</select>
		{#if agent === 'claude'}
			<label class="hk-check sa-rc">
				<input type="checkbox" bind:checked={remoteControl} disabled={busy} />
				Remote Control, for the Claude app
			</label>
		{/if}
		<button type="submit" class="hk-btn-secondary" aria-disabled={busy ? 'true' : undefined}
			>{busy ? 'Starting…' : 'Start agent'}</button
		>
	</form>
	{#if line}
		<p class={failed ? 'hk-field-error' : 'sa-line'} data-testid="start-agent-line" role="status">
			{line}
		</p>
	{/if}
</section>

<style>
	.sa-under {
		margin-top: 1rem;
	}
	.sa-none {
		margin: 0 0 0.75rem;
		color: var(--hk-text-2, inherit);
	}
	.sa-form {
		display: flex;
		flex-wrap: wrap;
		align-items: center;
		gap: 0.5rem 0.75rem;
	}
	.sa-form .hk-select {
		width: auto;
		min-width: 9rem;
	}
	.sa-rc {
		font-size: 0.875rem;
	}
	.sa-line {
		margin: 0.5rem 0 0;
		font-size: 0.875rem;
	}
	.hk-field-error {
		margin-top: 0.5rem;
	}
</style>
