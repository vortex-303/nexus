<script lang="ts">
	import { onMount, onDestroy } from 'svelte';
	import './editor.css';

	let { onsave, placeholder = 'Start writing...', initialContent = '' }: { onsave?: (html: string) => void; placeholder?: string; initialContent?: string } = $props();

	let editorEl: HTMLElement;
	let editor: any = null;
	let bubbleEl: HTMLElement;
	let bubbleTippy: any = null;
	let saveTimer: ReturnType<typeof setTimeout> | null = null;
	let linkMode = $state(false);
	let linkUrl = $state('');

	// Reactive marks for bubble menu
	let isBold = $state(false);
	let isItalic = $state(false);
	let isStrike = $state(false);
	let isCode = $state(false);
	let isLink = $state(false);

	onMount(async () => {
		const { Editor } = await import('@tiptap/core');
		const { default: StarterKit } = await import('@tiptap/starter-kit');
		const { default: Placeholder } = await import('@tiptap/extension-placeholder');
		const { default: Link } = await import('@tiptap/extension-link');
		const { default: TaskList } = await import('@tiptap/extension-task-list');
		const { default: TaskItem } = await import('@tiptap/extension-task-item');
		const { default: Image } = await import('@tiptap/extension-image');
		const { default: CodeBlockLowlight } = await import('@tiptap/extension-code-block-lowlight');
		const { common, createLowlight } = await import('lowlight');
		const { SlashCommands } = await import('./extensions/slash-commands');
		const { default: tippy } = await import('tippy.js');
		const { Plugin, PluginKey } = await import('@tiptap/pm/state');

		const lowlight = createLowlight(common);

		editor = new Editor({
			element: editorEl,
			extensions: [
				StarterKit.configure({
					codeBlock: false,
				}),
				Placeholder.configure({ placeholder }),
				Link.configure({ openOnClick: false, autolink: true }),
				TaskList,
				TaskItem.configure({ nested: true }),
				Image,
				CodeBlockLowlight.configure({ lowlight }),
				SlashCommands,
				// Bubble menu plugin
				Extension_BubbleMenu(tippy, bubbleEl),
			],
			content: initialContent || '',
			onUpdate: ({ editor: e }) => {
				updateMarks(e);
				if (onsave) {
					if (saveTimer) clearTimeout(saveTimer);
					saveTimer = setTimeout(() => {
						onsave(e.getHTML());
					}, 1500);
				}
			},
			onSelectionUpdate: ({ editor: e }) => {
				updateMarks(e);
			},
		});

		function Extension_BubbleMenu(tippyFn: typeof tippy, el: HTMLElement) {
			return new Plugin({
				key: new PluginKey('bubbleMenu'),
				view(view) {
					const instance = tippyFn(document.body, {
						getReferenceClientRect: () => {
							const { from, to } = view.state.selection;
							const start = view.coordsAtPos(from);
							const end = view.coordsAtPos(to);
							return {
								top: Math.min(start.top, end.top),
								bottom: Math.max(start.bottom, end.bottom),
								left: start.left,
								right: end.right,
								width: end.right - start.left,
								height: Math.max(start.bottom, end.bottom) - Math.min(start.top, end.top),
								x: start.left,
								y: Math.min(start.top, end.top),
								toJSON() { return this; },
							} as DOMRect;
						},
						content: el,
						interactive: true,
						trigger: 'manual',
						placement: 'top',
						arrow: false,
						offset: [0, 8],
						appendTo: () => document.body,
					});

					return {
						update(view) {
							const { from, to, empty } = view.state.selection;
							if (empty || from === to) {
								instance.hide();
								linkMode = false;
								return;
							}
							// Don't show in code blocks
							const resolvedPos = view.state.doc.resolve(from);
							if (resolvedPos.parent.type.name === 'codeBlock') {
								instance.hide();
								return;
							}
							instance.setProps({
								getReferenceClientRect: () => {
									const start = view.coordsAtPos(from);
									const end = view.coordsAtPos(to);
									return {
										top: Math.min(start.top, end.top),
										bottom: Math.max(start.bottom, end.bottom),
										left: start.left,
										right: end.right,
										width: end.right - start.left,
										height: Math.max(start.bottom, end.bottom) - Math.min(start.top, end.top),
										x: start.left,
										y: Math.min(start.top, end.top),
										toJSON() { return this; },
									} as DOMRect;
								},
							});
							instance.show();
						},
						destroy() {
							instance.destroy();
						},
					};
				},
			});
		}
	});

	onDestroy(() => {
		if (saveTimer) clearTimeout(saveTimer);
		if (editor) editor.destroy();
	});

	function updateMarks(e: any) {
		isBold = e.isActive('bold');
		isItalic = e.isActive('italic');
		isStrike = e.isActive('strike');
		isCode = e.isActive('code');
		isLink = e.isActive('link');
	}

	function toggleBold() { editor?.chain().focus().toggleBold().run(); }
	function toggleItalic() { editor?.chain().focus().toggleItalic().run(); }
	function toggleStrike() { editor?.chain().focus().toggleStrike().run(); }
	function toggleCode() { editor?.chain().focus().toggleCode().run(); }

	function handleLink() {
		if (isLink) {
			editor?.chain().focus().unsetLink().run();
			return;
		}
		linkMode = true;
		linkUrl = '';
	}

	function applyLink() {
		if (linkUrl) {
			const url = linkUrl.startsWith('http') ? linkUrl : `https://${linkUrl}`;
			editor?.chain().focus().setLink({ href: url }).run();
		}
		linkMode = false;
		linkUrl = '';
	}

	function handleLinkKeydown(e: KeyboardEvent) {
		if (e.key === 'Enter') { e.preventDefault(); applyLink(); }
		if (e.key === 'Escape') { linkMode = false; linkUrl = ''; }
	}

	export function setContent(html: string) {
		if (editor) {
			editor.commands.setContent(html, false);
		}
	}

	export function getHTML(): string {
		return editor?.getHTML() || '';
	}

	export function migrateContent(content: string): string {
		if (!content) return '';
		// If it already looks like HTML, pass through
		if (content.trimStart().startsWith('<')) return content;
		// Plain text → wrap each line in <p>
		return content
			.split('\n')
			.map((line) => `<p>${line || '<br>'}</p>`)
			.join('');
	}
</script>

<div class="tiptap-editor">
	<div bind:this={editorEl}></div>
</div>

<!-- Bubble menu (rendered offscreen, tippy teleports it) -->
<div bind:this={bubbleEl} class="bubble-menu" style="display:none;" onmousedown={(e) => e.preventDefault()}>
	{#if linkMode}
		<div class="link-input-wrap">
			<input
				class="link-input"
				type="text"
				placeholder="Paste URL..."
				bind:value={linkUrl}
				onkeydown={handleLinkKeydown}
			/>
			<button onclick={applyLink}>✓</button>
		</div>
	{:else}
		<button class:is-active={isBold} onclick={toggleBold} title="Bold"><strong>B</strong></button>
		<button class:is-active={isItalic} onclick={toggleItalic} title="Italic"><em>I</em></button>
		<button class:is-active={isStrike} onclick={toggleStrike} title="Strikethrough"><s>S</s></button>
		<button class:is-active={isCode} onclick={toggleCode} title="Code">⌘</button>
		<div class="separator"></div>
		<button class:is-active={isLink} onclick={handleLink} title="Link">🔗</button>
	{/if}
</div>
