// Dropshipper Motor · arranque (se carga al final, cuando ya existen todas las vistas).
'use strict';

hydrateIcons();
window.addEventListener('DOMContentLoaded', () => {
  boot().catch((err) => { if (!$('#login').classList.contains('hidden')) return; toast(err.message, true); });
});
