<script lang="ts">
	import { page } from '$app/state';
	import { goto } from '$app/navigation';
	import { onMount, onDestroy } from 'svelte';
	import { listCalendarEvents, createCalendarEvent, updateCalendarEvent, deleteCalendarEvent, getCurrentUser } from '$lib/api';
	import { connect, disconnect, onMessage } from '$lib/ws';

	let slug = $derived(page.params.slug);
	let currentUser = $state(getCurrentUser());

	type ViewMode = 'month' | 'week' | 'agenda';
	let viewMode = $state<ViewMode>('month');
	let events = $state<any[]>([]);
	let currentDate = $state(new Date());
	let showEventModal = $state(false);
	let editingEvent = $state<any>(null);
	let selectedSlot = $state<{ start: string; end: string } | null>(null);

	// Modal form state
	let formTitle = $state('');
	let formDescription = $state('');
	let formLocation = $state('');
	let formStartDate = $state('');
	let formStartTime = $state('');
	let formEndDate = $state('');
	let formEndTime = $state('');
	let formAllDay = $state(false);
	let formColor = $state('');
	let formRecurrence = $state('');

	const COLORS = [
		{ value: '', label: 'Default', hex: 'var(--accent)' },
		{ value: '#3b82f6', label: 'Blue', hex: '#3b82f6' },
		{ value: '#10b981', label: 'Green', hex: '#10b981' },
		{ value: '#8b5cf6', label: 'Purple', hex: '#8b5cf6' },
		{ value: '#ef4444', label: 'Red', hex: '#ef4444' },
		{ value: '#f59e0b', label: 'Amber', hex: '#f59e0b' },
		{ value: '#ec4899', label: 'Pink', hex: '#ec4899' },
	];

	const RECURRENCE_OPTIONS = [
		{ value: '', label: 'Does not repeat' },
		{ value: 'FREQ=DAILY', label: 'Daily' },
		{ value: 'FREQ=WEEKLY', label: 'Weekly' },
		{ value: 'FREQ=WEEKLY;BYDAY=MO,TU,WE,TH,FR', label: 'Every weekday' },
		{ value: 'FREQ=MONTHLY', label: 'Monthly' },
		{ value: 'FREQ=YEARLY', label: 'Yearly' },
	];

	let unsubWS: (() => void) | null = null;
	let now = $state(new Date());
	let nowTimer: ReturnType<typeof setInterval> | null = null;

	// Derived date ranges
	let viewStart = $derived.by(() => {
		const d = new Date(currentDate);
		if (viewMode === 'month') {
			const first = new Date(d.getFullYear(), d.getMonth(), 1);
			first.setDate(first.getDate() - first.getDay()); // start from Sunday
			return first;
		} else if (viewMode === 'week') {
			const day = d.getDay();
			const start = new Date(d);
			start.setDate(d.getDate() - day);
			start.setHours(0, 0, 0, 0);
			return start;
		}
		// agenda: show from today
		const today = new Date(d);
		today.setHours(0, 0, 0, 0);
		return today;
	});

	let viewEnd = $derived.by(() => {
		const start = new Date(viewStart);
		if (viewMode === 'month') {
			start.setDate(start.getDate() + 42); // 6 weeks
			return start;
		} else if (viewMode === 'week') {
			start.setDate(start.getDate() + 7);
			return start;
		}
		// agenda: 30 days
		start.setDate(start.getDate() + 30);
		return start;
	});

	let monthLabel = $derived(currentDate.toLocaleDateString('en-US', { month: 'long', year: 'numeric' }));

	// Month grid: 6 rows x 7 cols
	let monthDays = $derived.by(() => {
		const days: Date[] = [];
		const d = new Date(viewStart);
		for (let i = 0; i < 42; i++) {
			days.push(new Date(d));
			d.setDate(d.getDate() + 1);
		}
		return days;
	});

	// Week hours
	let weekDays = $derived.by(() => {
		const days: Date[] = [];
		const d = new Date(viewStart);
		for (let i = 0; i < 7; i++) {
			days.push(new Date(d));
			d.setDate(d.getDate() + 1);
		}
		return days;
	});

	// Agenda grouped by day
	let agendaDays = $derived.by(() => {
		const grouped: Map<string, any[]> = new Map();
		const sorted = [...events].sort((a, b) => a.start_time.localeCompare(b.start_time));
		for (const evt of sorted) {
			const day = evt.start_time.slice(0, 10);
			if (!grouped.has(day)) grouped.set(day, []);
			grouped.get(day)!.push(evt);
		}
		return grouped;
	});

	// Now-marker for week view: which day column (0-6, or -1 if not visible) and vertical %
	let nowDayCol = $derived.by(() => {
		if (viewMode !== 'week') return -1;
		const todayStr = dateToISO(now);
		const idx = weekDays.findIndex(d => dateToISO(d) === todayStr);
		return idx;
	});
	// 48px = min-height of each week-row
	let nowTopPx = $derived.by(() => {
		return (now.getHours() + now.getMinutes() / 60) * 48;
	});

	function eventsForDay(date: Date): any[] {
		const dayStr = dateToISO(date);
		return events.filter(e => {
			const eStart = dateToISO(new Date(e.start_time));
			const eEnd = dateToISO(new Date(e.end_time));
			return dayStr >= eStart && dayStr <= eEnd;
		});
	}

	function eventsForHour(date: Date, hour: number): any[] {
		return events.filter(e => {
			const eStart = new Date(e.start_time);
			const eEnd = new Date(e.end_time);
			const slotStart = new Date(date);
			slotStart.setHours(hour, 0, 0, 0);
			const slotEnd = new Date(date);
			slotEnd.setHours(hour + 1, 0, 0, 0);
			return eStart < slotEnd && eEnd > slotStart;
		});
	}

	function dateToISO(d: Date): string {
		const y = d.getFullYear();
		const m = String(d.getMonth() + 1).padStart(2, '0');
		const day = String(d.getDate()).padStart(2, '0');
		return `${y}-${m}-${day}`;
	}

	function isToday(d: Date): boolean {
		const today = new Date();
		return d.getDate() === today.getDate() && d.getMonth() === today.getMonth() && d.getFullYear() === today.getFullYear();
	}

	function isCurrentMonth(d: Date): boolean {
		return d.getMonth() === currentDate.getMonth();
	}

	function navigate(direction: number) {
		const d = new Date(currentDate);
		if (viewMode === 'month') {
			d.setMonth(d.getMonth() + direction);
		} else if (viewMode === 'week') {
			d.setDate(d.getDate() + direction * 7);
		} else {
			d.setDate(d.getDate() + direction * 7);
		}
		currentDate = d;
	}

	function goToday() {
		currentDate = new Date();
	}

	// Event loading
	async function loadEvents() {
		try {
			const data = await listCalendarEvents(slug, {
				start: viewStart.toISOString(),
				end: viewEnd.toISOString(),
			});
			events = data.events || [];
		} catch {
			events = [];
		}
	}

	$effect(() => {
		// Reload when view range changes
		viewStart; viewEnd;
		loadEvents();
	});

	// WebSocket handler — reload from API to ensure consistency (timezone, recurring expansion, etc.)
	function handleWS(type: string, payload: any) {
		if (type === 'event.created' || type === 'event.updated' || type === 'event.deleted') {
			loadEvents();
		}
	}

	onMount(() => {
		const saved = localStorage.getItem('nexus_cal_view');
		if (saved === 'month' || saved === 'week' || saved === 'agenda') viewMode = saved;
		connect();
		unsubWS = onMessage(handleWS);
		nowTimer = setInterval(() => { now = new Date(); }, 30000);
	});

	onDestroy(() => {
		if (unsubWS) unsubWS();
		if (nowTimer) clearInterval(nowTimer);
		disconnect();
	});

	// Modal
	function openNewEvent(start?: string, end?: string) {
		editingEvent = null;
		const now = new Date();
		const startDate = start ? new Date(start) : new Date(now.getFullYear(), now.getMonth(), now.getDate(), now.getHours() + 1);
		const endDate = end ? new Date(end) : new Date(startDate.getTime() + 60 * 60 * 1000);
		formTitle = '';
		formDescription = '';
		formLocation = '';
		formStartDate = dateToISO(startDate);
		formStartTime = pad(startDate.getHours()) + ':' + pad(startDate.getMinutes());
		formEndDate = dateToISO(endDate);
		formEndTime = pad(endDate.getHours()) + ':' + pad(endDate.getMinutes());
		formAllDay = false;
		formColor = '';
		formRecurrence = '';
		showEventModal = true;
	}

	function openEditEvent(evt: any) {
		// Don't edit synthetic recurring instances directly
		if (evt.recurrence_parent_id && evt.id.includes('_')) {
			evt = { ...evt, id: evt.recurrence_parent_id };
		}
		editingEvent = evt;
		formTitle = evt.title;
		formDescription = evt.description || '';
		formLocation = evt.location || '';
		const start = new Date(evt.start_time);
		const end = new Date(evt.end_time);
		formStartDate = dateToISO(start);
		formStartTime = pad(start.getHours()) + ':' + pad(start.getMinutes());
		formEndDate = dateToISO(end);
		formEndTime = pad(end.getHours()) + ':' + pad(end.getMinutes());
		formAllDay = evt.all_day;
		formColor = evt.color || '';
		formRecurrence = evt.recurrence_rule || '';
		showEventModal = true;
	}

	function pad(n: number): string {
		return n.toString().padStart(2, '0');
	}

	function localToISO(date: string, time: string): string {
		const d = new Date(`${date}T${time}:00`);
		return d.toISOString();
	}

	async function saveEvent() {
		if (!formTitle.trim()) return;
		const startTime = formAllDay
			? localToISO(formStartDate, '00:00')
			: localToISO(formStartDate, formStartTime);
		const endTime = formAllDay
			? localToISO(formEndDate, '23:59')
			: localToISO(formEndDate, formEndTime);

		try {
			if (editingEvent) {
				await updateCalendarEvent(slug, editingEvent.id, {
					title: formTitle,
					description: formDescription,
					location: formLocation,
					start_time: startTime,
					end_time: endTime,
					all_day: formAllDay,
					color: formColor,
					recurrence_rule: formRecurrence,
				});
			} else {
				await createCalendarEvent(slug, {
					title: formTitle,
					description: formDescription,
					location: formLocation,
					start_time: startTime,
					end_time: endTime,
					all_day: formAllDay,
					color: formColor,
					recurrence_rule: formRecurrence,
				});
			}
			showEventModal = false;
		} catch (err: any) {
			alert(err.message || 'Failed to save event');
		}
	}

	async function removeEvent() {
		if (!editingEvent) return;
		if (!confirm('Delete this event?')) return;
		try {
			await deleteCalendarEvent(slug, editingEvent.id);
			showEventModal = false;
		} catch (err: any) {
			alert(err.message || 'Failed to delete event');
		}
	}

	function handleDayClick(date: Date) {
		const start = new Date(date);
		start.setHours(9, 0, 0, 0);
		openNewEvent(start.toISOString());
	}

	function handleSlotClick(date: Date, hour: number) {
		const start = new Date(date);
		start.setHours(hour, 0, 0, 0);
		const end = new Date(start);
		end.setHours(hour + 1, 0, 0, 0);
		openNewEvent(start.toISOString(), end.toISOString());
	}

	function eventColor(evt: any): string {
		return evt.color || evt.display_color || 'var(--accent)';
	}

	function formatEventTime(iso: string): string {
		const d = new Date(iso);
		return d.toLocaleTimeString('en-US', { hour: 'numeric', minute: '2-digit', hour12: true });
	}

	function formatAgendaDate(dateStr: string): string {
		const d = new Date(dateStr + 'T12:00:00');
		return d.toLocaleDateString('en-US', { weekday: 'long', month: 'long', day: 'numeric' });
	}

	function switchView(mode: ViewMode) {
		viewMode = mode;
		localStorage.setItem('nexus_cal_view', mode);
	}
</script>

<div class="calendar-page">
	<header class="cal-header">
		<div class="cal-header-left">
			<button class="back-btn" onclick={() => goto(`/w/${slug}`)}>
				<svg width="16" height="16" viewBox="0 0 16 16" fill="none"><path d="M10 12L6 8L10 4" stroke="currentColor" stroke-width="1.5" stroke-linecap="round" stroke-linejoin="round"/></svg>
			</button>
			<h1>Calendar</h1>
		</div>
		<div class="cal-nav">
			<button class="nav-btn" onclick={() => navigate(-1)}>
				<svg width="14" height="14" viewBox="0 0 14 14" fill="none"><path d="M8.5 10.5L5 7L8.5 3.5" stroke="currentColor" stroke-width="1.5" stroke-linecap="round" stroke-linejoin="round"/></svg>
			</button>
			<button class="today-btn" onclick={goToday}>Today</button>
			<button class="nav-btn" onclick={() => navigate(1)}>
				<svg width="14" height="14" viewBox="0 0 14 14" fill="none"><path d="M5.5 3.5L9 7L5.5 10.5" stroke="currentColor" stroke-width="1.5" stroke-linecap="round" stroke-linejoin="round"/></svg>
			</button>
			<span class="month-label">{monthLabel}</span>
		</div>
		<div class="cal-header-right">
			<div class="view-toggle">
				<button class:active={viewMode === 'month'} onclick={() => switchView('month')}>Month</button>
				<button class:active={viewMode === 'week'} onclick={() => switchView('week')}>Week</button>
				<button class:active={viewMode === 'agenda'} onclick={() => switchView('agenda')}>Agenda</button>
			</div>
			<button class="new-event-btn" onclick={() => openNewEvent()}>+ Event</button>
		</div>
	</header>

	{#if viewMode === 'month'}
		<div class="month-grid">
			<div class="month-header">
				{#each ['Sun', 'Mon', 'Tue', 'Wed', 'Thu', 'Fri', 'Sat'] as day}
					<div class="day-header">{day}</div>
				{/each}
			</div>
			<div class="month-body">
				{#each monthDays as day, i}
					<button class="day-cell" class:other-month={!isCurrentMonth(day)} class:today={isToday(day)} onclick={() => handleDayClick(day)}>
						<span class="day-number">{day.getDate()}</span>
						<div class="day-events">
							{#each eventsForDay(day).slice(0, 3) as evt}
								<button class="day-event" style="background: {eventColor(evt)}; color: #fff;" onclick={(e) => { e.stopPropagation(); openEditEvent(evt); }} title={evt.title}>
									{#if !evt.all_day}<span class="evt-time">{formatEventTime(evt.start_time)}</span>{/if}
									{evt.title}
								</button>
							{/each}
							{#if eventsForDay(day).length > 3}
								<span class="more-events">+{eventsForDay(day).length - 3} more</span>
							{/if}
						</div>
					</button>
				{/each}
			</div>
		</div>

	{:else if viewMode === 'week'}
		<div class="week-grid">
			<div class="week-header">
				<div class="time-gutter-header"></div>
				{#each weekDays as day}
					<div class="week-day-header" class:today={isToday(day)}>
						<span class="wk-day-name">{day.toLocaleDateString('en-US', { weekday: 'short' })}</span>
						<span class="wk-day-num">{day.getDate()}</span>
					</div>
				{/each}
			</div>
			<div class="week-body" style="position: relative;">
				{#if nowDayCol >= 0}
					<div class="now-marker" style="top: {nowTopPx}px; left: calc(60px + {nowDayCol} * (100% - 60px) / 7); width: calc((100% - 60px) / 7);">
						<div class="now-dot"></div>
						<div class="now-line"></div>
					</div>
				{/if}
				{#each Array(24) as _, hour}
					<div class="week-row">
						<div class="time-gutter">{hour === 0 ? '12 AM' : hour < 12 ? `${hour} AM` : hour === 12 ? '12 PM' : `${hour - 12} PM`}</div>
						{#each weekDays as day}
							<button class="week-cell" onclick={() => handleSlotClick(day, hour)}>
								{#each eventsForHour(day, hour) as evt}
									<button class="week-event" style="background: {eventColor(evt)}; color: #fff;" onclick={(e) => { e.stopPropagation(); openEditEvent(evt); }} title={evt.title}>
										{formatEventTime(evt.start_time)} {evt.title}
									</button>
								{/each}
							</button>
						{/each}
					</div>
				{/each}
			</div>
		</div>

	{:else}
		<div class="agenda-view">
			{#if agendaDays.size === 0}
				<div class="empty-state">No upcoming events</div>
			{/if}
			{#each [...agendaDays.entries()] as [day, dayEvents]}
				<div class="agenda-day">
					<h3 class="agenda-date">{formatAgendaDate(day)}</h3>
					{#each dayEvents as evt}
						<button class="agenda-event" onclick={() => openEditEvent(evt)}>
							<div class="agenda-color" style="background: {eventColor(evt)}"></div>
							<div class="agenda-info">
								<div class="agenda-title">{evt.title}</div>
								<div class="agenda-time">
									{#if evt.all_day}
										All day
									{:else}
										{formatEventTime(evt.start_time)} - {formatEventTime(evt.end_time)}
									{/if}
									{#if evt.location}<span class="agenda-loc"> · {evt.location}</span>{/if}
								</div>
							</div>
							{#if evt.recurrence_rule}
								<svg class="recur-icon" width="14" height="14" viewBox="0 0 14 14" fill="none"><path d="M11 4H3M11 4L9 2M11 4L9 6M3 10H11M3 10L5 8M3 10L5 12" stroke="currentColor" stroke-width="1.2" stroke-linecap="round" stroke-linejoin="round"/></svg>
							{/if}
						</button>
					{/each}
				</div>
			{/each}
		</div>
	{/if}
</div>

<!-- Event Modal -->
{#if showEventModal}
	<div class="modal-overlay" onclick={() => showEventModal = false} role="dialog">
		<div class="modal" onclick={(e) => e.stopPropagation()}>
			<div class="modal-header">
				<h2>{editingEvent ? 'Edit Event' : 'New Event'}</h2>
				<button class="modal-close" onclick={() => showEventModal = false}>&times;</button>
			</div>
			<div class="modal-body">
				<input class="input" type="text" placeholder="Event title" bind:value={formTitle} autofocus />

				<div class="form-row">
					<label class="checkbox-label">
						<input type="checkbox" bind:checked={formAllDay} />
						All day
					</label>
				</div>

				<div class="form-row">
					<div class="form-group">
						<label>Start</label>
						<div class="datetime-row">
							<input class="input" type="date" bind:value={formStartDate} />
							{#if !formAllDay}
								<input class="input time-input" type="time" bind:value={formStartTime} />
							{/if}
						</div>
					</div>
					<div class="form-group">
						<label>End</label>
						<div class="datetime-row">
							<input class="input" type="date" bind:value={formEndDate} />
							{#if !formAllDay}
								<input class="input time-input" type="time" bind:value={formEndTime} />
							{/if}
						</div>
					</div>
				</div>

				<input class="input" type="text" placeholder="Location" bind:value={formLocation} />
				<textarea class="input textarea" placeholder="Description" bind:value={formDescription} rows="3"></textarea>

				<div class="form-row">
					<div class="form-group">
						<label>Repeat</label>
						<select class="input" bind:value={formRecurrence}>
							{#each RECURRENCE_OPTIONS as opt}
								<option value={opt.value}>{opt.label}</option>
							{/each}
						</select>
					</div>
					<div class="form-group">
						<label>Color</label>
						<div class="color-picker">
							{#each COLORS as c}
								<button class="color-dot" class:active={formColor === c.value} style="background: {c.hex}" onclick={() => formColor = c.value} title={c.label}></button>
							{/each}
						</div>
					</div>
				</div>
			</div>
			<div class="modal-footer">
				{#if editingEvent}
					<button class="btn btn-danger" onclick={removeEvent}>Delete</button>
				{/if}
				<div class="spacer"></div>
				<button class="btn btn-ghost" onclick={() => showEventModal = false}>Cancel</button>
				<button class="btn btn-primary" onclick={saveEvent} disabled={!formTitle.trim()}>
					{editingEvent ? 'Save' : 'Create'}
				</button>
			</div>
		</div>
	</div>
{/if}

<style>
	.calendar-page {
		display: flex;
		flex-direction: column;
		height: 100vh;
		background: var(--bg-root);
		color: var(--text-primary);
	}

	.cal-header {
		display: flex;
		align-items: center;
		justify-content: space-between;
		padding: 12px 20px;
		border-bottom: 1px solid var(--border-subtle);
		gap: 16px;
		flex-shrink: 0;
	}

	.cal-header-left {
		display: flex;
		align-items: center;
		gap: 12px;
	}

	.cal-header-left h1 {
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

	.cal-nav {
		display: flex;
		align-items: center;
		gap: 8px;
	}

	.nav-btn {
		background: none;
		border: 1px solid var(--border-default);
		color: var(--text-secondary);
		cursor: pointer;
		padding: 4px 8px;
		border-radius: 6px;
	}
	.nav-btn:hover { background: var(--bg-raised); color: var(--text-primary); }

	.today-btn {
		background: none;
		border: 1px solid var(--border-default);
		color: var(--text-secondary);
		cursor: pointer;
		padding: 4px 12px;
		border-radius: 6px;
		font-size: var(--text-sm);
	}
	.today-btn:hover { background: var(--bg-raised); color: var(--text-primary); }

	.month-label {
		font-size: var(--text-base);
		font-weight: 500;
		color: var(--text-primary);
		min-width: 160px;
	}

	.cal-header-right {
		display: flex;
		align-items: center;
		gap: 12px;
	}

	.view-toggle {
		display: flex;
		border: 1px solid var(--border-default);
		border-radius: 6px;
		overflow: hidden;
	}

	.view-toggle button {
		background: none;
		border: none;
		color: var(--text-secondary);
		padding: 4px 12px;
		font-size: var(--text-sm);
		cursor: pointer;
		border-right: 1px solid var(--border-default);
	}
	.view-toggle button:last-child { border-right: none; }
	.view-toggle button:hover { background: var(--bg-raised); }
	.view-toggle button.active { background: var(--accent); color: var(--text-inverse); }

	.new-event-btn {
		background: var(--accent);
		color: var(--text-inverse);
		border: none;
		padding: 6px 14px;
		border-radius: 6px;
		font-size: var(--text-sm);
		font-weight: 500;
		cursor: pointer;
	}
	.new-event-btn:hover { background: var(--accent-hover); }

	/* Month Grid */
	.month-grid {
		flex: 1;
		display: flex;
		flex-direction: column;
		overflow: hidden;
	}

	.month-header {
		display: grid;
		grid-template-columns: repeat(7, 1fr);
		border-bottom: 1px solid var(--border-subtle);
	}

	.day-header {
		text-align: center;
		padding: 8px;
		font-size: var(--text-xs);
		color: var(--text-tertiary);
		text-transform: uppercase;
		letter-spacing: 0.05em;
	}

	.month-body {
		flex: 1;
		display: grid;
		grid-template-columns: repeat(7, 1fr);
		grid-template-rows: repeat(6, 1fr);
	}

	.day-cell {
		border: none;
		background: none;
		border-right: 1px solid var(--border-subtle);
		border-bottom: 1px solid var(--border-subtle);
		padding: 4px;
		text-align: left;
		cursor: pointer;
		display: flex;
		flex-direction: column;
		overflow: hidden;
		color: var(--text-primary);
		min-height: 0;
	}
	.day-cell:hover { background: var(--bg-surface); }
	.day-cell.other-month { opacity: 0.4; }
	.day-cell.today .day-number {
		background: var(--accent);
		color: var(--text-inverse);
		border-radius: 50%;
		width: 24px;
		height: 24px;
		display: flex;
		align-items: center;
		justify-content: center;
	}

	.day-number {
		font-size: var(--text-sm);
		font-weight: 500;
		padding: 2px;
		flex-shrink: 0;
	}

	.day-events {
		flex: 1;
		overflow: hidden;
		display: flex;
		flex-direction: column;
		gap: 1px;
	}

	.day-event {
		font-size: 0.65rem;
		padding: 1px 4px;
		border-radius: 3px;
		white-space: nowrap;
		overflow: hidden;
		text-overflow: ellipsis;
		cursor: pointer;
		border: none;
		text-align: left;
		line-height: 1.4;
	}
	.day-event:hover { filter: brightness(1.2); }

	.evt-time {
		font-size: 0.6rem;
		opacity: 0.8;
		margin-right: 2px;
	}

	.more-events {
		font-size: 0.6rem;
		color: var(--text-tertiary);
		padding: 1px 4px;
	}

	/* Week Grid */
	.week-grid {
		flex: 1;
		display: flex;
		flex-direction: column;
		overflow: hidden;
	}

	.week-header {
		display: grid;
		grid-template-columns: 60px repeat(7, 1fr);
		border-bottom: 1px solid var(--border-subtle);
		flex-shrink: 0;
	}

	.time-gutter-header {
		border-right: 1px solid var(--border-subtle);
	}

	.week-day-header {
		text-align: center;
		padding: 8px 4px;
		display: flex;
		flex-direction: column;
		gap: 2px;
	}
	.week-day-header.today .wk-day-num {
		background: var(--accent);
		color: var(--text-inverse);
		border-radius: 50%;
		width: 28px;
		height: 28px;
		display: inline-flex;
		align-items: center;
		justify-content: center;
	}

	.wk-day-name {
		font-size: var(--text-xs);
		color: var(--text-tertiary);
		text-transform: uppercase;
	}
	.wk-day-num {
		font-size: var(--text-base);
		font-weight: 600;
	}

	.week-body {
		flex: 1;
		overflow-y: auto;
	}

	.week-row {
		display: grid;
		grid-template-columns: 60px repeat(7, 1fr);
		min-height: 48px;
	}

	.time-gutter {
		font-size: var(--text-xs);
		color: var(--text-tertiary);
		text-align: right;
		padding: 2px 8px 0 0;
		border-right: 1px solid var(--border-subtle);
	}

	.week-cell {
		border: none;
		background: none;
		border-bottom: 1px solid var(--border-subtle);
		border-right: 1px solid var(--border-subtle);
		padding: 1px 2px;
		cursor: pointer;
		display: flex;
		flex-direction: column;
		gap: 1px;
		color: var(--text-primary);
		text-align: left;
	}
	.week-cell:hover { background: var(--bg-surface); }

	.week-event {
		font-size: 0.65rem;
		padding: 2px 4px;
		border-radius: 3px;
		white-space: nowrap;
		overflow: hidden;
		text-overflow: ellipsis;
		cursor: pointer;
		border: none;
		text-align: left;
	}
	.week-event:hover { filter: brightness(1.2); }

	/* Current time marker */
	.now-marker {
		position: absolute;
		z-index: 5;
		pointer-events: none;
		display: flex;
		align-items: center;
	}
	.now-dot {
		width: 8px;
		height: 8px;
		border-radius: 50%;
		background: var(--accent);
		flex-shrink: 0;
		margin-left: -4px;
	}
	.now-line {
		flex: 1;
		height: 2px;
		background: var(--accent);
	}

	/* Agenda View */
	.agenda-view {
		flex: 1;
		overflow-y: auto;
		padding: 16px 24px;
		max-width: 800px;
		margin: 0 auto;
		width: 100%;
	}

	.empty-state {
		text-align: center;
		color: var(--text-tertiary);
		padding: 48px;
		font-size: var(--text-base);
	}

	.agenda-day {
		margin-bottom: 24px;
	}

	.agenda-date {
		font-size: var(--text-sm);
		font-weight: 600;
		color: var(--text-secondary);
		padding-bottom: 8px;
		border-bottom: 1px solid var(--border-subtle);
		margin: 0 0 8px 0;
	}

	.agenda-event {
		display: flex;
		align-items: center;
		gap: 12px;
		padding: 10px 12px;
		border-radius: 8px;
		cursor: pointer;
		border: none;
		background: none;
		width: 100%;
		text-align: left;
		color: var(--text-primary);
	}
	.agenda-event:hover { background: var(--bg-surface); }

	.agenda-color {
		width: 4px;
		height: 36px;
		border-radius: 2px;
		flex-shrink: 0;
	}

	.agenda-info {
		flex: 1;
		min-width: 0;
	}

	.agenda-title {
		font-size: var(--text-base);
		font-weight: 500;
	}

	.agenda-time {
		font-size: var(--text-sm);
		color: var(--text-secondary);
	}

	.agenda-loc { color: var(--text-tertiary); }

	.recur-icon {
		color: var(--text-tertiary);
		flex-shrink: 0;
	}

	/* Modal */
	.modal-overlay {
		position: fixed;
		inset: 0;
		background: rgba(0, 0, 0, 0.6);
		display: flex;
		align-items: center;
		justify-content: center;
		z-index: 1000;
	}

	.modal {
		background: var(--bg-surface);
		border: 1px solid var(--border-default);
		border-radius: 12px;
		width: 480px;
		max-width: 90vw;
		max-height: 85vh;
		overflow-y: auto;
		box-shadow: var(--shadow-lg);
	}

	.modal-header {
		display: flex;
		justify-content: space-between;
		align-items: center;
		padding: 16px 20px;
		border-bottom: 1px solid var(--border-subtle);
	}

	.modal-header h2 {
		font-size: var(--text-base);
		font-weight: 600;
		margin: 0;
	}

	.modal-close {
		background: none;
		border: none;
		color: var(--text-tertiary);
		font-size: 20px;
		cursor: pointer;
		padding: 0 4px;
	}
	.modal-close:hover { color: var(--text-primary); }

	.modal-body {
		padding: 16px 20px;
		display: flex;
		flex-direction: column;
		gap: 12px;
	}

	.input {
		background: var(--bg-input);
		border: 1px solid var(--border-default);
		color: var(--text-primary);
		padding: 8px 10px;
		border-radius: 6px;
		font-size: var(--text-sm);
		width: 100%;
	}
	.input:focus { outline: none; border-color: var(--accent); }

	.textarea { resize: vertical; font-family: inherit; }

	.form-row {
		display: flex;
		gap: 12px;
		align-items: flex-start;
	}

	.form-group {
		flex: 1;
		display: flex;
		flex-direction: column;
		gap: 4px;
	}

	.form-group label {
		font-size: var(--text-xs);
		color: var(--text-tertiary);
		text-transform: uppercase;
		letter-spacing: 0.05em;
	}

	.datetime-row {
		display: flex;
		gap: 6px;
	}

	.time-input { width: 100px; flex-shrink: 0; }

	.checkbox-label {
		display: flex;
		align-items: center;
		gap: 8px;
		font-size: var(--text-sm);
		color: var(--text-secondary);
		cursor: pointer;
	}

	.color-picker {
		display: flex;
		gap: 6px;
		padding: 4px 0;
	}

	.color-dot {
		width: 20px;
		height: 20px;
		border-radius: 50%;
		border: 2px solid transparent;
		cursor: pointer;
	}
	.color-dot.active { border-color: var(--text-primary); }
	.color-dot:hover { transform: scale(1.15); }

	.modal-footer {
		display: flex;
		align-items: center;
		gap: 8px;
		padding: 12px 20px;
		border-top: 1px solid var(--border-subtle);
	}

	.spacer { flex: 1; }

	.btn {
		padding: 6px 16px;
		border-radius: 6px;
		font-size: var(--text-sm);
		font-weight: 500;
		cursor: pointer;
		border: none;
	}

	.btn-primary { background: var(--accent); color: var(--text-inverse); }
	.btn-primary:hover { background: var(--accent-hover); }
	.btn-primary:disabled { opacity: 0.5; cursor: not-allowed; }

	.btn-ghost { background: none; color: var(--text-secondary); border: 1px solid var(--border-default); }
	.btn-ghost:hover { background: var(--bg-raised); }

	.btn-danger { background: #ef4444; color: white; }
	.btn-danger:hover { background: #dc2626; }

	select.input {
		appearance: none;
		background-image: url("data:image/svg+xml,%3Csvg xmlns='http://www.w3.org/2000/svg' width='12' height='12' viewBox='0 0 12 12'%3E%3Cpath d='M3 4.5L6 7.5L9 4.5' stroke='%23606068' stroke-width='1.5' fill='none' stroke-linecap='round'/%3E%3C/svg%3E");
		background-repeat: no-repeat;
		background-position: right 8px center;
		padding-right: 28px;
	}
</style>
