<script lang="ts">
	import { searchWorkspace } from '$lib/api';

	interface Props {
		slug: string;
		onclose: () => void;
		onnavigate?: (type: string, id: string, result?: any) => void;
	}

	let { slug, onclose, onnavigate }: Props = $props();

	let query = $state('');
	let results = $state<any[]>([]);
	let loading = $state(false);
	let activeFilter = $state('');
	let debounceTimer: ReturnType<typeof setTimeout>;
	let inputEl: HTMLInputElement;
	let selectedIndex = $state(0);

	const filters = [
		{ key: '', label: 'All' },
		{ key: 'message', label: 'Messages' },
		{ key: 'task', label: 'Tasks' },
		{ key: 'document', label: 'Notes' },
		{ key: 'knowledge', label: 'Knowledge' },
		{ key: 'member', label: 'Members' },
		{ key: 'channel', label: 'Channels' },
	];

	$effect(() => {
		inputEl?.focus();
	});

	function doSearch() {
		clearTimeout(debounceTimer);
		if (!query.trim()) {
			results = [];
			return;
		}
		debounceTimer = setTimeout(async () => {
			loading = true;
			try {
				const data = await searchWorkspace(slug, query, activeFilter || undefined);
				results = data.results || [];
				selectedIndex = 0;
			} catch {
				results = [];
			}
			loading = false;
		}, 300);
	}

	function handleInput() {
		doSearch();
	}

	function setFilter(key: string) {
		activeFilter = key;
		if (query.trim()) {
			doSearch();
		}
	}

	function handleKeydown(e: KeyboardEvent) {
		if (e.key === 'Escape') {
			onclose();
		} else if (e.key === 'ArrowDown') {
			e.preventDefault();
			if (selectedIndex < results.length - 1) selectedIndex++;
		} else if (e.key === 'ArrowUp') {
			e.preventDefault();
			if (selectedIndex > 0) selectedIndex--;
		} else if (e.key === 'Enter' && results.length > 0) {
			e.preventDefault();
			navigateToResult(results[selectedIndex]);
		}
	}

	function navigateToResult(result: any) {
		if (onnavigate) {
			onnavigate(result.type, result.id, result);
		}
		onclose();
	}

	function typeIcon(type: string): string {
		switch (type) {
			case 'message': return '\u{1F4AC}';
			case 'document': return '\u{1F4DD}';
			case 'task': return '\u{2705}';
			case 'knowledge': return '\u{1F4DA}';
			case 'member': return '\u{1F464}';
			case 'channel': return '#';
			default: return '\u{1F50D}';
		}
	}

	function typeLabel(type: string): string {
		switch (type) {
			case 'message': return 'Message';
			case 'document': return 'Note';
			case 'task': return 'Task';
			case 'knowledge': return 'Knowledge';
			case 'member': return 'Member';
			case 'channel': return 'Channel';
			default: return type;
		}
	}
</script>

<!-- svelte-ignore a11y_click_events_have_key_events -->
<!-- svelte-ignore a11y_no_static_element_interactions -->
<div class="search-overlay" onclick={onclose}>
	<div class="search-modal" onclick={(e) => e.stopPropagation()} onkeydown={handleKeydown}>
		<div class="search-input-row">
			<span class="search-icon">&#x2315;</span>
			<input
				bind:this={inputEl}
				bind:value={query}
				oninput={handleInput}
				placeholder="Search messages, notes, tasks, knowledge..."
				class="search-input"
				spellcheck="false"
			/>
			<kbd class="search-kbd">ESC</kbd>
		</div>

		<div class="filter-row">
			{#each filters as f}
				<button
					class="filter-pill"
					class:active={activeFilter === f.key}
					onclick={() => setFilter(f.key)}
				>{f.label}</button>
			{/each}
		</div>

		{#if loading}
			<div class="search-status">Searching...</div>
		{:else if query && results.length === 0}
			<div class="search-status">No results found</div>
		{:else if results.length > 0}
			<div class="search-results">
				{#each results as result, i}
					<button
						class="search-result"
						class:selected={i === selectedIndex}
						onclick={() => navigateToResult(result)}
						onmouseenter={() => selectedIndex = i}
					>
						<span class="result-icon">{typeIcon(result.type)}</span>
						<div class="result-body">
							{#if result.title}
								<div class="result-title">{result.title}</div>
							{/if}
							{#if result.sender && result.type === 'message'}
								<div class="result-sender">{result.sender}</div>
							{/if}
							{#if result.highlight}
								<div class="result-content">{@html result.highlight}</div>
							{:else}
								<div class="result-content">{result.content}</div>
							{/if}
						</div>
						<span class="result-type">{typeLabel(result.type)}</span>
					</button>
				{/each}
			</div>
		{:else}
			<div class="search-status search-hint">Type to search across your workspace</div>
		{/if}
	</div>
</div>

<style>
	.search-overlay {
		position: fixed;
		inset: 0;
		background: rgba(0, 0, 0, 0.6);
		backdrop-filter: blur(4px);
		z-index: 9999;
		display: flex;
		justify-content: center;
		padding-top: 15vh;
	}

	.search-modal {
		width: 600px;
		max-height: 480px;
		background: var(--bg-raised);
		border: 1px solid var(--border-subtle);
		border-radius: 12px;
		overflow: hidden;
		display: flex;
		flex-direction: column;
		box-shadow: 0 20px 60px rgba(0, 0, 0, 0.5);
	}

	.search-input-row {
		display: flex;
		align-items: center;
		gap: 8px;
		padding: 14px 16px;
		border-bottom: 1px solid var(--border-subtle);
	}

	.search-icon {
		font-size: 18px;
		color: var(--text-tertiary);
	}

	.search-input {
		flex: 1;
		background: none;
		border: none;
		outline: none;
		color: var(--text-primary);
		font-size: 16px;
		font-family: inherit;
	}

	.search-input::placeholder {
		color: var(--text-tertiary);
	}

	.search-kbd {
		font-size: 11px;
		padding: 2px 6px;
		background: var(--bg-overlay);
		border: 1px solid var(--border-subtle);
		border-radius: 4px;
		color: var(--text-tertiary);
	}

	.filter-row {
		display: flex;
		gap: 4px;
		padding: 8px 16px;
		border-bottom: 1px solid var(--border-subtle);
		overflow-x: auto;
	}

	.filter-pill {
		padding: 4px 10px;
		border-radius: 12px;
		border: 1px solid var(--border-subtle);
		background: none;
		color: var(--text-secondary);
		font-size: 12px;
		font-family: inherit;
		cursor: pointer;
		white-space: nowrap;
		transition: all 0.15s;
	}

	.filter-pill:hover {
		background: var(--bg-overlay);
	}

	.filter-pill.active {
		background: var(--accent);
		color: var(--bg-base);
		border-color: var(--accent);
	}

	.search-status {
		padding: 24px;
		text-align: center;
		color: var(--text-secondary);
		font-size: 14px;
	}

	.search-hint {
		color: var(--text-tertiary);
	}

	.search-results {
		overflow-y: auto;
		padding: 4px;
	}

	.search-result {
		display: flex;
		align-items: flex-start;
		gap: 10px;
		padding: 10px 12px;
		border-radius: 8px;
		cursor: pointer;
		width: 100%;
		text-align: left;
		background: none;
		border: none;
		color: var(--text-primary);
		font-family: inherit;
		font-size: 13px;
	}

	.search-result:hover,
	.search-result.selected {
		background: var(--bg-overlay);
	}

	.result-icon {
		font-size: 16px;
		flex-shrink: 0;
		margin-top: 1px;
	}

	.result-body {
		flex: 1;
		min-width: 0;
		overflow: hidden;
	}

	.result-title {
		font-weight: 600;
		margin-bottom: 2px;
		white-space: nowrap;
		overflow: hidden;
		text-overflow: ellipsis;
	}

	.result-sender {
		font-size: 11px;
		color: var(--text-tertiary);
		margin-bottom: 2px;
	}

	.result-content {
		color: var(--text-secondary);
		font-size: 12px;
		display: -webkit-box;
		-webkit-line-clamp: 2;
		-webkit-box-orient: vertical;
		overflow: hidden;
	}

	.result-content :global(mark) {
		background: rgba(255, 165, 0, 0.3);
		color: var(--text-primary);
		border-radius: 2px;
		padding: 0 1px;
	}

	.result-type {
		font-size: 11px;
		color: var(--text-tertiary);
		flex-shrink: 0;
		padding: 2px 6px;
		background: var(--bg-surface);
		border-radius: 4px;
		margin-top: 2px;
	}
</style>
