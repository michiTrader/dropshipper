// Dropshipper Motor · ajustes del negocio, la persona de la IA, la autonomía y los supuestos financieros.
'use strict';

const sw = (name, title, desc, on) => `<label class="switch"><input type="checkbox" name="${name}" ${on ? 'checked' : ''}><div><b>${title}</b><span>${desc}</span></div></label>`;
const pctField = (name, label, frac, help) => `<label class="field"><span>${label}</span><div class="suffix"><input name="${name}" type="number" step="0.5" min="0" max="100" value="${Math.round((Number(frac) || 0) * 1000) / 10}"><em>%</em></div>${help ? `<small>${help}</small>` : ''}</label>`;

async function renderAjustes() {
  const cfg = await api('/api/ajustes');
  const n = cfg.negocio; const p = n.persona || {}; const a = n.autonomia || {}; const c = n.conversacion || {}; const f = n.finanzas || {}; const d = n.dropi || {}; const nt = n.notificar || {};
  $('#ajustes').innerHTML = `
    <div class="view-head"><div><h2>Ajustes</h2><div class="sub">Datos del negocio y reglas de funcionamiento. Los cambios se aplican al instante, sin reiniciar.</div></div></div>
    <form id="ajForm" autocomplete="off">
      <div class="card"><div class="card-head"><span class="ico">${ic('user', 18)}</span><div><h3>Tienda y vendedor virtual</h3><div class="desc">Cómo se presenta la IA ante tus clientes.</div></div></div>
        <div class="fgrid">
          ${fld('nombre_tienda', 'Nombre de la tienda', n.nombre_tienda)}${fld('ciudad', 'Ciudad base', n.ciudad)}
          ${fld('persona_nombre', 'Nombre del vendedor', p.nombre, { help: 'El nombre con que se presenta en WhatsApp.' })}${fld('persona_rol', 'Rol', p.rol, { ph: 'Asesora de bienestar' })}
          <label class="field full"><span>Tono de comunicación</span><textarea name="persona_tono" rows="2" style="width:100%">${esc(p.tono)}</textarea><small>Ejemplo: cercano, respetuoso, sin presionar, en español colombiano.</small></label>
        </div></div>

      <div class="card" style="margin-top:16px"><div class="card-head"><span class="ico">${ic('shield', 18)}</span><div><h3>Autonomía de la IA</h3><div class="desc">La IA conversa; tú decides cuándo se compromete dinero. Puedes darle permiso para aprobar sola los pedidos de bajo riesgo.</div></div></div>
        ${sw('auto_aprobar_pedidos', 'Aprobar pedidos automáticamente', 'Si está apagado, cada pedido espera tu aprobación (recomendado al empezar).', a.auto_aprobar_pedidos)}
        <div class="fgrid" style="margin-left:26px">
          <label class="field"><span>Valor máximo para aprobar sola (COP)</span><input name="auto_max_valor" type="number" min="0" step="1000" value="${a.auto_max_valor || 0}"></label><div></div>
          ${sw('auto_solo_contraentrega', 'Solo contraentrega', 'No aprueba solas las que requieren anticipo.', a.auto_solo_contraentrega)}
          ${sw('auto_solo_riesgo_normal', 'Solo zonas de riesgo normal', 'Las zonas lejanas o difíciles siempre pasan por ti.', a.auto_solo_riesgo_normal)}
        </div></div>

      <div class="card" style="margin-top:16px"><div class="card-head"><span class="ico">${ic('dollar', 18)}</span><div><h3>Supuestos financieros</h3><div class="desc">Con ellos se calcula la ganancia esperada y cuánto puedes pagar por anuncio. Aplican a todos los productos.</div></div></div>
        <div class="fgrid">
          ${pctField('tasa_devolucion', 'Devoluciones esperadas', f.tasa_devolucion, 'De cada 100 pedidos enviados, cuántos regresan.')}
          ${pctField('tasa_confirmacion', 'Pedidos que se confirman', f.tasa_confirmacion, 'De los chats que llegan a pedido, cuántos se confirman.')}
          ${pctField('margen_seguridad', 'Margen de seguridad', f.margen_seguridad, 'Se resta del tope de anuncios para no operar al límite.')}
          <label class="field"><span>Dólar (COP por US$1)</span><input name="trm_cop_usd" type="number" min="0" step="any" value="${f.trm_cop_usd || 4000}"><small>Solo para mostrar equivalentes en dólares.</small></label>
        </div></div>

      <div class="card" style="margin-top:16px"><div class="card-head"><span class="ico">${ic('truck', 18)}</span><div><h3>Despacho y avisos</h3></div></div>
        <div class="fgrid">
          <label class="field"><span>Cómo se crean las órdenes en Dropi</span><select name="dropi_modo"><option value="manual" ${d.modo !== 'api' ? 'selected' : ''}>Manual: copio los datos y registro la guía</option><option value="api" ${d.modo === 'api' ? 'selected' : ''}>Por API (requiere token de integraciones de Dropi)</option></select></label>
          ${fld('whatsapp_dueno', 'Tu WhatsApp para avisos (opcional)', nt.whatsapp_dueno, { ph: '573001234567', help: 'Recibes avisos de pedidos nuevos y de clientes que piden una persona. Solo llega si escribiste al número del negocio en las últimas 24 h.' })}
        </div></div>

      <div class="card" style="margin-top:16px"><div class="card-head"><span class="ico">${ic('message', 18)}</span><div><h3>Ritmo de la conversación</h3><div class="desc">Hace que la IA se sienta natural: deja que el cliente termine de escribir y responde en varios mensajes cortos.</div></div></div>
        <div class="fgrid c3">
          <label class="field"><span>Espera antes de responder (segundos)</span><input name="espera_agrupar_seg" type="number" min="0" max="600" value="${c.espera_agrupar_seg ?? 60}"><small>La IA espera este tiempo desde el último mensaje del cliente, por si escribe en varios mensajes seguidos. Por defecto 60 (1 minuto); 0 responde de inmediato.</small></label>
          <label class="field"><span>Pausa entre mensajes (ms)</span><input name="pausa_entre_mensajes_ms" type="number" min="0" step="100" value="${c.pausa_entre_mensajes_ms ?? 1200}"></label>
          <label class="field"><span>Mensajes que la IA recuerda</span><input name="historial_maximo" type="number" min="4" max="200" value="${c.historial_maximo ?? 40}"></label>
        </div></div>

      <div class="form-actions" style="position:sticky;bottom:0;background:var(--bg);padding:12px 0;margin-top:8px"><span class="spacer"></span><button class="btn primary" type="submit">Guardar ajustes</button></div>
    </form>
    <div class="card" style="margin-top:16px"><div class="card-head"><span class="ico">${ic('sun', 18)}</span><div><h3>Apariencia</h3><div class="desc">Elige el tema del panel. Se recuerda en este dispositivo.</div></div></div>
      <div class="themes" id="themeOpts">${[['light', 'Claro', 'Fondo blanco, ideal de día', 'sun'], ['dark', 'Oscuro', 'Descansa la vista de noche', 'moon'], ['auto', 'Automático', 'Sigue el tema de tu equipo', 'monitor']]
        .map(([k, t, d2, i]) => `<button type="button" class="themeopt ${Theme.mode() === k ? 'sel' : ''}" data-theme="${k}">${ic(i, 20)}<div><b>${t}</b><span>${d2}</span></div></button>`).join('')}</div></div>
    <div class="card" style="margin-top:16px"><h3>Sesión</h3>
      <div class="small muted" style="margin-bottom:10px">Motor v${esc(S.status ? S.status.version : '')} · iniciado ${fmtTime(S.status && S.status.iniciado)}</div>
      <button class="btn" id="logout">Cerrar sesión en este dispositivo</button></div>`;

  $('#themeOpts').onclick = (e) => { const b = e.target.closest('[data-theme]'); if (!b) return; Theme.set(b.dataset.theme); $$('#themeOpts .themeopt').forEach((x) => x.classList.toggle('sel', x === b)); };
  $('#logout').onclick = () => { localStorage.removeItem('ds_token'); S.token = ''; location.reload(); };
  $('#ajForm').onsubmit = async (e) => {
    e.preventDefault();
    const g = formData(e.target); const ck = (k) => e.target[k].checked; const num = (k) => Number(g[k]) || 0;
    const body = {
      nombre_tienda: g.nombre_tienda.trim(), ciudad: g.ciudad.trim(),
      persona: { nombre: g.persona_nombre.trim(), rol: g.persona_rol.trim(), tono: g.persona_tono.trim() },
      autonomia: { auto_aprobar_pedidos: ck('auto_aprobar_pedidos'), auto_max_valor: num('auto_max_valor'), auto_solo_contraentrega: ck('auto_solo_contraentrega'), auto_solo_riesgo_normal: ck('auto_solo_riesgo_normal') },
      finanzas: { tasa_devolucion: num('tasa_devolucion') / 100, tasa_confirmacion: num('tasa_confirmacion') / 100, margen_seguridad: num('margen_seguridad') / 100, trm_cop_usd: num('trm_cop_usd') },
      dropi: { modo: g.dropi_modo }, notificar: { whatsapp_dueno: g.whatsapp_dueno.trim() },
      conversacion: { espera_agrupar_seg: num('espera_agrupar_seg'), pausa_entre_mensajes_ms: num('pausa_entre_mensajes_ms'), historial_maximo: num('historial_maximo') },
    };
    await busy(e.submitter, async () => {
      try { await put('/api/ajustes/negocio', body); toast('Ajustes guardados.'); refreshBadges(); } catch (err) { toast(err.message, true); }
    });
  };

}

Object.assign(window, { renderAjustes });
