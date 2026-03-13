<script lang="ts">
	import { page } from '$app/state';
	import { goto } from '$app/navigation';
	import { onMount } from 'svelte';
	import { listBriefs, createBrief, deleteBrief, generateBrief, getBrief, getCurrentUser, shareBrief, unshareBrief } from '$lib/api';

	let slug = $derived(page.params.slug);
	let briefs = $state<any[]>([]);
	let loading = $state(true);
	let currentUser = $state(getCurrentUser());

	// New brief form
	let showNewBrief = $state(false);
	let newTemplate = $state('workspace_pulse');
	let newTitle = $state('');
	let newTopic = $state('');
	let newSchedule = $state('manual');
	let newScheduleTime = $state('6:00');
	let creating = $state(false);

	// Brief detail
	let activeBrief = $state<any>(null);
	let generatingId = $state('');
	let sharingId = $state('');
	let copiedLink = $state(false);

	const TEMPLATES = [
		{ id: 'workspace_pulse', title: 'Workspace Pulse', desc: 'Activity, velocity, energy, what\'s hot' },
		{ id: 'north_star_status', title: 'North Star Status', desc: 'Mission alignment, drift alerts, theme check' },
		{ id: 'team_health', title: 'Team Health', desc: 'Engagement, workload, collaboration patterns' },
		{ id: 'custom', title: 'Custom', desc: 'Brief on any topic you define' },
	];

	async function load() {
		loading = true;
		try {
			briefs = await listBriefs(slug);
		} catch {}
		loading = false;
	}

	async function handleCreate() {
		creating = true;
		try {
			const tmpl = TEMPLATES.find(t => t.id === newTemplate);
			const result = await createBrief(slug, {
				title: newTitle || tmpl?.title || 'Untitled',
				template: newTemplate,
				topic: newTopic,
				schedule: newSchedule,
				schedule_time: newSchedule !== 'manual' ? newScheduleTime : '',
			});
			showNewBrief = false;
			newTitle = '';
			newTopic = '';
			newSchedule = 'manual';
			await load();
			// Auto-generate
			if (result?.id) {
				generatingId = result.id;
				await generateBrief(slug, result.id);
				setTimeout(async () => {
					await load();
					generatingId = '';
				}, 15000);
			}
		} catch (e: any) {
			alert(e.message);
		}
		creating = false;
	}

	async function handleGenerate(id: string) {
		generatingId = id;
		try {
			await generateBrief(slug, id);
			setTimeout(async () => {
				await load();
				const updated = briefs.find(b => b.id === id);
				if (activeBrief?.id === id && updated) activeBrief = updated;
				generatingId = '';
			}, 15000);
		} catch (e: any) {
			alert(e.message);
			generatingId = '';
		}
	}

	async function handleDelete(id: string) {
		if (!confirm('Delete this brief?')) return;
		try {
			await deleteBrief(slug, id);
			if (activeBrief?.id === id) activeBrief = null;
			await load();
		} catch {}
	}

	async function handleShare(id: string) {
		sharingId = id;
		try {
			const result = await shareBrief(slug, id);
			await load();
			const updated = briefs.find(b => b.id === id);
			if (activeBrief?.id === id && updated) activeBrief = updated;
		} catch (e: any) {
			alert(e.message);
		}
		sharingId = '';
	}

	async function handleUnshare(id: string) {
		if (!confirm('Revoke public link? Anyone with the link will lose access.')) return;
		try {
			await unshareBrief(slug, id);
			await load();
			const updated = briefs.find(b => b.id === id);
			if (activeBrief?.id === id && updated) activeBrief = updated;
		} catch {}
	}

	function copyShareLink(token: string) {
		const url = `${window.location.origin}/brief/${token}`;
		navigator.clipboard.writeText(url);
		copiedLink = true;
		setTimeout(() => copiedLink = false, 2000);
	}

	async function openBrief(b: any) {
		try {
			activeBrief = await getBrief(slug, b.id);
		} catch {
			activeBrief = b;
		}
	}

	function timeAgo(dateStr: string): string {
		if (!dateStr) return 'never';
		const diff = Date.now() - new Date(dateStr).getTime();
		const mins = Math.floor(diff / 60000);
		if (mins < 1) return 'just now';
		if (mins < 60) return `${mins}m ago`;
		const hours = Math.floor(mins / 60);
		if (hours < 24) return `${hours}h ago`;
		const days = Math.floor(hours / 24);
		return `${days}d ago`;
	}

	function templateIcon(template: string): string {
		switch (template) {
			case 'workspace_pulse': return '\u{1F4CA}';
			case 'north_star_status': return '\u{2B50}';
			case 'team_health': return '\u{1F49A}';
			default: return '\u{1F4DD}';
		}
	}

	function renderMarkdown(text: string): string {
		return text
			.replace(/&/g, '&amp;').replace(/</g, '&lt;').replace(/>/g, '&gt;')
			.replace(/^### (.+)$/gm, '<h3>$1</h3>')
			.replace(/^## (.+)$/gm, '<h2>$1</h2>')
			.replace(/^# (.+)$/gm, '<h1>$1</h1>')
			.replace(/\*\*(.+?)\*\*/g, '<strong>$1</strong>')
			.replace(/\*(.+?)\*/g, '<em>$1</em>')
			.replace(/^- (.+)$/gm, '<li>$1</li>')
			.replace(/\n\n/g, '</p><p>')
			.replace(/\n/g, '<br>')
			.replace(/^/, '<p>').replace(/$/, '</p>')
			.replace(/<p><h([123])>/g, '<h$1>').replace(/<\/h([123])><\/p>/g, '</h$1>')
			.replace(/<p><\/p>/g, '');
	}

	onMount(load);
</script>

<div class="briefs-page">
	<div class="briefs-header">
		<button class="back-btn" onclick={() => goto(`/w/${slug}`)}>
			<svg width="20" height="20" viewBox="0 0 20 20" fill="none">
				<path d="M12 4L6 10L12 16" stroke="currentColor" stroke-width="1.5" stroke-linecap="round" stroke-linejoin="round"/>
			</svg>
		</button>
		<h1>Living Briefs</h1>
		<button class="btn btn-primary btn-sm" onclick={() => showNewBrief = !showNewBrief}>New Brief</button>
	</div>

	{#if showNewBrief}
	<div class="new-brief-form">
		<h3>Create a Living Brief</h3>
		<div class="template-grid">
			{#each TEMPLATES as tmpl}
				<button
					class="template-card"
					class:selected={newTemplate === tmpl.id}
					onclick={() => { newTemplate = tmpl.id; newTitle = tmpl.id !== 'custom' ? tmpl.title : ''; }}
				>
					<span class="template-icon">{templateIcon(tmpl.id)}</span>
					<strong>{tmpl.title}</strong>
					<span class="template-desc">{tmpl.desc}</span>
				</button>
			{/each}
		</div>

		{#if newTemplate === 'custom'}
		<label class="field-label">Title</label>
		<input class="field-input" type="text" bind:value={newTitle} placeholder="Brief title" />
		<label class="field-label">Topic</label>
		<input class="field-input" type="text" bind:value={newTopic} placeholder="What should this brief cover?" />
		{/if}

		<label class="field-label">Schedule</label>
		<select class="field-input" bind:value={newSchedule}>
			<option value="manual">Manual only</option>
			<option value="daily">Daily</option>
			<option value="weekly">Weekly (Mondays)</option>
		</select>

		{#if newSchedule !== 'manual'}
		<label class="field-label">Time (UTC)</label>
		<input class="field-input" type="text" bind:value={newScheduleTime} placeholder="6:00" style="max-width:120px;" />
		{/if}

		<div class="form-actions">
			<button class="btn btn-primary btn-sm" onclick={handleCreate} disabled={creating}>
				{creating ? 'Creating...' : 'Create & Generate'}
			</button>
			<button class="btn btn-sm" onclick={() => showNewBrief = false}>Cancel</button>
		</div>
	</div>
	{/if}

	<div class="briefs-layout">
		<div class="briefs-list">
			{#if loading}
				<p class="empty-msg">Loading...</p>
			{:else if briefs.length === 0}
				<div class="empty-state">
					<p>No briefs yet.</p>
					<p class="empty-hint">Create your first Living Brief to get AI-generated workspace insights.</p>
				</div>
			{:else}
				{#each briefs as brief}
					<div
						class="brief-card"
						class:active={activeBrief?.id === brief.id}
						onclick={() => openBrief(brief)}
						role="button"
						tabindex="0"
					>
						<div class="brief-card-header">
							<span class="brief-icon">{templateIcon(brief.template)}</span>
							<div class="brief-meta">
								<strong>{brief.title}</strong>
								<span class="brief-time">
									{brief.generated_at ? timeAgo(brief.generated_at) : 'Not generated'}
									{#if brief.is_public}
										<span class="brief-share-badge">shared</span>
									{/if}
									{#if brief.schedule !== 'manual'}
										<span class="brief-schedule-badge">{brief.schedule}</span>
									{/if}
								</span>
							</div>
						</div>
						<div class="brief-card-actions">
							<button class="icon-btn" title="Regenerate" onclick={() => handleGenerate(brief.id)} disabled={generatingId === brief.id}>
								{generatingId === brief.id ? '...' : '↻'}
							</button>
							<button class="icon-btn" title="Delete" onclick={() => handleDelete(brief.id)}>
								✕
							</button>
						</div>
					</div>
				{/each}
			{/if}
		</div>

		<div class="brief-detail">
			{#if activeBrief}
				<div class="brief-detail-header">
					<h2>{activeBrief.title}</h2>
					{#if activeBrief.generated_at}
						<span class="brief-detail-time">Generated {timeAgo(activeBrief.generated_at)}</span>
					{/if}
					<div style="margin-left:auto;display:flex;gap:8px;align-items:center;">
						<button class="btn btn-sm" style="border:1px solid var(--border-subtle);" onclick={() => handleGenerate(activeBrief.id)} disabled={generatingId === activeBrief.id}>
							{generatingId === activeBrief.id ? 'Generating...' : 'Refresh'}
						</button>
						{#if activeBrief.is_public && activeBrief.share_token}
							<button class="btn btn-sm share-btn shared" onclick={() => copyShareLink(activeBrief.share_token)}>
								{copiedLink ? 'Copied!' : 'Copy Link'}
							</button>
							<button class="btn btn-sm" style="border:1px solid var(--border-subtle);color:var(--text-tertiary);" onclick={() => handleUnshare(activeBrief.id)}>
								Unshare
							</button>
						{:else}
							<button class="btn btn-sm share-btn" onclick={() => handleShare(activeBrief.id)} disabled={sharingId === activeBrief.id}>
								{sharingId === activeBrief.id ? 'Sharing...' : 'Share'}
							</button>
						{/if}
					</div>
				</div>
				{#if activeBrief.content}
					<div class="brief-content markdown-body">
						{@html renderMarkdown(activeBrief.content)}
					</div>
				{:else}
					<div class="empty-state">
						<p>This brief hasn't been generated yet.</p>
						<button class="btn btn-primary btn-sm" onclick={() => handleGenerate(activeBrief.id)}>Generate Now</button>
					</div>
				{/if}
			{:else}
				<div class="empty-state">
					<p>Select a brief to view, or create a new one.</p>
				</div>
			{/if}
		</div>
	</div>
</div>

<style>
	.briefs-page {
		flex: 1;
		display: flex;
		flex-direction: column;
		overflow: hidden;
		height: 100vh;
		background: var(--bg-primary);
	}
	.briefs-header {
		display: flex;
		align-items: center;
		gap: var(--space-md);
		padding: var(--space-lg) var(--space-xl);
		border-bottom: 1px solid var(--border-subtle);
	}
	.briefs-header h1 {
		font-size: 1.2rem;
		font-weight: 600;
		flex: 1;
	}
	.back-btn {
		background: none;
		border: none;
		color: var(--text-tertiary);
		cursor: pointer;
		padding: 4px;
	}
	.back-btn:hover { color: var(--text-primary); }

	/* New brief form */
	.new-brief-form {
		padding: var(--space-lg) var(--space-xl);
		border-bottom: 1px solid var(--border-subtle);
		background: var(--bg-secondary);
	}
	.new-brief-form h3 {
		font-size: 0.95rem;
		margin-bottom: var(--space-md);
	}
	.template-grid {
		display: grid;
		grid-template-columns: repeat(auto-fill, minmax(180px, 1fr));
		gap: var(--space-sm);
		margin-bottom: var(--space-md);
	}
	.template-card {
		display: flex;
		flex-direction: column;
		gap: 4px;
		padding: var(--space-md);
		background: var(--bg-primary);
		border: 1px solid var(--border-subtle);
		border-radius: var(--radius-sm);
		cursor: pointer;
		text-align: left;
		color: var(--text-primary);
		transition: border-color 0.15s;
	}
	.template-card:hover { border-color: var(--text-tertiary); }
	.template-card.selected { border-color: var(--accent); background: color-mix(in srgb, var(--accent) 5%, var(--bg-primary)); }
	.template-icon { font-size: 1.4rem; }
	.template-desc { font-size: 0.75rem; color: var(--text-tertiary); }
	.field-label {
		display: block;
		font-size: 0.8rem;
		color: var(--text-secondary);
		margin: var(--space-sm) 0 4px;
	}
	.field-input {
		width: 100%;
		padding: var(--space-sm) var(--space-md);
		background: var(--bg-primary);
		border: 1px solid var(--border-subtle);
		border-radius: var(--radius-sm);
		color: var(--text-primary);
		font-size: 0.85rem;
	}
	.field-input:focus { outline: none; border-color: var(--accent); }
	.form-actions {
		display: flex;
		gap: var(--space-sm);
		margin-top: var(--space-md);
	}

	/* Layout */
	.briefs-layout {
		display: flex;
		flex: 1;
		overflow: hidden;
	}
	.briefs-list {
		width: 320px;
		min-width: 280px;
		border-right: 1px solid var(--border-subtle);
		overflow-y: auto;
		padding: var(--space-md);
		display: flex;
		flex-direction: column;
		gap: var(--space-sm);
	}
	.brief-detail {
		flex: 1;
		overflow-y: auto;
		padding: var(--space-xl);
	}

	/* Brief cards */
	.brief-card {
		display: flex;
		align-items: center;
		justify-content: space-between;
		padding: var(--space-md);
		background: var(--bg-secondary);
		border: 1px solid var(--border-subtle);
		border-radius: var(--radius-sm);
		cursor: pointer;
		color: var(--text-primary);
		text-align: left;
		transition: border-color 0.15s;
		width: 100%;
	}
	.brief-card:hover { border-color: var(--text-tertiary); }
	.brief-card.active { border-color: var(--accent); background: color-mix(in srgb, var(--accent) 5%, var(--bg-secondary)); }
	.brief-card-header {
		display: flex;
		align-items: center;
		gap: var(--space-sm);
		flex: 1;
		min-width: 0;
	}
	.brief-icon { font-size: 1.2rem; flex-shrink: 0; }
	.brief-meta {
		display: flex;
		flex-direction: column;
		min-width: 0;
	}
	.brief-meta strong {
		font-size: 0.85rem;
		white-space: nowrap;
		overflow: hidden;
		text-overflow: ellipsis;
	}
	.brief-time {
		font-size: 0.72rem;
		color: var(--text-tertiary);
		display: flex;
		align-items: center;
		gap: 6px;
	}
	.brief-schedule-badge {
		font-size: 0.65rem;
		padding: 1px 5px;
		border-radius: 6px;
		background: color-mix(in srgb, var(--accent) 15%, transparent);
		color: var(--accent);
	}
	.brief-card-actions {
		display: flex;
		gap: 2px;
		flex-shrink: 0;
	}
	.icon-btn {
		background: none;
		border: none;
		cursor: pointer;
		font-size: 0.85rem;
		padding: 4px;
		border-radius: 4px;
		opacity: 0.5;
	}
	.icon-btn:hover { opacity: 1; background: var(--bg-root); }

	/* Detail view */
	.brief-detail-header {
		display: flex;
		align-items: center;
		gap: var(--space-md);
		margin-bottom: var(--space-lg);
	}
	.brief-detail-header h2 {
		font-size: 1.1rem;
		font-weight: 600;
	}
	.brief-detail-time {
		font-size: 0.75rem;
		color: var(--text-tertiary);
	}
	.brief-content {
		font-size: 0.9rem;
		line-height: 1.6;
		color: var(--text-primary);
		max-width: 720px;
	}
	.brief-content :global(h1) { font-size: 1.2rem; font-weight: 600; margin: 1.5em 0 0.5em; }
	.brief-content :global(h2) { font-size: 1.05rem; font-weight: 600; margin: 1.2em 0 0.4em; color: var(--accent); }
	.brief-content :global(h3) { font-size: 0.95rem; font-weight: 600; margin: 1em 0 0.3em; }
	.brief-content :global(ul) { padding-left: 1.2em; margin: 0.5em 0; }
	.brief-content :global(li) { margin: 0.3em 0; }
	.brief-content :global(strong) { color: var(--text-primary); }
	.brief-content :global(em) { color: var(--text-secondary); }
	.brief-content :global(p) { margin: 0.5em 0; }

	/* Empty states */
	.empty-state {
		display: flex;
		flex-direction: column;
		align-items: center;
		justify-content: center;
		padding: var(--space-xxl);
		color: var(--text-tertiary);
		text-align: center;
		gap: var(--space-sm);
	}
	.empty-hint { font-size: 0.8rem; }
	.empty-msg { color: var(--text-tertiary); font-size: 0.85rem; padding: var(--space-lg); text-align: center; }

	.btn { background: none; border: none; cursor: pointer; color: var(--text-primary); font-size: 0.8rem; padding: 6px 12px; border-radius: var(--radius-sm); }
	.btn:hover { background: var(--bg-root); }
	.btn-primary { background: var(--accent); color: #000; font-weight: 500; }
	.btn-primary:hover { opacity: 0.9; }
	.btn-primary:disabled { opacity: 0.5; }
	.btn-sm { font-size: 0.8rem; padding: 5px 12px; }

	/* Share */
	.share-btn { border: 1px solid var(--accent); color: var(--accent); }
	.share-btn:hover { background: color-mix(in srgb, var(--accent) 10%, transparent); }
	.share-btn.shared { background: color-mix(in srgb, var(--accent) 10%, transparent); }
	.brief-share-badge {
		font-size: 0.6rem;
		padding: 1px 5px;
		border-radius: 6px;
		background: color-mix(in srgb, #22c55e 15%, transparent);
		color: #22c55e;
		text-transform: uppercase;
		letter-spacing: 0.5px;
	}
</style>
