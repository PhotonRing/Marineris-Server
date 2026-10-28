<script>
  // Editable environment variables: KEY = value, or KEY taken from the .env.
  let { vars = $bindable([]), disabled = false } = $props();
</script>

<div class="list-editor">
  {#each vars as v, i (i)}
    <div class="row nowrap env-row">
      <input class="mono env-key" bind:value={v.key} placeholder="NAME" aria-label={`Variable ${i + 1} name`} {disabled} />
      <span class="muted">=</span>
      {#if v.unset}
        <input class="mono" value="" placeholder="value of the same name in the .env" aria-label={`Variable ${i + 1} value`} disabled />
      {:else}
        <input class="mono" bind:value={v.value} placeholder="value" aria-label={`Variable ${i + 1} value`} {disabled} />
      {/if}
      <label class="check small" title="Take the value from the stack's .env"><input type="checkbox" bind:checked={v.unset} {disabled} /> .env</label>
      <button type="button" class="ghost small" aria-label="Remove" title="Remove" {disabled} onclick={() => (vars = vars.filter((_, j) => j !== i))}>✕</button>
    </div>
  {/each}
  <button type="button" class="small" {disabled} onclick={() => (vars = [...vars, { key: '', value: '', unset: false }])}>+ Add variable</button>
</div>
