<script>
  // An app's icon, or its initials when it has none.
  let { src = '', name = '', size = 40 } = $props();
  let initials = $derived(
    name
      .split(/[\s_-]+/)
      .filter(Boolean)
      .map((w) => w[0])
      .join('')
      .slice(0, 2)
      .toUpperCase(),
  );
</script>

{#if src}
  <img class="app-icon" src={src} alt="" style:width={`${size}px`} style:height={`${size}px`} loading="lazy" />
{:else}
  <span class="app-icon tile-initials" aria-hidden="true" style:width={`${size}px`} style:height={`${size}px`} style:font-size={`${size / 2.6}px`}>{initials}</span>
{/if}
