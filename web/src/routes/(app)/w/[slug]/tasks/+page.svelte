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
	let showNewTask = $state(false);
	let newTaskTitle = $state('');
	let newTaskPriority = $state('medium');
	let newTaskStatus = $state('backlog');
	let newTaskDescription = $state('');
	let editingTask = $state<any>(null);

	// Drag state
	let draggedTask = $state<any>(null);
	let dropTarget = $state<{ status: string; index: number } | null>(null);

	let unsubWS: (() => void) | null = null;

	function tasksForStatus(status: string): any[] {
		return tasks.filter(t => t.status === status).sort((a, b) => (a.position ?? 0) - (b.position ?? 0));
	}

	onMount(async () => {
		const saved = localStorage.getItem('nexus_tasks_view');
		if (saved === 'list' || saved === 'board') viewMode = saved;

		connect();
		unsubWS = onMessage(handleWS);
		await loadTasks();
	});

	onDestroy(() => {
		if (unsubWS) unsubWS();
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

	async function handleCreateTask() {
		if (!newTaskTitle.trim()) return;
		try {
			await createTask(slug, {
				title: newTaskTitle.trim(),
				priority: newTaskPriority,
				status: newTaskStatus,
				description: newTaskDescription.trim() || undefined,
			});
			newTaskTitle = '';
			newTaskPriority = 'medium';
			newTaskStatus = 'backlog';
			newTaskDescription = '';
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

	function setViewMode(mode: ViewMode) {
		viewMode = mode;
		localStorage.setItem('nexus_tasks_view', mode);
	}

	function formatDate(iso: string) {
		return new Date(iso).toLocaleDateString([], { month: 'short', day: 'numeric' });
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
		// Only set if not already over a card
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

		// Calculate new position
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

		// Optimistic update
		tasks = tasks.map(t => t.id === taskId ? { ...t, status: targetStatus, position: newPosition } : t);
		draggedTask = null;
		dropTarget = null;

		try {
			const updates: Record<string, any> = { position: newPosition };
			if (oldStatus !== targetStatus) updates.status = targetStatus;
			await updateTask(slug, taskId, updates);
		} catch (e: any) {
			// Revert
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
			<button class="btn-new-task" onclick={() => showNewTask = !showNewTask}>+ New Task</button>
		</div>
	</header>

	{#if showNewTask}
		<div class="new-task-form">
			<input type="text" placeholder="Task title..." bind:value={newTaskTitle} onkeydown={(e) => e.key === 'Enter' && handleCreateTask()} />
			<textarea placeholder="Description (optional)..." bind:value={newTaskDescription} rows="2"></textarea>
			<div class="new-task-row">
				<select bind:value={newTaskPriority}>
					{#each PRIORITIES as p}<option value={p}>{p}</option>{/each}
				</select>
				<select bind:value={newTaskStatus}>
					{#each STATUSES as s}<option value={s}>{STATUS_LABELS[s]}</option>{/each}
				</select>
				<button class="btn-create" onclick={handleCreateTask}>Create</button>
				<button class="btn-cancel" onclick={() => showNewTask = false}>Cancel</button>
			</div>
		</div>
	{/if}

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
							<!-- Drop line before this card -->
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
								onclick={() => editingTask = editingTask?.id === task.id ? null : task}
							>
								<div class="task-card-header">
									<span class="task-priority-dot" style="background: {PRIORITY_COLORS[task.priority]}"></span>
									<span class="task-title">{task.title}</span>
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
						<!-- Drop line at the end -->
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
					<span class="tl-date">{formatDate(task.created_at)}</span>
					<span class="tl-actions">
						<button class="btn-del-sm" onclick={() => handleDeleteTask(task.id)} title="Delete">&#x2715;</button>
					</span>
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

	.new-task-form {
		display: flex; flex-direction: column; gap: var(--space-sm);
		padding: var(--space-md) var(--space-xl);
		background: var(--bg-surface); border-bottom: 1px solid var(--border-subtle);
	}
	.new-task-form input, .new-task-form textarea {
		padding: 8px 12px; background: var(--bg-input); color: var(--text-primary);
		border: 1px solid var(--border-default); border-radius: var(--radius-md);
		font-size: var(--text-sm); font-family: inherit; resize: none;
	}
	.new-task-form input:focus, .new-task-form textarea:focus { outline: none; border-color: var(--accent); }
	.new-task-row { display: flex; gap: var(--space-sm); align-items: center; }
	.new-task-row select {
		padding: 6px 8px; background: var(--bg-raised); color: var(--text-primary);
		border: 1px solid var(--border-default); border-radius: var(--radius-md);
		font-size: var(--text-xs); font-family: inherit;
	}
	.btn-create {
		padding: 6px 14px; background: var(--accent); color: var(--text-inverse);
		border: none; border-radius: var(--radius-md); font-size: var(--text-sm);
		font-weight: 600; cursor: pointer; font-family: inherit;
	}
	.btn-cancel {
		padding: 6px 14px; background: none; color: var(--text-secondary);
		border: 1px solid var(--border-default); border-radius: var(--radius-md);
		font-size: var(--text-sm); cursor: pointer; font-family: inherit;
	}

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

	/* Drop indicator line */
	.drop-line {
		height: 3px;
		background: var(--accent);
		border-radius: 2px;
		position: relative;
		margin: -1px 0;
		box-shadow: 0 0 6px var(--accent);
		animation: dropLineGlow 0.8s ease-in-out infinite alternate;
	}
	.drop-dot {
		position: absolute;
		left: -3px;
		top: -3px;
		width: 9px;
		height: 9px;
		background: var(--accent);
		border-radius: 50%;
		box-shadow: 0 0 6px var(--accent);
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
	.task-due { font-size: var(--text-xs); color: var(--text-tertiary); margin-top: var(--space-xs); }
	.task-card-actions {
		display: flex; gap: 4px; margin-top: var(--space-sm);
		padding-top: var(--space-sm); border-top: 1px solid var(--border-subtle);
	}
	.task-card-actions select {
		flex: 1; padding: 3px 4px; background: var(--bg-raised);
		color: var(--text-primary); border: 1px solid var(--border-default);
		border-radius: var(--radius-sm); font-size: 10px; font-family: inherit;
	}
	.btn-del {
		padding: 3px 8px; font-size: 10px; color: var(--red);
		background: rgba(239,68,68,0.1); border: 1px solid rgba(239,68,68,0.2);
		border-radius: var(--radius-sm); cursor: pointer; font-family: inherit;
	}
	.btn-del:hover { background: rgba(239,68,68,0.2); }

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
	}
	.task-list-row:hover { background: var(--bg-surface); }
	.tl-pri { width: 80px; flex-shrink: 0; }
	.tl-pri select {
		padding: 2px 4px; background: var(--bg-raised); color: var(--text-primary);
		border: 1px solid var(--border-default); border-radius: var(--radius-sm);
		font-size: var(--text-xs); font-family: inherit;
	}
	.tl-title { flex: 1; min-width: 0; font-size: var(--text-sm); color: var(--text-primary); overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
	.tl-status { width: 120px; flex-shrink: 0; }
	.tl-status select {
		padding: 2px 4px; background: var(--bg-raised); color: var(--text-primary);
		border: 1px solid var(--border-default); border-radius: var(--radius-sm);
		font-size: var(--text-xs); font-family: inherit;
	}
	.tl-tags { width: 120px; flex-shrink: 0; display: flex; gap: 3px; flex-wrap: wrap; }
	.tl-date { width: 80px; flex-shrink: 0; font-size: var(--text-xs); color: var(--text-tertiary); font-family: var(--font-mono); }
	.tl-actions { width: 40px; flex-shrink: 0; text-align: center; }
	.btn-del-sm {
		width: 20px; height: 20px; display: inline-flex; align-items: center; justify-content: center;
		border-radius: var(--radius-sm); color: var(--text-tertiary);
		font-size: 12px; cursor: pointer; background: none; border: none;
	}
	.btn-del-sm:hover { color: var(--red); background: rgba(239,68,68,0.1); }
	.empty-state { padding: 3rem; text-align: center; color: var(--text-tertiary); font-size: var(--text-sm); }
</style>
