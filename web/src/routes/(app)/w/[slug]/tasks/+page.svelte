<script lang="ts">
	import { page } from '$app/state';
	import { goto } from '$app/navigation';
	import { onMount, onDestroy } from 'svelte';
	import { listTasks, createTask, updateTask, deleteTask, listTaskRuns, getCurrentUser, listAgents, listChannels } from '$lib/api';
	import { connect, disconnect, onMessage } from '$lib/ws';
	import { members } from '$lib/stores/workspace';
	import { addToast } from '$lib/toast';

	let slug = $derived(page.params.slug);
	let currentUser = $state(getCurrentUser());

	const STATUSES = ['backlog', 'todo', 'in_progress', 'done', 'cancelled'] as const;
	const PRIORITIES = ['urgent', 'high', 'medium', 'low'] as const;
	const STATUS_LABELS: Record<string, string> = { backlog: 'Backlog', todo: 'To Do', in_progress: 'In Progress', done: 'Done', cancelled: 'Cancelled' };
	const PRIORITY_COLORS: Record<string, string> = { urgent: '#ef4444', high: '#f97316', medium: '#eab308', low: '#606068' };

	type ViewMode = 'board' | 'list';
	let viewMode = $state<ViewMode>('board');
	let tasks = $state<any[]>([]);
	let myTasksOnly = $state(false);

	// Modal state
	let showTaskModal = $state(false);
	let editingTask = $state<any>(null);
	let formTitle = $state('');
	let formDescription = $state('');
	let formStatus = $state('backlog');
	let formPriority = $state('medium');
	let formAssignee = $state('');
	let formDueDate = $state('');
	let formTags = $state('');
	let formAssignType = $state<'member' | 'agent'>('member');
	let formAgentId = $state('');
	let formScheduledAt = $state('');
	let formChannelId = $state('');
	let formRecurrence = $state('');
	let formRecurrenceEnd = $state('');

	let agentsList = $state<any[]>([]);
	let channelsList = $state<any[]>([]);

	// Execution runs state
	let taskRuns = $state<any[]>([]);
	let loadingRuns = $state(false);
	let expandedRunId = $state<string | null>(null);

	const RECURRENCE_OPTIONS = [
		{ value: '', label: 'Once' },
		{ value: 'hourly', label: 'Every hour' },
		{ value: 'daily', label: 'Every day' },
		{ value: 'weekday', label: 'Every weekday' },
		{ value: 'weekly', label: 'Every week' },
	];

	const RECURRENCE_LABELS: Record<string, string> = {
		hourly: 'Runs every hour', daily: 'Runs daily', weekday: 'Runs weekdays', weekly: 'Runs weekly',
	};

	function toLocalInput(d: Date) {
		const pad = (n: number) => String(n).padStart(2, '0');
		return `${d.getFullYear()}-${pad(d.getMonth() + 1)}-${pad(d.getDate())}T${pad(d.getHours())}:${pad(d.getMinutes())}`;
	}

	function nowLocal() {
		const d = new Date();
		d.setSeconds(0, 0);
		return toLocalInput(d);
	}

	function offsetLocal(minutes: number) {
		const d = new Date(Date.now() + minutes * 60000);
		d.setSeconds(0, 0);
		return toLocalInput(d);
	}

	function offsetDate(days: number) {
		const d = new Date();
		d.setDate(d.getDate() + days);
		return d.toISOString().slice(0, 10);
	}

	// Upcoming tray
	let showTaskTray = $state(false);

	// Agent tasks tray
	let showAgentTray = $state(false);

	let upcomingAgentTasks = $derived.by(() => {
		return tasks
			.filter(t => t.agent_id && t.status === 'in_progress' && t.scheduled_at)
			.sort((a, b) => a.scheduled_at.localeCompare(b.scheduled_at));
	});

	let completedAgentTasks = $derived.by(() => {
		return tasks
			.filter(t => t.agent_id && (t.status === 'done' || t.status === 'cancelled') && t.last_run_at)
			.sort((a, b) => b.last_run_at.localeCompare(a.last_run_at));
	});

	let membersList: any[] = [];
	const unsubMembers = members.subscribe(v => membersList = v);

	function getMember(id: string) {
		return membersList.find((m: any) => m.id === id);
	}

	function memberInitial(m: any) {
		return (m?.display_name || '?')[0].toUpperCase();
	}

	function memberColor(m: any) {
		return m?.color || 'var(--text-tertiary)';
	}

	let filteredTasks = $derived(myTasksOnly ? tasks.filter(t => t.assignee_id === currentUser?.uid) : tasks);

	// Upcoming tasks for tray
	let upcomingTasks = $derived.by(() => {
		return tasks.filter(t => t.due_date && t.status !== 'done' && t.status !== 'cancelled')
			.sort((a, b) => a.due_date.localeCompare(b.due_date));
	});

	function getTimeGroup(dateStr: string): string {
		const d = new Date(dateStr + 'T00:00:00');
		const now = new Date();
		const today = new Date(now.getFullYear(), now.getMonth(), now.getDate());
		const tomorrow = new Date(today); tomorrow.setDate(today.getDate() + 1);
		const weekEnd = new Date(today); weekEnd.setDate(today.getDate() + 7);
		if (d < today) return 'Overdue';
		if (d < tomorrow) return 'Today';
		if (d < weekEnd) return 'This Week';
		return 'Later';
	}

	let groupedUpcoming = $derived.by(() => {
		const groups: Record<string, any[]> = {};
		for (const t of upcomingTasks) {
			const g = getTimeGroup(t.due_date);
			if (!groups[g]) groups[g] = [];
			groups[g].push(t);
		}
		return groups;
	});

	// Drag state
	let draggedTask = $state<any>(null);
	let dropTarget = $state<{ status: string; index: number } | null>(null);

	let unsubWS: (() => void) | null = null;

	function tasksForStatus(status: string): any[] {
		return filteredTasks.filter(t => t.status === status).sort((a, b) => (a.position ?? 0) - (b.position ?? 0));
	}

	function toggleMyTasks() {
		myTasksOnly = !myTasksOnly;
		localStorage.setItem('nexus_my_tasks', String(myTasksOnly));
	}

	onMount(async () => {
		const saved = localStorage.getItem('nexus_tasks_view');
		if (saved === 'list' || saved === 'board') viewMode = saved;
		myTasksOnly = localStorage.getItem('nexus_my_tasks') === 'true';

		connect();
		unsubWS = onMessage(handleWS);
		await loadTasks();
		listAgents(slug).then(d => agentsList = d.agents || []).catch(() => {});
		listChannels(slug).then(d => channelsList = d || []).catch(() => {});
	});

	onDestroy(() => {
		if (unsubWS) unsubWS();
		unsubMembers();
		disconnect();
	});

	function handleWS(type: string, payload: any) {
		if (type === 'task.created') {
			tasks = [...tasks.filter(t => t.id !== payload.id), payload];
		} else if (type === 'task.updated') {
			const prev = tasks.find(t => t.id === payload.id);
			if (prev && payload.agent_id && payload.last_run_at && payload.last_run_at !== prev.last_run_at) {
				if (payload.last_run_status === 'error') {
					addToast(`Task "${payload.title}" failed`, 'error');
				} else {
					addToast(`Task "${payload.title}" completed`, 'success');
				}
			}
			tasks = tasks.map(t => t.id === payload.id ? payload : t);
			// Refresh runs if modal is open for this task
			if (editingTask && editingTask.id === payload.id) {
				editingTask = payload;
				loadTaskRuns(payload.id);
			}
		} else if (type === 'task.deleted') {
			tasks = tasks.filter(t => t.id !== payload.id);
		}
	}

	async function loadTasks() {
		try {
			const data = await listTasks(slug);
			tasks = data.tasks || [];
		} catch {}
	}

	// Modal functions
	function openCreateTask() {
		editingTask = null;
		formTitle = '';
		formDescription = '';
		formStatus = 'backlog';
		formPriority = 'medium';
		formAssignee = '';
		formDueDate = '';
		formTags = '';
		formAssignType = 'member';
		formAgentId = '';
		formScheduledAt = '';
		formChannelId = '';
		formRecurrence = '';
		formRecurrenceEnd = '';
		showTaskModal = true;
	}

	async function loadTaskRuns(taskId: string) {
		loadingRuns = true;
		try {
			const data = await listTaskRuns(slug, taskId);
			taskRuns = data.runs || [];
		} catch { taskRuns = []; }
		loadingRuns = false;
	}

	function openEditTask(task: any) {
		editingTask = task;
		formTitle = task.title || '';
		formDescription = task.description || '';
		formStatus = task.status || 'backlog';
		formPriority = task.priority || 'medium';
		formAssignee = task.assignee_id || '';
		formDueDate = task.due_date || '';
		formTags = (task.tags || []).join(', ');
		if (task.agent_id) {
			formAssignType = 'agent';
			formAgentId = task.agent_id;
			formScheduledAt = task.scheduled_at ? toLocalInput(new Date(task.scheduled_at)) : '';
			formChannelId = task.channel_id || '';
			formRecurrence = task.recurrence_rule || '';
			formRecurrenceEnd = task.recurrence_end || '';
		} else {
			formAssignType = 'member';
			formAgentId = '';
			formScheduledAt = '';
			formChannelId = '';
			formRecurrence = '';
			formRecurrenceEnd = '';
		}
		showTaskModal = true;
		// Load execution runs for agent tasks
		if (task.agent_id && task.run_count > 0) {
			loadTaskRuns(task.id);
		} else {
			taskRuns = [];
		}
	}

	async function saveTask() {
		if (!formTitle.trim()) return;
		const tags = formTags.split(',').map(t => t.trim()).filter(Boolean);
		const taskData: any = {
			title: formTitle.trim(),
			description: formDescription.trim() || undefined,
			status: formStatus,
			priority: formPriority,
			due_date: formDueDate || undefined,
			tags: tags.length > 0 ? tags : undefined,
		};
		if (formAssignType === 'agent') {
			taskData.agent_id = formAgentId || 'brain';
			taskData.scheduled_at = formScheduledAt ? new Date(formScheduledAt).toISOString() : new Date().toISOString();
			taskData.channel_id = formChannelId || undefined;
			taskData.recurrence_rule = formRecurrence || '';
			taskData.recurrence_end = formRecurrenceEnd || '';
			taskData.assignee_id = '';
			taskData.status = 'in_progress';
		} else {
			taskData.agent_id = '';
			taskData.scheduled_at = '';
			taskData.recurrence_rule = '';
			taskData.recurrence_end = '';
			if (formAssignee) taskData.assignee_id = formAssignee;
			else taskData.assignee_id = '';
		}

		try {
			if (editingTask) {
				const updated = await updateTask(slug, editingTask.id, taskData);
				tasks = tasks.map(t => t.id === editingTask.id ? updated : t);
			} else {
				await createTask(slug, taskData);
			}
			showTaskModal = false;
		} catch (e: any) {
			alert(e.message);
		}
	}

	async function removeTask() {
		if (!editingTask || !confirm('Delete this task?')) return;
		try {
			await deleteTask(slug, editingTask.id);
			tasks = tasks.filter(t => t.id !== editingTask.id);
			showTaskModal = false;
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

	function setViewMode(mode: ViewMode) {
		viewMode = mode;
		localStorage.setItem('nexus_tasks_view', mode);
	}

	function formatDate(iso: string) {
		return new Date(iso).toLocaleDateString([], { month: 'short', day: 'numeric' });
	}

	function formatDueDate(dateStr: string) {
		const d = new Date(dateStr + 'T00:00:00');
		return d.toLocaleDateString([], { month: 'short', day: 'numeric' });
	}

	function formatTimeAgo(iso: string) {
		const diff = Date.now() - new Date(iso).getTime();
		const mins = Math.floor(diff / 60000);
		if (mins < 1) return 'just now';
		if (mins < 60) return `${mins}m ago`;
		const hrs = Math.floor(mins / 60);
		if (hrs < 24) return `${hrs}h ago`;
		const days = Math.floor(hrs / 24);
		return `${days}d ago`;
	}

	function formatCountdown(iso: string, _tick?: number) {
		const diff = new Date(iso).getTime() - Date.now();
		if (diff <= 0) return 'any moment';
		const secs = Math.floor(diff / 1000);
		if (secs < 60) return `${secs}s`;
		const mins = Math.floor(secs / 60);
		if (mins < 60) return `${mins}m`;
		const hrs = Math.floor(mins / 60);
		const remMins = mins % 60;
		if (hrs < 24) return remMins > 0 ? `${hrs}h ${remMins}m` : `${hrs}h`;
		const days = Math.floor(hrs / 24);
		const remHrs = hrs % 24;
		return remHrs > 0 ? `${days}d ${remHrs}h` : `${days}d`;
	}

	// Tick every 10s to keep countdowns fresh
	let countdownTick = $state(0);
	let countdownInterval: ReturnType<typeof setInterval>;
	$effect(() => {
		countdownInterval = setInterval(() => { countdownTick++; }, 10000);
		return () => clearInterval(countdownInterval);
	});

	// --- Drag and drop with vertical reordering ---
	function handleDragStart(e: DragEvent, task: any) {
		draggedTask = task;
		if (e.dataTransfer) {
			e.dataTransfer.effectAllowed = 'move';
			e.dataTransfer.setData('text/plain', task.id);
		}
	}

	function handleCardDragOver(e: DragEvent, status: string, index: number) {
		e.preventDefault();
		if (e.dataTransfer) e.dataTransfer.dropEffect = 'move';
		const card = (e.currentTarget as HTMLElement);
		const rect = card.getBoundingClientRect();
		const midY = rect.top + rect.height / 2;
		const insertIndex = e.clientY < midY ? index : index + 1;
		dropTarget = { status, index: insertIndex };
	}

	function handleColumnDragOver(e: DragEvent, status: string) {
		e.preventDefault();
		if (e.dataTransfer) e.dataTransfer.dropEffect = 'move';
		const target = e.target as HTMLElement;
		if (target.closest('.task-card')) return;
		const columnTasks = tasksForStatus(status);
		dropTarget = { status, index: columnTasks.length };
	}

	function handleDragLeaveColumn(e: DragEvent) {
		const target = e.currentTarget as HTMLElement;
		const related = e.relatedTarget as HTMLElement;
		if (target.contains(related)) return;
		dropTarget = null;
	}

	async function handleDrop(e: DragEvent, status: string) {
		e.preventDefault();
		if (!draggedTask || !dropTarget) {
			draggedTask = null;
			dropTarget = null;
			return;
		}

		const taskId = draggedTask.id;
		const oldStatus = draggedTask.status;
		const oldPosition = draggedTask.position ?? 0;
		const targetStatus = dropTarget.status;
		const targetIndex = dropTarget.index;

		const columnTasks = tasksForStatus(targetStatus).filter(t => t.id !== taskId);
		let newPosition: number;
		if (columnTasks.length === 0) {
			newPosition = 1000;
		} else if (targetIndex === 0) {
			newPosition = (columnTasks[0]?.position ?? 1000) - 1000;
		} else if (targetIndex >= columnTasks.length) {
			newPosition = (columnTasks[columnTasks.length - 1]?.position ?? 0) + 1000;
		} else {
			const before = columnTasks[targetIndex - 1]?.position ?? 0;
			const after = columnTasks[targetIndex]?.position ?? before + 2000;
			newPosition = Math.floor((before + after) / 2);
		}

		tasks = tasks.map(t => t.id === taskId ? { ...t, status: targetStatus, position: newPosition } : t);
		draggedTask = null;
		dropTarget = null;

		try {
			const updates: Record<string, any> = { position: newPosition };
			if (oldStatus !== targetStatus) updates.status = targetStatus;
			await updateTask(slug, taskId, updates);
		} catch (e: any) {
			tasks = tasks.map(t => t.id === taskId ? { ...t, status: oldStatus, position: oldPosition } : t);
			alert(e.message);
		}
	}

	function handleDragEnd() {
		draggedTask = null;
		dropTarget = null;
	}
</script>

<div class="tasks-page">
	<header class="tasks-header">
		<div class="header-left">
			<button class="back-btn" onclick={() => goto(`/w/${slug}`)}>
				<svg width="16" height="16" viewBox="0 0 16 16" fill="none">
					<path d="M10 3L5 8L10 13" stroke="currentColor" stroke-width="1.5" stroke-linecap="round" stroke-linejoin="round"/>
				</svg>
			</button>
			<h1>Tasks</h1>
			<span class="task-count">{tasks.length}</span>
		</div>
		<div class="header-right">
			<button class="tray-btn" class:active={showAgentTray} onclick={() => { showAgentTray = !showAgentTray; if (showAgentTray) showTaskTray = false; }} title="Agent tasks">
				<svg width="16" height="16" viewBox="0 0 20 20" fill="none">
					<rect x="4" y="5" width="12" height="9" rx="2.5" stroke="currentColor" stroke-width="1.3"/>
					<circle cx="7.5" cy="9.5" r="1.3" fill="currentColor"/>
					<circle cx="12.5" cy="9.5" r="1.3" fill="currentColor"/>
					<line x1="10" y1="2" x2="10" y2="5" stroke="currentColor" stroke-width="1.2" stroke-linecap="round"/>
					<circle cx="10" cy="1.5" r="1" fill="currentColor"/>
				</svg>
			</button>
			<button class="tray-btn" class:active={showTaskTray} onclick={() => { showTaskTray = !showTaskTray; if (showTaskTray) showAgentTray = false; }} title="Upcoming deadlines">
				<svg width="16" height="16" viewBox="0 0 16 16" fill="none">
					<rect x="2" y="2" width="12" height="12" rx="2" stroke="currentColor" stroke-width="1.2"/>
					<path d="M2 6H14" stroke="currentColor" stroke-width="1.2"/>
					<path d="M5 1V3M11 1V3" stroke="currentColor" stroke-width="1.2" stroke-linecap="round"/>
				</svg>
			</button>
			<button class="my-tasks-btn" class:active={myTasksOnly} onclick={toggleMyTasks}>
				{myTasksOnly ? 'My Tasks' : 'All Tasks'}
			</button>
			<div class="view-toggle">
				<button class="toggle-btn" class:active={viewMode === 'board'} onclick={() => setViewMode('board')} title="Board view">
					<svg width="16" height="16" viewBox="0 0 16 16" fill="none">
						<rect x="1" y="1" width="4" height="14" rx="1" stroke="currentColor" stroke-width="1.2"/>
						<rect x="6" y="1" width="4" height="10" rx="1" stroke="currentColor" stroke-width="1.2"/>
						<rect x="11" y="1" width="4" height="7" rx="1" stroke="currentColor" stroke-width="1.2"/>
					</svg>
				</button>
				<button class="toggle-btn" class:active={viewMode === 'list'} onclick={() => setViewMode('list')} title="List view">
					<svg width="16" height="16" viewBox="0 0 16 16" fill="none">
						<path d="M2 4H14M2 8H14M2 12H14" stroke="currentColor" stroke-width="1.2" stroke-linecap="round"/>
					</svg>
				</button>
			</div>
			<button class="btn-new-task" onclick={openCreateTask}>+ New Task</button>
		</div>
	</header>

	{#if viewMode === 'board'}
		<div class="board">
			{#each STATUSES.filter(s => s !== 'cancelled') as status}
				{@const columnTasks = tasksForStatus(status)}
				<div
					class="board-col"
					ondragover={(e) => handleColumnDragOver(e, status)}
					ondragleave={handleDragLeaveColumn}
					ondrop={(e) => handleDrop(e, status)}
				>
					<div class="board-col-header">
						<span>{STATUS_LABELS[status]}</span>
						<span class="board-count">{columnTasks.length}</span>
					</div>
					<div class="board-cards">
						{#each columnTasks as task, i (task.id)}
							{#if dropTarget && dropTarget.status === status && dropTarget.index === i && draggedTask?.id !== task.id}
								<div class="drop-line">
									<div class="drop-dot"></div>
								</div>
							{/if}
							{@const isAgentActive = !!task.agent_id && task.status === 'in_progress'}
							{@const isAgentPaused = !!task.agent_id && task.status !== 'in_progress'}
							{@const isAgentError = !!task.agent_id && task.last_run_status === 'error'}
							<div
								class="task-card"
								class:dragging={draggedTask?.id === task.id}
								class:task-active={isAgentActive && !isAgentError}
								class:task-error={isAgentActive && isAgentError}
								class:task-paused={isAgentPaused}
								draggable="true"
								ondragstart={(e) => handleDragStart(e, task)}
								ondragover={(e) => handleCardDragOver(e, status, i)}
								ondragend={handleDragEnd}
								onclick={() => openEditTask(task)}
							>
								<div class="task-card-header">
									{#if isAgentActive}
										{@const botColor = isAgentError ? "#f97316" : "#14b8a6"}
										<span class="bot-icon bot-alive" class:bot-error={isAgentError}>
											<svg width="16" height="16" viewBox="0 0 20 20" fill="none">
												<!-- antenna -->
												<line x1="10" y1="1.5" x2="10" y2="4" stroke={botColor} stroke-width="1.2" stroke-linecap="round"/>
												<circle cx="10" cy="1.2" r="1.2" fill={botColor} class="bot-antenna-tip"/>
												<!-- head -->
												<rect x="3.5" y="4.5" width="13" height="9.5" rx="2.5" stroke={botColor} stroke-width="1.2" fill={isAgentError ? "rgba(249,115,22,0.06)" : "rgba(20,184,166,0.06)"}/>
												<!-- eyes -->
												<circle cx="7.2" cy="9" r="1.6" fill={botColor} class="bot-eye bot-eye-l"/>
												<circle cx="12.8" cy="9" r="1.6" fill={botColor} class="bot-eye bot-eye-r"/>
												<!-- mouth -->
												<path d="M7.5 12.2c.8.7 1.5 1 2.5 1s1.7-.3 2.5-1" stroke={botColor} stroke-width="1" stroke-linecap="round" fill="none"/>
												<!-- ears -->
												<rect x="1" y="7" width="2" height="3.5" rx="1" fill={botColor} opacity="0.5"/>
												<rect x="17" y="7" width="2" height="3.5" rx="1" fill={botColor} opacity="0.5"/>
											</svg>
										</span>
									{:else if isAgentPaused}
										<span class="bot-icon bot-off">
											<svg width="16" height="16" viewBox="0 0 20 20" fill="none">
												<!-- antenna (dead) -->
												<line x1="10" y1="1.5" x2="10" y2="4" stroke="var(--text-tertiary)" stroke-width="1.2" stroke-linecap="round" opacity="0.4"/>
												<circle cx="10" cy="1.2" r="1.2" fill="var(--text-tertiary)" opacity="0.3"/>
												<!-- head -->
												<rect x="3.5" y="4.5" width="13" height="9.5" rx="2.5" stroke="var(--text-tertiary)" stroke-width="1.2" opacity="0.4"/>
												<!-- X eyes -->
												<path d="M5.8 7.6l2.8 2.8M8.6 7.6L5.8 10.4" stroke="var(--text-tertiary)" stroke-width="1.1" stroke-linecap="round" opacity="0.5"/>
												<path d="M11.4 7.6l2.8 2.8M14.2 7.6l-2.8 2.8" stroke="var(--text-tertiary)" stroke-width="1.1" stroke-linecap="round" opacity="0.5"/>
												<!-- flat mouth -->
												<line x1="7.5" y1="12.2" x2="12.5" y2="12.2" stroke="var(--text-tertiary)" stroke-width="1" stroke-linecap="round" opacity="0.4"/>
												<!-- ears -->
												<rect x="1" y="7" width="2" height="3.5" rx="1" fill="var(--text-tertiary)" opacity="0.2"/>
												<rect x="17" y="7" width="2" height="3.5" rx="1" fill="var(--text-tertiary)" opacity="0.2"/>
											</svg>
										</span>
									{:else}
										<span class="task-priority-dot" style="background: {PRIORITY_COLORS[task.priority]}"></span>
									{/if}
									<span class="task-title">{task.title}</span>
									{#if task.agent_id}
										<span class="task-agent-badge" class:active-badge={isAgentActive} class:paused-badge={isAgentPaused} title="{isAgentActive ? 'Active' : 'Paused'} agent task{task.scheduled_at ? ' — ' + new Date(task.scheduled_at).toLocaleString() : ''}">
											<svg width="14" height="14" viewBox="0 0 16 16" fill="none">
												<rect x="3" y="1" width="10" height="12" rx="2" stroke="currentColor" stroke-width="1.3"/>
												<circle cx="8" cy="5.5" r="1.5" stroke="currentColor" stroke-width="1.2"/>
												<path d="M5 10.5C5 9 6.5 8.5 8 8.5s3 .5 3 2" stroke="currentColor" stroke-width="1.2" stroke-linecap="round"/>
											</svg>
										</span>
									{:else if task.assignee_id}
										{@const assignee = getMember(task.assignee_id)}
										<span class="task-avatar" style="background: {memberColor(assignee)}" title={assignee?.display_name || 'Unknown'}>{memberInitial(assignee)}</span>
									{/if}
								</div>
								{#if task.agent_id && isAgentPaused}
									<div class="task-paused-label">Paused</div>
								{/if}
								{#if task.recurrence_rule && RECURRENCE_LABELS[task.recurrence_rule]}
									<div class="task-recurrence" class:recurrence-paused={isAgentPaused}>{RECURRENCE_LABELS[task.recurrence_rule]}</div>
								{/if}
								{#if task.description && !task.recurrence_rule}
									<p class="task-desc">{task.description}</p>
								{/if}
								{#if task.tags?.length > 0}
									<div class="task-tags">
										{#each task.tags as tag}<span class="task-tag">{tag}</span>{/each}
									</div>
								{/if}
								{#if isAgentActive && task.scheduled_at}
									<div class="task-countdown">Next in {formatCountdown(task.scheduled_at, countdownTick)}</div>
								{:else if isAgentPaused && task.scheduled_at}
									<div class="task-next-muted">{new Date(task.scheduled_at).toLocaleString([], { month: 'short', day: 'numeric', hour: '2-digit', minute: '2-digit' })}</div>
								{/if}
								{#if task.run_count > 0}
									<div class="task-runs">{task.run_count} run{task.run_count !== 1 ? 's' : ''}{task.last_run_at ? ' · Last ' + formatTimeAgo(task.last_run_at) : ''}</div>
								{/if}
								{#if !task.agent_id && task.due_date}
									<div class="task-due">Due {formatDueDate(task.due_date)}</div>
								{/if}
							</div>
						{/each}
						{#if dropTarget && dropTarget.status === status && dropTarget.index >= columnTasks.length}
							<div class="drop-line">
								<div class="drop-dot"></div>
							</div>
						{/if}
						{#if columnTasks.length === 0 && !(dropTarget && dropTarget.status === status)}
							<div class="board-empty">Drop tasks here</div>
						{/if}
					</div>
				</div>
			{/each}
		</div>
	{:else}
		<div class="task-list">
			<div class="task-list-header">
				<span class="tl-pri">Priority</span>
				<span class="tl-title">Title</span>
				<span class="tl-assignee">Assignee</span>
				<span class="tl-status">Status</span>
				<span class="tl-tags">Tags</span>
				<span class="tl-date">Created</span>
			</div>
			{#each filteredTasks as task (task.id)}
				<div class="task-list-row" onclick={() => openEditTask(task)}>
					<span class="tl-pri">
						<span class="task-priority-dot" style="background: {PRIORITY_COLORS[task.priority]}"></span>
						<span class="pri-label">{task.priority}</span>
					</span>
					<span class="tl-title">{task.title}</span>
					<span class="tl-assignee">
						{#if task.assignee_id}
							{@const assignee = getMember(task.assignee_id)}
							<span class="task-avatar-sm" style="background: {memberColor(assignee)}">{memberInitial(assignee)}</span>
							<span class="assignee-name">{assignee?.display_name || 'Unknown'}</span>
						{:else}
							<span class="unassigned">—</span>
						{/if}
					</span>
					<span class="tl-status">
						<span class="status-badge status-{task.status}">{STATUS_LABELS[task.status]}</span>
					</span>
					<span class="tl-tags">
						{#if task.tags?.length > 0}
							{#each task.tags as tag}<span class="task-tag">{tag}</span>{/each}
						{/if}
					</span>
					<span class="tl-date">{formatDate(task.created_at)}</span>
				</div>
			{/each}
			{#if tasks.length === 0}
				<div class="empty-state">
					<p>No tasks yet. Create one to get started.</p>
				</div>
			{/if}
		</div>
	{/if}
</div>

<!-- Task Modal -->
{#if showTaskModal}
	<div class="modal-overlay" onclick={() => showTaskModal = false}>
		<div class="modal" onclick={(e) => e.stopPropagation()}>
			<div class="modal-header">
				<h3>{editingTask ? (formAssignType === 'agent' ? 'Edit Agent Task' : 'Edit Task') : (formAssignType === 'agent' ? 'New Agent Task' : 'New Task')}</h3>
				<button class="modal-close" onclick={() => showTaskModal = false}>&times;</button>
			</div>
			<div class="modal-body">
				<div class="assign-toggle">
					<button class="assign-btn" class:active={formAssignType === 'member'} onclick={() => formAssignType = 'member'}>Member</button>
					<button class="assign-btn" class:active={formAssignType === 'agent'} onclick={() => { formAssignType = 'agent'; if (!formScheduledAt) formScheduledAt = nowLocal(); const general = channelsList.find(ch => ch.name === 'general'); if (general && !formChannelId) formChannelId = general.id; }}>
						<svg width="12" height="12" viewBox="0 0 16 16" fill="none" style="margin-right:4px;vertical-align:-1px">
							<rect x="3" y="1" width="10" height="12" rx="2" stroke="currentColor" stroke-width="1.3"/>
							<circle cx="8" cy="5.5" r="1.5" stroke="currentColor" stroke-width="1.2"/>
							<path d="M5 10.5C5 9 6.5 8.5 8 8.5s3 .5 3 2" stroke="currentColor" stroke-width="1.2" stroke-linecap="round"/>
						</svg>
						Agent
					</button>
				</div>

				<div class="form-group">
					<label for="task-title">Title</label>
					<input id="task-title" type="text" placeholder="Task title..." bind:value={formTitle} onkeydown={(e) => e.key === 'Enter' && saveTask()} autofocus />
				</div>

				{#if formAssignType === 'member'}
					<div class="form-group">
						<label for="task-desc">Description</label>
						<textarea id="task-desc" placeholder="Description (optional)..." bind:value={formDescription} rows="3"></textarea>
					</div>
					<div class="form-row">
						<div class="form-group">
							<label for="task-status">Status</label>
							<select id="task-status" bind:value={formStatus}>
								{#each STATUSES as s}<option value={s}>{STATUS_LABELS[s]}</option>{/each}
							</select>
						</div>
						<div class="form-group">
							<label for="task-priority">Priority</label>
							<select id="task-priority" bind:value={formPriority}>
								{#each PRIORITIES as p}<option value={p}>{p}</option>{/each}
							</select>
						</div>
					</div>
					<div class="form-row">
						<div class="form-group">
							<label for="task-assignee">Assignee</label>
							<select id="task-assignee" bind:value={formAssignee}>
								<option value="">Unassigned</option>
								{#each membersList as m}<option value={m.id}>{m.display_name}</option>{/each}
							</select>
						</div>
						<div class="form-group">
							<label for="task-due">Due Date</label>
							<input id="task-due" type="date" bind:value={formDueDate} />
						</div>
					</div>
				{:else}
					<div class="form-group">
						<label for="task-prompt">Prompt</label>
						<textarea id="task-prompt" placeholder="What should the agent do?" bind:value={formDescription} rows="5"></textarea>
					</div>
					<div class="form-row">
						<div class="form-group">
							<label for="task-agent">Agent</label>
							<select id="task-agent" bind:value={formAgentId}>
								<option value="brain">Brain</option>
								{#each agentsList as a}<option value={a.id}>{a.name}</option>{/each}
							</select>
						</div>
						<div class="form-group">
							<label for="task-channel">Post to Channel</label>
							<select id="task-channel" bind:value={formChannelId}>
								<option value="">#general</option>
								{#each channelsList as ch}<option value={ch.id}>{ch.name}</option>{/each}
							</select>
						</div>
					</div>
					<div class="form-group">
						<label>Delivery</label>
						<input id="task-scheduled" type="datetime-local" bind:value={formScheduledAt} />
					</div>
					<div class="form-row">
						<div class="form-group">
							<label for="task-recurrence">Repeat</label>
							<select id="task-recurrence" bind:value={formRecurrence}>
								{#each RECURRENCE_OPTIONS as opt}<option value={opt.value}>{opt.label}</option>{/each}
							</select>
						</div>
						<div class="form-group">
							<label for="task-priority-agent">Priority</label>
							<select id="task-priority-agent" bind:value={formPriority}>
								{#each PRIORITIES as p}<option value={p}>{p}</option>{/each}
							</select>
						</div>
					</div>
					{#if formRecurrence}
					<div class="form-group">
						<label for="task-recurrence-end">Repeat Until</label>
						<div class="time-presets">
							<button class="preset-chip" onclick={() => formRecurrenceEnd = offsetDate(1)}>1 day</button>
							<button class="preset-chip" onclick={() => formRecurrenceEnd = offsetDate(7)}>1 week</button>
							<button class="preset-chip" onclick={() => formRecurrenceEnd = offsetDate(30)}>1 month</button>
						</div>
						<input id="task-recurrence-end" type="date" bind:value={formRecurrenceEnd} />
						{#if !formRecurrenceEnd}<span class="field-hint">No end date — runs indefinitely</span>{/if}
					</div>
					{/if}
				{/if}

				<div class="form-group">
					<label for="task-tags">Tags</label>
					<input id="task-tags" type="text" placeholder="Comma-separated tags..." bind:value={formTags} />
				</div>
				{#if editingTask}
					<div class="task-meta">
						<span>Created {formatDate(editingTask.created_at)}</span>
						{#if editingTask.updated_at}
							<span>Updated {formatDate(editingTask.updated_at)}</span>
						{/if}
						{#if editingTask.run_count > 0}
							<span>{editingTask.run_count} run{editingTask.run_count !== 1 ? 's' : ''}</span>
						{/if}
						{#if editingTask.last_run_at}
							<span>Last run {formatTimeAgo(editingTask.last_run_at)}</span>
						{/if}
					</div>
					{#if editingTask.agent_id && taskRuns.length > 0}
						<div class="runs-section">
							<div class="runs-header">Execution History</div>
							{#each taskRuns as run (run.id)}
								<div class="run-row" onclick={() => expandedRunId = expandedRunId === run.id ? null : run.id}>
									<span class="run-status" class:run-success={run.status === 'success'} class:run-error={run.status === 'error'}>
										{#if run.status === 'success'}
											<svg width="14" height="14" viewBox="0 0 16 16" fill="none"><path d="M3 8.5L6.5 12L13 4" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"/></svg>
										{:else}
											<svg width="14" height="14" viewBox="0 0 16 16" fill="none"><circle cx="8" cy="8" r="6" stroke="currentColor" stroke-width="1.5"/><path d="M8 5V9M8 11V11.5" stroke="currentColor" stroke-width="1.5" stroke-linecap="round"/></svg>
										{/if}
									</span>
									<span class="run-time">{formatTimeAgo(run.created_at)}</span>
									{#if run.duration_ms}
										<span class="run-duration">{run.duration_ms < 1000 ? run.duration_ms + 'ms' : (run.duration_ms / 1000).toFixed(1) + 's'}</span>
									{/if}
									<span class="run-output-preview">{run.output?.slice(0, 80) || '—'}{run.output?.length > 80 ? '...' : ''}</span>
								</div>
								{#if expandedRunId === run.id && run.output}
									<div class="run-output-full">{run.output}</div>
								{/if}
							{/each}
						</div>
					{:else if editingTask.agent_id && loadingRuns}
						<div class="runs-loading">Loading runs...</div>
					{/if}
				{/if}
			</div>
			<div class="modal-footer">
				{#if editingTask}
					<button class="btn-delete" onclick={removeTask}>Delete</button>
				{/if}
				<div class="footer-right">
					<button class="btn-cancel" onclick={() => showTaskModal = false}>Cancel</button>
					<button class="btn-save" onclick={saveTask}>{editingTask ? 'Save' : 'Create'}</button>
				</div>
			</div>
		</div>
	</div>
{/if}

<!-- Upcoming Tasks Tray -->
{#if showTaskTray}
	<div class="tray-overlay" onclick={() => showTaskTray = false}></div>
	<div class="task-tray">
		<div class="tray-header">
			<h3>Upcoming Deadlines</h3>
			<button class="modal-close" onclick={() => showTaskTray = false}>&times;</button>
		</div>
		<div class="tray-body">
			{#if upcomingTasks.length === 0}
				<div class="tray-empty">No upcoming deadlines</div>
			{:else}
				{#each ['Overdue', 'Today', 'This Week', 'Later'] as group}
					{#if groupedUpcoming[group]?.length}
						<div class="tray-group">
							<div class="tray-group-label" class:overdue={group === 'Overdue'}>{group}</div>
							{#each groupedUpcoming[group] as task}
								<div class="tray-item" onclick={() => { showTaskTray = false; openEditTask(task); }}>
									<span class="task-priority-dot" style="background: {PRIORITY_COLORS[task.priority]}"></span>
									<div class="tray-item-info">
										<span class="tray-item-title">{task.title}</span>
										<span class="tray-item-date">{formatDueDate(task.due_date)}</span>
									</div>
									{#if task.assignee_id}
										{@const assignee = getMember(task.assignee_id)}
										<span class="task-avatar-sm" style="background: {memberColor(assignee)}" title={assignee?.display_name || 'Unknown'}>{memberInitial(assignee)}</span>
									{/if}
								</div>
							{/each}
						</div>
					{/if}
				{/each}
			{/if}
		</div>
	</div>
{/if}

<!-- Agent Tasks Tray -->
{#if showAgentTray}
	<div class="tray-overlay" onclick={() => showAgentTray = false}></div>
	<div class="task-tray">
		<div class="tray-header">
			<h3>Agent Tasks</h3>
			<button class="modal-close" onclick={() => showAgentTray = false}>&times;</button>
		</div>
		<div class="tray-body">
			{#if upcomingAgentTasks.length === 0 && completedAgentTasks.length === 0}
				<div class="tray-empty">No agent tasks</div>
			{:else}
				{#if upcomingAgentTasks.length > 0}
					<div class="tray-group">
						<div class="tray-group-label">Upcoming</div>
						{#each upcomingAgentTasks as task}
							<div class="tray-item" onclick={() => { showAgentTray = false; openEditTask(task); }}>
								<span class="agent-tray-status" class:status-ok={task.last_run_status !== 'error'} class:status-err={task.last_run_status === 'error'}>
									{#if task.last_run_status === 'error'}
										<svg width="14" height="14" viewBox="0 0 16 16" fill="none"><circle cx="8" cy="8" r="6" stroke="currentColor" stroke-width="1.5"/><path d="M8 5V9M8 11V11.5" stroke="currentColor" stroke-width="1.5" stroke-linecap="round"/></svg>
									{:else}
										<svg width="14" height="14" viewBox="0 0 20 20" fill="none">
											<rect x="4" y="5" width="12" height="9" rx="2.5" stroke="currentColor" stroke-width="1.3"/>
											<circle cx="7.5" cy="9.5" r="1.3" fill="currentColor"/>
											<circle cx="12.5" cy="9.5" r="1.3" fill="currentColor"/>
										</svg>
									{/if}
								</span>
								<div class="tray-item-info">
									<span class="tray-item-title">{task.title}</span>
									<span class="tray-item-subtitle">
										{#if task.last_run_status === 'error'}
											<span class="subtitle-err">Last run failed</span> ·
										{:else if task.last_run_at}
											<span class="subtitle-ok">Last run OK</span> ·
										{/if}
										{#if task.recurrence_rule}
											{RECURRENCE_LABELS[task.recurrence_rule] || task.recurrence_rule} ·
										{/if}
										{task.agent_id === 'brain' ? 'Brain' : agentsList.find(a => a.id === task.agent_id)?.name || task.agent_id}
									</span>
								</div>
								<span class="tray-countdown">
									{formatCountdown(task.scheduled_at, countdownTick)}
								</span>
							</div>
						{/each}
					</div>
				{/if}
				{#if completedAgentTasks.length > 0}
					<div class="tray-group">
						<div class="tray-group-label">Completed</div>
						{#each completedAgentTasks as task}
							<div class="tray-item" onclick={() => { showAgentTray = false; openEditTask(task); }}>
								<span class="agent-tray-status" class:status-ok={task.last_run_status !== 'error'} class:status-err={task.last_run_status === 'error'}>
									{#if task.last_run_status === 'error'}
										<svg width="14" height="14" viewBox="0 0 16 16" fill="none"><circle cx="8" cy="8" r="6" stroke="currentColor" stroke-width="1.5"/><path d="M8 5V9M8 11V11.5" stroke="currentColor" stroke-width="1.5" stroke-linecap="round"/></svg>
									{:else}
										<svg width="14" height="14" viewBox="0 0 16 16" fill="none"><path d="M3 8.5L6.5 12L13 4" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"/></svg>
									{/if}
								</span>
								<div class="tray-item-info">
									<span class="tray-item-title">{task.title}</span>
									<span class="tray-item-subtitle">
										Ran {formatTimeAgo(task.last_run_at)}
										{#if task.run_count > 1} · {task.run_count} runs{/if}
									</span>
								</div>
							</div>
						{/each}
					</div>
				{/if}
			{/if}
		</div>
	</div>
{/if}

<style>
	.tasks-page {
		display: flex;
		flex-direction: column;
		height: 100vh;
		background: var(--bg-root);
		color: var(--text-primary);
		font-family: var(--font-sans);
	}
	.tasks-header {
		display: flex;
		align-items: center;
		justify-content: space-between;
		padding: var(--space-md) var(--space-xl);
		border-bottom: 1px solid var(--border-subtle);
		background: var(--bg-surface);
		flex-shrink: 0;
	}
	.header-left { display: flex; align-items: center; gap: var(--space-sm); }
	.header-left h1 { font-size: var(--text-xl); font-weight: 700; margin: 0; }
	.back-btn {
		display: flex; align-items: center; justify-content: center;
		width: 28px; height: 28px; background: none;
		border: 1px solid var(--border-default); border-radius: var(--radius-md);
		color: var(--text-secondary); cursor: pointer;
	}
	.back-btn:hover { color: var(--text-primary); border-color: var(--border-strong); }
	.task-count {
		font-size: var(--text-xs); background: var(--bg-raised);
		border: 1px solid var(--border-subtle); padding: 1px 8px;
		border-radius: var(--radius-full); color: var(--text-tertiary);
	}
	.header-right { display: flex; align-items: center; gap: var(--space-md); }
	.tray-btn {
		display: flex; align-items: center; justify-content: center;
		width: 32px; height: 28px; background: var(--bg-raised);
		border: 1px solid var(--border-default); border-radius: var(--radius-md);
		color: var(--text-tertiary); cursor: pointer;
	}
	.tray-btn:hover { color: var(--text-secondary); border-color: var(--border-strong); }
	.tray-btn.active { background: var(--accent); color: var(--text-inverse); border-color: var(--accent); }
	.view-toggle {
		display: flex; border: 1px solid var(--border-default);
		border-radius: var(--radius-md); overflow: hidden;
	}
	.toggle-btn {
		display: flex; align-items: center; justify-content: center;
		width: 32px; height: 28px; background: var(--bg-raised);
		border: none; color: var(--text-tertiary); cursor: pointer;
	}
	.toggle-btn:hover { color: var(--text-secondary); }
	.toggle-btn.active { background: var(--accent); color: var(--text-inverse); }
	.btn-new-task {
		padding: 6px 14px; background: var(--accent); color: var(--text-inverse);
		border: none; border-radius: var(--radius-md); font-size: var(--text-sm);
		font-weight: 600; cursor: pointer; font-family: inherit;
	}
	.btn-new-task:hover { background: var(--accent-hover); }
	.my-tasks-btn {
		padding: 5px 12px; background: var(--bg-raised); color: var(--text-secondary);
		border: 1px solid var(--border-default); border-radius: var(--radius-md);
		font-size: var(--text-xs); cursor: pointer; font-family: inherit; font-weight: 500;
	}
	.my-tasks-btn.active { background: var(--accent); color: var(--text-inverse); border-color: var(--accent); }

	/* Board */
	.board { flex: 1; display: flex; gap: var(--space-md); padding: var(--space-lg); overflow-x: auto; overflow-y: hidden; }
	.board-col {
		min-width: 220px; flex: 1; display: flex; flex-direction: column;
		gap: var(--space-sm); border-radius: var(--radius-lg);
	}
	.board-col-header {
		display: flex; align-items: center; justify-content: space-between;
		padding: var(--space-sm) var(--space-md); font-size: var(--text-xs);
		font-weight: 700; text-transform: uppercase; letter-spacing: 0.08em;
		color: var(--text-tertiary);
	}
	.board-count {
		background: var(--bg-raised); border: 1px solid var(--border-subtle);
		padding: 0 6px; border-radius: var(--radius-full); font-size: var(--text-xs);
	}
	.board-cards {
		flex: 1; display: flex; flex-direction: column; gap: 6px;
		overflow-y: auto; min-height: 60px; padding: 4px;
	}
	.board-empty {
		padding: var(--space-lg); text-align: center; font-size: var(--text-xs);
		color: var(--text-tertiary); border: 1px dashed var(--border-default);
		border-radius: var(--radius-md);
	}

	/* Drop indicator */
	.drop-line {
		height: 3px; background: var(--accent); border-radius: 2px;
		position: relative; margin: -1px 0;
		box-shadow: 0 0 6px var(--accent);
		animation: dropLineGlow 0.8s ease-in-out infinite alternate;
	}
	.drop-dot {
		position: absolute; left: -3px; top: -3px;
		width: 9px; height: 9px; background: var(--accent);
		border-radius: 50%; box-shadow: 0 0 6px var(--accent);
	}
	@keyframes dropLineGlow {
		from { box-shadow: 0 0 4px var(--accent); }
		to { box-shadow: 0 0 10px var(--accent); }
	}

	/* Task card */
	.task-card {
		background: var(--bg-surface); border: 1px solid var(--border-subtle);
		border-radius: var(--radius-md); padding: var(--space-md);
		cursor: grab; transition: border-color 150ms, opacity 150ms, transform 150ms;
		user-select: none;
	}
	.task-card:hover { border-color: var(--border-strong); }
	.task-card:active { cursor: grabbing; }
	.task-card.dragging { opacity: 0.3; transform: scale(0.97); }
	.task-card-header { display: flex; align-items: flex-start; gap: var(--space-sm); }
	.task-priority-dot {
		width: 8px; height: 8px; border-radius: 50%; flex-shrink: 0; margin-top: 5px;
	}
	.task-title { font-size: var(--text-sm); font-weight: 500; color: var(--text-primary); line-height: 1.4; }
	.task-desc {
		font-size: var(--text-xs); color: var(--text-secondary);
		margin: var(--space-xs) 0 0 0; line-height: 1.3;
		display: -webkit-box; -webkit-line-clamp: 2;
		-webkit-box-orient: vertical; overflow: hidden;
	}
	.task-tags { display: flex; gap: 4px; margin-top: var(--space-sm); flex-wrap: wrap; }
	.task-tag {
		font-size: 10px; padding: 1px 6px; border-radius: var(--radius-full);
		background: var(--bg-raised); border: 1px solid var(--border-subtle);
		color: var(--text-tertiary);
	}
	.task-avatar {
		width: 22px; height: 22px; border-radius: 50%; display: flex; align-items: center; justify-content: center;
		font-size: 11px; font-weight: 700; color: #fff; flex-shrink: 0; margin-left: auto;
	}
	.task-avatar-sm {
		width: 18px; height: 18px; border-radius: 50%; display: inline-flex; align-items: center; justify-content: center;
		font-size: 10px; font-weight: 700; color: #fff; flex-shrink: 0; vertical-align: middle;
	}
	.assignee-name { font-size: var(--text-xs); color: var(--text-secondary); margin-left: 4px; }
	.unassigned { color: var(--text-tertiary); font-size: var(--text-xs); }
	.task-due { font-size: var(--text-xs); color: var(--text-tertiary); margin-top: var(--space-xs); }

	/* List view */
	.task-list { flex: 1; overflow-y: auto; }
	.task-list-header {
		display: flex; align-items: center; padding: var(--space-sm) var(--space-xl);
		font-size: var(--text-xs); font-weight: 700; text-transform: uppercase;
		letter-spacing: 0.06em; color: var(--text-tertiary);
		border-bottom: 1px solid var(--border-subtle); background: var(--bg-surface);
		position: sticky; top: 0;
	}
	.task-list-row {
		display: flex; align-items: center; padding: var(--space-sm) var(--space-xl);
		border-bottom: 1px solid var(--border-subtle); transition: background 150ms;
		cursor: pointer;
	}
	.task-list-row:hover { background: var(--bg-surface); }
	.tl-pri { width: 80px; flex-shrink: 0; display: flex; align-items: center; gap: 6px; }
	.pri-label { font-size: var(--text-xs); color: var(--text-secondary); text-transform: capitalize; }
	.tl-title { flex: 1; min-width: 0; font-size: var(--text-sm); color: var(--text-primary); overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
	.tl-assignee { width: 140px; flex-shrink: 0; display: flex; align-items: center; }
	.tl-status { width: 120px; flex-shrink: 0; }
	.status-badge {
		font-size: var(--text-xs); padding: 2px 8px; border-radius: var(--radius-full);
		background: var(--bg-raised); border: 1px solid var(--border-subtle);
		color: var(--text-secondary);
	}
	.status-in_progress { color: var(--accent); border-color: var(--accent); }
	.status-done { color: #22c55e; border-color: #22c55e; }
	.tl-tags { width: 120px; flex-shrink: 0; display: flex; gap: 3px; flex-wrap: wrap; }
	.tl-date { width: 80px; flex-shrink: 0; font-size: var(--text-xs); color: var(--text-tertiary); font-family: var(--font-mono); }
	.empty-state { padding: 3rem; text-align: center; color: var(--text-tertiary); font-size: var(--text-sm); }

	/* Modal */
	.modal-overlay {
		position: fixed; top: 0; left: 0; right: 0; bottom: 0;
		background: rgba(0,0,0,0.6); z-index: 200;
		display: flex; align-items: center; justify-content: center;
	}
	.modal {
		background: var(--bg-surface); border: 1px solid var(--border-subtle);
		border-radius: var(--radius-lg, 12px); width: 480px; max-width: 90vw;
		max-height: 85vh; display: flex; flex-direction: column;
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
	.modal-close:hover { color: var(--text-primary); background: var(--bg-raised); }
	.modal-body {
		padding: 16px 20px; overflow-y: auto; flex: 1;
		display: flex; flex-direction: column; gap: 12px;
	}
	.modal-footer {
		display: flex; align-items: center; justify-content: space-between;
		padding: 12px 20px; border-top: 1px solid var(--border-subtle);
	}
	.footer-right { display: flex; gap: 8px; margin-left: auto; }
	.form-group { display: flex; flex-direction: column; gap: 4px; }
	.form-group label {
		font-size: var(--text-xs); font-weight: 600; color: var(--text-secondary);
		text-transform: uppercase; letter-spacing: 0.04em;
	}
	.form-group input, .form-group textarea, .form-group select {
		padding: 8px 10px; background: var(--bg-input, var(--bg-raised));
		color: var(--text-primary); border: 1px solid var(--border-default);
		border-radius: var(--radius-md); font-size: var(--text-sm);
		font-family: inherit; resize: none;
	}
	.form-group input:focus, .form-group textarea:focus, .form-group select:focus {
		outline: none; border-color: var(--accent);
	}
	.form-row { display: flex; gap: 12px; }
	.form-row .form-group { flex: 1; }
	.task-meta {
		display: flex; gap: 16px; font-size: var(--text-xs);
		color: var(--text-tertiary); padding-top: 8px;
		border-top: 1px solid var(--border-subtle);
	}
	.btn-save {
		padding: 7px 16px; background: var(--accent); color: var(--text-inverse);
		border: none; border-radius: var(--radius-md); font-size: var(--text-sm);
		font-weight: 600; cursor: pointer; font-family: inherit;
	}
	.btn-save:hover { background: var(--accent-hover); }
	.btn-cancel {
		padding: 7px 16px; background: none; color: var(--text-secondary);
		border: 1px solid var(--border-default); border-radius: var(--radius-md);
		font-size: var(--text-sm); cursor: pointer; font-family: inherit;
	}
	.btn-delete {
		padding: 7px 16px; color: #ef4444; background: rgba(239,68,68,0.1);
		border: 1px solid rgba(239,68,68,0.2); border-radius: var(--radius-md);
		font-size: var(--text-sm); cursor: pointer; font-family: inherit;
	}
	.btn-delete:hover { background: rgba(239,68,68,0.2); }

	/* Upcoming Tray */
	.tray-overlay {
		position: fixed; top: 0; left: 0; right: 0; bottom: 0;
		background: rgba(0,0,0,0.3); z-index: 998;
	}
	.task-tray {
		position: fixed; top: 0; right: 0; bottom: 0; width: 320px;
		background: var(--bg-surface); border-left: 1px solid var(--border-subtle);
		z-index: 999; display: flex; flex-direction: column;
		animation: slideIn 200ms ease-out;
	}
	@keyframes slideIn {
		from { transform: translateX(100%); }
		to { transform: translateX(0); }
	}
	.tray-header {
		display: flex; align-items: center; justify-content: space-between;
		padding: 16px 20px; border-bottom: 1px solid var(--border-subtle);
	}
	.tray-header h3 { font-size: var(--text-sm); font-weight: 600; margin: 0; }
	.tray-body { flex: 1; overflow-y: auto; padding: 12px 16px; }
	.tray-empty { padding: 2rem; text-align: center; color: var(--text-tertiary); font-size: var(--text-sm); }
	.tray-group { margin-bottom: 16px; }
	.tray-group-label {
		font-size: var(--text-xs); font-weight: 700; text-transform: uppercase;
		letter-spacing: 0.06em; color: var(--text-tertiary); margin-bottom: 8px;
	}
	.tray-group-label.overdue { color: #ef4444; }
	.tray-item {
		display: flex; align-items: center; gap: 8px;
		padding: 8px 10px; border-radius: var(--radius-md); cursor: pointer;
		transition: background 150ms;
	}
	.tray-item:hover { background: var(--bg-raised); }
	.tray-item-info { flex: 1; min-width: 0; }
	.tray-item-title {
		font-size: var(--text-sm); color: var(--text-primary);
		display: block; overflow: hidden; text-overflow: ellipsis; white-space: nowrap;
	}
	.tray-item-date { font-size: var(--text-xs); color: var(--text-tertiary); }

	/* Assign type toggle */
	.assign-toggle {
		display: flex; gap: 0; border: 1px solid var(--border-default);
		border-radius: var(--radius-md); overflow: hidden;
	}
	.assign-btn {
		flex: 1; padding: 6px 12px; background: var(--bg-raised);
		border: none; color: var(--text-secondary); font-size: var(--text-xs);
		font-weight: 600; cursor: pointer; font-family: inherit;
		display: flex; align-items: center; justify-content: center;
		transition: background 150ms, color 150ms;
	}
	.assign-btn:not(:last-child) { border-right: 1px solid var(--border-default); }
	.assign-btn.active { background: var(--accent); color: var(--text-inverse); }
	.assign-btn:hover:not(.active) { background: var(--bg-surface); }

	/* Agent badge on cards */
	.task-agent-badge {
		display: flex; align-items: center; justify-content: center;
		width: 22px; height: 22px; border-radius: 50%;
		background: var(--accent); color: var(--text-inverse);
		flex-shrink: 0; margin-left: auto;
	}
	.task-agent-badge.active-badge {
		background: #14b8a6;
	}
	.task-agent-badge.paused-badge {
		background: rgba(255, 255, 255, 0.1);
		color: var(--text-tertiary);
		opacity: 0.6;
	}

	/* Active agent task card */
	.task-active {
		border-color: rgba(20, 184, 166, 0.4);
		background: linear-gradient(135deg, var(--bg-surface) 0%, rgba(20, 184, 166, 0.05) 100%);
	}
	.task-active:hover { border-color: rgba(20, 184, 166, 0.6); }
	/* Robot icon — alive */
	.bot-icon {
		flex-shrink: 0; margin-top: 1px; line-height: 0;
	}
	.bot-alive .bot-antenna-tip {
		animation: antennaPulse 2s ease-in-out infinite;
	}
	.bot-alive .bot-eye-l {
		transform-origin: 7.2px 9px;
		animation: botBlink 3.5s ease-in-out infinite;
	}
	.bot-alive .bot-eye-r {
		transform-origin: 12.8px 9px;
		animation: botBlink 3.5s ease-in-out infinite;
		animation-delay: 0.1s;
	}
	@keyframes antennaPulse {
		0%, 100% { opacity: 1; filter: drop-shadow(0 0 0px #14b8a6); }
		50% { opacity: 0.6; filter: drop-shadow(0 0 3px #14b8a6); }
	}
	@keyframes botBlink {
		0%, 42%, 50%, 100% { transform: scaleY(1); }
		45%, 47% { transform: scaleY(0.1); }
	}

	/* Robot icon — off */
	.bot-off {
		opacity: 0.5;
	}

	/* Paused agent task card */
	.task-paused {
		border-color: rgba(255, 255, 255, 0.06);
		opacity: 0.55;
	}
	.task-paused:hover { opacity: 0.8; }
	.task-paused-label {
		font-size: 10px; font-weight: 600; text-transform: uppercase;
		letter-spacing: 0.05em; color: var(--text-tertiary);
		margin-top: 2px;
	}

	.task-recurrence {
		font-size: var(--text-xs); color: #14b8a6; font-weight: 600;
		margin-top: 2px;
	}
	.task-recurrence.recurrence-paused {
		color: var(--text-tertiary);
	}
	.task-countdown {
		font-size: var(--text-xs); color: #14b8a6; font-weight: 600;
		margin-top: var(--space-xs);
	}
	.task-next-muted {
		font-size: var(--text-xs); color: var(--text-tertiary);
		margin-top: var(--space-xs);
	}
	.task-runs {
		font-size: var(--text-xs); color: var(--text-tertiary);
		margin-top: var(--space-xs);
	}

	/* Delivery time presets */
	.time-presets {
		display: flex; gap: 6px; margin-bottom: 6px;
	}
	.preset-chip {
		padding: 3px 10px; background: var(--bg-raised);
		border: 1px solid var(--border-default); border-radius: var(--radius-full);
		font-size: var(--text-xs); color: var(--text-secondary);
		cursor: pointer; font-family: inherit; font-weight: 500;
	}
	.preset-chip:hover { border-color: var(--accent); color: var(--accent); }

	/* Field hint */
	.field-hint {
		font-size: var(--text-xs); color: var(--text-tertiary); font-style: italic;
	}

	/* Error agent task card */
	.task-error {
		border-color: rgba(249, 115, 22, 0.4);
		background: linear-gradient(135deg, var(--bg-surface) 0%, rgba(249, 115, 22, 0.05) 100%);
	}
	.task-error:hover { border-color: rgba(249, 115, 22, 0.6); }
	.bot-error .bot-antenna-tip {
		animation: antennaPulseError 1.5s ease-in-out infinite;
	}
	@keyframes antennaPulseError {
		0%, 100% { opacity: 1; filter: drop-shadow(0 0 0px #f97316); }
		50% { opacity: 0.6; filter: drop-shadow(0 0 3px #f97316); }
	}

	/* Execution history section */
	.runs-section {
		border-top: 1px solid var(--border-subtle);
		padding-top: 8px;
	}
	.runs-header {
		font-size: var(--text-xs); font-weight: 700; text-transform: uppercase;
		letter-spacing: 0.04em; color: var(--text-tertiary); margin-bottom: 8px;
	}
	.run-row {
		display: flex; align-items: center; gap: 8px;
		padding: 6px 8px; border-radius: var(--radius-md);
		cursor: pointer; transition: background 150ms;
		font-size: var(--text-xs);
	}
	.run-row:hover { background: var(--bg-raised); }
	.run-status { flex-shrink: 0; display: flex; align-items: center; }
	.run-success { color: #22c55e; }
	.run-error { color: #ef4444; }
	.run-time { color: var(--text-tertiary); white-space: nowrap; }
	.run-duration {
		background: var(--bg-raised); border: 1px solid var(--border-subtle);
		padding: 0 5px; border-radius: var(--radius-full);
		color: var(--text-tertiary); font-family: var(--font-mono);
		font-size: 10px; white-space: nowrap;
	}
	.run-output-preview {
		flex: 1; min-width: 0; color: var(--text-secondary);
		overflow: hidden; text-overflow: ellipsis; white-space: nowrap;
	}
	.run-output-full {
		padding: 8px 10px; margin: 4px 0 8px 22px;
		background: var(--bg-raised); border: 1px solid var(--border-subtle);
		border-radius: var(--radius-md); font-size: var(--text-xs);
		color: var(--text-secondary); white-space: pre-wrap;
		word-break: break-word; max-height: 200px; overflow-y: auto;
		font-family: var(--font-mono);
	}
	.runs-loading {
		font-size: var(--text-xs); color: var(--text-tertiary);
		padding: 8px 0; border-top: 1px solid var(--border-subtle);
	}

	/* Agent tray items */
	.agent-tray-status {
		flex-shrink: 0; display: flex; align-items: center;
		color: var(--text-tertiary);
	}
	.agent-tray-status.status-ok { color: #14b8a6; }
	.agent-tray-status.status-err { color: #f97316; }
	.tray-item-subtitle {
		font-size: 11px; color: var(--text-tertiary); display: block; margin-top: 1px;
	}
	.subtitle-err { color: #f97316; font-weight: 600; }
	.subtitle-ok { color: #22c55e; }
	.tray-countdown {
		flex-shrink: 0; font-size: 11px; font-weight: 600;
		color: #14b8a6; font-family: var(--font-mono);
		white-space: nowrap;
	}
</style>
