<script lang="ts">
	import { goto } from '$app/navigation';
	import { createWorkspace, getWorkspaceSlug, login, joinByCode, setToken, setWorkspaceSlug, getCurrentUser } from '$lib/api';
	import { onMount } from 'svelte';

	let mode = $state<'create' | 'login' | 'join'>('create');
	let workspaceName = $state('');
	let displayName = $state('');
	let email = $state('');
	let password = $state('');
	let inviteCode = $state('');
	let loading = $state(false);
	let error = $state('');

	onMount(() => {
		const slug = getWorkspaceSlug();
		if (slug) goto(`/w/${slug}`);
	});

	async function handleCreate() {
		if (!workspaceName.trim()) { error = 'Give your workspace a name'; return; }
		if (!displayName.trim()) { error = 'Enter your name'; return; }
		loading = true;
		error = '';
		try {
			const data = await createWorkspace(displayName.trim(), workspaceName.trim(), email.trim() || undefined, password || undefined);
			goto(`/w/${data.slug}`);
		} catch (e: any) {
			error = e.message;
		} finally {
			loading = false;
		}
	}

	async function handleLogin() {
		if (!email.trim() || !password) { error = 'Email and password required'; return; }
		loading = true;
		error = '';
		try {
			const data = await login(email.trim(), password);
			const user = getCurrentUser();
			if (user?.sa && !user?.ws) {
				goto('/admin');
			} else if (user?.ws) {
				goto(`/w/${user.ws}`);
			} else {
				goto('/workspaces');
			}
		} catch (e: any) {
			error = e.message;
		} finally {
			loading = false;
		}
	}

	async function handleJoin() {
		if (!inviteCode.trim()) { error = 'Enter your invite code'; return; }
		if (!displayName.trim()) { error = 'Enter your name'; return; }
		loading = true;
		error = '';
		try {
			const data = await joinByCode(inviteCode.trim(), displayName.trim());
			goto(`/w/${data.slug}`);
		} catch (e: any) {
			error = e.message;
		} finally {
			loading = false;
		}
	}

	function handleKeydown(e: KeyboardEvent) {
		if (e.key === 'Enter') {
			if (mode === 'create') handleCreate();
			else if (mode === 'login') handleLogin();
			else handleJoin();
		}
	}
</script>

<div class="landing">
	<!-- Ambient glow -->
	<div class="glow glow-1"></div>
	<div class="glow glow-2"></div>

	<div class="hero">
		<div class="logo-mark">
			<div class="logo-icon">
				<svg width="40" height="40" viewBox="0 0 40 40" fill="none">
					<path d="M8 12L20 4L32 12V28L20 36L8 28V12Z" stroke="var(--accent)" stroke-width="2" fill="none"/>
					<circle cx="20" cy="20" r="4" fill="var(--accent)"/>
					<line x1="20" y1="16" x2="20" y2="8" stroke="var(--accent)" stroke-width="1.5" opacity="0.6"/>
					<line x1="23.5" y1="22" x2="29" y2="26" stroke="var(--accent)" stroke-width="1.5" opacity="0.6"/>
					<line x1="16.5" y1="22" x2="11" y2="26" stroke="var(--accent)" stroke-width="1.5" opacity="0.6"/>
				</svg>
			</div>
			<h1>nexus</h1>
		</div>

		<p class="tagline">Your team's brain.</p>
		<p class="subtitle">Chat, tasks, docs, and an AI that never forgets — in one workspace.</p>

		<div class="form-card">
			{#if mode === 'create'}
			<div class="form-inner">
				<input
					type="text"
					placeholder="Workspace name"
					bind:value={workspaceName}
					onkeydown={handleKeydown}
					maxlength="50"
					class="name-input"
				/>
				<input
					type="text"
					placeholder="Your name"
					bind:value={displayName}
					onkeydown={handleKeydown}
					maxlength="50"
					class="name-input secondary-input"
				/>

				<div class="optional-auth">
					<p class="optional-label">Add email to access from other devices</p>
					<input
						type="email"
						placeholder="Email"
						bind:value={email}
						onkeydown={handleKeydown}
						class="auth-input"
					/>
					<input
						type="password"
						placeholder="Password"
						bind:value={password}
						onkeydown={handleKeydown}
						class="auth-input"
					/>
				</div>

				<button onclick={handleCreate} disabled={loading} class="btn btn-primary btn-launch">
					{#if loading}
						<span class="spinner"></span>
						Creating...
					{:else}
						Create Workspace
						<svg width="16" height="16" viewBox="0 0 16 16" fill="currentColor">
							<path d="M6.5 3.5L11 8L6.5 12.5" stroke="currentColor" stroke-width="2" fill="none" stroke-linecap="round" stroke-linejoin="round"/>
						</svg>
					{/if}
				</button>
			</div>
			{:else if mode === 'login'}
			<div class="form-inner">
				<input
					type="email"
					placeholder="Email"
					bind:value={email}
					onkeydown={handleKeydown}
					class="name-input"
				/>
				<input
					type="password"
					placeholder="Password"
					bind:value={password}
					onkeydown={handleKeydown}
					class="name-input"
				/>
				<button onclick={handleLogin} disabled={loading} class="btn btn-primary btn-launch">
					{#if loading}
						<span class="spinner"></span>
						Logging in...
					{:else}
						Log In
						<svg width="16" height="16" viewBox="0 0 16 16" fill="currentColor">
							<path d="M6.5 3.5L11 8L6.5 12.5" stroke="currentColor" stroke-width="2" fill="none" stroke-linecap="round" stroke-linejoin="round"/>
						</svg>
					{/if}
				</button>
			</div>
			{:else}
			<div class="form-inner">
				<input
					type="text"
					placeholder="NX-XXXX"
					bind:value={inviteCode}
					onkeydown={handleKeydown}
					maxlength="7"
					class="name-input invite-code-input"
				/>
				<input
					type="text"
					placeholder="Your name"
					bind:value={displayName}
					onkeydown={handleKeydown}
					maxlength="50"
					class="name-input secondary-input"
				/>
				<button onclick={handleJoin} disabled={loading} class="btn btn-primary btn-launch">
					{#if loading}
						<span class="spinner"></span>
						Joining...
					{:else}
						Join Workspace
						<svg width="16" height="16" viewBox="0 0 16 16" fill="currentColor">
							<path d="M6.5 3.5L11 8L6.5 12.5" stroke="currentColor" stroke-width="2" fill="none" stroke-linecap="round" stroke-linejoin="round"/>
						</svg>
					{/if}
				</button>
			</div>
			{/if}

			{#if error}
				<p class="error">{error}</p>
			{/if}
		</div>

		<p class="mode-toggle">
			{#if mode === 'create'}
				Have an invite code? <button class="link-btn" onclick={() => { mode = 'join'; error = ''; }}>Join workspace</button>
				&nbsp;·&nbsp;
				<button class="link-btn" onclick={() => { mode = 'login'; error = ''; }}>Log in</button>
			{:else if mode === 'login'}
				New here? <button class="link-btn" onclick={() => { mode = 'create'; error = ''; }}>Create a workspace</button>
				&nbsp;·&nbsp;
				Have a code? <button class="link-btn" onclick={() => { mode = 'join'; error = ''; }}>Join</button>
			{:else}
				New here? <button class="link-btn" onclick={() => { mode = 'create'; error = ''; }}>Create a workspace</button>
				&nbsp;·&nbsp;
				<button class="link-btn" onclick={() => { mode = 'login'; error = ''; }}>Log in</button>
			{/if}
		</p>

		<div class="features">
			<div class="feature">
				<span class="feature-icon">&#9670;</span>
				<span>Real-time chat</span>
			</div>
			<div class="feature-dot"></div>
			<div class="feature">
				<span class="feature-icon">&#9670;</span>
				<span>AI Brain</span>
			</div>
			<div class="feature-dot"></div>
			<div class="feature">
				<span class="feature-icon">&#9670;</span>
				<span>Tasks & Docs</span>
			</div>
		</div>
	</div>
</div>

<style>
	.landing {
		min-height: 100vh;
		display: flex;
		align-items: center;
		justify-content: center;
		position: relative;
		overflow: hidden;
	}

	/* Ambient orange glow blobs */
	.glow {
		position: absolute;
		border-radius: 50%;
		filter: blur(120px);
		pointer-events: none;
		opacity: 0.07;
	}
	.glow-1 {
		width: 600px; height: 600px;
		background: var(--accent);
		top: -200px; right: -100px;
	}
	.glow-2 {
		width: 400px; height: 400px;
		background: var(--orange-600);
		bottom: -150px; left: -100px;
	}

	.hero {
		text-align: center;
		padding: var(--space-2xl);
		max-width: 480px;
		position: relative;
		z-index: 1;
	}

	.logo-mark {
		display: flex;
		align-items: center;
		justify-content: center;
		gap: var(--space-md);
		margin-bottom: var(--space-xl);
	}
	.logo-icon {
		display: flex;
		filter: drop-shadow(0 0 8px rgba(249,115,22,0.4));
	}
	h1 {
		font-size: var(--text-4xl);
		font-weight: 800;
		letter-spacing: -0.04em;
		background: linear-gradient(135deg, var(--text-primary) 0%, var(--text-secondary) 100%);
		-webkit-background-clip: text;
		-webkit-text-fill-color: transparent;
		background-clip: text;
	}

	.tagline {
		font-size: var(--text-xl);
		font-weight: 600;
		color: var(--accent);
		margin-bottom: var(--space-sm);
		letter-spacing: -0.01em;
	}

	.subtitle {
		color: var(--text-secondary);
		font-size: var(--text-base);
		margin-bottom: var(--space-2xl);
		line-height: 1.6;
	}

	.form-card {
		background: var(--bg-surface);
		border: 1px solid var(--border-subtle);
		border-radius: var(--radius-xl);
		padding: var(--space-xl);
		margin-bottom: var(--space-lg);
		box-shadow: var(--shadow-lg), inset 0 1px 0 rgba(255,255,255,0.03);
	}

	.form-inner {
		display: flex;
		flex-direction: column;
		gap: var(--space-md);
	}

	.name-input {
		text-align: center;
		font-size: var(--text-lg) !important;
		padding: var(--space-md) var(--space-lg) !important;
		background: var(--bg-root) !important;
		border: 1px solid var(--border-default) !important;
	}
	.secondary-input {
		font-size: var(--text-base) !important;
	}

	.optional-auth {
		display: flex;
		flex-direction: column;
		gap: var(--space-sm);
		padding-top: var(--space-sm);
		border-top: 1px solid var(--border-subtle);
	}
	.optional-label {
		font-size: var(--text-sm);
		color: var(--text-tertiary);
		margin: 0;
	}
	.auth-input {
		text-align: center;
		font-size: var(--text-base) !important;
		padding: var(--space-sm) var(--space-lg) !important;
		background: var(--bg-root) !important;
		border: 1px solid var(--border-default) !important;
	}

	.btn-launch {
		padding: var(--space-md) var(--space-xl);
		font-size: var(--text-lg);
		border-radius: var(--radius-lg);
		font-weight: 700;
	}

	.spinner {
		width: 16px;
		height: 16px;
		border: 2px solid transparent;
		border-top-color: currentColor;
		border-radius: 50%;
		animation: spin 0.6s linear infinite;
	}
	@keyframes spin { to { transform: rotate(360deg); } }

	.error {
		color: var(--red);
		font-size: var(--text-sm);
		margin-top: var(--space-md);
	}

	.mode-toggle {
		color: var(--text-tertiary);
		font-size: var(--text-sm);
		margin-bottom: var(--space-2xl);
	}
	.link-btn {
		background: none;
		border: none;
		color: var(--accent);
		cursor: pointer;
		font-size: inherit;
		padding: 0;
		text-decoration: underline;
	}
	.link-btn:hover {
		color: var(--text-primary);
	}

	.features {
		display: flex;
		align-items: center;
		justify-content: center;
		gap: var(--space-md);
		color: var(--text-tertiary);
		font-size: var(--text-sm);
	}
	.feature {
		display: flex;
		align-items: center;
		gap: var(--space-xs);
	}
	.feature-icon {
		color: var(--accent);
		font-size: 8px;
		opacity: 0.7;
	}
	.feature-dot {
		width: 3px;
		height: 3px;
		border-radius: 50%;
		background: var(--border-strong);
	}

	.invite-code-input {
		text-transform: uppercase;
		letter-spacing: 0.15em;
		font-weight: 700 !important;
		font-size: var(--text-xl) !important;
	}

	@media (max-width: 480px) {
		h1 { font-size: var(--text-3xl); }
		.hero { padding: var(--space-lg); }
	}
</style>
