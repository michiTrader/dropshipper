// Dropshipper · vistas de conversación, simulador, producto y ajustes.
'use strict';

// ---------- CHAT (detalle) ----------
function msgHTML(m, simulator) {
  const side = m.direction === 'in' ? 'in' : 'out';
  const who = { cliente: '', ia: '🤖 IA', dueno: '👤 Tú', sistema: '⚙️ Sistema' }[m.sender] || '';
  let body = waFormat(m.body || '');
  if (m.kind === 'image') {
    body = (m.media_path ? `<img src="${mediaURL(m.media_path)}" alt="imagen">` : '📷 <i>Imagen</i>') + (m.body ? '<div>' + waFormat(m.body) + '</div>' : '');
  } else if (m.kind === 'audio') {
    body = '🎤 <i>Nota de voz</i>';
  } else if (m.kind !== 'text' && !m.body) {
    body = `<i>[${esc(m.kind)}]</i>`;
  }
  const ticks = { enviado: '✓', delivered: '✓✓', read: '✓✓ leído', fallido: '⚠️ no enviado', failed: '⚠️ no entregado', simulado: '', sin_whatsapp: '⚠️ sin WhatsApp' }[m.status] ?? '';
  const meta = [simulator && m.sender !== 'cliente' ? who : (!simulator ? who : ''), fmtTime(m.created_at), side === 'out' ? ticks : ''].filter(Boolean).join(' · ');
  return `<div class="msg ${side} ${esc(m.sender)}">${body}<span class="meta">${esc(meta)}</span></div>`;
}

async function showChat(phone) {
  const pane = $('#chatPane');
  $('.chat-layout').classList.add('has-chat');
  let data;
  try { data = await api('/api/contactos/' + encodeURIComponent(phone)); }
  catch (err) { pane.innerHTML = `<div class="empty">${esc(err.message)}</div>`; return; }
  const c = data.contacto;
  const open = data.pedidos.find((o) => ['borrador', 'esperando_pago', 'por_aprobar'].includes(o.status)) || data.pedidos[0];
  pane.innerHTML = `
    <div class="chat-head">
      <button class="btn small" id="chatBack" title="Volver">←</button>
      <div class="who"><b>${esc(contactName(c))}</b><div class="small muted">${c.simulated ? 'Chat de prueba' : '+' + esc(c.phone)} · ${esc(STAGE[c.stage] || c.stage)}</div></div>
      ${open ? `<span class="row gap">${statusTag(open.status)} <span class="small">#${open.id} ${cop(open.total)}</span></span>` : ''}
      <button class="btn small" id="chatAudit">🧠 Qué hizo la IA</button>
      ${c.ai_paused
        ? '<button class="btn small ok" id="chatResume">🤖 Devolver a la IA</button>'
        : '<button class="btn small" id="chatTake">✋ Yo tomo</button>'}
    </div>
    ${c.ai_paused ? `<div class="paused-bar">✋ La IA está en pausa en este chat: respondes tú. <button class="btn small" id="chatResume2">Devolver a la IA</button></div>` : ''}
    <div class="chat-body" id="chatBody"></div>
    <form class="chat-input" id="chatForm">
      <textarea id="chatText" rows="1" placeholder="${c.ai_paused ? 'Escribe tu mensaje…' : 'Escribir aquí pausa la IA en este chat (Yo tomo)…'}"></textarea>
      <button class="btn primary" type="submit">Enviar</button>
    </form>`;
  $('#chatBack').onclick = () => { S.chatPhone = null; $('.chat-layout').classList.remove('has-chat'); pane.innerHTML = '<div class="empty">Selecciona una conversación</div>'; loadContacts(); };
  const setPaused = async (paused) => {
    try {
      await post(`/api/contactos/${encodeURIComponent(phone)}/pausar`, { paused });
      if (!paused && confirm('¿Quieres que la IA responda ahora el último mensaje del cliente?')) {
        await post(`/api/contactos/${encodeURIComponent(phone)}/responder`);
      }
      toast(paused ? 'Tomaste el chat: la IA no responderá aquí' : 'La IA vuelve a atender este chat 🤖');
      showChat(phone);
    } catch (err) { toast(err.message, true); }
  };
  const t = $('#chatTake'); if (t) t.onclick = () => setPaused(true);
  ['#chatResume', '#chatResume2'].forEach((id) => { const b = $(id); if (b) b.onclick = () => setPaused(false); });
  $('#chatAudit').onclick = () => showAudit(phone);
  const ta = $('#chatText');
  ta.addEventListener('keydown', (e) => { if (e.key === 'Enter' && !e.shiftKey) { e.preventDefault(); $('#chatForm').requestSubmit(); } });
  $('#chatForm').onsubmit = async (e) => {
    e.preventDefault();
    const text = ta.value.trim();
    if (!text) return;
    ta.value = '';
    try { await post(`/api/contactos/${encodeURIComponent(phone)}/enviar`, { text }); if (!c.ai_paused) showChat(phone); else loadChatMessages(); }
    catch (err) { toast(err.message, true); ta.value = text; }
  };
  S.chatLastId = 0;
  $('#chatBody').innerHTML = '';
  await loadChatMessages();
  post(`/api/contactos/${encodeURIComponent(phone)}/leido`).then(() => loadContacts()).catch(() => {});
}

async function loadChatMessages() {
  const phone = S.chatPhone;
  const body = $('#chatBody');
  if (!phone || !body) return;
  const msgs = await api(`/api/contactos/${encodeURIComponent(phone)}/mensajes?after=${S.chatLastId}`);
  if (phone !== S.chatPhone) return;
  const nearBottom = body.scrollHeight - body.scrollTop - body.clientHeight < 120;
  $('.typing', body)?.remove();
  for (const m of msgs) { body.insertAdjacentHTML('beforeend', msgHTML(m, false)); S.chatLastId = Math.max(S.chatLastId, m.id); }
  if (S.typing[phone]) body.insertAdjacentHTML('beforeend', '<div class="typing">🤖 la IA está escribiendo…</div>');
  if (nearBottom || msgs.length) body.scrollTop = body.scrollHeight;
}

async function showAudit(phone) {
  const rows = await api(`/api/contactos/${encodeURIComponent(phone)}/auditoria`);
  openModal(`<h3>🧠 Lo que hizo la IA en este chat</h3>
    <p class="small muted">Cada herramienta que usó la IA, con los datos que envió y lo que le respondió el sistema. Útil para saber <i>por qué</i> decidió algo.</p>
    <div class="audit">${auditRows(rows) || '<div>Sin registros todavía.</div>'}</div>
    <div class="actions"><button class="btn" data-close>Cerrar</button></div>`);
}

function auditRows(rows) {
  return rows.slice().reverse().map((a) => {
    let detail = a.detail || '';
    if (detail.length > 600) detail = detail.slice(0, 600) + '…';
    return `<div><b>${esc(a.action.replace('herramienta:', '🔧 '))}</b> <span>(${esc(a.actor)}, ${fmtTime(a.created_at)})</span><br>${esc(detail)}</div>`;
  }).join('');
}

// ---------- SIMULADOR ----------
const SIM_IDEAS = [
  'Hola, vi el anuncio de las gomitas para dormir 😴',
  '¿Cuánto valen?',
  'Llevo meses durmiendo mal, me despierto a las 3am',
  '¿Eso tiene melatonina? ¿crea dependencia?',
  'Está muy caro',
  '¿Y si no me funciona?',
  'Tomo sertralina, ¿las puedo tomar?',
  '¿Eres un bot?',
  'Quiero hablar con una persona',
  'Me llevo las de 2 frascos',
  'Ana María Restrepo, cc 1036123456, cel 3001234567, Medellín Antioquia, barrio Laureles, Cra 76 # 33-20 apto 301',
  'Soy de Leticia, Amazonas, ¿me llega?',
  'Vivo en Vigía del Fuerte, Antioquia',
  'Sí, todo correcto, confirmo',
];

function newSimPhone() {
  const id = Math.random().toString(36).slice(2, 7);
  S.simPhone = 'sim-' + id;
  localStorage.setItem('ds_sim', S.simPhone);
  S.simLastId = 0;
}

async function renderSimulador() {
  if (!S.simPhone) newSimPhone();
  $('#simTitle').textContent = 'Cliente de prueba · ' + S.simPhone.slice(4);
  $('#simIdeas').innerHTML = SIM_IDEAS.map((t) => `<span class="chip">${esc(t)}</span>`).join('');
  $$('#simIdeas .chip').forEach((ch) => ch.onclick = () => { $('#simText').value = ch.textContent; $('#simText').focus(); });
  S.simLastId = 0;
  $('#simBody').innerHTML = '<div class="empty small">Escribe como si fueras un cliente que llega desde el anuncio. La IA responde igual que en WhatsApp (los mensajes del cliente aparecen en verde, a la derecha).</div>';
  await loadSimMessages();
  await loadSimSide();
}

async function loadSimMessages() {
  const body = $('#simBody');
  if (!S.simPhone || !body) return;
  let msgs = [];
  try { msgs = await api(`/api/contactos/${encodeURIComponent(S.simPhone)}/mensajes?after=${S.simLastId}`); } catch (_) { return; }
  if (msgs.length && S.simLastId === 0) body.innerHTML = '';
  $('.typing', body)?.remove();
  for (const m of msgs) { body.insertAdjacentHTML('beforeend', msgHTML(m, true)); S.simLastId = Math.max(S.simLastId, m.id); }
  if (S.typing[S.simPhone]) body.insertAdjacentHTML('beforeend', '<div class="typing">escribiendo…</div>');
  $('#simStatus').textContent = S.typing[S.simPhone] ? 'escribiendo…' : 'en línea';
  body.scrollTop = body.scrollHeight;
}

async function loadSimSide() {
  if (!S.simPhone) return;
  try {
    const [data, audit] = await Promise.all([
      api('/api/contactos/' + encodeURIComponent(S.simPhone)),
      api(`/api/contactos/${encodeURIComponent(S.simPhone)}/auditoria`),
    ]);
    const o = data.pedidos[0];
    $('#simOrder').innerHTML = o ? `<div class="row gap wrap">${statusTag(o.status)} ${payTag(o.payment_policy)} ${riskTag(o.risk)}</div>${orderKV(o)}${o.total ? moneyBox(o) : ''}
      <div class="actions">${o.status === 'por_aprobar' ? '<button class="btn ok small" id="simApprove">✅ Aprobar (como dueño)</button>' : ''}
      ${o.status === 'esperando_pago' ? '<button class="btn ok small" id="simPaid">💰 Marcar pago recibido</button>' : ''}</div>` : '<span class="muted">Aún no hay pedido.</span>';
    const ap = $('#simApprove'); if (ap) ap.onclick = async () => { try { await post(`/api/pedidos/${o.id}/aprobar`); toast('Aprobado'); } catch (e) { toast(e.message, true); } };
    const pd = $('#simPaid'); if (pd) pd.onclick = async () => { try { await post(`/api/pedidos/${o.id}/pago-confirmado`); toast('Pago confirmado'); } catch (e) { toast(e.message, true); } };
    $('#simAudit').innerHTML = auditRows(audit.filter((a) => a.action !== 'tokens')) || '<div>La IA aún no ha usado herramientas.</div>';
  } catch (_) {
    $('#simOrder').innerHTML = '<span class="muted">Aún no hay pedido.</span>';
    $('#simAudit').innerHTML = '';
  }
}

async function simSend(text, kind = 'text') {
  try {
    await post('/api/simulador/mensaje', { phone: S.simPhone, name: '', text, kind });
    await loadSimMessages();
  } catch (err) { toast(err.message, true); }
}

$('#simForm').addEventListener('submit', (e) => {
  e.preventDefault();
  const t = $('#simText').value.trim();
  if (!t) return;
  $('#simText').value = '';
  simSend(t);
});
$('#simPhoto').addEventListener('click', () => simSend('[foto del comprobante de pago]', 'image'));
$('#simNew').addEventListener('click', () => { newSimPhone(); renderSimulador(); });
$('#simReset').addEventListener('click', async () => {
  if (!confirm('¿Borrar este chat de prueba y su pedido?')) return;
  try { await del('/api/simulador/' + encodeURIComponent(S.simPhone)); } catch (_) { /* may not exist */ }
  newSimPhone();
  renderSimulador();
});

// ---------- PRODUCTO Y FINANZAS ----------
async function renderProducto() {
  const data = S.product = await api('/api/producto');
  const p = data.producto; const f = data.finanzas;
  const offers = data.ofertas.map((o) => `
    <div class="card offer ${o.destacada ? 'hl' : ''}">
      <div class="row"><b>${esc(o.etiqueta)}</b><span class="spacer"></span>${o.destacada ? '<span class="tag brand">recomendada</span>' : ''}</div>
      <div style="font-size:22px;font-weight:700;margin:6px 0">${cop(o.precio)} <span class="small muted">${usd(o.precio_usd)}</span></div>
      <div class="small muted">${cop(o.precio_por_noche)} por noche</div>
      <dl class="kv">
        <dt>Ganancia si entrega</dt><dd>${cop(o.contraentrega.ganancia_si_entrega)} (${o.contraentrega.margen_pct}%)</dd>
        <dt>Pérdida si devuelve</dt><dd>${cop(o.contraentrega.perdida_si_devuelve)}</dd>
        <dt>Esperada COD</dt><dd><b>${cop(o.contraentrega.ganancia_esperada)}</b></dd>
        <dt>Esperada anticipo</dt><dd>${cop(o.anticipo_parcial.ganancia_esperada)}</dd>
        <dt>CPA equilibrio</dt><dd>${cop(o.contraentrega.cpa_equilibrio)}</dd>
        <dt>CPA objetivo</dt><dd><b>${cop(o.cpa_objetivo)}</b> por pedido</dd>
        <dt>Costo por chat</dt><dd>${cop(o.costo_por_chat_objetivo)} máx.</dd>
      </dl>
      ${o.contraentrega.advertencia ? `<div class="small" style="color:#fcd34d">⚠️ ${esc(o.contraentrega.advertencia)}</div>` : ''}
    </div>`).join('');
  $('#productInfo').innerHTML = `
    ${p.costo_confirmado ? '' : '<div class="warnbox">⚠️ El costo de proveedor y el flete son <b>estimados</b>. Confírmalos en Dropi y edita <code>motor/knowledge/productos/lullabites.yaml</code> (<code>costo_confirmado: true</code>).</div>'}
    <div class="card">
      <h3>🌙 ${esc(p.nombre)} <span class="small muted">${esc(p.sku)}</span></h3>
      <p>${esc(p.descripcion_corta)}</p>
      <p class="small muted">${esc(p.presentacion)} · Costo proveedor ${cop(p.costo_proveedor)} · Flete ${cop(p.flete_estimado)}</p>
      <p class="small">Supuestos: devolución base ${pct(f.tasa_devolucion)} · margen de seguridad ${pct(f.margen_seguridad)} · confirmación ${pct(f.tasa_confirmacion)} · TRM ${cop(f.trm_cop_usd)} (edítalos en <code>negocio.yaml</code>).</p>
      <p class="small muted">Fórmula: Ganancia esperada = (1−d)·(Precio − Costo − Flete) − d·(Flete ida + vuelta − Anticipo). El CPA objetivo es lo máximo que puedes pagar en Meta por cada pedido despachado, conservando el margen de seguridad.</p>
    </div>
    <div class="offers">${offers}</div>`;
}

$('#zoneForm').addEventListener('submit', async (e) => {
  e.preventDefault();
  const body = Object.fromEntries(new FormData(e.target).entries());
  try {
    const d = await post('/api/zonas/consultar', body);
    $('#zoneResult').innerHTML = `<div style="margin-top:12px">
      <div class="row gap wrap">${riskTag(d.riesgo)} ${payTag(d.politica_pago)} ${d.zona_reconocida ? '' : '<span class="tag warn">zona no reconocida</span>'}</div>
      <dl class="kv"><dt>Motivo</dt><dd>${esc(d.motivo)}</dd>
      <dt>Anticipo</dt><dd>${cop(d.anticipo)} (sobre 1 frasco)</dd><dt>Contraentrega</dt><dd>${cop(d.contraentrega)}</dd>
      <dt>Transportadoras</dt><dd>${esc((d.transportadoras_sugeridas || []).join(', '))}</dd>
      <dt>Le dirá al cliente</dt><dd class="small">${esc(d.explicacion_cliente)}</dd></dl>
      <p class="small muted">Para cambiar estas reglas edita <code>motor/knowledge/zonas.yaml</code> y reinicia el Motor.</p></div>`;
  } catch (err) { toast(err.message, true); }
});

// ---------- AJUSTES ----------
async function renderAjustes() {
  const st = S.status = await api('/api/estado');
  const check = (ok, title, desc) => `<div class="check"><div class="ico">${ok ? '✅' : '⚠️'}</div><div><b>${title}</b><p>${desc}</p></div></div>`;
  const a = st.autonomia;
  $('#settings').innerHTML = `
    <div class="card">
      <h3>Conexiones</h3>
      ${check(st.ia.configurada, 'Inteligencia artificial', st.ia.configurada ? `Modelo <code>${esc(st.ia.modelo)}</code> en <code>${esc(st.ia.endpoint)}</code>.` : 'Agrega tu clave de DeepSeek en <code>LLM_API_KEY</code> (archivo <code>motor/.env</code>).')}
      ${check(st.whatsapp.configurado, 'WhatsApp Cloud API (oficial de Meta)', st.whatsapp.configurado
        ? `Conectado. URL del webhook: <code>${esc(st.whatsapp.webhook_url || 'configura PUBLIC_URL')}</code>. ${st.whatsapp.firma_verificada ? 'Firma de Meta verificada.' : '⚠️ Agrega WHATSAPP_APP_SECRET para verificar la firma de Meta.'}`
        : 'Configura <code>WHATSAPP_TOKEN</code>, <code>WHATSAPP_PHONE_NUMBER_ID</code> y <code>WHATSAPP_VERIFY_TOKEN</code>. Guía paso a paso en <code>docs/CONFIGURACION.md</code>.')}
      ${check(st.pagos.listo, 'Pagos anticipados', st.pagos.metodo === 'wompi'
        ? (st.pagos.wompi ? 'Wompi (Bancolombia): links de pago con confirmación automática.' : 'Método wompi elegido pero faltan las llaves de Wompi.')
        : `Manual con ${st.pagos.llave_bre_b ? 'llave Bre-B' : ''}${st.pagos.llave_bre_b && st.pagos.cuenta ? ' y ' : ''}${st.pagos.cuenta ? 'cuenta Bancolombia' : ''}${!st.pagos.llave_bre_b && !st.pagos.cuenta ? 'datos pendientes: agrega tu llave Bre-B o cuenta en <code>negocio.yaml</code>' : ''}. El cliente envía el comprobante y tú lo confirmas aquí.`)}
      ${check(true, 'Dropi', st.dropi.modo === 'api' ? 'API de integraciones.' : 'Modo manual: aprueba el pedido, usa "Copiar datos para Dropi", créalo en Dropi y registra la guía. La conexión por API se activa cuando Dropi te entregue el token de integraciones.')}
      ${check(st.aviso_dueno, 'Avisos a tu WhatsApp', st.aviso_dueno ? 'Recibirás avisos de pedidos nuevos y escalamientos (solo funciona si escribiste al número del negocio en las últimas 24 h).' : 'Opcional: pon tu número personal en <code>notificar.whatsapp_dueno</code> de <code>negocio.yaml</code>.')}
      ${check(st.requiere_token, 'Panel protegido', st.requiere_token ? 'El panel pide token.' : '⚠️ Define <code>ADMIN_TOKEN</code> antes de exponer el Motor a internet.')}
      ${check(st.producto.costo_confirmado, 'Costo del producto', st.producto.costo_confirmado ? 'Confirmado.' : 'Pendiente de confirmar en Dropi.')}
    </div>
    <div class="card" style="margin-top:14px">
      <h3>Autonomía de la IA</h3>
      <p class="small muted">Se edita en <code>negocio.yaml</code> → <code>autonomia</code>.</p>
      <div class="check"><div class="ico">💬</div><div><b>Responder y vender por WhatsApp</b><p>Automático. Puedes tomar cualquier chat con "Yo tomo".</p></div></div>
      <div class="check"><div class="ico">${a.auto_aprobar_pedidos ? '⚡' : '✋'}</div><div><b>Aprobar pedidos para despacho</b><p>${a.auto_aprobar_pedidos
        ? `Automático con límites: máx. ${cop(a.auto_max_valor)}${a.auto_solo_contraentrega ? ', solo contraentrega' : ''}${a.auto_solo_riesgo_normal ? ', solo zonas de riesgo normal' : ''}. Lo demás espera tu botón.`
        : 'Tú apruebas cada pedido con un botón (recomendado al empezar).'}</p></div></div>
      <div class="check"><div class="ico">✋</div><div><b>Confirmar pagos anticipados</b><p>${st.pagos.metodo === 'wompi' ? 'Automático cuando Wompi confirma el pago (verificado con firma).' : 'Tú confirmas tras revisar tu cuenta. La IA nunca da un pago por recibido con solo una captura.'}</p></div></div>
    </div>
    <div class="card" style="margin-top:14px">
      <h3>Sesión</h3>
      <button class="btn danger" id="logout">Cerrar sesión en este dispositivo</button>
      <p class="small muted">Motor v${esc(st.version)} · iniciado ${fmtTime(st.iniciado)}</p>
    </div>`;
  $('#logout').onclick = () => { localStorage.removeItem('ds_token'); S.token = ''; location.reload(); };
}

Object.assign(window, { showChat, loadChatMessages, renderSimulador, loadSimMessages, loadSimSide, renderProducto, renderAjustes });
