<script lang="ts">
	import { goto } from '$app/navigation';
	import { onMount } from 'svelte';
	import { getCurrentUser, clearSession, setToken, setWorkspaceSlug, adminStats, adminListWorkspaces, adminListAccounts, adminSuspendWorkspace, adminDeleteWorkspace, adminBanAccount, adminEnterWorkspace, adminAuditLog, adminWorkspaceDetail, adminExportWorkspace, adminResetPassword, adminSetAnnouncement, adminClearAnnouncement, adminGetModels, adminSetModels, browseModels, getAnnouncement, getFreeModels, adminSetFreeModels } from '$lib/api';

	type Tab = 'dashboard' | 'workspaces' | 'accounts' | 'audit' | 'models';
	let activeTab = $state<Tab>('dashboard');
	let loading = $state(true);

	// Dashboard
	let stats = $state<any>({});

	// Workspaces
	let workspaces = $state<any[]>([]);
	let expandedWorkspace = $state<string | null>(null);
	let workspaceDetail = $state<any>(null);

	// Accounts
	let accounts = $state<any[]>([]);

	// Audit
	let auditEntries = $state<any[]>([]);

	// Announcement
	let announcementMessage = $state('');
	let announcementType = $state('info');
	let currentAnnouncement = $state<any>(null);

	// Models
	let pinnedModels = $state<any[]>([]);
	let showBrowseModels = $state(false);
	let allModels = $state<any[]>([]);
	let modelSearch = $state('');
	let modelsLoading = $state(false);

	// Free Models
	let freeModels = $state<any[]>([]);
	let newFreeModelId = $state('');
	let newFreeModelName = $state('');

	onMount(async () => {
		const user = getCurrentUser();
		if (!user?.sa) { goto('/'); return; }
		await loadDashboard();
		loading = false;
	});

	async function loadDashboard() {
		try { stats = await adminStats(); } catch {}
		try {
			const ann = await getAnnouncement();
			currentAnnouncement = ann?.id ? ann : null;
		} catch {}
	}

	async function loadWorkspaces() {
		try {
			const data = await adminListWorkspaces();
			workspaces = data.workspaces || [];
		} catch {}
	}

	async function loadAccounts() {
		try {
			const data = await adminListAccounts();
			accounts = data.accounts || [];
		} catch {}
	}

	async function loadAudit() {
		try {
			const data = await adminAuditLog();
			auditEntries = data.entries || [];
		} catch {}
	}

	async function loadModels() {
		try {
			const data = await adminGetModels();
			pinnedModels = data.models || [];
		} catch {}
		try {
			const data = await getFreeModels();
			freeModels = data.models || [];
		} catch {}
	}

	async function switchTab(tab: Tab) {
		activeTab = tab;
		if (tab === 'dashboard') await loadDashboard();
		else if (tab === 'workspaces') await loadWorkspaces();
		else if (tab === 'accounts') await loadAccounts();
		else if (tab === 'audit') await loadAudit();
		else if (tab === 'models') await loadModels();
	}

	async function toggleSuspend(ws: any) {
		const newState = !ws.suspended;
		const reason = newState ? prompt('Reason for suspension:') || '' : '';
		try {
			await adminSuspendWorkspace(ws.slug, newState, reason);
			await loadWorkspaces();
		} catch (e: any) { alert(e.message); }
	}

	async function deleteWs(slug: string) {
		if (!confirm(`Permanently delete workspace "${slug}"? This removes it from the registry.`)) return;
		try {
			await adminDeleteWorkspace(slug);
			await loadWorkspaces();
		} catch (e: any) { alert(e.message); }
	}

	async function enterWorkspace(slug: string) {
		try {
			const data = await adminEnterWorkspace(slug);
			setToken(data.token);
			setWorkspaceSlug(data.slug);
			goto(`/w/${data.slug}`);
		} catch (e: any) { alert(e.message); }
	}

	async function toggleBan(account: any) {
		const newState = !account.banned;
		const verb = newState ? 'ban' : 'unban';
		if (!confirm(`${verb} ${account.email || account.display_name}?`)) return;
		try {
			await adminBanAccount(account.id, newState);
			await loadAccounts();
		} catch (e: any) { alert(e.message); }
	}

	async function toggleWorkspaceDetail(slug: string) {
		if (expandedWorkspace === slug) {
			expandedWorkspace = null;
			workspaceDetail = null;
			return;
		}
		try {
			workspaceDetail = await adminWorkspaceDetail(slug);
			expandedWorkspace = slug;
		} catch (e: any) { alert(e.message); }
	}

	async function exportWorkspace(slug: string) {
		try {
			const blob = await adminExportWorkspace(slug);
			const url = URL.createObjectURL(blob);
			const a = document.createElement('a');
			a.href = url;
			a.download = `${slug}-export.json`;
			a.click();
			URL.revokeObjectURL(url);
		} catch (e: any) { alert(e.message); }
	}

	async function resetPassword(account: any) {
		const newPw = prompt(`New password for ${account.email || account.display_name}:`);
		if (!newPw) return;
		if (newPw.length < 6) { alert('Password must be at least 6 characters'); return; }
		try {
			await adminResetPassword(account.id, newPw);
			alert('Password reset successfully');
		} catch (e: any) { alert(e.message); }
	}

	async function setAnnouncement() {
		if (!announcementMessage.trim()) return;
		try {
			await adminSetAnnouncement(announcementMessage.trim(), announcementType);
			announcementMessage = '';
			await loadDashboard();
		} catch (e: any) { alert(e.message); }
	}

	async function clearAnnouncement() {
		try {
			await adminClearAnnouncement();
			currentAnnouncement = null;
		} catch (e: any) { alert(e.message); }
	}

	async function openBrowseModels() {
		showBrowseModels = true;
		if (allModels.length === 0) {
			modelsLoading = true;
			try {
				const data = await browseModels();
				allModels = data.models || [];
			} catch (e: any) { alert(e.message); }
			modelsLoading = false;
		}
	}

	function filteredModels() {
		if (!modelSearch) return allModels.slice(0, 100);
		const q = modelSearch.toLowerCase();
		return allModels.filter((m: any) => m.id.toLowerCase().includes(q) || m.name.toLowerCase().includes(q)).slice(0, 100);
	}

	async function pinModel(model: any) {
		// Add to pinned if not already there
		if (pinnedModels.find((m: any) => m.id === model.id)) return;
		const newList = [...pinnedModels, { id: model.id, display_name: model.name || model.id, provider: model.provider, context_length: model.context_length, supports_tools: model.supports_tools }];
		try {
			await adminSetModels(newList);
			pinnedModels = newList;
		} catch (e: any) { alert(e.message); }
	}

	async function unpinModel(modelId: string) {
		const newList = pinnedModels.filter((m: any) => m.id !== modelId);
		try {
			await adminSetModels(newList);
			pinnedModels = newList;
		} catch (e: any) { alert(e.message); }
	}

	async function saveFreeModels(newList: any[]) {
		try {
			await adminSetFreeModels(newList);
			freeModels = newList;
		} catch (e: any) { alert(e.message); }
	}

	async function removeFreeModel(modelId: string) {
		await saveFreeModels(freeModels.filter((m: any) => m.id !== modelId));
	}

	async function moveFreeModel(index: number, direction: -1 | 1) {
		const newIndex = index + direction;
		if (newIndex < 0 || newIndex >= freeModels.length) return;
		const newList = [...freeModels];
		[newList[index], newList[newIndex]] = [newList[newIndex], newList[index]];
		newList.forEach((m: any, i: number) => m.priority = i);
		await saveFreeModels(newList);
	}

	async function addFreeModel() {
		if (!newFreeModelId.trim()) return;
		const name = newFreeModelName.trim() || newFreeModelId.trim();
		const newList = [...freeModels, { id: newFreeModelId.trim(), name, priority: freeModels.length }];
		await saveFreeModels(newList);
		newFreeModelId = '';
		newFreeModelName = '';
	}

	function handleLogout() {
		clearSession();
		goto('/');
	}

	function formatBytes(bytes: number): string {
		if (bytes < 1024) return bytes + ' B';
		if (bytes < 1024 * 1024) return (bytes / 1024).toFixed(1) + ' KB';
		if (bytes < 1024 * 1024 * 1024) return (bytes / (1024 * 1024)).toFixed(1) + ' MB';
		return (bytes / (1024 * 1024 * 1024)).toFixed(1) + ' GB';
	}

	function formatDate(iso: string): string {
		if (!iso) return '';
		return new Date(iso).toLocaleDateString('en-US', { month: 'short', day: 'numeric', year: 'numeric' });
	}

	function formatTime(iso: string): string {
		if (!iso) return '';
		return new Date(iso).toLocaleString('en-US', { month: 'short', day: 'numeric', hour: '2-digit', minute: '2-digit' });
	}
</script>

<div class="admin">
	<header class="admin-header">
		<div class="admin-title">
			<span class="admin-logo">nexus</span>
			<span class="admin-badge">ADMIN</span>
		</div>
		<div class="admin-user">
			<span class="admin-email">{getCurrentUser()?.name}</span>
			<button class="btn btn-ghost btn-sm" onclick={handleLogout}>Log Out</button>
		</div>
	</header>

	<div class="admin-layout">
		<nav class="admin-nav">
			<button class="nav-item" class:active={activeTab === 'dashboard'} onclick={() => switchTab('dashboard')}>Dashboard</button>
			<button class="nav-item" class:active={activeTab === 'workspaces'} onclick={() => switchTab('workspaces')}>Workspaces</button>
			<button class="nav-item" class:active={activeTab === 'accounts'} onclick={() => switchTab('accounts')}>Accounts</button>
			<button class="nav-item" class:active={activeTab === 'models'} onclick={() => switchTab('models')}>Models</button>
			<button class="nav-item" class:active={activeTab === 'audit'} onclick={() => switchTab('audit')}>Audit Log</button>
		</nav>

		<main class="admin-main">
			{#if loading}
				<p class="loading-text">Loading...</p>

			{:else if activeTab === 'dashboard'}
				<h2>Platform Overview</h2>
				<div class="stat-grid">
					<div class="stat-card">
						<span class="stat-value">{stats.workspaces ?? 0}</span>
						<span class="stat-label">Workspaces</span>
					</div>
					<div class="stat-card">
						<span class="stat-value">{stats.accounts ?? 0}</span>
						<span class="stat-label">Accounts</span>
					</div>
					<div class="stat-card">
						<span class="stat-value">{stats.total_members ?? 0}</span>
						<span class="stat-label">Total Members</span>
					</div>
					<div class="stat-card">
						<span class="stat-value">{stats.total_messages ?? 0}</span>
						<span class="stat-label">Total Messages</span>
					</div>
					<div class="stat-card">
						<span class="stat-value">{stats.total_files ?? 0}</span>
						<span class="stat-label">Files</span>
					</div>
					<div class="stat-card">
						<span class="stat-value">{formatBytes(stats.disk_bytes ?? 0)}</span>
						<span class="stat-label">Disk Usage</span>
					</div>
					<div class="stat-card" class:warn={stats.suspended_workspaces > 0}>
						<span class="stat-value">{stats.suspended_workspaces ?? 0}</span>
						<span class="stat-label">Suspended</span>
					</div>
					<div class="stat-card" class:warn={stats.banned_accounts > 0}>
						<span class="stat-value">{stats.banned_accounts ?? 0}</span>
						<span class="stat-label">Banned</span>
					</div>
				</div>

				<div class="section-divider"></div>

				<h3>Platform Announcement</h3>
				{#if currentAnnouncement}
					<div class="announcement-current">
						<span class="announcement-type-badge" data-type={currentAnnouncement.type}>{currentAnnouncement.type}</span>
						<span>{currentAnnouncement.message}</span>
						<button class="btn btn-ghost btn-xs danger" onclick={clearAnnouncement}>Clear</button>
					</div>
				{/if}
				<div class="announcement-form">
					<input type="text" class="admin-input" bind:value={announcementMessage} placeholder="Announcement message..." />
					<select class="admin-input admin-select" bind:value={announcementType}>
						<option value="info">Info</option>
						<option value="warning">Warning</option>
						<option value="success">Success</option>
					</select>
					<button class="btn btn-primary btn-sm" onclick={setAnnouncement}>Set</button>
				</div>

			{:else if activeTab === 'workspaces'}
				<h2>Workspaces ({workspaces.length})</h2>
				<div class="table-wrap">
					<table class="admin-table">
						<thead>
							<tr>
								<th>Slug</th>
								<th>Members</th>
								<th>Messages</th>
								<th>Created</th>
								<th>Status</th>
								<th>Actions</th>
							</tr>
						</thead>
						<tbody>
							{#each workspaces as ws}
							<tr class:suspended={ws.suspended}>
								<td class="mono clickable" onclick={() => toggleWorkspaceDetail(ws.slug)}>{ws.slug}</td>
								<td>{ws.member_count}</td>
								<td>{ws.message_count}</td>
								<td>{formatDate(ws.created_at)}</td>
								<td>
									{#if ws.suspended}
										<span class="status-badge suspended">Suspended</span>
									{:else}
										<span class="status-badge active">Active</span>
									{/if}
								</td>
								<td class="actions-cell">
									<button class="btn btn-ghost btn-xs" onclick={() => toggleWorkspaceDetail(ws.slug)}>Detail</button>
									<button class="btn btn-ghost btn-xs" onclick={() => enterWorkspace(ws.slug)}>Enter</button>
									<button class="btn btn-ghost btn-xs" onclick={() => exportWorkspace(ws.slug)}>Export</button>
									<button class="btn btn-ghost btn-xs" onclick={() => toggleSuspend(ws)}>
										{ws.suspended ? 'Unsuspend' : 'Suspend'}
									</button>
									<button class="btn btn-ghost btn-xs danger" onclick={() => deleteWs(ws.slug)}>Delete</button>
								</td>
							</tr>
							{#if expandedWorkspace === ws.slug && workspaceDetail}
							<tr class="detail-row">
								<td colspan="6">
									<div class="workspace-detail">
										<div class="detail-section">
											<h4>Members ({workspaceDetail.members?.length || 0})</h4>
											<div class="detail-list">
												{#each workspaceDetail.members || [] as member}
													<div class="detail-item">
														<span>{member.display_name}</span>
														<span class="role-badge" class:sa={member.role === 'admin'}>{member.role}</span>
														<span class="detail-date">{formatDate(member.joined_at)}</span>
													</div>
												{/each}
											</div>
										</div>
										<div class="detail-section">
											<h4>Channels ({workspaceDetail.channels?.length || 0})</h4>
											<div class="detail-list">
												{#each workspaceDetail.channels || [] as ch}
													<div class="detail-item">
														<span>{ch.name}</span>
														<span class="role-badge">{ch.type}</span>
														<span class="detail-date">{ch.message_count} msgs</span>
													</div>
												{/each}
											</div>
										</div>
									</div>
								</td>
							</tr>
							{/if}
							{/each}
						</tbody>
					</table>
				</div>

			{:else if activeTab === 'accounts'}
				<h2>Accounts ({accounts.length})</h2>
				<div class="table-wrap">
					<table class="admin-table">
						<thead>
							<tr>
								<th>Email</th>
								<th>Name</th>
								<th>Role</th>
								<th>Created</th>
								<th>Status</th>
								<th>Actions</th>
							</tr>
						</thead>
						<tbody>
							{#each accounts as account}
							<tr class:banned={account.banned}>
								<td class="mono">{account.email || '(anonymous)'}</td>
								<td>{account.display_name}</td>
								<td>
									{#if account.is_superadmin}
										<span class="role-badge sa">Superadmin</span>
									{:else}
										<span class="role-badge">User</span>
									{/if}
								</td>
								<td>{formatDate(account.created_at)}</td>
								<td>
									{#if account.banned}
										<span class="status-badge suspended">Banned</span>
									{:else}
										<span class="status-badge active">Active</span>
									{/if}
								</td>
								<td class="actions-cell">
									<button class="btn btn-ghost btn-xs" onclick={() => resetPassword(account)}>Reset PW</button>
									{#if !account.is_superadmin}
										<button class="btn btn-ghost btn-xs" class:danger={!account.banned} onclick={() => toggleBan(account)}>
											{account.banned ? 'Unban' : 'Ban'}
										</button>
									{/if}
								</td>
							</tr>
							{/each}
						</tbody>
					</table>
				</div>

			{:else if activeTab === 'models'}
				<h2>Pinned Models ({pinnedModels.length})</h2>
				<p class="section-hint">Models pinned here appear in workspace Brain settings dropdowns.</p>
				<div style="margin-bottom: 1rem;">
					<button class="btn btn-primary btn-sm" onclick={openBrowseModels}>Browse & Add Models</button>
				</div>

				{#if pinnedModels.length === 0}
					<p class="empty-text">No models pinned yet. Browse OpenRouter to add models.</p>
				{:else}
					<div class="table-wrap">
						<table class="admin-table">
							<thead>
								<tr>
									<th>Model ID</th>
									<th>Name</th>
									<th>Provider</th>
									<th>Context</th>
									<th>Actions</th>
								</tr>
							</thead>
							<tbody>
								{#each pinnedModels as model}
								<tr>
									<td class="mono">{model.id}</td>
									<td>{model.display_name}</td>
									<td>{model.provider}</td>
									<td>{model.context_length ? (model.context_length / 1000).toFixed(0) + 'K' : '-'}</td>
									<td>
										<button class="btn btn-ghost btn-xs danger" onclick={() => unpinModel(model.id)}>Remove</button>
									</td>
								</tr>
								{/each}
							</tbody>
						</table>
					</div>
				{/if}

				<h2 style="margin-top: 2rem;">Free Auto Models ({freeModels.length})</h2>
				<p class="section-hint">The "Free Auto (Nexus)" model cycles through these free models on failure. Drag to reorder priority.</p>

				{#if freeModels.length === 0}
					<p class="empty-text">Using built-in defaults. Add custom models to override.</p>
				{:else}
					<div class="table-wrap">
						<table class="admin-table">
							<thead>
								<tr>
									<th>#</th>
									<th>Model ID</th>
									<th>Name</th>
									<th>Actions</th>
								</tr>
							</thead>
							<tbody>
								{#each freeModels as model, i}
								<tr>
									<td>{i + 1}</td>
									<td class="mono">{model.id}</td>
									<td>{model.name}</td>
									<td style="display: flex; gap: 0.25rem;">
										<button class="btn btn-ghost btn-xs" onclick={() => moveFreeModel(i, -1)} disabled={i === 0}>Up</button>
										<button class="btn btn-ghost btn-xs" onclick={() => moveFreeModel(i, 1)} disabled={i === freeModels.length - 1}>Down</button>
										<button class="btn btn-ghost btn-xs danger" onclick={() => removeFreeModel(model.id)}>Remove</button>
									</td>
								</tr>
								{/each}
							</tbody>
						</table>
					</div>
				{/if}

				<div style="display: flex; gap: 0.5rem; margin-top: 0.75rem; align-items: flex-end;">
					<div style="flex: 1;">
						<label style="font-size: 0.75rem; color: var(--text-tertiary);">Model ID</label>
						<input class="admin-input" bind:value={newFreeModelId} placeholder="provider/model-name:free" />
					</div>
					<div style="flex: 1;">
						<label style="font-size: 0.75rem; color: var(--text-tertiary);">Display Name</label>
						<input class="admin-input" bind:value={newFreeModelName} placeholder="Model Name" />
					</div>
					<button class="btn btn-primary btn-sm" onclick={addFreeModel} disabled={!newFreeModelId.trim()}>Add</button>
				</div>

			{:else if activeTab === 'audit'}
				<h2>Audit Log</h2>
				{#if auditEntries.length === 0}
					<p class="empty-text">No audit entries yet.</p>
				{:else}
					<div class="table-wrap">
						<table class="admin-table">
							<thead>
								<tr>
									<th>Time</th>
									<th>Actor</th>
									<th>Action</th>
									<th>Target</th>
									<th>Detail</th>
								</tr>
							</thead>
							<tbody>
								{#each auditEntries as entry}
								<tr>
									<td class="nowrap">{formatTime(entry.created_at)}</td>
									<td class="mono">{entry.actor_email}</td>
									<td><span class="action-badge">{entry.action}</span></td>
									<td class="mono">{entry.target_type}: {entry.target_id}</td>
									<td>{entry.detail}</td>
								</tr>
								{/each}
							</tbody>
						</table>
					</div>
				{/if}
			{/if}
		</main>
	</div>
</div>

{#if showBrowseModels}
<div class="modal-overlay" onclick={() => showBrowseModels = false}>
	<div class="modal-content" onclick={(e) => e.stopPropagation()}>
		<div class="modal-header">
			<h3>Browse OpenRouter Models</h3>
			<button class="modal-close" onclick={() => showBrowseModels = false}>&times;</button>
		</div>
		<div class="modal-search">
			<input type="text" class="admin-input" bind:value={modelSearch} placeholder="Search models..." />
		</div>
		{#if modelsLoading}
			<p class="loading-text" style="padding: 1rem;">Loading models...</p>
		{:else}
			<div class="modal-list">
				{#each filteredModels() as model}
					<div class="modal-list-item">
						<div class="modal-list-info">
							<span class="modal-list-name">{model.name || model.id}</span>
							<span class="modal-list-meta">
								{model.provider}
								{#if model.context_length} &middot; {(model.context_length / 1000).toFixed(0)}K ctx{/if}
								{#if model.is_free} &middot; <span style="color: #4ade80">Free</span>{/if}
							</span>
						</div>
						{#if pinnedModels.find((m: any) => m.id === model.id)}
							<span class="pinned-badge">Pinned</span>
						{:else}
							<button class="btn btn-ghost btn-xs" onclick={() => pinModel(model)}>Pin</button>
						{/if}
					</div>
				{/each}
			</div>
		{/if}
	</div>
</div>
{/if}

<style>
	.admin {
		min-height: 100vh;
		background: var(--bg-root);
		color: var(--text-primary);
	}

	/* Header */
	.admin-header {
		display: flex;
		align-items: center;
		justify-content: space-between;
		padding: var(--space-md) var(--space-xl);
		border-bottom: 1px solid var(--border-subtle);
		background: var(--bg-surface);
	}
	.admin-title {
		display: flex;
		align-items: center;
		gap: var(--space-sm);
	}
	.admin-logo {
		font-size: 1.1rem;
		font-weight: 800;
		letter-spacing: -0.03em;
		color: var(--text-primary);
	}
	.admin-badge {
		font-size: 0.6rem;
		font-weight: 700;
		letter-spacing: 0.1em;
		padding: 2px 6px;
		border-radius: 4px;
		background: rgba(239,68,68,0.15);
		color: #f87171;
	}
	.admin-user {
		display: flex;
		align-items: center;
		gap: var(--space-md);
	}
	.admin-email {
		font-size: 0.85rem;
		color: var(--text-secondary);
	}

	/* Layout */
	.admin-layout {
		display: flex;
		min-height: calc(100vh - 50px);
	}
	.admin-nav {
		width: 200px;
		min-width: 200px;
		padding: var(--space-lg) 0;
		border-right: 1px solid var(--border-subtle);
		background: var(--bg-surface);
		display: flex;
		flex-direction: column;
		gap: 2px;
	}
	.nav-item {
		display: block;
		width: 100%;
		padding: var(--space-sm) var(--space-xl);
		background: none;
		border: none;
		text-align: left;
		color: var(--text-secondary);
		font-size: 0.85rem;
		cursor: pointer;
		border-left: 3px solid transparent;
	}
	.nav-item:hover {
		color: var(--text-primary);
		background: var(--bg-hover);
	}
	.nav-item.active {
		color: var(--accent);
		border-left-color: var(--accent);
		background: rgba(249,115,22,0.05);
	}
	.admin-main {
		flex: 1;
		padding: var(--space-xl) var(--space-2xl);
		overflow-x: auto;
	}
	.admin-main h2 {
		font-size: 1.15rem;
		font-weight: 600;
		margin-bottom: var(--space-xl);
	}
	.admin-main h3 {
		font-size: 1rem;
		font-weight: 600;
		margin-bottom: var(--space-md);
	}

	/* Stats */
	.stat-grid {
		display: grid;
		grid-template-columns: repeat(auto-fill, minmax(160px, 1fr));
		gap: var(--space-md);
	}
	.stat-card {
		background: var(--bg-surface);
		border: 1px solid var(--border-subtle);
		border-radius: var(--radius-md);
		padding: var(--space-lg);
		text-align: center;
	}
	.stat-card.warn {
		border-color: rgba(239,68,68,0.3);
	}
	.stat-value {
		display: block;
		font-size: 1.5rem;
		font-weight: 700;
		color: var(--text-primary);
		margin-bottom: var(--space-xs);
	}
	.stat-card.warn .stat-value {
		color: #f87171;
	}
	.stat-label {
		font-size: 0.75rem;
		color: var(--text-tertiary);
		text-transform: uppercase;
		letter-spacing: 0.05em;
	}

	/* Announcement */
	.section-divider {
		margin: var(--space-xl) 0;
		border-top: 1px solid var(--border-subtle);
	}
	.announcement-current {
		display: flex;
		align-items: center;
		gap: var(--space-md);
		padding: var(--space-sm) var(--space-md);
		background: var(--bg-surface);
		border: 1px solid var(--border-subtle);
		border-radius: var(--radius-md);
		margin-bottom: var(--space-md);
		font-size: 0.85rem;
	}
	.announcement-type-badge {
		padding: 1px 6px;
		border-radius: 4px;
		font-size: 0.7rem;
		font-weight: 600;
		text-transform: uppercase;
	}
	.announcement-type-badge[data-type="info"] {
		background: rgba(59,130,246,0.15);
		color: #60a5fa;
	}
	.announcement-type-badge[data-type="warning"] {
		background: rgba(239,68,68,0.15);
		color: #f87171;
	}
	.announcement-type-badge[data-type="success"] {
		background: rgba(34,197,94,0.15);
		color: #4ade80;
	}
	.announcement-form {
		display: flex;
		gap: var(--space-sm);
		align-items: center;
	}
	.admin-input {
		background: var(--bg-input, var(--bg-root));
		border: 1px solid var(--border-subtle);
		border-radius: var(--radius-sm, 6px);
		padding: 6px 10px;
		color: var(--text-primary);
		font-size: 0.85rem;
		flex: 1;
	}
	.admin-select {
		flex: 0;
		width: auto;
	}
	.section-hint {
		font-size: 0.8rem;
		color: var(--text-tertiary);
		margin-bottom: var(--space-md);
	}

	/* Tables */
	.table-wrap {
		overflow-x: auto;
	}
	.admin-table {
		width: 100%;
		border-collapse: collapse;
		font-size: 0.85rem;
	}
	.admin-table th {
		text-align: left;
		padding: var(--space-sm) var(--space-md);
		color: var(--text-tertiary);
		font-weight: 500;
		font-size: 0.75rem;
		text-transform: uppercase;
		letter-spacing: 0.05em;
		border-bottom: 1px solid var(--border-subtle);
	}
	.admin-table td {
		padding: var(--space-sm) var(--space-md);
		border-bottom: 1px solid var(--border-subtle);
		vertical-align: middle;
	}
	.admin-table tr:hover td {
		background: var(--bg-hover);
	}
	.admin-table tr.suspended td {
		opacity: 0.6;
	}
	.admin-table tr.banned td {
		opacity: 0.6;
	}
	.mono {
		font-family: var(--font-mono, monospace);
		font-size: 0.8rem;
	}
	.clickable {
		cursor: pointer;
	}
	.clickable:hover {
		text-decoration: underline;
	}
	.nowrap {
		white-space: nowrap;
	}
	.actions-cell {
		display: flex;
		gap: 4px;
		white-space: nowrap;
	}

	/* Detail row */
	.detail-row td {
		padding: 0 !important;
		background: var(--bg-root) !important;
	}
	.workspace-detail {
		display: grid;
		grid-template-columns: 1fr 1fr;
		gap: var(--space-lg);
		padding: var(--space-lg) var(--space-xl);
	}
	.detail-section h4 {
		font-size: 0.8rem;
		font-weight: 600;
		margin-bottom: var(--space-sm);
		color: var(--text-secondary);
	}
	.detail-list {
		display: flex;
		flex-direction: column;
		gap: 4px;
	}
	.detail-item {
		display: flex;
		gap: var(--space-md);
		font-size: 0.8rem;
		align-items: center;
	}
	.detail-date {
		color: var(--text-tertiary);
		font-size: 0.7rem;
	}

	/* Badges */
	.status-badge {
		display: inline-block;
		padding: 1px 8px;
		border-radius: 10px;
		font-size: 0.7rem;
		font-weight: 600;
	}
	.status-badge.active {
		background: rgba(34,197,94,0.15);
		color: #4ade80;
	}
	.status-badge.suspended {
		background: rgba(239,68,68,0.15);
		color: #f87171;
	}
	.role-badge {
		font-size: 0.7rem;
		color: var(--text-tertiary);
	}
	.role-badge.sa {
		color: #f87171;
		font-weight: 600;
	}
	.action-badge {
		display: inline-block;
		padding: 1px 6px;
		border-radius: 4px;
		font-size: 0.7rem;
		background: rgba(59,130,246,0.15);
		color: #60a5fa;
		font-family: var(--font-mono, monospace);
	}
	.pinned-badge {
		font-size: 0.7rem;
		color: var(--accent);
		font-weight: 600;
	}

	/* Buttons */
	.btn-xs {
		padding: 2px 8px;
		font-size: 0.7rem;
		border-radius: 4px;
	}
	.danger {
		color: #f87171 !important;
	}
	.danger:hover {
		background: rgba(239,68,68,0.1) !important;
	}

	/* Modal */
	.modal-overlay {
		position: fixed;
		top: 0;
		left: 0;
		right: 0;
		bottom: 0;
		background: rgba(0,0,0,0.6);
		z-index: 200;
		display: flex;
		align-items: center;
		justify-content: center;
	}
	.modal-content {
		background: var(--bg-surface);
		border: 1px solid var(--border-subtle);
		border-radius: 12px;
		max-width: 700px;
		width: 90vw;
		max-height: 80vh;
		display: flex;
		flex-direction: column;
	}
	.modal-header {
		display: flex;
		align-items: center;
		justify-content: space-between;
		padding: 16px 20px;
		border-bottom: 1px solid var(--border-subtle);
	}
	.modal-header h3 {
		margin: 0;
	}
	.modal-close {
		background: none;
		border: none;
		color: var(--text-tertiary);
		font-size: 1.5rem;
		cursor: pointer;
	}
	.modal-search {
		padding: 12px 20px;
		border-bottom: 1px solid var(--border-subtle);
	}
	.modal-list {
		overflow-y: auto;
		flex: 1;
	}
	.modal-list-item {
		display: flex;
		align-items: center;
		justify-content: space-between;
		padding: 8px 20px;
		gap: 12px;
	}
	.modal-list-item:hover {
		background: var(--bg-hover);
	}
	.modal-list-info {
		flex: 1;
		min-width: 0;
	}
	.modal-list-name {
		display: block;
		font-size: 0.85rem;
		font-weight: 500;
		overflow: hidden;
		text-overflow: ellipsis;
		white-space: nowrap;
	}
	.modal-list-meta {
		font-size: 0.7rem;
		color: var(--text-tertiary);
	}

	.loading-text, .empty-text {
		color: var(--text-tertiary);
		font-size: 0.85rem;
	}

	@media (max-width: 640px) {
		.admin-nav { width: 140px; min-width: 140px; }
		.admin-main { padding: var(--space-md); }
		.stat-grid { grid-template-columns: repeat(2, 1fr); }
		.workspace-detail { grid-template-columns: 1fr; }
	}
</style>
