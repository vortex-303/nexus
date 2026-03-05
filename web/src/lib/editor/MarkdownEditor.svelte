<script lang="ts">
	import { onMount, onDestroy } from 'svelte';

	let { content = '', onchange }: { content?: string; onchange?: (md: string) => void } = $props();

	let editorEl: HTMLElement;
	let view: any = null;

	onMount(async () => {
		const { EditorView, keymap, lineNumbers, highlightActiveLineGutter, highlightActiveLine, drawSelection } = await import('@codemirror/view');
		const { EditorState } = await import('@codemirror/state');
		const { markdown } = await import('@codemirror/lang-markdown');
		const { defaultHighlightStyle, syntaxHighlighting, bracketMatching, indentOnInput } = await import('@codemirror/language');
		const { defaultKeymap, history, historyKeymap } = await import('@codemirror/commands');

		const theme = EditorView.theme({
			'&': {
				height: '100%',
				fontSize: '14px',
				fontFamily: 'var(--font-mono)',
			},
			'.cm-content': {
				padding: '16px',
				caretColor: 'var(--accent)',
				color: 'var(--text-primary)',
				lineHeight: '1.6',
			},
			'.cm-gutters': {
				background: 'var(--bg-surface)',
				color: 'var(--text-tertiary)',
				border: 'none',
				borderRight: '1px solid var(--border-subtle)',
			},
			'.cm-activeLineGutter': {
				background: 'var(--bg-raised)',
			},
			'.cm-activeLine': {
				background: 'rgba(255,255,255,0.02)',
			},
			'.cm-selectionBackground, &.cm-focused .cm-selectionBackground': {
				background: 'rgba(249, 115, 22, 0.15) !important',
			},
			'.cm-cursor': {
				borderLeftColor: 'var(--accent)',
			},
			'.cm-scroller': {
				overflow: 'auto',
			},
			'&.cm-focused': {
				outline: 'none',
			},
		});

		view = new EditorView({
			parent: editorEl,
			state: EditorState.create({
				doc: content,
				extensions: [
					lineNumbers(),
					highlightActiveLineGutter(),
					highlightActiveLine(),
					drawSelection(),
					indentOnInput(),
					bracketMatching(),
					history(),
					keymap.of([...defaultKeymap, ...historyKeymap]),
					markdown(),
					syntaxHighlighting(defaultHighlightStyle),
					theme,
					EditorView.updateListener.of((update) => {
						if (update.docChanged && onchange) {
							onchange(update.state.doc.toString());
						}
					}),
				],
			}),
		});
	});

	onDestroy(() => {
		if (view) view.destroy();
	});

	export function setContent(md: string) {
		if (!view) return;
		const { EditorState } = view.state.constructor;
		view.dispatch({
			changes: {
				from: 0,
				to: view.state.doc.length,
				insert: md,
			},
		});
	}

	export function getContent(): string {
		return view?.state.doc.toString() || '';
	}
</script>

<div class="markdown-editor" bind:this={editorEl}></div>

<style>
	.markdown-editor {
		flex: 1;
		overflow: hidden;
		background: var(--bg-root);
	}
	.markdown-editor :global(.cm-editor) {
		height: 100%;
	}
</style>
