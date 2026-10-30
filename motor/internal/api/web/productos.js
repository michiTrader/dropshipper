// Dropshipper Motor · productos: lista -> detalle con pestañas (ficha, ofertas, finanzas, argumentos).
'use strict';

const PS = { sku: '', p: null, isNew: false, tab: 'ficha', real: null, timer: null };
const TABS = [['ficha', 'Ficha'], ['ofertas', 'Ofertas y costos'], ['finanzas', 'Finanzas'], ['argumentos', 'Argumentos de venta']];

const blankProduct = () => ({
  sku: '', nombre: '', categoria: '', descripcion_corta: '', presentacion: '', ingredientes: [], modo_uso: '', sensacion: '',
  precauciones: [], ofertas: [{ id: 'x1', etiqueta: '1 unidad', unidades: 1, precio: 0, destacada: false }],
  costo_proveedor: 0, flete_estimado: 0, costo_confirmado: false, dropi_product_id: '',
  envio: { tiempo_ciudades_principales: '', tiempo_otras_zonas: '', costo_para_cliente: '' }, garantia: '',
  dolores: [], beneficios: [], objeciones: [], activo: false,
});

function renderProductos(arg) {
  const root = $('#productos');
  if (!arg) return renderProductList(root);
  if (arg === 'nuevo') return openProduct(root, null);
  return openProduct(root, decodeURIComponent(arg));
}

// ---------- lista ----------
async function renderProductList(root) {
  const list = await api('/api/productos');
  root.innerHTML = `
    <div class="view-head"><div><h2>Productos</h2><div class="sub">Cada producto tiene su propia ficha, ofertas, costos y finanzas. La IA vende los productos activos.</div></div>
      <span class="spacer"></span>
      <button class="btn" id="pZone">${ic('truck', 14)} Consultar zona de envío</button>
      <button class="btn" id="pGuide">${ic('edit', 14)} Guía de ventas</button>
      <a class="btn primary" href="#productos/nuevo">${ic('plus', 14)} Nuevo producto</a></div>
    ${list.length ? `<div class="cards">${list.map(productCard).join('')}</div>` : `<div class="card"><div class="empty">Aún no tienes productos. Crea el primero para que la IA pueda venderlo.</div></div>`}`;
  $$('.pcard', root).forEach((c) => { c.onclick = () => { location.hash = 'productos/' + encodeURIComponent(c.dataset.sku); }; });
  $('#pGuide').onclick = openGuide;
  $('#pZone').onclick = () => openZone(list);
}

function productCard(p) {
  return `<div class="card pcard ${p.activo ? '' : 'off'}" data-sku="${esc(p.sku)}">
    <div class="top"><div><div class="nm">${esc(p.nombre)}</div><div class="sku">${esc(p.sku)}${p.categoria ? ' · ' + esc(p.categoria) : ''}</div></div>
      <div class="row gap wrap" style="justify-content:flex-end">${p.activo ? '<span class="tag ok">Activo</span>' : '<span class="tag">Inactivo</span>'}${p.costo_confirmado ? '' : '<span class="tag warn">Costo sin confirmar</span>'}</div></div>
    <div class="stats">
      <div><b>${p.precio_desde ? cop(p.precio_desde) : '-'}</b><span>Precio desde</span></div>
      <div><b class="${p.ganancia_esperada_mejor < 0 ? 'neg' : ''}">${p.ofertas ? cop(p.ganancia_esperada_mejor) : '-'}</b><span>Mejor ganancia esperada</span></div>
      <div><b>${p.ofertas}</b><span>Ofertas</span></div>
      <div><b>${p.pedidos}</b><span>Pedidos</span></div>
    </div>
    ${p.ofertas && !p.viable ? `<div class="small" style="color:var(--warn);margin-top:10px">${ic('alert', 13)} Ninguna oferta deja ganancia con los supuestos actuales.</div>` : ''}
  </div>`;
}

// ---------- detalle / alta ----------
async function openProduct(root, sku) {
  PS.tab = PS.tab || 'ficha';
  if (sku === null) {
    PS.isNew = true; PS.sku = ''; PS.p = blankProduct(); PS.real = null; PS.tab = 'ficha';
  } else {
    const d = await api('/api/productos/' + encodeURIComponent(sku));
    PS.isNew = false; PS.sku = d.producto.sku; PS.p = d.producto; PS.real = d.finanzas.real; PS.pedidos = d.pedidos; PS.supuestos = d.finanzas.supuestos;
    PS.eco = d.economia;
  }
  drawProduct(root);
  if (!PS.isNew) drawEconomics(PS.eco, PS.supuestos);
  else refreshEconomics();
}

const lst = (key, items, ph) => `<div class="listedit" data-list="${key}">${(items.length ? items : ['']).map((v) => listRow(v, ph)).join('')}
  <div><button class="btn small ghost" type="button" data-addrow="${key}">${ic('plus', 13)} Agregar</button></div></div>`;
const listRow = (v, ph) => `<div class="le"><input value="${esc(v)}" placeholder="${esc(ph || '')}"><button class="btn small icon ghost" type="button" data-delrow title="Quitar">${ic('x', 13)}</button></div>`;

const offerRow = (o) => `<div class="offer-row" data-offer style="grid-template-columns:70px 1fr 80px 120px 74px 32px">
  <input data-o="id" value="${esc(o.id)}" placeholder="x1"><input data-o="etiqueta" value="${esc(o.etiqueta)}" placeholder="2 unidades">
  <input data-o="unidades" type="number" min="1" value="${o.unidades || 1}"><input data-o="precio" type="number" min="0" step="100" value="${o.precio || 0}">
  <label class="small row gap" style="justify-content:center"><input data-o="destacada" type="checkbox" ${o.destacada ? 'checked' : ''}> Principal</label>
  <button class="btn small icon ghost" type="button" data-deloffer title="Quitar">${ic('x', 13)}</button></div>`;

const objRow = (o) => `<div class="card" data-obj style="padding:12px;margin-bottom:8px;background:var(--panel2)">
  <label class="field" style="margin-bottom:8px"><span>Objeción del cliente</span><input data-ob="objecion" value="${esc(o.objecion)}" placeholder="Está muy caro"></label>
  <label class="field" style="margin-bottom:6px"><span>Cómo debe responder la IA</span><textarea data-ob="respuesta" rows="3" style="width:100%">${esc(o.respuesta)}</textarea></label>
  <button class="btn small ghost" type="button" data-delobj>${ic('trash', 13)} Quitar</button></div>`;

function drawProduct(root) {
  const p = PS.p; const e = p.envio || {};
  const title = PS.isNew ? 'Nuevo producto' : p.nombre;
  root.innerHTML = `
    <div class="crumbs"><a href="#productos">Productos</a> / ${esc(title)}</div>
    <div class="view-head"><div><h2>${esc(title)}</h2>${PS.isNew ? '' : `<div class="sub"><code>${esc(p.sku)}</code> ${p.activo ? '<span class="tag ok">Activo</span>' : '<span class="tag">Inactivo</span>'} ${p.costo_confirmado ? '' : '<span class="tag warn">Costo sin confirmar</span>'}</div>`}</div>
      <span class="spacer"></span>
      ${PS.isNew ? '' : `<button class="btn ${p.activo ? '' : 'ok'}" id="pToggle">${p.activo ? 'Desactivar' : 'Activar para vender'}</button><button class="btn danger" id="pDelete">${ic('trash', 14)} Borrar</button>`}
      <button class="btn primary" id="pSave">${PS.isNew ? 'Crear producto' : 'Guardar cambios'}</button></div>
    <div class="tabs" id="pTabs">${TABS.map(([k, l]) => `<button type="button" data-tab="${k}" class="${PS.tab === k ? 'active' : ''}">${l}</button>`).join('')}</div>

    <form id="pForm" onsubmit="return false">
    <div data-pane="ficha">
      <div class="card"><div class="card-head"><div><h3>Datos del producto</h3><div class="desc">Lo que la IA sabe para presentarlo con precisión. No inventa nada que no esté aquí.</div></div></div>
        <div class="fgrid">
          <label class="field"><span>Código (SKU)</span><input data-f="sku" value="${esc(p.sku)}" ${PS.isNew ? '' : 'readonly'} placeholder="MI-PRODUCTO-60"><small>${PS.isNew ? 'Único. Letras, números y guiones.' : 'El código no se puede cambiar.'}</small></label>
          ${fld('nombre', 'Nombre', p.nombre).replace('name="nombre"', 'data-f="nombre"')}
          <label class="field"><span>Categoría</span><input data-f="categoria" value="${esc(p.categoria)}"></label>
          <label class="field"><span>Presentación</span><input data-f="presentacion" value="${esc(p.presentacion)}" placeholder="Frasco x 60 unidades"></label>
          <label class="field full"><span>Descripción corta</span><textarea data-f="descripcion_corta" rows="2" style="width:100%">${esc(p.descripcion_corta)}</textarea></label>
          <label class="field full"><span>Modo de uso</span><textarea data-f="modo_uso" rows="2" style="width:100%">${esc(p.modo_uso)}</textarea></label>
          <label class="field"><span>Sensación o resultado esperado</span><input data-f="sensacion" value="${esc(p.sensacion)}"></label>
          <label class="field"><span>Garantía</span><input data-f="garantia" value="${esc(p.garantia)}" placeholder="30 días de devolución"></label>
        </div></div>
      <div class="grid2" style="margin-top:14px">
        <div class="card"><h3>Ingredientes o componentes</h3>${lst('ingredientes', p.ingredientes || [], 'Ingrediente')}</div>
        <div class="card"><h3>Precauciones</h3><div class="desc small muted" style="margin-bottom:8px">Advertencias que la IA debe mencionar cuando corresponda.</div>${lst('precauciones', p.precauciones || [], 'Precaución')}</div>
      </div>
      <div class="card" style="margin-top:14px"><h3>Envío</h3><div class="fgrid c3">
        <label class="field"><span>Ciudades principales</span><input data-f="envio.tiempo_ciudades_principales" value="${esc(e.tiempo_ciudades_principales)}" placeholder="2 a 3 días hábiles"></label>
        <label class="field"><span>Otras zonas</span><input data-f="envio.tiempo_otras_zonas" value="${esc(e.tiempo_otras_zonas)}" placeholder="4 a 7 días hábiles"></label>
        <label class="field"><span>Costo para el cliente</span><input data-f="envio.costo_para_cliente" value="${esc(e.costo_para_cliente)}" placeholder="Envío gratis"></label></div></div>
    </div>

    <div data-pane="ofertas">
      <div class="card"><div class="card-head"><div><h3>Costos con el proveedor</h3><div class="desc">Con esto se calcula cuánto ganas realmente por cada venta.</div></div></div>
        <div class="fgrid">
          <label class="field"><span>Costo del producto (COP, por unidad)</span><input data-f="costo_proveedor" type="number" min="0" step="100" value="${p.costo_proveedor || 0}"></label>
          <label class="field"><span>Flete estimado (COP, por envío)</span><input data-f="flete_estimado" type="number" min="0" step="100" value="${p.flete_estimado || 0}"></label>
          <label class="field"><span>ID del producto en Dropi (opcional)</span><div class="inline"><input data-f="dropi_product_id" value="${esc(p.dropi_product_id)}"><button class="btn" type="button" id="pPullCost" ${PS.isNew ? 'disabled title="Guarda el producto primero"' : ''}>${ic('refresh', 14)} Traer costos de Dropi</button></div><small>Con el ID, el Motor copia el costo real desde Dropi y lo marca como confirmado. Requiere Dropi conectado en Conexiones.</small></label>
          <label class="switch" style="align-self:end"><input data-f="costo_confirmado" type="checkbox" ${p.costo_confirmado ? 'checked' : ''}><div><b>Costo confirmado</b><span>Márcalo cuando el proveedor confirmó costo y flete.</span></div></label>
        </div></div>
      <div class="card" style="margin-top:14px"><div class="card-head"><div><h3>Ofertas</h3><div class="desc">Combos que la IA puede ofrecer. Marca como principal la que debe recomendar primero.</div></div></div>
        <div class="offer-row head" style="grid-template-columns:70px 1fr 80px 120px 74px 32px"><span>Código</span><span>Nombre</span><span>Unidades</span><span>Precio (COP)</span><span></span><span></span></div>
        <div id="offerRows">${(p.ofertas || []).map(offerRow).join('')}</div>
        <button class="btn small" type="button" id="addOffer">${ic('plus', 13)} Agregar oferta</button></div>
    </div>

    <div data-pane="finanzas">
      <div id="finBox"></div>
    </div>

    <div data-pane="argumentos">
      <div class="grid2">
        <div class="card"><h3>Dolores del cliente</h3><div class="desc small muted" style="margin-bottom:8px">Problemas que resuelve el producto, con las palabras del cliente.</div>${lst('dolores', p.dolores || [], 'Me despierto cansado todos los días')}</div>
        <div class="card"><h3>Beneficios</h3><div class="desc small muted" style="margin-bottom:8px">Resultados concretos y verificables.</div>${lst('beneficios', p.beneficios || [], 'Beneficio')}</div>
      </div>
      <div class="card" style="margin-top:14px"><div class="card-head"><div><h3>Objeciones y respuestas</h3><div class="desc">Cómo debe responder la IA a las dudas más comunes.</div></div></div>
        <div id="objRows">${(p.objeciones || []).map(objRow).join('')}</div>
        <button class="btn small" type="button" id="addObj">${ic('plus', 13)} Agregar objeción</button></div>
    </div>
    </form>`;
  showTab(PS.tab);
  bindProduct(root);
}

function showTab(tab) {
  PS.tab = tab;
  $$('#pTabs button').forEach((b) => b.classList.toggle('active', b.dataset.tab === tab));
  $$('[data-pane]').forEach((d) => d.classList.toggle('hidden', d.dataset.pane !== tab));
}

// Lee el formulario completo y lo mezcla sobre el producto en memoria (conserva campos sin pantalla).
function collectProduct() {
  const out = JSON.parse(JSON.stringify(PS.p));
  const num = new Set(['costo_proveedor', 'flete_estimado']);
  $$('#pForm [data-f]').forEach((el) => {
    const path = el.dataset.f.split('.');
    let v = el.type === 'checkbox' ? el.checked : el.value;
    if (num.has(el.dataset.f)) v = Number(v) || 0;
    let o = out;
    for (let i = 0; i < path.length - 1; i++) { o[path[i]] = o[path[i]] || {}; o = o[path[i]]; }
    o[path[path.length - 1]] = v;
  });
  $$('#pForm [data-list]').forEach((box) => { out[box.dataset.list] = $$('.le input', box).map((i) => i.value.trim()).filter(Boolean); });
  out.ofertas = $$('#pForm [data-offer]').map((r) => {
    const g = (k) => $(`[data-o="${k}"]`, r);
    return { id: g('id').value.trim(), etiqueta: g('etiqueta').value.trim(), unidades: Math.max(1, parseInt(g('unidades').value, 10) || 1), precio: Math.round(Number(g('precio').value) || 0), destacada: g('destacada').checked };
  });
  out.objeciones = $$('#pForm [data-obj]').map((r) => ({ objecion: $('[data-ob="objecion"]', r).value, respuesta: $('[data-ob="respuesta"]', r).value }));
  return out;
}

function bindProduct(root) {
  $('#pTabs').onclick = (e) => { const b = e.target.closest('button[data-tab]'); if (b) showTab(b.dataset.tab); };
  const form = $('#pForm');
  form.onclick = (e) => {
    const add = e.target.closest('[data-addrow]');
    if (add) {
      const box = add.closest('[data-list]');
      add.parentElement.insertAdjacentHTML('beforebegin', listRow('', ''));
      $$('.le input', box).pop().focus();
      return;
    }
    if (e.target.closest('[data-delrow]')) { e.target.closest('.le').remove(); refreshEconomics(); return; }
    if (e.target.closest('[data-deloffer]')) { e.target.closest('[data-offer]').remove(); refreshEconomics(); return; }
    if (e.target.closest('[data-delobj]')) { e.target.closest('[data-obj]').remove(); }
  };
  form.oninput = () => refreshEconomics();
  $('#addOffer').onclick = () => {
    const n = $$('#pForm [data-offer]').length + 1;
    $('#offerRows').insertAdjacentHTML('beforeend', offerRow({ id: 'x' + n, etiqueta: n + ' unidades', unidades: n, precio: 0 }));
  };
  $('#addObj').onclick = () => $('#objRows').insertAdjacentHTML('beforeend', objRow({ objecion: '', respuesta: '' }));
  $('#pSave').onclick = (e) => busy(e.currentTarget, saveProduct);
  const pc = $('#pPullCost');
  if (pc) pc.onclick = (e) => busy(e.currentTarget, async () => {
    try {
      const idv = $('[data-f="dropi_product_id"]').value.trim();
      if (idv !== (PS.p.dropi_product_id || '')) await saveProduct();
      const r = await post('/api/productos/' + encodeURIComponent(PS.sku) + '/costos-dropi');
      toast(`Costo traído de Dropi: ${cop(r.costo)} por unidad (antes ${cop(r.costo_anterior)}). Ya está confirmado.`);
      await openProduct($('#productos'), PS.sku);
    } catch (err) { toast(err.message, true); }
  });
  const tg = $('#pToggle');
  if (tg) tg.onclick = (e) => busy(e.currentTarget, async () => {
    try {
      await post(`/api/productos/${encodeURIComponent(PS.sku)}/activar`, { activo: !PS.p.activo });
      toast(PS.p.activo ? 'Producto desactivado: la IA ya no lo ofrece.' : 'Producto activo: la IA ya puede venderlo.');
      await openProduct(root, PS.sku);
    } catch (err) { toast(err.message, true); }
  });
  const dl = $('#pDelete');
  if (dl) dl.onclick = async () => {
    if (!confirm(`¿Borrar "${PS.p.nombre}"? Esta acción no se puede deshacer.`)) return;
    try { await del('/api/productos/' + encodeURIComponent(PS.sku)); toast('Producto borrado.'); location.hash = 'productos'; } catch (err) { toast(err.message, true); }
  };
}

async function saveProduct() {
  const p = collectProduct();
  try {
    if (PS.isNew) {
      if (!p.sku.trim()) throw new Error('Escribe un código (SKU) para el producto.');
      const r = await post('/api/productos', p);
      toast('Producto creado. Actívalo cuando esté listo para vender.');
      location.hash = 'productos/' + encodeURIComponent(r.sku);
    } else {
      await put('/api/productos/' + encodeURIComponent(PS.sku), p);
      toast('Cambios guardados.');
      await openProduct($('#productos'), PS.sku);
    }
  } catch (err) { toast(err.message, true); }
}

// ---------- finanzas ----------
function refreshEconomics() {
  clearTimeout(PS.timer);
  PS.timer = setTimeout(async () => {
    if (!$('#finBox')) return;
    try {
      const d = await post('/api/productos/calcular', collectProduct());
      drawEconomics(d.economia, d.supuestos);
    } catch (_) { /* se reintenta al siguiente cambio */ }
  }, 350);
}

function drawEconomics(eco, sup) {
  const box = $('#finBox');
  if (!box) return;
  sup = sup || {};
  const real = PS.real;
  const cards = (eco || []).map((o) => `<div class="card ${o.destacada ? 'hl' : ''}" ${o.destacada ? 'style="border-color:var(--brand)"' : ''}>
      <div class="row"><b>${esc(o.etiqueta)}</b><span class="spacer"></span>${o.destacada ? '<span class="tag brand">Principal</span>' : ''}${o.contraentrega.viable ? '' : '<span class="tag bad">No rentable</span>'}</div>
      <div class="price">${cop(o.precio)} <span class="small muted" style="font-weight:400">${usd(o.precio_usd)}</span></div>
      <div class="small muted">${cop(o.precio_por_unidad)} por unidad · ${o.unidades} unidad(es)</div>
      <dl class="kv">
        <dt>Ganancia si entrega</dt><dd>${cop(o.contraentrega.ganancia_si_entrega)} (${o.contraentrega.margen_pct}%)</dd>
        <dt>Pérdida si devuelve</dt><dd>${cop(o.contraentrega.perdida_si_devuelve)}</dd>
        <dt>Esperada, contraentrega</dt><dd><b class="${o.contraentrega.ganancia_esperada < 0 ? 'neg' : 'pos'}">${cop(o.contraentrega.ganancia_esperada)}</b></dd>
        <dt>Esperada, con anticipo</dt><dd>${cop(o.anticipo_parcial.ganancia_esperada)}</dd>
        <dt>CPA de equilibrio</dt><dd>${cop(o.contraentrega.cpa_equilibrio)}</dd>
        <dt>CPA objetivo</dt><dd><b>${cop(o.cpa_objetivo)}</b> por pedido</dd>
        <dt>Costo por chat</dt><dd>${cop(o.costo_por_chat_objetivo)} máx.</dd>
      </dl>
      ${o.contraentrega.advertencia ? `<div class="small" style="color:var(--warn)">${ic('alert', 13)} ${esc(o.contraentrega.advertencia)}</div>` : ''}</div>`).join('');
  box.innerHTML = `
    ${PS.p && PS.p.costo_confirmado ? '' : notice('warn', 'El costo y el flete aún no están confirmados con el proveedor, así que estas cifras son estimadas.')}
    <div class="card" style="margin-bottom:14px"><div class="card-head"><div><h3>Supuestos del negocio</h3><div class="desc">Aplican a todos los productos. Se cambian en <a href="#ajustes">Ajustes</a>.</div></div></div>
      <div class="kpis"><div class="kpi"><div class="l">Devoluciones esperadas</div><div class="v">${pct(sup.tasa_devolucion)}</div></div>
      <div class="kpi"><div class="l">Margen de seguridad</div><div class="v">${pct(sup.margen_seguridad)}</div></div>
      <div class="kpi"><div class="l">Pedidos que se confirman</div><div class="v">${pct(sup.tasa_confirmacion)}</div></div>
      <div class="kpi"><div class="l">Dólar (TRM)</div><div class="v">${cop(sup.trm_cop_usd)}</div></div></div></div>
    <div class="section-title">Rentabilidad por oferta</div>
    ${cards ? `<div class="eco">${cards}</div>` : '<div class="card"><div class="empty">Agrega al menos una oferta con precio para ver la rentabilidad.</div></div>'}
    <div class="formula"><b>Cómo se calcula.</b> Ganancia esperada = (1 - d) x (precio - costo - flete) - d x (flete de ida y vuelta - anticipo), con d la tasa de devolución. El CPA objetivo es lo máximo que puedes pagar en anuncios por cada pedido despachado conservando el margen de seguridad.</div>
    ${real ? `<div class="section-title" style="margin-top:20px">Resultados reales de este producto</div>
      <div class="kpis">
        <div class="kpi"><div class="l">Pedidos</div><div class="v">${real.pedidos}</div></div>
        <div class="kpi"><div class="l">Abiertos</div><div class="v">${real.abiertos}</div></div>
        <div class="kpi"><div class="l">Aprobados</div><div class="v">${real.aprobados}</div></div>
        <div class="kpi"><div class="l">Entregados</div><div class="v">${real.entregados}</div></div>
        <div class="kpi"><div class="l">Devueltos</div><div class="v">${real.devueltos}</div></div>
        <div class="kpi"><div class="l">Cancelados</div><div class="v">${real.cancelados}</div></div>
        <div class="kpi"><div class="l">Ventas</div><div class="v">${cop(real.ventas)}</div></div>
        <div class="kpi"><div class="l">Anticipos cobrados</div><div class="v">${cop(real.anticipos_cobrados)}</div></div>
      </div>
      ${(PS.pedidos || []).length ? `<div class="card table-wrap" style="margin-top:14px"><table class="table"><thead><tr><th>#</th><th>Fecha</th><th>Cliente</th><th>Oferta</th><th>Total</th><th>Estado</th></tr></thead><tbody>
        ${PS.pedidos.map((o) => `<tr onclick="location.hash='pedidos'"><td>${o.id}</td><td>${fmtTime(o.created_at)}</td><td>${esc(o.customer_name || contactLabel(o.phone))}</td><td>${esc(o.offer_label || '-')}</td><td>${cop(o.total)}</td><td>${statusTag(o.status)}</td></tr>`).join('')}</tbody></table></div>` : ''}` : ''}`;
}

// ---------- guía de ventas ----------
async function openGuide() {
  let texto = '';
  try { texto = (await api('/api/guia-ventas')).texto || ''; } catch (err) { toast(err.message, true); return; }
  openModal(`<h3>Guía de ventas de la IA</h3>
    <p class="small muted">Instrucciones generales que la IA sigue con todos los productos: tono, cómo manejar objeciones, cuándo pedir los datos, qué nunca decir. Se escribe en texto normal (Markdown).</p>
    <textarea id="guideText" rows="16" style="width:100%;font-family:ui-monospace,Menlo,monospace;font-size:12px">${esc(texto)}</textarea>
    <div class="form-actions"><button class="btn primary" id="guideSave">Guardar guía</button><button class="btn" data-close>Cerrar</button></div>`, true);
  $('#guideSave').onclick = (e) => busy(e.currentTarget, async () => {
    try { await put('/api/guia-ventas', { texto: $('#guideText').value }); toast('Guía guardada. La IA la usa desde el próximo mensaje.'); closeModal(); } catch (err) { toast(err.message, true); }
  });
}

// ---------- consulta de zona ----------
function openZone(products) {
  openModal(`<h3>Consultar zona de envío</h3>
    <p class="small muted">Comprueba qué haría el Motor con un destino: nivel de riesgo, forma de pago y transportadoras sugeridas.</p>
    <form id="zoneForm"><div class="fgrid">
      ${fld('departamento', 'Departamento', '', { ph: 'Antioquia' })}${fld('ciudad', 'Ciudad', '', { ph: 'Medellín' })}
      ${fld('barrio', 'Barrio (opcional)', '')}${fld('total', 'Valor del pedido (COP)', '', { type: 'number', ph: '89900' })}
      <label class="field full"><span>Producto</span><select name="producto_sku">${products.map((p) => `<option value="${esc(p.sku)}">${esc(p.nombre)}</option>`).join('')}</select></label></div>
      <div class="form-actions"><button class="btn primary" type="submit">Consultar</button><button class="btn" type="button" data-close>Cerrar</button></div></form>
    <div id="zoneResult"></div>`);
  $('#zoneForm').onsubmit = async (e) => {
    e.preventDefault();
    const b = formData(e.target); b.total = Number(b.total) || 0;
    try {
      const d = await post('/api/zonas/consultar', b);
      $('#zoneResult').innerHTML = `<div style="margin-top:14px"><div class="row gap wrap">${riskTag(d.riesgo)} ${payTag(d.politica_pago)} ${d.zona_reconocida ? '' : '<span class="tag warn">Zona no reconocida</span>'}</div>
        <dl class="kv"><dt>Motivo</dt><dd>${esc(d.motivo)}</dd><dt>Anticipo</dt><dd>${cop(d.anticipo)}</dd><dt>Contraentrega</dt><dd>${cop(d.contraentrega)}</dd>
        <dt>Transportadoras</dt><dd>${esc((d.transportadoras_sugeridas || []).join(', '))}</dd>
        <dt>Le dirá al cliente</dt><dd class="small">${esc(d.explicacion_cliente)}</dd></dl>
        <p class="small muted">Las reglas de zonas están en <code>motor/knowledge/zonas.yaml</code>.</p></div>`;
    } catch (err) { toast(err.message, true); }
  };
}

Object.assign(window, { renderProductos });
