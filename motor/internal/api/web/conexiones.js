// Dropshipper Motor · conexiones: IA (cualquier API compatible con OpenAI), WhatsApp, cobros y seguridad.
'use strict';

const CN = { cfg: null, est: null };

const randHex = (n) => [...crypto.getRandomValues(new Uint8Array(n))].map((b) => b.toString(16).padStart(2, '0')).join('');

const copyRow = (text) => `<div class="copyrow"><code>${esc(text || '(se mostrará al definir la dirección pública)')}</code>${text ? `<button class="btn small" type="button" data-copy-text="${esc(text)}">${ic('copy', 13)} Copiar</button>` : ''}</div>`;

const secretHint = (s) => (s && s.configurada ? `<span class="secret-note">${ic('check', 12)} Guardada (${esc(s.mascara)}). Déjalo vacío para conservarla.</span>` : '');
const secretField = (name, label, s, o = {}) => `<label class="field${o.full ? ' full' : ''}"><span>${label}</span>
  <input class="secret-input" name="${name}" type="password" autocomplete="new-password" readonly data-lpignore="true" data-1p-ignore placeholder="${s && s.configurada ? 'Sin cambios' : (o.ph || '')}">
  ${secretHint(s)}${o.help ? `<small>${o.help}</small>` : ''}</label>`;

const testBox = () => '<div class="testres"></div>';
function showTest(box, r) {
  box.innerHTML = r.ok ? notice('ok', esc(r.mensaje || 'Conexión correcta.')) : notice('bad', esc(r.error || 'No se pudo conectar.'));
  hydrateIcons(box);
}

// Campos secretos: de solo lectura hasta que la persona entra en ellos, para que el
// administrador de contraseñas del navegador no pegue allí el token del panel.
document.addEventListener('focusin', (e) => { if (e.target.classList && e.target.classList.contains('secret-input')) e.target.removeAttribute('readonly'); });

document.addEventListener('click', async (e) => {
  const b = e.target.closest('[data-copy-text]');
  if (!b) return;
  try { await navigator.clipboard.writeText(b.dataset.copyText); toast('Copiado.'); } catch (_) { toast('No se pudo copiar; selecciónalo manualmente.', true); }
});

async function renderConexiones() {
  const [cfg, est] = await Promise.all([api('/api/ajustes'), api('/api/estado')]);
  CN.cfg = cfg; CN.est = est; S.status = est;
  const root = $('#conexiones');
  root.innerHTML = `
    <div class="view-head"><div><h2>Conexiones</h2><div class="sub">Todo lo que el Motor necesita para vender. Cada bloque tiene su botón de prueba.</div></div></div>
    <div id="cnIA"></div><div id="cnDropi" style="margin-top:16px"></div><div id="cnWA" style="margin-top:16px"></div><div id="cnPay" style="margin-top:16px"></div><div id="cnSec" style="margin-top:16px"></div>`;
  drawIA(); drawDropi(); drawWhatsApp(); drawPagos(); drawSeguridad();
  refreshBadges();
}

// =============== IA ===============
function drawIA() {
  const ia = CN.cfg.ia;
  const rows = ia.proveedores.map((p) => `<div class="prov ${p.activo ? 'active' : ''}" data-pid="${esc(p.id)}">
      <div class="mk">${ic('cpu', 18)}</div>
      <div class="info"><b>${esc(p.nombre)} ${p.activo ? '<span class="tag brand">En uso</span>' : ''} ${p.lista ? '' : '<span class="tag warn">Falta la clave</span>'}</b>
        <span>${esc(p.modelo)} · ${esc(p.base_url)}${p.sin_clave ? '' : ' · clave ' + (p.clave.configurada ? esc(p.clave.mascara) : 'sin definir')}</span></div>
      ${p.activo ? '' : '<button class="btn small ok" data-act="usar">Usar</button>'}
      <button class="btn small" data-act="editar">Editar</button>
      <button class="btn small icon ghost" data-act="quitar" title="Quitar">${ic('trash', 14)}</button></div>`).join('');
  $('#cnIA').innerHTML = `<div class="card">
    <div class="card-head"><span class="ico">${ic('cpu', 18)}</span><div><h3>Inteligencia artificial</h3>
      <div class="desc">Sirve cualquier proveedor compatible con la API de OpenAI: DeepSeek, OpenAI, OpenRouter, Groq, Gemini, Ollama local, Arxi u otro. Solo necesitas la dirección, el modelo y la clave.</div></div>
      <span class="spacer"></span><button class="btn primary" id="iaAdd">${ic('plus', 14)} Agregar proveedor</button></div>
    ${rows || '<div class="empty">Aún no hay ningún proveedor. Agrega uno para que el vendedor pueda responder.</div>'}</div>`;
  $('#iaAdd').onclick = () => openProvider(null);
  $$('#cnIA .prov').forEach((row) => {
    const p = ia.proveedores.find((x) => x.id === row.dataset.pid);
    row.onclick = async (e) => {
      const a = e.target.closest('[data-act]');
      if (!a) return;
      try {
        if (a.dataset.act === 'editar') return openProvider(p);
        if (a.dataset.act === 'usar') { await post(`/api/ia/proveedores/${encodeURIComponent(p.id)}/activar`); toast(`Ahora la IA usa ${p.nombre}.`); }
        if (a.dataset.act === 'quitar') {
          if (!confirm(`¿Quitar el proveedor "${p.nombre}"?`)) return;
          await del(`/api/ia/proveedores/${encodeURIComponent(p.id)}`); toast('Proveedor quitado.');
        }
        renderConexiones();
      } catch (err) { toast(err.message, true); }
    };
  });
}

function openProvider(p) {
  const presets = CN.cfg.ia.presets;
  const st = { preset: p ? (p.preset || 'custom') : (presets[0] ? presets[0].id : 'custom') };
  const cur = () => presets.find((x) => x.id === st.preset) || {};
  openModal(`<h3>${p ? 'Editar proveedor' : 'Agregar proveedor de IA'}</h3>
    ${p ? '' : `<div class="small muted" style="margin-bottom:8px">Elige un punto de partida. Siempre puedes cambiar la dirección y el modelo.</div>
    <div class="preset-grid" id="presetGrid">${presets.map((x) => `<button type="button" class="preset" data-preset="${esc(x.id)}"><b>${esc(x.nombre)}</b><span>${esc(x.modelo || 'Personalizado')}</span></button>`).join('')}</div>`}
    <form id="provForm" autocomplete="off">
      <div class="fgrid">
        ${fld('nombre', 'Nombre', p ? p.nombre : '', { ph: 'Ej.: DeepSeek principal' })}
        <label class="field"><span>Modelo</span><div class="inline"><input name="modelo" list="modelList" value="${esc(p ? p.modelo : '')}" placeholder="deepseek-chat"><button class="btn" type="button" id="loadModels">Cargar modelos</button></div>
          <datalist id="modelList"></datalist><small id="modelsHint">Escribe el nombre o cárgalos desde el proveedor.</small></label>
        ${fld('base_url', 'Dirección de la API (URL base)', p ? p.base_url : '', { full: true, ph: 'https://api.deepseek.com', help: 'Debe ser compatible con OpenAI (termina en /v1 o similar). No incluyas /chat/completions.' })}
        <label class="field full" id="keyField"><span>Clave de API</span><input name="api_key" type="password" autocomplete="new-password" placeholder="${p && p.clave.configurada ? 'Sin cambios' : 'sk-...'}">
          ${p ? secretHint(p.clave) : ''}<small id="keyHelp"></small></label>
        <label class="field"><span>Temperatura (opcional)</span><input name="temperatura" type="number" step="0.1" min="0" max="2" value="${p && p.temperatura != null ? p.temperatura : ''}" placeholder="Automática"><small>Más baja es más predecible.</small></label>
      </div>
      <div class="testres" id="provTest"></div>
      <div class="form-actions">
        <button class="btn" type="button" id="provTestBtn">${ic('refresh', 14)} Probar conexión</button><span class="spacer"></span>
        <button class="btn" type="button" data-close>Cancelar</button>
        <button class="btn primary" type="submit" data-activate="0">Guardar</button>
        ${p && p.activo ? '' : '<button class="btn ok" type="submit" data-activate="1">Guardar y usar</button>'}</div>
    </form>`, true);

  const f = $('#provForm');
  const applyPreset = (id, keepName) => {
    st.preset = id;
    $$('#presetGrid .preset').forEach((b) => b.classList.toggle('sel', b.dataset.preset === id));
    const c = cur();
    if (!p) {
      if (!keepName || !f.nombre.value) f.nombre.value = c.id === 'custom' ? '' : (c.nombre || '');
      f.base_url.value = c.base_url || '';
      f.modelo.value = c.modelo || '';
    }
    $('#keyField').classList.toggle('hidden', !!c.sin_clave);
    $('#keyHelp').innerHTML = c.sin_clave ? '' : esc(c.ayuda_clave || '');
  };
  if (p) { $('#keyField').classList.toggle('hidden', !!p.sin_clave); }
  else { applyPreset(st.preset, true); $('#presetGrid').onclick = (e) => { const b = e.target.closest('.preset'); if (b) applyPreset(b.dataset.preset, false); }; }

  const body = () => {
    const d = formData(f);
    return { id: p ? p.id : '', nombre: d.nombre.trim(), preset: st.preset, base_url: d.base_url.trim(), api_key: d.api_key || '', modelo: d.modelo.trim(),
      temperatura: d.temperatura === '' ? null : Number(d.temperatura) };
  };

  $('#loadModels').onclick = (e) => busy(e.currentTarget, async () => {
    const r = await post('/api/ia/modelos', body());
    if (!r.ok) { $('#modelsHint').innerHTML = `<span style="color:var(--bad)">${esc(r.error)}</span>`; return; }
    if (r.url_sugerida) f.base_url.value = r.url_sugerida;
    $('#modelList').innerHTML = r.modelos.map((m) => `<option value="${esc(m)}">`).join('');
    $('#modelsHint').textContent = `${r.modelos.length} modelos disponibles. Haz clic en el campo para elegir.`;
    if (!f.modelo.value && r.modelos[0]) f.modelo.value = r.modelos[0];
  });
  $('#provTestBtn').onclick = (e) => busy(e.currentTarget, async () => {
    $('#provTest').innerHTML = '';
    const r = await post('/api/ia/probar', body());
    // The server found the address only works under /v1: fix the field so
    // saving keeps the right one.
    if (r.ok && r.url_sugerida) f.base_url.value = r.url_sugerida;
    showTest($('#provTest'), r);
  });
  f.onsubmit = async (e) => {
    e.preventDefault();
    const btn = e.submitter; const activate = btn && btn.dataset.activate === '1';
    await busy(btn, async () => {
      try {
        const b = body(); b.activar = activate;
        if (p) await put(`/api/ia/proveedores/${encodeURIComponent(p.id)}`, b); else await post('/api/ia/proveedores', b);
        if (p && activate) await post(`/api/ia/proveedores/${encodeURIComponent(p.id)}/activar`);
        closeModal(); toast(activate ? 'Proveedor guardado y en uso.' : 'Proveedor guardado.'); renderConexiones();
      } catch (err) { toast(err.message, true); }
    });
  };
}


// =============== Dropi ===============
function drawDropi() {
  const d = CN.cfg.dropi;
  const modoApi = (CN.cfg.negocio.dropi || {}).modo === 'api';
  $('#cnDropi').innerHTML = `<div class="card">
    <div class="card-head"><span class="ico">${ic('truck', 18)}</span><div><h3>Dropi ${d.configurado ? '<span class="tag ok">Conectado</span>' : '<span class="tag">Sin conectar</span>'}</h3>
      <div class="desc">Con Dropi el Motor cotiza el flete real de cada transportadora, elige la más rentable y trae el costo de tus productos. Sin conectarlo funciona igual, con los valores que escribas a mano.</div></div></div>
    ${d.configurado ? '' : notice('info', '<b>Cómo conectarlo:</b> en Dropi entra a <b>Configuración → Integraciones</b>, crea una integración y copia el token. Pégalo aquí, indica desde qué ciudad despachas y pulsa <b>Probar conexión</b>.')}
    <form id="dpForm" autocomplete="off">
      <div class="fgrid">
        <label class="field full"><span>Token de integración de Dropi</span>
          <input class="secret-input" name="token" type="password" autocomplete="new-password" readonly data-lpignore="true" data-1p-ignore placeholder="${d.token.configurada ? 'Sin cambios' : 'Pega aquí el token completo'}">
          ${secretHint(d.token)}<small>Se guarda en la base de datos del Motor y nunca se vuelve a mostrar completo.</small></label>
        ${fld('origen_departamento', 'Departamento de origen', d.origen_departamento, { ph: 'Antioquia', help: 'Desde donde sale tu mercancía (bodega del proveedor o la tuya).' })}
        ${fld('origen_ciudad', 'Ciudad de origen', d.origen_ciudad, { ph: 'Medellín', help: 'Se usa para cotizar el flete hacia cada destino.' })}
        <label class="switch full"><input type="checkbox" name="sandbox" ${d.sandbox ? 'checked' : ''}><div><b>Entorno de pruebas de Dropi</b><span>Úsalo solo para probar el token. En pruebas no hay transportadoras, así que no se cotizan fletes; para eso desmárcalo.</span></div></label>
      </div>
      <div class="small muted" style="margin:2px 0 10px">Servidor: <code>${esc(d.base_url)}</code></div>
      ${d.configurado ? sw('dp_modo_api', 'Crear los pedidos aprobados directamente en Dropi', 'Si lo apagas, el Motor te entrega los datos listos para copiar y tú registras la guía a mano.', modoApi) : ''}
      <div class="testres" id="dpTest"></div>
      <div class="form-actions"><button class="btn" type="button" id="dpTestBtn">${ic('refresh', 14)} Probar conexión</button><span class="spacer"></span>
        ${d.configurado ? '<button class="btn danger" type="button" id="dpDel">Desconectar</button>' : ''}<button class="btn primary" type="submit">Guardar Dropi</button></div>
    </form>
    ${d.configurado ? `<div class="subcard"><h4>Simular un pedido</h4><div class="small muted" style="margin-bottom:8px">Prueba qué haría el Motor con un pedido real: cotiza, elige transportadora y revisa costos. <b>No crea nada en Dropi.</b> </div><a class="btn small" href="#envios">${ic('truck', 13)} Ir al simulador</a></div>` : ''}
  </div>`;
  const f = $('#dpForm');
  const body = () => { const x = formData(f); x.sandbox = f.sandbox.checked; return x; };
  $('#dpTestBtn').onclick = (e) => busy(e.currentTarget, async () => showTest($('#dpTest'), await post('/api/dropi/probar', body())));
  f.onsubmit = async (e) => {
    e.preventDefault();
    await busy(e.submitter, async () => {
      try {
        await put('/api/ajustes/dropi', body());
        if (f.dp_modo_api) await put('/api/ajustes/negocio', { dropi: { modo: f.dp_modo_api.checked ? 'api' : 'manual' } });
        toast('Dropi guardado.'); renderConexiones();
      } catch (err) { toast(err.message, true); }
    });
  };
  const del_ = $('#dpDel');
  if (del_) del_.onclick = async () => {
    if (!confirm('¿Desconectar Dropi? Se borra el token guardado y los fletes volverán a ser los estimados.')) return;
    try { await del('/api/ajustes/dropi'); toast('Dropi desconectado.'); renderConexiones(); } catch (err) { toast(err.message, true); }
  };
}

// =============== WhatsApp ===============
function drawWhatsApp() {
  const w = CN.cfg.whatsapp;
  $('#cnWA').innerHTML = `<div class="card">
    <div class="card-head"><span class="ico">${ic('message', 18)}</span><div><h3>WhatsApp Business ${w.configurado ? '<span class="tag ok">Conectado</span>' : '<span class="tag">Sin conectar</span>'}</h3>
      <div class="desc">API oficial de Meta (WhatsApp Cloud). Sin esto solo funciona el Simulador. Los pasos para obtener estos datos están en la guía de configuración.</div></div></div>
    <form id="waForm" autocomplete="off">
      <div class="fgrid">
        ${secretField('token', 'Token de acceso permanente', w.token, { ph: 'EAAG...', help: 'Se crea en Meta for Developers, con un usuario del sistema.' })}
        ${fld('phone_number_id', 'ID del número de teléfono', w.phone_number_id, { help: 'No es el número: es el identificador que muestra Meta.' })}
        <label class="field"><span>Token de verificación del webhook</span><div class="inline"><input class="secret-input" name="verify_token" type="password" autocomplete="new-password" readonly data-lpignore="true" data-1p-ignore placeholder="${w.verify_token.configurada ? 'Sin cambios' : 'Invéntalo o genéralo'}"><button class="btn" type="button" id="waGen">Generar</button></div>
          ${secretHint(w.verify_token)}<small>Debe ser el mismo texto que escribas en Meta al registrar el webhook.</small></label>
        ${secretField('app_secret', 'Secreto de la app (recomendado)', w.app_secret, { help: 'Permite verificar que los mensajes entrantes vienen de Meta.' })}
        ${fld('graph_version', 'Versión de la API', w.graph_version, { ph: 'v21.0' })}
      </div>
      <div class="field"><span style="font-size:12px;font-weight:600;color:var(--text-2)">Dirección del webhook (pégala en Meta)</span>${copyRow(w.webhook_url)}
        ${CN.cfg.general.url_publica ? '' : `<small class="muted">Usa tu dirección pública para que Meta pueda llegar. Defínela en Seguridad y acceso.</small>`}</div>
      <div class="testres" id="waTest"></div>
      <div class="form-actions"><button class="btn" type="button" id="waTestBtn">${ic('refresh', 14)} Probar conexión</button><span class="spacer"></span>
        ${w.configurado ? '<button class="btn danger" type="button" id="waDel">Desconectar</button>' : ''}<button class="btn primary" type="submit">Guardar</button></div>
    </form></div>`;
  const f = $('#waForm');
  $('#waGen').onclick = () => { f.verify_token.type = 'text'; f.verify_token.value = randHex(12); toast('Token generado. Guarda y cópialo en Meta.'); };
  $('#waTestBtn').onclick = (e) => busy(e.currentTarget, async () => showTest($('#waTest'), await post('/api/whatsapp/probar', formData(f))));
  f.onsubmit = async (e) => {
    e.preventDefault();
    await busy(e.submitter, async () => {
      try { await put('/api/ajustes/whatsapp', formData(f)); toast('WhatsApp guardado.'); renderConexiones(); } catch (err) { toast(err.message, true); }
    });
  };
  const d = $('#waDel');
  if (d) d.onclick = async () => {
    if (!confirm('¿Desconectar WhatsApp? Se borran el token y los secretos guardados.')) return;
    try { await del('/api/ajustes/whatsapp'); toast('WhatsApp desconectado.'); renderConexiones(); } catch (err) { toast(err.message, true); }
  };
}

// =============== Cobros ===============
function drawPagos() {
  const n = CN.cfg.negocio; const pg = n.pagos; const m = pg.manual || {}; const wp = CN.cfg.wompi;
  const st = { metodo: pg.metodo || 'manual' };
  $('#cnPay').innerHTML = `<div class="card">
    <div class="card-head"><span class="ico">${ic('wallet', 18)}</span><div><h3>Cobro de anticipos</h3>
      <div class="desc">Se usa en zonas de riesgo, donde se pide un pago por adelantado antes de despachar.</div></div></div>
    <div class="seg" id="pgSeg"><button type="button" data-m="manual">Manual (Bre-B o cuenta)</button><button type="button" data-m="wompi">Wompi (automático)</button></div>
    <form id="pgForm" autocomplete="off" style="margin-top:14px">
      <div data-pm="manual"><div class="fgrid">
        ${fld('llave_bre_b', 'Llave Bre-B', m.llave_bre_b, { help: 'Celular, correo o llave alfanumérica.' })}${fld('titular', 'Titular de la cuenta', m.titular)}
        ${fld('banco', 'Banco', m.banco, { ph: 'Bancolombia' })}${fld('tipo_cuenta', 'Tipo de cuenta', m.tipo_cuenta, { ph: 'Ahorros' })}
        ${fld('numero_cuenta', 'Número de cuenta', m.numero_cuenta)}${fld('horas_para_pagar', 'Horas que tiene el cliente para pagar', pg.horas_para_pagar || 24, { type: 'number' })}
        <label class="field full"><span>Instrucciones adicionales para el cliente</span><textarea name="instrucciones" rows="2" style="width:100%">${esc(m.instrucciones)}</textarea></label></div>
        ${notice('info', 'El cliente envía el comprobante y tú confirmas el pago desde Aprobaciones. La IA nunca da un pago por recibido solo con una captura.')}</div>
      <div data-pm="wompi" class="hidden">${notice('info', 'Wompi genera un enlace de pago y confirma el pago automáticamente. Las llaves se configuran en el bloque siguiente.')}
        ${wp.configurado ? notice('ok', 'Wompi está configurado.') : notice('warn', 'Falta configurar las llaves de Wompi (abajo).')}</div>
      <div class="form-actions"><span class="spacer"></span><button class="btn primary" type="submit">Guardar forma de cobro</button></div>
    </form></div>

    <div class="card" style="margin-top:16px">
    <div class="card-head"><span class="ico">${ic('dollar', 18)}</span><div><h3>Wompi ${wp.configurado ? '<span class="tag ok">Configurado</span>' : '<span class="tag">Opcional</span>'}</h3>
      <div class="desc">Enlaces de pago con confirmación automática. Las llaves están en el panel de comercios de Wompi.</div></div></div>
    <form id="wpForm" autocomplete="off"><div class="fgrid">
      ${fld('public_key', 'Llave pública', wp.public_key, { ph: 'pub_test_...' })}
      ${secretField('private_key', 'Llave privada', wp.private_key, { ph: 'prv_test_...' })}
      ${secretField('events_secret', 'Secreto de eventos', wp.events_secret, { help: 'Necesario para confirmar los pagos de forma segura.' })}
      <label class="switch" style="align-self:end"><input type="checkbox" name="sandbox" ${wp.sandbox ? 'checked' : ''}><div><b>Modo de pruebas</b><span>Desmárcalo para recibir pagos reales.</span></div></label></div>
      <div class="field"><span style="font-size:12px;font-weight:600;color:var(--text-2)">URL de eventos (pégala en Wompi)</span>${copyRow(wp.events_url)}</div>
      <div class="testres" id="wpTest"></div>
      <div class="form-actions"><button class="btn" type="button" id="wpTestBtn">${ic('refresh', 14)} Probar conexión</button><span class="spacer"></span>
        ${wp.configurado ? '<button class="btn danger" type="button" id="wpDel">Quitar Wompi</button>' : ''}<button class="btn primary" type="submit">Guardar Wompi</button></div></form></div>`;

  const setMethod = (mt) => {
    st.metodo = mt;
    $$('#pgSeg button').forEach((b) => b.classList.toggle('on', b.dataset.m === mt));
    $$('[data-pm]').forEach((d) => d.classList.toggle('hidden', d.dataset.pm !== mt));
  };
  setMethod(st.metodo);
  $('#pgSeg').onclick = (e) => { const b = e.target.closest('button'); if (b) setMethod(b.dataset.m); };
  $('#pgForm').onsubmit = async (e) => {
    e.preventDefault();
    const d = formData(e.target);
    await busy(e.submitter, async () => {
      try {
        await put('/api/ajustes/negocio', { pagos: { metodo: st.metodo, horas_para_pagar: Number(d.horas_para_pagar) || 24,
          manual: { llave_bre_b: d.llave_bre_b.trim(), banco: d.banco.trim(), tipo_cuenta: d.tipo_cuenta.trim(), numero_cuenta: d.numero_cuenta.trim(), titular: d.titular.trim(), instrucciones: d.instrucciones.trim() } } });
        toast('Forma de cobro guardada.'); renderConexiones();
      } catch (err) { toast(err.message, true); }
    });
  };
  const wbody = () => { const d = formData($('#wpForm')); d.sandbox = $('#wpForm').sandbox.checked; return d; };
  $('#wpTestBtn').onclick = (e) => busy(e.currentTarget, async () => showTest($('#wpTest'), await post('/api/wompi/probar', wbody())));
  $('#wpForm').onsubmit = async (e) => {
    e.preventDefault();
    await busy(e.submitter, async () => {
      try { await put('/api/ajustes/wompi', wbody()); toast('Wompi guardado.'); renderConexiones(); } catch (err) { toast(err.message, true); }
    });
  };
  const d = $('#wpDel');
  if (d) d.onclick = async () => {
    if (!confirm('¿Quitar Wompi? Si era tu método de cobro, volverá a manual.')) return;
    try { await del('/api/ajustes/wompi'); toast('Wompi quitado.'); renderConexiones(); } catch (err) { toast(err.message, true); }
  };
}

// =============== Seguridad y acceso ===============
function drawSeguridad() {
  const g = CN.cfg.general; const est = CN.est;
  $('#cnSec').innerHTML = `<div class="card">
    <div class="card-head"><span class="ico">${ic('shield', 18)}</span><div><h3>Seguridad y acceso</h3>
      <div class="desc">Quién puede abrir este panel y cuál es la dirección pública del Motor.</div></div></div>
    ${g.token.configurada
      ? notice('ok', `El panel está protegido con un token (${esc(g.token.mascara)}).`)
      : notice('warn', 'No hay token: el panel solo abre desde este mismo computador. Crea uno para entrar desde el celular o desde otro equipo, y antes de publicar el Motor en internet.')}
    <form id="urlForm"><label class="field"><span>Dirección pública del Motor</span>
      <div class="inline"><input name="url_publica" value="${esc(g.url_publica)}" placeholder="https://mi-motor.ejemplo.com"><button class="btn primary" type="submit">Guardar</button></div>
      <small>Con ella se arman las direcciones de los webhooks de WhatsApp y Wompi. Debe ser accesible desde internet (HTTPS).</small></label></form>
    <div class="row gap wrap" style="margin-top:6px">
      <button class="btn primary" id="tkGen">${ic('key', 14)} ${g.token.configurada ? 'Generar token nuevo' : 'Generar token de acceso'}</button>
      <button class="btn" id="tkSet">Definir mi propio token</button>
      ${g.token.configurada ? `<button class="btn danger" id="tkDel">Quitar token</button>` : ''}</div>
    <div id="tkResult"></div></div>`;

  $('#urlForm').onsubmit = async (e) => {
    e.preventDefault();
    try { await put('/api/ajustes/general', { url_publica: formData(e.target).url_publica.trim() }); toast('Dirección guardada.'); renderConexiones(); } catch (err) { toast(err.message, true); }
  };
  const applyToken = async (token) => {
    await put('/api/ajustes/general', { token_admin: token });
    S.token = token; localStorage.setItem('ds_token', token); connectEvents();
  };
  $('#tkGen').onclick = async () => {
    if (g.token.configurada && !confirm('El token actual dejará de funcionar en los demás dispositivos. ¿Continuar?')) return;
    const t = randHex(16);
    try {
      await applyToken(t);
      $('#tkResult').innerHTML = `<div style="margin-top:12px">${notice('warn', '<b>Guarda este token ahora.</b> No se vuelve a mostrar completo. Úsalo para entrar desde otros dispositivos.')}${copyRow(t)}</div>`;
      hydrateIcons($('#tkResult'));
      toast('Token creado. Este navegador ya lo usa.');
    } catch (err) { toast(err.message, true); }
  };
  $('#tkSet').onclick = () => {
    openModal(`<h3>Definir token de acceso</h3><form id="tkForm"><label class="field"><span>Token (mínimo 8 caracteres)</span><input name="t" type="password" autocomplete="new-password"></label>
      <div class="form-actions"><button class="btn primary" type="submit">Guardar</button><button class="btn" type="button" data-close>Cancelar</button></div></form>`);
    $('#tkForm').onsubmit = async (e) => {
      e.preventDefault();
      try { await applyToken(formData(e.target).t.trim()); closeModal(); toast('Token guardado.'); renderConexiones(); } catch (err) { toast(err.message, true); }
    };
  };
  const dl = $('#tkDel');
  if (dl) dl.onclick = async () => {
    if (!confirm('Sin token, el panel solo abrirá desde este computador. ¿Quitarlo?')) return;
    try { await put('/api/ajustes/general', { quitar_token: true }); S.token = ''; localStorage.removeItem('ds_token'); toast('Token quitado.'); renderConexiones(); } catch (err) { toast(err.message, true); }
  };
  void est;
}

Object.assign(window, { renderConexiones });
