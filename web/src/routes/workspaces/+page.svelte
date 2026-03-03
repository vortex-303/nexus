<script lang="ts">
	import { goto } from '$app/navigation';
	import { onMount } from 'svelte';
	import { listWorkspaces, setWorkspaceSlug, setToken, login, getCurrentUser, clearSession } from '$lib/api';

	let workspaces = $state<any[]>([]);
	let loading = $state(true);
	let error = $state('');

	onMount(async () => {
		const user = getCurrentUser();
		if (!user?.aid) {
			goto('/');
			return;
		}
		try {
			const data = await listWorkspaces();
			workspaces = data.workspaces || [];
		} catch (e: any) {
			error = e.message;
		} finally {
			loading = false;
		}
	});

	async function selectWorkspace(ws: any) {
		const user = getCurrentUser();
		if (!user) return;
		try {
			// Re-login with the specific workspace to get a workspace-scoped token
			// We need the email from the current token - parse it
			const data = await login(user.email || '', '', ws.slug);
			setWorkspaceSlug(ws.slug);
			goto(`/w/${ws.slug}`);
		} catch (e: any) {
			// If login fails, try just setting slug and redirecting
			setWorkspaceSlug(ws.slug);
			goto(`/w/${ws.slug}`);
		}
	}

	function handleLogout() {
		clearSession();
		goto('/');
	}
</script>

<div class="picker">
	<div class="picker-card">
		<h1>Your Workspaces</h1>

		{#if loading}
			<p class="hint">Loading...</p>
		{:else if error}
			<p class="error">{error}</p>
		{:else if workspaces.length === 0}
			<p class="hint">You're not a member of any workspaces yet.</p>
		{:else}
			<div class="workspace-list">
				{#each workspaces as ws}
					<button class="workspace-item" onclick={() => selectWorkspace(ws)}>
						<span class="ws-name">{ws.display_name || ws.slug}</span>
						<span class="ws-role">{ws.role}</span>
						<svg width="16" height="16" viewBox="0 0 16 16" fill="none">
							<path d="M6.5 3.5L11 8L6.5 12.5" stroke="currentColor" stroke-width="2" fill="none" stroke-linecap="round" stroke-linejoin="round"/>
						</svg>
					</button>
				{/each}
			</div>
		{/if}

		<div class="actions">
			<button class="btn btn-primary" onclick={() => goto('/')}>Create New Workspace</button>
			<button class="btn btn-ghost" onclick={handleLogout}>Log Out</button>
		</div>
	</div>
</div>

<style>
	.picker {
		min-height: 100vh;
		display: flex;
		align-items: center;
		justify-content: center;
		background: var(--bg-root);
	}
	.picker-card {
		background: var(--bg-surface);
		border: 1px solid var(--border-subtle);
		border-radius: var(--radius-xl);
		padding: var(--space-2xl);
		max-width: 420px;
		width: 100%;
		box-shadow: var(--shadow-lg);
	}
	h1 {
		font-size: 1.25rem;
		font-weight: 600;
		color: var(--text-primary);
		margin-bottom: var(--space-xl);
		text-align: center;
	}
	.workspace-list {
		display: flex;
		flex-direction: column;
		gap: var(--space-sm);
		margin-bottom: var(--space-xl);
	}
	.workspace-item {
		display: flex;
		align-items: center;
		gap: var(--space-md);
		padding: var(--space-md) var(--space-lg);
		background: var(--bg-root);
		border: 1px solid var(--border-subtle);
		border-radius: var(--radius-md);
		cursor: pointer;
		color: var(--text-primary);
		font-size: 0.9rem;
		width: 100%;
		text-align: left;
	}
	.workspace-item:hover {
		border-color: var(--accent);
		background: var(--bg-hover);
	}
	.ws-name {
		flex: 1;
		font-weight: 500;
	}
	.ws-role {
		font-size: 0.75rem;
		color: var(--text-tertiary);
		text-transform: capitalize;
	}
	.actions {
		display: flex;
		flex-direction: column;
		gap: var(--space-sm);
	}
	.hint {
		color: var(--text-tertiary);
		font-size: 0.85rem;
		text-align: center;
		margin-bottom: var(--space-xl);
	}
	.error {
		color: var(--red);
		font-size: 0.85rem;
		text-align: center;
		margin-bottom: var(--space-xl);
	}
</style>
