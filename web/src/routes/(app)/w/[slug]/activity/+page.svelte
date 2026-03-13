<script lang="ts">
	import { page } from '$app/state';
	import { goto } from '$app/navigation';
	import { onMount, onDestroy } from 'svelte';
	import { listActivity, getActivityStats } from '$lib/api';
	import { connect, disconnect, onMessage } from '$lib/ws';

	let slug = $derived(page.params.slug);
	let activities = $state<any[]>([]);
	let stats = $state<any>(null);
	let loading = $state(true);
	let loadingMore = $state(false);
	let hasMore = $state(true);
	let activeFilter = $state('all');
	let unsubWS: (() => void) | null = null;

	const FILTERS = [
		{ key: 'all', label: 'All' },
		{ key: 'message', label: 'Messages' },
		{ key: 'task', label: 'Tasks' },
		{ key: 'document', label: 'Docs' },
		{ key: 'file', label: 'Files' },
		{ key: 'event', label: 'Events' },
		{ key: 'agent', label: 'Agents' },
		{ key: 'integration', label: 'Integrations' },
	];

	const ICONS: Record<string, string> = {
		'message.sent': '\u{1F4AC}',
		'task.created': '\u{1F4CB}',
		'task.updated': '\u{270F}\u{FE0F}',
		'task.completed': '\u{2705}',
		'task.deleted': '\u{1F5D1}\u{FE0F}',
		'event.created': '\u{1F4C5}',
		'event.updated': '\u{1F4C5}',
		'event.deleted': '\u{1F4C5}',
		'document.created': '\u{1F4DD}',
		'document.updated': '\u{1F4DD}',
		'file.uploaded': '\u{1F4CE}',
		'channel.created': '\u{1F4E2}',
		'integration.received': '\u{1F4E7}',
		'agent.responded': '\u{1F916}',
	};

	function getIcon(type: string): string {
		return ICONS[type] || '\u{26A1}';
	}

	function timeAgo(dateStr: string): string {
		const now = Date.now();
		const then = new Date(dateStr).getTime();
		const diff = Math.floor((now - then) / 1000);
		if (diff < 60) return 'just now';
		if (diff < 3600) return `${Math.floor(diff / 60)}m ago`;
		if (diff < 86400) return `${Math.floor(diff / 3600)}h ago`;
		const days = Math.floor(diff / 86400);
		if (days === 1) return '1d ago';
		if (days < 30) return `${days}d ago`;
		return new Date(dateStr).toLocaleDateString();
	}

	function dayLabel(dateStr: string): string {
		const d = new Date(dateStr);
		const now = new Date();
		const today = new Date(now.getFullYear(), now.getMonth(), now.getDate());
		const target = new Date(d.getFullYear(), d.getMonth(), d.getDate());
		const diff = Math.floor((today.getTime() - target.getTime()) / 86400000);
		if (diff === 0) return 'Today';
		if (diff === 1) return 'Yesterday';
		return d.toLocaleDateString('en-US', { weekday: 'long', month: 'long', day: 'numeric' });
	}

	function groupByDay(items: any[]): { label: string; items: any[] }[] {
		const groups: { label: string; items: any[] }[] = [];
		let currentLabel = '';
		for (const item of items) {
			const label = dayLabel(item.created_at);
			if (label !== currentLabel) {
				currentLabel = label;
				groups.push({ label, items: [] });
			}
			groups[groups.length - 1].items.push(item);
		}
		return groups;
	}

	// Heatmap helpers
	function buildHeatmapGrid(dailyCounts: { date: string; count: number }[]) {
		const countMap = new Map<string, number>();
		for (const dc of dailyCounts) {
			countMap.set(dc.date, dc.count);
		}

		const today = new Date();
		const dow = today.getDay(); // 0=Sun
		// Grid: 53 columns x 7 rows, ending at today
		const totalDays = 52 * 7 + dow + 1;
		const startDate = new Date(today);
		startDate.setDate(startDate.getDate() - totalDays + 1);

		let maxCount = 0;
		const days: { date: string; count: number; dow: number; week: number }[] = [];
		for (let i = 0; i < totalDays; i++) {
			const d = new Date(startDate);
			d.setDate(d.getDate() + i);
			const dateStr = d.toISOString().slice(0, 10);
			const count = countMap.get(dateStr) || 0;
			if (count > maxCount) maxCount = count;
			days.push({ date: dateStr, count, dow: d.getDay(), week: Math.floor(i / 7) });
		}

		// Assign levels
		return { days, maxCount, totalDays };
	}

	function heatLevel(count: number, max: number): number {
		if (count === 0) return 0;
		if (max === 0) return 0;
		return Math.min(4, Math.ceil(count / (max / 4)));
	}

	function tooltipText(date: string, count: number): string {
		const d = new Date(date + 'T00:00:00');
		const formatted = d.toLocaleDateString('en-US', { month: 'long', day: 'numeric', year: 'numeric' });
		return `${count} activit${count === 1 ? 'y' : 'ies'} on ${formatted}`;
	}

	// Stats helpers
	function countByPrefix(typeCounts: Record<string, number>, prefix: string): number {
		let sum = 0;
		for (const [k, v] of Object.entries(typeCounts)) {
			if (k.startsWith(prefix)) sum += v;
		}
		return sum;
	}

	async function loadData() {
		loading = true;
		try {
			const typeParam = activeFilter === 'all' ? undefined : activeFilter + '.*';
			const [actResult, statsResult] = await Promise.all([
				listActivity(slug, { type: typeParam, limit: 50 }),
				getActivityStats(slug, 365),
			]);
			activities = actResult.activities || [];
			stats = statsResult;
			hasMore = activities.length === 50;
		} catch (e) {
			console.error('Failed to load activity:', e);
		}
		loading = false;
	}

	async function loadMore() {
		if (loadingMore || !hasMore || activities.length === 0) return;
		loadingMore = true;
		try {
			const last = activities[activities.length - 1];
			const typeParam = activeFilter === 'all' ? undefined : activeFilter + '.*';
			const result = await listActivity(slug, { type: typeParam, before: last.created_at, limit: 50 });
			const more = result.activities || [];
			activities = [...activities, ...more];
			hasMore = more.length === 50;
		} catch (e) {
			console.error('Failed to load more:', e);
		}
		loadingMore = false;
	}

	function setFilter(key: string) {
		activeFilter = key;
		loadData();
	}

	onMount(() => {
		connect();
		unsubWS = onMessage((type: string, payload: any) => {
			if (type === 'activity.new') {
				// Check if it matches current filter
				if (activeFilter === 'all' || payload.pulse_type.startsWith(activeFilter + '.')) {
					// Check if this is an update to an existing batched entry
					const existingIdx = activities.findIndex(a => a.id === payload.id);
					if (existingIdx >= 0) {
						// Update in place and move to top
						activities = [payload, ...activities.filter((_, i) => i !== existingIdx)];
					} else {
						activities = [payload, ...activities];
					}
				}
				// Update stats total
				if (stats) {
					stats = { ...stats, total: (stats.total || 0) + 1 };
				}
			}
			if (type === '_reconnected') {
				loadData();
			}
		});
		loadData();
	});

	onDestroy(() => {
		if (unsubWS) unsubWS();
		disconnect();
	});

	let dayGroups = $derived(groupByDay(activities));
	let heatmap = $derived(stats ? buildHeatmapGrid(stats.daily_counts || []) : null);
</script>

<div class="activity-page">
	<header class="act-header">
		<div class="act-header-left">
			<button class="back-btn" onclick={() => goto(`/w/${slug}`)}>
				<svg width="16" height="16" viewBox="0 0 16 16" fill="none">
					<path d="M10 3L5 8l5 5" stroke="currentColor" stroke-width="1.5" stroke-linecap="round" stroke-linejoin="round"/>
				</svg>
			</button>
			<h1>Activity</h1>
		</div>
	</header>

	{#if loading}
		<div class="loading-state">Loading activity...</div>
	{:else}
		<!-- Stats Bar -->
		{#if stats?.type_counts}
			<div class="stats-bar">
				<span class="stat-item">{countByPrefix(stats.type_counts, 'message.')} messages</span>
				<span class="stat-sep">&middot;</span>
				<span class="stat-item">{stats.type_counts['task.completed'] || 0} tasks done</span>
				<span class="stat-sep">&middot;</span>
				<span class="stat-item">{countByPrefix(stats.type_counts, 'event.')} events</span>
				<span class="stat-sep">&middot;</span>
				<span class="stat-item">{countByPrefix(stats.type_counts, 'file.')} files</span>
				<span class="stat-sep">&middot;</span>
				<span class="stat-item">{countByPrefix(stats.type_counts, 'agent.')} agent actions</span>
			</div>
		{/if}

		<!-- Heatmap -->
		{#if heatmap}
			<div class="heatmap-container">
				<div class="heatmap-scroll">
					<svg width={((heatmap.days[heatmap.days.length - 1]?.week || 0) + 1) * 14 + 2} height="112" class="heatmap-svg">
						{#each heatmap.days as day}
							<rect
								x={day.week * 14}
								y={day.dow * 14 + 14}
								width="11"
								height="11"
								rx="2"
								class="heatmap-cell level-{heatLevel(day.count, heatmap.maxCount)}"
							>
								<title>{tooltipText(day.date, day.count)}</title>
							</rect>
						{/each}
						<!-- Month labels -->
						{#each ['Jan','Feb','Mar','Apr','May','Jun','Jul','Aug','Sep','Oct','Nov','Dec'] as _m, mi}
							{@const firstDay = heatmap.days.find(d => {
								const m = new Date(d.date + 'T00:00:00').getMonth();
								const day = new Date(d.date + 'T00:00:00').getDate();
								return m === mi && day <= 7 && d.dow === 0;
							})}
							{#if firstDay}
								<text x={firstDay.week * 14} y="10" class="heatmap-label">{_m}</text>
							{/if}
						{/each}
					</svg>
				</div>
				<div class="heatmap-footer">
					<span class="heatmap-total">{stats?.total || 0} activities in the last year</span>
					<div class="heatmap-legend">
						<span class="legend-label">Less</span>
						<span class="legend-cell level-0"></span>
						<span class="legend-cell level-1"></span>
						<span class="legend-cell level-2"></span>
						<span class="legend-cell level-3"></span>
						<span class="legend-cell level-4"></span>
						<span class="legend-label">More</span>
					</div>
				</div>
			</div>
		{/if}

		<!-- Filter Chips -->
		<div class="filter-bar">
			{#each FILTERS as f}
				<button
					class="filter-chip"
					class:active={activeFilter === f.key}
					onclick={() => setFilter(f.key)}
				>{f.label}</button>
			{/each}
		</div>

		<!-- Timeline -->
		<div class="timeline">
			{#if activities.length === 0}
				<div class="empty-state">No activity yet. Start collaborating!</div>
			{:else}
				{#each dayGroups as group}
					<div class="day-group">
						<div class="day-label">{group.label}</div>
						{#each group.items as item}
							<div class="timeline-item">
								<span class="tl-icon">{getIcon(item.pulse_type)}</span>
								<div class="tl-content">
									<span class="tl-summary">{item.summary}</span>
									{#if item.detail && !/^\d+$/.test(item.detail)}
										<span class="tl-detail">{item.detail}</span>
									{/if}
								</div>
								<span class="tl-time">{timeAgo(item.created_at)}</span>
							</div>
						{/each}
					</div>
				{/each}

				{#if hasMore}
					<button class="load-more" onclick={loadMore} disabled={loadingMore}>
						{loadingMore ? 'Loading...' : 'Load more'}
					</button>
				{/if}
			{/if}
		</div>
	{/if}
</div>

<style>
	.activity-page {
		display: flex;
		flex-direction: column;
		height: 100vh;
		background: var(--bg-root);
		color: var(--text-primary);
		overflow-y: auto;
	}

	.act-header {
		display: flex;
		align-items: center;
		justify-content: space-between;
		padding: 16px 24px;
		border-bottom: 1px solid var(--border-subtle);
		flex-shrink: 0;
	}

	.act-header-left {
		display: flex;
		align-items: center;
		gap: 12px;
	}

	.act-header h1 {
		font-size: var(--text-lg);
		font-weight: 600;
		margin: 0;
	}

	.back-btn {
		background: none;
		border: none;
		color: var(--text-secondary);
		cursor: pointer;
		padding: 4px;
		border-radius: var(--radius-md);
		display: flex;
		align-items: center;
	}
	.back-btn:hover {
		color: var(--text-primary);
		background: var(--bg-surface);
	}

	.loading-state, .empty-state {
		padding: 48px 24px;
		text-align: center;
		color: var(--text-tertiary);
	}

	/* Stats Bar */
	.stats-bar {
		display: flex;
		align-items: center;
		gap: 8px;
		padding: 12px 24px;
		font-size: var(--text-sm);
		color: var(--text-secondary);
		border-bottom: 1px solid var(--border-subtle);
		flex-wrap: wrap;
	}
	.stat-sep { opacity: 0.4; }

	/* Heatmap */
	.heatmap-container {
		padding: 16px 24px;
		border-bottom: 1px solid var(--border-subtle);
	}
	.heatmap-scroll {
		overflow-x: auto;
		padding-bottom: 4px;
	}
	.heatmap-svg {
		display: block;
	}
	.heatmap-label {
		font-size: 10px;
		fill: var(--text-tertiary);
	}
	.heatmap-cell.level-0 { fill: var(--bg-surface); }
	.heatmap-cell.level-1 { fill: color-mix(in srgb, var(--accent) 20%, var(--bg-surface)); }
	.heatmap-cell.level-2 { fill: color-mix(in srgb, var(--accent) 40%, var(--bg-surface)); }
	.heatmap-cell.level-3 { fill: color-mix(in srgb, var(--accent) 65%, var(--bg-surface)); }
	.heatmap-cell.level-4 { fill: var(--accent); }

	.heatmap-footer {
		display: flex;
		justify-content: space-between;
		align-items: center;
		margin-top: 8px;
		font-size: var(--text-xs);
		color: var(--text-tertiary);
	}
	.heatmap-legend {
		display: flex;
		align-items: center;
		gap: 3px;
	}
	.legend-label {
		margin: 0 4px;
	}
	.legend-cell {
		width: 11px;
		height: 11px;
		border-radius: 2px;
		display: inline-block;
	}
	.legend-cell.level-0 { background: var(--bg-surface); }
	.legend-cell.level-1 { background: color-mix(in srgb, var(--accent) 20%, var(--bg-surface)); }
	.legend-cell.level-2 { background: color-mix(in srgb, var(--accent) 40%, var(--bg-surface)); }
	.legend-cell.level-3 { background: color-mix(in srgb, var(--accent) 65%, var(--bg-surface)); }
	.legend-cell.level-4 { background: var(--accent); }

	/* Filter Bar */
	.filter-bar {
		display: flex;
		gap: 6px;
		padding: 12px 24px;
		border-bottom: 1px solid var(--border-subtle);
		flex-wrap: wrap;
	}
	.filter-chip {
		padding: 4px 12px;
		border-radius: 999px;
		border: 1px solid var(--border-default);
		background: transparent;
		color: var(--text-secondary);
		font-size: var(--text-xs);
		cursor: pointer;
		transition: all 0.15s;
	}
	.filter-chip:hover {
		border-color: var(--text-tertiary);
		color: var(--text-primary);
	}
	.filter-chip.active {
		background: var(--accent);
		border-color: var(--accent);
		color: var(--text-inverse);
	}

	/* Timeline */
	.timeline {
		flex: 1;
		padding: 0 24px 24px;
	}

	.day-group {
		margin-top: 20px;
	}
	.day-label {
		font-size: var(--text-sm);
		font-weight: 600;
		color: var(--text-secondary);
		padding-bottom: 8px;
		border-bottom: 1px solid var(--border-subtle);
		margin-bottom: 4px;
	}

	.timeline-item {
		display: flex;
		align-items: flex-start;
		gap: 10px;
		padding: 8px 0;
		border-bottom: 1px solid color-mix(in srgb, var(--border-subtle) 50%, transparent);
	}
	.timeline-item:last-child {
		border-bottom: none;
	}

	.tl-icon {
		font-size: 16px;
		flex-shrink: 0;
		width: 24px;
		text-align: center;
		line-height: 1.4;
	}

	.tl-content {
		flex: 1;
		min-width: 0;
		display: flex;
		flex-direction: column;
		gap: 2px;
	}
	.tl-summary {
		font-size: var(--text-sm);
		color: var(--text-primary);
	}
	.tl-detail {
		font-size: var(--text-xs);
		color: var(--text-tertiary);
	}

	.tl-time {
		font-size: var(--text-xs);
		color: var(--text-tertiary);
		white-space: nowrap;
		flex-shrink: 0;
	}

	.load-more {
		display: block;
		width: 100%;
		padding: 10px;
		margin-top: 16px;
		background: var(--bg-surface);
		border: 1px solid var(--border-default);
		border-radius: var(--radius-md);
		color: var(--text-secondary);
		cursor: pointer;
		font-size: var(--text-sm);
	}
	.load-more:hover:not(:disabled) {
		background: var(--bg-raised);
		color: var(--text-primary);
	}
	.load-more:disabled {
		opacity: 0.5;
		cursor: default;
	}
</style>
