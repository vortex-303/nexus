<!--
  BriefCard — branded brief detail rendering, matching the styling of
  the shareable /brief/<token> public view (web/static/brief.html).
  Used by the in-app Living Briefs detail pane so the in-app view and
  the shareable URL are visually consistent — both feel like the
  premium published artifact rather than a list-item drilldown.

  Props:
    template  — brief template slug ('workspace_pulse', 'north_star_status',
                'team_health', 'custom'). Renders the orange pill badge.
    title     — full brief title (e.g. "North Star Status"). Rendered in
                Playfair Display serif.
    date      — ISO date string. Formatted as "Thursday, May 7, 2026 at 4:07 PM".
    bodyHtml  — already-rendered markdown HTML. Slotted into the
                .brief-body container, where serif headings, orange
                bullet markers, and generous spacing kick in.

  No backdrop/animation by default — the in-app view is panel-scoped
  with its own background. Pass `animated` to enable the orange spark
  particles + floating code fragments used on the shareable card.
-->
<script lang="ts">
	interface Props {
		template?: string;
		title: string;
		date?: string;
		bodyHtml: string;
		animated?: boolean;
	}
	let { template = '', title, date = '', bodyHtml, animated = false }: Props = $props();

	const TEMPLATE_LABELS: Record<string, string> = {
		workspace_pulse: 'Workspace Pulse',
		north_star_status: 'North Star',
		team_health: 'Team Health',
		custom: 'Custom Brief',
	};

	const templateLabel = $derived(TEMPLATE_LABELS[template] || template || '');

	function formatDate(iso: string): string {
		if (!iso) return '';
		const d = new Date(iso);
		if (isNaN(d.getTime())) return iso;
		return d.toLocaleDateString('en-US', {
			weekday: 'long',
			year: 'numeric',
			month: 'long',
			day: 'numeric',
		}) + ' at ' + d.toLocaleTimeString('en-US', { hour: 'numeric', minute: '2-digit' });
	}
</script>

<div class="brief-card" class:animated>
	<div class="brief-card-header">
		{#if templateLabel}
			<div class="template-badge">{templateLabel}</div>
		{/if}
		<h1 class="brief-title">{title}</h1>
		{#if date}
			<div class="brief-date">{formatDate(date)}</div>
		{/if}
	</div>
	<div class="brief-body">
		{@html bodyHtml}
	</div>
</div>

<style>
	/* Card chrome — glassmorphic + subtle orange-tinted border, mirrors
	   web/static/brief.html exactly so the in-app and shareable views
	   are visually consistent. */
	.brief-card {
		width: 100%;
		max-width: 720px;
		margin: 0 auto;
		background: rgba(15, 15, 20, 0.85);
		backdrop-filter: blur(24px);
		-webkit-backdrop-filter: blur(24px);
		border: 1px solid rgba(249, 115, 22, 0.15);
		border-radius: 16px;
		padding: 48px;
		position: relative;
	}
	.brief-card::before {
		content: '';
		position: absolute;
		inset: -1px;
		border-radius: 17px;
		background: linear-gradient(135deg, rgba(249,115,22,0.08), transparent 50%, rgba(249,115,22,0.04));
		z-index: -1;
		pointer-events: none;
	}

	/* Header */
	.brief-card-header {
		margin-bottom: 36px;
		padding-bottom: 28px;
		border-bottom: 1px solid rgba(255,255,255,0.06);
	}
	.template-badge {
		display: inline-block;
		background: rgba(249, 115, 22, 0.12);
		color: #f97316;
		font-size: 0.75rem;
		font-weight: 600;
		letter-spacing: 0.04em;
		text-transform: uppercase;
		padding: 5px 14px;
		border-radius: 999px;
		border: 1px solid rgba(249, 115, 22, 0.2);
		margin-bottom: 16px;
	}
	.brief-title {
		font-family: 'Playfair Display', 'Times New Roman', serif;
		font-size: 2.2rem;
		font-weight: 700;
		color: #fff;
		line-height: 1.3;
		margin: 0 0 12px 0;
	}
	.brief-date {
		font-size: 0.85rem;
		color: #52525b;
	}

	/* Body — serif headings, orange bullets, generous spacing */
	.brief-body {
		line-height: 1.8;
		font-size: 1rem;
		color: #d4d4d8;
	}
	.brief-body :global(h1),
	.brief-body :global(h2) {
		font-family: 'Playfair Display', 'Times New Roman', serif;
		font-size: 1.5rem;
		font-weight: 600;
		color: #fff;
		margin-top: 2rem;
		margin-bottom: 0.75rem;
	}
	.brief-body :global(h3) {
		font-family: 'Playfair Display', 'Times New Roman', serif;
		font-size: 1.3rem;
		font-weight: 600;
		color: #fff;
		margin-top: 2rem;
		margin-bottom: 0.75rem;
	}
	.brief-body :global(h4),
	.brief-body :global(h5),
	.brief-body :global(h6) {
		font-family: 'Playfair Display', 'Times New Roman', serif;
		font-weight: 600;
		color: #fff;
		margin-top: 1.5rem;
		margin-bottom: 0.5rem;
	}
	.brief-body :global(p) {
		margin-bottom: 1rem;
	}
	.brief-body :global(strong) {
		color: #fff;
		font-weight: 600;
	}
	.brief-body :global(em) {
		font-style: italic;
		color: #a1a1aa;
	}
	.brief-body :global(ul),
	.brief-body :global(ol) {
		margin-bottom: 1rem;
		padding-left: 1.5rem;
	}
	.brief-body :global(li) {
		margin-bottom: 0.4rem;
	}
	.brief-body :global(ul li::marker) {
		color: #f97316;
	}
	.brief-body :global(ol li::marker) {
		color: #f97316;
		font-weight: 600;
	}
	.brief-body :global(hr) {
		border: none;
		border-top: 1px solid rgba(255,255,255,0.06);
		margin: 2rem 0;
	}
	.brief-body :global(code) {
		font-family: 'SF Mono', 'Fira Code', 'Consolas', monospace;
		font-size: 0.9em;
		background: rgba(255,255,255,0.06);
		padding: 2px 6px;
		border-radius: 4px;
	}
	.brief-body :global(blockquote) {
		border-left: 3px solid rgba(249, 115, 22, 0.4);
		padding-left: 1rem;
		margin: 1rem 0;
		color: #a1a1aa;
	}

	@media (max-width: 640px) {
		.brief-card { padding: 28px; border-radius: 12px; }
		.brief-title { font-size: 1.7rem; }
	}
</style>
