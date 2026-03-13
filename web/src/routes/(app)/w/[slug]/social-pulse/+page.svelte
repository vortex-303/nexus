<script lang="ts">
	import { page } from '$app/state';
	import { goto } from '$app/navigation';
	import { onMount, onDestroy } from 'svelte';
	import { createSocialPulse, listSocialPulses, getSocialPulse, deleteSocialPulse, createDoc, createKnowledge } from '$lib/api';
	import { connect, disconnect, onMessage } from '$lib/ws';
	import { markdownToHtml } from '$lib/editor/markdown-utils';

	let slug = $derived(page.params.slug);
	let pulses = $state<any[]>([]);
	let loading = $state(true);
	let searchQuery = $state('');
	let searching = $state(false);
	let selectedPulse = $state<any>(null);
	let showSearch = $state(false);
	let mobileShowMain = $state(false);
	let saving = $state(false);
	let savingKB = $state(false);
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

	async function saveReport() {
		if (!selectedPulse || selectedPulse.status !== 'ready' || saving) return;
		saving = true;
		try {
			const p = selectedPulse;
			const date = new Date(p.created_at).toLocaleDateString('en-US', { year: 'numeric', month: 'long', day: 'numeric' });
			const md = buildReportMarkdown(p);
			await createDoc(slug, { title: `Pulse: ${p.topic} (${date})`, content: markdownToHtml(md) });
			alert('Saved to Documents');
		} catch (e: any) {
			alert(e.message || 'Failed to save');
		}
		saving = false;
	}

	function buildReportMarkdown(p: any): string {
		const date = new Date(p.created_at).toLocaleDateString('en-US', { year: 'numeric', month: 'long', day: 'numeric' });
		const themes = parseJSON(p.themes);
		const posts = parseJSON(p.key_posts);
		const sources = parseJSON(p.citations);
		const predictions = parseJSON(p.predictions);
		const risks = parseJSON(p.risks);
		const competitive = parseJSON(p.competitive_mentions);
		const audience = parseJSONObj(p.audience_breakdown);
		const srcBreakdown = parseJSONObj(p.source_breakdown);

		let md = `# Social Pulse: ${p.topic}\n\n`;
		md += `**Date:** ${date}  \n`;
		md += `**Sentiment:** ${p.sentiment_score}/100 — ${sentimentLabel(p.sentiment_score)}\n\n`;
		md += `---\n\n`;
		md += `## Summary\n\n${p.summary}\n\n`;

		if (themes.length > 0) {
			md += `## Themes\n\n`;
			md += `| Theme | Mentions | Sentiment | Description |\n|-------|----------|-----------|-------------|\n`;
			for (const t of themes) {
				md += `| ${t.name} | ${t.count} | ${t.sentiment} | ${t.description || ''} |\n`;
			}
			md += `\n`;
		}

		if (posts.length > 0) {
			md += `## Key Posts\n\n`;
			for (const post of posts) {
				const dot = post.sentiment === 'positive' ? '🟢' : post.sentiment === 'negative' ? '🔴' : '🟡';
				md += `${dot} ${post.text}`;
				if (post.author) md += ` — *${post.author}*`;
				if (post.source_type) md += ` [${post.source_type}]`;
				md += `\n\n`;
			}
		}

		if (predictions.length > 0) {
			md += `## Predictions\n\n`;
			for (const pred of predictions) {
				md += `- **${pred.prediction}** (${pred.confidence} confidence, ${pred.timeframe})\n  Basis: ${pred.basis}\n\n`;
			}
		}

		if (risks.length > 0) {
			md += `## Risks & Threats\n\n`;
			for (const r of risks) {
				md += `- **${r.risk}** (${r.severity} severity)\n  Evidence: ${r.evidence}\n\n`;
			}
		}

		if (competitive.length > 0) {
			md += `## Competitive Landscape\n\n`;
			md += `| Competitor | Sentiment | Context |\n|------------|-----------|----------|\n`;
			for (const c of competitive) {
				md += `| ${c.competitor} | ${c.sentiment} | ${c.context} |\n`;
			}
			md += `\n`;
		}

		if (audience.advocates || audience.critics || audience.neutral) {
			md += `## Audience Breakdown\n\n`;
			if (audience.advocates) md += `**Advocates:** ${audience.advocates}\n\n`;
			if (audience.critics) md += `**Critics:** ${audience.critics}\n\n`;
			if (audience.neutral) md += `**Neutral:** ${audience.neutral}\n\n`;
		}

		if (Object.keys(srcBreakdown).length > 0) {
			md += `## Source Breakdown\n\n`;
			if (srcBreakdown.x_posts) md += `- X/Twitter: ${srcBreakdown.x_posts}\n`;
			if (srcBreakdown.news_articles) md += `- News: ${srcBreakdown.news_articles}\n`;
			if (srcBreakdown.blogs) md += `- Blogs: ${srcBreakdown.blogs}\n`;
			if (srcBreakdown.forums) md += `- Forums: ${srcBreakdown.forums}\n`;
			if (srcBreakdown.other) md += `- Other: ${srcBreakdown.other}\n`;
			md += `\n`;
		}

		if (p.recommendations) {
			md += `## Recommendations\n\n${p.recommendations}\n\n`;
		}

		if (sources.length > 0) {
			md += `## Sources\n\n`;
			for (const url of sources) {
				md += `- ${url}\n`;
			}
		}

		return md.trim();
	}

	async function saveToKnowledge() {
		if (!selectedPulse || selectedPulse.status !== 'ready' || savingKB) return;
		savingKB = true;
		try {
			const p = selectedPulse;
			const date = new Date(p.created_at).toLocaleDateString('en-US', { year: 'numeric', month: 'long', day: 'numeric' });
			const md = buildReportMarkdown(p);
			await createKnowledge(slug, { title: `Pulse: ${p.topic} (${date})`, content: md });
			alert('Saved to Knowledge Base');
		} catch (e: any) {
			alert(e.message || 'Failed to save');
		}
		savingKB = false;
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

	function parseJSONObj(raw: any): Record<string, any> {
		if (!raw) return {};
		if (typeof raw === 'object' && !Array.isArray(raw)) return raw;
		try { return JSON.parse(raw); } catch { return {}; }
	}

	function statusLabel(status: string): string {
		switch (status) {
			case 'pending': return 'Queued...';
			case 'searching': return 'Searching X...';
			case 'searching_web': return 'Searching the web...';
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
								<span class="failed-icon">!</span>
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
				{#if selectedPulse.status === 'ready'}
					<button class="header-action save-btn" onclick={saveReport} disabled={saving} title="Save to Documents">
						{#if saving}
							<span class="spinner small"></span>
						{:else}
							<svg width="14" height="14" viewBox="0 0 16 16" fill="none"><path d="M3 14V2h8l2 2v10H3z" stroke="currentColor" stroke-width="1.3" stroke-linejoin="round"/><path d="M5 2v4h4V2" stroke="currentColor" stroke-width="1.3"/><path d="M5 10h6M5 12h4" stroke="currentColor" stroke-width="1.3" stroke-linecap="round"/></svg>
						{/if}
					</button>
					<button class="header-action save-btn" onclick={saveToKnowledge} disabled={savingKB} title="Save to Knowledge Base">
						{#if savingKB}
							<span class="spinner small"></span>
						{:else}
							<svg width="14" height="14" viewBox="0 0 16 16" fill="none"><path d="M8 1.5l1.8 3.7 4.1.6-3 2.9.7 4-3.6-1.9-3.6 1.9.7-4-3-2.9 4.1-.6L8 1.5z" stroke="currentColor" stroke-width="1.3" stroke-linejoin="round" fill="none"/></svg>
						{/if}
					</button>
				{/if}
				<button class="header-action delete-btn" onclick={(e) => handleDelete(selectedPulse.id, e)} title="Delete report">
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
					<!-- Sentiment + Summary — side by side -->
					<div class="pulse-row">
						<div class="pulse-card sentiment-bar-card">
							<h3>Sentiment</h3>
							<div class="sentiment-score-row">
								<span class="sentiment-number" style="color: {sentimentColor(selectedPulse.sentiment_score)}">{selectedPulse.sentiment_score}</span>
								<div class="sentiment-info">
									<span class="sentiment-label" style="color: {sentimentColor(selectedPulse.sentiment_score)}">{sentimentLabel(selectedPulse.sentiment_score)}</span>
									<div class="sentiment-track">
										<div class="sentiment-fill" style="width: {selectedPulse.sentiment_score}%; background: {sentimentColor(selectedPulse.sentiment_score)}"></div>
									</div>
								</div>
							</div>
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

					<!-- Predictions -->
					{#if parseJSON(selectedPulse.predictions).length > 0}
						<div class="pulse-card">
							<h3>Predictions</h3>
							<div class="predictions-list">
								{#each parseJSON(selectedPulse.predictions) as pred}
									<div class="prediction-row">
										<span class="confidence-badge" class:high={pred.confidence === 'high'} class:medium={pred.confidence === 'medium'} class:low={pred.confidence === 'low'}>{pred.confidence}</span>
										<div class="prediction-body">
											<p class="prediction-text">{pred.prediction}</p>
											<div class="prediction-meta">
												{#if pred.timeframe}<span class="timeframe-tag">{pred.timeframe}</span>{/if}
												<span class="prediction-basis">{pred.basis}</span>
											</div>
										</div>
									</div>
								{/each}
							</div>
						</div>
					{/if}

					<!-- Risks & Threats -->
					{#if parseJSON(selectedPulse.risks).length > 0}
						<div class="pulse-card">
							<h3>Risks & Threats</h3>
							<div class="risks-list">
								{#each parseJSON(selectedPulse.risks) as risk}
									<div class="risk-row">
										<span class="severity-badge" class:high={risk.severity === 'high'} class:medium={risk.severity === 'medium'} class:low={risk.severity === 'low'}>{risk.severity}</span>
										<div class="risk-body">
											<p class="risk-text">{risk.risk}</p>
											<p class="risk-evidence">{risk.evidence}</p>
										</div>
									</div>
								{/each}
							</div>
						</div>
					{/if}

					<!-- Competitive Landscape -->
					{#if parseJSON(selectedPulse.competitive_mentions).length > 0}
						<div class="pulse-card">
							<h3>Competitive Landscape</h3>
							<div class="competitive-table">
								<div class="comp-header">
									<span>Competitor</span>
									<span>Sentiment</span>
									<span>Context</span>
								</div>
								{#each parseJSON(selectedPulse.competitive_mentions) as comp}
									<div class="comp-row">
										<span class="comp-name">{comp.competitor}</span>
										<span class="sentiment-dot" style="background: {comp.sentiment === 'positive' ? 'var(--green, #22c55e)' : comp.sentiment === 'negative' ? 'var(--red, #ef4444)' : 'var(--yellow, #eab308)'}"></span>
										<span class="comp-context">{comp.context}</span>
									</div>
								{/each}
							</div>
						</div>
					{/if}

					<!-- Audience Breakdown -->
					{@const audience = parseJSONObj(selectedPulse.audience_breakdown)}
					{#if audience.advocates || audience.critics || audience.neutral}
						<div class="pulse-card">
							<h3>Audience Breakdown</h3>
							<div class="audience-grid">
								{#if audience.advocates}
									<div class="audience-col advocates">
										<span class="audience-label">Advocates</span>
										<p>{audience.advocates}</p>
									</div>
								{/if}
								{#if audience.critics}
									<div class="audience-col critics">
										<span class="audience-label">Critics</span>
										<p>{audience.critics}</p>
									</div>
								{/if}
								{#if audience.neutral}
									<div class="audience-col neutral-col">
										<span class="audience-label">Neutral</span>
										<p>{audience.neutral}</p>
									</div>
								{/if}
							</div>
						</div>
					{/if}

					<!-- Source Breakdown -->
					{@const srcBreakdown = parseJSONObj(selectedPulse.source_breakdown)}
					{@const srcTotal = (srcBreakdown.x_posts || 0) + (srcBreakdown.news_articles || 0) + (srcBreakdown.blogs || 0) + (srcBreakdown.forums || 0) + (srcBreakdown.other || 0)}
					{#if srcTotal > 0}
						<div class="pulse-card">
							<h3>Source Breakdown</h3>
							<div class="source-bar">
								{#if srcBreakdown.x_posts > 0}
									<div class="source-segment x" style="width: {(srcBreakdown.x_posts / srcTotal) * 100}%" title="X: {srcBreakdown.x_posts}"></div>
								{/if}
								{#if srcBreakdown.news_articles > 0}
									<div class="source-segment news" style="width: {(srcBreakdown.news_articles / srcTotal) * 100}%" title="News: {srcBreakdown.news_articles}"></div>
								{/if}
								{#if srcBreakdown.blogs > 0}
									<div class="source-segment blogs" style="width: {(srcBreakdown.blogs / srcTotal) * 100}%" title="Blogs: {srcBreakdown.blogs}"></div>
								{/if}
								{#if srcBreakdown.forums > 0}
									<div class="source-segment forums" style="width: {(srcBreakdown.forums / srcTotal) * 100}%" title="Forums: {srcBreakdown.forums}"></div>
								{/if}
								{#if srcBreakdown.other > 0}
									<div class="source-segment other" style="width: {(srcBreakdown.other / srcTotal) * 100}%" title="Other: {srcBreakdown.other}"></div>
								{/if}
							</div>
							<div class="source-legend">
								{#if srcBreakdown.x_posts > 0}<span class="legend-item"><span class="legend-dot x"></span>X ({srcBreakdown.x_posts})</span>{/if}
								{#if srcBreakdown.news_articles > 0}<span class="legend-item"><span class="legend-dot news"></span>News ({srcBreakdown.news_articles})</span>{/if}
								{#if srcBreakdown.blogs > 0}<span class="legend-item"><span class="legend-dot blogs"></span>Blogs ({srcBreakdown.blogs})</span>{/if}
								{#if srcBreakdown.forums > 0}<span class="legend-item"><span class="legend-dot forums"></span>Forums ({srcBreakdown.forums})</span>{/if}
								{#if srcBreakdown.other > 0}<span class="legend-item"><span class="legend-dot other"></span>Other ({srcBreakdown.other})</span>{/if}
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
		min-width: 18px;
		height: 18px;
		line-height: 18px;
		text-align: center;
		border-radius: 9px;
		padding: 0 5px;
		color: var(--bg-root);
		flex-shrink: 0;
	}

	.failed-icon {
		font-size: 11px;
		font-weight: 700;
		min-width: 18px;
		height: 18px;
		line-height: 18px;
		text-align: center;
		border-radius: 9px;
		padding: 0 5px;
		background: var(--red, #ef4444);
		color: var(--bg-root);
		flex-shrink: 0;
	}

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

	.header-action {
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
	.header-action:disabled { opacity: 0.5; cursor: not-allowed; }
	.save-btn:hover { color: var(--accent); }
	.delete-btn:hover { color: var(--red, #ef4444); }

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

	/* Side-by-side row */
	.pulse-row {
		display: flex;
		gap: var(--space-md);
	}

	.pulse-row .sentiment-bar-card {
		width: 200px;
		flex-shrink: 0;
	}

	.pulse-row .summary-card {
		flex: 1;
		min-width: 0;
	}

	/* Sentiment bar */
	.sentiment-bar-card { padding: var(--space-md) var(--space-lg); }

	.sentiment-score-row {
		display: flex;
		align-items: center;
		gap: var(--space-md);
	}

	.sentiment-number {
		font-size: 32px;
		font-weight: 700;
		line-height: 1;
		flex-shrink: 0;
	}

	.sentiment-info {
		flex: 1;
		display: flex;
		flex-direction: column;
		gap: var(--space-xs);
	}

	.sentiment-label {
		font-size: var(--text-sm);
		font-weight: 600;
	}

	.sentiment-track {
		height: 6px;
		background: var(--bg-root);
		border-radius: 3px;
		overflow: hidden;
	}

	.sentiment-fill {
		height: 100%;
		border-radius: 3px;
		transition: width 0.4s ease;
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

	/* Predictions */
	.predictions-list, .risks-list { display: flex; flex-direction: column; gap: var(--space-sm); }
	.prediction-row, .risk-row { display: flex; gap: var(--space-sm); align-items: flex-start; padding: var(--space-xs) var(--space-sm); border-radius: var(--radius-md); }
	.prediction-row:hover, .risk-row:hover { background: var(--bg-raised); }
	.prediction-body, .risk-body { flex: 1; min-width: 0; }
	.prediction-text, .risk-text { font-size: var(--text-sm); line-height: 1.5; margin: 0 0 4px 0; color: var(--text-secondary); }
	.prediction-meta { display: flex; align-items: center; gap: var(--space-sm); flex-wrap: wrap; }
	.timeframe-tag { font-size: var(--text-xs); background: var(--bg-raised); padding: 2px 8px; border-radius: 10px; color: var(--text-tertiary); }
	.prediction-basis { font-size: var(--text-xs); color: var(--text-tertiary); }
	.risk-evidence { font-size: var(--text-xs); color: var(--text-tertiary); margin: 0; }

	.confidence-badge, .severity-badge {
		font-size: 10px; font-weight: 600; text-transform: uppercase; letter-spacing: 0.03em;
		padding: 2px 8px; border-radius: 10px; flex-shrink: 0; margin-top: 2px;
		background: var(--bg-raised); color: var(--text-tertiary);
	}
	.confidence-badge.high, .severity-badge.high { background: rgba(34,197,94,0.15); color: var(--green, #22c55e); }
	:global(.severity-badge.high) { background: rgba(239,68,68,0.15); color: var(--red, #ef4444); }
	.confidence-badge.medium, .severity-badge.medium { background: rgba(234,179,8,0.15); color: var(--yellow, #eab308); }
	.confidence-badge.low { background: var(--bg-raised); color: var(--text-tertiary); }
	.severity-badge.low { background: var(--bg-raised); color: var(--text-tertiary); }

	/* Competitive table */
	.competitive-table { display: flex; flex-direction: column; gap: 2px; }
	.comp-header { display: grid; grid-template-columns: 140px 32px 1fr; gap: var(--space-sm); padding: var(--space-xs) var(--space-sm); font-size: var(--text-xs); color: var(--text-tertiary); text-transform: uppercase; letter-spacing: 0.05em; }
	.comp-row { display: grid; grid-template-columns: 140px 32px 1fr; gap: var(--space-sm); align-items: center; padding: var(--space-xs) var(--space-sm); border-radius: var(--radius-md); }
	.comp-row:hover { background: var(--bg-raised); }
	.comp-name { font-size: var(--text-sm); font-weight: 500; color: var(--text-primary); overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
	.comp-context { font-size: var(--text-sm); color: var(--text-secondary); }

	/* Audience breakdown */
	.audience-grid { display: grid; grid-template-columns: repeat(auto-fit, minmax(180px, 1fr)); gap: var(--space-md); }
	.audience-col { padding: var(--space-md); border-radius: var(--radius-md); background: var(--bg-root); }
	.audience-col p { font-size: var(--text-sm); line-height: 1.5; margin: 0; color: var(--text-secondary); }
	.audience-label { font-size: var(--text-xs); font-weight: 600; text-transform: uppercase; letter-spacing: 0.05em; display: block; margin-bottom: var(--space-xs); }
	.audience-col.advocates .audience-label { color: var(--green, #22c55e); }
	.audience-col.critics .audience-label { color: var(--red, #ef4444); }
	.audience-col.neutral-col .audience-label { color: var(--text-tertiary); }

	/* Source breakdown */
	.source-bar { display: flex; height: 24px; border-radius: 6px; overflow: hidden; margin-bottom: var(--space-sm); }
	.source-segment { min-width: 4px; transition: width 0.3s ease; }
	.source-segment.x { background: #1d9bf0; }
	.source-segment.news { background: var(--accent, #f97316); }
	.source-segment.blogs { background: var(--green, #22c55e); }
	.source-segment.forums { background: #a855f7; }
	.source-segment.other { background: var(--text-tertiary); }
	.source-legend { display: flex; gap: var(--space-md); flex-wrap: wrap; }
	.legend-item { display: flex; align-items: center; gap: 4px; font-size: var(--text-xs); color: var(--text-secondary); }
	.legend-dot { width: 8px; height: 8px; border-radius: 50%; flex-shrink: 0; }
	.legend-dot.x { background: #1d9bf0; }
	.legend-dot.news { background: var(--accent, #f97316); }
	.legend-dot.blogs { background: var(--green, #22c55e); }
	.legend-dot.forums { background: #a855f7; }
	.legend-dot.other { background: var(--text-tertiary); }

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

		.pulse-row {
			flex-direction: column;
		}

		.pulse-row .sentiment-bar-card {
			width: 100%;
		}
	}
</style>
