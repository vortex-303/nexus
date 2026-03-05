<script lang="ts">
	import { page } from '$app/state';
	import { goto } from '$app/navigation';
	import { onMount, onDestroy, tick } from 'svelte';
	import { getCurrentUser, listChannels, createChannel, getMessages, getBrainSettings, updateBrainSettings, getBrainDefinition, updateBrainDefinition, listMemories, deleteMemory, clearMemories, getPinnedModels } from '$lib/api';
	import { connect, disconnect, onMessage, sendMessage, sendTyping } from '$lib/ws';
	import { channels, members, messages, activeChannel } from '$lib/stores/workspace';
	import type { Channel } from '$lib/stores/workspace';
	import BrainAvatar from '$lib/components/BrainAvatar.svelte';

	let slug = $derived(page.params.slug);
	let currentUser = $state(getCurrentUser());
	let input = $state('');
	let messagesEl: HTMLElement;
	let brainState = $state<'idle' | 'thinking' | 'speaking'>('idle');
	let showSettings = $state(false);
	let isAdmin = $derived(currentUser?.role === 'admin');
	let lastTypingSent = 0;

	// Brain settings state
	let brainSettings = $state<any>({});
	let brainApiKey = $state('');
	let brainModel = $state('anthropic/claude-sonnet-4');
	let brainSaving = $state(false);
	let pinnedModels = $state<any[]>([]);

	let unsubWS: (() => void) | null = null;

	// DM helper
	function dmChannelName(myId: string, theirId: string): string {
		const sorted = [myId, theirId].sort();
		return `dm-${sorted[0]}-${sorted[1]}`;
	}

	function formatTime(iso: string) {
		const d = new Date(iso);
		const now = new Date();
		const isToday = d.toDateString() === now.toDateString();
		if (isToday) return d.toLocaleTimeString([], { hour: '2-digit', minute: '2-digit' });
		return d.toLocaleDateString([], { month: 'short', day: 'numeric' }) + ' ' + d.toLocaleTimeString([], { hour: '2-digit', minute: '2-digit' });
	}

	function isImageMime(mime: string): boolean {
		return mime?.startsWith('image/');
	}

	onMount(async () => {
		try {
			connect();
			unsubWS = onMessage(handleWS);

			// Load channels and find/create Brain DM
			const data = await listChannels(slug);
			channels.set(data.channels || []);

			const myId = currentUser?.uid;
			if (myId) {
				const expectedName = dmChannelName(myId, 'brain');
				let brainDM = (data.channels || []).find((ch: any) => ch.name === expectedName);
				if (!brainDM) {
					brainDM = await createChannel(slug, expectedName, 'dm');
					channels.update(chs => [...chs, brainDM]);
				}
				activeChannel.set(brainDM);
				const msgData = await getMessages(slug, brainDM.id);
				messages.set(msgData.messages || []);
				await tick();
				scrollToBottom();
			}

			// Load settings for sidebar
			try {
				brainSettings = await getBrainSettings(slug);
				if (brainSettings.model) brainModel = brainSettings.model;
				pinnedModels = await getPinnedModels(slug) || [];
			} catch {}
		} catch {
			goto(`/w/${slug}`);
		}
	});

	onDestroy(() => {
		if (unsubWS) unsubWS();
		disconnect();
	});

	function handleWS(type: string, payload: any) {
		if (type === 'message.new') {
			let current: Channel | null = null;
			activeChannel.subscribe(v => current = v)();
			if (payload.channel_id === current?.id) {
				messages.update(msgs => [...msgs, payload]);
				// Brain responding
				if (payload.sender_id === 'brain') {
					brainState = 'speaking';
					setTimeout(() => brainState = 'idle', 2000);
				}
				scrollToBottom();
			}
		} else if (type === 'message.edited') {
			messages.update(msgs => msgs.map(m =>
				m.id === payload.message_id ? { ...m, content: payload.content, edited_at: payload.edited_at } : m
			));
		} else if (type === 'message.deleted') {
			messages.update(msgs => msgs.filter(m => m.id !== payload.message_id));
		} else if (type === 'agent.state') {
			if (payload.agent_id === 'brain' || payload.agent_name === 'Brain') {
				if (payload.state === 'thinking' || payload.state === 'working') {
					brainState = 'thinking';
				} else if (payload.state === 'idle') {
					brainState = 'idle';
				}
			}
		}
	}

	function handleSend() {
		let current: Channel | null = null;
		activeChannel.subscribe(v => current = v)();
		if (!input.trim() || !current) return;
		sendMessage(current.id, input.trim());
		input = '';
		brainState = 'thinking';
	}

	function handleKeydown(e: KeyboardEvent) {
		if (e.key === 'Enter' && !e.shiftKey) {
			e.preventDefault();
			handleSend();
		}
		// Typing indicator
		const now = Date.now();
		if (now - lastTypingSent > 2000) {
			let current: Channel | null = null;
			activeChannel.subscribe(v => current = v)();
			if (current) sendTyping(current.id);
			lastTypingSent = now;
		}
	}

	function scrollToBottom() {
		requestAnimationFrame(() => {
			if (messagesEl) messagesEl.scrollTop = messagesEl.scrollHeight;
		});
	}

	async function saveBrainSettings() {
		brainSaving = true;
		try {
			const updates: Record<string, string> = { model: brainModel };
			if (brainApiKey) updates.api_key = brainApiKey;
			await updateBrainSettings(slug, updates);
			brainSettings = await getBrainSettings(slug);
			brainApiKey = '';
		} catch (e: any) {
			alert(e.message);
		}
		brainSaving = false;
	}
</script>

<div class="brain-page">
	<!-- Particle background -->
	<div class="particles">
		{#each Array(20) as _, i}
			<div class="particle" style="--delay: {i * 0.7}s; --x: {Math.random() * 100}%; --y: {Math.random() * 100}%; --size: {1 + Math.random() * 2}px;"></div>
		{/each}
	</div>

	<!-- Header -->
	<header class="brain-header">
		<button class="back-btn" onclick={() => goto(`/w/${slug}`)}>
			<svg width="16" height="16" viewBox="0 0 16 16" fill="none">
				<path d="M10 3L5 8L10 13" stroke="currentColor" stroke-width="1.5" stroke-linecap="round" stroke-linejoin="round"/>
			</svg>
		</button>
		<BrainAvatar state={brainState} />
		<div class="brain-info">
			<h1>Brain</h1>
			<span class="brain-status" data-state={brainState}>
				{brainState === 'thinking' ? 'Thinking...' : brainState === 'speaking' ? 'Responding...' : 'Online'}
			</span>
		</div>
		{#if isAdmin}
			<button class="settings-btn" onclick={() => showSettings = !showSettings} title="Brain Settings">
				<svg width="18" height="18" viewBox="0 0 18 18" fill="none">
					<path d="M9 11.5a2.5 2.5 0 100-5 2.5 2.5 0 000 5z" stroke="currentColor" stroke-width="1.2"/>
					<path d="M14.5 9a5.5 5.5 0 11-11 0 5.5 5.5 0 0111 0z" stroke="currentColor" stroke-width="1.2"/>
				</svg>
			</button>
		{/if}
	</header>

	<div class="brain-content">
		<!-- Messages -->
		<div class="messages-container" bind:this={messagesEl}>
			<div class="messages-intro">
				<div class="intro-avatar">
					<BrainAvatar state="idle" />
				</div>
				<h2>Chat with Brain</h2>
				<p>Your AI assistant. Ask questions, create tasks, search the web, or just chat.</p>
			</div>

			{#each $messages as msg (msg.id)}
				{@const isBrain = msg.sender_id === 'brain'}
				<div class="message" class:brain={isBrain} class:user={!isBrain}>
					{#if isBrain}
						<div class="msg-avatar-col">
							<div class="msg-brain-icon">
								<svg width="20" height="20" viewBox="0 0 64 64" fill="none">
									<circle cx="32" cy="32" r="5" fill="var(--accent)"/>
									<circle cx="32" cy="16" r="2" fill="var(--accent)" opacity="0.6"/>
									<circle cx="48" cy="32" r="2" fill="var(--accent)" opacity="0.6"/>
									<circle cx="32" cy="48" r="2" fill="var(--accent)" opacity="0.6"/>
									<circle cx="16" cy="32" r="2" fill="var(--accent)" opacity="0.6"/>
								</svg>
							</div>
						</div>
					{/if}
					<div class="msg-bubble">
						<div class="msg-content">{@html msg.content}</div>
						<span class="msg-time">{formatTime(msg.created_at)}</span>
					</div>
				</div>
			{/each}

			{#if brainState === 'thinking'}
				<div class="message brain">
					<div class="msg-avatar-col">
						<div class="msg-brain-icon">
							<svg width="20" height="20" viewBox="0 0 64 64" fill="none">
								<circle cx="32" cy="32" r="5" fill="var(--accent)"/>
							</svg>
						</div>
					</div>
					<div class="msg-bubble typing-bubble">
						<div class="typing-indicator">
							<span class="dot"></span>
							<span class="dot"></span>
							<span class="dot"></span>
						</div>
					</div>
				</div>
			{/if}
		</div>

		<!-- Settings Panel -->
		{#if showSettings}
			<div class="settings-panel">
				<div class="settings-header">
					<h3>Brain Settings</h3>
					<button class="settings-close" onclick={() => showSettings = false}>&times;</button>
				</div>
				<div class="settings-body">
					<div class="setting-field">
						<label>API Key</label>
						{#if brainSettings.api_key_set === 'true'}
							<div class="key-status">Key set ({brainSettings.api_key_masked})</div>
						{/if}
						<input type="password" placeholder="sk-or-v1-..." bind:value={brainApiKey} />
					</div>
					<div class="setting-field">
						<label>Model</label>
						<select bind:value={brainModel}>
							{#if pinnedModels.length > 0}
								{#each pinnedModels as m}
									<option value={m.id}>{m.display_name}</option>
								{/each}
							{:else}
								<option value="anthropic/claude-sonnet-4">Claude Sonnet 4</option>
								<option value="anthropic/claude-haiku-4">Claude Haiku 4</option>
								<option value="openai/gpt-4o">GPT-4o</option>
								<option value="google/gemini-2.5-flash">Gemini 2.5 Flash</option>
							{/if}
						</select>
					</div>
					<button class="btn-save" onclick={saveBrainSettings} disabled={brainSaving}>
						{brainSaving ? 'Saving...' : 'Save'}
					</button>
				</div>
			</div>
		{/if}
	</div>

	<!-- Input -->
	<div class="input-area">
		<div class="input-wrap">
			<textarea
				placeholder="Message Brain..."
				bind:value={input}
				onkeydown={handleKeydown}
				rows="1"
			></textarea>
			<button class="send-btn" onclick={handleSend} disabled={!input.trim()}>
				<svg width="18" height="18" viewBox="0 0 18 18" fill="none">
					<path d="M2 9L16 2L9 16L8 10L2 9Z" fill="currentColor"/>
				</svg>
			</button>
		</div>
	</div>
</div>

<style>
	.brain-page {
		display: flex;
		flex-direction: column;
		height: 100vh;
		background: var(--bg-root);
		color: var(--text-primary);
		font-family: var(--font-sans);
		position: relative;
		overflow: hidden;
	}

	/* Particles */
	.particles {
		position: absolute;
		inset: 0;
		pointer-events: none;
		z-index: 0;
		overflow: hidden;
	}
	.particle {
		position: absolute;
		width: var(--size);
		height: var(--size);
		background: var(--accent);
		border-radius: 50%;
		left: var(--x);
		top: var(--y);
		opacity: 0;
		animation: particleFloat 12s ease-in-out var(--delay) infinite;
	}
	@keyframes particleFloat {
		0% { opacity: 0; transform: translateY(0) scale(1); }
		20% { opacity: 0.4; }
		80% { opacity: 0.2; }
		100% { opacity: 0; transform: translateY(-80px) scale(0.5); }
	}

	/* Header */
	.brain-header {
		display: flex;
		align-items: center;
		gap: var(--space-md);
		padding: var(--space-md) var(--space-xl);
		border-bottom: 1px solid var(--border-subtle);
		background: rgba(14, 14, 18, 0.8);
		backdrop-filter: blur(12px);
		z-index: 2;
		flex-shrink: 0;
	}
	.back-btn {
		display: flex;
		align-items: center;
		justify-content: center;
		width: 28px;
		height: 28px;
		background: none;
		border: 1px solid var(--border-default);
		border-radius: var(--radius-md);
		color: var(--text-secondary);
		cursor: pointer;
	}
	.back-btn:hover { color: var(--text-primary); border-color: var(--border-strong); }
	.brain-info {
		flex: 1;
	}
	.brain-info h1 {
		font-size: var(--text-lg);
		font-weight: 700;
		margin: 0;
		line-height: 1;
	}
	.brain-status {
		font-size: var(--text-xs);
		color: var(--text-tertiary);
	}
	.brain-status[data-state="thinking"] {
		color: var(--accent);
		animation: statusPulse 1.5s ease-in-out infinite;
	}
	@keyframes statusPulse {
		0%, 100% { opacity: 1; }
		50% { opacity: 0.5; }
	}
	.settings-btn {
		display: flex;
		align-items: center;
		justify-content: center;
		width: 32px;
		height: 32px;
		background: none;
		border: 1px solid var(--border-default);
		border-radius: var(--radius-md);
		color: var(--text-secondary);
		cursor: pointer;
	}
	.settings-btn:hover { color: var(--accent); border-color: var(--accent-border); }

	/* Content area */
	.brain-content {
		flex: 1;
		display: flex;
		overflow: hidden;
		z-index: 1;
		position: relative;
	}

	/* Messages */
	.messages-container {
		flex: 1;
		overflow-y: auto;
		padding: var(--space-xl);
		display: flex;
		flex-direction: column;
		gap: var(--space-md);
	}
	.messages-intro {
		text-align: center;
		padding: 3rem 1rem 2rem;
	}
	.intro-avatar {
		display: flex;
		justify-content: center;
		margin-bottom: var(--space-lg);
	}
	.messages-intro h2 {
		font-size: var(--text-xl);
		font-weight: 700;
		margin: 0 0 var(--space-sm);
	}
	.messages-intro p {
		color: var(--text-tertiary);
		font-size: var(--text-sm);
		margin: 0;
	}

	/* Message bubbles */
	.message {
		display: flex;
		gap: var(--space-sm);
		max-width: 720px;
	}
	.message.user {
		align-self: flex-end;
		flex-direction: row-reverse;
	}
	.message.brain {
		align-self: flex-start;
	}
	.msg-avatar-col {
		flex-shrink: 0;
		padding-top: 4px;
	}
	.msg-brain-icon {
		width: 28px;
		height: 28px;
		display: flex;
		align-items: center;
		justify-content: center;
		border-radius: 50%;
		background: var(--accent-glow);
		border: 1px solid var(--accent-border);
	}
	.msg-bubble {
		padding: var(--space-md) var(--space-lg);
		border-radius: var(--radius-lg);
		position: relative;
		line-height: 1.5;
		font-size: var(--text-sm);
	}
	.message.brain .msg-bubble {
		background: rgba(20, 20, 26, 0.7);
		backdrop-filter: blur(8px);
		border: 1px solid var(--accent-border);
		box-shadow: 0 0 15px rgba(249, 115, 22, 0.05), inset 0 0 20px rgba(249, 115, 22, 0.02);
	}
	.message.user .msg-bubble {
		background: rgba(30, 30, 38, 0.8);
		backdrop-filter: blur(8px);
		border: 1px solid var(--border-default);
	}
	.msg-content {
		word-break: break-word;
	}
	.msg-content :global(a) {
		color: var(--accent);
		text-decoration: none;
	}
	.msg-content :global(a:hover) { text-decoration: underline; }
	.msg-content :global(code) {
		background: var(--bg-raised);
		padding: 1px 4px;
		border-radius: var(--radius-sm);
		font-family: var(--font-mono);
		font-size: 0.85em;
	}
	.msg-content :global(pre) {
		background: var(--bg-root);
		border: 1px solid var(--border-subtle);
		border-radius: var(--radius-md);
		padding: var(--space-md);
		overflow-x: auto;
		margin: var(--space-sm) 0;
	}
	.msg-content :global(pre code) {
		background: none;
		padding: 0;
	}
	.msg-time {
		display: block;
		font-size: 10px;
		color: var(--text-tertiary);
		margin-top: var(--space-xs);
		opacity: 0;
		transition: opacity 150ms;
	}
	.msg-bubble:hover .msg-time { opacity: 1; }

	/* Typing indicator */
	.typing-bubble {
		padding: var(--space-md) var(--space-lg);
	}
	.typing-indicator {
		display: flex;
		gap: 4px;
		align-items: center;
	}
	.dot {
		width: 6px;
		height: 6px;
		background: var(--accent);
		border-radius: 50%;
		animation: dotBounce 1.4s ease-in-out infinite;
	}
	.dot:nth-child(2) { animation-delay: 0.2s; }
	.dot:nth-child(3) { animation-delay: 0.4s; }
	@keyframes dotBounce {
		0%, 60%, 100% { transform: translateY(0); opacity: 0.4; }
		30% { transform: translateY(-6px); opacity: 1; }
	}

	/* Settings panel */
	.settings-panel {
		width: 320px;
		border-left: 1px solid var(--border-subtle);
		background: rgba(14, 14, 18, 0.95);
		backdrop-filter: blur(12px);
		display: flex;
		flex-direction: column;
		overflow-y: auto;
	}
	.settings-header {
		display: flex;
		align-items: center;
		justify-content: space-between;
		padding: var(--space-md) var(--space-lg);
		border-bottom: 1px solid var(--border-subtle);
	}
	.settings-header h3 { font-size: var(--text-base); margin: 0; }
	.settings-close {
		background: none;
		border: none;
		color: var(--text-secondary);
		font-size: 18px;
		cursor: pointer;
	}
	.settings-body {
		padding: var(--space-lg);
		display: flex;
		flex-direction: column;
		gap: var(--space-lg);
	}
	.setting-field {
		display: flex;
		flex-direction: column;
		gap: var(--space-xs);
	}
	.setting-field label {
		font-size: var(--text-xs);
		font-weight: 600;
		text-transform: uppercase;
		letter-spacing: 0.05em;
		color: var(--text-tertiary);
	}
	.setting-field input, .setting-field select {
		padding: 8px 10px;
		background: var(--bg-input);
		color: var(--text-primary);
		border: 1px solid var(--border-default);
		border-radius: var(--radius-md);
		font-size: var(--text-sm);
		font-family: inherit;
	}
	.setting-field input:focus, .setting-field select:focus {
		outline: none;
		border-color: var(--accent);
	}
	.key-status {
		font-size: var(--text-xs);
		color: var(--green);
	}
	.btn-save {
		padding: 8px 16px;
		background: var(--accent);
		color: var(--text-inverse);
		border: none;
		border-radius: var(--radius-md);
		font-size: var(--text-sm);
		font-weight: 600;
		cursor: pointer;
		font-family: inherit;
	}
	.btn-save:hover { background: var(--accent-hover); }
	.btn-save:disabled { opacity: 0.5; cursor: default; }

	/* Input area */
	.input-area {
		padding: var(--space-md) var(--space-xl) var(--space-lg);
		z-index: 2;
		flex-shrink: 0;
	}
	.input-wrap {
		display: flex;
		align-items: flex-end;
		gap: var(--space-sm);
		background: rgba(17, 17, 22, 0.9);
		backdrop-filter: blur(8px);
		border: 1px solid var(--border-default);
		border-radius: var(--radius-lg);
		padding: var(--space-sm) var(--space-sm) var(--space-sm) var(--space-md);
		transition: border-color 200ms, box-shadow 200ms;
	}
	.input-wrap:focus-within {
		border-color: var(--accent);
		box-shadow: 0 0 20px rgba(249, 115, 22, 0.1), 0 0 40px rgba(249, 115, 22, 0.05);
	}
	.input-wrap textarea {
		flex: 1;
		background: none;
		border: none;
		color: var(--text-primary);
		font-size: var(--text-sm);
		font-family: inherit;
		resize: none;
		min-height: 20px;
		max-height: 120px;
		padding: 6px 0;
	}
	.input-wrap textarea:focus { outline: none; }
	.input-wrap textarea::placeholder { color: var(--text-tertiary); }
	.send-btn {
		display: flex;
		align-items: center;
		justify-content: center;
		width: 32px;
		height: 32px;
		background: var(--accent);
		border: none;
		border-radius: var(--radius-md);
		color: var(--text-inverse);
		cursor: pointer;
		flex-shrink: 0;
		transition: background 150ms;
	}
	.send-btn:hover { background: var(--accent-hover); }
	.send-btn:disabled { opacity: 0.3; cursor: default; }
</style>
