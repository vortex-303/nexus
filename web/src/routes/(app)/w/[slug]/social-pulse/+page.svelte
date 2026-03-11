<script lang="ts">
	import { page } from '$app/state';
	import { goto } from '$app/navigation';
	import { onMount, onDestroy } from 'svelte';
	import { createSocialPulse, listSocialPulses, getSocialPulse, deleteSocialPulse } from '$lib/api';
	import { connect, disconnect, onMessage } from '$lib/ws';

	let slug = $derived(page.params.slug);
	let pulses = $state<any[]>([]);
	let loading = $state(true);
	let searchQuery = $state('');
	let searching = $state(false);
	let selectedPulse = $state<any>(null);
	let showSearch = $state(false);
	let mobileShowMain = $state(false);
	let unsubWS: (() => void) | null = null;
	let pollTimer: ReturnType<typeof setInterval> | null = null;

	onMount(async () => {
		connect();
		unsubWS = onMessage((type: string, payload: any) => {
			if (type === 'social_pulse.created') {
				pulses = [payload, ...pulses];
				selectedPulse = payload;
			} else if (type === 'social_pulse.updated') {
				pulses = pulses.map(p => p.id === payload.id ? { ...p, ...payload } : p);
				if (selectedPulse?.id === payload.id) {
					selectedPulse = { ...selectedPulse, ...payload };
				}
			} else if (type === 'social_pulse.deleted') {
				pulses = pulses.filter(p => p.id !== payload.id);
				if (selectedPulse?.id === payload.id) selectedPulse = null;
			}
		});
		await loadPulses();

		pollTimer = setInterval(async () => {
			if (selectedPulse && selectedPulse.status !== 'ready' && selectedPulse.status !== 'failed') {
				try {
					const fresh = await getSocialPulse(slug, selectedPulse.id);
					selectedPulse = fresh;
					pulses = pulses.map(p => p.id === fresh.id ? fresh : p);
				} catch {}
			}
		}, 5000);
	});

	onDestroy(() => {
		unsubWS?.();
		if (pollTimer) clearInterval(pollTimer);
		disconnect();
	});

	async function loadPulses() {
		loading = true;
		try {
			const data = await listSocialPulses(slug);
			pulses = data.pulses || [];
		} catch (e) { console.error(e); }
		loading = false;
	}

	async function selectPulse(pulse: any) {
		selectedPulse = pulse;
		mobileShowMain = true;
		if (pulse.status !== 'ready' && pulse.status !== 'failed') {
			try {
				const fresh = await getSocialPulse(slug, pulse.id);
				selectedPulse = fresh;
				pulses = pulses.map(p => p.id === fresh.id ? fresh : p);
			} catch {}
		}
	}

	async function handleSearch() {
		if (!searchQuery.trim() || searching) return;
		searching = true;
		try {
			const data = await createSocialPulse(slug, searchQuery.trim());
			selectedPulse = data;
			mobileShowMain = true;
			searchQuery = '';
			showSearch = false;
		} catch (e: any) {
			alert(e.message || 'Failed to create pulse');
		}
		searching = false;
	}

	async function handleDelete(id: string, e?: MouseEvent) {
		e?.stopPropagation();
		if (!confirm('Delete this pulse report?')) return;
		try {
			await deleteSocialPulse(slug, id);
		} catch (e) { console.error(e); }
	}

	function sentimentColor(score: number): string {
		if (score >= 65) return 'var(--green, #22c55e)';
		if (score >= 40) return 'var(--yellow, #eab308)';
		return 'var(--red, #ef4444)';
	}

	function sentimentLabel(score: number): string {
		if (score >= 75) return 'Very Positive';
		if (score >= 60) return 'Positive';
		if (score >= 45) return 'Neutral';
		if (score >= 30) return 'Negative';
		return 'Very Negative';
	}

	function statusLabel(status: string): string {
		switch (status) {
			case 'pending': return 'Queued...';
			case 'searching': return 'Searching X...';
			case 'analyzing': return 'Analyzing...';
			case 'ready': return 'Ready';
			case 'failed': return 'Failed';
			default: return status;
		}
	}

	function timeAgo(dateStr: string): string {
		const now = Date.now();
		const then = new Date(dateStr).getTime();
		const diff = Math.floor((now - then) / 1000);
		if (diff < 60) return 'just now';
		if (diff < 3600) return `${Math.floor(diff / 60)}m ago`;
		if (diff < 86400) return `${Math.floor(diff / 3600)}h ago`;
		return `${Math.floor(diff / 86400)}d ago`;
	}

	function parseJSON(raw: any): any[] {
		if (!raw) return [];
		if (Array.isArray(raw)) return raw;
		try { return JSON.parse(raw); } catch { return []; }
	}

	function gaugeArc(score: number): string {
		const angle = (score / 100) * 180;
		const rad = (angle - 180) * Math.PI / 180;
		const x = 100 + 70 * Math.cos(rad);
		const y = 90 + 70 * Math.sin(rad);
		const large = angle > 90 ? 1 : 0;
		return `M 30 90 A 70 70 0 ${large} 1 ${x.toFixed(1)} ${y.toFixed(1)}`;
	}
</script>

<div class="pulse-page" class:mobile-show-main={mobileShowMain}>
	<!-- Sidebar -->
	<div class="pulse-sidebar">
		<div class="pulse-sidebar-header">
			<button class="back-btn" onclick={() => goto(`/w/${slug}`)}>
				<svg width="16" height="16" viewBox="0 0 16 16" fill="none"><path d="M10 12L6 8l4-4" stroke="currentColor" stroke-width="1.5" stroke-linecap="round" stroke-linejoin="round"/></svg>
			</button>
			<h2>Social Pulse</h2>
			<button class="add-btn" onclick={() => { showSearch = !showSearch; }} title="New search">
				<svg width="16" height="16" viewBox="0 0 16 16" fill="none"><path d="M8 3v10M3 8h10" stroke="currentColor" stroke-width="1.5" stroke-linecap="round"/></svg>
			</button>
		</div>

		{#if showSearch}
			<div class="pulse-search">
				<input
					type="text"
					placeholder="Topic, brand, or trend..."
					bind:value={searchQuery}
					onkeydown={(e) => { if (e.key === 'Enter') handleSearch(); }}
					disabled={searching}
				/>
				<button class="search-go" onclick={handleSearch} disabled={searching || !searchQuery.trim()}>
					{#if searching}
						<span class="spinner"></span>
					{:else}
						<svg width="14" height="14" viewBox="0 0 16 16" fill="none"><path d="M14 14l-3.5-3.5M11 6.5a4.5 4.5 0 11-9 0 4.5 4.5 0 019 0z" stroke="currentColor" stroke-width="1.5" stroke-linecap="round"/></svg>
					{/if}
				</button>
			</div>
		{/if}

		<div class="pulse-list">
			{#if loading}
				<p class="list-empty">Loading...</p>
			{:else if pulses.length === 0}
				<p class="list-empty">No reports yet</p>
			{:else}
				{#each pulses as pulse}
					<!-- svelte-ignore a11y_click_events_have_key_events a11y_no_static_element_interactions -->
					<div
						class="pulse-item"
						class:active={selectedPulse?.id === pulse.id}
						onclick={() => selectPulse(pulse)}
						role="button"
						tabindex="0"
					>
						<div class="pulse-item-left">
							<span class="pulse-item-topic">{pulse.topic}</span>
							<span class="pulse-item-time">{timeAgo(pulse.created_at)}</span>
						</div>
						<div class="pulse-item-right">
							{#if pulse.status === 'ready'}
								<span class="score-badge" style="background: {sentimentColor(pulse.sentiment_score)}">{pulse.sentiment_score}</span>
							{:else if pulse.status === 'failed'}
								<span class="score-badge failed">!</span>
							{:else}
								<span class="spinner small"></span>
							{/if}
							<button class="delete-x" onclick={(e) => handleDelete(pulse.id, e)} title="Delete">
								<svg width="10" height="10" viewBox="0 0 10 10" fill="none"><path d="M2 2l6 6M8 2l-6 6" stroke="currentColor" stroke-width="1.2" stroke-linecap="round"/></svg>
							</button>
						</div>
					</div>
				{/each}
			{/if}
		</div>
	</div>

	<!-- Main content -->
	<div class="pulse-main">
		{#if selectedPulse}
			<div class="pulse-main-header">
				<button class="mobile-back" onclick={() => { mobileShowMain = false; }}>
					<svg width="16" height="16" viewBox="0 0 16 16" fill="none"><path d="M10 12L6 8l4-4" stroke="currentColor" stroke-width="1.5" stroke-linecap="round" stroke-linejoin="round"/></svg>
				</button>
				<h2>{selectedPulse.topic}</h2>
				<span class="header-status" class:ready={selectedPulse.status === 'ready'} class:failed={selectedPulse.status === 'failed'}>
					{#if selectedPulse.status !== 'ready' && selectedPulse.status !== 'failed'}
						<span class="spinner small"></span>
					{/if}
					{statusLabel(selectedPulse.status)}
				</span>
				<button class="header-delete" onclick={(e) => handleDelete(selectedPulse.id, e)} title="Delete report">
					<svg width="14" height="14" viewBox="0 0 14 14" fill="none"><path d="M3 3l8 8M11 3l-8 8" stroke="currentColor" stroke-width="1.3" stroke-linecap="round"/></svg>
				</button>
			</div>

			<div class="pulse-content">
				{#if selectedPulse.status !== 'ready' && selectedPulse.status !== 'failed'}
					<!-- In-progress status card -->
					<div class="pulse-card status-card">
						<span class="spinner"></span>
						<p>{statusLabel(selectedPulse.status)}</p>
					</div>
				{:else if selectedPulse.status === 'failed'}
					<div class="pulse-card error-card">
						<p>{selectedPulse.summary || 'Analysis failed'}</p>
					</div>
				{:else}
					<!-- Sentiment + Summary row -->
					<div class="sentiment-summary-row">
						<div class="pulse-card gauge-card">
							<h3>Sentiment</h3>
							<svg viewBox="0 0 200 110" class="gauge">
								<path d="M 30 90 A 70 70 0 0 1 170 90" fill="none" stroke="var(--border-subtle, #333)" stroke-width="12" stroke-linecap="round"/>
								<path d={gaugeArc(selectedPulse.sentiment_score)} fill="none" stroke={sentimentColor(selectedPulse.sentiment_score)} stroke-width="12" stroke-linecap="round"/>
								<text x="100" y="80" text-anchor="middle" fill="currentColor" font-size="28" font-weight="bold">{selectedPulse.sentiment_score}</text>
								<text x="100" y="100" text-anchor="middle" fill="var(--text-secondary, #888)" font-size="12">{sentimentLabel(selectedPulse.sentiment_score)}</text>
							</svg>
						</div>
						<div class="pulse-card summary-card">
							<h3>Summary</h3>
							<p>{selectedPulse.summary}</p>
						</div>
					</div>

					<!-- Themes -->
					{#if parseJSON(selectedPulse.themes).length > 0}
						<div class="pulse-card">
							<h3>Themes</h3>
							<div class="themes-chart">
								{#each parseJSON(selectedPulse.themes) as theme}
									<div class="theme-row">
										<span class="theme-name">{theme.name}</span>
										<div class="theme-bar-wrap">
											<div
												class="theme-bar"
												style="width: {Math.min(100, (theme.count / Math.max(...parseJSON(selectedPulse.themes).map((t: any) => t.count), 1)) * 100)}%; background: {theme.sentiment === 'positive' ? 'var(--green, #22c55e)' : theme.sentiment === 'negative' ? 'var(--red, #ef4444)' : 'var(--yellow, #eab308)'}"
											></div>
										</div>
										<span class="theme-count">{theme.count}</span>
									</div>
								{/each}
							</div>
						</div>
					{/if}

					<!-- Key Posts -->
					{#if parseJSON(selectedPulse.key_posts).length > 0}
						<div class="pulse-card">
							<h3>Key Posts</h3>
							<div class="posts-list">
								{#each parseJSON(selectedPulse.key_posts) as post}
									<div class="post-row">
										<span class="sentiment-dot" style="background: {post.sentiment === 'positive' ? 'var(--green, #22c55e)' : post.sentiment === 'negative' ? 'var(--red, #ef4444)' : 'var(--yellow, #eab308)'}"></span>
										<div class="post-body">
											<p class="post-text">{post.text}</p>
											{#if post.author}
												<span class="post-author">{post.author}</span>
											{/if}
										</div>
									</div>
								{/each}
							</div>
						</div>
					{/if}

					<!-- Recommendations -->
					{#if selectedPulse.recommendations}
						<div class="pulse-card">
							<h3>Recommendations</h3>
							<p>{selectedPulse.recommendations}</p>
						</div>
					{/if}

					<!-- Sources -->
					{#if parseJSON(selectedPulse.citations).length > 0}
						<div class="pulse-card">
							<h3>Sources</h3>
							<div class="sources-list">
								{#each parseJSON(selectedPulse.citations) as url}
									<a href={url} target="_blank" rel="noopener">{url}</a>
								{/each}
							</div>
						</div>
					{/if}
				{/if}
			</div>
		{:else}
			<!-- Empty state -->
			<div class="empty-state">
				<svg width="48" height="48" viewBox="0 0 48 48" fill="none" opacity="0.4">
					<circle cx="24" cy="24" r="20" stroke="currentColor" stroke-width="1.5"/>
					<path d="M16 28c2 3 5 5 8 5s6-2 8-5" stroke="currentColor" stroke-width="1.5" stroke-linecap="round"/>
					<circle cx="18" cy="20" r="2" fill="currentColor"/>
					<circle cx="30" cy="20" r="2" fill="currentColor"/>
				</svg>
				<p class="empty-title">Social Pulse</p>
				<p class="empty-sub">Search a topic to get started</p>
			</div>
		{/if}
	</div>
</div>

<style>
	/* Two-column layout */
	.pulse-page {
		display: flex;
		height: 100vh;
		background: var(--bg-root);
	}

	/* ---- Sidebar ---- */
	.pulse-sidebar {
		width: 280px;
		min-width: 280px;
		background: var(--bg-surface);
		border-right: 1px solid var(--border-subtle);
		display: flex;
		flex-direction: column;
		overflow: hidden;
	}

	.pulse-sidebar-header {
		display: flex;
		align-items: center;
		gap: var(--space-sm);
		padding: var(--space-md) var(--space-md);
		border-bottom: 1px solid var(--border-subtle);
	}

	.pulse-sidebar-header h2 {
		font-size: var(--text-base);
		font-weight: 700;
		margin: 0;
		flex: 1;
	}

	.back-btn, .add-btn {
		background: none;
		border: none;
		color: var(--text-secondary);
		cursor: pointer;
		padding: 4px;
		border-radius: var(--radius-sm, 4px);
		display: flex;
		align-items: center;
		justify-content: center;
	}
	.back-btn:hover, .add-btn:hover { color: var(--text-primary); }

	/* Search bar */
	.pulse-search {
		display: flex;
		gap: var(--space-xs);
		padding: var(--space-sm) var(--space-md);
		border-bottom: 1px solid var(--border-subtle);
	}

	.pulse-search input {
		flex: 1;
		background: var(--bg-root);
		border: 1px solid var(--border-subtle);
		border-radius: var(--radius-md);
		padding: 6px 10px;
		color: var(--text-primary);
		font-size: var(--text-sm);
		min-width: 0;
	}
	.pulse-search input::placeholder { color: var(--text-tertiary); }
	.pulse-search input:focus { outline: none; border-color: var(--accent); }

	.search-go {
		background: var(--accent);
		color: #fff;
		border: none;
		border-radius: var(--radius-md);
		padding: 6px 10px;
		cursor: pointer;
		display: flex;
		align-items: center;
		justify-content: center;
	}
	.search-go:hover { opacity: 0.9; }
	.search-go:disabled { opacity: 0.5; cursor: not-allowed; }

	/* Pulse list */
	.pulse-list {
		flex: 1;
		overflow-y: auto;
		padding: var(--space-xs) 0;
	}

	.list-empty {
		padding: var(--space-lg) var(--space-md);
		color: var(--text-tertiary);
		font-size: var(--text-sm);
		text-align: center;
		margin: 0;
	}

	.pulse-item {
		display: flex;
		align-items: center;
		justify-content: space-between;
		gap: var(--space-xs);
		width: calc(100% - 12px);
		margin: 1px 6px;
		padding: 8px var(--space-md);
		border-radius: var(--radius-md);
		font-size: var(--text-base);
		color: var(--text-secondary);
		text-align: left;
		background: none;
		border: 1px solid transparent;
		cursor: pointer;
	}
	.pulse-item:hover {
		background: var(--bg-raised);
		color: var(--text-primary);
	}
	.pulse-item.active {
		background: var(--accent-glow);
		color: var(--accent);
		border-color: var(--accent-border);
	}

	.pulse-item-left {
		display: flex;
		flex-direction: column;
		gap: 2px;
		min-width: 0;
		flex: 1;
	}

	.pulse-item-topic {
		font-size: var(--text-sm);
		font-weight: 500;
		overflow: hidden;
		text-overflow: ellipsis;
		white-space: nowrap;
		color: inherit;
	}

	.pulse-item-time {
		font-size: var(--text-xs);
		color: var(--text-tertiary);
	}

	.pulse-item-right {
		display: flex;
		align-items: center;
		gap: var(--space-xs);
		flex-shrink: 0;
	}

	.score-badge {
		font-size: 11px;
		font-weight: 700;
		color: #fff;
		min-width: 18px;
		height: 18px;
		line-height: 18px;
		text-align: center;
		border-radius: 9px;
		padding: 0 5px;
	}
	.score-badge.failed { background: var(--red, #ef4444); }

	.delete-x {
		background: none;
		border: none;
		color: var(--text-tertiary);
		cursor: pointer;
		padding: 2px;
		border-radius: 4px;
		opacity: 0;
		display: flex;
		align-items: center;
	}
	.pulse-item:hover .delete-x { opacity: 1; }
	.delete-x:hover { color: var(--red, #ef4444); }

	/* ---- Main area ---- */
	.pulse-main {
		flex: 1;
		display: flex;
		flex-direction: column;
		min-width: 0;
		background: var(--bg-root);
	}

	.pulse-main-header {
		padding: var(--space-md) var(--space-xl);
		border-bottom: 1px solid var(--border-subtle);
		display: flex;
		align-items: center;
		gap: var(--space-sm);
		background: var(--bg-surface);
	}

	.pulse-main-header h2 {
		font-size: var(--text-lg);
		font-weight: 700;
		margin: 0;
		flex: 1;
		overflow: hidden;
		text-overflow: ellipsis;
		white-space: nowrap;
	}

	.mobile-back {
		display: none;
		background: none;
		border: none;
		color: var(--text-secondary);
		cursor: pointer;
		padding: 4px;
		border-radius: var(--radius-sm, 4px);
		align-items: center;
		justify-content: center;
	}
	.mobile-back:hover { color: var(--text-primary); }

	.header-status {
		font-size: var(--text-xs);
		color: var(--text-tertiary);
		display: flex;
		align-items: center;
		gap: var(--space-xs);
		flex-shrink: 0;
	}
	.header-status.ready { color: var(--green, #22c55e); }
	.header-status.failed { color: var(--red, #ef4444); }

	.header-delete {
		background: none;
		border: none;
		color: var(--text-tertiary);
		cursor: pointer;
		padding: 4px;
		border-radius: var(--radius-sm, 4px);
		display: flex;
		align-items: center;
		flex-shrink: 0;
	}
	.header-delete:hover { color: var(--red, #ef4444); }

	/* Content area */
	.pulse-content {
		flex: 1;
		overflow-y: auto;
		padding: var(--space-lg) var(--space-xl);
		display: flex;
		flex-direction: column;
		gap: var(--space-md);
	}

	/* Cards — thread-style */
	.pulse-card {
		background: var(--bg-surface);
		border: 1px solid var(--border-subtle);
		border-radius: var(--radius-md);
		padding: var(--space-lg);
	}

	.pulse-card h3 {
		font-size: var(--text-xs);
		text-transform: uppercase;
		letter-spacing: 0.05em;
		color: var(--text-tertiary);
		margin: 0 0 var(--space-sm) 0;
	}

	.pulse-card p {
		margin: 0;
		line-height: 1.5;
		font-size: var(--text-sm);
		color: var(--text-secondary);
	}

	/* Status card */
	.status-card {
		display: flex;
		align-items: center;
		gap: var(--space-md);
	}
	.status-card p { color: var(--text-tertiary); }

	/* Error card */
	.error-card {
		border-color: var(--red, #ef4444);
	}
	.error-card p { color: var(--red, #ef4444); }

	/* Sentiment + Summary side-by-side */
	.sentiment-summary-row {
		display: grid;
		grid-template-columns: auto 1fr;
		gap: var(--space-md);
	}

	.gauge-card {
		display: flex;
		flex-direction: column;
		align-items: center;
	}
	.gauge { width: 160px; height: auto; }

	.summary-card {
		display: flex;
		flex-direction: column;
	}

	/* Themes */
	.themes-chart { display: flex; flex-direction: column; gap: 8px; }
	.theme-row { display: flex; align-items: center; gap: 8px; }
	.theme-name { width: 120px; font-size: var(--text-xs); text-align: right; flex-shrink: 0; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; color: var(--text-secondary); }
	.theme-bar-wrap { flex: 1; height: 20px; background: var(--bg-root); border-radius: 4px; overflow: hidden; }
	.theme-bar { height: 100%; border-radius: 4px; transition: width 0.3s ease; min-width: 4px; }
	.theme-count { font-size: var(--text-xs); color: var(--text-tertiary); width: 24px; text-align: right; }

	/* Key posts — message-row style */
	.posts-list { display: flex; flex-direction: column; gap: var(--space-sm); }
	.post-row {
		display: flex;
		gap: var(--space-sm);
		align-items: flex-start;
		padding: var(--space-xs) var(--space-sm);
		border-radius: var(--radius-md);
	}
	.post-row:hover { background: var(--bg-raised); }
	.sentiment-dot { width: 8px; height: 8px; border-radius: 50%; flex-shrink: 0; margin-top: 6px; }
	.post-body { flex: 1; min-width: 0; }
	.post-text { font-size: var(--text-sm); line-height: 1.5; margin: 0 0 2px 0; color: var(--text-secondary); }
	.post-author { font-size: var(--text-xs); color: var(--accent); }

	/* Sources */
	.sources-list { display: flex; flex-direction: column; gap: 4px; }
	.sources-list a {
		font-size: var(--text-xs);
		color: var(--accent);
		text-decoration: none;
		overflow: hidden;
		text-overflow: ellipsis;
		white-space: nowrap;
	}
	.sources-list a:hover { text-decoration: underline; }

	/* Empty state */
	.empty-state {
		flex: 1;
		display: flex;
		flex-direction: column;
		align-items: center;
		justify-content: center;
		gap: var(--space-md);
		padding: var(--space-xl);
	}

	.empty-title {
		font-size: var(--text-lg);
		font-weight: 600;
		color: var(--text-secondary);
		margin: 0;
	}

	.empty-sub {
		font-size: var(--text-base);
		color: var(--text-tertiary);
		margin: 0;
	}

	/* Spinner */
	.spinner {
		display: inline-block;
		width: 16px;
		height: 16px;
		border: 2px solid transparent;
		border-top-color: currentColor;
		border-radius: 50%;
		animation: spin 0.6s linear infinite;
		flex-shrink: 0;
	}
	.spinner.small { width: 12px; height: 12px; border-width: 1.5px; }
	@keyframes spin { to { transform: rotate(360deg); } }

	/* ---- Mobile ---- */
	@media (max-width: 640px) {
		.pulse-sidebar {
			width: 100%;
			min-width: 100%;
		}

		.pulse-main {
			display: none;
		}

		.pulse-page.mobile-show-main .pulse-sidebar {
			display: none;
		}

		.pulse-page.mobile-show-main .pulse-main {
			display: flex;
		}

		.mobile-back {
			display: flex;
		}

		.sentiment-summary-row {
			grid-template-columns: 1fr;
		}
	}
</style>
