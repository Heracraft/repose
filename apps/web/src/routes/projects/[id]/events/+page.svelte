<script lang="ts">
	// A project's Events tab (DECISIONS I-841): the pager and the filter by
	// kind, their place kept in the URL's events_* keys (I-662).
	import EventsCard from '$lib/components/project/EventsCard.svelte';
	import { useProject } from '$lib/components/project/context.svelte';

	const ctx = useProject();
</script>

<EventsCard projectId={ctx.id} pollFailures={ctx.pollFailures} />
