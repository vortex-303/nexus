<script lang="ts">
	import { page } from '$app/state';
	import { goto } from '$app/navigation';
	import { onMount, onDestroy, tick } from 'svelte';
	import { getCurrentUser, listChannels, createChannel, getMessages, createDoc, getBrainSettings, saveBrainMessage, getWebLLMContext } from '$lib/api';
	import { connect, disconnect, onMessage, sendMessage, sendTyping } from '$lib/ws';
	import { channels, members, messages, activeChannel } from '$lib/stores/workspace';
	import type { Channel } from '$lib/stores/workspace';
	import BrainAvatar from '$lib/components/BrainAvatar.svelte';
	import { safeMarkdownToHtml } from '$lib/editor/markdown-utils';

	let slug = $derived(page.params.slug);
	let currentUser = $state(getCurrentUser());
	let input = $state('');
	let messagesEl: HTMLElement;
	let brainState = $state<'idle' | 'thinking' | 'speaking'>('idle');
	let lastTypingSent = 0;

	// V2 state
	let mode = $state<'v1' | 'v2'>('v2');
	let v2State = $state<'idle' | 'thinking' | 'response'>('idle');
	let v2Question = $state('');
	let v2Response = $state('');
	let v2CopyFeedback = $state(false);
	let v2SaveFeedback = $state(false);

	// Engine state
	let engineLabel = $state('');
	let standardChatEnabled = $state(true);
	let llmEnabled = $state(true);

	// WebLLM state
	let webllmEnabled = $state(false);
	let webllmModel = $state('');
	let webllmModule: typeof import('$lib/webllm.svelte.ts') | null = null;
	let userWebLLMEnabled = $state(false); // per-user localStorage opt-in

	// V2 mouse tracking + grid canvas animation
	let mouseX = $state(0);
	let mouseY = $state(0);
	let canvasEl: HTMLCanvasElement;
	let animFrame: number;

	const GRID = 40;
	const ACCENT = [249, 115, 22];

	interface GridNode {
		x: number; y: number;
		phase: number; speed: number; size: number;
		life: number; maxLife: number;
		fadeIn: number;
	}

	interface GridWalker {
		x: number; y: number;
		dx: number; dy: number;
		horizontal: boolean;
		turnChance: number;
		size: number; opacity: number;
		tail: { x: number; y: number }[];
		life: number; maxLife: number;
	}

	interface GridConnection {
		a: number; b: number;
		flash: number; nextFlash: number;
	}

	let gridNodes: GridNode[] = [];
	let gridWalkers: GridWalker[] = [];
	let gridConnections: GridConnection[] = [];
	let ambientNodes: GridNode[] = [];
	let ambientConnections: GridConnection[] = [];
	let gTime = 0;

	function snapToGrid(v: number): number {
		return Math.round(v / GRID) * GRID;
	}

	function initAmbientNodes(w: number, h: number) {
		ambientNodes = [];
		const cols = Math.floor(w / GRID);
		const rows = Math.floor(h / GRID);
		for (let y = 0; y <= rows; y++) {
			for (let x = 0; x <= cols; x++) {
				if (Math.random() < 0.06) {
					ambientNodes.push({
						x: x * GRID, y: y * GRID,
						phase: Math.random() * Math.PI * 2,
						speed: 0.2 + Math.random() * 0.5,
						size: 1 + Math.random() * 1,
						life: 0, maxLife: Infinity, fadeIn: 1,
					});
				}
			}
		}
		// Ambient connections
		ambientConnections = [];
		for (let i = 0; i < ambientNodes.length; i++) {
			for (let j = i + 1; j < ambientNodes.length; j++) {
				const dx = ambientNodes[i].x - ambientNodes[j].x;
				const dy = ambientNodes[i].y - ambientNodes[j].y;
				if (Math.sqrt(dx * dx + dy * dy) <= GRID * 3) {
					ambientConnections.push({ a: i, b: j, flash: 0, nextFlash: 3 + Math.random() * 12 });
				}
			}
		}
	}

	function spawnMouseNodes(mx: number, my: number) {
		// Find grid intersections near mouse, seed nodes there
		const cx = snapToGrid(mx);
		const cy = snapToGrid(my);
		const range = 4; // grid cells radius
		for (let dy = -range; dy <= range; dy++) {
			for (let dx = -range; dx <= range; dx++) {
				const gx = cx + dx * GRID;
				const gy = cy + dy * GRID;
				const dist = Math.sqrt((gx - mx) ** 2 + (gy - my) ** 2);
				if (dist > range * GRID) continue;
				// Don't duplicate
				if (gridNodes.some(n => n.x === gx && n.y === gy)) continue;
				// Probability decreases with distance
				const prob = 0.15 * (1 - dist / (range * GRID));
				if (Math.random() < prob) {
					gridNodes.push({
						x: gx, y: gy,
						phase: Math.random() * Math.PI * 2,
						speed: 0.4 + Math.random() * 0.8,
						size: 1.5 + Math.random() * 1.5,
						life: 0, maxLife: 180 + Math.random() * 250,
						fadeIn: 0,
					});
				}
			}
		}
		// Cap nodes
		if (gridNodes.length > 50) gridNodes.splice(0, gridNodes.length - 50);
		// Rebuild connections
		rebuildConnections();
	}

	function spawnWalkerNearMouse(mx: number, my: number) {
		const horizontal = Math.random() > 0.5;
		const gx = snapToGrid(mx + (Math.random() - 0.5) * GRID * 6);
		const gy = snapToGrid(my + (Math.random() - 0.5) * GRID * 6);
		const speed = 0.5 + Math.random() * 1.5;
		gridWalkers.push({
			x: gx, y: gy,
			dx: horizontal ? (Math.random() > 0.5 ? speed : -speed) : 0,
			dy: horizontal ? 0 : (Math.random() > 0.5 ? speed : -speed),
			horizontal,
			turnChance: 0.012,
			size: 2 + Math.random(),
			opacity: 0.2 + Math.random() * 0.3,
			tail: [],
			life: 0,
			maxLife: 200 + Math.random() * 300,
		});
	}

	function rebuildConnections() {
		gridConnections = [];
		for (let i = 0; i < gridNodes.length; i++) {
			for (let j = i + 1; j < gridNodes.length; j++) {
				const dx = gridNodes[i].x - gridNodes[j].x;
				const dy = gridNodes[i].y - gridNodes[j].y;
				if (Math.sqrt(dx * dx + dy * dy) <= GRID * 2.5) {
					gridConnections.push({ a: i, b: j, flash: 0.8, nextFlash: 1 + Math.random() * 5 });
				}
			}
		}
	}

	let spawnTimer = 0;
	let walkerTimer = 0;

	function animateCanvas() {
		if (!canvasEl) { animFrame = requestAnimationFrame(animateCanvas); return; }
		const ctx = canvasEl.getContext('2d');
		if (!ctx) return;
		const w = canvasEl.width = canvasEl.offsetWidth;
		const h = canvasEl.height = canvasEl.offsetHeight;
		ctx.clearRect(0, 0, w, h);
		gTime += 0.016;
		spawnTimer += 0.016;
		walkerTimer += 0.016;

		// Spawn nodes and walkers near mouse periodically
		if (mouseX > 0 && mouseY > 0) {
			if (spawnTimer > 0.3) {
				spawnMouseNodes(mouseX, mouseY);
				spawnTimer = 0;
			}
			if (walkerTimer > 1.2 && gridWalkers.length < 12) {
				spawnWalkerNearMouse(mouseX, mouseY);
				walkerTimer = 0;
			}
		}

		// === AMBIENT: pulse nodes + connections ===
		for (const conn of ambientConnections) {
			conn.nextFlash -= 0.016;
			if (conn.nextFlash <= 0) { conn.flash = 1; conn.nextFlash = 5 + Math.random() * 12; }
			if (conn.flash > 0) {
				conn.flash -= 0.015;
				const a = ambientNodes[conn.a], b = ambientNodes[conn.b];
				ctx.beginPath();
				ctx.moveTo(a.x, a.y);
				if (a.x !== b.x && a.y !== b.y) ctx.lineTo(b.x, a.y); // L-shaped
				ctx.lineTo(b.x, b.y);
				ctx.strokeStyle = `rgba(${ACCENT[0]},${ACCENT[1]},${ACCENT[2]},${conn.flash * 0.06})`;
				ctx.lineWidth = 1;
				ctx.stroke();
			}
		}
		for (const nd of ambientNodes) {
			const pulse = (Math.sin(gTime * nd.speed + nd.phase) + 1) / 2;
			const alpha = 0.04 + pulse * 0.1;
			ctx.beginPath();
			ctx.arc(nd.x, nd.y, nd.size + pulse * 2, 0, Math.PI * 2);
			ctx.fillStyle = `rgba(${ACCENT[0]},${ACCENT[1]},${ACCENT[2]},${alpha * 0.3})`;
			ctx.fill();
			ctx.beginPath();
			ctx.arc(nd.x, nd.y, nd.size, 0, Math.PI * 2);
			ctx.fillStyle = `rgba(${ACCENT[0]},${ACCENT[1]},${ACCENT[2]},${alpha})`;
			ctx.fill();
		}

		// === MOUSE-SEEDED: nodes + connections ===
		// Update + cull
		for (let i = gridNodes.length - 1; i >= 0; i--) {
			const n = gridNodes[i];
			n.life++;
			n.fadeIn = Math.min(n.fadeIn + 0.05, 1);
			if (n.life > n.maxLife) {
				gridNodes.splice(i, 1);
				rebuildConnections();
			}
		}

		// Draw connections (L-shaped along grid)
		for (const conn of gridConnections) {
			if (conn.a >= gridNodes.length || conn.b >= gridNodes.length) continue;
			conn.nextFlash -= 0.016;
			if (conn.nextFlash <= 0) { conn.flash = 1; conn.nextFlash = 2 + Math.random() * 6; }
			if (conn.flash > 0) {
				conn.flash -= 0.02;
				const a = gridNodes[conn.a], b = gridNodes[conn.b];
				const fadeA = a.life > a.maxLife * 0.7 ? 1 - (a.life - a.maxLife * 0.7) / (a.maxLife * 0.3) : a.fadeIn;
				const fadeB = b.life > b.maxLife * 0.7 ? 1 - (b.life - b.maxLife * 0.7) / (b.maxLife * 0.3) : b.fadeIn;
				const alpha = conn.flash * 0.15 * Math.min(fadeA, fadeB);
				ctx.beginPath();
				ctx.moveTo(a.x, a.y);
				if (a.x !== b.x && a.y !== b.y) ctx.lineTo(b.x, a.y);
				ctx.lineTo(b.x, b.y);
				ctx.strokeStyle = `rgba(${ACCENT[0]},${ACCENT[1]},${ACCENT[2]},${alpha})`;
				ctx.lineWidth = 1;
				ctx.stroke();
			}
		}

		// Draw mouse-seeded pulse nodes
		for (const nd of gridNodes) {
			const pulse = (Math.sin(gTime * nd.speed + nd.phase) + 1) / 2;
			let fade = nd.fadeIn;
			if (nd.life > nd.maxLife * 0.7) fade = Math.max(0, 1 - (nd.life - nd.maxLife * 0.7) / (nd.maxLife * 0.3));
			const alpha = (0.1 + pulse * 0.35) * fade;
			// Glow
			ctx.beginPath();
			ctx.arc(nd.x, nd.y, nd.size + pulse * 4, 0, Math.PI * 2);
			ctx.fillStyle = `rgba(${ACCENT[0]},${ACCENT[1]},${ACCENT[2]},${alpha * 0.3})`;
			ctx.fill();
			// Core
			ctx.beginPath();
			ctx.arc(nd.x, nd.y, nd.size, 0, Math.PI * 2);
			ctx.fillStyle = `rgba(${ACCENT[0]},${ACCENT[1]},${ACCENT[2]},${alpha})`;
			ctx.fill();
		}

		// === WALKERS: move along grid lines ===
		for (let i = gridWalkers.length - 1; i >= 0; i--) {
			const wk = gridWalkers[i];
			wk.x += wk.dx;
			wk.y += wk.dy;
			wk.life++;
			wk.tail.push({ x: wk.x, y: wk.y });
			if (wk.tail.length > 18) wk.tail.shift();

			// Turn at grid intersections
			const onGX = Math.abs(wk.x % GRID) < 1.5 || Math.abs(wk.x % GRID - GRID) < 1.5;
			const onGY = Math.abs(wk.y % GRID) < 1.5 || Math.abs(wk.y % GRID - GRID) < 1.5;
			if (onGX && onGY && Math.random() < wk.turnChance) {
				const speed = Math.sqrt(wk.dx * wk.dx + wk.dy * wk.dy);
				if (wk.horizontal) {
					wk.dx = 0; wk.dy = (Math.random() > 0.5 ? 1 : -1) * speed; wk.horizontal = false;
				} else {
					wk.dy = 0; wk.dx = (Math.random() > 0.5 ? 1 : -1) * speed; wk.horizontal = true;
				}
				wk.x = snapToGrid(wk.x); wk.y = snapToGrid(wk.y);
			}

			// Fade based on life
			let fade = 1;
			if (wk.life > wk.maxLife * 0.7) fade = Math.max(0, 1 - (wk.life - wk.maxLife * 0.7) / (wk.maxLife * 0.3));

			// Draw tail
			for (let t = 0; t < wk.tail.length; t++) {
				const tp = wk.tail[t];
				const tAlpha = (t / wk.tail.length) * wk.opacity * 0.4 * fade;
				ctx.beginPath();
				ctx.arc(tp.x, tp.y, wk.size * 0.5, 0, Math.PI * 2);
				ctx.fillStyle = `rgba(${ACCENT[0]},${ACCENT[1]},${ACCENT[2]},${tAlpha})`;
				ctx.fill();
			}

			// Draw walker dot
			ctx.beginPath();
			ctx.arc(wk.x, wk.y, wk.size, 0, Math.PI * 2);
			ctx.fillStyle = `rgba(${ACCENT[0]},${ACCENT[1]},${ACCENT[2]},${wk.opacity * fade})`;
			ctx.fill();

			// Remove if off-screen or expired
			if (wk.x < -40 || wk.x > w + 40 || wk.y < -40 || wk.y > h + 40 || wk.life > wk.maxLife) {
				gridWalkers.splice(i, 1);
			}
		}

		animFrame = requestAnimationFrame(animateCanvas);
	}

	function handleMouseMove(e: MouseEvent) {
		mouseX = e.clientX;
		mouseY = e.clientY;
	}

	let unsubWS: (() => void) | null = null;

	const suggestionChips = [
		'How many messages?',
		'Who is online?',
		'List channels',
		'Search for recent activity'
	];

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
		// Init ambient grid nodes + start canvas
		initAmbientNodes(window.innerWidth, window.innerHeight);
		animFrame = requestAnimationFrame(animateCanvas);

		try {
			connect();
			unsubWS = onMessage(handleWS);

			// Load channels and find/create Brain DM
			const data = await listChannels(slug);
			channels.set(data.channels || []);

			// Load per-user WebLLM opt-in from localStorage
			try {
				userWebLLMEnabled = localStorage.getItem('nexus_user_webllm_' + slug) === 'true';
			} catch {}

			// Load engine state
			try {
				const bs = await getBrainSettings(slug);
				standardChatEnabled = bs.standard_chat_enabled !== 'false';
				llmEnabled = bs.llm_enabled !== 'false';
				webllmEnabled = bs.webllm_enabled === 'true';
				webllmModel = bs.webllm_model || '';

				// WebLLM active: admin workspace mode OR user opt-in
				const isWebLLMOnly = (webllmEnabled && !llmEnabled) || userWebLLMEnabled;
				if (isWebLLMOnly && webllmModel) {
					engineLabel = 'Local LLM';
					try {
						webllmModule = await import('$lib/webllm.svelte.ts');
						const state = webllmModule.getState();
						if (!state.isLoaded && !state.isDownloading) {
							await webllmModule.loadEngine(webllmModel);
						}
					} catch (e) {
						console.error('WebLLM init failed:', e);
						engineLabel = 'Local LLM (error)';
					}
				} else if (standardChatEnabled && llmEnabled) engineLabel = 'Standard Chat + LLM';
				else if (standardChatEnabled) engineLabel = 'Standard Chat';
				else if (llmEnabled) engineLabel = 'LLM';
				else engineLabel = 'Disabled';
			} catch {
				engineLabel = '';
			}

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

		} catch {
			goto(`/w/${slug}`);
		}
	});

	onDestroy(() => {
		if (unsubWS) unsubWS();
		if (animFrame) cancelAnimationFrame(animFrame);
		disconnect();
	});

	function handleWS(type: string, payload: any) {
		if (type === 'message.new') {
			let current: Channel | null = null;
			activeChannel.subscribe(v => current = v)();
			if (payload.channel_id === current?.id) {
				messages.update(msgs => {
					if (msgs.some(m => m.id === payload.id)) return msgs;
					return [...msgs, payload];
				});
				// Brain responding
				if (payload.sender_id === 'brain') {
					brainState = 'speaking';
					setTimeout(() => brainState = 'idle', 2000);

					// V2: capture response
					if (mode === 'v2' && v2State === 'thinking') {
						v2Response = payload.content;
						v2State = 'response';
					}
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
		if (mode === 'v2') {
			handleSendV2();
			return;
		}
		let current: Channel | null = null;
		activeChannel.subscribe(v => current = v)();
		if (!input.trim() || !current) return;
		sendMessage(current.id, input.trim());
		input = '';
		brainState = 'thinking';
	}

	async function handleSendV2() {
		let current: Channel | null = null;
		activeChannel.subscribe(v => current = v)();
		if (!input.trim() || !current) return;
		v2Question = input.trim();
		v2Response = '';
		v2State = 'thinking';
		brainState = 'thinking';
		const channelId = current.id;

		// Always save user message to server (tell server to skip brain if WebLLM handles it)
		const willHandleWebLLM = !!webllmModule && webllmModule.getState().isLoaded;
		sendMessage(channelId, v2Question, undefined, undefined, willHandleWebLLM);
		input = '';

		// If WebLLM is loaded, run local inference
		if (webllmModule) {
			const state = webllmModule.getState();
			if (state.isLoaded) {
				try {
					// Classify intent and fetch compact context
					const { classifyIntent } = await import('$lib/intent-classifier');
					const intents = classifyIntent(v2Question);

					let systemPrompt = 'You are Brain, a helpful AI assistant.';
					try {
						const contextData = await getWebLLMContext(slug, v2Question, intents, channelId, 4000);
						systemPrompt = contextData.prompt || systemPrompt;
					} catch (e) {
						console.warn('getWebLLMContext failed, using basic prompt:', e);
					}
					if (systemPrompt.length > 4000) {
						systemPrompt = systemPrompt.slice(0, 4000) + '\n[...]';
					}

					// Build recent message history (4 msgs for context window)
					let msgs: any[] = [];
					messages.subscribe(v => msgs = v)();
					const recentMsgs = msgs.slice(-4).map((m: any) => ({
						role: m.sender_id === 'brain' ? 'assistant' as const : 'user' as const,
						content: m.content,
					}));
					recentMsgs.push({ role: 'user' as const, content: v2Question });

					const response = await webllmModule.complete(systemPrompt, recentMsgs);
					v2Response = response;
					v2State = 'response';
					brainState = 'idle';

					// Save Brain's response to server
					await saveBrainMessage(slug, channelId, response);
					return;
				} catch (e) {
					console.error('WebLLM inference failed, falling back to server:', e);
					// Fall through — server already has the message and will respond via WS
				}
			}
		}
		// Server will respond via WS (handled by handleWS)
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

	async function copyResponse() {
		// Strip HTML tags for plain text copy
		const div = document.createElement('div');
		div.innerHTML = v2Response;
		await navigator.clipboard.writeText(div.textContent || div.innerText || '');
		v2CopyFeedback = true;
		setTimeout(() => v2CopyFeedback = false, 1500);
	}

	async function saveAsNote() {
		try {
			await createDoc(slug, {
				title: v2Question.slice(0, 80),
				content: v2Response
			});
			v2SaveFeedback = true;
			setTimeout(() => v2SaveFeedback = false, 1500);
		} catch (e: any) {
			alert('Failed to save note: ' + e.message);
		}
	}

	function askAnother() {
		v2State = 'idle';
		v2Question = '';
		v2Response = '';
		input = '';
	}

	function handleChipClick(text: string) {
		input = text;
		handleSendV2();
	}

</script>

{#if mode === 'v2'}
<div class="brain-page v2-page" onmousemove={handleMouseMove}>
	<!-- Matrix grid background -->
	<div class="matrix-grid"></div>
	<canvas class="node-canvas" bind:this={canvasEl}></canvas>

	<!-- Floating controls -->
	<button class="floating-back" onclick={() => goto(`/w/${slug}`)}>
		<svg width="20" height="20" viewBox="0 0 16 16" fill="none">
			<path d="M10 3L5 8L10 13" stroke="currentColor" stroke-width="1.5" stroke-linecap="round" stroke-linejoin="round"/>
		</svg>
	</button>
	<button class="floating-mode" onclick={() => mode = 'v1'}>Chat View</button>

	<!-- V2: Centered Q&A experience -->
	<div class="v2-container">
		<BrainAvatar state={brainState} size={200} />
		{#if engineLabel}
			<span class="engine-badge">{engineLabel}</span>
		{/if}

		{#if v2State === 'idle'}
			<p class="v2-subtitle">What's on your mind?</p>
			<div class="v2-input-wrap">
				<div class="input-wrap v2-input">
					<textarea
						placeholder="Ask Brain anything..."
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
			<div class="v2-chips">
				{#each suggestionChips as chip}
					<button class="v2-chip" onclick={() => handleChipClick(chip)}>{chip}</button>
				{/each}
			</div>
		{:else if v2State === 'thinking'}
			<p class="v2-question">{v2Question}</p>
			<div class="v2-thinking">
				<span class="dot"></span>
				<span class="dot"></span>
				<span class="dot"></span>
			</div>
		{:else if v2State === 'response'}
			<p class="v2-question">{v2Question}</p>
			<div class="v2-response-card">
				<div class="msg-content">{@html safeMarkdownToHtml(v2Response)}</div>
				<div class="v2-actions">
					<button class="v2-action-btn" onclick={copyResponse}>
						{v2CopyFeedback ? 'Copied!' : 'Copy'}
					</button>
					<button class="v2-action-btn" onclick={saveAsNote}>
						{v2SaveFeedback ? 'Saved!' : 'Save Note'}
					</button>
				</div>
			</div>
			<div class="v2-input-wrap">
				<div class="input-wrap v2-input">
					<textarea
						placeholder="Ask a follow-up..."
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
			<button class="v2-reset" onclick={askAnother}>New question</button>
		{/if}
	</div>
</div>
{:else}
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
			{#if engineLabel}
				<span class="engine-badge-inline">{engineLabel}</span>
			{/if}
		</div>
		<button class="mode-btn" onclick={() => mode = 'v2'}>Focus View</button>
	</header>
		<!-- V1: existing chat interface -->
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
							<div class="msg-content">{@html safeMarkdownToHtml(msg.content)}</div>
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

		</div>

		<!-- V1 Input -->
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
{/if}

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

	/* ===== V2: Immersive Matrix ===== */
	.v2-page {
		cursor: crosshair;
	}

	/* Matrix grid background */
	.matrix-grid {
		position: absolute;
		inset: 0;
		pointer-events: none;
		z-index: 0;
		background-image:
			linear-gradient(rgba(249,115,22,0.03) 1px, transparent 1px),
			linear-gradient(90deg, rgba(249,115,22,0.03) 1px, transparent 1px);
		background-size: 40px 40px;
	}

	/* Node particle canvas */
	.node-canvas {
		position: absolute;
		inset: 0;
		width: 100%;
		height: 100%;
		pointer-events: none;
		z-index: 0;
	}

	/* Floating back arrow */
	.floating-back {
		position: fixed;
		top: 20px;
		left: 20px;
		z-index: 10;
		display: flex;
		align-items: center;
		justify-content: center;
		width: 36px;
		height: 36px;
		background: rgba(14, 14, 18, 0.6);
		backdrop-filter: blur(8px);
		border: 1px solid var(--border-default);
		border-radius: var(--radius-md);
		color: var(--text-secondary);
		cursor: pointer;
		transition: color 150ms, border-color 150ms;
	}
	.floating-back:hover { color: var(--text-primary); border-color: var(--border-strong); }

	/* Floating mode toggle */
	.floating-mode {
		position: fixed;
		top: 20px;
		right: 20px;
		z-index: 10;
		padding: 6px 14px;
		background: rgba(14, 14, 18, 0.6);
		backdrop-filter: blur(8px);
		border: 1px solid var(--border-default);
		border-radius: var(--radius-md);
		color: var(--text-secondary);
		font-size: var(--text-xs);
		font-family: inherit;
		cursor: pointer;
		transition: color 150ms, border-color 150ms;
	}
	.floating-mode:hover { color: var(--text-primary); border-color: var(--border-strong); }

	/* Engine badge (V2) */
	.engine-badge {
		font-size: var(--text-xs);
		color: var(--accent);
		background: rgba(249, 115, 22, 0.08);
		border: 1px solid var(--accent-border);
		border-radius: 999px;
		padding: 3px 12px;
		letter-spacing: 0.02em;
		margin-top: calc(-1 * var(--space-md));
	}

	/* Engine badge (V1 header) */
	.engine-badge-inline {
		font-size: 10px;
		color: var(--accent);
		opacity: 0.7;
		margin-left: var(--space-xs);
	}

	/* Particles (V1 only) */
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

	/* Header (V1 only) */
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
	.mode-btn {
		padding: 4px 12px;
		background: none;
		border: 1px solid var(--border-default);
		border-radius: var(--radius-md);
		color: var(--text-secondary);
		font-size: var(--text-xs);
		font-family: inherit;
		cursor: pointer;
		transition: color 150ms, border-color 150ms;
	}
	.mode-btn:hover { color: var(--text-primary); border-color: var(--border-strong); }

	/* ===== V2: Focus View ===== */
	.v2-container {
		flex: 1;
		display: flex;
		flex-direction: column;
		align-items: center;
		justify-content: center;
		padding: var(--space-xl);
		overflow-y: auto;
		z-index: 1;
		gap: var(--space-xl);
	}
	.v2-subtitle {
		font-size: var(--text-xl);
		color: var(--text-tertiary);
		margin: 0;
		text-align: center;
	}
	.v2-input-wrap {
		width: 100%;
		max-width: 600px;
	}
	.v2-input textarea {
		font-size: var(--text-base) !important;
		padding: 10px 0 !important;
	}
	.v2-chips {
		display: flex;
		flex-wrap: wrap;
		gap: 8px;
		justify-content: center;
		max-width: 600px;
	}
	.v2-chip {
		padding: 8px 18px;
		background: none;
		border: 1px solid var(--border-default);
		border-radius: 999px;
		color: var(--text-secondary);
		font-size: var(--text-base);
		font-family: inherit;
		cursor: pointer;
		transition: color 150ms, border-color 150ms, background 150ms;
	}
	.v2-chip:hover {
		color: var(--accent);
		border-color: var(--accent-border);
		background: var(--accent-glow);
	}
	.v2-question {
		font-style: italic;
		color: var(--text-secondary);
		text-align: center;
		max-width: 600px;
		margin: 0;
		font-size: var(--text-base);
	}
	.v2-thinking {
		display: flex;
		gap: 4px;
		align-items: center;
	}
	.v2-response-card {
		background: rgba(20, 20, 28, 0.4);
		backdrop-filter: blur(20px);
		-webkit-backdrop-filter: blur(20px);
		border: 1px solid rgba(249, 115, 22, 0.15);
		border-radius: var(--radius-lg);
		padding: 32px;
		max-width: 720px;
		width: 100%;
		line-height: 1.7;
		font-size: var(--text-base);
		box-shadow: 0 8px 32px rgba(0, 0, 0, 0.3), inset 0 0 30px rgba(249, 115, 22, 0.03);
	}
	.v2-actions {
		display: flex;
		gap: var(--space-sm);
		justify-content: flex-end;
		margin-top: var(--space-lg);
		padding-top: var(--space-md);
		border-top: 1px solid var(--border-subtle);
	}
	.v2-action-btn {
		padding: 4px 12px;
		background: none;
		border: 1px solid var(--border-default);
		border-radius: var(--radius-md);
		color: var(--text-secondary);
		font-size: var(--text-xs);
		font-family: inherit;
		cursor: pointer;
		transition: color 150ms, border-color 150ms;
	}
	.v2-action-btn:hover {
		color: var(--accent);
		border-color: var(--accent-border);
	}
	.v2-reset {
		background: none;
		border: none;
		color: var(--text-tertiary);
		font-size: var(--text-xs);
		text-decoration: underline;
		cursor: pointer;
		font-family: inherit;
	}
	.v2-reset:hover { color: var(--text-secondary); }

	/* ===== V1: Chat View ===== */

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
	.typing-indicator, .v2-thinking {
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
