// Dropshipper · panel del dueño (sin dependencias, funciona en escritorio y móvil).
'use strict';

const S = {
  token: localStorage.getItem('ds_token') || '',
  view: 'inicio',
  status: null,
  product: null,
  contacts: [],
  chatPhone: null,
  chatLastId: 0,
  simPhone: localStorage.getItem('ds_sim') || '',
  simLastId: 0,
  typing: {},
  proofs: 0,
};

// ---------- helpers ----------
const $ = (sel, root = document) => root.querySelector(sel);
const $$ = (sel, root = document) => [...root.querySelectorAll(sel)];
const esc = (s) => String(s ?? '').replace(/[&<>"']/g, (c) => ({ '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#39;' }[c]));
const cop = (n) => '$' + Math.round(Number(n) || 0).toLocaleString('es-CO');
const usd = (n) => 'US$' + (Number(n) || 0).toFixed(2);
const pct = (f) => Math.round((Number(f) || 0) * 1000) / 10 + '%';

function fmtTime(iso) {
  if (!iso) return '';
  const d = new Date(iso);
  const same = d.toDateString() === new Date().toDateString();
  const hm = d.toLocaleTimeString('es-CO', { hour: '2-digit', minute: '2-digit' });
  return same ? hm : d.toLocaleDateString('es-CO', { day: '2-digit', month: 'short' }) + ' ' + hm;
}

// WhatsApp-style formatting on escaped text: *bold*, _italic_, links.
function waFormat(text) {
  let h = esc(text);
  h = h.replace(/\*([^*\n]+)\*/g, '<b>$1</b>').replace(/(^|\s)_([^_\n]+)_/g, '$1<i>$2</i>');
  h = h.replace(/(https?:\/\/[^\s<]+)/g, '<a href="$1" target="_blank" rel="noopener">$1</a>');
  return h;
}

function toast(msg, bad = false) {
  const t = $('#toast');
  t.textContent = msg;
  t.className = 'toast' + (bad ? ' bad' : '');
  clearTimeout(toast._t);
  toast._t = setTimeout(() => t.classList.add('hidden'), 3800);
}

async function api(path, opts = {}) {
  const headers = { 'Content-Type': 'application/json' };
  if (S.token) headers.Authorization = 'Bearer ' + S.token;
  const res = await fetch(path, {
    ...opts, headers: { ...headers, ...(opts.headers || {}) },
    body: opts.body && typeof opts.body !== 'string' ? JSON.stringify(opts.body) : opts.body,
  });
  let data = null;
  try { data = await res.json(); } catch (_) { /* empty body */ }
  if (res.status === 401) { showLogin(); throw new Error('Token inválido'); }
  if (!res.ok) throw new Error((data && data.error) || ('HTTP ' + res.status));
  return data;
}
const post = (path, body) => api(path, { method: 'POST', body: body || {} });
const del = (path) => api(path, { method: 'DELETE' });

const mediaURL = (file) => '/media/' + encodeURIComponent(file) + (S.token ? '?token=' + encodeURIComponent(S.token) : '');

const STATUS = {
  borrador: ['Borrador', ''], esperando_pago: ['Esperando pago', 'warn'], por_aprobar: ['Por aprobar', 'brand'],
  aprobado: ['Aprobado', 'info'], en_dropi: ['En Dropi', 'info'], en_transito: ['En tránsito', 'info'],
  entregado: ['Entregado', 'ok'], novedad: ['Novedad', 'warn'], devuelto: ['Devuelto', 'bad'],
  rechazado: ['Rechazado', 'bad'], cancelado: ['Cancelado', 'bad'],
};
const tag = (map, k) => { if (!k) return ''; const [l, c] = map[k] || [k, '']; return `<span class="tag ${c}">${esc(l)}</span>`; };
const statusTag = (s) => tag(STATUS, s);
const PAY = { contraentrega: ['Contraentrega', 'ok'], anticipo_parcial: ['Anticipo parcial', 'warn'], anticipado_total: ['Pago anticipado', 'bad'] };
const payTag = (p) => tag(PAY, p);
const RISK = { normal: ['Riesgo normal', 'ok'], lejano: ['Zona lejana', 'warn'], dificil_acceso: ['Difícil acceso', 'warn'], zona_roja: ['Zona roja', 'bad'] };
const riskTag = (r) => tag(RISK, r);
const STAGE = { nuevo: 'Nuevo', datos: 'Tomando datos', esperando_pago: 'Esperando pago', cerrado: 'Venta cerrada', perdido: 'Perdido' };

const contactLabel = (p) => (p.startsWith('sim-') ? 'Prueba ' + p.slice(4) : '+' + p);
const contactName = (c) => c.name || contactLabel(c.phone);

// ---------- auth ----------
function showLogin() {
  $('#app').classList.add('hidden');
  $('#login').classList.remove('hidden');
}
$('#loginForm').addEventListener('submit', async (e) => {
  e.preventDefault();
  S.token = $('#tokenInput').value.trim();
  localStorage.setItem('ds_token', S.token);
  $('#loginError').textContent = '';
  try { await boot(); } catch (err) { $('#loginError').textContent = err.message; }
});

// ---------- routing ----------
const VIEWS = {
  inicio: () => renderInicio(),
  aprobaciones: () => renderAprobaciones(),
  chats: () => renderChats(),
  pedidos: () => renderPedidos(),
  simulador: () => window.renderSimulador && window.renderSimulador(),
  producto: () => window.renderProducto && window.renderProducto(),
  ajustes: () => window.renderAjustes && window.renderAjustes(),
};

function go(view) {
  if (!VIEWS[view]) view = 'inicio';
  S.view = view;
  $$('.view').forEach((v) => v.classList.toggle('active', v.id === 'view-' + view));
  $$('#nav a').forEach((a) => a.classList.toggle('active', a.dataset.view === view));
  Promise.resolve(VIEWS[view]()).catch((err) => toast(err.message, true));
}
window.addEventListener('hashchange', () => go(location.hash.slice(1)));

// ---------- live events ----------
let es = null;
function connectEvents() {
  if (es) es.close();
  es = new EventSource('/api/eventos' + (S.token ? '?token=' + encodeURIComponent(S.token) : ''));
  es.onopen = () => $('#liveDot').classList.add('on');
  es.onerror = () => $('#liveDot').classList.remove('on');
  es.onmessage = (m) => { try { onEvent(JSON.parse(m.data)); } catch (_) { /* ignore */ } };
}

let refreshTimer = null;
function scheduleRefresh() {
  clearTimeout(refreshTimer);
  refreshTimer = setTimeout(() => {
    refreshBadges();
    const safe = (p) => Promise.resolve(p).catch(() => {});
    if (S.view === 'inicio') safe(renderInicio());
    if (S.view === 'aprobaciones') safe(renderAprobaciones());
    if (S.view === 'pedidos') safe(renderPedidos());
    if (S.view === 'chats') safe(loadContacts());
    if (S.view === 'simulador' && window.loadSimSide) safe(window.loadSimSide());
  }, 350);
}

function onEvent(ev) {
  if (ev.type === 'escribiendo') S.typing[ev.phone] = true;
  if (ev.type === 'listo') delete S.typing[ev.phone];
  if (ev.phone && ev.phone === S.chatPhone && S.view === 'chats' && window.loadChatMessages) window.loadChatMessages();
  if (ev.phone && ev.phone === S.simPhone && S.view === 'simulador' && window.loadSimMessages) window.loadSimMessages();
  if (ev.type === 'alerta' && ev.text) toast('🔔 ' + ev.text);
  if (ev.type !== 'escribiendo') scheduleRefresh();
}

async function refreshBadges() {
  try {
    const st = await api('/api/stats');
    S.stats = st;
    $('#badgeAprob').textContent = (st.por_aprobar + S.proofs) || '';
    $('#badgeChats').textContent = S.contacts.reduce((n, c) => n + (c.unread || 0), 0) || '';
  } catch (_) { /* offline */ }
}

// ---------- INICIO ----------
async function renderInicio() {
  $('#today').textContent = new Date().toLocaleDateString('es-CO', { weekday: 'long', day: 'numeric', month: 'long' });
  const [st, alerts, contacts] = await Promise.all([api('/api/stats'), api('/api/alertas'), api('/api/contactos')]);
  S.contacts = contacts;
  renderSetupWarnings();
  const k = (label, value, hot) => `<div class="kpi ${hot ? 'hot' : ''}"><div class="l">${label}</div><div class="v">${value}</div></div>`;
  $('#kpis').innerHTML = [
    k('Ventas aprobadas hoy', cop(st.ventas_hoy)),
    k('Ventas del mes', cop(st.ventas_mes) + `<div class="small muted">${st.pedidos_mes} pedidos</div>`),
    k('Por aprobar', st.por_aprobar, st.por_aprobar > 0),
    k('Esperando pago', st.esperando_pago, st.esperando_pago > 0),
    k('Chats hoy', st.chats_hoy),
    k('Activos 24 h', st.chats_activos_24h),
    k('Mensajes de la IA hoy', st.mensajes_ia_hoy),
    k('Chats con IA en pausa', st.ia_pausada, st.ia_pausada > 0),
  ].join('');

  $('#alertList').innerHTML = alerts.length ? alerts.map((a) => `
    <div class="item" data-alert="${a.id}" data-phone="${esc(a.phone)}" data-kind="${esc(a.kind)}">
      <div class="item-top"><b>${alertIcon(a.kind)} ${esc(alertLabel(a.kind))}</b><span class="small muted">${fmtTime(a.created_at)}</span></div>
      <div class="small">${esc(a.message)}</div>
      <div class="actions"><button class="btn small" data-open>Ver</button><button class="btn small" data-resolve>Listo</button></div>
    </div>`).join('') : '<div class="empty">Todo al día 🎉</div>';
  $$('#alertList .item').forEach((el) => {
    $('[data-resolve]', el).onclick = async () => { await post(`/api/alertas/${el.dataset.alert}/resolver`); renderInicio(); refreshBadges(); };
    $('[data-open]', el).onclick = () => {
      const kind = el.dataset.kind;
      if (kind === 'aprobacion' || kind === 'comprobante') location.hash = 'aprobaciones';
      else if (el.dataset.phone) openChat(el.dataset.phone);
    };
  });

  $('#recentChats').innerHTML = contacts.slice(0, 8).map(contactItem).join('') || '<div class="empty">Aún no hay conversaciones. Prueba el 🧪 Simulador.</div>';
  $$('#recentChats .item').forEach((el) => el.onclick = () => openChat(el.dataset.phone));
}

const alertIcon = (k) => ({ escalamiento: '🙋', aprobacion: '🛒', comprobante: '📸', error: '⚠️', info: 'ℹ️' }[k] || '🔔');
const alertLabel = (k) => ({ escalamiento: 'Cliente pide atención humana', aprobacion: 'Pedido por aprobar', comprobante: 'Comprobante de pago', error: 'Error', info: 'Aviso' }[k] || k);

function contactItem(c) {
  const flags = [c.ai_paused ? '<span class="tag warn">IA en pausa</span>' : '', c.simulated ? '<span class="tag">prueba</span>' : '',
    c.stage && c.stage !== 'nuevo' ? `<span class="tag info">${esc(STAGE[c.stage] || c.stage)}</span>` : ''].join(' ');
  return `<div class="item ${c.phone === S.chatPhone ? 'active' : ''}" data-phone="${esc(c.phone)}">
    <div class="item-top"><b>${esc(contactName(c))}</b><span class="small muted">${fmtTime(c.last_message_at)}</span></div>
    <div class="item-sub">${esc(c.last_preview || '')}</div>
    <div class="row gap small" style="margin-top:4px">${flags}${c.unread ? `<span class="badge" style="margin-left:auto">${c.unread}</span>` : ''}</div>
  </div>`;
}

function renderSetupWarnings() {
  const st = S.status;
  if (!st) return;
  const w = [];
  if (!st.ia.configurada) w.push('🤖 La IA no tiene API key. Pon tu clave de DeepSeek en <code>LLM_API_KEY</code> dentro de <code>motor/.env</code> y reinicia.');
  if (!st.whatsapp.configurado) w.push('💬 WhatsApp aún no está conectado: por ahora solo funciona el 🧪 Simulador. Ver <a href="#ajustes">Ajustes</a>.');
  if (!st.pagos.listo) w.push('💳 Faltan los datos para cobrar anticipos (llave Bre-B o cuenta Bancolombia) en <code>motor/negocio.yaml</code>.');
  if (!st.producto.costo_confirmado) w.push(`📦 Confirma en Dropi el costo y el flete de ${esc(st.producto.nombre)}: la rentabilidad mostrada es estimada.`);
  $('#setupWarnings').innerHTML = w.map((x) => `<div class="warnbox">${x}</div>`).join('');
}

// ---------- order cards ----------
function profitOf(o) { try { return JSON.parse(o.profit_json || '{}'); } catch (_) { return {}; } }

function orderKV(o) {
  const addr = [o.address, o.address_refs && '(' + o.address_refs + ')'].filter(Boolean).join(' ');
  return `<dl class="kv">
    <dt>Producto</dt><dd>${esc(o.product_name)} · ${esc(o.offer_label || 'sin oferta')}</dd>
    <dt>Cliente</dt><dd>${esc(o.customer_name || '-')} · CC ${esc(o.customer_id_number || '-')}</dd>
    <dt>Celular</dt><dd>${esc(o.customer_phone || '-')}</dd>
    <dt>Dirección</dt><dd>${esc(addr || '-')}</dd>
    <dt>Barrio / ciudad</dt><dd>${esc(o.neighborhood || '-')} · ${esc(o.city || '-')}, ${esc(o.department || '-')}</dd>
    <dt>Transportadora</dt><dd>${esc(o.carrier || '-')} <span class="small muted">(sugerida)</span></dd>
    <dt>Regla de zona</dt><dd class="small">${esc(o.zone_reason || '-')}</dd>
    ${o.notes ? `<dt>Notas</dt><dd class="small">${esc(o.notes)}</dd>` : ''}
  </dl>`;
}

function moneyBox(o) {
  const p = profitOf(o); const r = p.rentabilidad || {};
  const warn = [];
  if (p.costo_confirmado === false) warn.push('Costo del producto sin confirmar en Dropi: cifras estimadas.');
  if (r.advertencia) warn.push(r.advertencia);
  return `<div class="money">
      <div><b>${cop(o.total)}</b><span>Total</span></div>
      <div><b>${o.advance_amount ? cop(o.advance_amount) : '—'}</b><span>Anticipo</span></div>
      <div><b>${cop(o.cod_amount)}</b><span>Cobra transportadora</span></div>
      <div><b>${cop(r.ganancia_si_entrega)}</b><span>Ganancia si entrega</span></div>
      <div><b>${cop(r.ganancia_esperada)}</b><span>Esperada (dev. ${pct(r.tasa_devolucion)})</span></div>
      <div><b>${cop(p.cpa_objetivo)}</b><span>CPA máx. anuncios</span></div>
    </div>${warn.map((x) => `<div class="small" style="color:#fcd34d">⚠️ ${esc(x)}</div>`).join('')}`;
}

function orderCard(o, actions) {
  return `<div class="card ocard" data-order="${o.id}">
    <div class="head"><b>#${o.id} · ${esc(o.customer_name || contactLabel(o.phone))}</b>${statusTag(o.status)}</div>
    <div class="row gap wrap">${payTag(o.payment_policy)} ${riskTag(o.risk)} <span class="small muted">${fmtTime(o.created_at)}</span></div>
    ${orderKV(o)}${moneyBox(o)}
    <div class="actions">${actions}<button class="btn small" data-chat>💬 Ver chat</button></div>
  </div>`;
}

// ---------- APROBACIONES ----------
async function renderAprobaciones() {
  const [pending, waiting, approved] = await Promise.all([
    api('/api/pedidos?estado=por_aprobar'), api('/api/pedidos?estado=esperando_pago'), api('/api/pedidos?estado=aprobado,en_dropi'),
  ]);
  const proofs = waiting.filter((o) => o.payment_status === 'comprobante_recibido');
  S.proofs = proofs.length;

  $('#approvalCards').innerHTML = pending.length ? pending.map((o) => orderCard(o,
    '<button class="btn ok" data-approve>✅ Aceptar</button><button class="btn" data-edit>✏️ Editar</button><button class="btn danger" data-reject>Rechazar</button>')).join('')
    : '<div class="empty card">No hay pedidos por aprobar.</div>';

  const waitingAll = [...proofs, ...waiting.filter((o) => o.payment_status !== 'comprobante_recibido')];
  $('#paymentCards').innerHTML = waitingAll.length ? waitingAll.map((o) => {
    const proof = o.payment_status === 'comprobante_recibido';
    const img = o.payment_proof && /\.(jpe?g|png|webp)$/i.test(o.payment_proof) ? `<img class="proof" src="${mediaURL(o.payment_proof)}" alt="comprobante">` : '';
    const info = proof
      ? `<div class="okbox small">📸 El cliente envió un comprobante. <b>Verifica en tu app de Bancolombia</b> que llegaron ${cop(o.advance_amount)} (ref ${esc(o.payment_ref)}) antes de confirmar.</div>${img}`
      : `<div class="small muted">Esperando que el cliente pague ${cop(o.advance_amount)} (ref ${esc(o.payment_ref)}).${o.payment_link ? ` Link: <a href="${esc(o.payment_link)}" target="_blank">${esc(o.payment_link)}</a>` : ''}</div>`;
    return orderCard(o, `${info}<div class="actions" style="width:100%"><button class="btn ok" data-payok>💰 Pago recibido</button>${proof ? '<button class="btn danger" data-paybad>No llegó el pago</button>' : ''}<button class="btn" data-edit>✏️ Editar</button><button class="btn danger" data-reject>Cancelar pedido</button></div>`);
  }).join('') : '<div class="empty card">No hay pagos pendientes.</div>';

  $('#dispatchCards').innerHTML = approved.length ? approved.map((o) => orderCard(o,
    `<button class="btn primary" data-copy>📋 Copiar datos para Dropi</button><button class="btn ok" data-dispatch>🚚 Registrar guía</button>${o.tracking_number ? `<span class="tag info">Guía ${esc(o.tracking_number)}</span>` : ''}`)).join('')
    : '<div class="empty card">No hay pedidos aprobados pendientes de subir a Dropi.</div>';

  bindOrderButtons($('#view-aprobaciones'), [...pending, ...waiting, ...approved]);
  refreshBadges();
}

function bindOrderButtons(root, orders) {
  const byId = Object.fromEntries(orders.map((o) => [o.id, o]));
  $$('[data-order]', root).forEach((card) => {
    const o = byId[card.dataset.order];
    if (!o) return;
    const act = async (btn, fn, okMsg) => {
      btn.disabled = true;
      try { await fn(); toast(okMsg); scheduleRefresh(); } catch (err) { toast(err.message, true); btn.disabled = false; }
    };
    const on = (sel, fn) => { const b = $(sel, card); if (b) b.onclick = () => fn(b); };
    on('[data-approve]', (b) => act(b, () => post(`/api/pedidos/${o.id}/aprobar`), `Pedido #${o.id} aprobado. El cliente fue avisado ✅`));
    on('[data-payok]', (b) => { if (confirm(`¿Confirmas que recibiste ${cop(o.advance_amount)} en tu cuenta?`)) act(b, () => post(`/api/pedidos/${o.id}/pago-confirmado`), 'Pago confirmado 💰'); });
    on('[data-paybad]', (b) => act(b, () => post(`/api/pedidos/${o.id}/pago-rechazado`), 'Comprobante marcado como no válido. Escríbele al cliente.'));
    on('[data-reject]', (b) => { const m = prompt('Motivo (no se envía al cliente):', ''); if (m !== null) act(b, () => post(`/api/pedidos/${o.id}/rechazar`, { motivo: m }), 'Pedido rechazado'); });
    on('[data-edit]', () => openEditOrder(o));
    on('[data-dispatch]', () => openDispatch(o));
    on('[data-copy]', () => copyForDropi(o));
    on('[data-chat]', () => openChat(o.phone));
  });
}

function dropiText(o) {
  return [
    `Nombre: ${o.customer_name}`, `Cédula: ${o.customer_id_number}`, `Celular: ${o.customer_phone}`,
    `Departamento: ${o.department}`, `Ciudad: ${o.city}`, `Barrio: ${o.neighborhood}`,
    `Dirección: ${o.address}`, o.address_refs ? `Referencias: ${o.address_refs}` : '',
    `Producto: ${o.product_name} x${o.units} (${o.offer_label})`, `Total: ${o.total}`,
    `Valor a recaudar (contraentrega): ${o.cod_amount}`, o.advance_amount ? `Anticipo pagado: ${o.advance_amount}` : '',
    `Transportadora sugerida: ${o.carrier}`, o.notes ? `Notas: ${o.notes}` : '',
  ].filter(Boolean).join('\n');
}

async function copyForDropi(o) {
  const text = dropiText(o);
  try { await navigator.clipboard.writeText(text); toast('Datos copiados. Pégalos al crear la orden en Dropi 📋'); }
  catch (_) { openModal(`<h3>Datos para Dropi</h3><textarea rows="14" readonly>${esc(text)}</textarea><div class="actions"><button class="btn" data-close>Cerrar</button></div>`); }
}

// ---------- modal ----------
function openModal(html) {
  $('#modalBox').innerHTML = html;
  $('#modal').classList.remove('hidden');
  $$('[data-close]', $('#modalBox')).forEach((b) => b.onclick = closeModal);
}
function closeModal() { $('#modal').classList.add('hidden'); }
$('#modal').addEventListener('click', (e) => { if (e.target.id === 'modal') closeModal(); });

const field = (name, label, value) => `<label class="field"><span>${label}</span><input name="${name}" value="${esc(value || '')}"></label>`;

function openEditOrder(o) {
  const offers = (S.product && S.product.producto.ofertas) || [];
  openModal(`<h3>Editar pedido #${o.id}</h3>
    <form id="editForm">
      <label class="field"><span>Oferta</span><select name="oferta_id">${offers.map((x) => `<option value="${esc(x.id)}" ${x.id === o.offer_id ? 'selected' : ''}>${esc(x.etiqueta)} · ${cop(x.precio)}</option>`).join('')}</select></label>
      ${field('nombre_completo', 'Nombre', o.customer_name)}${field('cedula', 'Cédula', o.customer_id_number)}${field('celular', 'Celular', o.customer_phone)}
      ${field('departamento', 'Departamento', o.department)}${field('ciudad', 'Ciudad', o.city)}${field('barrio', 'Barrio', o.neighborhood)}
      ${field('direccion', 'Dirección', o.address)}${field('referencias', 'Referencias', o.address_refs)}${field('notas', 'Notas', o.notes)}
      <label class="field"><span>Riesgo de la zona (cambia la forma de pago)</span><select name="riesgo">
        <option value="">Automático según reglas (${esc(o.risk || '-')})</option>
        <option value="normal">Normal → contraentrega</option><option value="lejano">Lejano → anticipo parcial</option>
        <option value="dificil_acceso">Difícil acceso → anticipo mayor</option><option value="zona_roja">Zona roja → pago 100% anticipado</option>
      </select></label>
      <div class="actions"><button class="btn primary" type="submit">Guardar</button><button class="btn" type="button" data-close>Cancelar</button></div>
    </form>`);
  $('#editForm').onsubmit = async (e) => {
    e.preventDefault();
    try { await post(`/api/pedidos/${o.id}/editar`, Object.fromEntries(new FormData(e.target).entries())); closeModal(); toast('Pedido actualizado'); scheduleRefresh(); }
    catch (err) { toast(err.message, true); }
  };
}

function openDispatch(o) {
  openModal(`<h3>Registrar despacho #${o.id}</h3>
    <p class="small muted">Crea la orden en Dropi (usa "Copiar datos para Dropi") y pega aquí el número de guía. Al guardar, el cliente recibe la guía por WhatsApp.</p>
    <form id="dispForm">
      ${field('dropi_id', 'ID de la orden en Dropi', o.dropi_order_id)}
      ${field('guia', 'Número de guía', o.tracking_number)}
      ${field('transportadora', 'Transportadora', o.carrier)}
      <div class="actions"><button class="btn ok" type="submit">Guardar y avisar al cliente</button><button class="btn" type="button" data-close>Cancelar</button></div>
    </form>`);
  $('#dispForm').onsubmit = async (e) => {
    e.preventDefault();
    try { await post(`/api/pedidos/${o.id}/despachar`, Object.fromEntries(new FormData(e.target).entries())); closeModal(); toast('Despacho registrado 🚚'); scheduleRefresh(); }
    catch (err) { toast(err.message, true); }
  };
}

// ---------- PEDIDOS ----------
async function renderPedidos() {
  const f = $('#orderFilter').value;
  const orders = await api('/api/pedidos' + (f ? '?estado=' + encodeURIComponent(f) : ''));
  $('#orderTable').innerHTML = `<thead><tr><th>#</th><th>Fecha</th><th>Cliente</th><th>Ciudad</th><th>Oferta</th><th>Total</th><th>Pago</th><th>Estado</th><th>Guía</th></tr></thead>
    <tbody>${orders.map((o) => `<tr data-id="${o.id}">
      <td>${o.id}</td><td>${fmtTime(o.created_at)}</td><td>${esc(o.customer_name || contactLabel(o.phone))}</td><td>${esc(o.city)}</td>
      <td>${esc(o.offer_label || '-')}</td><td>${cop(o.total)}</td><td>${payTag(o.payment_policy)}</td><td>${statusTag(o.status)}</td><td>${esc(o.tracking_number || '')}</td>
    </tr>`).join('') || '<tr><td colspan="9" class="empty">Sin pedidos</td></tr>'}</tbody>`;
  $$('#orderTable tbody tr[data-id]').forEach((tr) => tr.onclick = () => openOrderDetail(orders.find((o) => String(o.id) === tr.dataset.id)));
}
$('#orderFilter').addEventListener('change', () => renderPedidos().catch((e) => toast(e.message, true)));

function openOrderDetail(o) {
  const statusBtns = ['en_transito', 'entregado', 'novedad', 'devuelto'].map((s) => `<button class="btn small" data-st="${s}">${STATUS[s][0]}</button>`).join('');
  const shipped = ['aprobado', 'en_dropi', 'en_transito', 'novedad'].includes(o.status);
  openModal(`<div class="row gap"><h3 style="margin:0">Pedido #${o.id}</h3>${statusTag(o.status)}</div>
    <div class="row gap wrap" style="margin-top:6px">${payTag(o.payment_policy)} ${riskTag(o.risk)}</div>
    ${orderKV(o)}${moneyBox(o)}
    ${o.tracking_number ? `<p>🚚 ${esc(o.carrier)} · guía <b>${esc(o.tracking_number)}</b>${o.dropi_order_id ? ' · Dropi ' + esc(o.dropi_order_id) : ''}</p>` : ''}
    ${shipped ? `<h4>Actualizar estado</h4><div class="actions">${statusBtns}</div>` : ''}
    <div class="actions"><button class="btn" data-copy>📋 Copiar datos</button><button class="btn" data-chatm>💬 Ver chat</button><button class="btn" data-close>Cerrar</button></div>`);
  $$('[data-st]', $('#modalBox')).forEach((b) => b.onclick = async () => {
    try { await post(`/api/pedidos/${o.id}/estado`, { estado: b.dataset.st }); closeModal(); toast('Estado actualizado'); renderPedidos(); } catch (err) { toast(err.message, true); }
  });
  $('[data-copy]', $('#modalBox')).onclick = () => copyForDropi(o);
  $('[data-chatm]', $('#modalBox')).onclick = () => { closeModal(); openChat(o.phone); };
}

// ---------- chats (lista; el detalle está en chat.js) ----------
async function loadContacts() {
  S.contacts = await api('/api/contactos');
  const q = ($('#chatSearch').value || '').toLowerCase();
  const list = S.contacts.filter((c) => !q || contactName(c).toLowerCase().includes(q) || c.phone.includes(q));
  $('#contactList').innerHTML = list.map(contactItem).join('') || '<div class="empty">Sin conversaciones</div>';
  $$('#contactList .item').forEach((el) => el.onclick = () => openChat(el.dataset.phone));
  refreshBadges();
}
$('#chatSearch').addEventListener('input', () => loadContacts().catch(() => {}));

async function renderChats() {
  await loadContacts();
  if (S.chatPhone && window.showChat) window.showChat(S.chatPhone);
}

function openChat(phone) {
  S.chatPhone = phone;
  S.chatLastId = 0;
  if (location.hash !== '#chats') location.hash = 'chats'; else renderChats();
}

// ---------- boot ----------
async function boot() {
  S.status = await api('/api/estado');
  try { S.product = await api('/api/producto'); } catch (_) { S.product = null; }
  $('#login').classList.add('hidden');
  $('#app').classList.remove('hidden');
  connectEvents();
  go(location.hash.slice(1) || 'inicio');
  refreshBadges();
}

Object.assign(window, {
  S, $, $$, esc, cop, usd, pct, fmtTime, waFormat, toast, api, post, del, mediaURL, statusTag, payTag, riskTag,
  contactName, contactLabel, openChat, openModal, closeModal, loadContacts, profitOf, orderKV, moneyBox, STATUS, STAGE, scheduleRefresh, boot,
});

window.addEventListener('DOMContentLoaded', () => {
  boot().catch((err) => { if (!/Token/.test(err.message)) toast(err.message, true); });
});
