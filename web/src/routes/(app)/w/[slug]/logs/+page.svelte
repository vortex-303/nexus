<script lang="ts">
	import { page } from '$app/state';
	import { goto } from '$app/navigation';
	import { onMount, onDestroy } from 'svelte';
	import { getLogs, getCurrentUser } from '$lib/api';

	let slug = $derived(page.params.slug);
	let currentUser = $state(getCurrentUser());

	let entries = $state<any[]>([]);
	let categories = $state<string[]>([]);
	let total = $state(0);
	let loading = $state(false);

	// Filters
	let selectedCategory = $state('');
	let selectedLevel = $state('');
	let selectedRange = $state('1h');
	let autoRefresh = $state(false);
	let refreshInterval: ReturnType<typeof setInterval> | null = null;

	// Expanded entries
	let expandedIds = $state<Set<number>>(new Set());

	const LEVELS = ['', 'info', 'warn', 'error'] as const;
	const RANGES = [
		{ label: '1h', value: '1h' },
		{ label: '24h', value: '24h' },
		{ label: '7d', value: '7d' },
	];

	const CATEGORY_COLORS: Record<string, string> = {
		security: '#ef4444',
		api: '#3b82f6',
		brain: '#8b5cf6',
		agent: '#10b981',
		calendar: '#f59e0b',
		email: '#06b6d4',
		system: '#6366f1',
		websocket: '#ec4899',
	};

	function levelColor(level: string): string {
		if (level === 'error') return '#ef4444';
		if (level === 'warn') return '#f59e0b';
		return 'var(--text-tertiary)';
	}

	async function fetchLogs() {
		loading = true;
		try {
			const data = await getLogs(slug, {
				category: selectedCategory || undefined,
				level: selectedLevel || undefined,
				since: selectedRange,
				limit: 200,
			});
			entries = data.entries || [];
			categories = data.categories || [];
			total = data.total || 0;
		} catch (e: any) {
			console.error('Failed to load logs:', e);
		}
		loading = false;
	}

	function toggleExpand(idx: number) {
		const next = new Set(expandedIds);
		if (next.has(idx)) next.delete(idx);
		else next.add(idx);
		expandedIds = next;
	}

	function formatTime(ts: string): string {
		try {
			const d = new Date(ts);
			return d.toLocaleTimeString([], { hour: '2-digit', minute: '2-digit', second: '2-digit' });
		} catch {
			return ts;
		}
	}

	function toggleAutoRefresh() {
		autoRefresh = !autoRefresh;
		if (autoRefresh) {
			refreshInterval = setInterval(fetchLogs, 5000);
		} else if (refreshInterval) {
			clearInterval(refreshInterval);
			refreshInterval = null;
		}
	}

	onMount(() => {
		if (currentUser?.role !== 'admin') {
			goto(`/w/${slug}`);
			return;
		}
		fetchLogs();
	});

	onDestroy(() => {
		if (refreshInterval) clearInterval(refreshInterval);
	});

	// Re-fetch when filters change
	$effect(() => {
		selectedCategory; selectedLevel; selectedRange;
		fetchLogs();
	});
</script>

<div class="logs-page">
	<header class="logs-header">
		<button class="back-btn" onclick={() => goto(`/w/${slug}`)}>
			<svg width="16" height="16" viewBox="0 0 16 16" fill="none"><path d="M10 3L5 8l5 5" stroke="currentColor" stroke-width="1.5" stroke-linecap="round" stroke-linejoin="round"/></svg>
		</button>
		<h1>System Logs</h1>
		<span class="log-count">{total} total</span>
		<div style="flex:1"></div>
		<button class="refresh-btn" class:active={autoRefresh} onclick={toggleAutoRefresh}>
			{autoRefresh ? 'Auto-refresh ON' : 'Auto-refresh'}
		</button>
		<button class="refresh-btn" onclick={fetchLogs} disabled={loading}>
			{loading ? 'Loading...' : 'Refresh'}
		</button>
	</header>

	<div class="filters">
		<div class="filter-group">
			<span class="filter-label">Category</span>
			<div class="filter-pills">
				<button class="pill" class:active={selectedCategory === ''} onclick={() => selectedCategory = ''}>All</button>
				{#each categories as cat}
					<button class="pill" class:active={selectedCategory === cat} onclick={() => selectedCategory = selectedCategory === cat ? '' : cat} style="--pill-color: {CATEGORY_COLORS[cat] || 'var(--text-tertiary)'}">
						{cat}
					</button>
				{/each}
			</div>
		</div>
		<div class="filter-group">
			<span class="filter-label">Level</span>
			<div class="filter-pills">
				{#each LEVELS as lvl}
					<button class="pill" class:active={selectedLevel === lvl} onclick={() => selectedLevel = lvl}>
						{lvl || 'All'}
					</button>
				{/each}
			</div>
		</div>
		<div class="filter-group">
			<span class="filter-label">Range</span>
			<div class="filter-pills">
				{#each RANGES as r}
					<button class="pill" class:active={selectedRange === r.value} onclick={() => selectedRange = r.value}>
						{r.label}
					</button>
				{/each}
			</div>
		</div>
	</div>

	<div class="log-entries">
		{#if entries.length === 0 && !loading}
			<div class="empty">No log entries found for the selected filters.</div>
		{/if}
		{#each entries as entry, idx}
			<button class="log-entry" onclick={() => toggleExpand(idx)}>
				<span class="log-time">{formatTime(entry.time)}</span>
				<span class="log-level" style="color: {levelColor(entry.level)}">{entry.level?.toUpperCase() || 'INFO'}</span>
				<span class="log-category" style="color: {CATEGORY_COLORS[entry.category] || 'var(--text-tertiary)'}">{entry.category || '-'}</span>
				<span class="log-message">{entry.message || ''}</span>
				<svg class="expand-icon" class:expanded={expandedIds.has(idx)} width="12" height="12" viewBox="0 0 12 12" fill="none"><path d="M3 5l3 3 3-3" stroke="currentColor" stroke-width="1.2" stroke-linecap="round"/></svg>
			</button>
			{#if expandedIds.has(idx)}
				<div class="log-detail">
					<pre>{JSON.stringify(entry.fields || entry, null, 2)}</pre>
				</div>
			{/if}
		{/each}
	</div>
</div>

<style>
	.logs-page {
		display: flex;
		flex-direction: column;
		height: 100vh;
		background: var(--bg-primary);
		color: var(--text-primary);
	}
	.logs-header {
		display: flex;
		align-items: center;
		gap: 12px;
		padding: 16px 24px;
		border-bottom: 1px solid var(--border);
	}
	.logs-header h1 {
		font-size: 18px;
		font-weight: 600;
		margin: 0;
	}
	.log-count {
		color: var(--text-tertiary);
		font-size: 13px;
	}
	.back-btn {
		background: none;
		border: none;
		color: var(--text-secondary);
		cursor: pointer;
		padding: 4px;
		border-radius: 4px;
	}
	.back-btn:hover { background: var(--bg-tertiary); }
	.refresh-btn {
		background: var(--bg-secondary);
		border: 1px solid var(--border);
		color: var(--text-secondary);
		padding: 6px 12px;
		border-radius: 6px;
		font-size: 12px;
		cursor: pointer;
	}
	.refresh-btn:hover { background: var(--bg-tertiary); }
	.refresh-btn.active {
		background: var(--accent);
		color: white;
		border-color: var(--accent);
	}
	.filters {
		display: flex;
		flex-wrap: wrap;
		gap: 16px;
		padding: 12px 24px;
		border-bottom: 1px solid var(--border);
	}
	.filter-group {
		display: flex;
		align-items: center;
		gap: 8px;
	}
	.filter-label {
		font-size: 12px;
		color: var(--text-tertiary);
		font-weight: 500;
		text-transform: uppercase;
		letter-spacing: 0.5px;
	}
	.filter-pills {
		display: flex;
		gap: 4px;
		flex-wrap: wrap;
	}
	.pill {
		background: var(--bg-secondary);
		border: 1px solid var(--border);
		color: var(--text-secondary);
		padding: 3px 10px;
		border-radius: 12px;
		font-size: 11px;
		cursor: pointer;
		transition: all 0.15s;
	}
	.pill:hover { background: var(--bg-tertiary); }
	.pill.active {
		background: var(--pill-color, var(--accent));
		color: white;
		border-color: var(--pill-color, var(--accent));
	}
	.log-entries {
		flex: 1;
		overflow-y: auto;
		padding: 0;
	}
	.empty {
		text-align: center;
		color: var(--text-tertiary);
		padding: 48px 24px;
		font-size: 14px;
	}
	.log-entry {
		display: flex;
		align-items: center;
		gap: 12px;
		padding: 8px 24px;
		border-bottom: 1px solid var(--border);
		width: 100%;
		background: none;
		border-left: none;
		border-right: none;
		border-top: none;
		color: inherit;
		font-family: inherit;
		font-size: 12px;
		cursor: pointer;
		text-align: left;
	}
	.log-entry:hover { background: var(--bg-secondary); }
	.log-time {
		color: var(--text-tertiary);
		font-family: monospace;
		font-size: 11px;
		flex-shrink: 0;
		min-width: 72px;
	}
	.log-level {
		font-weight: 600;
		font-size: 10px;
		text-transform: uppercase;
		flex-shrink: 0;
		min-width: 40px;
	}
	.log-category {
		font-size: 11px;
		font-weight: 500;
		flex-shrink: 0;
		min-width: 72px;
	}
	.log-message {
		flex: 1;
		overflow: hidden;
		text-overflow: ellipsis;
		white-space: nowrap;
		color: var(--text-primary);
	}
	.expand-icon {
		flex-shrink: 0;
		transition: transform 0.15s;
		color: var(--text-tertiary);
	}
	.expand-icon.expanded { transform: rotate(180deg); }
	.log-detail {
		background: var(--bg-secondary);
		padding: 12px 24px 12px 48px;
		border-bottom: 1px solid var(--border);
	}
	.log-detail pre {
		margin: 0;
		font-size: 11px;
		color: var(--text-secondary);
		white-space: pre-wrap;
		word-break: break-all;
		font-family: monospace;
	}
</style>
