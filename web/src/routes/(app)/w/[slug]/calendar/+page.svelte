<script lang="ts">
	import { page } from '$app/state';
	import { goto } from '$app/navigation';
	import { onMount, onDestroy } from 'svelte';
	import { listCalendarEvents, createCalendarEvent, updateCalendarEvent, deleteCalendarEvent, getEventOutcome, clearPastAgentEvents, getCurrentUser, getWorkspaceModels, listChannels } from '$lib/api';
	import { connect, disconnect, onMessage } from '$lib/ws';
	import { members } from '$lib/stores/workspace';

	let slug = $derived(page.params.slug);
	let currentUser = $state(getCurrentUser());

	type ViewMode = 'month' | 'week' | 'agenda';
	let viewMode = $state<ViewMode>('month');
	let calScope = $state<'team' | 'my'>('team');
	let events = $state<any[]>([]);
	let currentDate = $state(new Date());
	let showEventModal = $state(false);
	let editingEvent = $state<any>(null);
	let selectedSlot = $state<{ start: string; end: string } | null>(null);
	let showAgentModal = $state(false);
	let showTray = $state(false);
	let showOutcome = $state(false);
	let outcomeData = $state<any>(null);
	let outcomeLoading = $state(false);
	let outcomeEvent = $state<any>(null);

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
	let formAgentId = $state('');
	let formModel = $state('');
	let formChannelId = $state('');
	let workspaceModels = $state<any[]>([]);
	let workspaceChannels = $state<any[]>([]);

	let membersList: any[] = [];
	const unsubMembers = members.subscribe(v => membersList = v);
	// Filter to agents only (Brain has role 'agent')
	let agentMembers = $derived(membersList.filter((m: any) => m.role === 'agent'));

	// Agent event derivations
	let agentEvents = $derived(events.filter(e => e.agent_id).sort((a, b) => a.start_time.localeCompare(b.start_time)));
	let upcomingAgentEvents = $derived(agentEvents.filter(e => new Date(e.start_time) > now));
	let pastAgentEvents = $derived(agentEvents.filter(e => new Date(e.start_time) <= now).reverse().slice(0, 10));

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
			const filters: any = {
				start: viewStart.toISOString(),
				end: viewEnd.toISOString(),
			};
			if (calScope === 'my') filters.scope = 'my';
			const data = await listCalendarEvents(slug, filters);
			events = data.events || [];
		} catch {
			events = [];
		}
	}

	function setCalScope(s: 'team' | 'my') {
		calScope = s;
		localStorage.setItem('nexus_cal_scope', s);
		loadEvents();
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

	onMount(async () => {
		const saved = localStorage.getItem('nexus_cal_view');
		if (saved === 'month' || saved === 'week' || saved === 'agenda') viewMode = saved;
		const savedScope = localStorage.getItem('nexus_cal_scope');
		if (savedScope === 'my' || savedScope === 'team') calScope = savedScope;
		connect();
		unsubWS = onMessage(handleWS);
		nowTimer = setInterval(() => { now = new Date(); }, 30000);
		try {
			const res = await getWorkspaceModels(slug);
			workspaceModels = res.models || [];
		} catch {}
		try {
			const res = await listChannels(slug);
			const allChannels = Array.isArray(res) ? res : res.channels || [];
			workspaceChannels = allChannels.filter((c: any) => c.type === 'public' || c.type === 'group');
		} catch {}
	});

	onDestroy(() => {
		if (unsubWS) unsubWS();
		if (nowTimer) clearInterval(nowTimer);
		unsubMembers();
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
		formAgentId = '';
		formModel = '';
		formChannelId = '';
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
		formAgentId = evt.agent_id || '';
		formModel = evt.model || '';
		formChannelId = evt.channel_id || '';
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
					agent_id: formAgentId,
					model: formAgentId ? formModel : '',
					channel_id: formAgentId ? formChannelId : '',
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
					agent_id: formAgentId || undefined,
					model: formAgentId ? formModel || undefined : undefined,
					channel_id: formAgentId ? formChannelId || undefined : undefined,
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

	// Agent tray helpers
	function formatCountdown(startTime: string, ref: Date): string {
		const diff = new Date(startTime).getTime() - ref.getTime();
		if (diff < 60_000) return 'now';
		const mins = Math.floor(diff / 60_000);
		if (mins < 60) return `in ${mins}m`;
		const hrs = Math.floor(mins / 60);
		const remMins = mins % 60;
		if (hrs < 24) return remMins > 0 ? `in ${hrs}h ${remMins}m` : `in ${hrs}h`;
		const d = new Date(startTime);
		return d.toLocaleDateString('en-US', { weekday: 'short', month: 'short', day: 'numeric', hour: 'numeric', minute: '2-digit' });
	}

	function formatTimeAgo(startTime: string, ref: Date): string {
		const diff = ref.getTime() - new Date(startTime).getTime();
		if (diff < 60_000) return 'just now';
		const mins = Math.floor(diff / 60_000);
		if (mins < 60) return `${mins}m ago`;
		const hrs = Math.floor(mins / 60);
		if (hrs < 24) return `${hrs}h ago`;
		const d = new Date(startTime);
		return d.toLocaleDateString('en-US', { weekday: 'short', hour: 'numeric', minute: '2-digit' });
	}

	function openAgentModal() {
		editingEvent = null;
		const n = new Date();
		const startDate = new Date(n.getFullYear(), n.getMonth(), n.getDate(), n.getHours() + 1);
		const endDate = new Date(startDate.getTime() + 60 * 60 * 1000);
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
		formAgentId = 'brain';
		formModel = '';
		formChannelId = workspaceChannels.length > 0 ? workspaceChannels[0].id : '';
		showAgentModal = true;
	}

	async function saveAgentEvent() {
		if (!formAgentId) return;
		if (!formTitle.trim()) formTitle = 'Agent task';
		const startTime = localToISO(formStartDate, formStartTime);
		const endDate = formEndDate || formStartDate;
		const endTime = localToISO(endDate, formEndTime);
		try {
			await createCalendarEvent(slug, {
				title: formTitle,
				description: formDescription,
				start_time: startTime,
				end_time: endTime,
				agent_id: formAgentId,
				model: formModel || undefined,
				channel_id: formChannelId || undefined,
			});
			showAgentModal = false;
			showTray = true;
		} catch (err: any) {
			alert(err.message || 'Failed to schedule agent');
		}
	}

	async function clearAgentEvents(mode: 'past' | 'all' = 'past') {
		const msg = mode === 'all' ? 'Clear ALL agent events (past + upcoming)?' : 'Clear past agent events?';
		if (!confirm(msg)) return;
		try {
			await clearPastAgentEvents(slug, mode);
			loadEvents();
		} catch (err: any) {
			alert(err.message || 'Failed to clear events');
		}
	}

	async function openOutcome(evt: any) {
		outcomeEvent = evt;
		outcomeData = null;
		outcomeLoading = true;
		showOutcome = true;
		try {
			outcomeData = await getEventOutcome(slug, evt.id);
		} catch {
			outcomeData = { status: 'error', response: '' };
		} finally {
			outcomeLoading = false;
		}
	}

	function handleTrayEventClick(evt: any) {
		const isPast = new Date(evt.start_time) <= now;
		if (isPast) {
			openOutcome(evt);
		} else {
			openEditEvent(evt);
		}
	}

	function agentName(agentId: string): string {
		if (agentId === 'brain') return 'Brain';
		const m = membersList.find((a: any) => a.id === agentId);
		return m?.display_name || agentId;
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
			<div class="scope-toggle">
				<button class:active={calScope === 'team'} onclick={() => setCalScope('team')}>Team</button>
				<button class:active={calScope === 'my'} onclick={() => setCalScope('my')}>My Calendar</button>
			</div>
			<div class="view-toggle">
				<button class:active={viewMode === 'month'} onclick={() => switchView('month')}>Month</button>
				<button class:active={viewMode === 'week'} onclick={() => switchView('week')}>Week</button>
				<button class:active={viewMode === 'agenda'} onclick={() => switchView('agenda')}>Agenda</button>
			</div>
			<button class="new-event-btn" onclick={() => openNewEvent()}>+ Event</button>
			<button class="agent-schedule-btn" onclick={openAgentModal}>
				<svg width="14" height="14" viewBox="0 0 16 16" fill="none"><path d="M13 2L3 8.5L7 9.5L9 14L13 2Z" stroke="currentColor" stroke-width="1.3" stroke-linejoin="round"/></svg>
				Schedule Agent
			</button>
			<button class="tray-toggle-btn" onclick={() => showTray = !showTray} class:active={showTray}>
				<svg width="16" height="16" viewBox="0 0 16 16" fill="none"><path d="M2 4h12M2 8h12M2 12h8" stroke="currentColor" stroke-width="1.3" stroke-linecap="round"/></svg>
				{#if upcomingAgentEvents.length > 0}
					<span class="tray-badge">{upcomingAgentEvents.length}</span>
				{/if}
			</button>
		</div>
	</header>

	<div class="cal-content" style="position: relative; flex: 1; display: flex; overflow: hidden;">
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
								<div class="agenda-title">{evt.title}{#if calScope === 'team' && evt.created_by_name}<span class="agenda-creator"> · {evt.created_by_name}</span>{/if}</div>
								<div class="agenda-time">
									{#if evt.all_day}
										All day
									{:else}
										{formatEventTime(evt.start_time)} - {formatEventTime(evt.end_time)}
									{/if}
									{#if evt.location}<span class="agenda-loc"> · {evt.location}</span>{/if}
								</div>
							</div>
							{#if evt.agent_id}
								<span class="agent-badge" title="Agent: {evt.agent_id === 'brain' ? 'Brain' : evt.agent_id}">
									<svg width="14" height="14" viewBox="0 0 16 16" fill="none"><circle cx="8" cy="6" r="3" stroke="currentColor" stroke-width="1.2"/><path d="M3 14c0-2.8 2.2-5 5-5s5 2.2 5 5" stroke="currentColor" stroke-width="1.2" stroke-linecap="round"/></svg>
								</span>
							{/if}
							{#if evt.recurrence_rule}
								<svg class="recur-icon" width="14" height="14" viewBox="0 0 14 14" fill="none"><path d="M11 4H3M11 4L9 2M11 4L9 6M3 10H11M3 10L5 8M3 10L5 12" stroke="currentColor" stroke-width="1.2" stroke-linecap="round" stroke-linejoin="round"/></svg>
							{/if}
						</button>
					{/each}
				</div>
			{/each}
		</div>
	{/if}

	<!-- Outgoing Tray -->
	{#if showTray}
		<div class="outgoing-tray">
			<div class="tray-header">
				<span class="tray-title">Outgoing</span>
				{#if agentEvents.length > 0}
					<button class="tray-clear-btn" onclick={() => clearAgentEvents('all')}>Clear all</button>
				{/if}
				<button class="modal-close" onclick={() => showTray = false}>&times;</button>
			</div>

			{#if upcomingAgentEvents.length === 0 && pastAgentEvents.length === 0}
				<div class="tray-empty">
					<p>No agent events scheduled</p>
					<button class="agent-schedule-btn" onclick={openAgentModal}>Schedule Agent</button>
				</div>
			{/if}

			{#if upcomingAgentEvents.length > 0}
				<div class="tray-section">
					<div class="tray-section-title">Upcoming</div>
					{#each upcomingAgentEvents as evt}
						<button class="tray-event" onclick={() => openEditEvent(evt)}>
							<div class="tray-event-top">
								<span class="tray-agent-badge">{agentName(evt.agent_id)}</span>
								<span class="tray-countdown" class:urgent={new Date(evt.start_time).getTime() - now.getTime() < 3600_000}>{formatCountdown(evt.start_time, now)}</span>
							</div>
							<div class="tray-event-title">{evt.title}</div>
							{#if evt.description}
								<div class="tray-prompt">{evt.description.slice(0, 80)}{evt.description.length > 80 ? '...' : ''}</div>
							{/if}
							{#if evt.model}
								<span class="tray-model-badge">{evt.model}</span>
							{/if}
						</button>
					{/each}
				</div>
			{/if}

			{#if pastAgentEvents.length > 0}
				<div class="tray-section">
					<div class="tray-section-title">Completed</div>
					{#each pastAgentEvents as evt}
						<button class="tray-event tray-event-past" onclick={() => handleTrayEventClick(evt)}>
							<div class="tray-event-top">
								<span class="tray-agent-badge">{agentName(evt.agent_id)}</span>
								<span class="tray-time-ago">
									{#if evt.executed_at}
										<svg width="12" height="12" viewBox="0 0 16 16" fill="none"><path d="M13.5 5.5L6.5 12.5L2.5 8.5" stroke="#10b981" stroke-width="1.5" stroke-linecap="round" stroke-linejoin="round"/></svg>
									{:else}
										<svg width="12" height="12" viewBox="0 0 16 16" fill="none"><path d="M4 4L12 12M12 4L4 12" stroke="#ef4444" stroke-width="1.5" stroke-linecap="round"/></svg>
									{/if}
									{formatTimeAgo(evt.start_time, now)}
								</span>
							</div>
							<div class="tray-event-title">{evt.title}</div>
						</button>
					{/each}
				</div>
			{/if}
		</div>
	{/if}
	</div>
</div>

<!-- Agent Schedule Modal -->
{#if showAgentModal}
	<div class="modal-overlay" onclick={() => showAgentModal = false} role="dialog">
		<div class="modal" onclick={(e) => e.stopPropagation()}>
			<div class="modal-header">
				<h2>Schedule Agent</h2>
				<button class="modal-close" onclick={() => showAgentModal = false}>&times;</button>
			</div>
			<div class="modal-body">
				<div class="form-group">
					<label>Agent</label>
					<select class="input" bind:value={formAgentId}>
						<option value="brain">Brain</option>
						{#each agentMembers.filter(a => a.id !== 'brain') as agent}
							<option value={agent.id}>{agent.display_name}</option>
						{/each}
					</select>
				</div>

				<div class="form-group">
					<label>Prompt</label>
					<textarea class="input textarea" placeholder="What should the agent do?" bind:value={formDescription} rows="4"></textarea>
				</div>

				<div class="form-group">
					<label>Title</label>
					<input class="input" type="text" placeholder="Short label (optional)" bind:value={formTitle} />
				</div>

				<div class="form-row">
					<div class="form-group">
						<label>Date</label>
						<input class="input" type="date" bind:value={formStartDate} oninput={() => { formEndDate = formStartDate; }} />
					</div>
					<div class="form-group">
						<label>Time</label>
						<input class="input time-input" type="time" bind:value={formStartTime} oninput={() => {
							const [h, m] = formStartTime.split(':').map(Number);
							const end = new Date(2000, 0, 1, h + 1, m);
							formEndTime = pad(end.getHours()) + ':' + pad(end.getMinutes());
						}} />
					</div>
				</div>

				<div class="form-group">
					<label>Post to Channel</label>
					<select class="input" bind:value={formChannelId}>
						<option value="">Auto (first available)</option>
						{#each workspaceChannels as ch}
							<option value={ch.id}>#{ch.name}</option>
						{/each}
					</select>
				</div>

				<div class="form-group">
					<label>Model Override</label>
					<select class="input" bind:value={formModel}>
						<option value="">Workspace default</option>
						{#each workspaceModels as m}
							<option value={m.id}>{m.display_name || m.id}</option>
						{/each}
					</select>
				</div>
			</div>
			<div class="modal-footer">
				<div class="spacer"></div>
				<button class="btn btn-ghost" onclick={() => showAgentModal = false}>Cancel</button>
				<button class="btn btn-primary" onclick={saveAgentEvent}>Create</button>
			</div>
		</div>
	</div>
{/if}

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

				<div class="form-row">
					<div class="form-group" style="flex:1">
						<label>Assign to Agent</label>
						<select class="input" bind:value={formAgentId}>
							<option value="">None — regular event</option>
							<option value="brain">Brain</option>
							{#each agentMembers.filter(a => a.id !== 'brain') as agent}
								<option value={agent.id}>{agent.display_name}</option>
							{/each}
						</select>
						{#if formAgentId}
							<span class="agent-hint">Agent will execute at event start time. Use Description as the prompt.</span>
							<label style="margin-top:0.5rem">Post to Channel</label>
							<select class="input" bind:value={formChannelId}>
								<option value="">Auto (first available)</option>
								{#each workspaceChannels as ch}
									<option value={ch.id}>#{ch.name}</option>
								{/each}
							</select>
							<label style="margin-top:0.5rem">Model Override</label>
							<select class="input" bind:value={formModel}>
								<option value="">Workspace default</option>
								{#each workspaceModels as m}
									<option value={m.id}>{m.display_name || m.id}</option>
								{/each}
							</select>
						{/if}
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

<!-- Outcome Modal -->
{#if showOutcome}
	<div class="modal-overlay" onclick={() => showOutcome = false} role="dialog">
		<div class="modal outcome-modal" onclick={(e) => e.stopPropagation()}>
			<div class="modal-header">
				<div>
					<h2>Agent task</h2>
					{#if outcomeEvent}
						<span class="outcome-subtitle">
							{agentName(outcomeEvent.agent_id)}
							{#if outcomeData?.channel_name} &middot; #{outcomeData.channel_name}{/if}
							&middot; {formatTimeAgo(outcomeEvent.start_time, now)}
						</span>
					{/if}
				</div>
				<button class="modal-close" onclick={() => showOutcome = false}>&times;</button>
			</div>
			<div class="modal-body outcome-body">
				{#if outcomeLoading}
					<div class="outcome-loading">Loading...</div>
				{:else if outcomeData}
					<div class="outcome-section">
						<div class="outcome-label">STATUS</div>
						{#if outcomeData.status === 'executed'}
							<div class="outcome-status outcome-status-ok">
								<svg width="16" height="16" viewBox="0 0 16 16" fill="none"><path d="M13.5 5.5L6.5 12.5L2.5 8.5" stroke="currentColor" stroke-width="1.5" stroke-linecap="round" stroke-linejoin="round"/></svg>
								Executed
							</div>
						{:else if outcomeData.status === 'missed'}
							<div class="outcome-status outcome-status-missed">
								<svg width="16" height="16" viewBox="0 0 16 16" fill="none"><path d="M4 4L12 12M12 4L4 12" stroke="currentColor" stroke-width="1.5" stroke-linecap="round"/></svg>
								Missed
							</div>
						{:else}
							<div class="outcome-status outcome-status-pending">
								<svg width="16" height="16" viewBox="0 0 16 16" fill="none"><circle cx="8" cy="8" r="6" stroke="currentColor" stroke-width="1.3"/><path d="M8 4.5V8L10.5 9.5" stroke="currentColor" stroke-width="1.3" stroke-linecap="round"/></svg>
								Pending
							</div>
						{/if}
					</div>

					{#if outcomeData.prompt || outcomeEvent?.description}
						<div class="outcome-section">
							<div class="outcome-label">PROMPT</div>
							<div class="outcome-text outcome-prompt-text">{outcomeData.prompt || outcomeEvent?.description}</div>
						</div>
					{/if}

					{#if outcomeData.response}
						<div class="outcome-section">
							<div class="outcome-label">RESPONSE</div>
							<div class="outcome-text">{outcomeData.response}</div>
						</div>
					{/if}

					{#if outcomeData.tools_used?.length > 0}
						<div class="outcome-section">
							<div class="outcome-label">TOOLS USED</div>
							<div class="outcome-tools">
								{#each outcomeData.tools_used as tool}
									<span class="outcome-tool-badge">{tool}</span>
								{/each}
							</div>
						</div>
					{/if}

					{#if outcomeData.model}
						<div class="outcome-section">
							<div class="outcome-label">MODEL</div>
							<div class="outcome-text outcome-model">{outcomeData.model}</div>
						</div>
					{/if}
				{/if}
			</div>
			<div class="modal-footer">
				{#if outcomeData?.channel_id}
					<button class="btn btn-ghost" onclick={() => { showOutcome = false; goto(`/w/${slug}?channel=${outcomeData.channel_id}`); }}>Open in Channel</button>
				{/if}
				<div class="spacer"></div>
				{#if outcomeEvent}
					<button class="btn btn-ghost" onclick={() => { showOutcome = false; openEditEvent(outcomeEvent); }}>Edit Event</button>
				{/if}
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

	.scope-toggle {
		display: flex;
		border: 1px solid var(--border-default);
		border-radius: 6px;
		overflow: hidden;
	}
	.scope-toggle button {
		background: none;
		border: none;
		color: var(--text-secondary);
		padding: 4px 10px;
		font-size: var(--text-xs);
		cursor: pointer;
		border-right: 1px solid var(--border-default);
	}
	.scope-toggle button:last-child { border-right: none; }
	.scope-toggle button:hover { background: var(--bg-raised); }
	.scope-toggle button.active { background: var(--accent); color: var(--text-inverse); }

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
	.agenda-creator {
		font-weight: 400;
		color: var(--text-tertiary);
		font-size: var(--text-sm);
	}
	.agent-hint {
		display: block;
		font-size: var(--text-xs);
		color: var(--accent);
		margin-top: 4px;
	}
	.agent-badge {
		color: var(--accent);
		display: flex;
		align-items: center;
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

	/* Agent Schedule Button */
	.agent-schedule-btn {
		display: flex;
		align-items: center;
		gap: 6px;
		background: none;
		border: 1px solid var(--accent);
		color: var(--accent);
		padding: 6px 14px;
		border-radius: 6px;
		font-size: var(--text-sm);
		font-weight: 500;
		cursor: pointer;
	}
	.agent-schedule-btn:hover {
		background: var(--accent);
		color: var(--text-inverse);
	}

	/* Tray Toggle */
	.tray-toggle-btn {
		position: relative;
		background: none;
		border: 1px solid var(--border-default);
		color: var(--text-secondary);
		padding: 6px 8px;
		border-radius: 6px;
		cursor: pointer;
		display: flex;
		align-items: center;
	}
	.tray-toggle-btn:hover, .tray-toggle-btn.active {
		background: var(--bg-raised);
		color: var(--text-primary);
	}
	.tray-badge {
		position: absolute;
		top: -6px;
		right: -6px;
		background: var(--accent);
		color: var(--text-inverse);
		font-size: 0.6rem;
		font-weight: 700;
		min-width: 16px;
		height: 16px;
		border-radius: 8px;
		display: flex;
		align-items: center;
		justify-content: center;
		padding: 0 4px;
	}

	/* Outgoing Tray */
	.outgoing-tray {
		position: absolute;
		right: 0;
		top: 0;
		bottom: 0;
		width: 360px;
		max-width: 100%;
		background: var(--bg-surface);
		border-left: 1px solid var(--border-default);
		z-index: 50;
		overflow-y: auto;
		display: flex;
		flex-direction: column;
		animation: tray-slide-in 0.15s ease-out;
	}
	@keyframes tray-slide-in {
		from { transform: translateX(100%); }
		to { transform: translateX(0); }
	}
	.tray-header {
		display: flex;
		justify-content: space-between;
		align-items: center;
		padding: 12px 16px;
		border-bottom: 1px solid var(--border-subtle);
		flex-shrink: 0;
	}
	.tray-title {
		font-size: var(--text-base);
		font-weight: 600;
	}
	.tray-section {
		padding: 8px 0;
	}
	.tray-section-title {
		font-size: var(--text-xs);
		text-transform: uppercase;
		letter-spacing: 0.08em;
		color: var(--text-tertiary);
		padding: 4px 16px 8px;
		font-weight: 600;
	}
	.tray-event {
		display: flex;
		flex-direction: column;
		gap: 4px;
		padding: 10px 16px;
		border-left: 3px solid var(--accent);
		margin: 0 12px 6px;
		border-radius: 0 6px 6px 0;
		background: none;
		border-top: none;
		border-right: none;
		border-bottom: none;
		border-left: 3px solid var(--accent);
		cursor: pointer;
		text-align: left;
		width: calc(100% - 24px);
		color: var(--text-primary);
	}
	.tray-event:hover {
		background: var(--bg-raised);
	}
	.tray-event-past {
		border-left-color: var(--text-tertiary);
		opacity: 0.75;
	}
	.tray-event-top {
		display: flex;
		justify-content: space-between;
		align-items: center;
		gap: 8px;
	}
	.tray-event-title {
		font-size: var(--text-sm);
		font-weight: 500;
	}
	.tray-agent-badge {
		font-size: var(--text-xs);
		font-weight: 600;
		color: var(--accent);
		background: color-mix(in srgb, var(--accent) 12%, transparent);
		padding: 2px 8px;
		border-radius: 10px;
	}
	.tray-countdown {
		font-size: var(--text-xs);
		font-family: var(--font-mono, monospace);
		color: var(--text-secondary);
	}
	.tray-countdown.urgent {
		color: var(--accent);
		font-weight: 600;
	}
	.tray-time-ago {
		font-size: var(--text-xs);
		color: var(--text-tertiary);
		display: flex;
		align-items: center;
		gap: 4px;
	}
	.tray-time-ago svg {
		color: #10b981;
	}
	.tray-prompt {
		font-size: var(--text-xs);
		color: var(--text-tertiary);
		font-style: italic;
		white-space: nowrap;
		overflow: hidden;
		text-overflow: ellipsis;
	}
	.tray-model-badge {
		font-size: 0.6rem;
		color: var(--text-tertiary);
		background: var(--bg-raised);
		padding: 1px 6px;
		border-radius: 4px;
		align-self: flex-start;
	}
	.tray-empty {
		padding: 48px 16px;
		text-align: center;
		color: var(--text-tertiary);
		display: flex;
		flex-direction: column;
		align-items: center;
		gap: 12px;
	}
	.tray-empty p { margin: 0; }
	.tray-clear-btn { background: none; border: 1px solid var(--border-subtle); color: var(--text-secondary); font-size: 11px; padding: 2px 8px; border-radius: 4px; cursor: pointer; margin-left: auto; }
	.tray-clear-btn:hover { color: #ef4444; border-color: #ef4444; }

	/* Outcome Modal */
	.outcome-modal { max-width: 520px; }
	.outcome-subtitle { font-size: var(--text-xs); color: var(--text-secondary); }
	.outcome-body { display: flex; flex-direction: column; gap: 16px; }
	.outcome-loading { text-align: center; color: var(--text-secondary); padding: 2rem 0; }
	.outcome-section { display: flex; flex-direction: column; gap: 4px; }
	.outcome-label { font-size: 10px; font-weight: 600; text-transform: uppercase; letter-spacing: 0.05em; color: var(--text-secondary); }
	.outcome-status { display: flex; align-items: center; gap: 6px; font-size: var(--text-sm); font-weight: 500; }
	.outcome-status-ok { color: #10b981; }
	.outcome-status-missed { color: #ef4444; }
	.outcome-status-pending { color: #f59e0b; }
	.outcome-text { font-size: var(--text-sm); color: var(--text-primary); white-space: pre-wrap; word-break: break-word; line-height: 1.5; }
	.outcome-prompt-text { background: var(--bg-raised); padding: 8px 10px; border-radius: 6px; border-left: 3px solid var(--accent); }
	.outcome-tools { display: flex; flex-wrap: wrap; gap: 4px; }
	.outcome-tool-badge { font-size: 11px; background: var(--bg-raised); color: var(--text-secondary); padding: 2px 8px; border-radius: 4px; font-family: monospace; }
	.outcome-model { font-family: monospace; font-size: 12px; color: var(--text-secondary); }
</style>
