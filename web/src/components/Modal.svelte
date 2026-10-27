<script>
  let { open = $bindable(false), title = '', wide = false, onclose = () => {}, children, actions } = $props();
  let dlg;

  $effect(() => {
    if (!dlg) return;
    if (open && !dlg.open) dlg.showModal();
    if (!open && dlg.open) dlg.close();
  });

  function closed() {
    open = false;
    onclose();
  }
</script>

<dialog bind:this={dlg} class:wide onclose={closed}>
  {#if open}
    <div class="spread">
      <h2>{title}</h2>
      <button class="ghost small" type="button" aria-label="Close" onclick={() => (open = false)}>✕</button>
    </div>
    <div class="stack">
      {@render children?.()}
    </div>
    {#if actions}
      <div class="row mt end">
        {@render actions()}
      </div>
    {/if}
  {/if}
</dialog>
