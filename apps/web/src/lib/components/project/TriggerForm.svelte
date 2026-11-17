<!--
  Make a trigger, or change one (DECISIONS I-1266, amending I-1258).
  Everything `repose triggers add` takes, with its defaults and its
  refusals: the door, the prompt, the agent, the working directory, the
  timeout, the concurrency, the gate, the dedupe key and the four
  switches.

  The prompt is the field a browser could not fill before. It is a file
  in the machine and the api holds a path, so the text goes to the api
  only to be written into the machine, and the machine is the only place
  it is kept, which is why this form can write a prompt and cannot show
  the one that is there. The other half of the field is a path to a file
  the machine already has, which is `--prompt-file`.

  The settings past the door and the prompt open on a button, because
  they have defaults that are right and a form of eighteen fields is a
  form nobody reads.
-->
<script lang="ts">
	import { untrack } from 'svelte';
	import { putTrigger, listWebhooks } from '$lib/api/client';
	import { toastApiError } from '$lib/api/toast';
	import { ApiError } from '$lib/api/errors';
	import { focusAfterRender } from '$lib/focus';
	import {
		buildSpec,
		parseSpec,
		EMPTY_FIELDS,
		SHAPE_LABELS,
		WEEKDAYS,
		type SpecFields,
		type SpecShape
	} from '$lib/triggerspec';
	import type { Project, Trigger } from '$lib/api/types';

	let {
		project,
		trigger,
		onsaved,
		oncancel
	}: {
		project: Project;
		/** The trigger being changed; absent makes a new one. */
		trigger?: Trigger;
		onsaved: (t: Trigger, created: boolean) => void;
		oncancel: () => void;
	} = $props();

	const NAME_RE = /^[a-z0-9][a-z0-9-]{0,31}$/;
	const AGENTS = ['claude', 'codex', 'opencode', 'gemini', 'pi'];
	// The form opens on what the trigger is now and is its own state
	// after that: the list behind it polls every ten seconds, and a
	// field that re-read the row would throw away what is being typed.
	// The parent gives this component a fresh instance per open.
	const was = untrack(() => trigger);
	const editing = was !== undefined;

	let name = $state(was?.name ?? '');
	let kind = $state<'route' | 'schedule'>(was?.kind ?? 'route');
	let route = $state(was?.route?.name ?? '');
	let spec = $state<SpecFields>(
		(was?.spec ? parseSpec(was.spec) : undefined) ?? { ...EMPTY_FIELDS }
	);
	// The zone the schedule's times are read in: the trigger's own, else
	// this browser's, which is what the CLI sends from the laptop
	// (I-1252's order).
	let zone = $state(was?.zone || Intl.DateTimeFormat().resolvedOptions().timeZone || 'UTC');
	let promptKind = $state<'text' | 'file'>(editing ? 'file' : 'text');
	let promptText = $state('');
	let promptPath = $state(was?.prompt_path ?? '');
	let agent = $state(was?.agent ?? '');
	let workdir = $state(was?.workdir ?? 'checkout');
	let timeoutMin = $state(Math.round((was?.timeout_s ?? 3600) / 60));
	let concurrency = $state(was?.concurrency ?? 1);
	let gate = $state(was?.gate ?? '');
	let dedupe = $state(was?.dedupe ?? '');
	let allowStart = $state(was?.allow_start ?? false);
	let stopAfter = $state(was?.stop_after ?? false);
	let catchUp = $state(was?.catch_up ?? false);
	let noNotify = $state(was?.notify === 'never');

	let more = $state(false);
	let saving = $state(false);
	let error = $state<string | undefined>(undefined);
	/** The project's route names, so an existing route is one keystroke. */
	let routeNames = $state<string[]>([]);

	$effect(() => {
		// Best effort: the field takes a name typed in full either way,
		// so a listing that fails costs the suggestions and nothing else.
		void listWebhooks(project.id)
			.then((rs) => (routeNames = rs.map((r) => r.name)))
			.catch(() => undefined);
	});

	/** The machine has to be running for a prompt to be written into it. */
	const canWritePrompt = $derived(project.state === 'running');

	const specText = $derived(buildSpec(spec));
	const specLine = $derived('spec' in specText ? `${specText.spec} ${zone}` : '');

	function body(): Record<string, unknown> | { error: string } {
		const n = name.trim();
		if (!NAME_RE.test(n)) {
			return { error: 'A name is 1 to 32 of a-z, 0-9 and -, starting with a letter or digit.' };
		}
		const out: Record<string, unknown> = { name: n };
		if (kind === 'route') {
			const r = route.trim();
			if (!NAME_RE.test(r)) {
				return {
					error: 'A route name is 1 to 32 of a-z, 0-9 and -, starting with a letter or digit.'
				};
			}
			out.kind = 'route';
			out.route = r;
		} else {
			if ('error' in specText) return { error: specText.error };
			out.kind = 'schedule';
			out.spec = specText.spec;
			out.zone = zone.trim();
		}
		if (promptKind === 'text') {
			const text = promptText.trim();
			if (!editing && text === '')
				return { error: 'Write the prompt, or name a file in the machine.' };
			if (text !== '') {
				if (!canWritePrompt) {
					return {
						error: `${project.slug} is ${project.state}, and a prompt is written into the machine. Start it, or name a file that is already there.`
					};
				}
				out.prompt = text;
			}
		} else {
			const p = promptPath.trim();
			if (p === '') return { error: 'Name the prompt file, relative to the working directory.' };
			out.prompt_path = p;
		}
		out.workdir = workdir.trim() || 'checkout';
		out.agent = agent;
		out.timeout_s = Math.round(timeoutMin) * 60;
		out.concurrency = concurrency;
		out.gate = gate.trim();
		out.dedupe = kind === 'route' ? dedupe.trim() : '';
		out.notify = noNotify ? 'never' : 'failed';
		out.allow_start = allowStart;
		out.stop_after = stopAfter;
		out.catch_up = catchUp;
		return out;
	}

	async function onsubmit(event: SubmitEvent) {
		event.preventDefault();
		const b = body();
		if ('error' in b && typeof b.error === 'string') {
			error = b.error;
			return;
		}
		saving = true;
		try {
			const t = await putTrigger(project.id, b);
			onsaved(t, !editing);
		} catch (err) {
			// The api's own words for a refusal it owns: a temporary
			// machine, a herdr machine, a 21st trigger, a bad spec.
			if (err instanceof ApiError && (err.code === 'invalid' || err.code === 'conflict')) {
				error = err.message;
			} else {
				toastApiError(err, editing ? `Could not change ${name}.` : 'Could not add the trigger.');
				error = 'Could not save it. Try again.';
			}
		} finally {
			saving = false;
		}
	}

	function pickDay(day: string, on: boolean) {
		spec = { ...spec, days: on ? [...spec.days, day] : spec.days.filter((d) => d !== day) };
	}
</script>

<form class="tform" {onsubmit} data-testid="trigger-form">
	<div class="hk-fields tfields">
		<div class="tfield">
			<label for="trigger-name" class="hk-field-label">Name</label>
			<!-- svelte-ignore a11y_autofocus -->
			<input
				id="trigger-name"
				class="hk-input"
				autofocus={!editing}
				autocomplete="off"
				spellcheck="false"
				placeholder="triage"
				disabled={editing}
				bind:value={name}
			/>
		</div>
		<div class="tfield">
			<span class="hk-field-label" id="trigger-kind-label">Fires on</span>
			<div class="hk-seg--buttons hk-seg--sm" role="group" aria-labelledby="trigger-kind-label">
				<button type="button" aria-pressed={kind === 'route'} onclick={() => (kind = 'route')}
					>A webhook</button
				>
				<button type="button" aria-pressed={kind === 'schedule'} onclick={() => (kind = 'schedule')}
					>A schedule</button
				>
			</div>
		</div>
	</div>

	{#if kind === 'route'}
		<div class="tfield">
			<label for="trigger-route" class="hk-field-label">Route</label>
			<input
				id="trigger-route"
				class="hk-input tnarrow"
				autocomplete="off"
				spellcheck="false"
				placeholder="recruiting"
				list="trigger-routes"
				bind:value={route}
			/>
			<datalist id="trigger-routes">
				{#each routeNames as r (r)}<option value={r}></option>{/each}
			</datalist>
			<p class="thelp">A route that is not there is made, and its URL is yours to paste.</p>
		</div>
	{:else}
		<div class="hk-fields tfields">
			<div class="tfield">
				<label for="trigger-shape" class="hk-field-label">When</label>
				<select
					id="trigger-shape"
					class="hk-select"
					value={spec.shape}
					onchange={(e) => (spec = { ...spec, shape: e.currentTarget.value as SpecShape })}
				>
					{#each Object.entries(SHAPE_LABELS) as [value, label] (value)}
						<option {value}>{label}</option>
					{/each}
				</select>
			</div>
			{#if spec.shape === 'every'}
				<div class="tfield">
					<label for="trigger-every" class="hk-field-label">Minutes apart</label>
					<input
						id="trigger-every"
						class="hk-input"
						type="number"
						min="15"
						max="1440"
						step="1"
						value={spec.minutes}
						oninput={(e) => (spec = { ...spec, minutes: e.currentTarget.valueAsNumber })}
					/>
				</div>
			{:else}
				{#if spec.shape === 'monthly'}
					<div class="tfield">
						<label for="trigger-day" class="hk-field-label">Day of the month</label>
						<input
							id="trigger-day"
							class="hk-input"
							type="number"
							min="1"
							max="28"
							step="1"
							value={spec.day}
							oninput={(e) => (spec = { ...spec, day: e.currentTarget.valueAsNumber })}
						/>
					</div>
				{/if}
				<div class="tfield">
					<label for="trigger-at" class="hk-field-label">At</label>
					<input
						id="trigger-at"
						class="hk-input"
						type="time"
						value={spec.at}
						oninput={(e) => (spec = { ...spec, at: e.currentTarget.value })}
					/>
				</div>
			{/if}
			<div class="tfield">
				<label for="trigger-zone" class="hk-field-label">Time zone</label>
				<input
					id="trigger-zone"
					class="hk-input"
					autocomplete="off"
					spellcheck="false"
					placeholder="Europe/Berlin"
					bind:value={zone}
				/>
			</div>
		</div>
		{#if spec.shape === 'days'}
			<fieldset class="tdays">
				<legend class="hk-field-label">Days</legend>
				{#each WEEKDAYS as d (d)}
					<label class="hk-check">
						<input
							type="checkbox"
							checked={spec.days.includes(d)}
							onchange={(e) => pickDay(d, e.currentTarget.checked)}
						/>
						<span>{d}</span>
					</label>
				{/each}
			</fieldset>
		{/if}
		{#if specLine}<p class="thelp" data-testid="trigger-spec">It fires {specLine}.</p>{/if}
	{/if}

	<div class="tfield">
		<span class="hk-field-label" id="trigger-prompt-label">Prompt</span>
		<div class="hk-seg--buttons hk-seg--sm" role="group" aria-labelledby="trigger-prompt-label">
			<button
				type="button"
				aria-pressed={promptKind === 'text'}
				onclick={() => (promptKind = 'text')}>Write it here</button
			>
			<button
				type="button"
				aria-pressed={promptKind === 'file'}
				onclick={() => (promptKind = 'file')}>A file in the machine</button
			>
		</div>
	</div>
	{#if promptKind === 'text'}
		<div class="tfield">
			<label for="trigger-prompt" class="hk-sr">The prompt the agent starts with</label>
			<textarea
				id="trigger-prompt"
				class="hk-textarea tprompt"
				rows="5"
				spellcheck="false"
				placeholder="Triage the new issues; tell me what needs me"
				bind:value={promptText}
			></textarea>
			{#if !canWritePrompt}
				<p class="thelp">
					{project.slug} is {project.state}, and a prompt is written into the machine. Start it, or
					name a file that is already there.
				</p>
			{:else if editing}
				<p class="thelp">
					It is written to <code class="hk-mono">{promptPath || `.repose/triggers/${name}.md`}</code
					>, over the prompt that is there. Leave it empty to keep that one.
				</p>
			{:else}
				<p class="thelp">
					It is written into the machine as <code class="hk-mono"
						>.repose/triggers/{name || 'NAME'}.md</code
					>.
				</p>
			{/if}
		</div>
	{:else}
		<div class="tfield">
			<label for="trigger-prompt-path" class="hk-sr">The prompt file in the machine</label>
			<input
				id="trigger-prompt-path"
				class="hk-input tnarrow"
				autocomplete="off"
				spellcheck="false"
				placeholder=".repose/triggers/{name || 'NAME'}.md"
				bind:value={promptPath}
			/>
			<p class="thelp">Relative to the working directory. It is not read until the first fire.</p>
		</div>
	{/if}

	<button
		type="button"
		class="hk-btn-ghost hk-btn-sm tmore"
		aria-expanded={more}
		onclick={() => (more = !more)}>More settings</button
	>
	{#if more}
		<div class="hk-fields tfields" data-testid="trigger-more">
			<div class="tfield">
				<label for="trigger-agent" class="hk-field-label">Agent</label>
				<select id="trigger-agent" class="hk-select" bind:value={agent}>
					<option value="">{project.agent_default} (the project's)</option>
					{#each AGENTS as a (a)}<option value={a}>{a}</option>{/each}
				</select>
			</div>
			<div class="tfield">
				<label for="trigger-workdir" class="hk-field-label">Working directory</label>
				<input
					id="trigger-workdir"
					class="hk-input"
					autocomplete="off"
					spellcheck="false"
					placeholder="checkout"
					bind:value={workdir}
				/>
			</div>
			<div class="tfield">
				<label for="trigger-timeout" class="hk-field-label">Timeout, minutes</label>
				<input
					id="trigger-timeout"
					class="hk-input"
					type="number"
					min="0"
					max="1440"
					step="1"
					bind:value={timeoutMin}
				/>
			</div>
			<div class="tfield">
				<label for="trigger-concurrency" class="hk-field-label">Runs at once</label>
				<input
					id="trigger-concurrency"
					class="hk-input"
					type="number"
					min="1"
					max="3"
					step="1"
					bind:value={concurrency}
				/>
			</div>
		</div>
		<div class="tfield">
			<label for="trigger-gate" class="hk-field-label">Gate</label>
			<input
				id="trigger-gate"
				class="hk-input"
				autocomplete="off"
				spellcheck="false"
				placeholder="./verify-github.sh"
				bind:value={gate}
			/>
			<p class="thelp">
				A command, with the event on its stdin. A non-zero exit skips the run; this is where a
				provider's signature is checked.
			</p>
		</div>
		{#if kind === 'route'}
			<div class="tfield">
				<label for="trigger-dedupe" class="hk-field-label">Dedupe key</label>
				<input
					id="trigger-dedupe"
					class="hk-input tnarrow"
					autocomplete="off"
					spellcheck="false"
					placeholder="header:X-GitHub-Delivery"
					bind:value={dedupe}
				/>
				<p class="thelp">
					<code class="hk-mono">header:NAME</code> or <code class="hk-mono">json:FIELD</code>: an
					event whose key was seen in the last 24 hours is skipped.
				</p>
			</div>
		{/if}
		<div class="tswitches">
			<label class="hk-check">
				<input type="checkbox" bind:checked={allowStart} />
				<span>Let a fire start the machine while it is stopped</span>
			</label>
			<label class="hk-check">
				<input type="checkbox" bind:checked={stopAfter} />
				<span>Stop it again when the run ends, if this trigger started it</span>
			</label>
			<label class="hk-check">
				<input type="checkbox" bind:checked={catchUp} />
				<span>Run one missed fire when the machine comes back</span>
			</label>
			<label class="hk-check">
				<input type="checkbox" bind:checked={noNotify} />
				<span>Do not tell me when a run fails</span>
			</label>
		</div>
	{/if}

	{#if error}<p class="hk-field-error" data-testid="trigger-form-error">{error}</p>{/if}
	<div class="hk-confirm-actions">
		<button type="submit" class="hk-btn hk-btn-sm" disabled={saving}
			>{saving ? 'Saving…' : editing ? 'Save' : 'Add trigger'}</button
		>
		<button
			type="button"
			class="hk-btn-ghost hk-btn-sm"
			onclick={() => {
				oncancel();
				void focusAfterRender('trigger-add-open');
			}}>Cancel</button
		>
	</div>
</form>

<style>
	.tform {
		margin-top: 1rem;
		display: flex;
		flex-direction: column;
		gap: 0.75rem;
	}
	/* The fields that sit side by side from 640, as the webhook add
	   form's two do; below it each takes its own line. */
	.tfields {
		display: grid;
		gap: 0.75rem;
		grid-template-columns: 1fr;
	}
	@media (min-width: 640px) {
		.tfields {
			grid-template-columns: repeat(auto-fit, minmax(10rem, 1fr));
		}
	}
	.tfield {
		display: flex;
		flex-direction: column;
		gap: 0.25rem;
		min-width: 0;
	}
	/* A pair of words each: the track is as wide as its two items, not
	   as wide as the frame, which is how the Usage range reads. */
	.tfield :global(.hk-seg--buttons) {
		align-self: flex-start;
		max-width: 100%;
	}
	/* A name, a path and a key are short: the field says so rather than
	   running the width of the frame. */
	.tnarrow {
		max-width: 24rem;
	}
	.tprompt {
		min-height: 7rem;
		font-family: var(--hk-font-mono);
	}
	.thelp {
		margin: 0;
		font-size: 0.8125rem;
		color: var(--hk-muted);
	}
	.tdays {
		display: flex;
		flex-wrap: wrap;
		align-items: center;
		gap: 0.25rem 0.875rem;
		margin: 0;
		padding: 0;
		border: 0;
	}
	.tdays legend {
		float: left;
		width: 100%;
		padding: 0;
	}
	.tswitches {
		display: flex;
		flex-direction: column;
		gap: 0.375rem;
	}
	.tmore {
		align-self: flex-start;
		margin-left: -0.625rem;
	}
</style>
