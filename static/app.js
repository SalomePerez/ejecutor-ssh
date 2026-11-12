// ============================================================
// Ejecutor SSH — logica de la interfaz
// Modulos: VMs (lista y creacion; la Red vive en network.js), Comandos,
// Llaves SSH, Usuarios y Configuracion. Avisos con toast() y dialogos con
// ask(), en lugar de alert/confirm/prompt del navegador.
// ============================================================
'use strict';

// ============== FUNCIONES EN PRUEBA ==============
// Redes virtuales, tarjetas de red y distribuciones distintas de Ubuntu aun no
// estan verificadas en el hipervisor: se ocultan salvo con ?experimental en la URL.
const EXPERIMENTAL = new URLSearchParams(location.search).has('experimental');
document.documentElement.classList.toggle('experimental', EXPERIMENTAL);
// Plantillas verificadas de punta a punta (crear, primer arranque, agente, Red).
// Debian 12 solo emulada: con KVM tumba el hipervisor (ver EMULATED_DISTROS).
const VERIFIED_TEMPLATE = /ubuntu|debian-12/i;

// ============== UTILIDADES ==============
const $ = (sel, root = document) => root.querySelector(sel);
const $$ = (sel, root = document) => [...root.querySelectorAll(sel)];
const esc = s => String(s ?? '').replace(/&/g, '&amp;').replace(/</g, '&lt;').replace(/>/g, '&gt;').replace(/"/g, '&quot;');

const store = {
  get(k, d = null) { try { const v = localStorage.getItem(k); return v === null ? d : v; } catch (e) { return d; } },
  set(k, v) { try { localStorage.setItem(k, v); } catch (e) { /* almacenamiento no disponible */ } },
  del(k) { try { localStorage.removeItem(k); } catch (e) { /* idem */ } }
};

// api hace la solicitud y siempre devuelve {ok, status, data}; si el servidor
// responde texto en vez de JSON, el texto queda en data.error.
async function api(path, { method = 'GET', body, headers = {} } = {}) {
  const opts = { method, headers: { ...headers } };
  if (body !== undefined) {
    opts.headers['Content-Type'] = 'application/json';
    opts.body = JSON.stringify(body);
  }
  let response;
  try {
    response = await fetch(path, opts);
  } catch (err) {
    return { ok: false, status: 0, data: { error: 'Sin conexión con la aplicación: ' + err.message } };
  }
  const text = await response.text();
  let data;
  try { data = text ? JSON.parse(text) : {}; } catch (e) { data = { error: text.trim() }; }
  return { ok: response.ok, status: response.status, data };
}
const errMsg = (r, fallback) => (r.data && (r.data.error || r.data.message)) || fallback;

function toast(type, message, ms) {
  const box = $('#toasts');
  const el = document.createElement('div');
  el.className = 'toast ' + type;
  el.setAttribute('role', type === 'error' ? 'alert' : 'status');
  el.innerHTML = `<span class="ico">${{ success: '✓', error: '✕', warning: '!', info: 'i' }[type] || 'i'}</span><div></div><button type="button" aria-label="Cerrar">×</button>`;
  el.children[1].textContent = message;
  el.querySelector('button').addEventListener('click', () => el.remove());
  box.appendChild(el);
  setTimeout(() => el.remove(), ms || (type === 'error' || type === 'warning' ? 9000 : 4500));
}

// ask abre el dialogo comun. Con input devuelve el texto (o null); sin input,
// true/false. confirmText obliga a escribir un texto exacto (borrados).
function ask({ title, html = '', ok = 'Aceptar', danger = false, input = null, confirmText = null }) {
  const dlg = $('#modal');
  $('#modal-title').textContent = title;
  const body = $('#modal-body');
  body.innerHTML = html;
  let field = null;
  if (input || confirmText) {
    const wrap = document.createElement('div');
    wrap.className = 'form-item';
    wrap.innerHTML = `<label for="modal-input">${esc(input?.label || 'Escriba «' + confirmText + '» para confirmar')}</label>` +
      `<input id="modal-input" type="${input?.type || 'text'}" autocomplete="off" spellcheck="false" placeholder="${esc(input?.placeholder || confirmText || '')}">` +
      (input?.remember ? '<label class="label" style="display:flex;gap:6px;align-items:center;font-weight:400"><input type="checkbox" id="modal-remember" checked> Recordar en este navegador</label>' : '');
    body.appendChild(wrap);
    field = wrap.querySelector('#modal-input');
  }
  const okBtn = $('#modal-ok');
  okBtn.textContent = ok;
  okBtn.className = danger ? 'btn-danger' : 'btn-primary';
  const sync = () => { okBtn.disabled = !!confirmText && field.value.trim() !== confirmText; };
  if (field) field.addEventListener('input', sync);
  sync();
  return new Promise(resolve => {
    const done = value => { dlg.removeEventListener('close', onClose); resolve(value); };
    const onClose = () => {
      if (dlg.returnValue !== 'ok') return done(input ? null : false);
      if (input) return done({ value: field.value, remember: !!$('#modal-remember')?.checked });
      done(true);
    };
    $('#modal-cancel').onclick = () => dlg.close('cancel');
    dlg.returnValue = '';
    dlg.addEventListener('close', onClose);
    dlg.showModal();
    (field || okBtn).focus();
  });
}

// El token de API protege descargar/borrar llaves y crear/borrar usuarios.
// Se pide una vez y, si el usuario quiere, se recuerda en este navegador.
let sessionToken = '';
async function apiToken(reason) {
  const saved = store.get('apiToken') || sessionToken;
  if (saved) return saved;
  const answer = await ask({
    title: 'Token de API',
    html: `<p>${esc(reason)} Es el valor de <code>security.apiToken</code> en <code>config.properties</code>.</p>`,
    ok: 'Continuar',
    input: { type: 'password', label: 'Token', placeholder: 'token', remember: true }
  });
  if (!answer || !answer.value.trim()) return null;
  sessionToken = answer.value.trim();
  if (answer.remember) store.set('apiToken', sessionToken);
  return sessionToken;
}
function forgetToken() { sessionToken = ''; store.del('apiToken'); }

// withToken repite la solicitud una vez si el token guardado ya no sirve.
async function withToken(reason, call) {
  for (let attempt = 0; attempt < 2; attempt++) {
    const token = await apiToken(reason);
    if (!token) return null;
    const r = await call(token);
    if (r.status !== 401) return r;
    forgetToken();
    toast('warning', 'El token no fue aceptado: ' + errMsg(r, 'token inválido'));
  }
  return null;
}

function busy(btn, on, text) {
  if (!btn) return;
  if (on) {
    btn.dataset.label = btn.innerHTML;
    btn.disabled = true;
    btn.innerHTML = `<span class="spinner" aria-hidden="true"></span>${esc(text || btn.textContent)}`;
  } else {
    btn.disabled = false;
    if (btn.dataset.label) btn.innerHTML = btn.dataset.label;
  }
}

function statusBox(el, type, message, ms) {
  el.textContent = message;
  el.className = 'status-box show ' + type;
  clearTimeout(el._t);
  if (ms !== 0) el._t = setTimeout(() => el.classList.remove('show'), ms || 6000);
}

async function copyText(text, what) {
  try {
    await navigator.clipboard.writeText(text);
    toast('success', (what || 'Texto') + ' copiado al portapapeles.');
  } catch (e) {
    toast('error', 'No se pudo copiar: ' + e.message);
  }
}

const relTime = ts => {
  const s = Math.round((Date.now() - ts) / 1000);
  if (s < 60) return 'hace ' + s + ' s';
  if (s < 3600) return 'hace ' + Math.round(s / 60) + ' min';
  return new Date(ts).toLocaleString();
};

// ============== TEMA ==============
$('#theme-toggle').addEventListener('click', () => {
  const root = document.documentElement;
  const dark = root.dataset.theme ? root.dataset.theme === 'dark' : matchMedia('(prefers-color-scheme: dark)').matches;
  root.dataset.theme = dark ? 'light' : 'dark';
  store.set('theme', root.dataset.theme);
});

// ============== PESTAÑAS ==============
const tabLoaders = {
  vms: () => listVMs(),
  commands: () => loadCommandConfig(),
  keys: () => listKeys(),
  users: () => listUsers(),
  config: () => loadConfig()
};
function showTab(name, { load = true } = {}) {
  if (!tabLoaders[name]) name = 'vms';
  $$('.tab-btn').forEach(b => {
    const on = b.dataset.tab === name;
    b.classList.toggle('active', on);
    b.setAttribute('aria-selected', on);
  });
  $$('.tab-content').forEach(c => c.classList.toggle('active', c.id === name + '-tab'));
  store.set('tab', name);
  if (location.hash.slice(1).split('/')[0] !== name) history.replaceState(null, '', '#' + name);
  if (load) tabLoaders[name]();
}
$$('.tab-btn').forEach(btn => btn.addEventListener('click', () => showTab(btn.dataset.tab)));

// ============== ESTADO DEL HIPERVISOR ==============
// La pastilla de la barra refleja la ultima respuesta de /api/vms.
function setHypervisorState(ok, detail) {
  const pill = $('#hv-status');
  pill.classList.remove('wait', 'ok', 'bad');
  pill.classList.add(ok ? 'ok' : 'bad');
  pill.querySelector('.txt').textContent = ok ? 'Hipervisor conectado' : 'Hipervisor sin conexión';
  pill.title = (detail || '') + ' — clic para ver la configuración';
  const chip = $('#cfg-hv-state');
  if (chip) {
    chip.className = 'chip ' + (ok ? 'ok' : 'bad');
    chip.textContent = ok ? 'conectado' : 'sin conexión';
  }
}
$('#hv-status').addEventListener('click', () => showTab('config'));

// ============== CONFIGURACIÓN ==============
let configCache = {};

function fillConfig(data) {
  configCache = data;
  $$('.config-input').forEach(el => {
    const v = data[el.dataset.key];
    el.value = v === undefined || v === null ? '' : String(v).replace(/,/g, el.tagName === 'TEXTAREA' ? ', ' : ',');
    el.dataset.orig = el.value;
    el.classList.remove('input-dirty');
  });
  markDirty();
}

function markDirty() {
  const changed = $$('.config-input').filter(el => el.value !== el.dataset.orig);
  $$('.config-input').forEach(el => el.classList.toggle('input-dirty', el.value !== el.dataset.orig));
  const tag = $('#cfg-dirty');
  tag.textContent = changed.length ? changed.length + (changed.length === 1 ? ' cambio sin guardar' : ' cambios sin guardar') : 'Sin cambios';
  tag.classList.toggle('on', changed.length > 0);
}
$$('.config-input').forEach(el => el.addEventListener('input', markDirty));

async function loadConfig() {
  const r = await api('/api/config');
  if (!r.ok) { toast('error', 'No se pudo leer la configuración: ' + errMsg(r, 'error')); return null; }
  fillConfig(r.data);
  updateCommandTarget(r.data);
  return r.data;
}

async function saveConfig() {
  const updates = {};
  $$('.config-input').forEach(el => {
    if (el.value === el.dataset.orig) return;
    let v = el.value.trim();
    if (el.dataset.key === 'commands.allowed') v = v.split(',').map(s => s.trim()).filter(Boolean).join(',');
    updates[el.dataset.key] = v;
  });
  if (!Object.keys(updates).length) { toast('info', 'No hay cambios para guardar.'); return; }
  const btn = $('#cfg-save-btn');
  busy(btn, true, 'Guardando…');
  const r = await api('/api/config', { method: 'PUT', body: updates });
  busy(btn, false);
  if (!r.ok) { toast('error', 'No se guardó: ' + errMsg(r, 'el servidor rechazó la configuración')); return; }
  fillConfig(r.data);
  updateCommandTarget(r.data);
  const restart = ('web.addr' in updates || 'web.port' in updates);
  toast(restart ? 'warning' : 'success', restart ? 'Guardado. Reinicie la app para usar la nueva dirección web.' : 'Configuración guardada.');
  if (Object.keys(updates).some(k => k.startsWith('vm.ssh'))) testHypervisor();
}

async function testHypervisor() {
  const btn = $('#cfg-hv-test');
  if ($$('.config-input').some(el => el.dataset.key.startsWith('vm.ssh') && el.value !== el.dataset.orig)) {
    toast('info', 'Guarde primero los cambios del hipervisor para probarlos.');
    return;
  }
  busy(btn, true, 'Probando…');
  const r = await api('/api/vms');
  busy(btn, false);
  const ok = r.ok && r.data.success;
  setHypervisorState(ok, ok ? r.data.data.length + ' VMs' : errMsg(r, ''));
  toast(ok ? 'success' : 'error', ok ? `Conexión correcta: ${r.data.data.length} VMs en el hipervisor.` : 'Sin conexión: ' + errMsg(r, 'error'));
}

$('#cfg-load-btn').addEventListener('click', async () => { await loadConfig(); toast('info', 'Configuración recargada desde el servidor.'); });
$('#cfg-save-btn').addEventListener('click', saveConfig);
$('#cfg-hv-test').addEventListener('click', testHypervisor);

// ============== COMANDOS ==============
function updateCommandTarget(data) {
  const mode = (data['execution.mode'] || 'remote').toLowerCase();
  $('#cmd-execution-mode-select').value = mode;
  $('#ssh-host').value = data['ssh.host'] || '';
  $('#cmd-ssh-port').value = data['ssh.port'] || '22';
  $('#cmd-ssh-user').value = data['ssh.user'] || '';
  syncCommandMode();

  const allowed = String(data['commands.allowed'] || '').split(',').map(s => s.trim()).filter(Boolean);
  $('#cmd-allowed').innerHTML = allowed.length
    ? allowed.map(c => `<button type="button" class="chip" data-cmd="${esc(c)}">${esc(c)}</button>`).join('')
    : '<small>No hay comandos permitidos: agréguelos en Configuración.</small>';
}

function syncCommandMode() {
  const local = $('#cmd-execution-mode-select').value === 'local';
  ['#ssh-host', '#cmd-ssh-port', '#cmd-ssh-user'].forEach(s => { $(s).disabled = local; });
  const badge = $('#cmd-execution-mode');
  badge.textContent = local ? 'Modo: local' : 'Modo: remoto por SSH';
  badge.className = 'status-badge ' + (local ? 'success' : 'connecting');
  $('#command-target-help').textContent = local
    ? 'Se ejecutará en el equipo donde corre la app.'
    : 'Se ejecutará en el host indicado. El destino se guarda al ejecutar.';
}
$('#cmd-execution-mode-select').addEventListener('change', syncCommandMode);

async function loadCommandConfig() {
  const r = await api('/api/config');
  if (r.ok) updateCommandTarget(r.data);
  renderHistory();
}

$('#cmd-allowed').addEventListener('click', e => {
  const b = e.target.closest('[data-cmd]');
  if (!b) return;
  const area = $('#command');
  area.value = b.dataset.cmd + ' ';
  area.focus();
});

$('#command').addEventListener('keydown', e => {
  if (e.key === 'Enter' && (e.ctrlKey || e.metaKey)) {
    e.preventDefault();
    $('#command-form').requestSubmit();
  }
});

function getHistory() { try { return JSON.parse(store.get('cmdHistory', '[]')); } catch (e) { return []; } }
function pushHistory(entry) {
  const list = getHistory().filter(h => !(h.cmd === entry.cmd && h.target === entry.target));
  list.unshift(entry);
  store.set('cmdHistory', JSON.stringify(list.slice(0, 8)));
  renderHistory();
}
function renderHistory() {
  const list = getHistory();
  $('#cmd-history').innerHTML = list.length
    ? list.map((h, i) => `<button type="button" data-i="${i}" title="Usar de nuevo"><span class="mono">${h.ok ? '✓' : '✕'} ${esc(h.cmd)}</span><span class="when">${esc(h.target)} · ${relTime(h.at)}</span></button>`).join('')
    : '<small>Aún no hay comandos.</small>';
}
$('#cmd-history').addEventListener('click', e => {
  const b = e.target.closest('[data-i]');
  if (!b) return;
  $('#command').value = getHistory()[+b.dataset.i].cmd;
  $('#command').focus();
});
$('#cmd-history-clear').addEventListener('click', () => { store.del('cmdHistory'); renderHistory(); });
$('#cmd-copy').addEventListener('click', () => copyText($('#cmd-output').textContent, 'Resultado'));

$('#command-form').addEventListener('submit', async e => {
  e.preventDefault();
  const cmd = $('#command').value.trim();
  if (!cmd) { toast('warning', 'Escriba un comando.'); return; }
  const output = $('#cmd-output');
  const status = $('#cmd-status');
  const submit = $('#command-submit');
  const target = {
    executionMode: $('#cmd-execution-mode-select').value,
    sshHost: $('#ssh-host').value.trim(),
    sshPort: $('#cmd-ssh-port').value.trim(),
    sshUser: $('#cmd-ssh-user').value.trim()
  };
  const targetLabel = target.executionMode === 'local' ? 'local' : `${target.sshUser}@${target.sshHost}`;

  output.classList.remove('is-error');
  output.innerHTML = '<span class="dim">Ejecutando en ' + esc(targetLabel) + '…</span>';
  status.textContent = 'Ejecutando';
  status.className = 'status-badge connecting';
  busy(submit, true, 'Ejecutando…');

  // El destino elegido se guarda para la proxima vez (y para Configuracion).
  api('/api/config', {
    method: 'PUT',
    body: { 'execution.mode': target.executionMode, 'ssh.host': target.sshHost, 'ssh.port': target.sshPort, 'ssh.user': target.sshUser }
  });

  const started = performance.now();
  const r = await api('/execute', { method: 'POST', body: { command: cmd, ...target } });
  const secs = ((performance.now() - started) / 1000).toFixed(1);
  busy(submit, false);

  const text = [r.data.output, r.data.error && (r.data.output ? '\n— ' + r.data.error : r.data.error)].filter(Boolean).join('');
  output.textContent = text || (r.ok ? 'El comando terminó sin producir salida.' : 'Sin respuesta.');
  output.classList.toggle('is-error', !r.ok);
  status.textContent = (r.ok ? 'Completado' : r.status === 403 ? 'No permitido' : 'Error') + ' · ' + secs + ' s';
  status.className = 'status-badge ' + (r.ok ? 'success' : 'error');
  pushHistory({ cmd, target: targetLabel, ok: r.ok, at: Date.now() });
});

// ============== LLAVES SSH ==============
$('#key-create-form').addEventListener('submit', async e => {
  e.preventDefault();
  const keyName = $('#key-name').value.trim();
  const username = $('#key-user').value.trim();
  const statusEl = $('#key-create-status');
  if (!/^[A-Za-z0-9_.-]+$/.test(keyName)) { statusBox(statusEl, 'error', 'El nombre solo puede tener letras, números, punto, guion y guion bajo.'); return; }
  const btn = e.submitter;
  busy(btn, true, 'Generando…');
  const r = await api('/api/keys', { method: 'POST', body: { keyName, username } });
  busy(btn, false);
  if (!r.ok) { statusBox(statusEl, 'error', 'No se generó: ' + errMsg(r, 'error')); return; }
  statusBox(statusEl, 'success', `Llave «${keyName}» generada (${r.data.data.algorithm}).`);
  $('#key-create-form').reset();
  listKeys();
});
$('#keys-refresh').addEventListener('click', () => listKeys());

async function listKeys() {
  const list = $('#keys-list');
  const r = await api('/api/keys');
  if (!r.ok || !r.data.success) { list.innerHTML = `<p class="placeholder err">No se pudieron leer las llaves: ${esc(errMsg(r, 'error'))}</p>`; return; }
  const keys = r.data.data || [];
  if (!keys.length) { list.innerHTML = '<p class="placeholder">Todavía no hay llaves. Genere una con el formulario.</p>'; return; }
  list.innerHTML = keys.map(k => {
    const vmKey = /-ssh$/.test(k.name);
    return `<div class="item-card" data-key="${esc(k.name)}">
      <div class="item-top"><span class="item-name mono">${esc(k.name)}</span>${vmKey ? '<span class="chip">llave de VM</span>' : ''}</div>
      <div class="item-meta"><span>${esc(k.algorithm)}</span><span class="mono">${esc(k.path)}</span></div>
      <div class="item-actions">
        <button type="button" class="btn-small" data-act="pub">Copiar pública</button>
        <button type="button" class="btn-small" data-act="download">Descargar privada</button>
        <span class="sep"></span>
        <button type="button" class="btn-small btn-danger" data-act="delete">Eliminar</button>
      </div>
    </div>`;
  }).join('');
}

$('#keys-list').addEventListener('click', async e => {
  const b = e.target.closest('[data-act]');
  if (!b) return;
  const name = b.closest('[data-key]').dataset.key;
  if (b.dataset.act === 'pub') {
    const r = await api('/api/keys/' + encodeURIComponent(name) + '/public');
    if (r.ok) copyText(r.data.data.publicKey, 'Llave pública');
    else toast('error', errMsg(r, 'No se pudo leer la llave pública'));
  } else if (b.dataset.act === 'download') {
    downloadKey(name);
  } else if (b.dataset.act === 'delete') {
    deleteKey(name);
  }
});

async function downloadKey(keyName) {
  // La descarga exige el header X-API-Key, por eso no puede ser un <a href>.
  let blob = null;
  const r = await withToken('Descargar la llave privada requiere el token de API.', async token => {
    const res = await fetch('/api/keys/' + encodeURIComponent(keyName) + '/download', { headers: { 'X-API-Key': token } });
    if (res.ok) { blob = await res.blob(); return { ok: true, status: 200, data: {} }; }
    const text = (await res.text()).trim();
    let data; try { data = JSON.parse(text); } catch (e) { data = { error: text }; }
    return { ok: false, status: res.status, data };
  });
  if (!r) return;
  if (!blob) { toast('error', 'No se descargó: ' + errMsg(r, 'error')); return; }
  const url = URL.createObjectURL(blob);
  const link = document.createElement('a');
  link.href = url;
  link.download = keyName;
  link.click();
  URL.revokeObjectURL(url);
  toast('success', 'Llave privada descargada. Guárdela con permisos restringidos.');
}

async function deleteKey(keyName) {
  const yes = await ask({
    title: 'Eliminar llave',
    html: `<p>Se borrarán <b>${esc(keyName)}</b> y <b>${esc(keyName)}.pub</b> del servidor. Quien use esta llave dejará de poder entrar.</p>`,
    ok: 'Eliminar', danger: true
  });
  if (!yes) return;
  const r = await withToken('Eliminar llaves requiere el token de API.', token =>
    api('/api/keys/' + encodeURIComponent(keyName), { method: 'DELETE', headers: { 'X-API-Key': token } }));
  if (!r) return;
  if (r.ok) { toast('success', `Llave «${keyName}» eliminada.`); listKeys(); }
  else toast('error', 'No se eliminó: ' + errMsg(r, 'error'));
}

// ============== USUARIOS ==============
// Cuentas que Windows crea solo; no se ofrecen para borrar.
const BUILTIN_USERS = ['administrator', 'administrador', 'guest', 'invitado', 'defaultaccount', 'wdagutilityaccount'];

$('#user-create-form').addEventListener('submit', async e => {
  e.preventDefault();
  const username = $('#user-name').value.trim();
  const password = $('#user-pass').value;
  const fullName = $('#user-full').value.trim();
  const statusEl = $('#user-create-status');
  if (password.length < 8) { statusBox(statusEl, 'error', 'La contraseña debe tener al menos 8 caracteres.'); return; }
  const btn = e.submitter;
  busy(btn, true, 'Creando…');
  const r = await withToken('Crear usuarios de Windows requiere el token de API.', token =>
    api('/api/users', { method: 'POST', body: { username, password, fullName }, headers: { 'X-API-Key': token } }));
  busy(btn, false);
  if (!r) return;
  if (r.ok) {
    statusBox(statusEl, 'success', `Usuario «${username}» creado.`);
    $('#user-create-form').reset();
    listUsers();
  } else {
    statusBox(statusEl, 'error', 'No se creó: ' + errMsg(r, 'error') + (r.status === 500 ? ' (¿la app corre como administrador?)' : ''), 0);
  }
});
$('#users-refresh').addEventListener('click', () => listUsers());

async function listUsers() {
  const list = $('#users-list');
  const r = await api('/api/users');
  if (!r.ok || !r.data.success) { list.innerHTML = `<p class="placeholder err">No se pudieron leer las cuentas: ${esc(errMsg(r, 'error'))}</p>`; return; }
  const users = r.data.data || [];
  if (!users.length) { list.innerHTML = '<p class="placeholder">No hay cuentas locales.</p>'; return; }
  users.sort((a, b) => (b.enabled - a.enabled) || a.name.localeCompare(b.name));
  list.innerHTML = users.map(u => {
    const builtin = BUILTIN_USERS.includes(u.name.toLowerCase());
    return `<div class="item-card" data-user="${esc(u.name)}">
      <div class="item-top">
        <span class="item-name">${esc(u.name)}</span>
        <span>${u.enabled ? '<span class="chip ok">activa</span>' : '<span class="chip dim">deshabilitada</span>'}${builtin ? ' <span class="chip dim">del sistema</span>' : ''}</span>
      </div>
      ${u.fullName || u.description ? `<div class="item-meta">${u.fullName ? '<span>' + esc(u.fullName) + '</span>' : ''}${u.description ? '<span>' + esc(u.description) + '</span>' : ''}</div>` : ''}
      ${builtin ? '' : '<div class="item-actions"><span class="sep"></span><button type="button" class="btn-small btn-danger" data-act="delete">Eliminar</button></div>'}
    </div>`;
  }).join('');
}

$('#users-list').addEventListener('click', async e => {
  const b = e.target.closest('[data-act="delete"]');
  if (!b) return;
  const name = b.closest('[data-user]').dataset.user;
  const yes = await ask({
    title: 'Eliminar usuario de Windows',
    html: `<p>Se eliminará la cuenta <b>${esc(name)}</b> de este equipo. Su carpeta de perfil no se borra.</p>`,
    ok: 'Eliminar', danger: true, confirmText: name
  });
  if (!yes) return;
  const r = await withToken('Eliminar usuarios requiere el token de API.', token =>
    api('/api/users/' + encodeURIComponent(name), { method: 'DELETE', headers: { 'X-API-Key': token } }));
  if (!r) return;
  if (r.ok) { toast('success', `Usuario «${name}» eliminado.`); listUsers(); }
  else toast('error', 'No se eliminó: ' + errMsg(r, 'error'));
});

// ============== MÁQUINAS VIRTUALES ==============
const DISTRO_NAMES = { 'ubuntu-24.04': 'Ubuntu 24.04', 'debian-12': 'Debian 12', 'debian-13': 'Debian 13', 'fedora-42': 'Fedora 42', 'arch': 'Arch Linux', 'rocky-9': 'Rocky 9' };
// Usuario que se sugiere al crear la VM, como el de la imagen oficial de cada distribucion.
const DISTRO_USERS = { 'ubuntu-24.04': 'ubuntu', 'debian-12': 'debian', 'debian-13': 'debian', 'fedora-42': 'fedora', 'arch': 'arch', 'rocky-9': 'rocky' };
const NET_MODES = { nat: 'NAT', route: 'Enrutada', isolated: 'Aislada', bridge: 'Puente', open: 'Abierta', internal: 'Interna', hostonly: 'Solo anfitrión' };
let vnetCache = [];
const VM_NAME_RE = /^[A-Za-z0-9-]+$/;
let vmCache = [];
let vmTimer = null;
let hostInfo = null; // memoria y CPU del hipervisor (GET /api/hypervisor)

const runningMemory = () => vmCache.filter(v => v.status === 'running').reduce((a, v) => a + (v.memoryMB || 0), 0);

function vmStateLabel(status) {
  if (status === 'running') return ['running', 'Encendida'];
  if (status === 'stopped') return ['stopped', 'Apagada'];
  return ['other', status || 'desconocido'];
}

// Comando para entrar a la VM desde este equipo. Con libvirt las VMs estan
// en la red NAT del hipervisor y se salta por el (-J); con VirtualBox la IP
// se alcanza directo (puente, solo anfitrion) desde este mismo equipo.
function sshHint(vm) {
  if (!vm.ip) return '';
  const c = configCache;
  const jump = c['vm.provider'] === 'libvirt' && c['vm.ssh.host'] ? ` -J ${c['vm.ssh.user'] || 'root'}@${c['vm.ssh.host']}${c['vm.ssh.port'] && c['vm.ssh.port'] !== '22' ? ':' + c['vm.ssh.port'] : ''}` : '';
  // Ruta absoluta (la manda el servidor) y entre comillas: la carpeta del
  // proyecto tiene espacios y el comando se pega desde cualquier carpeta.
  const keys = (c['keys.storePath'] || './keys').replace(/^\.\//, '');
  const key = vm.sshPrivateKeyPath || `${keys}/${vm.sshKeyName || vm.name + '-ssh'}`;
  return `ssh -i "${key}"${jump} ${vm.sshUser || 'ubuntu'}@${vm.ip}`;
}

function renderVMs() {
  const list = $('#vms-list');
  const on = vmCache.filter(v => v.status === 'running').length;
  const used = runningMemory();
  const over = hostInfo && used > hostInfo.memoryMB;
  $('#vm-summary').innerHTML = (vmCache.length
    ? `<span class="chip dim">${vmCache.length} ${vmCache.length === 1 ? 'VM' : 'VMs'}</span><span class="chip ok">${on} encendida${on === 1 ? '' : 's'}</span>`
    : '') + (hostInfo ? `<span class="chip ${over ? 'bad' : 'dim'}" title="Memoria de las VMs encendidas / memoria total del hipervisor">RAM ${(used / 1024).toFixed(1)} de ${(hostInfo.memoryMB / 1024).toFixed(1)} GB</span>` : '');
  $('#vm-mem-warning').hidden = !over;
  if (over) $('#vm-mem-warning').innerHTML = `<span>⚠</span><span>Las VMs encendidas piden <b>${(used / 1024).toFixed(1)} GB</b> y el hipervisor tiene <b>${(hostInfo.memoryMB / 1024).toFixed(1)} GB</b>. Todo irá lento y el agente QEMU tardará mucho en responder. Apague las VMs que no esté usando.</span>`;
  if (!vmCache.length) { list.innerHTML = '<p class="placeholder">No hay máquinas virtuales. Cree la primera con el formulario.</p>'; return; }
  list.innerHTML = vmCache.map(vm => {
    const [cls, label] = vmStateLabel(vm.status);
    const running = vm.status === 'running';
    const hint = running ? sshHint(vm) : '';
    return `<div class="item-card" data-vm="${esc(vm.name)}">
      <div class="item-top"><span class="item-name">${esc(vm.name)}</span><span class="vm-state ${cls}">${esc(label)}</span></div>
      <div class="item-meta">
        <span>${esc(DISTRO_NAMES[vm.distro] || vm.distro)}</span>
        <span>${vm.cpu || '?'} vCPU</span>
        <span>${vm.memoryMB ? vm.memoryMB + ' MB' : '? MB'}</span>
        <span>${vm.diskGB ? vm.diskGB + ' GB' : '— GB'}</span>
        ${running ? `<span>IP <b class="mono">${vm.ip ? esc(vm.ip) : 'esperando…'}</b></span>` : ''}
      </div>
      ${(vm.networks || []).length ? `<div class="item-meta">${vm.networks.map((n, i) => `<span class="chip ${i ? 'dim' : ''}" title="MAC ${esc(n.mac)}">${i ? '' : '★ '}${esc(n.network)}</span>`).join('')}</div>` : ''}
      ${hint ? `<div class="vm-ssh"><code title="Desde este equipo, saltando por el hipervisor">${esc(hint)}</code><button type="button" class="btn-small" data-act="copyssh">Copiar</button></div>` : ''}
      <div class="item-actions">
        ${running
          ? '<button type="button" class="btn-small" data-act="stop">Apagar</button>'
          : '<button type="button" class="btn-small go" data-act="start">Encender</button>'}
        <button type="button" class="btn-small" data-act="net"${running ? '' : ' disabled title="Encienda la VM: la red se lee por el canal del hipervisor"'}>Red</button>
        <button type="button" class="btn-small" data-act="disks" aria-expanded="false">Discos</button>
        <button type="button" class="btn-small" data-act="nics" aria-expanded="false">Tarjetas</button>
        <button type="button" class="btn-small" data-act="edit" aria-expanded="false">Editar</button>
        <span class="sep"></span>
        <button type="button" class="btn-small btn-danger" data-act="delete">Eliminar</button>
      </div>
      <form class="vm-edit" hidden>
        <div class="form-row">
          <div class="form-item"><label>Nombre</label><input name="name" value="${esc(vm.name)}" required spellcheck="false"></div>
          <div class="form-item"><label>vCPU</label><input name="cpu" type="number" min="1" max="64" value="${vm.cpu || 2}" required></div>
          <div class="form-item"><label>Memoria (MB)</label><input name="memoryMB" type="number" min="512" step="256" value="${vm.memoryMB || 2048}" required></div>
        </div>
        ${running ? '<small>⚠ Para aplicar los cambios la VM se apaga.</small>' : ''}
        <div class="btn-row"><button type="submit" class="btn-primary">Guardar</button><button type="button" class="btn-ghost" data-act="edit">Cancelar</button></div>
      </form>
      <div class="vm-disks-panel" hidden></div>
      <div class="vm-disks-panel vm-nics" hidden></div>
    </div>`;
  }).join('');
}

async function listVMs({ quiet = false } = {}) {
  const list = $('#vms-list');
  const btn = $('#vm-refresh');
  if (!quiet) busy(btn, true, 'Leyendo…');
  if (!configCache['vm.ssh.host']) await loadConfig();
  const [r, h] = await Promise.all([api('/api/vms'), api('/api/hypervisor')]);
  if (h.ok && h.data.success) hostInfo = h.data.data;
  if (!quiet) busy(btn, false);
  const ok = r.ok && r.data.success && Array.isArray(r.data.data);
  setHypervisorState(ok, ok ? r.data.data.length + ' VMs' : errMsg(r, ''));
  if (!ok) {
    list.innerHTML = `<div class="placeholder err"><b>No se pudo hablar con el hipervisor.</b><br>${esc(errMsg(r, 'error'))}<br><br>Revise que la VM del hipervisor esté encendida y los datos en <a href="#config" data-goto="config">Configuración → Hipervisor</a>.</div>`;
    return;
  }
  vmCache = r.data.data;
  renderVMs();
  scheduleVMRefresh();
}

// Mientras una VM encendida no tenga IP (primer arranque), se vuelve a
// consultar sola cada 20 s; si no, cada 60 s mientras la pestana este visible.
function scheduleVMRefresh() {
  clearTimeout(vmTimer);
  const pending = vmCache.some(v => v.status === 'running' && !v.ip);
  vmTimer = setTimeout(() => {
    const visible = !document.hidden && $('#vms-tab').classList.contains('active') && !$('#vm-pane-manage').hidden;
    const editing = $$('#vms-list .vm-edit:not([hidden]), #vms-list .vm-disks-panel:not([hidden])').length > 0;
    if (visible && !editing) listVMs({ quiet: true }); else scheduleVMRefresh();
  }, pending ? 20000 : 60000);
}

$('#vm-refresh').addEventListener('click', () => listVMs());
document.addEventListener('click', e => {
  const g = e.target.closest('[data-goto]');
  if (g) { e.preventDefault(); showTab(g.dataset.goto); }
});

$('#vms-list').addEventListener('click', async e => {
  const b = e.target.closest('button[data-act]');
  if (!b) return;
  const card = b.closest('[data-vm]');
  const name = card.dataset.vm;
  const vm = vmCache.find(v => v.name === name) || { name };
  const act = b.dataset.act;

  if (act === 'copyssh') return copyText(sshHint(vm), 'Comando SSH');
  if (act === 'net') return window.openNetwork(name);
  if (act === 'nics') return toggleNics(card, vm, b);
  if (act === 'nic-del') return detachNic(vm, b.dataset.mac, b.dataset.net);
  if (act === 'nic-add') return attachNic(card, vm, b);
  if (act === 'edit') {
    const form = $('.vm-edit', card);
    form.hidden = !form.hidden;
    $$('[data-act="edit"]', card)[0].setAttribute('aria-expanded', !form.hidden);
    if (!form.hidden) form.elements.cpu.focus();
    return;
  }
  if (act === 'disks') return toggleDisks(card, name, b);
  if (act === 'start') return vmAction(b, name, 'start', 'Encendiendo…', `VM «${name}» encendida.`);
  if (act === 'stop') {
    const yes = await ask({
      title: 'Apagar VM',
      html: `<p>Se apagará <b>${esc(name)}</b> de inmediato, como al desconectar la corriente. Guarde antes lo que tenga abierto dentro de la VM.</p>`,
      ok: 'Apagar', danger: true
    });
    if (yes) vmAction(b, name, 'stop', 'Apagando…', `VM «${name}» apagada.`);
    return;
  }
  if (act === 'delete') return deleteVM(name);
});

async function vmAction(btn, name, action, label, okText) {
  busy(btn, true, label);
  const r = await api('/api/vms/' + encodeURIComponent(name) + '/' + action, { method: 'POST' });
  busy(btn, false);
  if (r.ok && r.data.success) { toast('success', okText); listVMs({ quiet: true }); }
  else toast('error', errMsg(r, 'La operación falló'));
}

$('#vms-list').addEventListener('submit', async e => {
  const form = e.target.closest('.vm-edit');
  if (!form) return;
  e.preventDefault();
  const name = form.closest('[data-vm]').dataset.vm;
  const body = { name: form.elements.name.value.trim(), cpu: +form.elements.cpu.value, memoryMB: +form.elements.memoryMB.value };
  if (!VM_NAME_RE.test(body.name)) { toast('error', 'El nombre solo puede tener letras, números y guiones.'); return; }
  if (!(body.cpu >= 1) || !(body.memoryMB >= 512)) { toast('error', 'Mínimo 1 vCPU y 512 MB de memoria.'); return; }
  const btn = form.querySelector('[type="submit"]');
  busy(btn, true, 'Guardando…');
  const r = await api('/api/vms/' + encodeURIComponent(name), { method: 'PUT', body });
  busy(btn, false);
  if (r.ok && r.data.success) { toast('success', `VM «${body.name}» actualizada.`); listVMs({ quiet: true }); }
  else toast('error', 'No se actualizó: ' + errMsg(r, 'error'));
});

async function toggleDisks(card, name, btn) {
  const panel = $('.vm-disks-panel', card);
  if (!panel.hidden) { panel.hidden = true; btn.setAttribute('aria-expanded', 'false'); return; }
  panel.hidden = false;
  btn.setAttribute('aria-expanded', 'true');
  panel.innerHTML = '<small>Leyendo discos…</small>';
  const r = await api('/api/vms/' + encodeURIComponent(name) + '/disks');
  if (!r.ok || !r.data.success) { panel.innerHTML = `<p class="placeholder err">${esc(errMsg(r, 'No se pudieron leer los discos'))}</p>`; return; }
  const disks = r.data.data || [];
  panel.innerHTML = disks.length ? disks.map(d => `<div class="vm-disk-item">
      <b>${esc(d.name)}</b><span>${d.sizeGB || '?'} GB</span><span class="mono">${esc(d.path)}</span>
      ${d.readOnly ? '<span class="chip dim">solo lectura</span>' : ''}${d.shareable ? '<span class="chip warn">compartido</span>' : ''}
    </div>`).join('') : '<small>Sin discos.</small>';
}

// ---------- tarjetas de red de una VM ----------
function toggleNics(card, vm, btn) {
  const panel = $('.vm-nics', card);
  if (!panel.hidden) { panel.hidden = true; btn.setAttribute('aria-expanded', 'false'); return; }
  btn.setAttribute('aria-expanded', 'true');
  const nics = vm.networks || [];
  panel.innerHTML = `<div class="nic-list">${nics.length ? nics.map((n, i) => `<div class="nic-row">
      <b>${i === 0 ? 'Principal' : 'Tarjeta ' + (i + 1)}</b><span class="chip ${i ? 'dim' : ''}">${esc(n.network)}</span><span class="mono">${esc(n.mac)}</span>
      <span class="sep"></span><button type="button" class="btn-small btn-danger" data-act="nic-del" data-mac="${esc(n.mac)}" data-net="${esc(n.network)}">Quitar</button>
    </div>`).join('') : '<small>La VM no tiene tarjetas de red.</small>'}</div>
    <div class="nic-add"><select aria-label="Red para la nueva tarjeta">${vnetCache.map(n => `<option value="${esc(n.name)}">${esc(n.name)} · ${esc(NET_MODES[n.mode] || n.mode)} · ${esc(n.subnet || '')}</option>`).join('')}</select>
      <button type="button" class="btn-small go" data-act="nic-add">+ Agregar tarjeta</button></div>
    <small>${vm.status === 'running' ? '<b>Apague la VM</b> para agregar o quitar tarjetas: VirtualBox no lo permite con la VM encendida.' : 'La tarjeta aparecerá al encender la VM.'} Configure su IP en <b>Red</b>.</small>`;
  panel.hidden = false;
  if (!vnetCache.length) loadVirtualNetworks().then(() => { if (!panel.hidden) { panel.hidden = true; toggleNics(card, vm, btn); } });
}

async function attachNic(card, vm, btn) {
  const network = $('.vm-nics select', card).value;
  if (!network) return;
  busy(btn, true, 'Agregando…');
  const r = await api('/api/vms/' + encodeURIComponent(vm.name) + '/interfaces', { method: 'POST', body: { network } });
  busy(btn, false);
  if (r.ok && r.data.success) { toast('success', `Tarjeta en «${network}» agregada a «${vm.name}».`); listVMs({ quiet: true }); }
  else toast('error', 'No se agregó: ' + errMsg(r, 'error'));
}

async function detachNic(vm, mac, network) {
  const only = (vm.networks || []).length === 1;
  const yes = await ask({
    title: 'Quitar tarjeta de red',
    html: `<p>Se quitará de <b>${esc(vm.name)}</b> la tarjeta <b>${esc(mac)}</b> (red «${esc(network)}»).</p>` +
      (only ? '<p><b>Es su única tarjeta:</b> la VM quedará sin red. El canal del hipervisor (Guest Additions) sigue funcionando porque no usa la red.</p>' : ''),
    ok: 'Quitar', danger: true
  });
  if (!yes) return;
  const r = await api('/api/vms/' + encodeURIComponent(vm.name) + '/interfaces/' + encodeURIComponent(mac), { method: 'DELETE' });
  if (r.ok && r.data.success) { toast('success', 'Tarjeta quitada.'); listVMs({ quiet: true }); }
  else toast('error', 'No se quitó: ' + errMsg(r, 'error'));
}

// ---------- redes virtuales (switches de libvirt) ----------
async function loadVirtualNetworks() {
  const r = await api('/api/networks');
  if (!r.ok || !r.data.success) {
    $('#vnet-list').innerHTML = `<p class="placeholder err">No se pudieron leer las redes: ${esc(errMsg(r, 'error'))}</p>`;
    return;
  }
  vnetCache = r.data.data || [];
  renderVirtualNetworks();
  fillNetworkSelects();
}
window.loadVirtualNetworks = loadVirtualNetworks;

function renderVirtualNetworks() {
  const list = $('#vnet-list');
  if (!vnetCache.length) { list.innerHTML = '<p class="placeholder">No hay redes en el hipervisor.</p>'; return; }
  list.innerHTML = vnetCache.map(n => `<div class="item-card" data-net="${esc(n.name)}">
    <div class="item-top"><span class="item-name">${esc(n.name)}</span>
      <span><span class="chip ${n.mode === 'nat' ? 'ok' : n.mode === 'isolated' ? 'warn' : ''}">${esc(NET_MODES[n.mode] || n.mode || '?')}</span> ${n.active ? '<span class="chip dim">activa</span>' : '<span class="chip bad">detenida</span>'}</span></div>
    <dl class="vnet-facts">
      <dt>Subred</dt><dd>${esc(n.subnet || '—')}</dd>
      <dt>Hipervisor (puerta de enlace)</dt><dd>${esc(n.hostIP || '—')}</dd>
      <dt>DHCP</dt><dd>${n.dhcpStart ? esc(n.dhcpStart + ' – ' + n.dhcpEnd) : 'no (IPs fijas)'}</dd>
      <dt>Bridge</dt><dd>${esc(n.bridge || '—')}</dd>
      <dt>VMs conectadas</dt><dd>${n.vms.length ? n.vms.map(esc).join(', ') : 'ninguna'}</dd>
    </dl>
    <div class="item-actions"><span class="sep"></span>${n.name === 'default' ? '<small>Red por defecto: no se elimina</small>' : `<button type="button" class="btn-small btn-danger" data-act="vnet-del"${n.vms.length ? ' disabled title="Quite primero las tarjetas de las VMs conectadas"' : ''}>Eliminar</button>`}</div>
  </div>`).join('');
}

function fillNetworkSelects() {
  const main = $('#vm-net-main');
  const current = main.value || 'default';
  const nat = vnetCache.filter(n => n.mode === 'nat');
  main.innerHTML = (nat.length ? nat : [{ name: 'default', mode: 'nat' }]).map(n => `<option value="${esc(n.name)}">${esc(n.name)}${n.subnet ? ' · ' + esc(n.subnet) : ''}</option>`).join('');
  if ([...main.options].some(o => o.value === current)) main.value = current;
  const syncExtra = () => {
    const others = vnetCache.filter(n => n.name !== main.value);
    const checked = new Set($$('#vm-net-extra input:checked').map(i => i.value));
    $('#vm-net-extra').innerHTML = others.length
      ? others.map(n => `<label><input type="checkbox" value="${esc(n.name)}"${checked.has(n.name) ? ' checked' : ''}> ${esc(n.name)} <span class="chip dim">${esc(NET_MODES[n.mode] || n.mode)}</span></label>`).join('')
      : '<small>No hay otras redes. Créelas en <a href="#" data-vmtab-go="nets">Redes virtuales</a>.</small>';
  };
  main.onchange = syncExtra;
  syncExtra();
}

function vnetPreview() {
  const subnet = $('#vnet-subnet').value.trim();
  const m = /^(\d+)\.(\d+)\.(\d+)\.(\d+)\/(\d+)$/.exec(subnet);
  if (!m || +m[5] < 16 || +m[5] > 28) { $('#vnet-preview').textContent = 'Escriba la subred como 192.168.100.0/24 (de /16 a /28).'; return; }
  const base = ((+m[1] << 24) >>> 0) + (+m[2] << 16) + (+m[3] << 8) + +m[4];
  const size = 2 ** (32 - +m[5]);
  const ip = n => [n >>> 24, (n >>> 16) & 255, (n >>> 8) & 255, n & 255].join('.');
  $('#vnet-preview').textContent = `Hipervisor: ${ip(base + 1)} · ` +
    ($('#vnet-dhcp').checked ? `DHCP: ${ip(base + size / 2)} – ${ip(base + size - 2)}` : 'sin DHCP') + ` · broadcast ${ip(base + size - 1)}`;
}
['#vnet-subnet', '#vnet-dhcp'].forEach(sel => $(sel).addEventListener('input', vnetPreview));
vnetPreview();
$('#vnet-refresh').addEventListener('click', () => loadVirtualNetworks());

$('#vnet-form').addEventListener('submit', async e => {
  e.preventDefault();
  const statusEl = $('#vnet-status');
  const body = { name: $('#vnet-name').value.trim(), mode: $('input[name="vnet-mode"]:checked').value, subnet: $('#vnet-subnet').value.trim(), dhcp: $('#vnet-dhcp').checked };
  if (!VM_NAME_RE.test(body.name)) { statusBox(statusEl, 'error', 'El nombre solo puede tener letras, números y guiones.'); return; }
  const btn = e.submitter;
  busy(btn, true, 'Creando…');
  const r = await api('/api/networks', { method: 'POST', body });
  busy(btn, false);
  if (r.ok && r.data.success) {
    statusBox(statusEl, 'success', `Red «${body.name}» creada y activa.`);
    $('#vnet-name').value = '';
    loadVirtualNetworks();
  } else statusBox(statusEl, 'error', 'No se creó: ' + errMsg(r, 'error'), 0);
});

$('#vnet-list').addEventListener('click', async e => {
  const b = e.target.closest('[data-act="vnet-del"]');
  if (!b) return;
  const name = b.closest('[data-net]').dataset.net;
  const yes = await ask({ title: 'Eliminar red virtual', html: `<p>Se apagará y borrará la red <b>${esc(name)}</b> del hipervisor.</p>`, ok: 'Eliminar', danger: true });
  if (!yes) return;
  const r = await api('/api/networks/' + encodeURIComponent(name), { method: 'DELETE' });
  if (r.ok && r.data.success) { toast('success', `Red «${name}» eliminada.`); loadVirtualNetworks(); }
  else toast('error', 'No se eliminó: ' + errMsg(r, 'error'));
});

document.addEventListener('click', e => {
  const go = e.target.closest('[data-vmtab-go]');
  if (go) { e.preventDefault(); window.showVmTab(go.dataset.vmtabGo); }
});

async function deleteVM(name) {
  const yes = await ask({
    title: 'Eliminar VM',
    html: `<p>Se eliminará <b>${esc(name)}</b>, su disco del sistema y su llave <b>${esc(name)}-ssh</b>. Los discos <b>compartidos</b> se conservan. No se puede deshacer.</p>`,
    ok: 'Eliminar VM', danger: true, confirmText: name
  });
  if (!yes) return;
  toast('info', `Eliminando «${name}»…`);
  const r = await api('/api/vms/' + encodeURIComponent(name), { method: 'DELETE' });
  if (r.ok && r.data.success) {
    toast(r.data.warning ? 'warning' : 'success', r.data.warning ? `VM eliminada. ${r.data.warning}` : `VM «${name}» eliminada.`, r.data.warning ? 14000 : 0);
    listVMs({ quiet: true });
    loadTemplates();
  } else toast('error', 'No se eliminó: ' + errMsg(r, 'error'));
}

// ---------- formulario de creacion ----------
function distroFromTemplate(path) {
  const n = (path || '').toLowerCase();
  if (n.includes('ubuntu')) return 'ubuntu-24.04';
  if (n.includes('debian-13') || n.includes('trixie')) return 'debian-13';
  if (n.includes('debian')) return 'debian-12';
  if (n.includes('fedora')) return 'fedora-42';
  if (n.includes('arch')) return 'arch';
  if (n.includes('rocky') || n.includes('rhel')) return 'rocky-9';
  return '';
}
function syncDistro() {
  const d = distroFromTemplate($('#vm-template').value);
  const sel = $('#vm-distro');
  if (d) sel.value = d;
  sel.disabled = !!d;
  // El usuario sugerido sigue a la distribucion mientras no se haya editado a mano.
  const user = $('#vm-user');
  if (!user.dataset.touched) user.value = DISTRO_USERS[sel.value] || 'ubuntu';
  $('#vm-distro-help').textContent = d ? 'Detectada por el nombre de la plantilla.' : 'No se reconoce la plantilla: elija la distribución.';
  syncEmulated();
}
// Distribuciones que con KVM tumban el hipervisor (VT-x anidado de VirtualBox):
// se proponen emuladas mientras no se haya cambiado la casilla a mano.
const EMULATED_DISTROS = ['debian-12', 'debian-13'];
function syncEmulated() {
  const box = $('#vm-emulated');
  if (!box.dataset.touched) box.checked = EMULATED_DISTROS.includes($('#vm-distro').value);
}
$('#vm-template').addEventListener('change', syncDistro);
$('#vm-distro').addEventListener('change', () => { if (!$('#vm-user').dataset.touched) $('#vm-user').value = DISTRO_USERS[$('#vm-distro').value] || 'ubuntu'; syncEmulated(); });
$('#vm-emulated').addEventListener('change', () => { $('#vm-emulated').dataset.touched = '1'; });
$('#vm-user').addEventListener('input', () => { $('#vm-user').dataset.touched = '1'; });

async function loadTemplates() {
  const select = $('#vm-template');
  const r = await api('/api/templates');
  if (!r.ok || !r.data.success) { select.innerHTML = '<option value="">No se pudieron cargar las plantillas</option>'; return; }
  const all = r.data.data || [];
  const list = EXPERIMENTAL ? all : all.filter(t => VERIFIED_TEMPLATE.test(t.name));
  select.innerHTML = list.length
    ? list.map(t => `<option value="${esc(t.path)}">${esc(t.name)} (${esc(t.format)})</option>`).join('')
    : '<option value="">No hay plantillas en /var/lib/libvirt/templates</option>';
  syncDistro();
}

$('#vm-presets').addEventListener('click', e => {
  const b = e.target.closest('button');
  if (!b) return;
  $('#vm-cpu').value = b.dataset.cpu;
  $('#vm-memory').value = b.dataset.mem;
  $('#vm-disk').value = b.dataset.disk;
  syncPresets();
});
function syncPresets() {
  $$('#vm-presets button').forEach(b => b.setAttribute('aria-pressed',
    b.dataset.cpu === $('#vm-cpu').value && b.dataset.mem === $('#vm-memory').value && b.dataset.disk === $('#vm-disk').value));
}
['#vm-cpu', '#vm-memory', '#vm-disk'].forEach(s => $(s).addEventListener('input', syncPresets));

function addDiskRow() {
  const row = document.createElement('div');
  row.className = 'vm-disk-row';
  row.innerHTML = `
    <input class="vm-disk-name" type="text" placeholder="datos" aria-label="Nombre del disco" spellcheck="false">
    <input class="vm-disk-size" type="number" min="1" max="1024" value="10" aria-label="Tamaño en GB">
    <label><input type="checkbox" class="vm-disk-readonly"> Solo lectura</label>
    <label><input type="checkbox" class="vm-disk-shareable"> Compartido</label>
    <button type="button" class="btn-small btn-danger" aria-label="Quitar disco">Quitar</button>`;
  row.querySelector('button').addEventListener('click', () => { row.remove(); countDisks(); });
  $('#vm-disks').appendChild(row);
  row.querySelector('input').focus();
  countDisks();
}
function countDisks() { $('#vm-disks-count').textContent = $$('#vm-disks .vm-disk-row').length; }
$('#vm-add-disk').addEventListener('click', addDiskRow);

$('#vm-create-form').addEventListener('submit', async e => {
  e.preventDefault();
  const statusEl = $('#vm-create-status');
  const name = $('#vm-name').value.trim();
  const payload = {
    name,
    distro: $('#vm-distro').value,
    cpu: +$('#vm-cpu').value,
    memoryMB: +$('#vm-memory').value,
    diskGB: +$('#vm-disk').value,
    sshUser: $('#vm-user').value.trim() || 'ubuntu',
    imagePath: $('#vm-template').value,
    networks: EXPERIMENTAL ? [$('#vm-net-main').value || 'default', ...$$('#vm-net-extra input:checked').map(i => i.value)] : ['default'],
    emulated: $('#vm-emulated').checked,
    disks: $$('#vm-disks .vm-disk-row').map(row => ({
      name: row.querySelector('.vm-disk-name').value.trim(),
      sizeGB: +row.querySelector('.vm-disk-size').value,
      readOnly: row.querySelector('.vm-disk-readonly').checked,
      shareable: row.querySelector('.vm-disk-shareable').checked
    }))
  };

  const problems = [];
  if (!VM_NAME_RE.test(name)) problems.push('el nombre solo puede tener letras, números y guiones');
  if (vmCache.some(v => v.name === name)) problems.push('ya existe una VM con ese nombre');
  if (!payload.imagePath) problems.push('no hay plantilla seleccionada');
  const mainNet = vnetCache.find(n => n.name === payload.networks[0]);
  if (mainNet && mainNet.mode !== 'nat') problems.push(`la tarjeta principal está en «${mainNet.name}» (${NET_MODES[mainNet.mode] || mainNet.mode}), sin internet: el primer arranque no podría instalar el agente QEMU. Use una red NAT como principal`);
  if (!(payload.cpu >= 1)) problems.push('mínimo 1 vCPU');
  if (!(payload.memoryMB >= 512)) problems.push('mínimo 512 MB de memoria');
  if (!(payload.diskGB >= 5)) problems.push('mínimo 5 GB de disco');
  if (payload.disks.some(d => d.name && !VM_NAME_RE.test(d.name))) problems.push('los nombres de disco solo admiten letras, números y guiones');
  if (problems.length) { statusBox(statusEl, 'error', 'Revise: ' + problems.join('; ') + '.', 0); return; }
  if (hostInfo && runningMemory() + payload.memoryMB > hostInfo.memoryMB) {
    const go = await ask({
      title: 'Memoria insuficiente en el hipervisor',
      html: `<p>Las VMs encendidas ya piden <b>${runningMemory()} MB</b> y esta pediría <b>${payload.memoryMB} MB</b> más, pero el hipervisor tiene <b>${hostInfo.memoryMB} MB</b>.</p><p>La VM se creará, pero todo irá lento y su primer arranque puede tardar mucho. Lo mejor es apagar antes las VMs que no use.</p>`,
      ok: 'Crear de todos modos'
    });
    if (!go) return;
  }

  const btn = $('#vm-create-btn');
  busy(btn, true, 'Creando VM… (unos 30 s)');
  statusBox(statusEl, 'info', 'Creando el disco derivado y definiendo la VM en libvirt…', 0);
  const r = await api('/api/vms', { method: 'POST', body: payload });
  busy(btn, false);
  if (r.ok && r.data.success) {
    const keyName = r.data.data?.sshKeyName || name + '-ssh';
    statusBox(statusEl, r.data.warning ? 'warning' : 'success',
      `VM «${name}» creada y encendida. Su llave es «${keyName}». En 3–4 minutos termina el primer arranque y aparece su IP.` + (r.data.warning ? ' Advertencia: ' + r.data.warning : ''), 20000);
    $('#vm-create-form').reset();
    delete $('#vm-user').dataset.touched;
    $('#vm-disks').innerHTML = '';
    countDisks();
    syncPresets();
    loadTemplates();
    listVMs({ quiet: true });
  } else {
    statusBox(statusEl, 'error', 'No se creó: ' + errMsg(r, 'error'), 0);
  }
});

// ============== INICIALIZACIÓN ==============
window.listVMs = listVMs;
window.ask = ask;
window.toast = toast;
window.addEventListener('DOMContentLoaded', async () => {
  await loadConfig();
  loadTemplates();
  if (EXPERIMENTAL) loadVirtualNetworks();
  // Enlace directo: #vms/red/<vm> abre el panel de Red de esa VM.
  const [start, sub, vmName] = decodeURIComponent((location.hash || '').slice(1) || store.get('tab', 'vms')).split('/');
  showTab(start);
  if (start !== 'vms') listVMs({ quiet: true }); // la pastilla del hipervisor siempre se actualiza
  if (start === 'vms' && sub === 'red' && vmName) window.openNetwork(vmName);
  else if (start === 'vms' && sub === 'docs') window.showVmTab('docs');
  window.addEventListener('hashchange', () => showTab(location.hash.slice(1).split('/')[0]));
});
