<script lang="ts">
	import { onMount } from 'svelte';

	let { onselect }: { onselect: (emoji: string) => void } = $props();

	let search = $state('');
	let activeCategory = $state('smileys');

	const categories = [
		{ id: 'smileys', label: '😀', name: 'Smileys' },
		{ id: 'people', label: '👋', name: 'People' },
		{ id: 'nature', label: '🌿', name: 'Nature' },
		{ id: 'food', label: '🍕', name: 'Food' },
		{ id: 'objects', label: '💡', name: 'Objects' },
		{ id: 'symbols', label: '❤️', name: 'Symbols' },
	];

	const emojis: Record<string, { emoji: string; name: string }[]> = {
		smileys: [
			{ emoji: '😀', name: 'grinning' }, { emoji: '😃', name: 'smiley' }, { emoji: '😄', name: 'smile' },
			{ emoji: '😁', name: 'grin' }, { emoji: '😆', name: 'laughing' }, { emoji: '😅', name: 'sweat smile' },
			{ emoji: '🤣', name: 'rofl' }, { emoji: '😂', name: 'joy' }, { emoji: '🙂', name: 'slightly smiling' },
			{ emoji: '😊', name: 'blush' }, { emoji: '😇', name: 'innocent' }, { emoji: '🥰', name: 'smiling hearts' },
			{ emoji: '😍', name: 'heart eyes' }, { emoji: '🤩', name: 'star struck' }, { emoji: '😘', name: 'kissing heart' },
			{ emoji: '😜', name: 'winking tongue' }, { emoji: '🤔', name: 'thinking' }, { emoji: '🤗', name: 'hugging' },
			{ emoji: '😎', name: 'sunglasses' }, { emoji: '🥳', name: 'partying' }, { emoji: '😤', name: 'triumph' },
			{ emoji: '😭', name: 'sob' }, { emoji: '😱', name: 'scream' }, { emoji: '🤯', name: 'exploding head' },
			{ emoji: '😴', name: 'sleeping' }, { emoji: '🥱', name: 'yawning' }, { emoji: '😷', name: 'mask' },
		],
		people: [
			{ emoji: '👍', name: 'thumbs up' }, { emoji: '👎', name: 'thumbs down' }, { emoji: '👏', name: 'clap' },
			{ emoji: '🙌', name: 'raised hands' }, { emoji: '🤝', name: 'handshake' }, { emoji: '✌️', name: 'peace' },
			{ emoji: '🤞', name: 'crossed fingers' }, { emoji: '💪', name: 'muscle' }, { emoji: '🙏', name: 'pray' },
			{ emoji: '👋', name: 'wave' }, { emoji: '✋', name: 'raised hand' }, { emoji: '🤙', name: 'call me' },
			{ emoji: '👀', name: 'eyes' }, { emoji: '🧠', name: 'brain' }, { emoji: '🗣️', name: 'speaking head' },
			{ emoji: '💀', name: 'skull' }, { emoji: '🫡', name: 'salute' }, { emoji: '🫶', name: 'heart hands' },
		],
		nature: [
			{ emoji: '🔥', name: 'fire' }, { emoji: '⭐', name: 'star' }, { emoji: '🌟', name: 'glowing star' },
			{ emoji: '💫', name: 'dizzy' }, { emoji: '✨', name: 'sparkles' }, { emoji: '⚡', name: 'lightning' },
			{ emoji: '🌈', name: 'rainbow' }, { emoji: '☀️', name: 'sun' }, { emoji: '🌙', name: 'moon' },
			{ emoji: '🌍', name: 'earth' }, { emoji: '🐶', name: 'dog' }, { emoji: '🐱', name: 'cat' },
			{ emoji: '🦊', name: 'fox' }, { emoji: '🐻', name: 'bear' }, { emoji: '🌸', name: 'cherry blossom' },
			{ emoji: '🌺', name: 'hibiscus' }, { emoji: '🍀', name: 'four leaf clover' }, { emoji: '🌿', name: 'herb' },
		],
		food: [
			{ emoji: '☕', name: 'coffee' }, { emoji: '🍺', name: 'beer' }, { emoji: '🍕', name: 'pizza' },
			{ emoji: '🍔', name: 'burger' }, { emoji: '🌮', name: 'taco' }, { emoji: '🍣', name: 'sushi' },
			{ emoji: '🍩', name: 'donut' }, { emoji: '🎂', name: 'birthday cake' }, { emoji: '🍰', name: 'cake' },
			{ emoji: '🍫', name: 'chocolate' }, { emoji: '🍿', name: 'popcorn' }, { emoji: '🥤', name: 'cup with straw' },
		],
		objects: [
			{ emoji: '💡', name: 'lightbulb' }, { emoji: '🎯', name: 'target' }, { emoji: '🏆', name: 'trophy' },
			{ emoji: '🎉', name: 'party popper' }, { emoji: '🎊', name: 'confetti' }, { emoji: '🚀', name: 'rocket' },
			{ emoji: '💻', name: 'laptop' }, { emoji: '📱', name: 'phone' }, { emoji: '⌨️', name: 'keyboard' },
			{ emoji: '🔧', name: 'wrench' }, { emoji: '🔗', name: 'link' }, { emoji: '📎', name: 'paperclip' },
			{ emoji: '📝', name: 'memo' }, { emoji: '📊', name: 'bar chart' }, { emoji: '🔒', name: 'lock' },
			{ emoji: '🔑', name: 'key' }, { emoji: '💰', name: 'money bag' }, { emoji: '⏰', name: 'alarm clock' },
		],
		symbols: [
			{ emoji: '❤️', name: 'red heart' }, { emoji: '🧡', name: 'orange heart' }, { emoji: '💛', name: 'yellow heart' },
			{ emoji: '💚', name: 'green heart' }, { emoji: '💙', name: 'blue heart' }, { emoji: '💜', name: 'purple heart' },
			{ emoji: '✅', name: 'check mark' }, { emoji: '❌', name: 'cross mark' }, { emoji: '⚠️', name: 'warning' },
			{ emoji: '❓', name: 'question' }, { emoji: '❗', name: 'exclamation' }, { emoji: '💯', name: 'hundred' },
			{ emoji: '➕', name: 'plus' }, { emoji: '➖', name: 'minus' }, { emoji: '♻️', name: 'recycle' },
			{ emoji: '🔴', name: 'red circle' }, { emoji: '🟢', name: 'green circle' }, { emoji: '🔵', name: 'blue circle' },
		],
	};

	let filtered = $derived.by(() => {
		if (!search) return emojis[activeCategory] || [];
		const q = search.toLowerCase();
		const results: { emoji: string; name: string }[] = [];
		for (const cat of Object.values(emojis)) {
			for (const e of cat) {
				if (e.name.includes(q) || e.emoji === q) results.push(e);
			}
		}
		return results;
	});

	let searchEl: HTMLInputElement;
	onMount(() => searchEl?.focus());
</script>

<div class="emoji-picker" onclick={(e) => e.stopPropagation()}>
	<div class="ep-search">
		<input type="text" placeholder="Search emoji..." bind:value={search} bind:this={searchEl} />
	</div>
	<div class="ep-categories">
		{#each categories as cat}
			<button
				class="ep-cat-btn"
				class:active={activeCategory === cat.id && !search}
				onclick={() => { activeCategory = cat.id; search = ''; }}
				title={cat.name}
			>{cat.label}</button>
		{/each}
	</div>
	<div class="ep-grid">
		{#each filtered as e}
			<button class="ep-emoji" onclick={() => onselect(e.emoji)} title={e.name}>
				{e.emoji}
			</button>
		{/each}
		{#if filtered.length === 0}
			<div class="ep-empty">No emoji found</div>
		{/if}
	</div>
</div>

<style>
	.emoji-picker {
		width: 320px;
		background: var(--bg-raised);
		border: 1px solid var(--border-subtle);
		border-radius: var(--radius-lg);
		box-shadow: 0 8px 32px rgba(0,0,0,0.3);
		overflow: hidden;
	}
	.ep-search {
		padding: 8px;
		border-bottom: 1px solid var(--border-subtle);
	}
	.ep-search input {
		width: 100%;
		padding: 6px 10px;
		background: var(--bg-surface);
		border: 1px solid var(--border-subtle);
		border-radius: var(--radius-sm);
		color: var(--text-primary);
		font-size: 13px;
		outline: none;
	}
	.ep-search input:focus {
		border-color: var(--accent);
	}
	.ep-categories {
		display: flex;
		gap: 2px;
		padding: 4px 8px;
		border-bottom: 1px solid var(--border-subtle);
	}
	.ep-cat-btn {
		flex: 1;
		padding: 4px;
		background: none;
		border: none;
		border-radius: var(--radius-sm);
		cursor: pointer;
		font-size: 16px;
		opacity: 0.5;
		transition: opacity 0.15s, background 0.15s;
	}
	.ep-cat-btn:hover, .ep-cat-btn.active {
		opacity: 1;
		background: var(--bg-surface);
	}
	.ep-grid {
		display: grid;
		grid-template-columns: repeat(8, 1fr);
		gap: 2px;
		padding: 8px;
		max-height: 200px;
		overflow-y: auto;
	}
	.ep-emoji {
		padding: 4px;
		background: none;
		border: none;
		border-radius: var(--radius-sm);
		cursor: pointer;
		font-size: 20px;
		line-height: 1;
		transition: background 0.1s, transform 0.1s;
	}
	.ep-emoji:hover {
		background: var(--bg-surface);
		transform: scale(1.2);
	}
	.ep-empty {
		grid-column: 1 / -1;
		text-align: center;
		color: var(--text-tertiary);
		font-size: 13px;
		padding: 16px;
	}
</style>
