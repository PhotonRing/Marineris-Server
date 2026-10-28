<script>
  // Status of a service or a stack (see internal/api/stack_services.go).
  let { state = '' } = $props();
  const look = {
    running: ['ok', 'running'],
    starting: ['warn', 'starting'],
    unhealthy: ['error', 'unhealthy'],
    partial: ['warn', 'partly running'],
    stopped: ['error', 'stopped'],
    not_deployed: ['', 'not deployed'],
  };
  let [cls, label] = $derived(look[state] || ['', state || 'unknown']);
</script>

<span class="badge {cls}"><span class="dot {cls}" aria-hidden="true"></span>{label}</span>
