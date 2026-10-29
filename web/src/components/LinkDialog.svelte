<script>
  // A link shown once (Invite, Reset link): copy button and a QR code drawn
  // here from the link, without any external service.
  import { encode } from 'uqr';
  import Copy from '@lucide/svelte/icons/copy';
  import Modal from './Modal.svelte';
  import { toast } from '../lib/state.svelte.js';

  let { open = $bindable(false), title = 'Link', url = '', children } = $props();

  let qr = $derived(url ? encode(url, { border: 2 }) : null);
  // One path for every dark module, drawn on a white background.
  let modules = $derived.by(() => {
    if (!qr) return '';
    let d = '';
    qr.data.forEach((row, y) => {
      row.forEach((dark, x) => {
        if (dark) d += `M${x} ${y}h1v1h-1z`;
      });
    });
    return d;
  });

  async function copy() {
    try {
      await navigator.clipboard.writeText(url);
      toast('Link copied');
    } catch {
      toast('Copy failed: select the link and copy it', 'error');
    }
  }
</script>

<Modal bind:open {title}>
  {@render children?.()}
  <div class="alert">This link is shown only once. Pass it on now: anyone holding it can use it.</div>
  <div class="row">
    <input class="mono grow" value={url} readonly aria-label="Link" onfocus={(e) => e.currentTarget.select()} />
    <button class="primary" type="button" onclick={copy}><Copy size={14} aria-hidden="true" />Copy</button>
  </div>
  {#if qr}
    <svg
      class="qr"
      viewBox="0 0 {qr.size} {qr.size}"
      role="img"
      aria-label="QR code of the link"
      shape-rendering="crispEdges"
    >
      <rect width={qr.size} height={qr.size} fill="#fff" />
      <path d={modules} fill="#000" />
    </svg>
  {/if}
</Modal>

<style>
  .grow {
    flex: 1;
    min-width: 0;
  }
  .qr {
    display: block;
    width: 220px;
    max-width: 100%;
    height: auto;
    margin: 0 auto;
  }
</style>
