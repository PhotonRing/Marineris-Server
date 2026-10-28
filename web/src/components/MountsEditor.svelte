<script>
  // Mounts of a service: a named volume (declared in the stack, an existing
  // Docker volume, or a new one), an anonymous volume, or a host folder.
  import X from '@lucide/svelte/icons/x';
  let { mounts = $bindable([]), declared = [], dockerVolumes = [], bindRoots = [], disabled = false } = $props();
  const rootsId = `roots-${Math.random().toString(36).slice(2)}`;

  // The volume picker's value: declared:<name>, docker:<name>, new or anonymous.
  function pickValue(m) {
    if (m.type !== 'volume') return '';
    if (m.newVolume) return 'new';
    if (!m.source) return 'anonymous';
    if (declared.includes(m.source)) return `declared:${m.source}`;
    return `docker:${m.source}`;
  }
  function pick(m, value) {
    m.newVolume = value === 'new';
    m.external = value.startsWith('docker:');
    m.source = value.includes(':') ? value.slice(value.indexOf(':') + 1) : '';
    if (value === 'anonymous') m.read_only = false;
  }
  function setType(m, type) {
    m.type = type;
    m.source = type === 'bind' ? './' : declared[0] || '';
    m.external = false;
    m.newVolume = type === 'volume' && !declared.length;
  }
  function add() {
    mounts = [...mounts, { type: 'volume', source: declared[0] || '', target: '', read_only: false, external: false, newVolume: !declared.length }];
  }
  let others = $derived(dockerVolumes.filter((v) => !declared.includes(v)));
</script>

<div class="mounts">
  {#if mounts.length}
    <div class="mount-row mount-head muted small" aria-hidden="true"><span>Type</span><span>Source</span><span>Path in the container</span><span>Read-only</span><span></span></div>
  {/if}
  {#each mounts as m, i (i)}
    <div class="mount-row">
      <select value={m.type} onchange={(e) => setType(m, e.currentTarget.value)} aria-label={`Mount ${i + 1} type`} {disabled}>
        <option value="volume">Volume</option>
        <option value="bind">Host folder</option>
      </select>
      {#if m.type === 'volume'}
        <div class="row nowrap">
          <select value={pickValue(m)} onchange={(e) => pick(m, e.currentTarget.value)} aria-label={`Mount ${i + 1} volume`} {disabled}>
            {#if declared.length}
              <optgroup label="This stack">
                {#each declared as v (v)}<option value={`declared:${v}`}>{v}</option>{/each}
              </optgroup>
            {/if}
            {#if others.length}
              <optgroup label="Other Docker volumes">
                {#each others as v (v)}<option value={`docker:${v}`}>{v}</option>{/each}
              </optgroup>
            {/if}
            {#if m.source && !declared.includes(m.source) && !others.includes(m.source) && !m.newVolume}<option value={`docker:${m.source}`}>{m.source}</option>{/if}
            <option value="new">New volume…</option>
            <option value="anonymous">Anonymous (deleted with the container)</option>
          </select>
          {#if m.newVolume}
            <input class="mono" bind:value={m.source} placeholder="volume name" aria-label={`Mount ${i + 1} new volume name`} {disabled} />
          {/if}
        </div>
      {:else}
        <input class="mono" bind:value={m.source} placeholder="./data or /srv/…" list={rootsId} aria-label={`Mount ${i + 1} host folder`} {disabled} />
      {/if}
      <input class="mono" bind:value={m.target} placeholder="/data" aria-label={`Mount ${i + 1} path in the container`} {disabled} />
      <label class="check" title="The container cannot write to it"><input type="checkbox" bind:checked={m.read_only} disabled={disabled || (m.type === 'volume' && !m.source && !m.newVolume)} aria-label={`Mount ${i + 1} read-only`} /></label>
      <button type="button" class="ghost small" aria-label="Remove" title="Remove" {disabled} onclick={() => (mounts = mounts.filter((_, j) => j !== i))}><X size={16} aria-hidden="true" /></button>
    </div>
  {/each}
  <button type="button" class="small" {disabled} onclick={add}>+ Add mount</button>
  <datalist id={rootsId}>
    <option value="./">the stack's folder</option>
    {#each bindRoots as r (r)}<option value={`${r}/`}></option>{/each}
  </datalist>
  <p class="help small muted">
    Host folders must be inside the stack's folder (<code>./…</code>){#if bindRoots.length}{' or '}{bindRoots.map((r) => r + '/…').join(', ')}{/if}. A new volume is added to the stack;
    another stack's volume is used as an external volume.
  </p>
</div>
