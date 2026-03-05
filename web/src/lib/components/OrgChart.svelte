<script>
	import { onMount, onDestroy, tick } from 'svelte';
	import { OrgChart as D3OrgChart } from 'd3-org-chart';

	let { nodes = [], isAdmin = false, onReparent = () => {}, onNodeClick = () => {}, onFit = $bindable(null), onExpandAll = $bindable(null), onCollapseAll = $bindable(null) } = $props();

	let wrapperEl;
	let containerEl;
	let chart;

	function getNodeColor(node) {
		if (node.type === 'system_agent') return '#8b5cf6';
		if (node.type === 'agent') return '#3b82f6';
		if (node.type === 'role_slot') return '#f59e0b';
		return '#22c55e';
	}

	function getNodeEmoji(node) {
		if (node.avatar) return node.avatar;
		if (node.type === 'system_agent') return '🧠';
		if (node.type === 'agent') return '🤖';
		if (node.type === 'role_slot') return '📋';
		return node.name?.charAt(0)?.toUpperCase() || '?';
	}

	function getBadge(node) {
		if (node.type === 'system_agent') return '<span style="background:#8b5cf6;color:#fff;padding:1px 6px;border-radius:8px;font-size:10px;">System Agent</span>';
		if (node.type === 'role_slot') return '<span style="background:#f59e0b;color:#fff;padding:1px 6px;border-radius:8px;font-size:10px;">Vacant</span>';
		if (node.type === 'agent' && !node.is_active) return '<span style="background:#ef4444;color:#fff;padding:1px 6px;border-radius:8px;font-size:10px;">Paused</span>';
		if (node.type === 'agent') return '<span style="background:#3b82f6;color:#fff;padding:1px 6px;border-radius:8px;font-size:10px;">Agent</span>';
		return '';
	}

	function getStatusDot(node) {
		if (node.type === 'agent') {
			return node.is_active
				? '<span style="display:inline-block;width:8px;height:8px;border-radius:50%;background:#22c55e;margin-left:4px;"></span>'
				: '<span style="display:inline-block;width:8px;height:8px;border-radius:50%;background:#ef4444;margin-left:4px;"></span>';
		}
		if (node.online) {
			return '<span style="display:inline-block;width:8px;height:8px;border-radius:50%;background:#22c55e;margin-left:4px;"></span>';
		}
		return '';
	}

	function getStatsLine(node) {
		if (node.type === 'role_slot') {
			const desc = node.role || '';
			if (desc) return `<div style="font-size:10px;color:#6b6b7b;text-align:center;overflow:hidden;text-overflow:ellipsis;max-width:100%;white-space:nowrap;">${desc}</div>`;
			return '';
		}
		if (node.type === 'system_agent') {
			const childCount = nodes.filter(n => n.reports_to === node.id || (!n.reports_to && n.id !== node.id)).length;
			return `<div style="font-size:10px;color:#6b6b7b;">${childCount} direct reports</div>`;
		}
		const parts = [];
		const tc = node.task_count || 0;
		const mc = node.message_count || 0;
		if (tc > 0) parts.push(`${tc} task${tc !== 1 ? 's' : ''}`);
		if (mc > 0) parts.push(`${mc} msg${mc !== 1 ? 's' : ''}`);
		if (parts.length === 0) return '';
		return `<div style="font-size:10px;color:#6b6b7b;">${parts.join(' · ')}</div>`;
	}

	function getTriggerBadge(node) {
		if (node.type !== 'agent' || !node.trigger_type) return '';
		const colors = { mention: '#6366f1', always: '#22c55e', schedule: '#f59e0b', webhook: '#ec4899' };
		const color = colors[node.trigger_type] || '#6b7280';
		return `<span style="background:${color}22;color:${color};padding:1px 5px;border-radius:6px;font-size:9px;border:1px solid ${color}44;">${node.trigger_type}</span>`;
	}

	function buildChartData(nodes) {
		return nodes.map(n => ({
			id: n.id,
			parentId: n.reports_to || '',
			name: n.name,
			role: n.role || n.title || '',
			type: n.type,
			avatar: n.avatar,
			is_active: n.is_active,
			online: n.online,
			message_count: n.message_count || 0,
			task_count: n.task_count || 0,
			last_active: n.last_active || '',
			trigger_type: n.trigger_type || '',
			_rawNode: n
		}));
	}

	function renderChart() {
		if (!containerEl || !wrapperEl || !nodes.length) return;

		const rect = wrapperEl.getBoundingClientRect();
		const w = Math.floor(rect.width);
		const h = Math.floor(rect.height);
		if (w < 100 || h < 100) return;
		containerEl.style.width = w + 'px';
		containerEl.style.height = h + 'px';

		const data = buildChartData(nodes);

		const ids = new Set(data.map(d => d.id));
		const brainNode = data.find(d => d.type === 'system_agent') || data[0];
		const rootId = brainNode?.id || data[0]?.id;
		data.forEach(d => {
			if (d.id === rootId) {
				d.parentId = null;
			} else if (!d.parentId || !ids.has(d.parentId)) {
				d.parentId = rootId;
			}
		});

		if (!chart) {
			chart = new D3OrgChart();
		}

		chart
			.container(containerEl)
			.data(data)
			.svgWidth(w)
			.svgHeight(h)
			.nodeWidth(() => 200)
			.nodeHeight(() => 130)
			.compactMarginBetween(() => 25)
			.compactMarginPair(() => 50)
			.childrenMargin(() => 50)
			.siblingsMargin(() => 30)
			.nodeContent((d) => {
				const node = d.data;
				const color = getNodeColor(node);
				const emoji = getNodeEmoji(node);
				const badge = getBadge(node);
				const dot = getStatusDot(node);
				const stats = getStatsLine(node);
				const trigger = getTriggerBadge(node);

				return `
					<div style="
						background: #1e1e2e;
						border: 2px solid ${color};
						border-radius: 10px;
						padding: 10px 12px;
						font-family: -apple-system, BlinkMacSystemFont, sans-serif;
						width: ${d.width}px;
						height: ${d.height}px;
						box-sizing: border-box;
						cursor: pointer;
						display: flex;
						flex-direction: column;
						justify-content: center;
						align-items: center;
						gap: 3px;
						overflow: hidden;
					">
						<div style="font-size: 22px; line-height: 1;">${emoji}${dot}</div>
						<div style="font-weight: 600; font-size: 13px; color: #e1e1e6; text-align: center; overflow: hidden; text-overflow: ellipsis; max-width: 100%; white-space: nowrap;">${node.name}</div>
						<div style="font-size: 11px; color: #9898a6; text-align: center; overflow: hidden; text-overflow: ellipsis; max-width: 100%; white-space: nowrap;">${node.role}</div>
						${badge || trigger ? `<div style="display:flex;gap:4px;align-items:center;margin-top:1px;">${badge}${trigger}</div>` : ''}
						${stats}
					</div>
				`;
			})
			.onNodeClick((d) => {
				onNodeClick(d.data._rawNode);
			})
			.render();

		if (isAdmin && typeof chart.dragEnabled === 'function') {
			chart.dragEnabled(true);
			if (typeof chart.onNodeDrop === 'function') {
				chart.onNodeDrop((source, target) => {
					if (source && target) {
						onReparent(source.id, target.id);
					}
				});
			}
		}

		chart.expandAll();
		requestAnimationFrame(() => {
			chart.fit();
		});

		// Expose control functions via bindable props
		onFit = () => chart?.fit();
		onExpandAll = () => { chart?.expandAll(); chart?.render(); };
		onCollapseAll = () => { chart?.collapseAll(); chart?.render(); };
	}

	onMount(async () => {
		await tick();
		setTimeout(() => renderChart(), 50);
	});

	onDestroy(() => {
		chart = null;
	});

	$effect(() => {
		if (nodes && containerEl && wrapperEl) {
			requestAnimationFrame(() => renderChart());
		}
	});
</script>

<div class="org-chart-wrapper" bind:this={wrapperEl}>
	<div class="org-chart-inner" bind:this={containerEl}></div>
</div>

<style>
	.org-chart-wrapper {
		width: 100%;
		height: calc(100vh - 160px);
		min-height: 400px;
		position: relative;
		overflow: hidden;
	}

	.org-chart-inner {
		position: absolute;
		inset: 0;
	}

	.org-chart-wrapper :global(svg) {
		background: transparent !important;
	}

	.org-chart-wrapper :global(.link) {
		stroke: var(--border, #333) !important;
		stroke-width: 1.5px;
	}
</style>
