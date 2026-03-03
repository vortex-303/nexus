<script lang="ts">
	import { page } from '$app/state';
	import { goto } from '$app/navigation';
	import { onMount } from 'svelte';
	import { getWorkspaceSlug, listChannels, getWorkspace, getMessages, createChannel, createInvite, clearSession, getCurrentUser, getMember, updateMemberRole, kickMember, listTasks, createTask, updateTask, deleteTask, uploadFile, fileUrl, listDocs, createDoc, updateDoc, deleteDoc, getBrainSettings, updateBrainSettings, getBrainDefinition, updateBrainDefinition, listMemories, deleteMemory, clearMemories, listActions, listSkills, getSkill, updateSkill, deleteSkill, listKnowledge, createKnowledge, uploadKnowledge, updateKnowledge, deleteKnowledge, importKnowledgeURL, getAnnouncement, getPinnedModels, browseModels, listAgents, createAgent, updateAgent, deleteAgent, listAgentTemplates, createAgentFromTemplate, generateAgentConfig, getOrgChart, updateOrgPosition, updateMemberProfile, createOrgRole, updateOrgRole, deleteOrgRole, fillOrgRole, listAgentSkills, getAgentSkill, updateAgentSkill, deleteAgentSkill, getMe, updateMe, changePassword, getOnlineMembers, createWebhook, listWebhooks, deleteWebhook, listWebhookEvents, listEmailThreads, deleteEmailThread, listTelegramChats, deleteTelegramChat } from '$lib/api';
	import { connect, disconnect, onMessage, sendMessage, sendTyping, clearChannel } from '$lib/ws';
	import { channels, members, messages, activeChannel, typingUsers, onlineUsers } from '$lib/stores/workspace';
	import type { Channel } from '$lib/stores/workspace';
	import TiptapEditor from '$lib/editor/TiptapEditor.svelte';
	import OrgChart from '$lib/components/OrgChart.svelte';

	const ROLES = ['admin', 'member', 'designer', 'marketing_coordinator', 'marketing_strategist', 'researcher', 'sales', 'guest'];

	let slug = $derived(page.params.slug);
	let input = $state('');
	let messagesEl: HTMLElement;
	let showNewChannel = $state(false);
	let newChannelName = $state('');
	let inviteUrl = $state('');
	let lastTypingSent = 0;
	let currentUser = $state(getCurrentUser());
	let isAdmin = $derived(currentUser?.role === 'admin');
	let selectedMember = $state<any>(null);
	let memberDetail = $state<any>(null);

	// DM helpers
	let publicChannels = $derived($channels.filter(ch => ch.type !== 'dm'));
	let dmChannels = $derived($channels.filter(ch => ch.type === 'dm'));

	function dmChannelName(myId: string, theirId: string): string {
		const sorted = [myId, theirId].sort();
		return `dm-${sorted[0]}-${sorted[1]}`;
	}

	function getDMPartnerName(channel: Channel): string {
		const myId = currentUser?.uid;
		for (const m of $members) {
			if (m.id !== myId && channel.name.includes(m.id)) return m.display_name;
		}
		// Fallback: strip dm- prefix and our ID
		const parts = channel.name.replace('dm-', '').split('-');
		const partnerId = parts.find((p: string) => p !== myId) || parts[0];
		return partnerId;
	}

	function isDMChannel(channel: Channel | null): boolean {
		return channel?.type === 'dm';
	}

	// View state
	type View = 'chat' | 'board' | 'list' | 'notes' | 'brain' | 'team';
	let activeView = $state<View>('chat');

	// Tasks state
	const STATUSES = ['backlog', 'todo', 'in_progress', 'done', 'cancelled'] as const;
	const PRIORITIES = ['urgent', 'high', 'medium', 'low'] as const;
	const STATUS_LABELS: Record<string, string> = { backlog: 'Backlog', todo: 'To Do', in_progress: 'In Progress', done: 'Done', cancelled: 'Cancelled' };
	const PRIORITY_COLORS: Record<string, string> = { urgent: 'var(--red)', high: 'var(--accent)', medium: 'var(--yellow)', low: 'var(--text-tertiary)' };
	let tasks = $state<any[]>([]);
	let showNewTask = $state(false);
	let newTaskTitle = $state('');
	let newTaskPriority = $state('medium');
	let newTaskStatus = $state('backlog');
	let editingTask = $state<any>(null);

	// Files state
	let uploading = $state(false);
	let fileInputEl: HTMLInputElement;

	// Notes state
	let docs = $state<any[]>([]);
	let activeDoc = $state<any>(null);
	let docTitle = $state('');
	let docContent = $state('');
	let docSaving = $state(false);
	let showNewDoc = $state(false);
	let creatingDoc = $state(false);
	let editorRef = $state<TiptapEditor>();

	// User menu state
	let showUserMenu = $state(false);
	let userInitial = $derived(currentUser?.name?.charAt(0)?.toUpperCase() || '?');

	// Agent Library modal
	let showAgentLibrary = $state(false);
	let agentLibSearch = $state('');
	let agentLibFilter = $state('all');

	const agentLibCategories = [
		{ id: 'all', label: 'All' },
		{ id: 'general', label: 'General' },
		{ id: 'coding', label: 'Coding' },
		{ id: 'research', label: 'Research' },
		{ id: 'creative', label: 'Creative' },
		{ id: 'support', label: 'Support' },
		{ id: 'custom', label: 'Custom' },
	];

	function getAgentLibCategory(agent: any): string {
		const role = (agent.role || '').toLowerCase();
		if (/creative|design|artist/.test(role)) return 'creative';
		if (/engineer|developer|coder|coding/.test(role)) return 'coding';
		if (/research|analyst/.test(role)) return 'research';
		if (/support|triage|help/.test(role)) return 'support';
		if (!agent.is_system) return 'custom';
		return 'general';
	}

	function agentLibMatchesFilter(agent: any): boolean {
		if (agentLibFilter !== 'all' && getAgentLibCategory(agent) !== agentLibFilter) return false;
		if (agentLibSearch) {
			const q = agentLibSearch.toLowerCase();
			if (!(agent.name || '').toLowerCase().includes(q) &&
				!(agent.description || '').toLowerCase().includes(q) &&
				!(agent.role || '').toLowerCase().includes(q)) return false;
		}
		return true;
	}

	function templateMatchesFilter(tmpl: any): boolean {
		if (agentLibFilter !== 'all') {
			const role = (tmpl.role || '').toLowerCase();
			let cat = 'general';
			if (/creative|design|artist/.test(role)) cat = 'creative';
			else if (/engineer|developer|coder|coding/.test(role)) cat = 'coding';
			else if (/research|analyst/.test(role)) cat = 'research';
			else if (/support|triage|help/.test(role)) cat = 'support';
			if (cat !== agentLibFilter) return false;
		}
		if (agentLibSearch) {
			const q = agentLibSearch.toLowerCase();
			if (!(tmpl.name || '').toLowerCase().includes(q) &&
				!(tmpl.description || '').toLowerCase().includes(q)) return false;
		}
		return true;
	}

	async function openAgentLibrary() {
		await loadAgents();
		await loadTemplates();
		agentLibSearch = '';
		agentLibFilter = 'all';
		showAgentLibrary = true;
	}

	function agentLibChat(agent: any) {
		showAgentLibrary = false;
		startDMWithAgent(agent);
	}

	function agentLibEdit(agent: any) {
		showAgentLibrary = false;
		activeView = 'team';
		onViewChange();
		teamTab = 'agents';
		openEditAgent(agent);
	}

	async function agentLibDelete(agentId: string) {
		if (!confirm('Delete this agent? This cannot be undone.')) return;
		try {
			await deleteAgent(slug, agentId);
			agentsList = agentsList.filter(a => a.id !== agentId);
		} catch (e: any) {
			alert(e.message);
		}
	}

	async function agentLibUseTemplate(templateId: string) {
		try {
			await createAgentFromTemplate(slug, templateId);
			await loadAgents();
		} catch (e: any) {
			alert(e.message);
		}
	}

	let filteredBuiltinAgents = $derived(agentsList.filter((a: any) => a.is_system && agentLibMatchesFilter(a)));
	let filteredUserAgents = $derived(agentsList.filter((a: any) => !a.is_system && agentLibMatchesFilter(a)));
	let filteredTemplates = $derived(agentTemplates.filter((t: any) => templateMatchesFilter(t)));

	// User preferences modal
	let showPreferences = $state(false);
	let prefsTab = $state<'profile' | 'security' | 'appearance'>('profile');
	let prefsDisplayName = $state('');
	let prefsEmail = $state('');
	let prefsCurrentPw = $state('');
	let prefsNewPw = $state('');
	let prefsConfirmPw = $state('');
	let prefsMsg = $state('');
	let prefsLoading = $state(false);

	// Built-in agents in sidebar
	let builtinAgents = $derived(agentsList.filter((a: any) => a.is_system && a.id !== 'brain' && a.is_active));

	// @-mention autocomplete
	let mentionActive = $state(false);
	let mentionQuery = $state('');
	let mentionResults: any[] = $state([]);
	let mentionIndex = $state(0);
	let mentionStartPos = $state(0);
	let inputEl: HTMLInputElement | undefined = $state();

	// Online members for channel header
	let onlineMembersList = $state<any[]>([]);

	// Member drawer
	let showMemberDrawer = $state(false);
	let offlineMembers = $derived(() => {
		const onlineIds = new Set(onlineMembersList.map((m: any) => m.user_id));
		return $members.filter((m: any) => !onlineIds.has(m.id));
	});

	// Brain state
	let brainSettings = $state<any>({});
	let brainApiKey = $state('');
	let brainModel = $state('anthropic/claude-sonnet-4');
	let brainImageModel = $state('gemini-2.5-flash-image');
	let brainGeminiKey = $state('');
	let brainMemoryEnabled = $state(true);
	let brainMemoryModel = $state('openai/gpt-4o-mini');
	let brainExtractFreq = $state(15);
	let brainDefFiles = ['SOUL.md', 'INSTRUCTIONS.md', 'TEAM.md', 'MEMORY.md', 'HEARTBEAT.md'] as const;
	let brainActiveFile = $state('');
	let brainFileContent = $state('');
	let brainSaving = $state(false);
	let brainTab = $state<'settings' | 'definitions' | 'memory' | 'activity' | 'skills' | 'knowledge' | 'integrations'>('settings');
	let memories = $state<any[]>([]);
	let memoryCounts = $state<Record<string, number>>({});
	let brainActions = $state<any[]>([]);
	let brainActionsTotal = $state(0);
	let brainSkills = $state<any[]>([]);
	let activeSkill = $state<any>(null);
	let skillContent = $state('');

	// Knowledge state
	let knowledgeItems = $state<any[]>([]);
	let activeKnowledgeItem = $state<any>(null);
	let knowledgeTitle = $state('');
	let knowledgeContent = $state('');
	let showNewKnowledge = $state(false);
	let knowledgeSaving = $state(false);
	let knowledgeFileInput: HTMLInputElement;

	// URL Import state
	let showUrlImport = $state(false);
	let importUrl = $state('');
	let urlImporting = $state(false);
	let urlPreview = $state<any>(null);

	// Integrations state
	let webhooks = $state<any[]>([]);
	let webhookEvents = $state<Record<string, any[]>>({});
	let newWebhookChannel = $state('');
	let newWebhookDesc = $state('');
	let emailThreads = $state<any[]>([]);
	let telegramChats = $state<any[]>([]);

	// Announcement state
	let announcement = $state<any>(null);
	let announcementDismissed = $state(false);

	// Dynamic models state
	let pinnedModels = $state<any[]>([]);
	let showModelBrowser = $state(false);
	let browseModelList = $state<any[]>([]);
	let modelSearchQuery = $state('');
	let modelFilter = $state('');
	let modelBrowserLoading = $state(false);

	// Team / Agents state
	let teamTab = $state<'members' | 'agents' | 'orgchart'>('orgchart');
	let agentsList = $state<any[]>([]);
	let agentTemplates = $state<any[]>([]);
	let showAgentForm = $state(false);
	let showTemplateGallery = $state(false);
	let editingAgent = $state<any>(null);
	let agentGenerating = $state(false);
	let agentSaving = $state(false);
	let orgChartNodes = $state<any[]>([]);
	let orgChartLayout = $state<'vertical' | 'horizontal'>('vertical');

	// Agent state indicators
	let agentStates = $state<Map<string, {state: string, toolName: string, channelID: string, agentName: string}>>(new Map());

	// Agent skills state
	let agentSkillsList = $state<any[]>([]);
	let showSkillEditor = $state(false);
	let editingSkillFile = $state('');
	let skillEditorContent = $state('');
	let selectedNodeForPanel = $state<any>(null);

	// Role dialog state
	let showRoleDialog = $state(false);
	let roleSaving = $state(false);
	let roleForm = $state({ title: '', description: '', department: '', reports_to: '' });
	let pendingRoleFill = $state<string | null>(null);

	// OrgChart control callbacks
	let chartFit = $state<(() => void) | null>(null);
	let chartExpandAll = $state<(() => void) | null>(null);
	let chartCollapseAll = $state<(() => void) | null>(null);
	let chartExpanded = $state(true);

	// Agent form fields
	let agentForm = $state({
		name: '', description: '', avatar: '', role: '', goal: '', backstory: '', instructions: '',
		constraints: '', escalation_prompt: '', model: '', temperature: 0.7, max_tokens: 2048,
		tools: [] as string[], channels: [] as string[], knowledge_access: false, memory_access: false,
		can_delegate: false, max_iterations: 5, trigger_type: 'mention'
	});
	const AGENT_TOOLS = ['create_task', 'list_tasks', 'search_messages', 'create_document', 'search_knowledge'];

	onMount(async () => {
		if (!getWorkspaceSlug()) { goto('/'); return; }

		// Fetch announcement
		try {
			const ann = await getAnnouncement();
			if (ann && ann.id) {
				const dismissKey = `dismissed_announcement_${ann.id}`;
				if (!localStorage.getItem(dismissKey)) {
					announcement = ann;
				}
			}
		} catch {}

		try {
			const ws = await getWorkspace(slug);
			members.set(ws.members);

			const chs = await listChannels(slug);
			channels.set(chs);

			if (chs.length > 0) selectChannel(chs[0]);

			// Load agents for sidebar built-in agents
			loadAgents().catch(() => {});

			connect();
			const unsub = onMessage(handleWS);

			// Fetch online members periodically
			async function fetchOnline() {
				try {
					const list = await getOnlineMembers(slug);
					onlineMembersList = list || [];
				} catch {}
			}
			fetchOnline();
			const onlineInterval = setInterval(fetchOnline, 30000);

			return () => { unsub(); disconnect(); clearInterval(onlineInterval); };
		} catch {
			clearSession();
			goto('/');
		}
	});

	async function selectChannel(ch: Channel) {
		activeChannel.set(ch);
		const data = await getMessages(slug, ch.id);
		messages.set(data.messages);
		scrollToBottom();
	}

	function handleWS(type: string, payload: any) {
		if (type === 'message.new') {
			let current: Channel | null = null;
			activeChannel.subscribe(v => current = v)();
			if (payload.channel_id === current?.id) {
				messages.update(msgs => [...msgs, payload]);
				scrollToBottom();
			}
		} else if (type === 'message.edited') {
			messages.update(msgs => msgs.map(m =>
				m.id === payload.message_id ? { ...m, content: payload.content, edited_at: payload.edited_at } : m
			));
		} else if (type === 'message.deleted') {
			messages.update(msgs => msgs.filter(m => m.id !== payload.message_id));
		} else if (type === 'channel.cleared') {
			let current: Channel | null = null;
			activeChannel.subscribe(v => current = v)();
			if (payload.channel_id === current?.id) {
				messages.set([]);
			}
		} else if (type === 'typing') {
			typingUsers.update(map => {
				const next = new Map(map);
				next.set(payload.user_id, payload.display_name);
				setTimeout(() => {
					typingUsers.update(m => { const n = new Map(m); n.delete(payload.user_id); return n; });
				}, 3000);
				return next;
			});
		} else if (type === 'file.new') {
			// Show file as a message-like entry
			let current: Channel | null = null;
			activeChannel.subscribe(v => current = v)();
			if (payload.channel_id === current?.id) {
				messages.update(msgs => [...msgs, {
					id: payload.id,
					channel_id: payload.channel_id,
					sender_id: payload.uploader_id,
					sender_name: getMemberName(payload.uploader_id),
					content: `📎 [${payload.name}](${payload.url})`,
					created_at: payload.created_at,
					file: payload
				}]);
				scrollToBottom();
			}
		} else if (type === 'doc.created') {
			docs = [payload, ...docs.filter(d => d.id !== payload.id)];
		} else if (type === 'doc.updated') {
			docs = docs.map(d => d.id === payload.id ? payload : d);
			if (activeDoc?.id === payload.id) {
				activeDoc = payload;
				docTitle = payload.title;
				// Only update editor if content actually changed (prevents cursor jump)
				const currentHTML = editorRef?.getHTML() || '';
				if (payload.content !== currentHTML && payload.content !== docContent) {
					docContent = payload.content;
					if (editorRef) {
						editorRef.setContent(editorRef.migrateContent(payload.content));
					}
				}
			}
		} else if (type === 'doc.deleted') {
			docs = docs.filter(d => d.id !== payload.id);
			if (activeDoc?.id === payload.id) {
				activeDoc = null;
				docTitle = '';
				docContent = '';
			}
		} else if (type === 'task.created') {
			tasks = [payload, ...tasks.filter(t => t.id !== payload.id)];
		} else if (type === 'task.updated') {
			tasks = tasks.map(t => t.id === payload.id ? payload : t);
		} else if (type === 'task.deleted') {
			tasks = tasks.filter(t => t.id !== payload.id);
		} else if (type === 'presence') {
			onlineUsers.update(set => {
				const next = new Set(set);
				if (payload.status === 'online') next.add(payload.user_id);
				else next.delete(payload.user_id);
				return next;
			});
			members.update(mems => mems.map(m =>
				m.id === payload.user_id ? { ...m, online: payload.status === 'online' } : m
			));
		} else if (type === 'agent.state') {
			const next = new Map(agentStates);
			if (payload.state === 'idle') {
				next.delete(payload.agent_id);
			} else {
				next.set(payload.agent_id, {
					state: payload.state,
					toolName: payload.tool_name || '',
					channelID: payload.channel_id,
					agentName: payload.agent_name
				});
				// Safety timeout: auto-clear after 30s
				setTimeout(() => {
					agentStates = new Map([...agentStates].filter(([k]) => k !== payload.agent_id));
				}, 30000);
			}
			agentStates = next;
		}
	}

	function handleSend() {
		let current: Channel | null = null;
		activeChannel.subscribe(v => current = v)();
		if (!input.trim() || !current) return;
		sendMessage(current.id, input.trim());
		input = '';
	}

	function handleInputKeydown(e: KeyboardEvent) {
		if (mentionActive) {
			if (e.key === 'ArrowDown') {
				e.preventDefault();
				mentionIndex = (mentionIndex + 1) % mentionResults.length;
				return;
			} else if (e.key === 'ArrowUp') {
				e.preventDefault();
				mentionIndex = (mentionIndex - 1 + mentionResults.length) % mentionResults.length;
				return;
			} else if (e.key === 'Enter' || e.key === 'Tab') {
				if (mentionResults.length > 0) {
					e.preventDefault();
					insertMention(mentionResults[mentionIndex]);
					return;
				}
			} else if (e.key === 'Escape') {
				e.preventDefault();
				mentionActive = false;
				return;
			}
		}
		if (e.key === 'Enter' && !e.shiftKey) {
			e.preventDefault();
			handleSend();
		} else {
			const now = Date.now();
			if (now - lastTypingSent > 2000) {
				let current: Channel | null = null;
				activeChannel.subscribe(v => current = v)();
				if (current) sendTyping(current.id);
				lastTypingSent = now;
			}
		}
	}

	function handleMentionInput() {
		const el = inputEl;
		if (!el) return;
		const pos = el.selectionStart ?? 0;
		const text = el.value.substring(0, pos);
		const atMatch = text.match(/@(\w*)$/);
		if (atMatch) {
			mentionStartPos = pos - atMatch[0].length;
			mentionQuery = atMatch[1].toLowerCase();
			// Combine members + agents as mention candidates
			const allCandidates = [
				...$members.map((m: any) => ({ id: m.id, display_name: m.display_name, role: m.role || 'member' })),
				...builtinAgents.map((a: any) => ({ id: a.id, display_name: a.name, role: 'agent' }))
			];
			mentionResults = allCandidates
				.filter((c: any) => c.display_name.toLowerCase().startsWith(mentionQuery) || (mentionQuery === '' && true))
				.slice(0, 8);
			mentionIndex = 0;
			mentionActive = mentionResults.length > 0;
		} else {
			mentionActive = false;
		}
	}

	function insertMention(item: any) {
		const el = inputEl;
		if (!el) return;
		const before = input.substring(0, mentionStartPos);
		const after = input.substring(el.selectionStart ?? input.length);
		input = before + '@' + item.display_name + ' ' + after;
		mentionActive = false;
		// Restore cursor position after the mention
		requestAnimationFrame(() => {
			const newPos = before.length + 1 + item.display_name.length + 1;
			el.setSelectionRange(newPos, newPos);
			el.focus();
		});
	}

	async function handleCreateChannel() {
		if (!newChannelName.trim()) return;
		const ch = await createChannel(slug, newChannelName.trim());
		channels.update(chs => [...chs, { ...ch, classification: 'public' }]);
		newChannelName = '';
		showNewChannel = false;
	}

	async function handleInvite() {
		const data = await createInvite(slug);
		inviteUrl = location.origin + data.invite_url;
	}

	function handleCopyInvite() {
		navigator.clipboard.writeText(inviteUrl);
		inviteUrl = '';
	}

	async function handleMemberClick(member: any) {
		if (member.id === currentUser?.uid) return;
		// Open DM with this member
		const myId = currentUser?.uid;
		if (!myId) return;
		const dmName = dmChannelName(myId, member.id);
		let existing = $channels.find(ch => ch.name === dmName && ch.type === 'dm');
		if (existing) {
			selectChannel(existing);
		} else {
			try {
				const ch = await createChannel(slug, dmName, 'dm');
				const newCh = { ...ch, classification: 'dm', type: 'dm' };
				channels.update(chs => [...chs, newCh]);
				selectChannel(newCh);
			} catch (e: any) {
				alert(e.message);
				return;
			}
		}
		activeView = 'chat';
	}

	async function handleManageMember(member: any) {
		selectedMember = member;
		try {
			memberDetail = await getMember(slug, member.id);
		} catch {
			memberDetail = null;
		}
	}

	async function handleRoleChange(role: string) {
		if (!selectedMember) return;
		try {
			await updateMemberRole(slug, selectedMember.id, role);
			selectedMember = { ...selectedMember, role };
			members.update(mems => mems.map(m => m.id === selectedMember.id ? { ...m, role } : m));
			memberDetail = await getMember(slug, selectedMember.id);
		} catch (e: any) {
			alert(e.message);
		}
	}

	async function handleKick() {
		if (!selectedMember || !confirm(`Remove ${selectedMember.display_name}?`)) return;
		try {
			await kickMember(slug, selectedMember.id);
			members.update(mems => mems.filter(m => m.id !== selectedMember.id));
			selectedMember = null;
			memberDetail = null;
		} catch (e: any) {
			alert(e.message);
		}
	}

	async function onViewChange() {
		selectedMember = null;
		memberDetail = null;
		if ((activeView === 'board' || activeView === 'list') && tasks.length === 0) {
			await loadTasks();
		}
		if (activeView === 'notes' && docs.length === 0) {
			await loadDocs();
		}
		if (activeView === 'brain') {
			await loadBrainSettings();
		}
		if (activeView === 'team') {
			if (teamTab === 'orgchart') await loadOrgChart();
			else if (teamTab === 'agents') await loadAgents();
		}
	}

	async function loadTasks() {
		try {
			const data = await listTasks(slug);
			tasks = data.tasks || [];
		} catch {}
	}

	async function handleCreateTask() {
		if (!newTaskTitle.trim()) return;
		try {
			await createTask(slug, { title: newTaskTitle.trim(), priority: newTaskPriority, status: newTaskStatus });
			// Don't add to tasks here — the WebSocket task.created event handles it
			newTaskTitle = '';
			newTaskPriority = 'medium';
			newTaskStatus = 'backlog';
			showNewTask = false;
		} catch (e: any) {
			alert(e.message);
		}
	}

	async function handleTaskStatusChange(taskId: string, status: string) {
		try {
			const updated = await updateTask(slug, taskId, { status });
			tasks = tasks.map(t => t.id === taskId ? updated : t);
		} catch (e: any) {
			alert(e.message);
		}
	}

	async function handleTaskPriorityChange(taskId: string, priority: string) {
		try {
			const updated = await updateTask(slug, taskId, { priority });
			tasks = tasks.map(t => t.id === taskId ? updated : t);
		} catch (e: any) {
			alert(e.message);
		}
	}

	async function handleDeleteTask(taskId: string) {
		if (!confirm('Delete this task?')) return;
		try {
			await deleteTask(slug, taskId);
			tasks = tasks.filter(t => t.id !== taskId);
		} catch (e: any) {
			alert(e.message);
		}
	}

	async function handleFileUpload(e: Event) {
		const input = e.target as HTMLInputElement;
		const file = input.files?.[0];
		if (!file) return;
		let current: Channel | null = null;
		activeChannel.subscribe(v => current = v)();
		if (!current) return;
		uploading = true;
		try {
			await uploadFile(slug, current.id, file);
		} catch (err: any) {
			alert(err.message);
		} finally {
			uploading = false;
			input.value = '';
		}
	}

	function isImageMime(mime: string): boolean {
		return mime?.startsWith('image/');
	}

	function getMemberName(memberId: string): string {
		let name = memberId;
		members.subscribe(mems => {
			const m = mems.find((m: any) => m.id === memberId);
			if (m) name = m.display_name;
		})();
		return name;
	}

	async function loadDocs() {
		try {
			const data = await listDocs(slug);
			docs = data.documents || [];
		} catch {}
	}

	async function handleCreateDoc() {
		if (creatingDoc) return;
		creatingDoc = true;
		try {
			const doc = await createDoc(slug, { title: 'Untitled', content: '' });
			// Don't add to docs here — the WebSocket doc.created event handles it
			selectDoc(doc);
			showNewDoc = false;
		} catch (e: any) {
			alert(e.message);
		} finally {
			creatingDoc = false;
		}
	}

	function selectDoc(doc: any) {
		activeDoc = doc;
		docTitle = doc.title;
		docContent = doc.content;
		if (editorRef) {
			editorRef.setContent(editorRef.migrateContent(doc.content));
		}
	}

	async function handleSaveDoc() {
		if (!activeDoc) return;
		docSaving = true;
		try {
			const updated = await updateDoc(slug, activeDoc.id, { title: docTitle, content: docContent });
			activeDoc = updated;
			docs = docs.map(d => d.id === updated.id ? updated : d);
		} catch (e: any) {
			alert(e.message);
		} finally {
			docSaving = false;
		}
	}

	async function handleAutoSave(html: string) {
		if (!activeDoc) return;
		docContent = html;
		docSaving = true;
		try {
			const updated = await updateDoc(slug, activeDoc.id, { title: docTitle, content: html });
			activeDoc = updated;
			docs = docs.map(d => d.id === updated.id ? updated : d);
		} catch (e: any) {
			console.error('Auto-save failed:', e.message);
		} finally {
			docSaving = false;
		}
	}

	async function handleDeleteDoc(docId: string) {
		if (!confirm('Delete this document?')) return;
		try {
			await deleteDoc(slug, docId);
			docs = docs.filter(d => d.id !== docId);
			if (activeDoc?.id === docId) {
				activeDoc = null;
				docTitle = '';
				docContent = '';
			}
		} catch (e: any) {
			alert(e.message);
		}
	}

	// Brain functions
	async function loadBrainSettings() {
		try {
			brainSettings = await getBrainSettings(slug);
			brainModel = brainSettings.model || 'anthropic/claude-sonnet-4';
			brainImageModel = brainSettings.image_model || 'gemini-2.5-flash-image';
			brainGeminiKey = '';
			brainMemoryModel = brainSettings.memory_model || 'openai/gpt-4o-mini';
			brainMemoryEnabled = brainSettings.memory_enabled !== 'false';
			brainExtractFreq = parseInt(brainSettings.extraction_frequency) || 15;
			brainApiKey = '';
			await loadPinnedModels();
		} catch {}
	}

	async function saveBrainSettings() {
		brainSaving = true;
		try {
			const updates: Record<string, string> = {
				model: brainModel,
				image_model: brainImageModel,
				memory_model: brainMemoryModel,
				memory_enabled: String(brainMemoryEnabled),
				extraction_frequency: String(brainExtractFreq),
			};
			if (brainApiKey) updates.api_key = brainApiKey;
			if (brainGeminiKey) updates.gemini_api_key = brainGeminiKey;
			await updateBrainSettings(slug, updates);
			await loadBrainSettings();
			brainApiKey = '';
		} catch (e: any) {
			alert(e.message);
		}
		brainSaving = false;
	}

	async function handleBrainSettingChange(key: string, value: string) {
		try {
			await updateBrainSettings(slug, { [key]: value });
			brainSettings[key] = value;
		} catch (e: any) {
			alert(e.message);
		}
	}

	async function selectBrainFile(file: string) {
		brainActiveFile = file;
		try {
			const data = await getBrainDefinition(slug, file);
			brainFileContent = data.content;
		} catch {
			brainFileContent = '';
		}
	}

	async function loadMemories() {
		try {
			const data = await listMemories(slug);
			memories = data.memories || [];
			memoryCounts = data.counts || {};
		} catch {}
	}

	async function handleDeleteMemory(memId: string) {
		try {
			await deleteMemory(slug, memId);
			memories = memories.filter(m => m.id !== memId);
			// Recount
			const newCounts: Record<string, number> = {};
			for (const m of memories) newCounts[m.type] = (newCounts[m.type] || 0) + 1;
			memoryCounts = newCounts;
		} catch (e: any) {
			alert(e.message);
		}
	}

	async function handleClearMemories() {
		if (!confirm('Clear all Brain memories? This cannot be undone.')) return;
		try {
			await clearMemories(slug);
			memories = [];
			memoryCounts = {};
		} catch (e: any) {
			alert(e.message);
		}
	}

	async function loadActions() {
		try {
			const data = await listActions(slug);
			brainActions = data.actions || [];
			brainActionsTotal = data.total || 0;
		} catch {}
	}

	async function loadSkills() {
		try {
			const data = await listSkills(slug);
			brainSkills = data.skills || [];
		} catch {}
	}

	async function selectSkill(skill: any) {
		activeSkill = skill;
		try {
			const data = await getSkill(slug, skill.file_name);
			skillContent = data.content;
		} catch {}
	}

	async function saveSkill() {
		if (!activeSkill) return;
		brainSaving = true;
		try {
			await updateSkill(slug, activeSkill.file_name, skillContent);
			await loadSkills();
		} catch (e: any) {
			alert(e.message);
		}
		brainSaving = false;
	}

	async function handleDeleteSkill(fileName: string) {
		if (!confirm('Delete this skill?')) return;
		try {
			await deleteSkill(slug, fileName);
			if (activeSkill?.file_name === fileName) {
				activeSkill = null;
				skillContent = '';
			}
			await loadSkills();
		} catch (e: any) {
			alert(e.message);
		}
	}

	// Knowledge functions
	async function loadKnowledge() {
		try {
			const data = await listKnowledge(slug);
			knowledgeItems = data.articles || [];
		} catch (e: any) {
			console.error('Failed to load knowledge:', e);
		}
	}

	async function loadIntegrations() {
		try {
			const [wh, et, tc] = await Promise.all([
				listWebhooks(slug).catch(() => []),
				listEmailThreads(slug).catch(() => []),
				listTelegramChats(slug).catch(() => []),
			]);
			webhooks = wh;
			emailThreads = et;
			telegramChats = tc;
		} catch (e: any) {
			console.error('Failed to load integrations:', e);
		}
	}

	async function handleCreateWebhook() {
		if (!newWebhookChannel) return;
		try {
			const result = await createWebhook(slug, newWebhookChannel, newWebhookDesc);
			newWebhookChannel = '';
			newWebhookDesc = '';
			await loadIntegrations();
			alert(`Webhook created!\nURL: ${result.url}`);
		} catch (e: any) {
			alert(e.message);
		}
	}

	async function handleDeleteWebhook(id: string) {
		if (!confirm('Delete this webhook?')) return;
		try {
			await deleteWebhook(slug, id);
			await loadIntegrations();
		} catch (e: any) {
			alert(e.message);
		}
	}

	async function loadEventsForHook(hookId: string) {
		try {
			const events = await listWebhookEvents(slug, hookId);
			webhookEvents = { ...webhookEvents, [hookId]: events };
		} catch (e: any) {
			console.error('Failed to load events:', e);
		}
	}

	async function handleDeleteEmailThread(id: string) {
		if (!confirm('Delete this email thread?')) return;
		try {
			await deleteEmailThread(slug, id);
			await loadIntegrations();
		} catch (e: any) {
			alert(e.message);
		}
	}

	async function handleDeleteTelegramChat(id: string) {
		if (!confirm('Unlink this Telegram chat?')) return;
		try {
			await deleteTelegramChat(slug, id);
			await loadIntegrations();
		} catch (e: any) {
			alert(e.message);
		}
	}

	async function handleCreateKnowledge() {
		if (!knowledgeTitle.trim() || !knowledgeContent.trim()) return;
		knowledgeSaving = true;
		try {
			await createKnowledge(slug, { title: knowledgeTitle.trim(), content: knowledgeContent.trim() });
			knowledgeTitle = '';
			knowledgeContent = '';
			showNewKnowledge = false;
			await loadKnowledge();
		} catch (e: any) {
			alert(e.message);
		} finally {
			knowledgeSaving = false;
		}
	}

	async function handleUploadKnowledgeFile(e: Event) {
		const input = e.target as HTMLInputElement;
		const file = input.files?.[0];
		if (!file) return;
		try {
			await uploadKnowledge(slug, file);
			await loadKnowledge();
		} catch (e: any) {
			alert(e.message);
		}
		input.value = '';
	}

	async function selectKnowledgeItem(item: any) {
		activeKnowledgeItem = item;
		knowledgeTitle = item.title;
		knowledgeContent = item.content || '';
		// Fetch full content if not loaded
		if (!item.content) {
			try {
				const full = await (await fetch(`/api/workspaces/${slug}/brain/knowledge/${item.id}`, {
					headers: { Authorization: `Bearer ${localStorage.getItem('nexus_token')}` }
				})).json();
				knowledgeContent = full.content || '';
			} catch {}
		}
		showNewKnowledge = false;
	}

	async function handleUpdateKnowledge() {
		if (!activeKnowledgeItem) return;
		knowledgeSaving = true;
		try {
			await updateKnowledge(slug, activeKnowledgeItem.id, { title: knowledgeTitle.trim(), content: knowledgeContent });
			activeKnowledgeItem = null;
			knowledgeTitle = '';
			knowledgeContent = '';
			await loadKnowledge();
		} catch (e: any) {
			alert(e.message);
		} finally {
			knowledgeSaving = false;
		}
	}

	async function handleDeleteKnowledge(id: string) {
		if (!confirm('Delete this knowledge article?')) return;
		try {
			await deleteKnowledge(slug, id);
			if (activeKnowledgeItem?.id === id) {
				activeKnowledgeItem = null;
				knowledgeTitle = '';
				knowledgeContent = '';
			}
			await loadKnowledge();
		} catch (e: any) {
			alert(e.message);
		}
	}

	// URL Import
	async function handleFetchUrl() {
		if (!importUrl.trim()) return;
		urlImporting = true;
		urlPreview = null;
		try {
			urlPreview = await importKnowledgeURL(slug, importUrl.trim());
		} catch (e: any) {
			alert(e.message);
		} finally {
			urlImporting = false;
		}
	}

	async function handleSaveUrlImport() {
		if (!urlPreview) return;
		knowledgeSaving = true;
		try {
			await createKnowledge(slug, { title: urlPreview.title, content: urlPreview.content });
			urlPreview = null;
			importUrl = '';
			showUrlImport = false;
			await loadKnowledge();
		} catch (e: any) {
			alert(e.message);
		} finally {
			knowledgeSaving = false;
		}
	}

	// Announcement
	function dismissAnnouncement() {
		if (announcement) {
			localStorage.setItem(`dismissed_announcement_${announcement.id}`, '1');
		}
		announcement = null;
		announcementDismissed = true;
	}

	// Model browser
	async function openModelBrowser() {
		showModelBrowser = true;
		if (browseModelList.length === 0) {
			modelBrowserLoading = true;
			try {
				const data = await browseModels();
				browseModelList = data.models || [];
			} catch (e: any) {
				alert('Failed to load models: ' + e.message);
			} finally {
				modelBrowserLoading = false;
			}
		}
	}

	async function loadPinnedModels() {
		try {
			const data = await getPinnedModels();
			pinnedModels = data.models || [];
		} catch {}
	}

	function filteredBrowseModels() {
		let list = browseModelList;
		if (modelSearchQuery) {
			const q = modelSearchQuery.toLowerCase();
			list = list.filter((m: any) => m.id.toLowerCase().includes(q) || m.name.toLowerCase().includes(q));
		}
		if (modelFilter === 'free') list = list.filter((m: any) => m.is_free);
		if (modelFilter === 'new') list = list.filter((m: any) => m.is_new);
		return list.slice(0, 100); // Limit display
	}

	async function saveBrainFile() {
		if (!brainActiveFile) return;
		brainSaving = true;
		try {
			await updateBrainDefinition(slug, brainActiveFile, brainFileContent);
		} catch (e: any) {
			alert(e.message);
		}
		brainSaving = false;
	}

	// Agent / Team functions
	async function loadAgents() {
		try {
			const data = await listAgents(slug);
			agentsList = data || [];
		} catch {}
	}

	async function loadTemplates() {
		if (agentTemplates.length > 0) return;
		try {
			agentTemplates = await listAgentTemplates(slug);
		} catch {}
	}

	async function loadOrgChart() {
		try {
			const data = await getOrgChart(slug);
			orgChartNodes = data.nodes || [];
		} catch {}
	}

	async function handleOrgReparent(nodeId: string, newParentId: string) {
		try {
			await updateOrgPosition(slug, nodeId, newParentId);
			await loadOrgChart();
		} catch (e: any) {
			alert(e.message);
		}
	}

	function handleOrgNodeClick(node: any) {
		selectedNodeForPanel = selectedNodeForPanel?.id === node.id ? null : node;
	}

	function handleAddOrgRole() {
		roleForm = { title: '', description: '', department: '', reports_to: '' };
		showRoleDialog = true;
	}

	async function handleCreateRole() {
		if (!roleForm.title.trim()) { alert('Title is required'); return; }
		roleSaving = true;
		try {
			const desc = [roleForm.department, roleForm.description].filter(Boolean).join(' — ');
			await createOrgRole(slug, { title: roleForm.title, description: desc, reports_to: roleForm.reports_to || 'brain' });
			showRoleDialog = false;
			await loadOrgChart();
		} catch (e: any) {
			alert(e.message);
		} finally {
			roleSaving = false;
		}
	}

	async function handleFillRole(roleId: string, filledBy: string, filledType: string) {
		if (!filledBy) return;
		try {
			await fillOrgRole(slug, roleId, filledBy, filledType);
			selectedNodeForPanel = null;
			await loadOrgChart();
		} catch (e: any) {
			alert(e.message);
		}
	}

	async function handleDeleteOrgRoleAction(roleId: string) {
		if (!confirm('Delete this role slot?')) return;
		try {
			await deleteOrgRole(slug, roleId);
			selectedNodeForPanel = null;
			await loadOrgChart();
		} catch (e: any) {
			alert(e.message);
		}
	}

	async function handleToggleActive(node: any) {
		try {
			await updateAgent(slug, node.id, { is_active: !node.is_active });
			agentsList = agentsList.map(a => a.id === node.id ? { ...a, is_active: !a.is_active } : a);
			selectedNodeForPanel = null;
			await loadOrgChart();
		} catch (e: any) {
			alert(e.message);
		}
	}

	function handleCreateAgentForRole(roleId: string, roleTitle: string, roleDescription: string) {
		pendingRoleFill = roleId;
		resetAgentForm();
		agentForm.name = roleTitle;
		agentForm.description = roleDescription;
		agentForm.role = roleTitle;
		editingAgent = null;
		showAgentForm = true;
		selectedNodeForPanel = null;
		teamTab = 'agents';
	}

	function formatLastActive(iso: string): string {
		if (!iso) return '';
		const d = new Date(iso);
		const now = new Date();
		const diffMs = now.getTime() - d.getTime();
		const diffMin = Math.floor(diffMs / 60000);
		if (diffMin < 1) return 'just now';
		if (diffMin < 60) return `${diffMin}m ago`;
		const diffH = Math.floor(diffMin / 60);
		if (diffH < 24) return `${diffH}h ago`;
		const diffD = Math.floor(diffH / 24);
		if (diffD < 30) return `${diffD}d ago`;
		return d.toLocaleDateString();
	}

	async function loadAgentSkills(agentId: string) {
		try {
			const data = await listAgentSkills(slug, agentId);
			agentSkillsList = data.skills || [];
		} catch {
			agentSkillsList = [];
		}
	}

	async function handleSaveAgentSkill(agentId: string) {
		if (!editingSkillFile) return;
		try {
			await updateAgentSkill(slug, agentId, editingSkillFile, skillEditorContent);
			showSkillEditor = false;
			await loadAgentSkills(agentId);
		} catch (e: any) {
			alert(e.message);
		}
	}

	async function handleDeleteAgentSkill(agentId: string, file: string) {
		if (!confirm(`Delete skill ${file}?`)) return;
		try {
			await deleteAgentSkill(slug, agentId, file);
			await loadAgentSkills(agentId);
		} catch (e: any) {
			alert(e.message);
		}
	}

	async function handleNewAgentSkill(agentId: string) {
		const name = prompt('Skill filename (e.g. my-skill.md):');
		if (!name) return;
		const fileName = name.endsWith('.md') ? name : name + '.md';
		editingSkillFile = fileName;
		skillEditorContent = `---
name: ${fileName.replace('.md', '').replace(/-/g, ' ')}
description:
trigger: mention
autonomy: reactive
---

# Skill Instructions

`;
		showSkillEditor = true;
	}

	function resetAgentForm() {
		agentForm = {
			name: '', description: '', avatar: '', role: '', goal: '', backstory: '', instructions: '',
			constraints: '', escalation_prompt: '', model: '', temperature: 0.7, max_tokens: 2048,
			tools: [], channels: [], knowledge_access: false, memory_access: false,
			can_delegate: false, max_iterations: 5, trigger_type: 'mention'
		};
	}

	function openNewAgent() {
		resetAgentForm();
		editingAgent = null;
		showAgentForm = true;
		showTemplateGallery = false;
	}

	function openEditAgent(agent: any) {
		editingAgent = agent;
		agentForm = {
			name: agent.name, description: agent.description, avatar: agent.avatar,
			role: agent.role, goal: agent.goal, backstory: agent.backstory,
			instructions: agent.instructions, constraints: agent.constraints,
			escalation_prompt: agent.escalation_prompt, model: agent.model,
			temperature: agent.temperature, max_tokens: agent.max_tokens,
			tools: JSON.parse(JSON.stringify(agent.tools || '[]')),
			channels: JSON.parse(JSON.stringify(agent.channels || '[]')),
			knowledge_access: agent.knowledge_access, memory_access: agent.memory_access,
			can_delegate: agent.can_delegate, max_iterations: agent.max_iterations,
			trigger_type: agent.trigger_type || 'mention'
		};
		// Parse tools/channels if they're strings
		if (typeof agentForm.tools === 'string') agentForm.tools = JSON.parse(agentForm.tools);
		if (typeof agentForm.channels === 'string') agentForm.channels = JSON.parse(agentForm.channels);
		showAgentForm = true;
		showTemplateGallery = false;
		showSkillEditor = false;
		// Load agent skills
		if (!agent.is_system) loadAgentSkills(agent.id);
	}

	async function handleSaveAgent() {
		if (!agentForm.name.trim()) { alert('Name is required'); return; }
		agentSaving = true;
		try {
			let newAgent;
			if (editingAgent) {
				await updateAgent(slug, editingAgent.id, agentForm);
			} else {
				newAgent = await createAgent(slug, agentForm);
			}
			showAgentForm = false;
			await loadAgents();

			// If we were creating an agent for a role, fill the role automatically
			if (pendingRoleFill && newAgent?.id) {
				await fillOrgRole(slug, pendingRoleFill, newAgent.id, 'agent');
				pendingRoleFill = null;
				teamTab = 'orgchart';
				await loadOrgChart();
			}
		} catch (e: any) {
			alert(e.message);
		} finally {
			agentSaving = false;
		}
	}

	async function handleDeleteAgent(agentId: string) {
		if (!confirm('Delete this agent? This cannot be undone.')) return;
		try {
			await deleteAgent(slug, agentId);
			agentsList = agentsList.filter(a => a.id !== agentId);
			if (editingAgent?.id === agentId) {
				showAgentForm = false;
				editingAgent = null;
			}
		} catch (e: any) {
			alert(e.message);
		}
	}

	async function handleToggleAgent(agent: any) {
		try {
			await updateAgent(slug, agent.id, { is_active: !agent.is_active });
			agentsList = agentsList.map(a => a.id === agent.id ? { ...a, is_active: !a.is_active } : a);
		} catch (e: any) {
			alert(e.message);
		}
	}

	async function handleCreateFromTemplate(templateId: string) {
		try {
			await createAgentFromTemplate(slug, templateId);
			showTemplateGallery = false;
			await loadAgents();
		} catch (e: any) {
			alert(e.message);
		}
	}

	async function handleGenerateAgent() {
		const desc = prompt('Describe the agent you want to create:');
		if (!desc) return;
		agentGenerating = true;
		try {
			const config = await generateAgentConfig(slug, desc);
			agentForm = {
				name: config.name || '', description: config.description || '', avatar: config.avatar || '',
				role: config.role || '', goal: config.goal || '', backstory: config.backstory || '',
				instructions: config.instructions || '', constraints: config.constraints || '',
				escalation_prompt: config.escalation_prompt || '', model: '', temperature: 0.7, max_tokens: 2048,
				tools: config.tools || [], channels: [],
				knowledge_access: config.knowledge_access || false,
				memory_access: config.memory_access || false,
				can_delegate: false, max_iterations: 5, trigger_type: 'mention'
			};
			editingAgent = null;
			showAgentForm = true;
		} catch (e: any) {
			alert(e.message);
		} finally {
			agentGenerating = false;
		}
	}

	function toggleTool(tool: string) {
		if (agentForm.tools.includes(tool)) {
			agentForm.tools = agentForm.tools.filter(t => t !== tool);
		} else {
			agentForm.tools = [...agentForm.tools, tool];
		}
	}

	async function handleTeamTabChange(tab: 'members' | 'agents' | 'orgchart') {
		teamTab = tab;
		if (tab === 'agents' && agentsList.length === 0) await loadAgents();
		if (tab === 'orgchart') await loadOrgChart();
	}

	async function handleUpdateProfile(memberId: string, field: string, value: string) {
		try {
			await updateMemberProfile(slug, memberId, { [field]: value });
		} catch (e: any) {
			alert(e.message);
		}
	}

	function handleLeave() {
		clearSession();
		disconnect();
		goto('/');
	}

	async function openPreferences() {
		showPreferences = true;
		prefsTab = 'profile';
		prefsMsg = '';
		try {
			const me = await getMe();
			prefsDisplayName = me.display_name || '';
			prefsEmail = me.email || '';
		} catch { /* ignore */ }
	}

	async function handleSaveProfile() {
		prefsLoading = true;
		prefsMsg = '';
		try {
			await updateMe({ display_name: prefsDisplayName, email: prefsEmail });
			prefsMsg = 'Profile updated';
		} catch (e: any) {
			prefsMsg = e.message;
		}
		prefsLoading = false;
	}

	async function handleChangePassword() {
		if (prefsNewPw !== prefsConfirmPw) {
			prefsMsg = 'Passwords do not match';
			return;
		}
		prefsLoading = true;
		prefsMsg = '';
		try {
			await changePassword({ current_password: prefsCurrentPw, new_password: prefsNewPw });
			prefsMsg = 'Password changed';
			prefsCurrentPw = '';
			prefsNewPw = '';
			prefsConfirmPw = '';
		} catch (e: any) {
			prefsMsg = e.message;
		}
		prefsLoading = false;
	}

	function extractSkillBadge(content: string): { badge: string; cleanContent: string } | null {
		const match = content.match(/^\[skill:([^\]]+)\]\s*/);
		if (match) {
			return { badge: match[1], cleanContent: content.slice(match[0].length) };
		}
		return null;
	}

	function startDMWithAgent(agent: any) {
		// Find or create DM with this agent
		const myId = currentUser?.uid;
		if (!myId) return;
		const expectedName = dmChannelName(myId, agent.id);
		const existingDM = dmChannels.find((ch: any) => ch.name === expectedName);
		if (existingDM) {
			selectChannel(existingDM);
		} else {
			// Create DM channel (backend dedup returns existing if already created)
			createChannel(slug, expectedName, 'dm').then((ch: any) => {
				// Avoid duplicate in store
				if (!$channels.find((c: any) => c.id === ch.id)) {
					channels.update(chs => [...chs, ch]);
				}
				selectChannel(ch);
			}).catch((err: any) => {
				console.error('Failed to create DM with agent:', err);
			});
		}
	}

	// Helper to render message content with inline images and image prompts from markdown
	function renderMessageContent(content: string): { text: string; images: {url: string; alt: string}[]; imagePrompt: string | null } {
		const images: {url: string; alt: string}[] = [];
		let imagePrompt: string | null = null;

		// Extract <image-prompt>...</image-prompt> blocks
		let cleaned = content.replace(/<image-prompt>\n?([\s\S]*?)\n?<\/image-prompt>/g, (_match, prompt) => {
			imagePrompt = prompt.trim();
			return '';
		});

		const text = cleaned.replace(/!\[([^\]]*)\]\(([^)]+)\)/g, (_match, alt, url) => {
			images.push({ url, alt: alt || 'Image' });
			return '';
		}).trim();
		return { text, images, imagePrompt };
	}

	function scrollToBottom() {
		requestAnimationFrame(() => {
			if (messagesEl) messagesEl.scrollTop = messagesEl.scrollHeight;
		});
	}

	function formatTime(iso: string) {
		return new Date(iso).toLocaleTimeString([], { hour: '2-digit', minute: '2-digit' });
	}
</script>

{#if announcement && !announcementDismissed}
<div class="announcement-banner" data-type={announcement.type}>
	<span class="announcement-text">{announcement.message}</span>
	<button class="announcement-dismiss" onclick={dismissAnnouncement}>&times;</button>
</div>
{/if}

<div class="workspace">
	<!-- Sidebar -->
	<aside class="sidebar">
		<div class="sidebar-header">
			<div class="logo-row">
				<svg width="20" height="20" viewBox="0 0 40 40" fill="none">
					<path d="M8 12L20 4L32 12V28L20 36L8 28V12Z" stroke="var(--accent)" stroke-width="2.5" fill="none"/>
					<circle cx="20" cy="20" r="3" fill="var(--accent)"/>
				</svg>
				<span class="logo-text">nexus</span>
			</div>
			<span class="slug-badge">/{slug}</span>
		</div>

		<nav class="sidebar-nav">
			<!-- View Dropdown -->
			<div class="view-dropdown-wrap">
				<select class="view-dropdown" bind:value={activeView} onchange={() => onViewChange()}>
					<option value="chat">Chat</option>
					<option value="board">Tasks — Board</option>
					<option value="list">Tasks — List</option>
					<option value="notes">Notes</option>
					<option value="team">Team</option>
				</select>
			</div>

			{#if activeView === 'chat'}
				<!-- Channels -->
				<div class="nav-section">
					<div class="nav-section-header">
						<span>Channels</span>
						<button class="nav-action" onclick={() => showNewChannel = !showNewChannel} title="New channel">
							<svg width="14" height="14" viewBox="0 0 14 14" fill="none">
								<path d="M7 2V12M2 7H12" stroke="currentColor" stroke-width="1.5" stroke-linecap="round"/>
							</svg>
						</button>
					</div>

					{#if showNewChannel}
						<div class="new-channel-form">
							<input
								type="text"
								placeholder="channel-name"
								bind:value={newChannelName}
								onkeydown={(e) => e.key === 'Enter' && handleCreateChannel()}
							/>
						</div>
					{/if}

					{#each publicChannels as ch}
						<button
							class="nav-item"
							class:active={$activeChannel?.id === ch.id}
							onclick={() => selectChannel(ch)}
						>
							<span class="channel-hash">#</span>
							<span class="channel-name">{ch.name}</span>
						</button>
					{/each}
				</div>

				<!-- Direct Messages -->
				{#if dmChannels.length > 0}
					<div class="nav-section">
						<div class="nav-section-header">
							<span>Direct Messages</span>
						</div>

						{#each dmChannels as ch}
							<button
								class="nav-item"
								class:active={$activeChannel?.id === ch.id}
								onclick={() => selectChannel(ch)}
							>
								<span class="channel-hash">@</span>
								<span class="channel-name">{getDMPartnerName(ch)}</span>
							</button>
						{/each}
					</div>
				{/if}

				<!-- Built-in Agents -->
				{#if builtinAgents.length > 0}
					<div class="nav-section">
						<div class="nav-section-header">
							<span>Agents</span>
						</div>
						{#each builtinAgents as agent}
							<button
								class="nav-item"
								onclick={() => startDMWithAgent(agent)}
							>
								<span class="channel-hash">{agent.avatar || '🤖'}</span>
								<span class="channel-name">{agent.name}</span>
							</button>
						{/each}
					</div>
				{/if}

				<!-- Members -->
				<div class="nav-section">
					<div class="nav-section-header">
						<span>Members</span>
						<span class="member-count">{$members.length}</span>
					</div>

					{#each $members as member}
						<div class="member-row-wrap">
							<button
								class="member-row"
								class:clickable={member.id !== currentUser?.uid}
								class:selected={selectedMember?.id === member.id}
								onclick={() => handleMemberClick(member)}
							>
								<span class="presence" class:online={member.online}></span>
								<span class="member-name">{member.display_name}</span>
								{#if member.role && member.role !== 'member'}
									<span class="role-tag" class:admin-tag={member.role === 'admin'}>{member.role}</span>
								{/if}
							</button>
							{#if isAdmin && member.id !== currentUser?.uid && member.role !== 'brain'}
								<button class="member-gear" onclick={() => handleManageMember(member)} title="Manage member">
									<svg width="12" height="12" viewBox="0 0 12 12" fill="none">
										<circle cx="6" cy="6" r="2" stroke="currentColor" stroke-width="1.2"/>
										<path d="M6 1v1M6 10v1M1 6h1M10 6h1M2.5 2.5l.7.7M8.8 8.8l.7.7M9.5 2.5l-.7.7M3.2 8.8l-.7.7" stroke="currentColor" stroke-width="1" stroke-linecap="round"/>
									</svg>
								</button>
							{/if}
						</div>
					{/each}
				</div>

			{:else if activeView === 'notes'}
				<!-- Notes List -->
				<div class="nav-section">
					<div class="nav-section-header">
						<span>Documents</span>
						<button class="nav-action" onclick={handleCreateDoc} title="New document">
							<svg width="14" height="14" viewBox="0 0 14 14" fill="none">
								<path d="M7 2V12M2 7H12" stroke="currentColor" stroke-width="1.5" stroke-linecap="round"/>
							</svg>
						</button>
					</div>

					{#each docs as doc}
						<button
							class="nav-item"
							class:active={activeDoc?.id === doc.id}
							onclick={() => selectDoc(doc)}
						>
							<svg width="14" height="14" viewBox="0 0 14 14" fill="none" style="flex-shrink:0;opacity:0.5">
								<path d="M4 1h4l4 4v7a1 1 0 01-1 1H4a1 1 0 01-1-1V2a1 1 0 011-1z" stroke="currentColor" stroke-width="1.2"/>
								<path d="M8 1v4h4" stroke="currentColor" stroke-width="1.2"/>
							</svg>
							<span class="channel-name">{doc.title || 'Untitled'}</span>
						</button>
					{/each}

					{#if docs.length === 0}
						<p class="sidebar-empty">No documents yet</p>
					{/if}
				</div>

			{:else if activeView === 'team'}
				<div class="nav-section">
					<div class="nav-section-header">
						<span>Team</span>
					</div>
					<button class="nav-item" class:active={teamTab === 'orgchart'} onclick={() => handleTeamTabChange('orgchart')}>
						<svg width="14" height="14" viewBox="0 0 14 14" fill="none" style="flex-shrink:0;opacity:0.5">
							<rect x="4.5" y="1" width="5" height="3" rx="0.75" stroke="currentColor" stroke-width="1.2"/>
							<rect x="0.5" y="9" width="4" height="3" rx="0.75" stroke="currentColor" stroke-width="1.2"/>
							<rect x="9.5" y="9" width="4" height="3" rx="0.75" stroke="currentColor" stroke-width="1.2"/>
							<path d="M7 4v2.5M7 6.5H2.5V9M7 6.5h4.5V9" stroke="currentColor" stroke-width="1.2"/>
						</svg>
						<span class="channel-name">Org Chart</span>
					</button>
					<button class="nav-item" class:active={teamTab === 'members'} onclick={() => handleTeamTabChange('members')}>
						<svg width="14" height="14" viewBox="0 0 14 14" fill="none" style="flex-shrink:0;opacity:0.5">
							<circle cx="7" cy="4" r="2.5" stroke="currentColor" stroke-width="1.2"/>
							<path d="M2 12.5c0-2.5 2-4 5-4s5 1.5 5 4" stroke="currentColor" stroke-width="1.2" stroke-linecap="round"/>
						</svg>
						<span class="channel-name">Members</span>
					</button>
					<button class="nav-item" class:active={teamTab === 'agents'} onclick={() => handleTeamTabChange('agents')}>
						<svg width="14" height="14" viewBox="0 0 14 14" fill="none" style="flex-shrink:0;opacity:0.5">
							<rect x="3" y="2" width="8" height="6" rx="1" stroke="currentColor" stroke-width="1.2"/>
							<circle cx="5.5" cy="5" r="0.75" fill="currentColor"/>
							<circle cx="8.5" cy="5" r="0.75" fill="currentColor"/>
							<path d="M5 10v2M9 10v2" stroke="currentColor" stroke-width="1.2" stroke-linecap="round"/>
						</svg>
						<span class="channel-name">Agents</span>
					</button>
				</div>
			{/if}
		</nav>

		<div class="user-menu-wrap">
			{#if inviteUrl}
				<div class="invite-bar">
					<input type="text" value={inviteUrl} readonly onclick={(e) => (e.target as HTMLInputElement).select()} />
					<button class="btn btn-primary btn-sm" onclick={handleCopyInvite}>Copy</button>
				</div>
			{/if}

			<button class="user-menu-trigger" onclick={() => showUserMenu = !showUserMenu}>
				<span class="user-avatar">{userInitial}</span>
				<span class="user-name">{currentUser?.name || 'Anonymous'}</span>
				<svg width="12" height="12" viewBox="0 0 12 12" fill="none" class="user-chevron" class:open={showUserMenu}>
					<path d="M3 5L6 8L9 5" stroke="currentColor" stroke-width="1.5" stroke-linecap="round" stroke-linejoin="round"/>
				</svg>
			</button>

			{#if showUserMenu}
				<!-- svelte-ignore a11y_no_static_element_interactions -->
				<div class="user-menu-backdrop" onclick={() => showUserMenu = false}></div>
				<div class="user-menu-popover">
					<button class="user-menu-item" onclick={() => { handleInvite(); showUserMenu = false; }}>
						<svg width="14" height="14" viewBox="0 0 14 14" fill="none">
							<path d="M9 7.5a2.5 2.5 0 100-5 2.5 2.5 0 000 5zM1.5 12.5c0-2.5 2-4 4-4" stroke="currentColor" stroke-width="1.2" stroke-linecap="round"/>
							<path d="M10 9V13M8 11H12" stroke="currentColor" stroke-width="1.2" stroke-linecap="round"/>
						</svg>
						Invite People
					</button>
					<button class="user-menu-item" onclick={() => { openPreferences(); showUserMenu = false; }}>
						<svg width="14" height="14" viewBox="0 0 14 14" fill="none">
							<path d="M7 9a2 2 0 100-4 2 2 0 000 4z" stroke="currentColor" stroke-width="1.2"/>
							<path d="M12 7a5 5 0 11-10 0 5 5 0 0110 0z" stroke="currentColor" stroke-width="1.2"/>
						</svg>
						Preferences
					</button>
					<button class="user-menu-item" onclick={() => { openAgentLibrary(); showUserMenu = false; }}>
						<svg width="14" height="14" viewBox="0 0 14 14" fill="none">
							<rect x="1" y="1" width="5" height="5" rx="1" stroke="currentColor" stroke-width="1.2"/>
							<rect x="8" y="1" width="5" height="5" rx="1" stroke="currentColor" stroke-width="1.2"/>
							<rect x="1" y="8" width="5" height="5" rx="1" stroke="currentColor" stroke-width="1.2"/>
							<rect x="8" y="8" width="5" height="5" rx="1" stroke="currentColor" stroke-width="1.2"/>
						</svg>
						Agent Library
					</button>
					{#if isAdmin}
						<button class="user-menu-item" onclick={() => { activeView = 'brain'; onViewChange(); showUserMenu = false; }}>
							<svg width="14" height="14" viewBox="0 0 14 14" fill="none">
								<circle cx="7" cy="7" r="5.5" stroke="currentColor" stroke-width="1.2"/>
								<circle cx="7" cy="7" r="1.5" fill="currentColor"/>
								<path d="M7 1.5V3.5M7 10.5V12.5M1.5 7H3.5M10.5 7H12.5" stroke="currentColor" stroke-width="1.2" stroke-linecap="round"/>
							</svg>
							Brain Settings
						</button>
					{/if}
					<div class="user-menu-divider"></div>
					<button class="user-menu-item user-menu-danger" onclick={() => { handleLeave(); showUserMenu = false; }}>
						<svg width="14" height="14" viewBox="0 0 14 14" fill="none">
							<path d="M5 1.5H3a1.5 1.5 0 00-1.5 1.5v8A1.5 1.5 0 003 12.5h2M9.5 10l3-3-3-3M5 7h7.5" stroke="currentColor" stroke-width="1.2" stroke-linecap="round" stroke-linejoin="round"/>
						</svg>
						Leave Workspace
					</button>
				</div>
			{/if}
		</div>
	</aside>

	<!-- Member Panel (admin, chat only) -->
	{#if activeView === 'chat' && selectedMember && memberDetail}
		<aside class="member-panel">
			<div class="panel-header">
				<h3>Manage Member</h3>
				<button class="panel-close" onclick={() => { selectedMember = null; memberDetail = null; }}>
					<svg width="14" height="14" viewBox="0 0 14 14" fill="none">
						<path d="M2 2L12 12M12 2L2 12" stroke="currentColor" stroke-width="1.5" stroke-linecap="round"/>
					</svg>
				</button>
			</div>
			<div class="panel-body">
				<div class="panel-avatar">
					{memberDetail.display_name.charAt(0).toUpperCase()}
				</div>
				<p class="panel-name">{memberDetail.display_name}</p>

				<div class="panel-field">
					<label>Role</label>
					<select value={memberDetail.role} onchange={(e) => handleRoleChange((e.target as HTMLSelectElement).value)}>
						{#each ROLES as role}
							<option value={role}>{role.replace(/_/g, ' ')}</option>
						{/each}
					</select>
				</div>

				<div class="panel-field">
					<label>Permissions</label>
					<div class="perm-list">
						{#each Object.entries(memberDetail.permissions) as [perm, granted]}
							<div class="perm-row">
								<span class="perm-name">{perm}</span>
								<span class="perm-val" class:granted={granted}>{granted ? 'yes' : 'no'}</span>
							</div>
						{/each}
					</div>
				</div>

				<button class="btn btn-sm kick-btn" onclick={handleKick}>Remove from workspace</button>
			</div>
		</aside>
	{/if}

	{#if activeView === 'chat'}
	<!-- Chat area + member drawer wrapper -->
	<div class="chat-area-wrapper">
	<main class="chat-main">
		{#if $activeChannel}
			<!-- Channel header -->
			<header class="chat-header">
				<div class="chat-header-left">
					{#if isDMChannel($activeChannel)}
						<span class="header-hash">@</span>
						<h2>{getDMPartnerName($activeChannel)}</h2>
					{:else}
						<span class="header-hash">#</span>
						<h2>{$activeChannel.name}</h2>
					{/if}
				</div>
				<div class="chat-header-right">
					{#if isDMChannel($activeChannel)}
						{#if isAdmin}
							<button class="clear-chat-btn" title="Clear chat history" onclick={() => { if (confirm('Clear all messages in this conversation? This cannot be undone.')) clearChannel($activeChannel.id); }}>
								<svg width="14" height="14" viewBox="0 0 14 14" fill="none">
									<path d="M2 4h10M5 4V3a1 1 0 011-1h2a1 1 0 011 1v1M6 6.5v4M8 6.5v4M3 4l.5 7.5a1.5 1.5 0 001.5 1.5h4a1.5 1.5 0 001.5-1.5L11 4" stroke="currentColor" stroke-width="1.2" stroke-linecap="round" stroke-linejoin="round"/>
								</svg>
							</button>
						{/if}
						<span class="header-meta">Direct Message</span>
					{:else}
						<div class="online-members">
							{#each onlineMembersList.slice(0, 5) as om}
								<div class="online-avatar" title={om.display_name}>
									{om.display_name?.charAt(0)?.toUpperCase() || '?'}
									<span class="presence-dot"></span>
								</div>
							{/each}
							{#if onlineMembersList.length > 5}
								<span class="online-overflow">+{onlineMembersList.length - 5}</span>
							{/if}
						</div>
						<button class="member-drawer-toggle" class:active={showMemberDrawer} onclick={() => showMemberDrawer = !showMemberDrawer} title="Toggle member list">
							<svg width="16" height="16" viewBox="0 0 16 16" fill="none">
								<path d="M6 4a2 2 0 100-4 2 2 0 000 4zM1 8c0-1.7 1.3-3 3-3h4c1.7 0 3 1.3 3 3" stroke="currentColor" stroke-width="1.3" stroke-linecap="round"/>
								<path d="M12 5a1.5 1.5 0 100-3 1.5 1.5 0 000 3zM14.5 8.5c0-1.1-.9-2-2-2h-.5" stroke="currentColor" stroke-width="1.2" stroke-linecap="round"/>
							</svg>
							<span>{$members.length}</span>
						</button>
					{/if}
				</div>
			</header>

			<!-- Messages -->
			<div class="messages-area" bind:this={messagesEl}>
				{#if $messages.length === 0}
					<div class="empty-state">
						<div class="empty-icon">
							<svg width="40" height="40" viewBox="0 0 40 40" fill="none">
								<rect x="4" y="8" width="32" height="24" rx="4" stroke="var(--accent)" stroke-width="1.5" fill="none" opacity="0.3"/>
								<path d="M4 16H36" stroke="var(--accent)" stroke-width="1" opacity="0.2"/>
								<circle cx="12" cy="24" r="2" fill="var(--accent)" opacity="0.3"/>
								<line x1="18" y1="23" x2="30" y2="23" stroke="var(--accent)" stroke-width="1.5" opacity="0.2" stroke-linecap="round"/>
								<line x1="18" y1="27" x2="26" y2="27" stroke="var(--accent)" stroke-width="1.5" opacity="0.15" stroke-linecap="round"/>
							</svg>
						</div>
						{#if isDMChannel($activeChannel)}
							<p class="empty-title">DM with {getDMPartnerName($activeChannel)}</p>
						{:else}
							<p class="empty-title">Welcome to #{$activeChannel.name}</p>
						{/if}
						<p class="empty-sub">This is the beginning of the conversation.</p>
					</div>
				{/if}

				{#each $messages as msg (msg.id)}
					<div class="message-row">
						<div class="avatar">
							{msg.sender_name.charAt(0).toUpperCase()}
						</div>
						<div class="message-body">
							<div class="message-meta">
								<span class="sender">{msg.sender_name}</span>
								<span class="timestamp">{formatTime(msg.created_at)}</span>
								{#if msg.edited_at}
									<span class="edited-tag">edited</span>
								{/if}
							</div>
							{#if msg.file}
								<div class="message-file">
									{#if isImageMime(msg.file.mime)}
										<a href={msg.file.url} target="_blank" rel="noopener">
											<img src={msg.file.url} alt={msg.file.name} class="file-preview-img" />
										</a>
									{:else}
										<a href={msg.file.url} class="file-link" download={msg.file.name}>
											<svg width="16" height="16" viewBox="0 0 16 16" fill="none"><path d="M4 14h8a1 1 0 001-1V6l-4-4H4a1 1 0 00-1 1v10a1 1 0 001 1z" stroke="currentColor" stroke-width="1.2"/><path d="M9 2v4h4" stroke="currentColor" stroke-width="1.2"/></svg>
											<span>{msg.file.name}</span>
											<span class="file-size">({(msg.file.size / 1024).toFixed(1)} KB)</span>
										</a>
									{/if}
								</div>
							{:else}
								{@const skillInfo = extractSkillBadge(msg.content)}
								{@const rendered = renderMessageContent(skillInfo ? skillInfo.cleanContent : msg.content)}
								{#if skillInfo}
									<div class="skill-badge">{skillInfo.badge}</div>
								{/if}
								<div class="message-text">{rendered.text}</div>
								{#each rendered.images as img}
									<div class="message-file">
										<a href={img.url} target="_blank" rel="noopener">
											<img src={img.url} alt={img.alt} class="file-preview-img" />
										</a>
									</div>
								{/each}
								{#if rendered.imagePrompt}
									<details class="image-prompt-details">
										<summary class="image-prompt-toggle">
											<svg width="12" height="12" viewBox="0 0 12 12" fill="none">
												<path d="M2 4l4 4 4-4" stroke="currentColor" stroke-width="1.2" stroke-linecap="round" stroke-linejoin="round"/>
											</svg>
											Image prompt
										</summary>
										<pre class="image-prompt-content">{rendered.imagePrompt}</pre>
									</details>
								{/if}
							{/if}
						</div>
					</div>
				{/each}
			</div>

			<!-- Typing indicator -->
			{#if $typingUsers.size > 0}
				<div class="typing-bar">
					<span class="typing-dots">
						<span></span><span></span><span></span>
					</span>
					<span>{[...$typingUsers.values()].join(', ')} is typing...</span>
				</div>
			{/if}

			<!-- Agent state indicators -->
			{#each [...agentStates.entries()] as [agentId, agentState]}
				{#if agentState.channelID === $activeChannel?.id}
					<div class="typing-bar agent-state-bar">
						<span class="agent-state-dot"></span>
						{#if agentState.state === 'thinking'}
							<span>{agentState.agentName} is thinking...</span>
						{:else if agentState.state === 'tool_executing'}
							<span>{agentState.agentName} is executing {agentState.toolName}...</span>
						{/if}
					</div>
				{/if}
			{/each}

			<!-- @-mention autocomplete popup -->
			{#if mentionActive && mentionResults.length > 0}
				<div class="mention-popup">
					{#each mentionResults as m, i}
						<button
							class="mention-item"
							class:active={i === mentionIndex}
							onmousedown={(e) => { e.preventDefault(); insertMention(m); }}
						>
							<span class="mention-name">{m.display_name}</span>
							<span class="mention-role">{m.role}</span>
						</button>
					{/each}
				</div>
			{/if}

			<!-- Input -->
			<div class="input-bar">
				<div class="input-wrapper">
					<input type="file" bind:this={fileInputEl} onchange={handleFileUpload} style="display:none" />
					<button
						class="attach-button"
						onclick={() => fileInputEl?.click()}
						disabled={uploading}
						title="Attach file"
					>
						{#if uploading}
							<svg width="16" height="16" viewBox="0 0 16 16" class="spin"><circle cx="8" cy="8" r="6" stroke="currentColor" stroke-width="1.5" fill="none" stroke-dasharray="20 12"/></svg>
						{:else}
							<svg width="16" height="16" viewBox="0 0 16 16" fill="none"><path d="M14 8l-5.3 5.3a3.5 3.5 0 01-5 0 3.5 3.5 0 010-5L9 3a2 2 0 013 0 2 2 0 010 3L6.5 11.5a.5.5 0 01-.7 0 .5.5 0 010-.7L11 5.5" stroke="currentColor" stroke-width="1.2" stroke-linecap="round"/></svg>
						{/if}
					</button>
					<input
						type="text"
						placeholder={isDMChannel($activeChannel) ? `Message ${getDMPartnerName($activeChannel)}...` : `Message #${$activeChannel.name}...`}
						bind:value={input}
						bind:this={inputEl}
						onkeydown={handleInputKeydown}
						oninput={handleMentionInput}
					/>
					<button
						class="send-button"
						onclick={handleSend}
						disabled={!input.trim()}
						title="Send message"
					>
						<svg width="18" height="18" viewBox="0 0 18 18" fill="none">
							<path d="M2 9L16 2L9 16L7.5 10.5L2 9Z" fill="currentColor"/>
						</svg>
					</button>
				</div>
			</div>
		{:else}
			<div class="empty-state">
				<p class="empty-sub">Select a channel to start chatting</p>
			</div>
		{/if}
	</main>

	<!-- Member Drawer -->
	{#if showMemberDrawer && $activeChannel && !isDMChannel($activeChannel)}
		<aside class="member-drawer">
			<div class="drawer-header">
				<h3>Members</h3>
				<button class="drawer-close" onclick={() => showMemberDrawer = false}>
					<svg width="14" height="14" viewBox="0 0 14 14" fill="none">
						<path d="M2 2L12 12M12 2L2 12" stroke="currentColor" stroke-width="1.5" stroke-linecap="round"/>
					</svg>
				</button>
			</div>
			<div class="drawer-section">
				<div class="drawer-section-label">Online — {onlineMembersList.length}</div>
				{#each onlineMembersList as om}
					<div class="drawer-member">
						<div class="drawer-avatar">
							{om.display_name?.charAt(0)?.toUpperCase() || '?'}
							<span class="presence-dot online"></span>
						</div>
						<div class="drawer-member-info">
							<span class="drawer-member-name">{om.display_name}</span>
							<span class="drawer-member-role">{om.role}</span>
						</div>
					</div>
				{/each}
			</div>
			{#if offlineMembers().length > 0}
				<div class="drawer-section">
					<div class="drawer-section-label">Offline — {offlineMembers().length}</div>
					{#each offlineMembers() as m}
						<div class="drawer-member offline">
							<div class="drawer-avatar">
								{m.display_name?.charAt(0)?.toUpperCase() || '?'}
							</div>
							<div class="drawer-member-info">
								<span class="drawer-member-name">{m.display_name}</span>
								<span class="drawer-member-role">{m.role}</span>
							</div>
						</div>
					{/each}
				</div>
			{/if}
		</aside>
	{/if}
	</div>

	{:else if activeView === 'board'}
	<!-- Board View -->
	<main class="task-main">
		<header class="task-header">
			<h2>Tasks — Board</h2>
			<button class="btn btn-primary btn-sm" onclick={() => showNewTask = !showNewTask}>+ New Task</button>
		</header>

		{#if showNewTask}
			<div class="new-task-bar">
				<input type="text" placeholder="Task title..." bind:value={newTaskTitle} onkeydown={(e) => e.key === 'Enter' && handleCreateTask()} />
				<select bind:value={newTaskPriority}>
					{#each PRIORITIES as p}<option value={p}>{p}</option>{/each}
				</select>
				<select bind:value={newTaskStatus}>
					{#each STATUSES as s}<option value={s}>{STATUS_LABELS[s]}</option>{/each}
				</select>
				<button class="btn btn-primary btn-sm" onclick={handleCreateTask}>Create</button>
			</div>
		{/if}

		<div class="board">
			{#each STATUSES.filter(s => s !== 'cancelled') as status}
				<div class="board-col">
					<div class="board-col-header">
						<span>{STATUS_LABELS[status]}</span>
						<span class="board-count">{tasks.filter(t => t.status === status).length}</span>
					</div>
					<div class="board-cards">
						{#each tasks.filter(t => t.status === status) as task (task.id)}
							<div class="task-card" onclick={() => editingTask = editingTask?.id === task.id ? null : task}>
								<div class="task-card-header">
									<span class="task-priority-dot" style="background: {PRIORITY_COLORS[task.priority]}"></span>
									<span class="task-title">{task.title}</span>
								</div>
								{#if task.tags?.length > 0}
									<div class="task-tags">
										{#each task.tags as tag}
											<span class="task-tag">{tag}</span>
										{/each}
									</div>
								{/if}
								{#if task.due_date}
									<div class="task-due">Due {task.due_date}</div>
								{/if}
								{#if editingTask?.id === task.id}
									<div class="task-card-actions">
										<select value={task.status} onchange={(e) => handleTaskStatusChange(task.id, (e.target as HTMLSelectElement).value)}>
											{#each STATUSES as s}<option value={s}>{STATUS_LABELS[s]}</option>{/each}
										</select>
										<select value={task.priority} onchange={(e) => handleTaskPriorityChange(task.id, (e.target as HTMLSelectElement).value)}>
											{#each PRIORITIES as p}<option value={p}>{p}</option>{/each}
										</select>
										<button class="btn-del" onclick={() => handleDeleteTask(task.id)}>Delete</button>
									</div>
								{/if}
							</div>
						{/each}
					</div>
				</div>
			{/each}
		</div>
	</main>

	{:else if activeView === 'list'}
	<!-- List View -->
	<main class="task-main">
		<header class="task-header">
			<h2>Tasks — List</h2>
			<button class="btn btn-primary btn-sm" onclick={() => showNewTask = !showNewTask}>+ New Task</button>
		</header>

		{#if showNewTask}
			<div class="new-task-bar">
				<input type="text" placeholder="Task title..." bind:value={newTaskTitle} onkeydown={(e) => e.key === 'Enter' && handleCreateTask()} />
				<select bind:value={newTaskPriority}>
					{#each PRIORITIES as p}<option value={p}>{p}</option>{/each}
				</select>
				<select bind:value={newTaskStatus}>
					{#each STATUSES as s}<option value={s}>{STATUS_LABELS[s]}</option>{/each}
				</select>
				<button class="btn btn-primary btn-sm" onclick={handleCreateTask}>Create</button>
			</div>
		{/if}

		<div class="task-list">
			<div class="task-list-header">
				<span class="tl-pri">Pri</span>
				<span class="tl-title">Title</span>
				<span class="tl-status">Status</span>
				<span class="tl-tags">Tags</span>
				<span class="tl-date">Created</span>
				<span class="tl-actions"></span>
			</div>
			{#each tasks as task (task.id)}
				<div class="task-list-row">
					<span class="tl-pri">
						<select value={task.priority} onchange={(e) => handleTaskPriorityChange(task.id, (e.target as HTMLSelectElement).value)}>
							{#each PRIORITIES as p}<option value={p}>{p}</option>{/each}
						</select>
					</span>
					<span class="tl-title">{task.title}</span>
					<span class="tl-status">
						<select value={task.status} onchange={(e) => handleTaskStatusChange(task.id, (e.target as HTMLSelectElement).value)}>
							{#each STATUSES as s}<option value={s}>{STATUS_LABELS[s]}</option>{/each}
						</select>
					</span>
					<span class="tl-tags">
						{#if task.tags?.length > 0}
							{#each task.tags as tag}<span class="task-tag">{tag}</span>{/each}
						{/if}
					</span>
					<span class="tl-date">{formatTime(task.created_at)}</span>
					<span class="tl-actions">
						<button class="btn-del-sm" onclick={() => handleDeleteTask(task.id)} title="Delete">✕</button>
					</span>
				</div>
			{/each}
			{#if tasks.length === 0}
				<div class="empty-state" style="padding: 3rem;">
					<p class="empty-sub">No tasks yet. Create one to get started.</p>
				</div>
			{/if}
		</div>
	</main>

	{:else if activeView === 'notes'}
	<!-- Notes View -->
	<main class="notes-main">
		{#if activeDoc}
			<div class="notes-editor">
				<div class="notes-toolbar">
					<input
						type="text"
						class="notes-title-input"
						placeholder="Document title..."
						bind:value={docTitle}
						onblur={handleSaveDoc}
					/>
					<div class="notes-toolbar-actions">
						<span class="notes-saved" class:visible={docSaving}>Saving...</span>
						<button class="btn btn-ghost btn-sm" onclick={handleSaveDoc} disabled={docSaving}>Save</button>
						<button class="btn btn-ghost btn-sm notes-del-btn" onclick={() => handleDeleteDoc(activeDoc.id)}>Delete</button>
					</div>
				</div>
				<TiptapEditor
					bind:this={editorRef}
					onsave={handleAutoSave}
					placeholder="Start writing..."
				/>
				<div class="notes-meta">
					<span>Created {formatTime(activeDoc.created_at)}</span>
					{#if activeDoc.updated_at !== activeDoc.created_at}
						<span>· Updated {formatTime(activeDoc.updated_at)}</span>
					{/if}
				</div>
			</div>
		{:else}
			<div class="empty-state">
				<div class="empty-icon">
					<svg width="48" height="48" viewBox="0 0 48 48" fill="none">
						<path d="M12 4h16l12 12v24a4 4 0 01-4 4H12a4 4 0 01-4-4V8a4 4 0 014-4z" stroke="var(--accent)" stroke-width="1.5" fill="none" opacity="0.3"/>
						<path d="M28 4v12h12" stroke="var(--accent)" stroke-width="1.5" opacity="0.3"/>
						<path d="M16 24h16M16 30h12M16 36h8" stroke="var(--accent)" stroke-width="1.5" opacity="0.2" stroke-linecap="round"/>
					</svg>
				</div>
				<p class="empty-title">Notes</p>
				<p class="empty-sub">Select a document or create a new one</p>
				<button class="btn btn-primary btn-sm" style="margin-top: 1rem" onclick={handleCreateDoc}>+ New Document</button>
			</div>
		{/if}
	</main>

	{:else if activeView === 'brain'}
	<!-- Brain Settings View -->
	<main class="brain-main">
		<div class="brain-settings">
			<h2 class="brain-heading">Brain Configuration</h2>

			<div class="brain-tabs">
				<button class="brain-tab" class:active={brainTab === 'settings'} onclick={() => brainTab = 'settings'}>Settings</button>
				<button class="brain-tab" class:active={brainTab === 'definitions'} onclick={() => brainTab = 'definitions'}>Personality</button>
				<button class="brain-tab" class:active={brainTab === 'memory'} onclick={() => { brainTab = 'memory'; loadMemories(); }}>Memory</button>
				<button class="brain-tab" class:active={brainTab === 'activity'} onclick={() => { brainTab = 'activity'; loadActions(); }}>Activity</button>
				<button class="brain-tab" class:active={brainTab === 'skills'} onclick={() => { brainTab = 'skills'; loadSkills(); }}>Skills</button>
				<button class="brain-tab" class:active={brainTab === 'knowledge'} onclick={() => { brainTab = 'knowledge'; loadKnowledge(); }}>Knowledge</button>
				<button class="brain-tab" class:active={brainTab === 'integrations'} onclick={() => { brainTab = 'integrations'; loadIntegrations(); }}>Integrations</button>
			</div>

			{#if brainTab === 'settings'}
			<div class="brain-section">
				<h3 class="brain-section-title">API Settings</h3>
				<div class="brain-field">
					<label>OpenRouter API Key</label>
					{#if brainSettings.api_key_set === 'true'}
						<div class="brain-key-status">Key set ({brainSettings.api_key_masked})</div>
					{/if}
					<input
						type="password"
						class="brain-input"
						placeholder="sk-or-v1-..."
						bind:value={brainApiKey}
					/>
					<span class="brain-hint">Get a key at <a href="https://openrouter.ai/keys" target="_blank" rel="noopener">openrouter.ai/keys</a></span>
				</div>

				<div class="brain-field">
					<label>Model</label>
					<div style="display: flex; gap: 0.5rem; align-items: center;">
						<select class="brain-input" bind:value={brainModel} style="flex:1">
							{#if pinnedModels.length > 0}
								{#each pinnedModels as m}
									<option value={m.id}>{m.display_name}</option>
								{/each}
							{:else}
								<option value="anthropic/claude-sonnet-4">Claude Sonnet 4</option>
								<option value="anthropic/claude-haiku-4">Claude Haiku 4</option>
								<option value="openai/gpt-4o">GPT-4o</option>
								<option value="openai/gpt-4o-mini">GPT-4o Mini</option>
								<option value="google/gemini-2.5-flash">Gemini 2.5 Flash</option>
								<option value="meta-llama/llama-3.3-70b-instruct">Llama 3.3 70B</option>
							{/if}
						</select>
						{#if isAdmin}
							<button class="btn btn-ghost btn-sm" onclick={openModelBrowser}>Browse</button>
						{/if}
					</div>
				</div>

				<div class="brain-field">
					<label>Gemini API Key</label>
					{#if brainSettings.gemini_api_key_set === 'true'}
						<div class="brain-key-status">Key set ({brainSettings.gemini_api_key_masked})</div>
					{/if}
					<input
						type="password"
						class="brain-input"
						placeholder="AIza..."
						bind:value={brainGeminiKey}
					/>
					<span class="brain-hint">For image generation. Get a key at <a href="https://aistudio.google.com/apikey" target="_blank" rel="noopener">aistudio.google.com</a></span>
				</div>

				<div class="brain-field">
					<label>Image Model</label>
					<select class="brain-input" bind:value={brainImageModel}>
						<option value="gemini-2.5-flash-image">Gemini 2.5 Flash Image</option>
						<option value="gemini-3-pro-image-preview">Gemini 3 Pro Image</option>
						<option value="gemini-3.1-flash-image-preview">Gemini 3.1 Flash Image</option>
					</select>
					<span class="brain-hint">Used by agents with the generate_image tool (via Gemini API)</span>
				</div>

				<button class="btn btn-primary btn-sm" onclick={saveBrainSettings} disabled={brainSaving}>
					{brainSaving ? 'Saving...' : 'Save Settings'}
				</button>
			</div>

			<div class="brain-section">
				<h3 class="brain-section-title">Memory</h3>
				<div class="brain-field">
					<label class="brain-toggle-row">
						<input type="checkbox" bind:checked={brainMemoryEnabled} />
						<span>Enable automatic memory extraction</span>
					</label>
					<span class="brain-hint">Brain extracts key facts, decisions, and commitments from conversations automatically.</span>
				</div>

				{#if brainMemoryEnabled}
				<div class="brain-field">
					<label>Memory Model</label>
					<select class="brain-input" bind:value={brainMemoryModel}>
						<option value="openai/gpt-4o-mini">GPT-4o Mini</option>
						<option value="openai/gpt-4o">GPT-4o</option>
						<option value="anthropic/claude-haiku-4">Claude Haiku 4</option>
						<option value="anthropic/claude-sonnet-4">Claude Sonnet 4</option>
						<option value="google/gemini-2.5-flash">Gemini 2.5 Flash</option>
						<option value="meta-llama/llama-3.3-70b-instruct">Llama 3.3 70B</option>
					</select>
					<span class="brain-hint">Model used for memory extraction. Cheaper models work well here.</span>
				</div>

				<div class="brain-field">
					<label>Extraction frequency</label>
					<div class="brain-freq-row">
						<input type="range" min="5" max="50" step="5" bind:value={brainExtractFreq} class="brain-range" />
						<span class="brain-freq-val">Every {brainExtractFreq} messages</span>
					</div>
					<span class="brain-hint">Lower = more frequent extraction (uses more API calls). Default is 15.</span>
				</div>
				{/if}
			</div>

			<div class="brain-section">
				<h3 class="brain-section-title">Usage</h3>
				<p class="brain-hint">Mention <strong>@Brain</strong> in any channel to get a response. Brain can create tasks, search messages, and write documents. It reads the last 20 messages plus stored memories for context.</p>
			</div>

			<div class="brain-section">
				<h3 class="brain-section-title">Built-in Agents</h3>
				<p class="brain-hint" style="margin-bottom: 0.75rem">Toggle built-in agents on or off. Brain is always active.</p>
				{#each agentsList.filter((a: any) => a.is_system && a.id !== 'brain') as agent}
					<label class="brain-toggle-row">
						<input type="checkbox" checked={agent.is_active}
							onchange={(e) => {
								const enabled = (e.target as HTMLInputElement).checked;
								updateBrainSettings(slug, { [`builtin_agent_${agent.id}_enabled`]: enabled ? 'true' : 'false' })
									.then(() => loadAgents())
									.catch(() => {});
							}}
						/>
						<span>{agent.avatar} {agent.name}</span>
						<span class="brain-hint" style="margin-left: auto; font-size: 0.75rem">{agent.role}</span>
					</label>
				{/each}
			</div>

			{:else if brainTab === 'definitions'}
			<div class="brain-section">
				<p class="brain-hint" style="margin-bottom: 0.75rem">These files shape Brain's personality and behavior. Edit them to customize how Brain acts in your workspace.</p>

				<div class="brain-files">
					{#each brainDefFiles as file}
						<button
							class="brain-file-btn"
							class:active={brainActiveFile === file}
							onclick={() => selectBrainFile(file)}
						>
							{file}
						</button>
					{/each}
				</div>

				{#if brainActiveFile}
					<div class="brain-editor">
						<div class="brain-editor-header">
							<span class="brain-file-name">{brainActiveFile}</span>
							<button class="btn btn-primary btn-sm" onclick={saveBrainFile} disabled={brainSaving}>
								{brainSaving ? 'Saving...' : 'Save'}
							</button>
						</div>
						<textarea
							class="brain-file-content"
							bind:value={brainFileContent}
						></textarea>
					</div>
				{/if}
			</div>

			{:else if brainTab === 'memory'}
			<div class="brain-section">
				<div class="memory-stats">
					{#each Object.entries(memoryCounts) as [type, count]}
						<div class="memory-stat">
							<span class="memory-stat-count">{count}</span>
							<span class="memory-stat-label">{type}s</span>
						</div>
					{/each}
					{#if Object.keys(memoryCounts).length === 0}
						<p class="brain-hint">No memories yet. Brain extracts memories automatically as conversations happen.</p>
					{/if}
				</div>

				{#if memories.length > 0}
					<div class="memory-actions">
						<button class="btn btn-ghost btn-sm memory-clear-btn" onclick={handleClearMemories}>Clear All Memories</button>
					</div>

					<div class="memory-list">
						{#each memories as mem}
							<div class="memory-item">
								<span class="memory-type-badge" data-type={mem.type}>{mem.type}</span>
								<span class="memory-content">{mem.content}</span>
								<button class="memory-delete" onclick={() => handleDeleteMemory(mem.id)} title="Delete">
									<svg width="12" height="12" viewBox="0 0 12 12" fill="none">
										<path d="M2 2L10 10M10 2L2 10" stroke="currentColor" stroke-width="1.5" stroke-linecap="round"/>
									</svg>
								</button>
							</div>
						{/each}
					</div>
				{/if}
			</div>

			{:else if brainTab === 'activity'}
			<div class="brain-section">
				<p class="brain-hint" style="margin-bottom: 0.75rem">{brainActionsTotal} total actions logged</p>

				{#if brainActions.length === 0}
					<p class="brain-hint">No activity yet. Brain logs actions when responding to mentions.</p>
				{:else}
					<div class="action-list">
						{#each brainActions as action}
							<div class="action-item">
								<div class="action-header">
									<span class="action-type-badge" data-type={action.action_type}>{action.action_type}</span>
									<span class="action-model">{action.model}</span>
									<span class="action-time">{new Date(action.created_at).toLocaleString()}</span>
								</div>
								{#if action.trigger_text}
									<div class="action-trigger">{action.trigger_text}</div>
								{/if}
								{#if action.tools_used?.length > 0}
									<div class="action-tools">
										{#each action.tools_used as tool}
											<span class="action-tool-badge">{tool}</span>
										{/each}
									</div>
								{/if}
								{#if action.response_text}
									<div class="action-response">{action.response_text}</div>
								{/if}
							</div>
						{/each}
					</div>
				{/if}
			</div>

			{:else if brainTab === 'skills'}
			<div class="brain-section">
				<p class="brain-hint" style="margin-bottom: 0.75rem">Skills define specialized behaviors Brain can perform. Edit or add new skill files.</p>

				<div class="skill-list">
					{#each brainSkills as skill}
						<div class="skill-item" class:active={activeSkill?.file_name === skill.file_name}>
							<button class="skill-select" onclick={() => selectSkill(skill)}>
								<span class="skill-name">{skill.name}</span>
								<span class="skill-desc">{skill.description}</span>
								<span class="skill-meta">{skill.trigger} &middot; {skill.autonomy}</span>
							</button>
							{#if isAdmin}
								<button class="skill-delete" onclick={() => handleDeleteSkill(skill.file_name)} title="Delete">
									<svg width="12" height="12" viewBox="0 0 12 12" fill="none">
										<path d="M2 2L10 10M10 2L2 10" stroke="currentColor" stroke-width="1.5" stroke-linecap="round"/>
									</svg>
								</button>
							{/if}
						</div>
					{/each}
					{#if brainSkills.length === 0}
						<p class="brain-hint">No skills configured. Skills will be created automatically for new workspaces.</p>
					{/if}
				</div>

				{#if activeSkill}
					<div class="brain-editor" style="margin-top: 0.75rem">
						<div class="brain-editor-header">
							<span class="brain-file-name">{activeSkill.file_name}</span>
							{#if isAdmin}
								<button class="btn btn-primary btn-sm" onclick={saveSkill} disabled={brainSaving}>
									{brainSaving ? 'Saving...' : 'Save'}
								</button>
							{/if}
						</div>
						<textarea
							class="brain-file-content"
							bind:value={skillContent}
							readonly={!isAdmin}
						></textarea>
					</div>
				{/if}
			</div>

			{:else if brainTab === 'knowledge'}
			<div class="brain-section">
				<p class="brain-hint" style="margin-bottom: 0.75rem">Reference materials Brain can search when responding. Upload docs or add articles directly.</p>

				{#if isAdmin}
				<div class="knowledge-actions" style="margin-bottom: 0.75rem; display: flex; gap: 0.5rem;">
					<button class="btn btn-primary btn-sm" onclick={() => { showNewKnowledge = true; activeKnowledgeItem = null; knowledgeTitle = ''; knowledgeContent = ''; showUrlImport = false; }}>Add Article</button>
					<button class="btn btn-ghost btn-sm" onclick={() => knowledgeFileInput?.click()}>Upload File</button>
					<button class="btn btn-ghost btn-sm" onclick={() => { showUrlImport = true; showNewKnowledge = false; activeKnowledgeItem = null; urlPreview = null; importUrl = ''; }}>Import URL</button>
					<input type="file" accept=".txt,.md,.pdf" style="display:none" bind:this={knowledgeFileInput} onchange={handleUploadKnowledgeFile} />
				</div>
				{/if}

				{#if showNewKnowledge}
				<div class="brain-editor" style="margin-bottom: 0.75rem">
					<div class="brain-field">
						<label>Title</label>
						<input type="text" class="brain-input" bind:value={knowledgeTitle} placeholder="Article title" />
					</div>
					<div class="brain-field">
						<label>Content</label>
						<textarea class="brain-file-content" bind:value={knowledgeContent} placeholder="Article content (markdown supported)" rows="8"></textarea>
					</div>
					<div style="display: flex; gap: 0.5rem;">
						<button class="btn btn-primary btn-sm" onclick={handleCreateKnowledge} disabled={knowledgeSaving}>
							{knowledgeSaving ? 'Saving...' : 'Save'}
						</button>
						<button class="btn btn-ghost btn-sm" onclick={() => { showNewKnowledge = false; knowledgeTitle = ''; knowledgeContent = ''; }}>Cancel</button>
					</div>
				</div>
				{/if}

				{#if showUrlImport}
				<div class="brain-editor" style="margin-bottom: 0.75rem">
					<div class="brain-field">
						<label>URL</label>
						<div style="display: flex; gap: 0.5rem;">
							<input type="url" class="brain-input" bind:value={importUrl} placeholder="https://example.com/article" style="flex:1" />
							<button class="btn btn-primary btn-sm" onclick={handleFetchUrl} disabled={urlImporting}>
								{urlImporting ? 'Fetching...' : 'Fetch'}
							</button>
						</div>
					</div>
					{#if urlPreview}
						<div class="brain-field">
							<label>Title</label>
							<input type="text" class="brain-input" bind:value={urlPreview.title} />
						</div>
						<div class="brain-field">
							<label>Content Preview ({urlPreview.content.length} chars, ~{Math.round(urlPreview.content.length/4)} tokens)</label>
							<textarea class="brain-file-content" bind:value={urlPreview.content} rows="8"></textarea>
						</div>
						<div style="display: flex; gap: 0.5rem;">
							<button class="btn btn-primary btn-sm" onclick={handleSaveUrlImport} disabled={knowledgeSaving}>
								{knowledgeSaving ? 'Saving...' : 'Save as Knowledge'}
							</button>
							<button class="btn btn-ghost btn-sm" onclick={() => { showUrlImport = false; urlPreview = null; importUrl = ''; }}>Cancel</button>
						</div>
					{:else}
						<button class="btn btn-ghost btn-sm" onclick={() => { showUrlImport = false; }}>Cancel</button>
					{/if}
				</div>
				{/if}

				{#if activeKnowledgeItem && !showNewKnowledge && !showUrlImport}
				<div class="brain-editor" style="margin-bottom: 0.75rem">
					<div class="brain-editor-header">
						<span class="brain-file-name">{activeKnowledgeItem.title}</span>
						{#if isAdmin}
							<button class="btn btn-primary btn-sm" onclick={handleUpdateKnowledge} disabled={knowledgeSaving}>
								{knowledgeSaving ? 'Saving...' : 'Save'}
							</button>
						{/if}
					</div>
					<div class="brain-field">
						<label>Title</label>
						<input type="text" class="brain-input" bind:value={knowledgeTitle} readonly={!isAdmin} />
					</div>
					<textarea class="brain-file-content" bind:value={knowledgeContent} readonly={!isAdmin}></textarea>
					<button class="btn btn-ghost btn-sm" style="margin-top: 0.5rem;" onclick={() => { activeKnowledgeItem = null; knowledgeTitle = ''; knowledgeContent = ''; }}>Close</button>
				</div>
				{/if}

				<div class="knowledge-list">
					{#each knowledgeItems as item}
						<div class="knowledge-item">
							<button class="knowledge-select" onclick={() => selectKnowledgeItem(item)}>
								<span class="knowledge-title">{item.title}</span>
								<span class="knowledge-meta">
									<span class="knowledge-badge" data-type={item.source_type}>{item.source_type}</span>
									<span class="knowledge-tokens">{item.tokens} tokens</span>
								</span>
							</button>
							{#if isAdmin}
								<button class="memory-delete" onclick={() => handleDeleteKnowledge(item.id)} title="Delete">
									<svg width="12" height="12" viewBox="0 0 12 12" fill="none">
										<path d="M2 2L10 10M10 2L2 10" stroke="currentColor" stroke-width="1.5" stroke-linecap="round"/>
									</svg>
								</button>
							{/if}
						</div>
					{/each}
					{#if knowledgeItems.length === 0}
						<p class="brain-hint">No knowledge articles yet. Add articles or upload .txt/.md/.pdf files.</p>
					{/if}
				</div>
			</div>

			{:else if brainTab === 'integrations'}
			<div class="brain-section">
				<p class="brain-hint" style="margin-bottom: 1rem">Connect external systems to Brain via webhooks, email, or Telegram.</p>

				<!-- Webhooks Section -->
				<h3 class="brain-section-title">Webhooks</h3>
				<div class="brain-field">
					<label>Autonomy</label>
					<select class="brain-input" value={brainSettings.webhook_autonomy || 'autonomous'} onchange={(e) => handleBrainSettingChange('webhook_autonomy', e.currentTarget.value)}>
						<option value="autonomous">Autonomous — Brain responds automatically</option>
						<option value="draft">Draft — Brain responds in channel only</option>
						<option value="never">Never — Message saved, Brain silent</option>
					</select>
				</div>

				{#if isAdmin}
				<div style="display: flex; gap: 0.5rem; margin: 0.75rem 0; align-items: flex-end;">
					<div class="brain-field" style="flex: 1; margin: 0;">
						<label>Channel</label>
						<select class="brain-input" bind:value={newWebhookChannel}>
							<option value="">Select channel...</option>
							{#each channels as ch}
								<option value={ch.id}>{ch.name}</option>
							{/each}
						</select>
					</div>
					<div class="brain-field" style="flex: 1; margin: 0;">
						<label>Description</label>
						<input type="text" class="brain-input" bind:value={newWebhookDesc} placeholder="e.g. GitHub notifications" />
					</div>
					<button class="btn btn-primary btn-sm" onclick={handleCreateWebhook}>Create</button>
				</div>
				{/if}

				{#each webhooks as hook}
				<div class="knowledge-item" style="margin-bottom: 0.5rem;">
					<div style="flex: 1;">
						<div style="font-weight: 500;">{hook.description || 'Unnamed webhook'}</div>
						<div style="font-size: 0.75rem; color: var(--text-dim); margin-top: 0.25rem; font-family: monospace; word-break: break-all;">
							{hook.url}
						</div>
						<div style="font-size: 0.7rem; color: var(--text-dim); margin-top: 0.25rem;">
							Channel: {hook.channel_id} · Created: {new Date(hook.created_at).toLocaleDateString()}
							<button class="btn btn-ghost btn-xs" style="margin-left: 0.5rem;" onclick={() => loadEventsForHook(hook.id)}>Events</button>
						</div>
						{#if webhookEvents[hook.id]}
						<div style="margin-top: 0.5rem; font-size: 0.75rem;">
							{#each webhookEvents[hook.id].slice(0, 10) as evt}
							<div style="display: flex; gap: 0.5rem; padding: 0.15rem 0; border-bottom: 1px solid var(--border);">
								<span class="knowledge-badge" data-type={evt.status}>{evt.status}</span>
								<span style="color: var(--text-dim);">{new Date(evt.created_at).toLocaleString()}</span>
								<span style="flex:1; overflow:hidden; text-overflow:ellipsis; white-space:nowrap;">{evt.payload.substring(0, 80)}</span>
							</div>
							{/each}
							{#if webhookEvents[hook.id].length === 0}
								<span style="color: var(--text-dim);">No events yet</span>
							{/if}
						</div>
						{/if}
					</div>
					{#if isAdmin}
					<button class="memory-delete" onclick={() => handleDeleteWebhook(hook.id)} title="Delete">
						<svg width="12" height="12" viewBox="0 0 12 12" fill="none">
							<path d="M2 2L10 10M10 2L2 10" stroke="currentColor" stroke-width="1.5" stroke-linecap="round"/>
						</svg>
					</button>
					{/if}
				</div>
				{/each}
				{#if webhooks.length === 0}
					<p class="brain-hint">No webhooks yet. Create one to receive external events.</p>
				{/if}

				<!-- Email Section -->
				<h3 class="brain-section-title" style="margin-top: 1.5rem;">Email</h3>
				<div class="brain-field">
					<label>
						<input type="checkbox" checked={brainSettings.email_enabled === 'true'} onchange={(e) => handleBrainSettingChange('email_enabled', e.currentTarget.checked ? 'true' : 'false')} />
						Enable inbound email
					</label>
				</div>
				{#if brainSettings.email_enabled === 'true'}
				<div class="brain-field">
					<label>Inbound address</label>
					<input type="text" class="brain-input" value="brain-{slug}@your-domain:2525" readonly style="font-family: monospace; color: var(--text-dim);" />
				</div>
				<div class="brain-field">
					<label>Autonomy</label>
					<select class="brain-input" value={brainSettings.email_autonomy || 'draft'} onchange={(e) => handleBrainSettingChange('email_autonomy', e.currentTarget.value)}>
						<option value="autonomous">Autonomous — Brain auto-replies via email</option>
						<option value="draft">Draft — Brain responds in channel only</option>
						<option value="never">Never — Message saved, Brain silent</option>
					</select>
				</div>
				<div class="brain-field">
					<label>Reply scope</label>
					<select class="brain-input" value={brainSettings.email_reply_scope || 'anyone'} onchange={(e) => handleBrainSettingChange('email_reply_scope', e.currentTarget.value)}>
						<option value="anyone">Anyone</option>
						<option value="contacts">Known contacts only</option>
						<option value="internal">Internal workspace members only</option>
					</select>
				</div>

				<details style="margin-top: 0.5rem;">
					<summary style="cursor: pointer; font-size: 0.85rem; color: var(--text-dim);">Outbound SMTP settings</summary>
					<div style="margin-top: 0.5rem;">
						<div class="brain-field">
							<label>SMTP Host</label>
							<input type="text" class="brain-input" value={brainSettings.email_outbound_host || ''} onchange={(e) => handleBrainSettingChange('email_outbound_host', e.currentTarget.value)} placeholder="smtp.gmail.com" />
						</div>
						<div class="brain-field">
							<label>Port</label>
							<input type="text" class="brain-input" value={brainSettings.email_outbound_port || '587'} onchange={(e) => handleBrainSettingChange('email_outbound_port', e.currentTarget.value)} />
						</div>
						<div class="brain-field">
							<label>Username</label>
							<input type="text" class="brain-input" value={brainSettings.email_outbound_user || ''} onchange={(e) => handleBrainSettingChange('email_outbound_user', e.currentTarget.value)} />
						</div>
						<div class="brain-field">
							<label>Password</label>
							<input type="password" class="brain-input" value={brainSettings.email_outbound_pass || ''} onchange={(e) => handleBrainSettingChange('email_outbound_pass', e.currentTarget.value)} />
						</div>
					</div>
				</details>

				{#if emailThreads.length > 0}
				<h4 style="margin-top: 1rem; font-size: 0.85rem; color: var(--text-dim);">Email Threads</h4>
				{#each emailThreads as thread}
				<div class="knowledge-item">
					<div style="flex: 1;">
						<div style="font-weight: 500;">{thread.subject || 'No subject'}</div>
						<div style="font-size: 0.7rem; color: var(--text-dim);">Last reply: {new Date(thread.last_reply_at).toLocaleString()}</div>
					</div>
					{#if isAdmin}
					<button class="memory-delete" onclick={() => handleDeleteEmailThread(thread.id)} title="Delete">
						<svg width="12" height="12" viewBox="0 0 12 12" fill="none">
							<path d="M2 2L10 10M10 2L2 10" stroke="currentColor" stroke-width="1.5" stroke-linecap="round"/>
						</svg>
					</button>
					{/if}
				</div>
				{/each}
				{/if}
				{/if}

				<!-- Telegram Section -->
				<h3 class="brain-section-title" style="margin-top: 1.5rem;">Telegram</h3>
				<div class="brain-field">
					<label>Bot Token</label>
					<input type="password" class="brain-input" value={brainSettings.telegram_bot_token || ''} onchange={(e) => handleBrainSettingChange('telegram_bot_token', e.currentTarget.value)} placeholder="123456:ABC-DEF..." />
					<span style="font-size: 0.7rem; color: var(--text-dim);">Saving auto-registers the Telegram webhook</span>
				</div>
				{#if brainSettings.telegram_bot_token}
				<div class="brain-field">
					<label>Autonomy</label>
					<select class="brain-input" value={brainSettings.telegram_autonomy || 'autonomous'} onchange={(e) => handleBrainSettingChange('telegram_autonomy', e.currentTarget.value)}>
						<option value="autonomous">Autonomous — Brain replies in Telegram</option>
						<option value="draft">Draft — Brain responds in channel only</option>
						<option value="never">Never — Message saved, Brain silent</option>
					</select>
				</div>

				{#if telegramChats.length > 0}
				<h4 style="margin-top: 0.75rem; font-size: 0.85rem; color: var(--text-dim);">Linked Chats</h4>
				{#each telegramChats as chat}
				<div class="knowledge-item">
					<div style="flex: 1;">
						<div style="font-weight: 500;">{chat.label || 'Chat ' + chat.chat_id}</div>
						<div style="font-size: 0.7rem; color: var(--text-dim);">Chat ID: {chat.chat_id}</div>
					</div>
					{#if isAdmin}
					<button class="memory-delete" onclick={() => handleDeleteTelegramChat(chat.id)} title="Unlink">
						<svg width="12" height="12" viewBox="0 0 12 12" fill="none">
							<path d="M2 2L10 10M10 2L2 10" stroke="currentColor" stroke-width="1.5" stroke-linecap="round"/>
						</svg>
					</button>
					{/if}
				</div>
				{/each}
				{/if}
				{/if}
			</div>

			{/if}
		</div>
	</main>

	{:else if activeView === 'team'}
	<main class="main-content">
		<div class="team-view">
			{#if teamTab === 'members'}
			<div class="agents-toolbar">
				{#if isAdmin}
					<button class="btn btn-primary" onclick={handleAddOrgRole}>Create Role</button>
				{/if}
			</div>
			<div class="agents-grid">
				{#each $members.filter(m => m.role !== 'agent' && m.role !== 'brain') as member}
					<div class="agent-card">
						<div class="agent-card-header">
							<span class="agent-avatar member-avatar-circle">{member.display_name?.charAt(0)?.toUpperCase() || '?'}</span>
							<div class="agent-card-name-row">
								<span class="agent-name">{member.display_name}</span>
								<span class="member-online-dot" class:online={member.online}></span>
							</div>
						</div>
						<div class="agent-card-role">{member.role}</div>
						{#if member.title}<div class="agent-card-goal">{member.title}</div>{/if}
						{#if member.goals}<div class="agent-card-goal">{member.goals}</div>{/if}
						{#if isAdmin}
							<div class="agent-card-actions">
								<button class="btn btn-ghost btn-xs" onclick={() => { selectedMember = member; }}>Manage</button>
							</div>
						{/if}
					</div>
				{/each}
				{#if $members.filter(m => m.role !== 'agent' && m.role !== 'brain').length === 0}
					<div class="empty-state">No members yet.</div>
				{/if}
			</div>

			{:else if teamTab === 'agents'}
			<div class="team-agents">
				{#if !showAgentForm && !showTemplateGallery}
					<div class="agents-toolbar">
						{#if isAdmin}
							<button class="btn btn-primary" onclick={openNewAgent}>Create Agent</button>
							<button class="btn btn-ghost" onclick={() => { loadTemplates(); showTemplateGallery = true; }}>From Template</button>
							<button class="btn btn-ghost" onclick={handleGenerateAgent} disabled={agentGenerating}>
								{agentGenerating ? 'Generating...' : 'Generate with AI'}
							</button>
						{/if}
					</div>

					<div class="agents-grid">
						{#each agentsList as agent}
							<div class="agent-card" class:inactive={!agent.is_active} class:system={agent.is_system}>
								<div class="agent-card-header">
									<span class="agent-avatar">{agent.avatar || '🤖'}</span>
									<div class="agent-card-name-row">
										<span class="agent-name">{agent.name}</span>
										{#if agent.is_system}<span class="agent-badge system">System</span>{/if}
										{#if !agent.is_active}<span class="agent-badge paused">Paused</span>{/if}
										{#if agent.trigger_type === 'all'}<span class="agent-badge trigger-all">@all</span>{/if}
										{#if agent.trigger_type === 'always'}<span class="agent-badge trigger-always">Always</span>{/if}
									</div>
								</div>
								<div class="agent-card-role">{agent.role}</div>
								{#if agent.goal}<div class="agent-card-goal">{agent.goal}</div>{/if}
								<div class="agent-card-tools">
									{#each JSON.parse(typeof agent.tools === 'string' ? agent.tools : JSON.stringify(agent.tools || [])) as tool}
										<span class="tool-chip">{tool}</span>
									{/each}
								</div>
								{#if isAdmin && !agent.is_system}
									<div class="agent-card-actions">
										<button class="btn btn-ghost btn-xs" onclick={() => openEditAgent(agent)}>Edit</button>
										<button class="btn btn-ghost btn-xs" onclick={() => handleToggleAgent(agent)}>
											{agent.is_active ? 'Pause' : 'Activate'}
										</button>
										<button class="btn btn-ghost btn-xs btn-danger" onclick={() => handleDeleteAgent(agent.id)}>Delete</button>
									</div>
								{/if}
							</div>
						{/each}
						{#if agentsList.length === 0}
							<div class="empty-state">No agents yet. Create one or use a template.</div>
						{/if}
					</div>

				{:else if showTemplateGallery}
					<div class="template-gallery">
						<div class="template-header">
							<h3>Agent Templates</h3>
							<button class="btn btn-ghost" onclick={() => showTemplateGallery = false}>Back</button>
						</div>
						<div class="agents-grid">
							{#each agentTemplates as tmpl}
								<div class="agent-card template-card">
									<div class="agent-card-header">
										<span class="agent-avatar">{tmpl.avatar}</span>
										<span class="agent-name">{tmpl.name}</span>
									</div>
									<div class="agent-card-role">{tmpl.role}</div>
									<div class="agent-card-goal">{tmpl.description}</div>
									<div class="agent-card-tools">
										{#each tmpl.tools as tool}
											<span class="tool-chip">{tool}</span>
										{/each}
									</div>
									<button class="btn btn-primary btn-sm" onclick={() => handleCreateFromTemplate(tmpl.id)}>Use Template</button>
								</div>
							{/each}
						</div>
					</div>

				{:else if showAgentForm}
					<div class="agent-form">
						<div class="agent-form-header">
							<h3>{editingAgent ? `Edit ${editingAgent.name}` : 'Create Agent'}</h3>
							<button class="btn btn-ghost" onclick={() => { showAgentForm = false; editingAgent = null; }}>Cancel</button>
						</div>

						<div class="form-section">
							<h4>Identity</h4>
							<label class="form-field">
								<span>Name</span>
								<input type="text" bind:value={agentForm.name} placeholder="Sales Assistant" />
							</label>
							<label class="form-field">
								<span>Description</span>
								<input type="text" bind:value={agentForm.description} placeholder="What does this agent do?" />
							</label>
							<label class="form-field">
								<span>Avatar (emoji)</span>
								<input type="text" bind:value={agentForm.avatar} placeholder="🤖" maxlength="4" style="width:80px" />
							</label>
						</div>

						<div class="form-section">
							<h4>Personality</h4>
							<label class="form-field">
								<span>Role</span>
								<input type="text" bind:value={agentForm.role} placeholder="Customer Support Lead" />
							</label>
							<label class="form-field">
								<span>Goal</span>
								<input type="text" bind:value={agentForm.goal} placeholder="Resolve issues fast" />
							</label>
							<label class="form-field">
								<span>Backstory</span>
								<textarea bind:value={agentForm.backstory} rows="3" placeholder="Background and expertise..."></textarea>
							</label>
							<label class="form-field">
								<span>Instructions</span>
								<textarea bind:value={agentForm.instructions} rows="5" placeholder="How should this agent behave?"></textarea>
							</label>
							<label class="form-field">
								<span>Constraints</span>
								<textarea bind:value={agentForm.constraints} rows="3" placeholder="Things this agent should NOT do"></textarea>
							</label>
						</div>

						<div class="form-section">
							<h4>Capabilities</h4>
							<label class="form-field">
								<span>Model (empty = workspace default)</span>
								<input type="text" bind:value={agentForm.model} placeholder="anthropic/claude-sonnet-4" />
							</label>
							<label class="form-field">
								<span>Temperature: {agentForm.temperature}</span>
								<input type="range" min="0" max="2" step="0.1" bind:value={agentForm.temperature} />
							</label>
							<div class="form-field">
								<span>Tools</span>
								<div class="tools-checkboxes">
									{#each AGENT_TOOLS as tool}
										<label class="checkbox-label">
											<input type="checkbox" checked={agentForm.tools.includes(tool)} onchange={() => toggleTool(tool)} />
											{tool}
										</label>
									{/each}
								</div>
							</div>
							<div class="form-toggles">
								<label class="toggle-label">
									<input type="checkbox" bind:checked={agentForm.knowledge_access} /> Knowledge Access
								</label>
								<label class="toggle-label">
									<input type="checkbox" bind:checked={agentForm.memory_access} /> Memory Access
								</label>
								<label class="toggle-label">
									<input type="checkbox" bind:checked={agentForm.can_delegate} /> Can Delegate
								</label>
							</div>
							<div class="form-group" style="margin-top:8px">
								<label>Response Mode</label>
								<select bind:value={agentForm.trigger_type}>
									<option value="mention">@name only (default)</option>
									<option value="all">@name + @all</option>
									<option value="always">Always respond</option>
								</select>
							</div>
						</div>

						{#if editingAgent && !editingAgent.is_system}
						<div class="form-section">
							<h4>Skills</h4>
							{#if showSkillEditor}
								<div class="skill-editor">
									<div class="skill-editor-header">
										<span class="text-sm">{editingSkillFile}</span>
										<div>
											<button class="btn btn-sm btn-primary" onclick={() => handleSaveAgentSkill(editingAgent.id)}>Save</button>
											<button class="btn btn-sm btn-ghost" onclick={() => showSkillEditor = false}>Cancel</button>
										</div>
									</div>
									<textarea class="skill-textarea" bind:value={skillEditorContent} rows="12" placeholder="Skill markdown content..."></textarea>
								</div>
							{:else}
								<div class="skill-list">
									{#each agentSkillsList as skill}
										<div class="skill-item">
											<div>
												<strong>{skill.name}</strong>
												<span class="text-muted text-sm"> ({skill.trigger})</span>
												<div class="text-muted text-sm">{skill.description}</div>
											</div>
											<div class="skill-item-actions">
												<button class="btn btn-ghost btn-sm" onclick={async () => {
													const data = await getAgentSkill(slug, editingAgent.id, skill.file_name);
													editingSkillFile = skill.file_name;
													skillEditorContent = data.content;
													showSkillEditor = true;
												}}>Edit</button>
												<button class="btn btn-ghost btn-sm" onclick={() => handleDeleteAgentSkill(editingAgent.id, skill.file_name)}>x</button>
											</div>
										</div>
									{/each}
									{#if agentSkillsList.length === 0}
										<p class="text-muted text-sm">No skills yet.</p>
									{/if}
								</div>
								<button class="btn btn-sm" onclick={() => handleNewAgentSkill(editingAgent.id)}>+ Add Skill</button>
							{/if}
						</div>
						{/if}

						<div class="form-section">
							<h4>Guardrails</h4>
							<label class="form-field">
								<span>Max Iterations</span>
								<input type="number" bind:value={agentForm.max_iterations} min="1" max="20" style="width:80px" />
							</label>
							<label class="form-field">
								<span>Escalation Prompt</span>
								<textarea bind:value={agentForm.escalation_prompt} rows="2" placeholder="When to hand off to Brain..."></textarea>
							</label>
						</div>

						<div class="form-actions">
							<button class="btn btn-primary" onclick={handleSaveAgent} disabled={agentSaving}>
								{agentSaving ? 'Saving...' : (editingAgent ? 'Update Agent' : 'Create Agent')}
							</button>
							{#if editingAgent && !editingAgent.is_system}
								<button class="btn btn-danger" onclick={() => handleDeleteAgent(editingAgent.id)}>Delete</button>
							{/if}
						</div>
					</div>
				{/if}
			</div>

			{:else if teamTab === 'orgchart'}
			<div class="org-chart">
				<div class="agents-toolbar">
					{#if isAdmin}
						<button class="btn btn-primary" onclick={handleAddOrgRole}>Create Role</button>
					{/if}
					<button class="btn btn-ghost" onclick={() => chartFit?.()}>Zoom to Fit</button>
					<button class="btn btn-ghost" onclick={() => { if (chartExpanded) { chartCollapseAll?.(); } else { chartExpandAll?.(); } chartExpanded = !chartExpanded; }}>
						{chartExpanded ? 'Collapse All' : 'Expand All'}
					</button>
					<button class="btn btn-ghost" onclick={() => loadOrgChart()}>Refresh</button>
				</div>

				{#if orgChartNodes.length === 0}
					<div class="empty-state">Loading org chart...</div>
				{:else}
					<OrgChart
						nodes={orgChartNodes}
						{isAdmin}
						onReparent={handleOrgReparent}
						onNodeClick={handleOrgNodeClick}
						bind:onFit={chartFit}
						bind:onExpandAll={chartExpandAll}
						bind:onCollapseAll={chartCollapseAll}
					/>
				{/if}

				{#if selectedNodeForPanel}
				<div class="org-node-panel">
					<div class="panel-header">
						<div>
							<h4>{selectedNodeForPanel.name}</h4>
							<span class="panel-type-badge" style="text-transform:capitalize">{selectedNodeForPanel.type.replace('_', ' ')}</span>
						</div>
						<button class="role-dialog-close" onclick={() => selectedNodeForPanel = null}>&times;</button>
					</div>
					<div class="panel-body">
						{#if selectedNodeForPanel.reports_to}
							<div class="panel-meta">
								<span class="text-muted text-sm">Reports to:</span>
								<span class="text-sm">{orgChartNodes.find(n => n.id === selectedNodeForPanel.reports_to)?.name || selectedNodeForPanel.reports_to}</span>
							</div>
						{/if}

						<!-- Stats section for all non-role nodes -->
						{#if selectedNodeForPanel.type !== 'role_slot'}
							<div class="panel-stats">
								<div class="stat-item">
									<span class="stat-value">{selectedNodeForPanel.message_count || 0}</span>
									<span class="stat-label">Messages</span>
								</div>
								<div class="stat-item">
									<span class="stat-value">{selectedNodeForPanel.task_count || 0}</span>
									<span class="stat-label">Tasks</span>
								</div>
								{#if selectedNodeForPanel.last_active}
									<div class="stat-item">
										<span class="stat-value">{formatLastActive(selectedNodeForPanel.last_active)}</span>
										<span class="stat-label">Last Active</span>
									</div>
								{/if}
							</div>
						{/if}

						<!-- Human member panel -->
						{#if selectedNodeForPanel.type === 'human' && isAdmin}
							<div class="panel-section">
								<label class="panel-field">
									<span class="text-muted text-sm">Title</span>
									<input class="panel-input" type="text" value={selectedNodeForPanel.title || ''}
										onblur={(e) => handleUpdateProfile(selectedNodeForPanel.id, 'title', e.target.value)}
										placeholder="e.g. Marketing Lead" />
								</label>
								<label class="panel-field">
									<span class="text-muted text-sm">Bio</span>
									<textarea class="panel-input" rows="2"
										onblur={(e) => handleUpdateProfile(selectedNodeForPanel.id, 'bio', e.target.value)}
										placeholder="Short bio...">{selectedNodeForPanel.bio || ''}</textarea>
								</label>
								<label class="panel-field">
									<span class="text-muted text-sm">Goals</span>
									<textarea class="panel-input" rows="2"
										onblur={(e) => handleUpdateProfile(selectedNodeForPanel.id, 'goals', e.target.value)}
										placeholder="Current goals...">{selectedNodeForPanel.goals || ''}</textarea>
								</label>
							</div>
							<div class="panel-actions">
								<label class="text-muted text-sm">Change Role:</label>
								<select class="panel-select" value={selectedNodeForPanel.role} onchange={(e) => {
									updateMemberRole(slug, selectedNodeForPanel.id, e.target.value);
									selectedNodeForPanel = { ...selectedNodeForPanel, role: e.target.value };
								}}>
									{#each ROLES as r}
										<option value={r}>{r}</option>
									{/each}
								</select>
								<button class="btn btn-danger btn-sm" style="margin-top:8px" onclick={() => { if (confirm('Remove this member?')) { kickMember(slug, selectedNodeForPanel.id); selectedNodeForPanel = null; loadOrgChart(); } }}>Remove from Workspace</button>
							</div>

						<!-- Agent panel -->
						{:else if selectedNodeForPanel.type === 'agent'}
							<div class="panel-section">
								{#if selectedNodeForPanel.description}
									<div class="panel-field">
										<span class="text-muted text-sm">Description</span>
										<p class="text-sm">{selectedNodeForPanel.description}</p>
									</div>
								{/if}
								{#if selectedNodeForPanel.goal}
									<div class="panel-field">
										<span class="text-muted text-sm">Goal</span>
										<p class="text-sm">{selectedNodeForPanel.goal}</p>
									</div>
								{/if}
								{#if selectedNodeForPanel.role}
									<div class="panel-field">
										<span class="text-muted text-sm">Role</span>
										<p class="text-sm">{selectedNodeForPanel.role}</p>
									</div>
								{/if}
							</div>
							{#if isAdmin && !selectedNodeForPanel.is_system}
								<div class="panel-actions">
									<button class="btn btn-sm" onclick={() => { const agent = agentsList.find(a => a.id === selectedNodeForPanel.id); if (agent) { openEditAgent(agent); teamTab = 'agents'; selectedNodeForPanel = null; } }}>Edit Agent</button>
									<button class="btn btn-sm" onclick={() => handleToggleActive(selectedNodeForPanel)}>
										{selectedNodeForPanel.is_active ? 'Pause' : 'Activate'}
									</button>
									<button class="btn btn-danger btn-sm" onclick={() => { handleDeleteAgent(selectedNodeForPanel.id); selectedNodeForPanel = null; loadOrgChart(); }}>Delete</button>
								</div>
							{/if}

						<!-- Vacant role panel -->
						{:else if selectedNodeForPanel.type === 'role_slot' && isAdmin}
							{#if selectedNodeForPanel.role}
								<div class="panel-field">
									<span class="text-muted text-sm">Description</span>
									<p class="text-sm">{selectedNodeForPanel.role}</p>
								</div>
							{/if}
							<div class="panel-actions">
								<label class="text-muted text-sm">Assign to member:</label>
								<select class="panel-select" onchange={(e) => { handleFillRole(selectedNodeForPanel.id, e.target.value, 'human'); e.target.value = ''; }}>
									<option value="">Select member...</option>
									{#each $members.filter(m => m.role !== 'agent' && m.role !== 'brain') as m}
										<option value={m.id}>{m.display_name}</option>
									{/each}
								</select>
								<label class="text-muted text-sm" style="margin-top:6px">Or assign agent:</label>
								<select class="panel-select" onchange={(e) => { handleFillRole(selectedNodeForPanel.id, e.target.value, 'agent'); e.target.value = ''; }}>
									<option value="">Select agent...</option>
									{#each agentsList.filter(a => !a.is_system) as a}
										<option value={a.id}>{a.name}</option>
									{/each}
								</select>
								<button class="btn btn-sm btn-primary" style="margin-top:8px" onclick={() => handleCreateAgentForRole(selectedNodeForPanel.id, selectedNodeForPanel.name, selectedNodeForPanel.role)}>Create Agent for Role</button>
								<button class="btn btn-danger btn-sm" style="margin-top:4px" onclick={() => handleDeleteOrgRoleAction(selectedNodeForPanel.id)}>Delete Role</button>
							</div>
						{/if}
					</div>
				</div>
				{/if}
			</div>
			{/if}
		</div>
	</main>
	{/if}
</div>

{#if showRoleDialog}
<div class="role-dialog-overlay" onclick={() => showRoleDialog = false}>
	<div class="role-dialog" onclick={(e) => e.stopPropagation()}>
		<div class="role-dialog-header">
			<h3>Create Role</h3>
			<button class="role-dialog-close" onclick={() => showRoleDialog = false}>&times;</button>
		</div>
		<div class="role-dialog-form">
			<div class="form-group">
				<label>Title <span class="required">*</span></label>
				<input type="text" bind:value={roleForm.title} placeholder="Marketing Manager" />
			</div>
			<div class="form-group">
				<label>Department</label>
				<input type="text" bind:value={roleForm.department} placeholder="Marketing" />
			</div>
			<div class="form-group">
				<label>Description</label>
				<textarea bind:value={roleForm.description} rows="3" placeholder="Responsibilities, authority, scope..."></textarea>
			</div>
			<div class="form-group">
				<label>Reports To</label>
				<select bind:value={roleForm.reports_to}>
					<option value="">Brain (default)</option>
					{#each $members.filter(m => m.role !== 'agent' && m.role !== 'brain') as member}
						<option value={member.id}>{member.display_name}</option>
					{/each}
					{#each agentsList.filter(a => !a.is_system) as agent}
						<option value={agent.id}>{agent.name} (Agent)</option>
					{/each}
				</select>
			</div>
		</div>
		<div class="role-dialog-actions">
			<button class="btn btn-ghost" onclick={() => showRoleDialog = false}>Cancel</button>
			<button class="btn btn-primary" onclick={handleCreateRole} disabled={roleSaving}>
				{roleSaving ? 'Creating...' : 'Create'}
			</button>
		</div>
	</div>
</div>
{/if}

{#if showModelBrowser}
<div class="modal-overlay" onclick={() => showModelBrowser = false}>
	<div class="modal-content model-browser" onclick={(e) => e.stopPropagation()}>
		<div class="modal-header">
			<h3>Browse Models</h3>
			<button class="modal-close" onclick={() => showModelBrowser = false}>&times;</button>
		</div>
		<div class="model-browser-controls">
			<input type="text" class="brain-input" bind:value={modelSearchQuery} placeholder="Search models..." style="flex:1" />
			<div class="model-filters">
				<button class="btn btn-ghost btn-xs" class:active={modelFilter === ''} onclick={() => modelFilter = ''}>All</button>
				<button class="btn btn-ghost btn-xs" class:active={modelFilter === 'free'} onclick={() => modelFilter = 'free'}>Free</button>
				<button class="btn btn-ghost btn-xs" class:active={modelFilter === 'new'} onclick={() => modelFilter = 'new'}>New</button>
			</div>
		</div>
		{#if modelBrowserLoading}
			<p class="brain-hint">Loading models...</p>
		{:else}
			<div class="model-browser-list">
				{#each filteredBrowseModels() as model}
					<div class="model-browser-item">
						<div class="model-browser-info">
							<span class="model-browser-name">{model.name || model.id}</span>
							<span class="model-browser-meta">
								<span class="model-browser-provider">{model.provider}</span>
								{#if model.context_length}
									<span>{(model.context_length / 1000).toFixed(0)}K ctx</span>
								{/if}
								{#if model.is_free}
									<span class="model-badge free">Free</span>
								{/if}
								{#if model.is_new}
									<span class="model-badge new">New</span>
								{/if}
							</span>
						</div>
						<button class="btn btn-ghost btn-xs" onclick={() => { brainModel = model.id; showModelBrowser = false; }}>
							Select
						</button>
					</div>
				{/each}
			</div>
		{/if}
	</div>
</div>
{/if}

{#if showAgentLibrary}
<!-- svelte-ignore a11y_no_static_element_interactions -->
<div class="modal-overlay" onclick={() => showAgentLibrary = false}>
	<!-- svelte-ignore a11y_no_static_element_interactions -->
	<div class="modal-dialog agent-library-modal" onclick={(e) => e.stopPropagation()}>
		<div class="agent-lib-header">
			<div>
				<h3>Agent Library</h3>
				<p class="agent-lib-subtitle">Browse, create, and manage your AI agents</p>
			</div>
			<div class="agent-lib-header-actions">
				{#if isAdmin}
					<button class="btn btn-primary btn-sm" onclick={() => { showAgentLibrary = false; activeView = 'team'; onViewChange(); teamTab = 'agents'; openNewAgent(); }}>+ Create Agent</button>
				{/if}
				<button class="panel-close" onclick={() => showAgentLibrary = false}>&times;</button>
			</div>
		</div>

		<div class="agent-lib-search">
			<svg width="14" height="14" viewBox="0 0 14 14" fill="none">
				<circle cx="6" cy="6" r="4.5" stroke="currentColor" stroke-width="1.2"/>
				<path d="M9.5 9.5L12.5 12.5" stroke="currentColor" stroke-width="1.2" stroke-linecap="round"/>
			</svg>
			<input type="text" placeholder="Search agents..." bind:value={agentLibSearch} />
		</div>

		<div class="agent-lib-tabs">
			{#each agentLibCategories as cat}
				<button
					class="agent-lib-tab"
					class:active={agentLibFilter === cat.id}
					onclick={() => agentLibFilter = cat.id}
				>{cat.label}</button>
			{/each}
		</div>

		<div class="agent-lib-body">
			{#if filteredBuiltinAgents.length > 0}
				<div class="agent-lib-section">
					<h4>Built-in Agents</h4>
					<p class="agent-lib-section-desc">Official agents with specialized capabilities</p>
					<div class="agent-lib-grid">
						{#each filteredBuiltinAgents as agent}
							<div class="agent-lib-card" style="--card-accent: var(--accent)">
								<div class="agent-lib-card-top-bar builtin"></div>
								<div class="agent-lib-card-header">
									<span class="agent-lib-card-avatar">{agent.avatar || '🤖'}</span>
									<div>
										<div class="agent-lib-card-name">{agent.name}</div>
										<div class="agent-lib-card-desc">{agent.description || agent.role || ''}</div>
									</div>
								</div>
								<div class="agent-lib-card-tools">
									{#each JSON.parse(typeof agent.tools === 'string' ? agent.tools : JSON.stringify(agent.tools || [])).slice(0, 4) as tool}
										<span class="tool-chip">{tool}</span>
									{/each}
								</div>
								<div class="agent-lib-card-footer">
									<span class="agent-lib-badge builtin">Built-in</span>
									<div class="agent-lib-card-actions">
										<button class="btn btn-ghost btn-xs" onclick={() => agentLibChat(agent)}>Chat</button>
										{#if isAdmin}
											<button class="btn btn-ghost btn-xs" onclick={() => agentLibEdit(agent)}>Edit</button>
										{/if}
									</div>
								</div>
							</div>
						{/each}
					</div>
				</div>
			{/if}

			{#if filteredUserAgents.length > 0}
				<div class="agent-lib-section">
					<h4>Your Agents</h4>
					<p class="agent-lib-section-desc">Custom agents you've created</p>
					<div class="agent-lib-grid">
						{#each filteredUserAgents as agent}
							<div class="agent-lib-card" style="--card-accent: var(--blue, #3b82f6)">
								<div class="agent-lib-card-top-bar user"></div>
								<div class="agent-lib-card-header">
									<span class="agent-lib-card-avatar">{agent.avatar || '🤖'}</span>
									<div>
										<div class="agent-lib-card-name">{agent.name}</div>
										<div class="agent-lib-card-desc">{agent.description || agent.role || ''}</div>
									</div>
								</div>
								<div class="agent-lib-card-tools">
									{#each JSON.parse(typeof agent.tools === 'string' ? agent.tools : JSON.stringify(agent.tools || [])).slice(0, 4) as tool}
										<span class="tool-chip">{tool}</span>
									{/each}
								</div>
								<div class="agent-lib-card-footer">
									<span class="agent-lib-badge custom">Custom</span>
									<div class="agent-lib-card-actions">
										<button class="btn btn-ghost btn-xs" onclick={() => agentLibChat(agent)}>Chat</button>
										{#if isAdmin}
											<button class="btn btn-ghost btn-xs" onclick={() => agentLibEdit(agent)}>Edit</button>
											<button class="btn btn-ghost btn-xs btn-danger" onclick={() => agentLibDelete(agent.id)}>Delete</button>
										{/if}
									</div>
								</div>
							</div>
						{/each}
					</div>
				</div>
			{/if}

			{#if filteredTemplates.length > 0}
				<div class="agent-lib-section">
					<h4>Community Templates</h4>
					<p class="agent-lib-section-desc">Ready-to-use agent configurations</p>
					<div class="agent-lib-grid">
						{#each filteredTemplates as tmpl}
							<div class="agent-lib-card" style="--card-accent: var(--purple, #a855f7)">
								<div class="agent-lib-card-top-bar template"></div>
								<div class="agent-lib-card-header">
									<span class="agent-lib-card-avatar">{tmpl.avatar || '📋'}</span>
									<div>
										<div class="agent-lib-card-name">{tmpl.name}</div>
										<div class="agent-lib-card-desc">{tmpl.description || tmpl.role || ''}</div>
									</div>
								</div>
								<div class="agent-lib-card-tools">
									{#each (tmpl.tools || []).slice(0, 4) as tool}
										<span class="tool-chip">{tool}</span>
									{/each}
								</div>
								<div class="agent-lib-card-footer">
									<span class="agent-lib-badge template">Template</span>
									<div class="agent-lib-card-actions">
										<button class="btn btn-primary btn-xs" onclick={() => agentLibUseTemplate(tmpl.id)}>Use Template</button>
									</div>
								</div>
							</div>
						{/each}
					</div>
				</div>
			{/if}

			{#if filteredBuiltinAgents.length === 0 && filteredUserAgents.length === 0 && filteredTemplates.length === 0}
				<div class="empty-state" style="padding: 48px 0; text-align: center;">
					<div style="font-size: 2rem; margin-bottom: 8px;">🤖</div>
					<p>No agents match your search.</p>
				</div>
			{/if}
		</div>
	</div>
</div>
{/if}

{#if showPreferences}
<!-- svelte-ignore a11y_no_static_element_interactions -->
<div class="modal-overlay" onclick={() => showPreferences = false}>
	<!-- svelte-ignore a11y_no_static_element_interactions -->
	<div class="modal-dialog" onclick={(e) => e.stopPropagation()} style="max-width: 480px">
		<div class="modal-header">
			<h3>Preferences</h3>
			<button class="panel-close" onclick={() => showPreferences = false}>&times;</button>
		</div>
		<div class="brain-tabs" style="margin-bottom: 1rem">
			<button class="brain-tab" class:active={prefsTab === 'profile'} onclick={() => prefsTab = 'profile'}>Profile</button>
			<button class="brain-tab" class:active={prefsTab === 'security'} onclick={() => prefsTab = 'security'}>Security</button>
			<button class="brain-tab" class:active={prefsTab === 'appearance'} onclick={() => prefsTab = 'appearance'}>Appearance</button>
		</div>

		{#if prefsMsg}
			<div class="brain-hint" style="margin-bottom: 0.75rem; color: var(--accent)">{prefsMsg}</div>
		{/if}

		{#if prefsTab === 'profile'}
			<div class="brain-field">
				<label>Display Name</label>
				<input class="brain-input" type="text" bind:value={prefsDisplayName} />
			</div>
			<div class="brain-field">
				<label>Email</label>
				<input class="brain-input" type="email" bind:value={prefsEmail} />
			</div>
			<button class="btn btn-primary" onclick={handleSaveProfile} disabled={prefsLoading}>
				{prefsLoading ? 'Saving...' : 'Save'}
			</button>
		{:else if prefsTab === 'security'}
			<div class="brain-field">
				<label>Current Password</label>
				<input class="brain-input" type="password" bind:value={prefsCurrentPw} />
			</div>
			<div class="brain-field">
				<label>New Password</label>
				<input class="brain-input" type="password" bind:value={prefsNewPw} />
			</div>
			<div class="brain-field">
				<label>Confirm New Password</label>
				<input class="brain-input" type="password" bind:value={prefsConfirmPw} />
			</div>
			<button class="btn btn-primary" onclick={handleChangePassword} disabled={prefsLoading}>
				{prefsLoading ? 'Changing...' : 'Change Password'}
			</button>
		{:else if prefsTab === 'appearance'}
			<div class="brain-section">
				<p class="brain-hint">Theme settings coming soon.</p>
			</div>
		{/if}
	</div>
</div>
{/if}

<style>
	.workspace {
		display: flex;
		height: 100vh;
		background: var(--bg-root);
	}

	/* ================================
	   SIDEBAR
	   ================================ */
	.sidebar {
		width: 260px;
		min-width: 260px;
		background: var(--bg-surface);
		border-right: 1px solid var(--border-subtle);
		display: flex;
		flex-direction: column;
		overflow: hidden;
	}

	.sidebar-header {
		padding: var(--space-lg);
		border-bottom: 1px solid var(--border-subtle);
		display: flex;
		align-items: center;
		justify-content: space-between;
	}
	.logo-row {
		display: flex;
		align-items: center;
		gap: var(--space-sm);
	}
	.logo-text {
		font-size: var(--text-lg);
		font-weight: 800;
		letter-spacing: -0.03em;
	}
	.slug-badge {
		font-size: var(--text-xs);
		color: var(--text-tertiary);
		background: var(--bg-raised);
		padding: 2px 8px;
		border-radius: var(--radius-full);
		border: 1px solid var(--border-subtle);
		font-family: var(--font-mono);
	}

	/* Nav */
	.sidebar-nav {
		flex: 1;
		overflow-y: auto;
		padding: var(--space-sm) 0;
	}

	.nav-section {
		margin-bottom: var(--space-md);
	}
	.nav-section-header {
		display: flex;
		align-items: center;
		justify-content: space-between;
		padding: var(--space-sm) var(--space-lg);
		font-size: var(--text-xs);
		font-weight: 700;
		text-transform: uppercase;
		letter-spacing: 0.08em;
		color: var(--text-tertiary);
	}
	.nav-action {
		color: var(--text-tertiary);
		padding: 2px;
		border-radius: var(--radius-sm);
		display: flex;
	}
	.nav-action:hover {
		color: var(--accent);
		background: var(--accent-glow);
	}

	.member-count {
		font-size: var(--text-xs);
		color: var(--text-tertiary);
		font-weight: 500;
	}

	/* Channel items */
	.nav-item {
		display: flex;
		align-items: center;
		gap: var(--space-xs);
		width: calc(100% - 12px);
		margin: 1px 6px;
		padding: 6px var(--space-md);
		border-radius: var(--radius-md);
		font-size: var(--text-base);
		color: var(--text-secondary);
		text-align: left;
	}
	.nav-item:hover {
		background: var(--bg-raised);
		color: var(--text-primary);
	}
	.nav-item.active {
		background: var(--accent-glow);
		color: var(--accent);
		border: 1px solid var(--accent-border);
	}
	.channel-hash {
		color: var(--text-tertiary);
		font-weight: 500;
		font-size: var(--text-sm);
		width: 14px;
		text-align: center;
		flex-shrink: 0;
	}
	.nav-item.active .channel-hash {
		color: var(--accent);
	}

	.new-channel-form {
		padding: 2px var(--space-md);
		margin-bottom: var(--space-xs);
	}
	.new-channel-form input {
		font-size: var(--text-sm) !important;
		padding: 4px 8px !important;
	}

	/* Members */
	.member-row {
		display: flex;
		align-items: center;
		gap: var(--space-sm);
		padding: 4px var(--space-lg);
		font-size: var(--text-sm);
		color: var(--text-secondary);
		width: 100%;
		text-align: left;
		border-radius: var(--radius-sm);
		background: none;
		border: none;
		cursor: default;
	}
	.member-row.clickable {
		cursor: pointer;
	}
	.member-row.clickable:hover {
		background: var(--bg-raised);
	}
	.member-row.selected {
		background: var(--accent-glow);
		border: 1px solid var(--accent-border);
	}
	.presence {
		width: 7px;
		height: 7px;
		border-radius: 50%;
		background: var(--border-strong);
		flex-shrink: 0;
		transition: background var(--transition-base);
	}
	.presence.online {
		background: var(--green);
		box-shadow: 0 0 6px rgba(34,197,94,0.4);
	}
	.member-name {
		flex: 1;
		min-width: 0;
		overflow: hidden;
		text-overflow: ellipsis;
		white-space: nowrap;
	}
	.member-row-wrap {
		display: flex;
		align-items: center;
		padding-right: var(--space-sm);
	}
	.member-row-wrap .member-row {
		flex: 1;
		min-width: 0;
	}
	.member-gear {
		flex-shrink: 0;
		width: 22px;
		height: 22px;
		display: flex;
		align-items: center;
		justify-content: center;
		border-radius: var(--radius-sm);
		color: var(--text-tertiary);
		opacity: 0;
		transition: opacity var(--transition-fast), color var(--transition-fast);
		background: none;
		border: none;
		cursor: pointer;
	}
	.member-row-wrap:hover .member-gear {
		opacity: 1;
	}
	.member-gear:hover {
		color: var(--accent);
		background: var(--accent-glow);
	}
	.role-tag {
		font-size: var(--text-xs);
		color: var(--text-tertiary);
		background: var(--bg-raised);
		padding: 1px 6px;
		border-radius: var(--radius-full);
		border: 1px solid var(--border-subtle);
		font-weight: 600;
	}
	.role-tag.admin-tag {
		color: var(--accent);
		background: var(--accent-glow);
		border-color: var(--accent-border);
	}

	/* Sidebar footer */
	/* User Menu */
	.user-menu-wrap {
		position: relative;
		padding: var(--space-sm) var(--space-md);
		border-top: 1px solid var(--border-subtle);
	}
	.user-menu-trigger {
		display: flex;
		align-items: center;
		gap: var(--space-sm);
		width: 100%;
		padding: var(--space-sm) var(--space-sm);
		background: transparent;
		border: none;
		border-radius: var(--radius-sm);
		color: var(--text-primary);
		cursor: pointer;
		font-size: 0.85rem;
		text-align: left;
	}
	.user-menu-trigger:hover {
		background: var(--bg-hover);
	}
	.user-avatar {
		display: flex;
		align-items: center;
		justify-content: center;
		width: 28px;
		height: 28px;
		border-radius: 50%;
		background: var(--accent);
		color: var(--bg-root);
		font-size: 0.75rem;
		font-weight: 700;
		flex-shrink: 0;
	}
	.user-name {
		flex: 1;
		overflow: hidden;
		text-overflow: ellipsis;
		white-space: nowrap;
	}
	.user-chevron {
		flex-shrink: 0;
		opacity: 0.5;
		transition: transform 0.15s;
	}
	.user-chevron.open {
		transform: rotate(180deg);
	}
	.user-menu-backdrop {
		position: fixed;
		inset: 0;
		z-index: 99;
	}
	.user-menu-popover {
		position: absolute;
		bottom: calc(100% + 4px);
		left: var(--space-sm);
		right: var(--space-sm);
		background: var(--bg-surface);
		border: 1px solid var(--border-subtle);
		border-radius: var(--radius-md);
		box-shadow: 0 -4px 16px rgba(0,0,0,0.3);
		z-index: 100;
		padding: var(--space-xs);
	}
	.user-menu-item {
		display: flex;
		align-items: center;
		gap: var(--space-sm);
		width: 100%;
		padding: var(--space-sm) var(--space-md);
		background: none;
		border: none;
		border-radius: var(--radius-sm);
		color: var(--text-secondary);
		font-size: 0.8rem;
		cursor: pointer;
		text-align: left;
	}
	.user-menu-item:hover {
		background: var(--bg-hover);
		color: var(--text-primary);
	}
	.user-menu-divider {
		height: 1px;
		background: var(--border-subtle);
		margin: var(--space-xs) 0;
	}
	.user-menu-danger:hover {
		color: var(--red) !important;
		background: rgba(239,68,68,0.1) !important;
	}
	.invite-bar {
		display: flex;
		gap: var(--space-xs);
		margin-bottom: var(--space-sm);
	}
	.invite-bar input {
		flex: 1;
		font-size: var(--text-xs) !important;
		padding: 4px 6px !important;
		font-family: var(--font-mono);
	}

	/* ================================
	   CHAT MAIN
	   ================================ */
	.chat-main {
		flex: 1;
		display: flex;
		flex-direction: column;
		min-width: 0;
		background: var(--bg-root);
	}

	/* Channel header */
	.chat-header {
		padding: var(--space-md) var(--space-xl);
		border-bottom: 1px solid var(--border-subtle);
		display: flex;
		align-items: center;
		justify-content: space-between;
		background: var(--bg-surface);
	}
	.chat-header-left {
		display: flex;
		align-items: center;
		gap: var(--space-sm);
	}
	.header-hash {
		color: var(--accent);
		font-size: var(--text-xl);
		font-weight: 600;
		opacity: 0.6;
	}
	.chat-header h2 {
		font-size: var(--text-lg);
		font-weight: 700;
	}
	.header-meta {
		font-size: var(--text-sm);
		color: var(--text-tertiary);
	}
	.clear-chat-btn {
		padding: 4px;
		background: none;
		border: none;
		color: var(--text-tertiary);
		cursor: pointer;
		border-radius: var(--radius-sm);
		opacity: 0;
		transition: opacity 0.15s, color 0.15s;
	}
	.chat-header:hover .clear-chat-btn {
		opacity: 0.6;
	}
	.clear-chat-btn:hover {
		opacity: 1 !important;
		color: var(--red);
	}

	/* Messages area */
	.messages-area {
		flex: 1;
		overflow-y: auto;
		padding: var(--space-lg) var(--space-xl);
		display: flex;
		flex-direction: column;
		gap: 2px;
	}

	.message-row {
		display: flex;
		gap: var(--space-md);
		padding: var(--space-sm) var(--space-md);
		border-radius: var(--radius-md);
		transition: background var(--transition-fast);
	}
	.message-row:hover {
		background: var(--bg-surface);
	}

	.avatar {
		width: 34px;
		height: 34px;
		border-radius: var(--radius-md);
		background: var(--accent-glow);
		border: 1px solid var(--accent-border);
		color: var(--accent);
		display: flex;
		align-items: center;
		justify-content: center;
		font-weight: 700;
		font-size: var(--text-sm);
		flex-shrink: 0;
		margin-top: 2px;
	}

	.message-body {
		flex: 1;
		min-width: 0;
	}
	.message-meta {
		display: flex;
		align-items: baseline;
		gap: var(--space-sm);
		margin-bottom: 1px;
	}
	.sender {
		font-weight: 600;
		font-size: var(--text-base);
		color: var(--text-primary);
	}
	.timestamp {
		font-size: var(--text-xs);
		color: var(--text-tertiary);
		font-family: var(--font-mono);
	}
	.edited-tag {
		font-size: var(--text-xs);
		color: var(--text-tertiary);
		font-style: italic;
	}
	.message-text {
		font-size: var(--text-base);
		line-height: 1.5;
		color: var(--text-secondary);
		white-space: pre-wrap;
		word-break: break-word;
	}

	.skill-badge {
		display: inline-block;
		font-size: 0.7rem;
		font-weight: 600;
		color: var(--accent);
		background: color-mix(in srgb, var(--accent) 12%, transparent);
		padding: 2px 8px;
		border-radius: 4px;
		margin-bottom: 4px;
		letter-spacing: 0.02em;
	}

	/* File attachments */
	.message-file {
		margin-top: var(--space-xs);
	}
	.file-preview-img {
		max-width: 400px;
		max-height: 300px;
		border-radius: var(--radius-md);
		border: 1px solid var(--border-subtle);
		cursor: pointer;
		transition: border-color var(--transition-fast);
	}
	.file-preview-img:hover {
		border-color: var(--accent);
	}
	.image-prompt-details {
		margin-top: var(--space-sm);
	}
	.image-prompt-toggle {
		display: inline-flex;
		align-items: center;
		gap: 4px;
		font-size: var(--text-xs);
		color: var(--text-tertiary);
		cursor: pointer;
		user-select: none;
		padding: 2px 6px;
		border-radius: var(--radius-sm);
		transition: color 0.15s, background 0.15s;
	}
	.image-prompt-toggle:hover {
		color: var(--text-secondary);
		background: var(--bg-surface);
	}
	.image-prompt-details[open] .image-prompt-toggle svg {
		transform: rotate(180deg);
	}
	.image-prompt-content {
		margin-top: var(--space-xs);
		padding: var(--space-sm) var(--space-md);
		background: var(--bg-raised);
		border: 1px solid var(--border-subtle);
		border-radius: var(--radius-md);
		font-size: var(--text-xs);
		color: var(--text-secondary);
		white-space: pre-wrap;
		word-break: break-word;
		line-height: 1.5;
		max-height: 200px;
		overflow-y: auto;
	}
	.file-link {
		display: inline-flex;
		align-items: center;
		gap: var(--space-sm);
		padding: var(--space-sm) var(--space-md);
		background: var(--bg-surface);
		border: 1px solid var(--border-default);
		border-radius: var(--radius-md);
		color: var(--accent);
		font-size: var(--text-sm);
		text-decoration: none;
		transition: border-color var(--transition-fast);
	}
	.file-link:hover {
		border-color: var(--accent);
		background: var(--accent-glow);
	}
	.file-size {
		color: var(--text-tertiary);
		font-size: var(--text-xs);
	}

	/* Attach button */
	.attach-button {
		padding: var(--space-sm) var(--space-md);
		margin-left: var(--space-sm);
		color: var(--text-tertiary);
		border-radius: var(--radius-md);
		display: flex;
		align-items: center;
		justify-content: center;
	}
	.attach-button:hover:not(:disabled) {
		color: var(--accent);
		background: var(--accent-glow);
	}
	.attach-button:disabled {
		opacity: 0.5;
		cursor: not-allowed;
	}
	@keyframes spin {
		to { transform: rotate(360deg); }
	}
	.spin {
		animation: spin 1s linear infinite;
	}

	/* Empty state */
	.empty-state {
		flex: 1;
		display: flex;
		flex-direction: column;
		align-items: center;
		justify-content: center;
		gap: var(--space-md);
		padding: var(--space-3xl);
	}
	.empty-icon { opacity: 0.5; }
	.empty-title {
		font-size: var(--text-lg);
		font-weight: 600;
		color: var(--text-secondary);
	}
	.empty-sub {
		font-size: var(--text-base);
		color: var(--text-tertiary);
	}

	/* Typing indicator */
	.typing-bar {
		padding: var(--space-xs) var(--space-xl);
		display: flex;
		align-items: center;
		gap: var(--space-sm);
		font-size: var(--text-sm);
		color: var(--text-tertiary);
	}
	.typing-dots {
		display: flex;
		gap: 3px;
	}
	.typing-dots span {
		width: 4px;
		height: 4px;
		border-radius: 50%;
		background: var(--accent);
		animation: typingBounce 1.2s infinite;
	}
	.typing-dots span:nth-child(2) { animation-delay: 0.2s; }
	.typing-dots span:nth-child(3) { animation-delay: 0.4s; }

	@keyframes typingBounce {
		0%, 60%, 100% { opacity: 0.3; transform: translateY(0); }
		30% { opacity: 1; transform: translateY(-3px); }
	}

	/* Input bar */
	.input-bar {
		padding: var(--space-md) var(--space-xl) var(--space-lg);
	}
	.input-wrapper {
		display: flex;
		align-items: center;
		background: var(--bg-surface);
		border: 1px solid var(--border-default);
		border-radius: var(--radius-lg);
		overflow: hidden;
		transition: border-color var(--transition-base), box-shadow var(--transition-base);
	}
	.input-wrapper:focus-within {
		border-color: var(--accent);
		box-shadow: 0 0 0 3px var(--accent-glow), 0 0 20px rgba(249,115,22,0.05);
	}
	.input-wrapper input {
		flex: 1;
		border: none !important;
		background: transparent !important;
		padding: var(--space-md) var(--space-lg) !important;
		border-radius: 0 !important;
		font-size: var(--text-base) !important;
	}
	.input-wrapper input:focus {
		box-shadow: none !important;
	}

	.send-button {
		padding: var(--space-sm) var(--space-md);
		margin-right: var(--space-sm);
		color: var(--accent);
		border-radius: var(--radius-md);
		display: flex;
		align-items: center;
		justify-content: center;
	}
	.send-button:hover:not(:disabled) {
		background: var(--accent-glow);
	}
	.send-button:disabled {
		color: var(--text-tertiary);
		opacity: 0.3;
		cursor: not-allowed;
	}

	/* ================================
	   VIEW DROPDOWN
	   ================================ */
	.view-dropdown-wrap {
		padding: var(--space-sm) var(--space-md);
		margin-bottom: var(--space-xs);
	}
	.view-dropdown {
		width: 100%;
		padding: 7px 10px;
		background: var(--bg-raised);
		color: var(--text-primary);
		border: 1px solid var(--border-default);
		border-radius: var(--radius-md);
		font-size: var(--text-sm);
		font-weight: 600;
		font-family: inherit;
		cursor: pointer;
		appearance: none;
		background-image: url("data:image/svg+xml,%3Csvg width='10' height='6' viewBox='0 0 10 6' fill='none' xmlns='http://www.w3.org/2000/svg'%3E%3Cpath d='M1 1l4 4 4-4' stroke='%23666' stroke-width='1.5' stroke-linecap='round'/%3E%3C/svg%3E");
		background-repeat: no-repeat;
		background-position: right 10px center;
		padding-right: 28px;
	}
	.view-dropdown:focus {
		border-color: var(--accent);
		outline: none;
		box-shadow: 0 0 0 2px var(--accent-glow);
	}
	.sidebar-empty {
		padding: var(--space-md) var(--space-lg);
		font-size: var(--text-xs);
		color: var(--text-tertiary);
	}

	/* ================================
	   TASK VIEWS
	   ================================ */
	.task-main {
		flex: 1;
		display: flex;
		flex-direction: column;
		min-width: 0;
		background: var(--bg-root);
		overflow: hidden;
	}
	.task-header {
		display: flex;
		align-items: center;
		justify-content: space-between;
		padding: var(--space-md) var(--space-xl);
		border-bottom: 1px solid var(--border-subtle);
		background: var(--bg-surface);
	}
	.task-header h2 {
		font-size: var(--text-lg);
		font-weight: 700;
	}
	.new-task-bar {
		display: flex;
		gap: var(--space-sm);
		padding: var(--space-md) var(--space-xl);
		background: var(--bg-surface);
		border-bottom: 1px solid var(--border-subtle);
	}
	.new-task-bar input {
		flex: 1;
		padding: 6px 10px !important;
		font-size: var(--text-sm) !important;
	}
	.new-task-bar select {
		padding: 6px 8px;
		background: var(--bg-raised);
		color: var(--text-primary);
		border: 1px solid var(--border-default);
		border-radius: var(--radius-md);
		font-size: var(--text-xs);
		font-family: inherit;
	}

	/* Board */
	.board {
		flex: 1;
		display: flex;
		gap: var(--space-md);
		padding: var(--space-lg);
		overflow-x: auto;
	}
	.board-col {
		min-width: 220px;
		flex: 1;
		display: flex;
		flex-direction: column;
		gap: var(--space-sm);
	}
	.board-col-header {
		display: flex;
		align-items: center;
		justify-content: space-between;
		padding: var(--space-sm) var(--space-md);
		font-size: var(--text-xs);
		font-weight: 700;
		text-transform: uppercase;
		letter-spacing: 0.08em;
		color: var(--text-tertiary);
	}
	.board-count {
		background: var(--bg-raised);
		border: 1px solid var(--border-subtle);
		padding: 0 6px;
		border-radius: var(--radius-full);
		font-size: var(--text-xs);
	}
	.board-cards {
		flex: 1;
		display: flex;
		flex-direction: column;
		gap: 6px;
		overflow-y: auto;
	}

	/* Task card */
	.task-card {
		background: var(--bg-surface);
		border: 1px solid var(--border-subtle);
		border-radius: var(--radius-md);
		padding: var(--space-md);
		cursor: pointer;
		transition: border-color var(--transition-fast);
	}
	.task-card:hover {
		border-color: var(--border-strong);
	}
	.task-card-header {
		display: flex;
		align-items: flex-start;
		gap: var(--space-sm);
	}
	.task-priority-dot {
		width: 8px;
		height: 8px;
		border-radius: 50%;
		flex-shrink: 0;
		margin-top: 5px;
	}
	.task-title {
		font-size: var(--text-sm);
		font-weight: 500;
		color: var(--text-primary);
		line-height: 1.4;
	}
	.task-tags {
		display: flex;
		gap: 4px;
		margin-top: var(--space-sm);
		flex-wrap: wrap;
	}
	.task-tag {
		font-size: 10px;
		padding: 1px 6px;
		border-radius: var(--radius-full);
		background: var(--bg-raised);
		border: 1px solid var(--border-subtle);
		color: var(--text-tertiary);
	}
	.task-due {
		font-size: var(--text-xs);
		color: var(--text-tertiary);
		margin-top: var(--space-xs);
	}
	.task-card-actions {
		display: flex;
		gap: 4px;
		margin-top: var(--space-sm);
		padding-top: var(--space-sm);
		border-top: 1px solid var(--border-subtle);
	}
	.task-card-actions select {
		flex: 1;
		padding: 3px 4px;
		background: var(--bg-raised);
		color: var(--text-primary);
		border: 1px solid var(--border-default);
		border-radius: var(--radius-sm);
		font-size: 10px;
		font-family: inherit;
	}
	.btn-del {
		padding: 3px 8px;
		font-size: 10px;
		color: var(--red);
		background: rgba(239,68,68,0.1);
		border: 1px solid rgba(239,68,68,0.2);
		border-radius: var(--radius-sm);
		cursor: pointer;
	}
	.btn-del:hover { background: rgba(239,68,68,0.2); }

	/* List view */
	.task-list {
		flex: 1;
		overflow-y: auto;
	}
	.task-list-header {
		display: flex;
		align-items: center;
		padding: var(--space-sm) var(--space-xl);
		font-size: var(--text-xs);
		font-weight: 700;
		text-transform: uppercase;
		letter-spacing: 0.06em;
		color: var(--text-tertiary);
		border-bottom: 1px solid var(--border-subtle);
		background: var(--bg-surface);
		position: sticky;
		top: 0;
	}
	.task-list-row {
		display: flex;
		align-items: center;
		padding: var(--space-sm) var(--space-xl);
		border-bottom: 1px solid var(--border-subtle);
		transition: background var(--transition-fast);
	}
	.task-list-row:hover { background: var(--bg-surface); }
	.tl-pri { width: 80px; flex-shrink: 0; }
	.tl-pri select {
		padding: 2px 4px;
		background: var(--bg-raised);
		color: var(--text-primary);
		border: 1px solid var(--border-default);
		border-radius: var(--radius-sm);
		font-size: var(--text-xs);
		font-family: inherit;
	}
	.tl-title { flex: 1; min-width: 0; font-size: var(--text-sm); color: var(--text-primary); overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
	.tl-status { width: 120px; flex-shrink: 0; }
	.tl-status select {
		padding: 2px 4px;
		background: var(--bg-raised);
		color: var(--text-primary);
		border: 1px solid var(--border-default);
		border-radius: var(--radius-sm);
		font-size: var(--text-xs);
		font-family: inherit;
	}
	.tl-tags { width: 120px; flex-shrink: 0; display: flex; gap: 3px; flex-wrap: wrap; }
	.tl-date { width: 80px; flex-shrink: 0; font-size: var(--text-xs); color: var(--text-tertiary); font-family: var(--font-mono); }
	.tl-actions { width: 40px; flex-shrink: 0; text-align: center; }
	.btn-del-sm {
		width: 20px; height: 20px;
		display: inline-flex; align-items: center; justify-content: center;
		border-radius: var(--radius-sm);
		color: var(--text-tertiary);
		font-size: 12px;
		cursor: pointer;
		background: none; border: none;
	}
	.btn-del-sm:hover { color: var(--red); background: rgba(239,68,68,0.1); }

	/* ================================
	   NOTES VIEW
	   ================================ */
	.notes-main {
		flex: 1;
		display: flex;
		flex-direction: column;
		min-width: 0;
		background: var(--bg-root);
	}
	.notes-editor {
		flex: 1;
		display: flex;
		flex-direction: column;
		max-width: 800px;
		width: 100%;
		margin: 0 auto;
		padding: var(--space-xl);
	}
	.notes-toolbar {
		display: flex;
		align-items: center;
		gap: var(--space-md);
		margin-bottom: var(--space-lg);
		padding-bottom: var(--space-md);
		border-bottom: 1px solid var(--border-subtle);
	}
	.notes-title-input {
		flex: 1;
		background: none !important;
		border: none !important;
		font-size: var(--text-xl) !important;
		font-weight: 800 !important;
		color: var(--text-primary) !important;
		padding: 0 !important;
		letter-spacing: -0.02em;
	}
	.notes-title-input:focus {
		box-shadow: none !important;
	}
	.notes-title-input::placeholder {
		color: var(--text-tertiary);
	}
	.notes-toolbar-actions {
		display: flex;
		align-items: center;
		gap: var(--space-sm);
		flex-shrink: 0;
	}
	.notes-saved {
		font-size: var(--text-xs);
		color: var(--accent);
		opacity: 0;
		transition: opacity 0.2s;
	}
	.notes-saved.visible { opacity: 1; }
	.notes-del-btn:hover {
		color: var(--red) !important;
	}
	.notes-meta {
		margin-top: var(--space-lg);
		padding-top: var(--space-md);
		border-top: 1px solid var(--border-subtle);
		font-size: var(--text-xs);
		color: var(--text-tertiary);
		display: flex;
		gap: var(--space-sm);
	}

	/* ================================
	   MEMBER PANEL
	   ================================ */
	.member-panel {
		width: 280px;
		min-width: 280px;
		background: var(--bg-surface);
		border-left: 1px solid var(--border-subtle);
		display: flex;
		flex-direction: column;
		overflow-y: auto;
	}
	.panel-header {
		display: flex;
		align-items: center;
		justify-content: space-between;
		padding: var(--space-md) var(--space-lg);
		border-bottom: 1px solid var(--border-subtle);
	}
	.panel-header h3 {
		font-size: var(--text-base);
		font-weight: 700;
	}
	.panel-close {
		color: var(--text-tertiary);
		padding: 4px;
		border-radius: var(--radius-sm);
	}
	.panel-close:hover {
		color: var(--text-primary);
		background: var(--bg-raised);
	}
	.panel-body {
		padding: var(--space-lg);
		display: flex;
		flex-direction: column;
		align-items: center;
		gap: var(--space-lg);
	}
	.panel-avatar {
		width: 56px;
		height: 56px;
		border-radius: var(--radius-lg);
		background: var(--accent-glow);
		border: 2px solid var(--accent-border);
		color: var(--accent);
		display: flex;
		align-items: center;
		justify-content: center;
		font-weight: 800;
		font-size: var(--text-xl);
	}
	.panel-name {
		font-weight: 700;
		font-size: var(--text-lg);
	}
	.panel-field {
		width: 100%;
	}
	.panel-field label {
		display: block;
		font-size: var(--text-xs);
		font-weight: 700;
		text-transform: uppercase;
		letter-spacing: 0.08em;
		color: var(--text-tertiary);
		margin-bottom: var(--space-xs);
	}
	.panel-field select {
		width: 100%;
		padding: 6px 10px;
		background: var(--bg-raised);
		color: var(--text-primary);
		border: 1px solid var(--border-default);
		border-radius: var(--radius-md);
		font-size: var(--text-sm);
		font-family: inherit;
	}
	.panel-field select:focus {
		border-color: var(--accent);
		outline: none;
		box-shadow: 0 0 0 2px var(--accent-glow);
	}
	.perm-list {
		display: flex;
		flex-direction: column;
		gap: 2px;
	}
	.perm-row {
		display: flex;
		justify-content: space-between;
		padding: 3px 0;
		font-size: var(--text-xs);
	}
	.perm-name {
		color: var(--text-secondary);
		font-family: var(--font-mono);
	}
	.perm-val {
		color: var(--red);
		font-weight: 600;
	}
	.perm-val.granted {
		color: var(--green);
	}
	.kick-btn {
		width: 100%;
		margin-top: var(--space-md);
		color: var(--red) !important;
		border: 1px solid rgba(239,68,68,0.3) !important;
		background: rgba(239,68,68,0.05) !important;
		justify-content: center;
	}
	.kick-btn:hover {
		background: rgba(239,68,68,0.15) !important;
	}

	/* ================================
	   BRAIN SETTINGS
	   ================================ */
	.brain-main {
		flex: 1;
		overflow-y: auto;
		padding: var(--space-xl);
	}
	.brain-settings {
		max-width: 640px;
	}
	.brain-heading {
		font-size: 1.25rem;
		font-weight: 600;
		color: var(--text-primary);
		margin-bottom: var(--space-md);
	}
	.brain-tabs {
		display: flex;
		gap: 0;
		border-bottom: 1px solid var(--border-subtle);
		margin-bottom: var(--space-xl);
	}
	.brain-tab {
		padding: var(--space-sm) var(--space-lg);
		background: none;
		border: none;
		border-bottom: 2px solid transparent;
		color: var(--text-tertiary);
		font-size: 0.85rem;
		cursor: pointer;
	}
	.brain-tab:hover {
		color: var(--text-primary);
	}
	.brain-tab.active {
		color: var(--accent);
		border-bottom-color: var(--accent);
	}
	.brain-section {
		margin-bottom: var(--space-xl);
		padding-bottom: var(--space-xl);
		border-bottom: 1px solid var(--border-subtle);
	}
	.brain-section:last-child {
		border-bottom: none;
	}
	.brain-section-title {
		font-size: 0.9rem;
		font-weight: 600;
		color: var(--text-primary);
		margin-bottom: var(--space-md);
	}
	.brain-field {
		margin-bottom: var(--space-md);
	}
	.brain-field label {
		display: block;
		font-size: 0.8rem;
		color: var(--text-secondary);
		margin-bottom: var(--space-xs);
	}
	.brain-input {
		width: 100%;
		padding: var(--space-sm) var(--space-md);
		background: var(--bg-root);
		border: 1px solid var(--border-subtle);
		border-radius: var(--radius-sm);
		color: var(--text-primary);
		font-size: 0.85rem;
		font-family: var(--font-mono, monospace);
	}
	.brain-input:focus {
		outline: none;
		border-color: var(--accent);
	}
	.brain-hint {
		display: block;
		font-size: 0.75rem;
		color: var(--text-tertiary);
		margin-top: var(--space-xs);
	}
	.brain-hint a {
		color: var(--accent);
	}
	.brain-toggle-row {
		display: flex;
		align-items: center;
		gap: var(--space-sm);
		cursor: pointer;
		font-size: 0.85rem;
		color: var(--text-primary);
	}
	.brain-toggle-row input[type="checkbox"] {
		accent-color: var(--accent);
		width: 16px;
		height: 16px;
	}
	.brain-freq-row {
		display: flex;
		align-items: center;
		gap: var(--space-md);
	}
	.brain-range {
		flex: 1;
		accent-color: var(--accent);
	}
	.brain-freq-val {
		font-size: 0.8rem;
		color: var(--text-secondary);
		white-space: nowrap;
		min-width: 120px;
	}
	.brain-key-status {
		font-size: 0.8rem;
		color: var(--green, #22c55e);
		margin-bottom: var(--space-xs);
	}
	.brain-files {
		display: flex;
		gap: var(--space-sm);
		margin-bottom: var(--space-md);
		flex-wrap: wrap;
	}
	.brain-file-btn {
		padding: var(--space-xs) var(--space-md);
		background: var(--bg-root);
		border: 1px solid var(--border-subtle);
		border-radius: var(--radius-sm);
		color: var(--text-secondary);
		font-size: 0.8rem;
		cursor: pointer;
	}
	.brain-file-btn:hover {
		border-color: var(--accent);
		color: var(--text-primary);
	}
	.brain-file-btn.active {
		border-color: var(--accent);
		color: var(--accent);
		background: color-mix(in srgb, var(--accent) 10%, transparent);
	}
	.brain-editor {
		border: 1px solid var(--border-subtle);
		border-radius: var(--radius-sm);
		overflow: hidden;
	}
	.brain-editor-header {
		display: flex;
		align-items: center;
		justify-content: space-between;
		padding: var(--space-sm) var(--space-md);
		background: var(--bg-surface);
		border-bottom: 1px solid var(--border-subtle);
	}
	.brain-file-name {
		font-size: 0.8rem;
		font-weight: 500;
		color: var(--text-secondary);
	}
	.brain-file-content {
		width: 100%;
		min-height: 300px;
		padding: var(--space-md);
		background: var(--bg-root);
		border: none;
		color: var(--text-primary);
		font-size: 0.85rem;
		font-family: var(--font-mono, monospace);
		line-height: 1.6;
		resize: vertical;
	}
	.brain-file-content:focus {
		outline: none;
	}

	/* Memory List */
	.memory-stats {
		display: flex;
		gap: var(--space-lg);
		margin-bottom: var(--space-lg);
	}
	.memory-stat {
		display: flex;
		flex-direction: column;
		align-items: center;
	}
	.memory-stat-count {
		font-size: 1.5rem;
		font-weight: 700;
		color: var(--accent);
	}
	.memory-stat-label {
		font-size: 0.75rem;
		color: var(--text-tertiary);
		text-transform: capitalize;
	}
	.memory-actions {
		margin-bottom: var(--space-md);
	}
	.memory-clear-btn:hover {
		color: var(--red) !important;
		background: rgba(239,68,68,0.1) !important;
	}
	.memory-list {
		display: flex;
		flex-direction: column;
		gap: var(--space-xs);
	}
	.memory-item {
		display: flex;
		align-items: flex-start;
		gap: var(--space-sm);
		padding: var(--space-sm) var(--space-md);
		background: var(--bg-root);
		border: 1px solid var(--border-subtle);
		border-radius: var(--radius-sm);
	}
	.memory-type-badge {
		flex-shrink: 0;
		padding: 2px 8px;
		border-radius: 9999px;
		font-size: 0.7rem;
		font-weight: 600;
		text-transform: uppercase;
		background: color-mix(in srgb, var(--accent) 15%, transparent);
		color: var(--accent);
	}
	.memory-type-badge[data-type="decision"] {
		background: color-mix(in srgb, var(--yellow, #eab308) 15%, transparent);
		color: var(--yellow, #eab308);
	}
	.memory-type-badge[data-type="commitment"] {
		background: color-mix(in srgb, var(--green, #22c55e) 15%, transparent);
		color: var(--green, #22c55e);
	}
	.memory-type-badge[data-type="person"] {
		background: color-mix(in srgb, #8b5cf6 15%, transparent);
		color: #8b5cf6;
	}
	.memory-content {
		flex: 1;
		font-size: 0.85rem;
		color: var(--text-primary);
		line-height: 1.4;
	}
	.memory-delete {
		flex-shrink: 0;
		padding: 4px;
		background: none;
		border: none;
		color: var(--text-tertiary);
		cursor: pointer;
		border-radius: var(--radius-sm);
		opacity: 0;
	}
	.memory-item:hover .memory-delete {
		opacity: 1;
	}
	.memory-delete:hover {
		color: var(--red);
		background: rgba(239,68,68,0.1);
	}

	/* Activity Log */
	.action-list {
		display: flex;
		flex-direction: column;
		gap: 0.5rem;
	}
	.action-item {
		background: var(--bg-root);
		border: 1px solid var(--border-subtle);
		border-radius: var(--radius-md);
		padding: 0.75rem;
	}
	.action-header {
		display: flex;
		align-items: center;
		gap: 0.5rem;
		margin-bottom: 0.35rem;
	}
	.action-type-badge {
		font-size: 0.7rem;
		font-weight: 600;
		text-transform: uppercase;
		padding: 2px 6px;
		border-radius: var(--radius-sm);
		background: var(--bg-hover);
		color: var(--text-secondary);
	}
	.action-type-badge[data-type="mention"] { background: rgba(59,130,246,0.15); color: #60a5fa; }
	.action-type-badge[data-type="extraction"] { background: rgba(168,85,247,0.15); color: #c084fc; }
	.action-type-badge[data-type="heartbeat"] { background: rgba(34,197,94,0.15); color: #4ade80; }
	.action-model {
		font-size: 0.7rem;
		color: var(--text-tertiary);
	}
	.action-time {
		font-size: 0.7rem;
		color: var(--text-tertiary);
		margin-left: auto;
	}
	.action-trigger {
		font-size: 0.8rem;
		color: var(--text-secondary);
		margin-bottom: 0.25rem;
		white-space: nowrap;
		overflow: hidden;
		text-overflow: ellipsis;
	}
	.action-tools {
		display: flex;
		gap: 0.25rem;
		flex-wrap: wrap;
		margin-bottom: 0.25rem;
	}
	.action-tool-badge {
		font-size: 0.65rem;
		padding: 1px 5px;
		border-radius: var(--radius-sm);
		background: rgba(251,191,36,0.15);
		color: #fbbf24;
		font-family: var(--font-mono, monospace);
	}
	.action-response {
		font-size: 0.8rem;
		color: var(--text-tertiary);
		white-space: nowrap;
		overflow: hidden;
		text-overflow: ellipsis;
	}

	/* Skills */
	.skill-list {
		display: flex;
		flex-direction: column;
		gap: 0.35rem;
	}
	.skill-item {
		display: flex;
		align-items: center;
		border: 1px solid var(--border-subtle);
		border-radius: var(--radius-md);
		overflow: hidden;
	}
	.skill-item.active {
		border-color: var(--accent);
	}
	.skill-select {
		flex: 1;
		display: flex;
		flex-direction: column;
		align-items: flex-start;
		padding: 0.5rem 0.75rem;
		background: none;
		border: none;
		cursor: pointer;
		text-align: left;
		color: var(--text-primary);
	}
	.skill-select:hover {
		background: var(--bg-hover);
	}
	.skill-name {
		font-weight: 600;
		font-size: 0.85rem;
	}
	.skill-desc {
		font-size: 0.75rem;
		color: var(--text-secondary);
	}
	.skill-meta {
		font-size: 0.65rem;
		color: var(--text-tertiary);
		margin-top: 2px;
	}
	.skill-delete {
		padding: 8px;
		background: none;
		border: none;
		color: var(--text-tertiary);
		cursor: pointer;
		margin-right: 4px;
	}
	.skill-delete:hover {
		color: var(--red);
	}

	/* Knowledge */
	.knowledge-list {
		display: flex;
		flex-direction: column;
		gap: 2px;
	}
	.knowledge-item {
		display: flex;
		align-items: center;
		border-radius: var(--radius-sm);
		background: var(--bg-root);
		border: 1px solid var(--border-subtle);
	}
	.knowledge-item:hover {
		border-color: var(--border-default);
	}
	.knowledge-select {
		flex: 1;
		display: flex;
		flex-direction: column;
		gap: 2px;
		padding: 8px 12px;
		background: none;
		border: none;
		cursor: pointer;
		text-align: left;
		color: var(--text-primary);
	}
	.knowledge-title {
		font-size: 0.85rem;
		font-weight: 500;
	}
	.knowledge-meta {
		display: flex;
		align-items: center;
		gap: var(--space-sm);
		font-size: 0.7rem;
	}
	.knowledge-badge {
		padding: 1px 6px;
		border-radius: 4px;
		font-size: 0.65rem;
		background: rgba(59,130,246,0.15);
		color: #60a5fa;
	}
	.knowledge-badge[data-type="file"] {
		background: rgba(168,85,247,0.15);
		color: #c084fc;
	}
	.knowledge-tokens {
		color: var(--text-tertiary);
	}

	/* Announcement Banner */
	.announcement-banner {
		display: flex;
		align-items: center;
		justify-content: center;
		padding: 8px 16px;
		font-size: 0.85rem;
		gap: 12px;
		position: relative;
		z-index: 100;
	}
	.announcement-banner[data-type="info"] {
		background: rgba(59,130,246,0.15);
		color: #60a5fa;
	}
	.announcement-banner[data-type="warning"] {
		background: rgba(239,68,68,0.15);
		color: #f87171;
	}
	.announcement-banner[data-type="success"] {
		background: rgba(34,197,94,0.15);
		color: #4ade80;
	}
	.announcement-text {
		flex: 1;
		text-align: center;
	}
	.announcement-dismiss {
		background: none;
		border: none;
		color: inherit;
		font-size: 1.2rem;
		cursor: pointer;
		opacity: 0.7;
		padding: 0 4px;
	}
	.announcement-dismiss:hover {
		opacity: 1;
	}

	/* Model Browser Modal */
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
		border-radius: var(--radius-lg, 12px);
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
		font-size: 1rem;
		font-weight: 600;
		margin: 0;
	}
	.modal-close {
		background: none;
		border: none;
		color: var(--text-tertiary);
		font-size: 1.5rem;
		cursor: pointer;
		line-height: 1;
	}
	.model-browser-controls {
		padding: 12px 20px;
		display: flex;
		gap: 8px;
		align-items: center;
		border-bottom: 1px solid var(--border-subtle);
	}
	.model-filters {
		display: flex;
		gap: 4px;
	}
	.model-filters .btn-xs.active {
		background: var(--accent);
		color: #fff;
	}
	.model-browser-list {
		overflow-y: auto;
		flex: 1;
		padding: 8px 0;
	}
	.model-browser-item {
		display: flex;
		align-items: center;
		justify-content: space-between;
		padding: 8px 20px;
		gap: 12px;
	}
	.model-browser-item:hover {
		background: var(--bg-hover);
	}
	.model-browser-info {
		flex: 1;
		min-width: 0;
	}
	.model-browser-name {
		display: block;
		font-size: 0.85rem;
		font-weight: 500;
		white-space: nowrap;
		overflow: hidden;
		text-overflow: ellipsis;
	}
	.model-browser-meta {
		display: flex;
		gap: 8px;
		font-size: 0.7rem;
		color: var(--text-tertiary);
		margin-top: 2px;
	}
	.model-browser-provider {
		color: var(--text-secondary);
	}
	.model-badge {
		padding: 0 4px;
		border-radius: 3px;
		font-size: 0.65rem;
		font-weight: 600;
	}
	.model-badge.free {
		background: rgba(34,197,94,0.15);
		color: #4ade80;
	}
	.model-badge.new {
		background: rgba(59,130,246,0.15);
		color: #60a5fa;
	}

	/* ================================
	   TEAM VIEW
	   ================================ */
	.main-content {
		flex: 1;
		display: flex;
		flex-direction: column;
		min-width: 0;
		overflow: auto;
	}
	.team-view { padding: 20px; width: 100%; height: 100%; }
	/* Member avatar (circle style reusing agent-avatar position) */
	.member-avatar-circle {
		width: 32px; height: 32px; border-radius: 50%; background: var(--accent);
		color: white; display: flex; align-items: center; justify-content: center;
		font-weight: 600; font-size: 0.8rem; flex-shrink: 0;
	}
	.member-online-dot {
		width: 8px; height: 8px; border-radius: 50%; background: var(--text-tertiary); flex-shrink: 0;
	}
	.member-online-dot.online { background: var(--green, #22c55e); }

	/* Agents tab */
	.team-agents { width: 100%; }
	.agents-toolbar { display: flex; gap: 8px; margin-bottom: 16px; }
	.agents-grid { display: grid; grid-template-columns: repeat(auto-fill, minmax(300px, 1fr)); gap: 12px; }
	.agent-card {
		padding: 16px; background: var(--bg-secondary); border-radius: 8px;
		border: 1px solid var(--border); display: flex; flex-direction: column; gap: 8px;
	}
	.agent-card.inactive { opacity: 0.6; }
	.agent-card.system { border-color: var(--accent); }
	.agent-card-header { display: flex; align-items: center; gap: 8px; }
	.agent-avatar { font-size: 1.5rem; }
	.agent-card-name-row { display: flex; align-items: center; gap: 6px; }
	.agent-name { font-weight: 600; color: var(--text-primary); font-size: 0.9rem; }
	.agent-badge {
		font-size: 0.65rem; padding: 1px 6px; border-radius: 10px;
		text-transform: uppercase; font-weight: 500;
	}
	.agent-badge.system { background: var(--accent); color: white; }
	.agent-badge.paused { background: var(--text-tertiary); color: white; }
	.agent-badge.trigger-all { background: var(--blue, #3b82f6); color: white; }
	.agent-badge.trigger-always { background: var(--green, #22c55e); color: white; }
	.agent-card-role { color: var(--text-secondary); font-size: 0.8rem; }
	.agent-card-goal { color: var(--text-tertiary); font-size: 0.75rem; }
	.agent-card-tools { display: flex; flex-wrap: wrap; gap: 4px; }
	.tool-chip {
		font-size: 0.65rem; padding: 2px 6px; border-radius: 4px;
		background: var(--bg-input); color: var(--text-secondary); font-family: monospace;
	}
	.agent-card-actions { display: flex; gap: 4px; margin-top: 4px; border-top: 1px solid var(--border); padding-top: 8px; }
	.btn-danger { color: var(--red, #ef4444) !important; }

	/* Template gallery */
	.template-gallery { width: 100%; }
	.template-header { display: flex; align-items: center; justify-content: space-between; margin-bottom: 16px; }
	.template-header h3 { font-size: 1rem; font-weight: 600; color: var(--text-primary); }
	.template-card { border-style: dashed; }

	/* Agent form */
	.agent-form { max-width: 640px; }
	.agent-form-header { display: flex; align-items: center; justify-content: space-between; margin-bottom: 16px; }
	.agent-form-header h3 { font-size: 1rem; font-weight: 600; color: var(--text-primary); }
	.form-section { margin-bottom: 20px; }
	.form-section h4 {
		font-size: 0.75rem; text-transform: uppercase; letter-spacing: 0.05em;
		color: var(--text-tertiary); margin-bottom: 10px; font-weight: 600;
	}
	.form-field { display: flex; flex-direction: column; gap: 4px; margin-bottom: 10px; }
	.form-field span { font-size: 0.8rem; color: var(--text-secondary); }
	.form-field input[type="text"], .form-field input[type="number"], .form-field textarea {
		padding: 8px 10px; border-radius: 6px; border: 1px solid var(--border);
		background: var(--bg-input); color: var(--text-primary); font-size: 0.85rem;
		font-family: inherit;
	}
	.form-field textarea { resize: vertical; }
	.tools-checkboxes { display: flex; flex-wrap: wrap; gap: 8px; }
	.checkbox-label, .toggle-label {
		display: flex; align-items: center; gap: 4px;
		font-size: 0.8rem; color: var(--text-secondary); cursor: pointer;
	}
	.form-toggles { display: flex; gap: 16px; flex-wrap: wrap; margin-top: 8px; }
	.form-actions { display: flex; gap: 8px; margin-top: 16px; }
	.btn-sm { font-size: 0.8rem; padding: 6px 12px; }

	/* Org chart */
	.org-chart { padding: 0; height: calc(100vh - 100px); position: relative; display: flex; flex-direction: column; }
	.org-tree { display: flex; flex-direction: column; align-items: center; gap: 20px; }
	.org-node {
		display: flex; flex-direction: column; align-items: center; gap: 4px;
		padding: 16px 24px; background: var(--bg-secondary); border-radius: 10px;
		border: 1px solid var(--border); min-width: 140px; text-align: center;
	}
	.org-node.system { border-color: var(--accent); border-width: 2px; }
	.org-node.inactive { opacity: 0.5; }
	.org-node-avatar { font-size: 1.5rem; }
	.org-node-name { font-weight: 600; color: var(--text-primary); font-size: 0.85rem; }
	.org-node-role { color: var(--text-secondary); font-size: 0.75rem; }
	.org-children {
		display: flex; flex-wrap: wrap; gap: 12px; justify-content: center;
		padding-top: 12px; border-top: 2px solid var(--border); width: 100%; max-width: 900px;
	}

	.empty-state { color: var(--text-tertiary); font-size: 0.85rem; padding: 40px; text-align: center; }

	/* Agent state indicator */
	.agent-state-bar {
		color: var(--accent);
	}
	.agent-state-dot {
		display: inline-block;
		width: 6px;
		height: 6px;
		border-radius: 50%;
		background: var(--accent);
		animation: agentPulse 1.5s ease-in-out infinite;
	}
	@keyframes agentPulse {
		0%, 100% { opacity: 0.4; }
		50% { opacity: 1; }
	}

	/* Org chart toolbar */
	.org-chart-toolbar {
		display: flex;
		gap: var(--space-sm);
		padding: var(--space-sm) 0;
	}

	/* Org node detail panel */
	.org-node-panel {
		position: absolute;
		right: 16px;
		top: 60px;
		width: 320px;
		background: #1a1a2e;
		border: 1px solid #333;
		border-radius: 12px;
		padding: 20px;
		z-index: 10;
		box-shadow: 0 12px 40px rgba(0,0,0,0.4);
	}
	.org-node-panel .panel-header {
		display: flex;
		justify-content: space-between;
		align-items: center;
		margin-bottom: 12px;
		padding-bottom: 10px;
		border-bottom: 1px solid #333;
	}
	.org-node-panel .panel-header h4 {
		margin: 0;
		font-size: 1rem;
		color: #e1e1e6;
		font-weight: 600;
	}
	.org-node-panel .panel-body {
		display: flex;
		flex-direction: column;
		gap: 8px;
	}
	.org-node-panel .panel-body .text-muted {
		color: #9898a6;
	}
	.org-node-panel .panel-body .text-sm {
		font-size: 0.8rem;
	}
	.org-node-panel .panel-actions {
		display: flex; flex-direction: column; gap: 4px; margin-top: 10px;
		padding-top: 10px; border-top: 1px solid #333;
	}
	.panel-type-badge {
		font-size: 0.7rem;
		color: #6b6b7b;
	}
	.panel-meta {
		display: flex;
		gap: 6px;
		align-items: center;
	}
	.panel-stats {
		display: flex;
		gap: 12px;
		padding: 10px 0;
		border-top: 1px solid #333;
		border-bottom: 1px solid #333;
		margin: 4px 0;
	}
	.stat-item {
		display: flex;
		flex-direction: column;
		align-items: center;
		flex: 1;
	}
	.stat-value {
		font-size: 1rem;
		font-weight: 700;
		color: #e1e1e6;
	}
	.stat-label {
		font-size: 0.65rem;
		color: #6b6b7b;
		text-transform: uppercase;
		letter-spacing: 0.5px;
	}
	.panel-section {
		display: flex;
		flex-direction: column;
		gap: 8px;
		margin-top: 6px;
	}
	.panel-field {
		display: flex;
		flex-direction: column;
		gap: 2px;
	}
	.panel-input {
		background: #12121e;
		border: 1px solid #333;
		border-radius: 6px;
		padding: 6px 8px;
		color: #e1e1e6;
		font-size: 0.8rem;
		outline: none;
		font-family: inherit;
		resize: vertical;
	}
	.panel-input:focus { border-color: var(--accent, #e8622b); }
	.panel-select {
		background: #12121e; border: 1px solid #333; border-radius: 6px;
		padding: 6px 8px; color: #e1e1e6; font-size: 0.8rem; outline: none;
	}
	.panel-select:focus { border-color: var(--accent, #e8622b); }

	/* Role dialog */
	.role-dialog-overlay {
		position: fixed; inset: 0; background: rgba(0,0,0,0.7); z-index: 1000;
		display: flex; align-items: center; justify-content: center;
		backdrop-filter: blur(4px);
	}
	.role-dialog {
		background: #1a1a2e; border: 1px solid #333; border-radius: 12px;
		padding: 24px; width: 440px; max-width: 90vw; display: flex; flex-direction: column; gap: 16px;
		box-shadow: 0 20px 60px rgba(0,0,0,0.5);
	}
	.role-dialog-header {
		display: flex; justify-content: space-between; align-items: center;
	}
	.role-dialog-header h3 { margin: 0; color: #e1e1e6; font-size: 1.1rem; }
	.role-dialog-close {
		background: none; border: none; color: #9898a6; font-size: 1.5rem; cursor: pointer;
		padding: 0 4px; line-height: 1; border-radius: 4px;
	}
	.role-dialog-close:hover { color: #e1e1e6; background: rgba(255,255,255,0.1); }
	.role-dialog-form { display: flex; flex-direction: column; gap: 14px; }
	.role-dialog .form-group { display: flex; flex-direction: column; gap: 4px; }
	.role-dialog .form-group label { font-size: 0.8rem; color: #9898a6; font-weight: 500; }
	.role-dialog .form-group input,
	.role-dialog .form-group textarea,
	.role-dialog .form-group select {
		background: #12121e; border: 1px solid #333; border-radius: 6px;
		padding: 8px 10px; color: #e1e1e6; font-size: 0.85rem;
		outline: none; font-family: inherit;
	}
	.role-dialog .form-group input:focus,
	.role-dialog .form-group textarea:focus,
	.role-dialog .form-group select:focus {
		border-color: var(--accent, #e8622b);
	}
	.role-dialog .form-group input::placeholder,
	.role-dialog .form-group textarea::placeholder { color: #555; }
	.role-dialog .required { color: #ef4444; }
	.role-dialog-actions { display: flex; justify-content: flex-end; gap: 8px; padding-top: 4px; }

	/* Agent skills */
	.skill-list {
		display: flex;
		flex-direction: column;
		gap: var(--space-xs);
		margin-bottom: var(--space-sm);
	}
	.skill-item {
		display: flex;
		justify-content: space-between;
		align-items: flex-start;
		padding: var(--space-xs) var(--space-sm);
		background: var(--bg-tertiary);
		border-radius: 6px;
	}
	.skill-item-actions {
		display: flex;
		gap: var(--space-xs);
		flex-shrink: 0;
	}
	.skill-editor {
		display: flex;
		flex-direction: column;
		gap: var(--space-sm);
	}
	.skill-editor-header {
		display: flex;
		justify-content: space-between;
		align-items: center;
	}
	.skill-textarea {
		width: 100%;
		font-family: var(--font-mono);
		font-size: var(--text-sm);
		background: var(--bg-tertiary);
		color: var(--text-primary);
		border: 1px solid var(--border);
		border-radius: 6px;
		padding: var(--space-sm);
		resize: vertical;
	}

	/* Chat area wrapper (chat-main + member drawer) */
	.chat-area-wrapper {
		flex: 1;
		display: flex;
		min-width: 0;
	}

	/* Member drawer toggle button */
	.member-drawer-toggle {
		display: flex;
		align-items: center;
		gap: 4px;
		padding: 4px 8px;
		border-radius: var(--radius-md);
		font-size: var(--text-sm);
		color: var(--text-secondary);
		background: none;
		border: none;
		cursor: pointer;
		transition: background var(--transition-base), color var(--transition-base);
	}
	.member-drawer-toggle:hover, .member-drawer-toggle.active {
		background: var(--bg-tertiary);
		color: var(--text-primary);
	}

	/* Member drawer */
	.member-drawer {
		width: 240px;
		min-width: 240px;
		background: var(--bg-surface);
		border-left: 1px solid var(--border-subtle);
		display: flex;
		flex-direction: column;
		overflow-y: auto;
	}
	.drawer-header {
		padding: var(--space-md) var(--space-lg);
		border-bottom: 1px solid var(--border-subtle);
		display: flex;
		align-items: center;
		justify-content: space-between;
	}
	.drawer-header h3 {
		font-size: var(--text-sm);
		font-weight: 700;
		text-transform: uppercase;
		letter-spacing: 0.05em;
		color: var(--text-secondary);
	}
	.drawer-close {
		background: none;
		border: none;
		color: var(--text-muted);
		cursor: pointer;
		padding: 2px;
		border-radius: var(--radius-sm);
	}
	.drawer-close:hover {
		color: var(--text-primary);
		background: var(--bg-tertiary);
	}
	.drawer-section {
		padding: var(--space-sm) var(--space-lg);
	}
	.drawer-section-label {
		font-size: var(--text-xs);
		font-weight: 600;
		text-transform: uppercase;
		letter-spacing: 0.05em;
		color: var(--text-muted);
		margin-bottom: var(--space-sm);
	}
	.drawer-member {
		display: flex;
		align-items: center;
		gap: var(--space-sm);
		padding: 4px 0;
	}
	.drawer-member.offline {
		opacity: 0.5;
	}
	.drawer-avatar {
		position: relative;
		width: 28px;
		height: 28px;
		border-radius: 50%;
		background: var(--bg-tertiary);
		color: var(--text-primary);
		font-size: 12px;
		font-weight: 600;
		display: flex;
		align-items: center;
		justify-content: center;
		flex-shrink: 0;
	}
	.drawer-avatar .presence-dot.online {
		position: absolute;
		bottom: -1px;
		right: -1px;
		width: 8px;
		height: 8px;
		border-radius: 50%;
		background: #22c55e;
		border: 2px solid var(--bg-surface);
	}
	.drawer-member-info {
		display: flex;
		flex-direction: column;
		min-width: 0;
	}
	.drawer-member-name {
		font-size: var(--text-sm);
		font-weight: 500;
		color: var(--text-primary);
		white-space: nowrap;
		overflow: hidden;
		text-overflow: ellipsis;
	}
	.drawer-member-role {
		font-size: var(--text-xs);
		color: var(--text-muted);
		text-transform: capitalize;
	}

	/* @-mention autocomplete popup */
	.mention-popup {
		position: relative;
		margin: 0 var(--space-xl);
		background: var(--bg-surface);
		border: 1px solid var(--border-default);
		border-radius: var(--radius-md);
		box-shadow: 0 -4px 12px rgba(0,0,0,0.15);
		max-height: 240px;
		overflow-y: auto;
		z-index: 10;
	}
	.mention-item {
		display: flex;
		align-items: center;
		justify-content: space-between;
		width: 100%;
		padding: var(--space-sm) var(--space-md);
		text-align: left;
		background: none;
		border: none;
		color: var(--text-primary);
		cursor: pointer;
		font-size: var(--text-sm);
	}
	.mention-item:hover, .mention-item.active {
		background: var(--accent-glow);
	}
	.mention-name {
		font-weight: 500;
	}
	.mention-role {
		color: var(--text-muted);
		font-size: var(--text-xs);
		text-transform: capitalize;
	}

	/* Online members in channel header */
	.online-members {
		display: flex;
		align-items: center;
		gap: 2px;
		margin-right: var(--space-sm);
	}
	.online-avatar {
		position: relative;
		width: 24px;
		height: 24px;
		border-radius: 50%;
		background: var(--bg-tertiary);
		color: var(--text-primary);
		font-size: 11px;
		font-weight: 600;
		display: flex;
		align-items: center;
		justify-content: center;
		border: 2px solid var(--bg-primary);
		margin-left: -4px;
	}
	.online-avatar:first-child {
		margin-left: 0;
	}
	.presence-dot {
		position: absolute;
		bottom: -1px;
		right: -1px;
		width: 8px;
		height: 8px;
		border-radius: 50%;
		background: #22c55e;
		border: 2px solid var(--bg-primary);
	}
	.online-overflow {
		font-size: var(--text-xs);
		color: var(--text-muted);
		margin-left: var(--space-xs);
	}

	/* Modal dialog base (shared) */
	.modal-dialog {
		background: var(--bg-surface);
		border: 1px solid var(--border-subtle);
		border-radius: var(--radius-lg, 12px);
		max-width: 480px;
		width: 90vw;
		max-height: 80vh;
		display: flex;
		flex-direction: column;
		overflow: hidden;
	}

	/* Agent Library Modal */
	.agent-library-modal {
		max-width: 800px;
		width: 90vw;
		max-height: 80vh;
		display: flex;
		flex-direction: column;
		overflow: hidden;
		padding: 0;
	}
	.agent-lib-header {
		display: flex;
		align-items: flex-start;
		justify-content: space-between;
		padding: 20px 24px 12px;
	}
	.agent-lib-header h3 {
		font-size: 1.1rem;
		font-weight: 700;
		color: var(--text-primary);
		margin: 0;
	}
	.agent-lib-subtitle {
		font-size: 0.8rem;
		color: var(--text-tertiary);
		margin: 2px 0 0;
	}
	.agent-lib-header-actions {
		display: flex;
		align-items: center;
		gap: 8px;
	}
	.agent-lib-search {
		display: flex;
		align-items: center;
		gap: 8px;
		margin: 0 24px 8px;
		padding: 8px 12px;
		background: var(--bg-input);
		border: 1px solid var(--border);
		border-radius: 8px;
	}
	.agent-lib-search svg { color: var(--text-tertiary); flex-shrink: 0; }
	.agent-lib-search input {
		flex: 1;
		background: none;
		border: none;
		outline: none;
		color: var(--text-primary);
		font-size: 0.85rem;
	}
	.agent-lib-tabs {
		display: flex;
		flex-wrap: wrap;
		gap: 6px;
		padding: 0 24px 12px;
		border-bottom: 1px solid var(--border);
	}
	.agent-lib-tab {
		padding: 4px 12px;
		border-radius: 20px;
		border: 1px solid var(--border);
		background: none;
		color: var(--text-secondary);
		font-size: 0.75rem;
		cursor: pointer;
		transition: all 0.15s;
	}
	.agent-lib-tab:hover { background: var(--bg-secondary); }
	.agent-lib-tab.active {
		background: var(--accent);
		color: white;
		border-color: var(--accent);
	}
	.agent-lib-body {
		flex: 1;
		overflow-y: auto;
		padding: 16px 24px 24px;
	}
	.agent-lib-section { margin-bottom: 24px; }
	.agent-lib-section h4 {
		font-size: 0.85rem;
		font-weight: 600;
		color: var(--text-primary);
		margin: 0 0 2px;
	}
	.agent-lib-section-desc {
		font-size: 0.75rem;
		color: var(--text-tertiary);
		margin: 0 0 12px;
	}
	.agent-lib-grid {
		display: grid;
		grid-template-columns: repeat(auto-fill, minmax(220px, 1fr));
		gap: 12px;
	}
	.agent-lib-card {
		background: var(--bg-secondary);
		border: 1px solid var(--border);
		border-radius: 10px;
		overflow: hidden;
		display: flex;
		flex-direction: column;
		gap: 8px;
		transition: border-color 0.15s;
	}
	.agent-lib-card:hover { border-color: var(--text-tertiary); }
	.agent-lib-card-top-bar {
		height: 3px;
	}
	.agent-lib-card-top-bar.builtin { background: var(--accent); }
	.agent-lib-card-top-bar.user { background: var(--blue, #3b82f6); }
	.agent-lib-card-top-bar.template { background: var(--purple, #a855f7); }
	.agent-lib-card-header {
		display: flex;
		align-items: flex-start;
		gap: 10px;
		padding: 12px 14px 0;
	}
	.agent-lib-card-avatar { font-size: 1.5rem; line-height: 1; }
	.agent-lib-card-name {
		font-weight: 600;
		font-size: 0.85rem;
		color: var(--text-primary);
	}
	.agent-lib-card-desc {
		font-size: 0.72rem;
		color: var(--text-tertiary);
		display: -webkit-box;
		-webkit-line-clamp: 2;
		-webkit-box-orient: vertical;
		overflow: hidden;
		line-height: 1.4;
	}
	.agent-lib-card-tools {
		display: flex;
		flex-wrap: wrap;
		gap: 4px;
		padding: 0 14px;
	}
	.agent-lib-card-footer {
		display: flex;
		align-items: center;
		justify-content: space-between;
		padding: 8px 14px;
		border-top: 1px solid var(--border);
		margin-top: auto;
	}
	.agent-lib-card-actions { display: flex; gap: 4px; }
	.agent-lib-badge {
		font-size: 0.6rem;
		padding: 2px 8px;
		border-radius: 10px;
		font-weight: 600;
		text-transform: uppercase;
	}
	.agent-lib-badge.builtin { background: var(--accent); color: white; }
	.agent-lib-badge.custom { background: var(--blue, #3b82f6); color: white; }
	.agent-lib-badge.template { background: var(--purple, #a855f7); color: white; }

	/* Responsive */
	@media (max-width: 640px) {
		.sidebar { width: 220px; min-width: 220px; }
	}
</style>
