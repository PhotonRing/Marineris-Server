<script>
  import X from '@lucide/svelte/icons/x';
  // Editable list of strings (volumes, ports, networks).
  let { items = $bindable([]), placeholder = '', addLabel = 'Add', disabled = false, suggestions = [], id = '' } = $props();
  const listId = `list-${Math.random().toString(36).slice(2)}`;
</script>

<div class="list-editor">
  {#each items as _, i (i)}
    <div class="row nowrap">
      <input id={i === 0 ? id : undefined} class="mono" bind:value={items[i]} {placeholder} {disabled} list={suggestions.length ? listId : undefined} aria-label={`${placeholder || 'Item'} ${i + 1}`} />
      <button type="button" class="ghost small" aria-label="Remove" title="Remove" {disabled} onclick={() => (items = items.filter((__, j) => j !== i))}><X size={16} aria-hidden="true" /></button>
    </div>
  {/each}
  <button type="button" class="small" {disabled} onclick={() => (items = [...items, ''])}>+ {addLabel}</button>
  {#if suggestions.length}
    <datalist id={listId}>
      {#each suggestions as s (s)}<option value={s}></option>{/each}
    </datalist>
  {/if}
</div>
