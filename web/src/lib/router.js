// Minimal history-API router. The Go server answers every unknown path with
// index.html, so deep links work.
import { writable } from 'svelte/store';

export const path = writable(window.location.pathname);

export function navigate(to, { replace = false } = {}) {
  if (to === window.location.pathname) return;
  if (replace) history.replaceState({}, '', to);
  else history.pushState({}, '', to);
  path.set(to);
  window.scrollTo(0, 0);
}

window.addEventListener('popstate', () => path.set(window.location.pathname));

// Svelte action: <a href="/x" use:link>
export function link(node) {
  const onClick = (e) => {
    if (e.defaultPrevented || e.button !== 0 || e.metaKey || e.ctrlKey || e.shiftKey || e.altKey) return;
    const href = node.getAttribute('href');
    if (!href || !href.startsWith('/')) return;
    e.preventDefault();
    navigate(href);
  };
  node.addEventListener('click', onClick);
  return { destroy: () => node.removeEventListener('click', onClick) };
}
