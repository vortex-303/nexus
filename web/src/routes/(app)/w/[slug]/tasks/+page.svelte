<script lang="ts">
	import { page } from '$app/state';
	import { goto } from '$app/navigation';
	import { onMount, onDestroy } from 'svelte';
	import { listTasks, createTask, updateTask, deleteTask, getCurrentUser } from '$lib/api';
	import { connect, disconnect, onMessage } from '$lib/ws';
	import { members } from '$lib/stores/workspace';

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

	// Upcoming tray
	let showTaskTray = $state(false);

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
			tasks = tasks.map(t => t.id === payload.id ? payload : t);
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
		showTaskModal = true;
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
		showTaskModal = true;
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
		if (formAssignee) taskData.assignee_id = formAssignee;
		else taskData.assignee_id = '';

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
			<button class="tray-btn" class:active={showTaskTray} onclick={() => showTaskTray = !showTaskTray} title="Upcoming deadlines">
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
							<div
								class="task-card"
								class:dragging={draggedTask?.id === task.id}
								draggable="true"
								ondragstart={(e) => handleDragStart(e, task)}
								ondragover={(e) => handleCardDragOver(e, status, i)}
								ondragend={handleDragEnd}
								onclick={() => openEditTask(task)}
							>
								<div class="task-card-header">
									<span class="task-priority-dot" style="background: {PRIORITY_COLORS[task.priority]}"></span>
									<span class="task-title">{task.title}</span>
									{#if task.assignee_id}
										{@const assignee = getMember(task.assignee_id)}
										<span class="task-avatar" style="background: {memberColor(assignee)}" title={assignee?.display_name || 'Unknown'}>{memberInitial(assignee)}</span>
									{/if}
								</div>
								{#if task.description}
									<p class="task-desc">{task.description}</p>
								{/if}
								{#if task.tags?.length > 0}
									<div class="task-tags">
										{#each task.tags as tag}<span class="task-tag">{tag}</span>{/each}
									</div>
								{/if}
								{#if task.due_date}
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
				<h3>{editingTask ? 'Edit Task' : 'New Task'}</h3>
				<button class="modal-close" onclick={() => showTaskModal = false}>&times;</button>
			</div>
			<div class="modal-body">
				<div class="form-group">
					<label for="task-title">Title</label>
					<input id="task-title" type="text" placeholder="Task title..." bind:value={formTitle} onkeydown={(e) => e.key === 'Enter' && saveTask()} autofocus />
				</div>
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
					</div>
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
</style>
