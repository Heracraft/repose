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
	import { syntaxHighlighting, bracketMatching } from '@codemirror/language';
	import { classHighlighter } from '@lezer/highlight';
	import { nix } from '@replit/codemirror-lang-nix';

	let {
		value = $bindable(''),
		errorLine,
		readonly = false,
		label = 'Nix fragment',
		placeholder = '{ pkgs, ... }: { home.packages = [ ]; }',
		floor,
		footer
	}: {
		value: string;
		errorLine?: number;
		readonly?: boolean;
		/** The editor's accessible name: CodeMirror's content is a role=textbox with none of its own. */
		label?: string;
		/** Shown in the pane's muted colour while the editor is empty; never saved. */
		placeholder?: string;
		/**
		 * A CSS length the code area is at least, over the five-line floor:
		 * the config page, where the editor is the page's subject, asks for
		 * room to write in.
		 */
		floor?: string;
		/**
		 * A bar inside the editor's frame, under the code: the caller's
		 * status on the left and its action (Save, Apply) on the right.
		 */
		footer?: Snippet;
	} = $props();

	// The editor is as tall as its text plus one empty line, at least five
	// lines (or the caller's floor) and at most 24; past that it scrolls
	// inside itself. A one-line machine.nix took six lines and three of
	// them were empty paper.
	const MIN_ROWS = 5;
	const MAX_ROWS = 24;
	let rows = $derived(Math.min(MAX_ROWS, Math.max(MIN_ROWS, value.split('\n').length + 1)));

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
				// Classes, not inline colours: nix-editor.css paints them in the
				// code pane's tokens, which hold on its near-black ground.
				syntaxHighlighting(classHighlighter),
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

<!-- The frame is the near-black pane (nix-editor.css): it carries the
     rings and the focus ring, so the footer bar sits inside the same box as
     the code it acts on. -->
<div class="nix-frame">
	<div
		class="nix-editor"
		style:--nix-rows={rows}
		style:--nix-max={MAX_ROWS}
		style:--nix-floor={floor}
		bind:this={container}
	></div>
	{#if footer}
		<div class="nix-footer">
			{@render footer()}
		</div>
	{/if}
</div>

<style>
	/* 20px is the editor's own line; 8px is the content's 4px padding
	   above and below. The gutter column gets the same floor, over the
	   height CodeMirror writes on it inline, so its rule runs the editor's
	   full height. */
	.nix-editor :global(.cm-content) {
		min-height: max(calc(var(--nix-rows) * 1.25rem + 8px), var(--nix-floor, 0px));
	}
	.nix-editor :global(.cm-gutters) {
		min-height: max(calc(var(--nix-rows) * 1.25rem + 8px), var(--nix-floor, 0px)) !important;
	}
	.nix-editor :global(.cm-scroller) {
		max-height: calc(var(--nix-max) * 1.25rem + 8px);
	}
</style>
