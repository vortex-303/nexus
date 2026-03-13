<script lang="ts">
	import { page } from '$app/state';
	import { goto } from '$app/navigation';
	import { onMount, onDestroy } from 'svelte';
	import { getCurrentUser, listAgents, listAgentTemplates, getOrgChart, updateOrgPosition, updateMemberProfile, createOrgRole, updateOrgRole, deleteOrgRole, fillOrgRole, listOrgRoles, updateAgent, deleteAgent, createAgent, createAgentFromTemplate, generateAgentConfig, editAgentWithAI, listAgentSkills, getAgentSkill, updateAgentSkill, deleteAgentSkill, getMember, updateMemberRole, kickMember, updateMemberPermission, listMCPServers } from '$lib/api';
	import { connect, disconnect, onMessage } from '$lib/ws';
	import { channels, members } from '$lib/stores/workspace';
	import OrgChart from '$lib/components/OrgChart.svelte';

	const ROLES = ['admin', 'member', 'guest'];
	const ROLE_LABELS: Record<string, string> = {
		admin: 'Admin', member: 'Member', guest: 'Guest'
	};
	const BUILTIN_AGENT_TOOLS = ['create_task', 'list_tasks', 'search_messages', 'create_document', 'search_knowledge'];
	const KNOWN_AGENT_MODELS = ['', 'google/gemini-3.1-pro-preview', 'google/gemini-3-flash-preview', 'google/gemini-3.1-flash-lite-preview', 'google/gemini-3.1-flash-image-preview', 'google/gemini-3-pro-image-preview', 'google/gemini-2.5-flash', 'google/gemini-2.5-pro', 'nexus/free-auto'];

	let slug = $derived(page.params.slug);
	let currentUser = $state(getCurrentUser());
	let isAdmin = $derived(currentUser?.role === 'admin');

	// Tab state
	let teamTab = $state<'members' | 'agents' | 'orgchart'>('orgchart');

	// Agents state
	let agentsList = $state<any[]>([]);
	let agentTemplates = $state<any[]>([]);
	let showAgentForm = $state(false);
	let showTemplateGallery = $state(false);
	let editingAgent = $state<any>(null);
	let agentGenerating = $state(false);
	let agentEditingWithAI = $state(false);
	let showAIEditInput = $state(false);
	let aiEditInstruction = $state('');
	let agentSaving = $state(false);

	// Org chart state
	let orgChartNodes = $state<any[]>([]);
	let orgRoles = $state<any[]>([]);
	let selectedNodeForPanel = $state<any>(null);
	let chartFit = $state<(() => void) | null>(null);
	let chartExpandAll = $state<(() => void) | null>(null);
	let chartCollapseAll = $state<(() => void) | null>(null);
	let chartExpanded = $state(true);

	// Role dialog state
	let showRoleDialog = $state(false);
	let roleSaving = $state(false);
	let roleForm = $state({ title: '', description: '', department: '', reports_to: '', preset: '' });
	let pendingRoleFill = $state<string | null>(null);

	// Agent skills state
	let agentSkillsList = $state<any[]>([]);
	let showSkillEditor = $state(false);
	let editingSkillFile = $state('');
	let skillEditorContent = $state('');

	// MCP servers for tool list
	let mcpServers = $state<any[]>([]);

	// Member management
	let selectedMember = $state<any>(null);
	let memberDetail = $state<any>(null);

	let allRoles = $derived([...ROLES, ...orgRoles.map((r: any) => r.title.toLowerCase().replace(/\s+/g, '_')).filter((r: string) => !ROLES.includes(r))]);
	let publicChannels = $derived($channels.filter(ch => ch.type !== 'dm' && ch.type !== 'group'));
	let allAgentTools = $derived([...BUILTIN_AGENT_TOOLS, ...mcpServers.flatMap((s: any) => (s.tools || []).map((t: any) => t.qual_name))]);

	// Agent form fields
	let agentForm = $state({
		name: '', description: '', avatar: '', role: '', goal: '', backstory: '', instructions: '',
		constraints: '', escalation_prompt: '', model: '', image_model: '', temperature: 0.7, max_tokens: 2048,
		tools: [] as string[], channels: [] as string[], knowledge_access: false, memory_access: false,
		can_delegate: false, max_iterations: 5, trigger_type: 'mention',
		cooldown_seconds: 30, follow_ttl_minutes: 10, follow_max_messages: 20,
		channel_modes: {} as Record<string, string>,
		respond_to_agents: false, auto_follow_threads: true, respond_in_threads: true
	});

	let unsubWS: (() => void) | null = null;

	onMount(async () => {
		const ws = connect(slug);
		unsubWS = onMessage((msg: any) => {
			// Handle member updates from WS if needed
		});

		// Load initial data
		await Promise.all([
			loadOrgChart(),
			loadAgents(),
			loadOrgRoles(),
			loadMCPServers(),
		]);
	});

	onDestroy(() => {
		unsubWS?.();
	});

	async function loadMCPServers() {
		try { mcpServers = await listMCPServers(slug); } catch { mcpServers = []; }
	}

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

	async function loadOrgRoles() {
		try { orgRoles = await listOrgRoles(slug); } catch { orgRoles = []; }
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
		roleForm = { title: '', description: '', department: '', reports_to: '', preset: '' };
		showRoleDialog = true;
	}

	function handleRolePresetChange(value: string) {
		roleForm.preset = value;
		if (value && value !== '_custom') {
			roleForm.title = ROLE_LABELS[value] || value;
		} else {
			roleForm.title = '';
		}
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
			constraints: '', escalation_prompt: '', model: '', image_model: '', temperature: 0.7, max_tokens: 2048,
			tools: [], channels: [], knowledge_access: false, memory_access: false,
			can_delegate: false, max_iterations: 5, trigger_type: 'mention',
			cooldown_seconds: 30, follow_ttl_minutes: 10, follow_max_messages: 20,
			channel_modes: {},
			respond_to_agents: false, auto_follow_threads: true, respond_in_threads: true
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
		const bc = agent.behavior_config || {};
		agentForm = {
			name: agent.name, description: agent.description, avatar: agent.avatar,
			role: agent.role, goal: agent.goal, backstory: agent.backstory,
			instructions: agent.instructions, constraints: agent.constraints,
			escalation_prompt: agent.escalation_prompt, model: agent.model, image_model: agent.image_model || '',
			temperature: agent.temperature, max_tokens: agent.max_tokens,
			tools: JSON.parse(JSON.stringify(agent.tools || '[]')),
			channels: JSON.parse(JSON.stringify(agent.channels || '[]')),
			knowledge_access: agent.knowledge_access, memory_access: agent.memory_access,
			can_delegate: agent.can_delegate, max_iterations: agent.max_iterations,
			trigger_type: agent.trigger_type || 'mention',
			cooldown_seconds: bc.cooldown_seconds || 30,
			follow_ttl_minutes: bc.follow_ttl_minutes || 10,
			follow_max_messages: bc.follow_max_messages || 20,
			channel_modes: bc.channel_modes || {},
			respond_to_agents: bc.respond_to_agents ?? false,
			auto_follow_threads: bc.auto_follow_threads ?? true,
			respond_in_threads: bc.respond_in_threads ?? true
		};
		if (typeof agentForm.tools === 'string') agentForm.tools = JSON.parse(agentForm.tools);
		if (typeof agentForm.channels === 'string') agentForm.channels = JSON.parse(agentForm.channels);
		showAgentForm = true;
		showTemplateGallery = false;
		showSkillEditor = false;
		if (!agent.is_system) loadAgentSkills(agent.id);
	}

	function coerceToString(val: any): string {
		if (Array.isArray(val)) return val.join('\n');
		if (typeof val === 'object' && val !== null) return JSON.stringify(val);
		return String(val || '');
	}

	async function handleSaveAgent() {
		if (!agentForm.name.trim()) { alert('Name is required'); return; }
		agentSaving = true;
		const behaviorConfig: Record<string, any> = {
			cooldown_seconds: agentForm.cooldown_seconds || 30,
			follow_ttl_minutes: agentForm.follow_ttl_minutes || 0,
			follow_max_messages: agentForm.follow_max_messages || 20,
			channel_modes: agentForm.channel_modes || {},
			respond_to_agents: agentForm.respond_to_agents ?? false,
			auto_follow_threads: agentForm.auto_follow_threads ?? true,
			respond_in_threads: agentForm.respond_in_threads ?? true
		};
		const { follow_ttl_minutes, follow_max_messages, cooldown_seconds, channel_modes, respond_to_agents, auto_follow_threads, respond_in_threads, ...rest } = agentForm;
		const payload = {
			...rest,
			instructions: coerceToString(agentForm.instructions),
			constraints: coerceToString(agentForm.constraints),
			backstory: coerceToString(agentForm.backstory),
			escalation_prompt: coerceToString(agentForm.escalation_prompt),
			behavior_config: behaviorConfig,
		};
		try {
			let newAgent;
			if (editingAgent) {
				await updateAgent(slug, editingAgent.id, payload);
			} else {
				newAgent = await createAgent(slug, payload);
			}
			showAgentForm = false;
			await loadAgents();

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
				role: config.role || '', goal: config.goal || '', backstory: coerceToString(config.backstory),
				instructions: coerceToString(config.instructions), constraints: coerceToString(config.constraints),
				escalation_prompt: coerceToString(config.escalation_prompt), model: '', temperature: 0.7, max_tokens: 2048,
				tools: Array.isArray(config.tools) ? config.tools : [], channels: [],
				knowledge_access: !!config.knowledge_access,
				memory_access: !!config.memory_access,
				can_delegate: false, max_iterations: 5, trigger_type: 'mention',
				cooldown_seconds: 30, follow_ttl_minutes: 10, follow_max_messages: 20,
				channel_modes: {},
				respond_to_agents: false, auto_follow_threads: true, respond_in_threads: true
			};
			editingAgent = null;
			showAgentForm = true;
		} catch (e: any) {
			alert(e.message);
		} finally {
			agentGenerating = false;
		}
	}

	async function handleEditWithAI() {
		if (!aiEditInstruction.trim()) return;
		agentEditingWithAI = true;
		try {
			const current = {
				name: agentForm.name, description: agentForm.description, avatar: agentForm.avatar,
				role: agentForm.role, goal: agentForm.goal, backstory: agentForm.backstory,
				instructions: agentForm.instructions, constraints: agentForm.constraints,
				escalation_prompt: agentForm.escalation_prompt, tools: agentForm.tools,
				knowledge_access: agentForm.knowledge_access, memory_access: agentForm.memory_access,
				can_delegate: agentForm.can_delegate, temperature: agentForm.temperature,
				max_iterations: agentForm.max_iterations, trigger_type: agentForm.trigger_type,
			};
			const config = await editAgentWithAI(slug, aiEditInstruction, current);
			agentForm = {
				...agentForm,
				name: config.name ?? agentForm.name,
				description: config.description ?? agentForm.description,
				avatar: config.avatar ?? agentForm.avatar,
				role: config.role ?? agentForm.role,
				goal: config.goal ?? agentForm.goal,
				backstory: coerceToString(config.backstory ?? agentForm.backstory),
				instructions: coerceToString(config.instructions ?? agentForm.instructions),
				constraints: coerceToString(config.constraints ?? agentForm.constraints),
				escalation_prompt: coerceToString(config.escalation_prompt ?? agentForm.escalation_prompt),
				tools: Array.isArray(config.tools) ? config.tools : agentForm.tools,
				knowledge_access: config.knowledge_access ?? agentForm.knowledge_access,
				memory_access: config.memory_access ?? agentForm.memory_access,
				can_delegate: config.can_delegate ?? agentForm.can_delegate,
				temperature: config.temperature ?? agentForm.temperature,
				max_iterations: config.max_iterations ?? agentForm.max_iterations,
				trigger_type: config.trigger_type ?? agentForm.trigger_type,
			};
			aiEditInstruction = '';
			showAIEditInput = false;
		} catch (e: any) {
			alert(e.message);
		} finally {
			agentEditingWithAI = false;
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
		if (tab === 'orgchart') { await loadOrgChart(); if (orgRoles.length === 0) loadOrgRoles(); }
	}

	async function handleUpdateProfile(memberId: string, field: string, value: string) {
		try {
			await updateMemberProfile(slug, memberId, { [field]: value });
		} catch (e: any) {
			alert(e.message);
		}
	}

	async function handleManageMember(member: any) {
		selectedMember = member;
		try {
			memberDetail = await getMember(slug, member.id);
			if (orgRoles.length === 0) loadOrgRoles();
		} catch (err: any) {
			console.error('[manage-member] failed to load member:', member.id, err);
			memberDetail = null;
		}
	}
</script>

<div class="team-page">
	<header class="team-header">
		<div class="team-header-left">
			<button class="back-btn" onclick={() => goto(`/w/${slug}`)}>
				<svg width="16" height="16" viewBox="0 0 16 16" fill="none"><path d="M10 12L6 8L10 4" stroke="currentColor" stroke-width="1.5" stroke-linecap="round" stroke-linejoin="round"/></svg>
			</button>
			<h1>Team</h1>
		</div>
		<div class="team-tabs">
			<button class="tab-btn" class:active={teamTab === 'orgchart'} onclick={() => handleTeamTabChange('orgchart')}>
				<svg width="14" height="14" viewBox="0 0 14 14" fill="none" style="flex-shrink:0;opacity:0.5">
					<rect x="4.5" y="1" width="5" height="3" rx="0.75" stroke="currentColor" stroke-width="1.2"/>
					<rect x="0.5" y="9" width="4" height="3" rx="0.75" stroke="currentColor" stroke-width="1.2"/>
					<rect x="9.5" y="9" width="4" height="3" rx="0.75" stroke="currentColor" stroke-width="1.2"/>
					<path d="M7 4v2.5M7 6.5H2.5V9M7 6.5h4.5V9" stroke="currentColor" stroke-width="1.2"/>
				</svg>
				Org Chart
			</button>
			<button class="tab-btn" class:active={teamTab === 'members'} onclick={() => handleTeamTabChange('members')}>
				<svg width="14" height="14" viewBox="0 0 14 14" fill="none" style="flex-shrink:0;opacity:0.5">
					<circle cx="7" cy="4" r="2.5" stroke="currentColor" stroke-width="1.2"/>
					<path d="M2 12.5c0-2.5 2-4 5-4s5 1.5 5 4" stroke="currentColor" stroke-width="1.2" stroke-linecap="round"/>
				</svg>
				Members
			</button>
			<button class="tab-btn" class:active={teamTab === 'agents'} onclick={() => handleTeamTabChange('agents')}>
				<svg width="14" height="14" viewBox="0 0 14 14" fill="none" style="flex-shrink:0;opacity:0.5">
					<rect x="3" y="2" width="8" height="6" rx="1" stroke="currentColor" stroke-width="1.2"/>
					<circle cx="5.5" cy="5" r="0.75" fill="currentColor"/>
					<circle cx="8.5" cy="5" r="0.75" fill="currentColor"/>
					<path d="M5 10v2M9 10v2" stroke="currentColor" stroke-width="1.2" stroke-linecap="round"/>
				</svg>
				Agents
			</button>
		</div>
	</header>

	<div class="team-content">
		{#if teamTab === 'members'}
		<div class="agents-toolbar">
			{#if isAdmin}
				<button class="btn btn-primary" onclick={handleAddOrgRole}>Add Role</button>
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
							<button class="btn btn-ghost btn-xs" onclick={() => handleManageMember(member)}>Manage</button>
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
								{#if (JSON.parse(typeof agent.channels === 'string' ? agent.channels : '[]') || []).length > 0}
									<span class="agent-badge channel-count">{(JSON.parse(typeof agent.channels === 'string' ? agent.channels : '[]') || []).length} ch</span>
								{/if}
								</div>
							</div>
							<div class="agent-card-role">{agent.role}</div>
							{#if agent.goal}<div class="agent-card-goal">{agent.goal}</div>{/if}
							<div class="agent-card-tools">
								{#each JSON.parse(typeof agent.tools === 'string' ? agent.tools : JSON.stringify(agent.tools || [])) as tool}
									<span class="tool-chip">{tool}</span>
								{/each}
							</div>
							{#if isAdmin}
								<div class="agent-card-actions">
									<button class="btn btn-ghost btn-xs" onclick={() => openEditAgent(agent)}>Edit</button>
									{#if !agent.is_system}
									<button class="btn btn-ghost btn-xs" onclick={() => handleToggleAgent(agent)}>
										{agent.is_active ? 'Pause' : 'Activate'}
									</button>
									<button class="btn btn-ghost btn-xs btn-danger" onclick={() => handleDeleteAgent(agent.id)}>Delete</button>
									{/if}
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
						<div style="display:flex;gap:8px;align-items:center">
							{#if editingAgent && !editingAgent.is_system}
								<button class="btn btn-ghost" onclick={() => { showAIEditInput = !showAIEditInput; aiEditInstruction = ''; }}>
									{showAIEditInput ? 'Cancel AI' : 'Edit with AI'}
								</button>
							{/if}
							<button class="btn btn-ghost" onclick={() => { showAgentForm = false; editingAgent = null; showAIEditInput = false; }}>Cancel</button>
						</div>
					</div>
					{#if showAIEditInput}
					<div class="ai-edit-bar">
						<input
							type="text"
							class="ai-edit-input"
							placeholder="Describe what to change... e.g. 'make it more formal' or 'add web search tool'"
							bind:value={aiEditInstruction}
							onkeydown={(e) => { if (e.key === 'Enter' && !agentEditingWithAI) handleEditWithAI(); }}
						/>
						<button class="btn btn-primary btn-sm" onclick={handleEditWithAI} disabled={agentEditingWithAI || !aiEditInstruction.trim()}>
							{agentEditingWithAI ? 'Applying...' : 'Apply'}
						</button>
					</div>
					{/if}

					{#if !editingAgent?.is_system}
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
							<select value={KNOWN_AGENT_MODELS.includes(agentForm.model) ? agentForm.model : '__custom__'} onchange={(e) => { const v = (e.target as HTMLSelectElement).value; agentForm.model = v === '__custom__' ? '' : v; }}>
								<option value="">Workspace default</option>
								<optgroup label="Gemini 3">
									<option value="google/gemini-3.1-pro-preview">Gemini 3.1 Pro</option>
									<option value="google/gemini-3-flash-preview">Gemini 3 Flash</option>
									<option value="google/gemini-3.1-flash-lite-preview">Gemini 3.1 Flash Lite</option>
								</optgroup>
								<optgroup label="Gemini 3 Image">
									<option value="google/gemini-3.1-flash-image-preview">Gemini 3.1 Flash Image</option>
									<option value="google/gemini-3-pro-image-preview">Gemini 3 Pro Image</option>
								</optgroup>
								<optgroup label="Gemini 2.5">
									<option value="google/gemini-2.5-flash">Gemini 2.5 Flash</option>
									<option value="google/gemini-2.5-pro">Gemini 2.5 Pro</option>
								</optgroup>
								<optgroup label="OpenRouter">
									<option value="nexus/free-auto">Free Auto</option>
									<option value="__custom__">Custom model ID...</option>
								</optgroup>
							</select>
							{#if !KNOWN_AGENT_MODELS.includes(agentForm.model)}
								<input type="text" bind:value={agentForm.model} placeholder="e.g. anthropic/claude-3.5-sonnet" style="margin-top: 0.25rem" />
							{/if}
						</label>
						{#if agentForm.tools.includes('generate_image')}
							<label class="form-field">
								<span>Image Model (empty = workspace default)</span>
								<select bind:value={agentForm.image_model}>
									<option value="">Workspace default</option>
									<option value="gemini-3.1-flash-image-preview">Gemini 3.1 Flash Image</option>
									<option value="gemini-3-pro-image-preview">Gemini 3 Pro Image</option>
									<option value="gemini-2.5-flash-image">Gemini 2.5 Flash Image</option>
								</select>
							</label>
						{/if}
						<label class="form-field">
							<span>Temperature: {agentForm.temperature}</span>
							<input type="range" min="0" max="2" step="0.1" bind:value={agentForm.temperature} />
						</label>
						<div class="form-field">
							<span>Tools</span>
							<div class="tools-checkboxes">
								{#each allAgentTools as tool}
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
					</div>
					{/if}

					{#if editingAgent?.is_system}
					<div class="form-section">
						<h4>Model</h4>
						<label class="form-field">
							<span>Model (empty = workspace default)</span>
							<select value={KNOWN_AGENT_MODELS.includes(agentForm.model) ? agentForm.model : '__custom__'} onchange={(e) => { const v = (e.target as HTMLSelectElement).value; agentForm.model = v === '__custom__' ? '' : v; }}>
								<option value="">Workspace default</option>
								<optgroup label="Gemini 3">
									<option value="google/gemini-3.1-pro-preview">Gemini 3.1 Pro</option>
									<option value="google/gemini-3-flash-preview">Gemini 3 Flash</option>
									<option value="google/gemini-3.1-flash-lite-preview">Gemini 3.1 Flash Lite</option>
								</optgroup>
								<optgroup label="Gemini 3 Image">
									<option value="google/gemini-3.1-flash-image-preview">Gemini 3.1 Flash Image</option>
									<option value="google/gemini-3-pro-image-preview">Gemini 3 Pro Image</option>
								</optgroup>
								<optgroup label="Gemini 2.5">
									<option value="google/gemini-2.5-flash">Gemini 2.5 Flash</option>
									<option value="google/gemini-2.5-pro">Gemini 2.5 Pro</option>
								</optgroup>
								<optgroup label="OpenRouter">
									<option value="nexus/free-auto">Free Auto</option>
									<option value="__custom__">Custom model ID...</option>
								</optgroup>
							</select>
							{#if !KNOWN_AGENT_MODELS.includes(agentForm.model)}
								<input type="text" bind:value={agentForm.model} placeholder="e.g. anthropic/claude-3.5-sonnet" style="margin-top: 0.25rem" />
							{/if}
						</label>
						{#if agentForm.tools.includes('generate_image')}
							<label class="form-field">
								<span>Image Model (empty = workspace default)</span>
								<select bind:value={agentForm.image_model}>
									<option value="">Workspace default</option>
									<option value="gemini-3.1-flash-image-preview">Gemini 3.1 Flash Image</option>
									<option value="gemini-3-pro-image-preview">Gemini 3 Pro Image</option>
									<option value="gemini-2.5-flash-image">Gemini 2.5 Flash Image</option>
								</select>
							</label>
						{/if}
					</div>
					{/if}

					<div class="form-section">
						<h4>Response & Behavior</h4>
						<span class="form-hint">Agent always responds to @mentions and DMs. Assign channels above to auto-respond there.</span>
						<div class="form-row" style="display:flex;gap:12px;margin-top:8px">
							<label class="form-field" style="flex:1">
								<span>Cooldown (seconds)</span>
								<input type="number" bind:value={agentForm.cooldown_seconds} min="0" max="600" style="width:80px" />
								<span class="form-hint">Min seconds between auto-responses in a channel. 0 = no cooldown.</span>
							</label>
							<label class="form-field" style="flex:1">
								<span>Max follow-up messages</span>
								<input type="number" bind:value={agentForm.follow_max_messages} min="1" max="100" style="width:80px" />
							</label>
						</div>
						<div style="display:flex;flex-direction:column;gap:6px;margin-top:8px">
							<label class="toggle-label">
								<input type="checkbox" bind:checked={agentForm.respond_to_agents} /> Respond to other agents
								<span class="form-hint">React to messages sent by other agents in the channel</span>
							</label>
							<label class="toggle-label">
								<input type="checkbox" bind:checked={agentForm.auto_follow_threads} /> Auto-follow threads
								<span class="form-hint">Respond when users reply to this agent's messages</span>
							</label>
							<label class="toggle-label">
								<input type="checkbox" bind:checked={agentForm.respond_in_threads} /> Respond in threads
								<span class="form-hint">Participate in thread conversations</span>
							</label>
						</div>
						{#if (agentForm.channels || []).length > 0}
						<div class="form-field">
							<span>Per-Channel Overrides</span>
							<div class="channel-overrides">
								{#each agentForm.channels || [] as chId}
									{#if publicChannels.find((c: any) => c.id === chId)}
									{@const ch = publicChannels.find((c: any) => c.id === chId)}
									<div class="channel-override-row">
										<span class="channel-override-name">#{ch.name}</span>
										<select value={agentForm.channel_modes[ch.id] || 'active'} onchange={(e) => {
											const val = (e.target as HTMLSelectElement).value;
											if (val === 'active') {
												const { [ch.id]: _, ...rest } = agentForm.channel_modes;
												agentForm.channel_modes = rest;
											} else {
												agentForm.channel_modes = { ...agentForm.channel_modes, [ch.id]: val };
											}
										}}>
											<option value="active">Active</option>
											<option value="silent">Silent</option>
										</select>
									</div>
									{/if}
								{/each}
							</div>
						</div>
						{/if}
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
					<button class="btn btn-primary" onclick={handleAddOrgRole}>Add Role</button>
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
								{#each allRoles as r}
									<option value={r}>{r.replace(/_/g, ' ')}</option>
								{/each}
							</select>
							<button class="btn btn-danger btn-sm" style="margin-top:8px" onclick={() => { if (confirm('Remove this member?')) { kickMember(slug, selectedNodeForPanel.id); selectedNodeForPanel = null; loadOrgChart(); } }}>Remove from Workspace</button>
						</div>

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
</div>

{#if showRoleDialog}
<!-- svelte-ignore a11y_no_static_element_interactions -->
<div class="modal-overlay" onclick={() => showRoleDialog = false}>
	<!-- svelte-ignore a11y_no_static_element_interactions -->
	<div class="modal-dialog" onclick={(e) => e.stopPropagation()} style="max-width: 440px">
		<div class="modal-header">
			<h3>Add Role</h3>
			<button class="modal-close" onclick={() => showRoleDialog = false}>&times;</button>
		</div>
		<div class="modal-body">
			<div class="role-dialog-fields">
				<div class="form-group">
					<label>Role</label>
					<select value={roleForm.preset} onchange={(e) => handleRolePresetChange((e.target as HTMLSelectElement).value)}>
						<option value="">-- Select a role --</option>
						{#each ROLES.filter(r => r !== 'custom') as role}
							<option value={role}>{ROLE_LABELS[role] || role}</option>
						{/each}
						<option value="_custom">Custom...</option>
					</select>
				</div>
				{#if roleForm.preset === '_custom'}
				<div class="form-group">
					<label>Title <span class="required">*</span></label>
					<input type="text" bind:value={roleForm.title} placeholder="Marketing Manager" />
				</div>
				{/if}
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
</div>
{/if}

<style>
	.team-page {
		display: flex;
		flex-direction: column;
		height: 100vh;
		width: 100%;
		background: var(--bg-primary);
		color: var(--text-primary);
	}

	.team-header {
		display: flex;
		align-items: center;
		justify-content: space-between;
		padding: 12px 20px;
		border-bottom: 1px solid var(--border);
		flex-shrink: 0;
	}

	.team-header-left {
		display: flex;
		align-items: center;
		gap: 12px;
	}

	.team-header-left h1 {
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
		border-radius: 4px;
	}
	.back-btn:hover { color: var(--text-primary); background: var(--bg-raised); }

	.team-tabs {
		display: flex;
		gap: 4px;
	}

	.tab-btn {
		display: flex;
		align-items: center;
		gap: 6px;
		padding: 6px 12px;
		border-radius: 6px;
		border: 1px solid transparent;
		background: none;
		color: var(--text-secondary);
		font-size: 0.8rem;
		cursor: pointer;
		transition: all 0.15s;
	}
	.tab-btn:hover { background: var(--bg-secondary); color: var(--text-primary); }
	.tab-btn.active { background: var(--bg-secondary); color: var(--text-primary); border-color: var(--border); }

	.team-content {
		flex: 1;
		overflow-y: auto;
		padding: 20px;
	}

	/* Member avatar */
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
	.agent-badge.channel-count { background: var(--blue, #3b82f6); color: white; }
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
	.ai-edit-bar {
		display: flex; gap: 8px; align-items: center;
		padding: 10px 12px; margin-bottom: 16px;
		background: rgba(249, 115, 22, 0.06);
		border: 1px solid rgba(249, 115, 22, 0.2);
		border-radius: var(--radius-md);
	}
	.ai-edit-input {
		flex: 1; padding: 6px 10px;
		background: var(--bg-surface); border: 1px solid var(--border-subtle);
		border-radius: var(--radius-sm); color: var(--text-primary);
		font-size: 13px; outline: none;
	}
	.ai-edit-input:focus { border-color: var(--accent); }
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
	.form-hint { font-size: 0.7rem; color: var(--text-tertiary); margin-top: 2px; }
	.channel-overrides { display: flex; flex-direction: column; gap: 6px; }
	.channel-override-row { display: flex; align-items: center; gap: 8px; }
	.channel-override-name { font-size: 0.8rem; color: var(--text-secondary); min-width: 120px; }
	.channel-override-row select { font-size: 0.8rem; padding: 2px 6px; background: var(--bg-secondary); color: var(--text-primary); border: 1px solid var(--border); border-radius: 4px; }
	.form-actions { display: flex; gap: 8px; margin-top: 16px; }
	.btn-sm { font-size: 0.8rem; padding: 6px 12px; }

	/* Org chart */
	.org-chart { padding: 0; height: calc(100vh - 140px); position: relative; display: flex; flex-direction: column; }

	.empty-state { color: var(--text-tertiary); font-size: 0.85rem; padding: 40px; text-align: center; }

	/* Org node panel */
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
	.org-node-panel .panel-body .text-muted { color: #9898a6; }
	.org-node-panel .panel-body .text-sm { font-size: 0.8rem; }
	.org-node-panel .panel-actions {
		display: flex; flex-direction: column; gap: 4px; margin-top: 10px;
		padding-top: 10px; border-top: 1px solid #333;
	}
	.panel-type-badge { font-size: 0.7rem; color: #6b6b7b; }
	.panel-meta { display: flex; gap: 6px; align-items: center; }
	.panel-stats {
		display: flex; gap: 12px; padding: 10px 0;
		border-top: 1px solid #333; border-bottom: 1px solid #333; margin: 4px 0;
	}
	.stat-item { display: flex; flex-direction: column; align-items: center; flex: 1; }
	.stat-value { font-size: 1rem; font-weight: 700; color: #e1e1e6; }
	.stat-label { font-size: 0.65rem; color: #6b6b7b; text-transform: uppercase; letter-spacing: 0.5px; }
	.panel-section { display: flex; flex-direction: column; gap: 8px; margin-top: 6px; }
	.panel-field { display: flex; flex-direction: column; gap: 2px; }
	.panel-input {
		background: #12121e; border: 1px solid #333; border-radius: 6px;
		padding: 6px 8px; color: #e1e1e6; font-size: 0.8rem;
		outline: none; font-family: inherit; resize: vertical;
	}
	.panel-input:focus { border-color: var(--accent, #e8622b); }
	.panel-select {
		background: #12121e; border: 1px solid #333; border-radius: 6px;
		padding: 6px 8px; color: #e1e1e6; font-size: 0.8rem; outline: none;
	}
	.panel-select:focus { border-color: var(--accent, #e8622b); }
	.role-dialog-close {
		background: none; border: none; color: var(--text-secondary);
		font-size: 1.2rem; cursor: pointer; padding: 4px;
	}

	/* Role dialog */
	.role-dialog-fields { display: flex; flex-direction: column; gap: 14px; }
	.role-dialog-fields .form-group { display: flex; flex-direction: column; gap: 4px; }
	.role-dialog-fields .form-group label { font-size: 0.8rem; color: var(--text-tertiary); font-weight: 500; }
	.role-dialog-fields .form-group input,
	.role-dialog-fields .form-group textarea,
	.role-dialog-fields .form-group select {
		background: var(--bg-primary, #12121e); border: 1px solid var(--border-subtle, #333); border-radius: 6px;
		padding: 8px 10px; color: var(--text-primary); font-size: 0.85rem;
		outline: none; font-family: inherit;
	}
	.role-dialog-fields .form-group input:focus,
	.role-dialog-fields .form-group textarea:focus,
	.role-dialog-fields .form-group select:focus {
		border-color: var(--accent, #e8622b);
	}
	.role-dialog-fields .form-group input::placeholder,
	.role-dialog-fields .form-group textarea::placeholder { color: var(--text-muted, #555); }
	.required { color: #ef4444; }
	.role-dialog-actions { display: flex; justify-content: flex-end; gap: 8px; padding-top: 12px; }

	/* Agent skills */
	.skill-list {
		display: flex; flex-direction: column;
		gap: var(--space-xs); margin-bottom: var(--space-sm);
	}
	.skill-item {
		display: flex; justify-content: space-between; align-items: flex-start;
		padding: var(--space-xs) var(--space-sm);
		background: var(--bg-tertiary); border-radius: 6px;
	}
	.skill-item-actions { display: flex; gap: var(--space-xs); flex-shrink: 0; }
	.skill-editor { display: flex; flex-direction: column; gap: var(--space-sm); }
	.skill-editor-header { display: flex; justify-content: space-between; align-items: center; }
	.skill-textarea {
		width: 100%; font-family: var(--font-mono); font-size: var(--text-sm);
		background: var(--bg-tertiary); color: var(--text-primary);
		border: 1px solid var(--border); border-radius: 6px;
		padding: var(--space-sm); resize: vertical;
	}

	/* Modal */
	.modal-overlay {
		position: fixed; top: 0; left: 0; right: 0; bottom: 0;
		background: rgba(0,0,0,0.6); z-index: 200;
		display: flex; align-items: center; justify-content: center;
	}
	.modal-dialog {
		background: var(--bg-surface); border: 1px solid var(--border-subtle);
		border-radius: var(--radius-lg, 12px); max-width: 480px; width: 90vw;
		max-height: 80vh; display: flex; flex-direction: column; overflow: hidden;
	}
	.modal-header {
		display: flex; align-items: center; justify-content: space-between;
		padding: 16px 20px; border-bottom: 1px solid var(--border-subtle);
	}
	.modal-header h3 { font-size: 1rem; font-weight: 600; margin: 0; }
	.modal-close {
		background: none; border: none; color: var(--text-tertiary);
		font-size: 1.5rem; cursor: pointer; line-height: 1; padding: 0 4px; border-radius: 4px;
	}
	.modal-close:hover { color: var(--text-primary); background: var(--bg-raised, rgba(255,255,255,0.1)); }
	.modal-body { padding: 0 20px 20px; overflow-y: auto; flex: 1; }
</style>
