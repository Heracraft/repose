<script lang="ts">
	import { onMount, type Snippet } from 'svelte';
	import { StateEffect, StateField } from '@codemirror/state';
	import {
		EditorView,
		keymap,
		lineNumbers,
		highlightActiveLine,
		highlightActiveLineGutter,
		placeholder as placeholderText,
		Decoration,
		type DecorationSet
	} from '@codemirror/view';
	import { defaultKeymap, history, historyKeymap } from '@codemirror/commands';
	import { syntaxHighlighting, defaultHighlightStyle, bracketMatching } from '@codemirror/language';
	import { nix } from '@replit/codemirror-lang-nix';

	let {
		value = $bindable(''),
		errorLine,
		readonly = false,
		label = 'Nix fragment',
		placeholder = '{ pkgs, ... }: { home.packages = [ ]; }',
		footer
	}: {
		value: string;
		errorLine?: number;
		readonly?: boolean;
		/** The editor's accessible name: CodeMirror's content is a role=textbox with none of its own. */
		label?: string;
		/** Shown in --ink-faint while the editor is empty; never saved. */
		placeholder?: string;
		/**
		 * A bar inside the editor's frame, under the code: the caller's
		 * status on the left and its action (Save, Apply) on the right.
		 */
		footer?: Snippet;
	} = $props();

	// The editor is as tall as its text plus three empty lines, at least
	// six and at most 24; past that it scrolls inside itself. A fixed
	// 320px box was mostly empty for a one-line fragment and too short for
	// a long one.
	const MIN_ROWS = 6;
	const MAX_ROWS = 24;
	let rows = $derived(Math.min(MAX_ROWS, Math.max(MIN_ROWS, value.split('\n').length + 3)));

	let container: HTMLDivElement;
	let view: EditorView | undefined;

	// A build error names a fragment.nix line (nix-build-contract.md); this
	// paints that one line so the user does not have to count.
	const setErrorLine = StateEffect.define<number | null>();
	const errorLineField = StateField.define<DecorationSet>({
		create: () => Decoration.none,
		update(deco, tr) {
			deco = deco.map(tr.changes);
			for (const e of tr.effects) {
				if (e.is(setErrorLine)) {
					if (e.value == null || e.value < 1 || e.value > tr.state.doc.lines)
						return Decoration.none;
					const line = tr.state.doc.line(e.value);
					return Decoration.set([Decoration.line({ class: 'cm-line-error' }).range(line.from)]);
				}
			}
			return deco;
		},
		provide: (f) => EditorView.decorations.from(f)
	});

	onMount(() => {
		view = new EditorView({
			doc: value,
			parent: container,
			extensions: [
				lineNumbers(),
				highlightActiveLine(),
				highlightActiveLineGutter(),
				history(),
				bracketMatching(),
				syntaxHighlighting(defaultHighlightStyle, { fallback: true }),
				nix(),
				errorLineField,
				keymap.of([...defaultKeymap, ...historyKeymap]),
				EditorView.editable.of(!readonly),
				placeholderText(placeholder),
				EditorView.contentAttributes.of({ 'aria-label': label }),
				EditorView.updateListener.of((u) => {
					if (u.docChanged) value = u.state.doc.toString();
				})
			]
		});
		return () => view?.destroy();
	});

	// The parent replaced the whole document (loaded a revision, "Edit as
	// Nix" copied the generated fragment in) — not the user typing, which
	// already flows the other way via the update listener above.
	$effect(() => {
		if (view && view.state.doc.toString() !== value) {
			view.dispatch({ changes: { from: 0, to: view.state.doc.length, insert: value } });
		}
	});

	$effect(() => {
		view?.dispatch({ effects: setErrorLine.of(errorLine ?? null) });
	});
</script>

<!-- The frame carries the control's edge and the focus ring, so the
     footer bar sits inside the same box as the code it acts on. -->
<div class="nix-frame">
	<div
		class="nix-editor"
		style:--nix-rows={rows}
		style:--nix-max={MAX_ROWS}
		bind:this={container}
	></div>
	{#if footer}
		<div
			class="flex min-h-11 flex-wrap items-center justify-between gap-x-3 gap-y-2 border-t border-rule bg-surface px-3 py-2 text-xs text-ink-muted"
		>
			{@render footer()}
		</div>
	{/if}
</div>

<style>
	.nix-frame {
		border: 1px solid var(--control-edge);
		border-radius: 2px;
		transition: border-color 150ms cubic-bezier(0.4, 0, 0.2, 1);
	}
	.nix-frame:has(:global(.cm-focused)) {
		border-color: var(--ink);
		outline: 2px solid var(--focus);
		outline-offset: 2px;
	}
	/* layout.css draws the edge and the ring on .cm-editor itself; here
	   the frame does, around the footer too. */
	.nix-frame :global(.cm-editor),
	.nix-frame :global(.cm-editor.cm-focused) {
		border: 0;
		border-radius: 1px;
		outline: none;
	}
	/* 1lh is the editor's own line (14px at 1.4); 8px is the content's
	   4px padding above and below. The gutter column gets the same floor,
	   over the height CodeMirror writes on it inline, so its rule runs the
	   editor's full height. */
	.nix-editor :global(.cm-content) {
		min-height: calc(var(--nix-rows) * 1lh + 8px);
	}
	.nix-editor :global(.cm-gutters) {
		min-height: calc(var(--nix-rows) * 1lh + 8px) !important;
	}
	.nix-editor :global(.cm-scroller) {
		max-height: calc(var(--nix-max) * 1lh + 8px);
	}
	.nix-editor :global(.cm-placeholder) {
		color: var(--ink-faint);
	}
</style>
