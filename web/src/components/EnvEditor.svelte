<script>
  // Editable KEY = value pairs: environment variables (a value can come from
  // the stack's .env) or labels (with suggested keys and their meaning).
  import X from '@lucide/svelte/icons/x';
  let { vars = $bindable([]), disabled = false, unsetOption = true, suggestions = [], keyPlaceholder = 'NAME', addLabel = 'Add variable' } = $props();
  const listId = `keys-${Math.random().toString(36).slice(2)}`;
  const help = (key) => suggestions.find((s) => s.key === key);
</script>

<div class="list-editor">
  {#each vars as v, i (i)}
    <div class="row nowrap env-row">
      <input class="mono env-key" bind:value={v.key} placeholder={keyPlaceholder} aria-label={`Name ${i + 1}`} list={suggestions.length ? listId : undefined} {disabled} />
      <span class="muted">=</span>
      {#if v.unset}
        <input class="mono" value="" placeholder="value of the same name in the .env" aria-label={`Value ${i + 1}`} disabled />
      {:else if help(v.key)?.values}
        <select bind:value={v.value} aria-label={`Value ${i + 1}`} {disabled}>
          {#each help(v.key).values as val (val)}<option value={val}>{val}</option>{/each}
        </select>
      {:else}
        <input class="mono" bind:value={v.value} placeholder={help(v.key)?.placeholder || 'value'} aria-label={`Value ${i + 1}`} {disabled} />
      {/if}
      {#if unsetOption}
        <label class="check small" title="Take the value from the stack's .env"><input type="checkbox" bind:checked={v.unset} {disabled} /> .env</label>
      {/if}
      <button type="button" class="ghost small" aria-label="Remove" title="Remove" {disabled} onclick={() => (vars = vars.filter((_, j) => j !== i))}><X size={16} aria-hidden="true" /></button>
    </div>
    {#if help(v.key)}<div class="help small muted key-help">{help(v.key).help}</div>{/if}
  {/each}
  <button type="button" class="small" {disabled} onclick={() => (vars = [...vars, { key: '', value: '', unset: false }])}>+ {addLabel}</button>
  {#if suggestions.length}
    <datalist id={listId}>
      {#each suggestions as s (s.key)}<option value={s.key}>{s.help}</option>{/each}
    </datalist>
  {/if}
</div>
