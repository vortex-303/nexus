<script lang="ts">
	import { goto } from '$app/navigation';
	import { getWorkspaceSlug } from '$lib/api';
	import { onMount } from 'svelte';

	onMount(() => {
		const slug = getWorkspaceSlug();
		if (slug) {
			goto(`/w/${slug}`);
		} else {
			// Full navigation to landing page (served by Go at /)
			window.location.href = '/';
		}
	});
</script>
