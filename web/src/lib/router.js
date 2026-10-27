// Minimal history-API router. The Go server answers every unknown path with
// index.html, so deep links work. The static demo (GitHub Pages, which has no
// such fallback) keeps the path in the URL fragment instead: #/routes.
import { writable } from 'svelte/store';

const DEMO = import.meta.env.VITE_DEMO === '1';
const current = () => (DEMO ? window.location.hash.slice(1) || '/' : window.location.pathname);

export const path = writable(current());

export function navigate(to, { replace = false } = {}) {
  if (to === current()) return;
  const url = DEMO ? '#' + to : to;
  if (replace) history.replaceState({}, '', url);
  else history.pushState({}, '', url);
  path.set(to);
  window.scrollTo(0, 0);
}

window.addEventListener('popstate', () => path.set(current()));

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
