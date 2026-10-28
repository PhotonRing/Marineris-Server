<script>
  // Logo of the login and setup screens: the full Marineris logo, or the
  // custom icon and name chosen in Settings → Branding.
  import { app } from '../lib/state.svelte.js';
  let brand = $derived(app.meta?.brand);
</script>

{#if brand?.logo || brand?.custom_name}
  <div class="auth-brand">
    <img src={brand.logo || '/branding/icon-192.png'} alt="" />
    <span class="brand-name">{brand.name}</span>
  </div>
{:else}
  <div class="auth-logo"><img src="/branding/logo-full.svg" alt="Marineris" /></div>
{/if}
