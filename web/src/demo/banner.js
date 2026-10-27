// Notice shown at the top of every page of the static demo.
const bar = document.createElement('div');
bar.setAttribute('role', 'note');
bar.className = 'demo-banner';
bar.innerHTML =
  '<strong>Demo</strong> — sample data that lives in your browser: nothing is real and changes are lost when you reload. ' +
  '<a href="https://github.com/PhotonRing/Marineris-Server" rel="noopener">Install Marineris</a>';
document.body.prepend(bar);
const css = new CSSStyleSheet();
css.replaceSync(`
  .demo-banner { position: sticky; top: 0; z-index: 1000; padding: 0.45rem 1rem; text-align: center; font-size: 0.85rem;
    background: var(--accent, #e8743b); color: #fff; }
  .demo-banner a { color: #fff; font-weight: 600; margin-left: 0.5rem; }
`);
document.adoptedStyleSheets = [...document.adoptedStyleSheets, css];
