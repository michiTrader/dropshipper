// Dropshipper Motor · arranque (se carga al final, cuando ya existen todas las vistas).
'use strict';

// Selector de tema: Claro -> Oscuro -> Automático (ver theme.js).
function paintThemeButton() {
  const m = window.Theme.mode();
  const icon = { light: 'sun', dark: 'moon', auto: 'monitor' }[m];
  const label = { light: 'Tema: Claro', dark: 'Tema: Oscuro', auto: 'Tema: Automático' }[m];
  $('#themeIcon').innerHTML = ic(icon, 15);
  $('#themeLabel').textContent = label;
}
$('#themeToggle').addEventListener('click', () => window.Theme.cycle());
document.addEventListener('themechange', paintThemeButton);

hydrateIcons();
paintThemeButton();
window.addEventListener('DOMContentLoaded', () => {
  boot().catch((err) => { if (!$('#login').classList.contains('hidden')) return; toast(err.message, true); });
});
