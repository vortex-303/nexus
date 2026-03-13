<script lang="ts">
	let { state = 'idle', size = 48 }: { state?: 'idle' | 'thinking' | 'speaking', size?: number } = $props();
</script>

<div class="brain-avatar" data-state={state} style="width: {size}px; height: {size}px;">
	<svg viewBox="0 0 64 64" fill="none" class="brain-svg">
		<!-- Outer ring -->
		<circle cx="32" cy="32" r="28" stroke="var(--accent)" stroke-width="1" opacity="0.3" class="ring ring-outer"/>
		<circle cx="32" cy="32" r="22" stroke="var(--accent)" stroke-width="0.5" opacity="0.2" class="ring ring-inner"/>

		<!-- Neural nodes -->
		<circle cx="32" cy="16" r="2.5" fill="var(--accent)" class="node n1"/>
		<circle cx="44" cy="22" r="2" fill="var(--accent)" class="node n2"/>
		<circle cx="48" cy="34" r="2.5" fill="var(--accent)" class="node n3"/>
		<circle cx="42" cy="46" r="2" fill="var(--accent)" class="node n4"/>
		<circle cx="32" cy="48" r="2.5" fill="var(--accent)" class="node n5"/>
		<circle cx="22" cy="44" r="2" fill="var(--accent)" class="node n6"/>
		<circle cx="16" cy="34" r="2.5" fill="var(--accent)" class="node n7"/>
		<circle cx="20" cy="22" r="2" fill="var(--accent)" class="node n8"/>

		<!-- Central core -->
		<circle cx="32" cy="32" r="5" fill="var(--accent)" class="core"/>
		<circle cx="32" cy="32" r="8" stroke="var(--accent)" stroke-width="0.8" opacity="0.4" class="core-ring"/>

		<!-- Neural connections -->
		<line x1="32" y1="16" x2="32" y2="27" stroke="var(--accent)" stroke-width="0.8" opacity="0.5" class="conn c1"/>
		<line x1="44" y1="22" x2="37" y2="29" stroke="var(--accent)" stroke-width="0.8" opacity="0.5" class="conn c2"/>
		<line x1="48" y1="34" x2="40" y2="33" stroke="var(--accent)" stroke-width="0.8" opacity="0.5" class="conn c3"/>
		<line x1="42" y1="46" x2="36" y2="36" stroke="var(--accent)" stroke-width="0.8" opacity="0.5" class="conn c4"/>
		<line x1="32" y1="48" x2="32" y2="37" stroke="var(--accent)" stroke-width="0.8" opacity="0.5" class="conn c5"/>
		<line x1="22" y1="44" x2="28" y2="36" stroke="var(--accent)" stroke-width="0.8" opacity="0.5" class="conn c6"/>
		<line x1="16" y1="34" x2="24" y2="33" stroke="var(--accent)" stroke-width="0.8" opacity="0.5" class="conn c7"/>
		<line x1="20" y1="22" x2="27" y2="29" stroke="var(--accent)" stroke-width="0.8" opacity="0.5" class="conn c8"/>
	</svg>
</div>

<style>
	.brain-avatar {
		flex-shrink: 0;
	}
	.brain-svg {
		width: 100%;
		height: 100%;
	}

	/* Idle: gentle pulse glow on core */
	.core {
		filter: drop-shadow(0 0 4px var(--accent));
		animation: corePulse 3s ease-in-out infinite;
	}
	@keyframes corePulse {
		0%, 100% { opacity: 0.8; filter: drop-shadow(0 0 4px var(--accent)); }
		50% { opacity: 1; filter: drop-shadow(0 0 8px var(--accent)); }
	}

	/* Thinking: sequential node highlights */
	[data-state="thinking"] .node {
		animation: nodeFlash 1.6s ease-in-out infinite;
	}
	[data-state="thinking"] .n1 { animation-delay: 0s; }
	[data-state="thinking"] .n2 { animation-delay: 0.2s; }
	[data-state="thinking"] .n3 { animation-delay: 0.4s; }
	[data-state="thinking"] .n4 { animation-delay: 0.6s; }
	[data-state="thinking"] .n5 { animation-delay: 0.8s; }
	[data-state="thinking"] .n6 { animation-delay: 1.0s; }
	[data-state="thinking"] .n7 { animation-delay: 1.2s; }
	[data-state="thinking"] .n8 { animation-delay: 1.4s; }

	@keyframes nodeFlash {
		0%, 70%, 100% { opacity: 0.5; r: 2; }
		35% { opacity: 1; r: 3.5; filter: drop-shadow(0 0 6px var(--accent)); }
	}

	[data-state="thinking"] .conn {
		animation: connPulse 1.6s ease-in-out infinite;
	}
	[data-state="thinking"] .c1 { animation-delay: 0.1s; }
	[data-state="thinking"] .c2 { animation-delay: 0.3s; }
	[data-state="thinking"] .c3 { animation-delay: 0.5s; }
	[data-state="thinking"] .c4 { animation-delay: 0.7s; }
	[data-state="thinking"] .c5 { animation-delay: 0.9s; }
	[data-state="thinking"] .c6 { animation-delay: 1.1s; }
	[data-state="thinking"] .c7 { animation-delay: 1.3s; }
	[data-state="thinking"] .c8 { animation-delay: 1.5s; }

	@keyframes connPulse {
		0%, 70%, 100% { opacity: 0.3; stroke-width: 0.8; }
		35% { opacity: 0.9; stroke-width: 1.5; }
	}

	/* Speaking: wave on core ring */
	[data-state="speaking"] .core-ring {
		animation: coreWave 1s ease-in-out infinite;
	}
	@keyframes coreWave {
		0%, 100% { r: 8; opacity: 0.4; }
		50% { r: 12; opacity: 0.1; }
	}
	[data-state="speaking"] .core {
		animation: coreSpeakPulse 0.5s ease-in-out infinite;
	}
	@keyframes coreSpeakPulse {
		0%, 100% { filter: drop-shadow(0 0 4px var(--accent)); }
		50% { filter: drop-shadow(0 0 12px var(--accent)); }
	}

	.ring-outer {
		animation: ringRotate 20s linear infinite;
		transform-origin: 32px 32px;
	}
	@keyframes ringRotate {
		from { transform: rotate(0deg); }
		to { transform: rotate(360deg); }
	}
</style>
