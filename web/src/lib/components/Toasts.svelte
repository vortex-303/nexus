<script lang="ts">
	import { toasts } from '$lib/toast';
	import { fly } from 'svelte/transition';

	let items = $state<any[]>([]);
	const unsub = toasts.subscribe(v => items = v);
</script>

{#if items.length > 0}
	<div class="toast-container">
		{#each items as toast (toast.id)}
			<div class="toast toast-{toast.type}" transition:fly={{ x: 300, duration: 300 }}>
				<span class="toast-icon">
					{#if toast.type === 'success'}
						<svg width="16" height="16" viewBox="0 0 16 16" fill="none"><path d="M3 8.5L6.5 12L13 4" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"/></svg>
					{:else if toast.type === 'error'}
						<svg width="16" height="16" viewBox="0 0 16 16" fill="none"><circle cx="8" cy="8" r="6" stroke="currentColor" stroke-width="1.5"/><path d="M8 5V9M8 11V11.5" stroke="currentColor" stroke-width="1.5" stroke-linecap="round"/></svg>
					{:else}
						<svg width="16" height="16" viewBox="0 0 16 16" fill="none"><circle cx="8" cy="8" r="6" stroke="currentColor" stroke-width="1.5"/><path d="M8 7V11M8 5V5.5" stroke="currentColor" stroke-width="1.5" stroke-linecap="round"/></svg>
					{/if}
				</span>
				<span class="toast-message">{toast.message}</span>
			</div>
		{/each}
	</div>
{/if}

<style>
	.toast-container {
		position: fixed;
		bottom: 20px;
		right: 20px;
		z-index: 9999;
		display: flex;
		flex-direction: column;
		gap: 8px;
		pointer-events: none;
	}
	.toast {
		display: flex;
		align-items: center;
		gap: 8px;
		padding: 10px 16px;
		border-radius: var(--radius-md, 8px);
		font-size: 13px;
		font-family: var(--font-sans, inherit);
		color: #fff;
		background: var(--bg-surface, #1e1e2e);
		border: 1px solid var(--border-subtle, #333);
		box-shadow: 0 4px 20px rgba(0,0,0,0.4);
		pointer-events: auto;
		max-width: 360px;
	}
	.toast-success {
		border-color: #22c55e;
		background: linear-gradient(135deg, rgba(34,197,94,0.15), var(--bg-surface, #1e1e2e));
		color: #86efac;
	}
	.toast-error {
		border-color: #ef4444;
		background: linear-gradient(135deg, rgba(239,68,68,0.15), var(--bg-surface, #1e1e2e));
		color: #fca5a5;
	}
	.toast-info {
		border-color: var(--border-default, #444);
		color: var(--text-secondary, #aaa);
	}
	.toast-icon {
		flex-shrink: 0;
		display: flex;
		align-items: center;
	}
	.toast-message {
		line-height: 1.3;
	}
</style>
