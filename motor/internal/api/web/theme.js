// Dropshipper Motor · tema claro/oscuro. Se carga en <head> para que no haya parpadeo.
'use strict';
(function () {
  const KEY = 'ds_theme';
  const mq = window.matchMedia ? window.matchMedia('(prefers-color-scheme: dark)') : null;
  const mode = () => { const m = localStorage.getItem(KEY); return m === 'light' || m === 'dark' ? m : 'auto'; };
  const effective = () => { const m = mode(); return m === 'auto' ? (mq && mq.matches ? 'dark' : 'light') : m; };
  function apply() {
    const e = effective();
    const root = document.documentElement;
    root.dataset.theme = e; root.dataset.themeMode = mode();
    const meta = document.querySelector('meta[name="theme-color"]');
    if (meta) meta.setAttribute('content', e === 'dark' ? '#0e1420' : '#f4f6f9');
    document.dispatchEvent(new CustomEvent('themechange', { detail: { mode: mode(), effective: e } }));
  }
  window.Theme = {
    mode, effective,
    set(m) { if (m === 'auto') localStorage.removeItem(KEY); else localStorage.setItem(KEY, m); apply(); },
    // Claro -> Oscuro -> Automático
    cycle() { this.set({ light: 'dark', dark: 'auto', auto: 'light' }[mode()]); },
  };
  if (mq) (mq.addEventListener ? mq.addEventListener('change', apply) : mq.addListener(apply));
  apply();
})();
