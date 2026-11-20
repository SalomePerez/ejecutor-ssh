// ============== ENRUTAMIENTO Y DIAGNÓSTICO DE UNA VM ==============
// Tres secciones, todas por el canal elegido (interno o de gestión):
//   Rutas        la tabla de rutas real de la VM, interpretada (GET .../network/routing)
//   Reenvío IPv4 estado y activar/desactivar (POST .../network/forwarding)
//   Comandos     catálogo de consulta y diagnóstico (POST .../network/cmd)
// Las direcciones de cada tarjeta NO se cambian aquí: eso es «Configuración de red».

(function () {
  const $ = s => document.querySelector(s);
  const esc = s => String(s == null ? '' : s).replace(/[&<>"']/g, c => ({ '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#39;' }[c]));
  const MAN = 'https://manpages.debian.org/';
  const man = (page, sec) => `<a href="${MAN}${page}.${sec}" target="_blank" rel="noopener"><code>${page}(${sec})</code></a>`;
  const RFC = (n, txt) => `<a href="https://www.rfc-editor.org/rfc/rfc${n}" target="_blank" rel="noopener">${txt || 'RFC ' + n}</a>`;

  const S = { vm: '', channel: '', tab: 'routes', cmd: 'addr', catalog: [], filters: [], busy: false, ifaces: [], routing: null, f: { target: '', iface: 'any', count: 10, filter: '' } };
  // Los que ya tienen su propia sección (Rutas) no se repiten en Comandos.
  const IN_ROUTES = ['route', 'routeget'];

  // ---------- textos ----------
  const DESC = {
    addr: 'Las interfaces de la VM, su estado, su MAC y sus direcciones IP.',
    neigh: 'Los equipos de la red local que la VM ya conoce: su IP y su MAC.',
    arp: 'La misma tabla de vecinos, con la herramienta antigua.',
    ss: 'Qué servicios esperan conexiones y en qué puerto.',
    conns: 'Todas las conexiones TCP abiertas y su estado.',
    netstat: 'Las conexiones TCP, con la herramienta antigua.',
    ping: '¿Responde el destino? Además dice por dónde sale el paquete.',
    tracepath: 'Por qué routers pasa el paquete hasta llegar al destino.',
    tcpdump: 'Los paquetes que pasan por una interfaz, uno por línea.'
  };
  const LEARN = {
    addr: `<p><code>ip addr</code> (paquete <b>iproute2</b>) lista cada interfaz con su estado (<code>UP</code>/<code>DOWN</code>), su MAC (<code>link/ether</code>), su MTU y sus direcciones <code>inet</code> con el prefijo (<code>/24</code>). La palabra <code>dynamic</code> indica que la dirección llegó por DHCP; sin ella, es fija.</p><p><code>ip -br addr</code> resume todo en una línea por interfaz. Reemplaza a <code>ifconfig</code> (net-tools). Manual: ${man('ip-address', 8)}.</p>`,
    neigh: `<p>La tabla de vecinos es la caché ARP ${RFC(826)}: para cada IP de la red local, la MAC que le respondió (<code>lladdr</code>) y el estado (<code>REACHABLE</code>, <code>STALE</code>, <code>FAILED</code>).</p><p>Solo aparecen los equipos con los que la VM ya habló: si está vacía, haga primero un ping. <code>FAILED</code> significa que nadie respondió a la pregunta ARP: la IP no existe o está en otra red. Es la primera pista cuando una puerta de enlace no responde. Manual: ${man('ip-neighbour', 8)}.</p>`,
    arp: `<p><code>arp -a</code> muestra la misma tabla que <code>ip neigh</code>, con la herramienta antigua del paquete <b>net-tools</b>, que muchas distribuciones ya no instalan. Hoy se recomienda <code>ip neigh</code>. Manual: ${man('arp', 8)}.</p>`,
    ss: `<p><code>ss -tulnp</code>: <code>-t</code> TCP, <code>-u</code> UDP, <code>-l</code> solo los que escuchan, <code>-n</code> números en vez de nombres, <code>-p</code> el proceso dueño.</p><p>En <i>Local Address:Port</i>, <code>0.0.0.0:22</code> es un servicio (sshd) que escucha en todas las interfaces; <code>127.0.0.1:…</code> solo se alcanza desde la misma VM. Manual: ${man('ss', 8)}.</p>`,
    conns: `<p><code>ss -tan</code> lista todas las conexiones TCP y su estado: <code>LISTEN</code> (esperando), <code>ESTAB</code> (abierta), <code>TIME-WAIT</code> (recién cerrada). Si usa el canal de gestión, aquí verá la propia conexión SSH de la app. Manual: ${man('ss', 8)}.</p>`,
    netstat: `<p><code>netstat -ant</code> muestra lo mismo que <code>ss -tan</code> con la herramienta clásica (<b>net-tools</b>). Está en desuso: <code>ss</code>, de iproute2, la reemplaza y lee la información directamente del kernel. Manual: ${man('netstat', 8)}.</p>`,
    ping: `<p><code>ping</code> envía mensajes ICMP <i>echo request</i> ${RFC(792)} y espera el <i>echo reply</i>. <code>time</code> es el tiempo de ida y vuelta; la última línea cuenta los paquetes perdidos.</p><p>La app agrega <code>ip route get</code>, que dice por qué interfaz y puerta de enlace saldría el paquete. Si falla, revise esa ruta, que el router tenga el reenvío IPv4 activo y que el destino tenga ruta de regreso. Manual: ${man('ping', 8)}.</p>`,
    tracepath: `<p><code>tracepath</code> muestra cada router por el que pasa el paquete (un salto por línea) y el MTU del camino. A diferencia de <code>traceroute</code>, no necesita privilegios. Si una VM hace de router entre dos redes, su IP debe aparecer como primer salto. Manual: ${man('tracepath', 8)}.</p>`,
    tcpdump: `<p><code>tcpdump</code> captura los paquetes de una interfaz: <code>-i</code> la interfaz (<code>any</code> = todas), <code>-n</code> sin resolver nombres, <code>-c</code> cuántos paquetes, <code>-l</code> línea a línea. La app corta la captura a los 15 s.</p><p>Para ver algo, provoque tráfico mientras captura (por ejemplo, un ping desde otra VM). Con el canal de gestión use el filtro <i>todo menos SSH</i>, o verá sobre todo la conexión de la propia app. Manual: ${man('tcpdump', 8)}.</p>`
  };
  const FILTERS = { '': ['todo el tráfico', ''], icmp: ['solo ICMP (ping)', 'icmp'], arp: ['solo ARP', 'arp'], ssh: ['solo SSH (puerto 22)', 'tcp port 22'], dns: ['solo DNS (puerto 53)', 'port 53'], dhcp: ['solo DHCP', 'port 67 or port 68'], 'no-ssh': ['todo menos SSH', 'not tcp port 22'] };
  const PROTO = { kernel: 'kernel', dhcp: 'DHCP', static: 'configurada', boot: 'manual', ra: 'router IPv6' };
  const PROTO_TIP = { kernel: 'la crea el kernel al tener una IP en esa red', dhcp: 'la entregó el servidor DHCP', static: 'la escribió el gestor de red a partir de su archivo', boot: 'se agregó a mano o con un script (ip route add)' };

  // La línea exacta, igual a la que arma el servidor (mv/network_cmds.go).
  function cmdLine(id) {
    const t = S.f.target || '<destino>', i = S.f.iface || '<interfaz>', f = (FILTERS[S.f.filter] || FILTERS[''])[1];
    return {
      addr: 'ip -br addr ; ip addr', neigh: 'ip neigh', arp: 'arp -a', ss: 'ss -tulnp', conns: 'ss -tan', netstat: 'netstat -ant',
      ping: `ping -c 4 -W 1 ${t} ; ip route get ${t}`, tracepath: `tracepath -n -m 8 ${t}`,
      tcpdump: `tcpdump -i ${i} -n -l -c ${S.f.count || 10}${f ? " '" + f + "'" : ''}`
    }[id] || '';
  }

  // ---------- datos ----------
  async function loadCatalog() {
    if (S.catalog.length) return;
    try {
      const d = await (await fetch('/api/netcmds')).json();
      if (d.success) { S.catalog = d.data.commands.filter(c => !IN_ROUTES.includes(c.id)); S.filters = d.data.filters; }
    } catch (_) { /* queda vacio */ }
  }
  const qs = () => (S.channel ? '?canal=' + S.channel : '');
  async function post(path, body) {
    try {
      const r = await fetch('/api/vms/' + encodeURIComponent(S.vm) + path + qs(), { method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify(body) });
      return await r.json();
    } catch (err) { return { success: false, error: err.message }; }
  }
  const terminal = lines => lines.map(l => `<span class="k2">$ ${esc(l.cmd)}</span>\n` + (l.note ? `<span class="nt"># ${esc(l.note)}</span>\n` : '') +
    `<span class="${l.error ? 'er' : l.warn ? 'wr' : 'o'}">${esc(l.output || '(sin salida)')}</span>`).join('\n\n');

  async function renderVmList() {
    const box = $('#nc-vmlist');
    try {
      const data = await (await fetch('/api/vms')).json();
      if (!data.success || !Array.isArray(data.data) || !data.data.length) {
        box.innerHTML = '<p class="placeholder">' + (data.success ? 'No hay máquinas virtuales' : esc(data.error || 'No se pudieron cargar')) + '</p>';
        return;
      }
      box.innerHTML = data.data.map(v => {
        const on = v.status === 'running';
        return `<button type="button" class="net-vm" data-vm="${esc(v.name)}" aria-pressed="${v.name === S.vm}"${on && !S.busy ? '' : ' disabled'}>` +
          `<span class="n"><span class="st${on ? ' on' : ''}"></span>${esc(v.name)}</span><span class="m">${on ? 'encendida' : 'apagada'}${v.ip ? ' · ' + esc(v.ip) : ''}</span></button>`;
      }).join('');
    } catch (err) {
      box.innerHTML = '<p class="placeholder">Error: ' + esc(err.message) + '</p>';
    }
  }

  // ---------- secciones ----------
  function showTab(t) {
    S.tab = t;
    document.querySelectorAll('.nc-tabs [data-ntab]').forEach(b => b.setAttribute('aria-selected', b.dataset.ntab === t));
    $('#nc-empty').hidden = !!S.vm;
    ['routes', 'fwd', 'cmds'].forEach(k => { $('#nc-p-' + k).hidden = !S.vm || k !== t; });
    if (S.vm && (t === 'routes' || t === 'fwd') && !S.routing) loadRouting();
    if (t === 'cmds') renderCmds();
  }

  // ---------- rutas ----------
  async function loadRouting() {
    const vm = S.vm;
    $('#nc-r-summary').innerHTML = '<span class="spinner"></span> Leyendo la tabla de rutas…';
    $('#nc-f-title').textContent = 'Leyendo…';
    let data;
    try { data = await (await fetch('/api/vms/' + encodeURIComponent(vm) + '/network/routing' + qs())).json(); }
    catch (err) { data = { success: false, error: err.message }; }
    if (vm !== S.vm) return;
    if (!data.success) {
      S.routing = null;
      $('#nc-r-summary').innerHTML = `<span class="err">No se pudo leer: ${esc(data.error || 'error')}</span>`;
      $('#nc-r-table tbody').innerHTML = '';
      $('#nc-f-title').textContent = 'No se pudo leer el estado';
      $('#nc-f-text').textContent = data.error || '';
      return;
    }
    S.routing = data.data;
    renderRoutes();
    renderFwd();
  }

  function renderRoutes() {
    const R = S.routing;
    $('#nc-r-summary').textContent = R.summary;
    $('#nc-r-time').textContent = 'leída a las ' + new Date(R.readAt).toLocaleTimeString();
    $('#nc-r-warn').hidden = !R.warning;
    if (R.warning) $('#nc-r-warn').innerHTML = `<span>⚠</span><span>${esc(R.warning)}</span>`;
    const tag = r => r.used ? '<span class="net-chip">en uso</span>'
      : r.kind === 'default' ? '<span class="net-chip warn">respaldo</span>'
      : r.kind === 'connected' ? '<span class="net-chip dim">red conectada</span>'
      : r.kind === 'via' ? '<span class="net-chip dim">por puerta de enlace</span>'
      : `<span class="net-chip warn">${esc(r.type)}</span>`;
    $('#nc-r-table tbody').innerHTML = R.routes.length ? R.routes.map(r => `<tr class="${r.kind}${r.used ? ' used' : ''}">
      <td data-l="Destino"><code>${r.kind === 'default' ? 'default' : esc(r.dest)}</code>${r.kind === 'default' ? '<small>todo lo demás (0.0.0.0/0)</small>' : ''}</td>
      <td data-l="Puerta de enlace">${r.via ? '<code>' + esc(r.via) + '</code>' : '<small>directa</small>'}</td>
      <td data-l="Interfaz"><code>${esc(r.dev || '—')}</code></td>
      <td data-l="Origen" title="${esc(PROTO_TIP[r.proto] || '')}">${esc(PROTO[r.proto] || r.proto || '—')}</td>
      <td data-l="Métrica">${r.metric}</td>
      <td>${tag(r)}</td></tr>`).join('') : '<tr><td colspan="6"><small>La tabla está vacía.</small></td></tr>';
    $('#nc-r-raw').textContent = R.routes.map(r => r.raw).join('\n') || '(vacía)';
  }

  async function probe(e) {
    e.preventDefault();
    const target = $('#nc-r-target').value.trim();
    const out = $('#nc-r-probe-out');
    if (!target || !S.vm) return;
    out.hidden = false;
    out.innerHTML = '<span class="nt"># consultando…</span>';
    const d = await post('/network/cmd', { cmd: 'routeget', target });
    const lines = d.data && d.data.steps ? d.data.steps.flatMap(s => s.lines || []) : [];
    out.innerHTML = lines.length ? terminal(lines) : `<span class="er">${esc(d.error || 'error')}</span>`;
    // Resalta en la tabla la ruta que escogió el kernel.
    const m = lines.length && /(?:via (\S+) )?dev (\S+)/.exec(lines[0].output || '');
    document.querySelectorAll('#nc-r-table tbody tr').forEach(tr => tr.classList.remove('hit'));
    if (m && S.routing) {
      const k = S.routing.routes.findIndex(r => r.dev === m[2] && (r.via || '') === (m[1] || '') && (r.kind !== 'default' || r.used));
      const tr = document.querySelectorAll('#nc-r-table tbody tr')[k];
      if (tr) tr.classList.add('hit');
    }
  }

  // ---------- reenvío ----------
  function renderFwd() {
    const on = S.routing && S.routing.forwarding;
    $('#nc-f-card').className = 'nc-fwd ' + (on ? 'on' : 'off');
    $('#nc-f-title').textContent = on ? 'Reenvío IPv4 activo' : 'Reenvío IPv4 desactivado';
    $('#nc-f-text').textContent = on
      ? S.vm + ' pasa paquetes de una interfaz a otra: funciona como router entre sus redes.'
      : S.vm + ' solo atiende los paquetes dirigidos a ella; no pasa paquetes entre sus redes.';
    const b = $('#nc-f-toggle');
    b.textContent = on ? 'Desactivar reenvío' : 'Activar reenvío (router)';
    b.className = on ? 'btn-secondary' : 'btn-primary';
    b.disabled = S.busy || !S.routing;
  }

  async function toggleFwd() {
    if (!S.routing || S.busy) return;
    const enabled = !S.routing.forwarding;
    S.busy = true;
    const b = $('#nc-f-toggle');
    b.disabled = true;
    b.textContent = enabled ? 'Activando…' : 'Desactivando…';
    const d = await post('/network/forwarding', { enabled });
    S.busy = false;
    const res = d.data && d.data.steps ? d.data : null;
    $('#nc-f-run').hidden = false;
    $('#nc-f-log').innerHTML = res ? terminal(res.steps.flatMap(s => s.lines || [])) : '';
    $('#nc-f-result').innerHTML = `<div class="net-result ${d.success ? 'ok' : 'bad'}">${esc(d.message || d.error || 'error')}</div>`;
    await loadRouting();
  }

  // ---------- comandos ----------
  function current() { return S.catalog.find(c => c.id === S.cmd) || S.catalog[0]; }

  function renderCmds() {
    const c = current();
    if (!c) { $('#nc-catalog').innerHTML = '<p class="placeholder">No se pudo cargar el catálogo.</p>'; return; }
    S.cmd = c.id;
    const groups = [];
    S.catalog.forEach(x => { let g = groups.find(y => y.name === x.group); if (!g) groups.push(g = { name: x.group, items: [] }); g.items.push(x); });
    $('#nc-catalog').innerHTML = groups.map(g => `<div class="nc-group"><h4>${esc(g.name)}</h4>` +
      g.items.map(x => `<button type="button" class="nc-cmd" data-cmd="${esc(x.id)}" aria-pressed="${x.id === S.cmd}"><b>${esc(x.title)}</b><code>${esc(x.example)}</code></button>`).join('') + '</div>').join('');
    $('#nc-c-title').textContent = c.title;
    $('#nc-c-pkg').innerHTML = `<span class="net-chip ${c.package === 'net-tools' ? 'warn' : 'dim'}" title="${c.package === 'net-tools' ? 'Herramienta antigua: muchas distribuciones ya no la instalan' : 'Paquete que trae el comando'}">${esc(c.package)}</span>`;
    $('#nc-c-desc').textContent = DESC[c.id] || '';
    renderParams();
    const l = $('#nc-learn');
    l.querySelector('.net-ls').textContent = c.title.toLowerCase();
    l.querySelector('.net-lb').innerHTML = LEARN[c.id] || '';
  }

  function renderParams() {
    const c = current();
    const needs = c.needs || [];
    const p = [];
    if (needs.includes('target')) {
      p.push(`<label class="nc-param">Destino <input id="nc-target" value="${esc(S.f.target)}" placeholder="una IP o un nombre" autocomplete="off" spellcheck="false"></label>`);
    }
    if (needs.includes('iface')) {
      p.push(`<label class="nc-param">Interfaz <input id="nc-iface" list="nc-ifaces" value="${esc(S.f.iface)}" autocomplete="off" spellcheck="false">` +
        `<datalist id="nc-ifaces"><option value="any">${S.ifaces.map(i => `<option value="${esc(i)}">`).join('')}</datalist></label>`);
      p.push(`<label class="nc-param">Paquetes <input id="nc-count" type="number" min="1" max="50" value="${S.f.count}"></label>`);
      p.push(`<label class="nc-param">Filtro <select id="nc-filter">${S.filters.map(f => `<option value="${esc(f)}"${f === S.f.filter ? ' selected' : ''}>${esc((FILTERS[f] || [f])[0])}</option>`).join('')}</select></label>`);
    }
    $('#nc-params').innerHTML = p.join('') + `<button type="submit" class="btn-primary" id="nc-run"${S.busy ? ' disabled' : ''}>${S.busy ? 'Ejecutando…' : 'Ejecutar'}</button>`;
    $('#nc-c-line').textContent = cmdLine(c.id);
  }

  function readParams() {
    const v = id => { const el = $(id); return el ? el.value : undefined; };
    if (v('#nc-target') !== undefined) S.f.target = v('#nc-target').trim();
    if (v('#nc-iface') !== undefined) S.f.iface = v('#nc-iface').trim();
    if (v('#nc-count') !== undefined) S.f.count = +v('#nc-count') || 10;
    if (v('#nc-filter') !== undefined) S.f.filter = v('#nc-filter');
  }

  // Lee las interfaces de "ip -br addr" para sugerirlas en tcpdump.
  function learnIfaces(lines) {
    const l = lines.find(x => x.cmd === 'ip -br addr');
    if (l && l.output) S.ifaces = l.output.split('\n').map(x => x.trim().split(/\s+/)[0]).filter(x => x && x !== 'lo').map(x => x.replace(/@.*/, ''));
  }

  async function run(e) {
    e.preventDefault();
    const c = current();
    if (!S.vm || S.busy || !c) return;
    readParams();
    const body = { cmd: c.id };
    if ((c.needs || []).includes('target')) body.target = S.f.target;
    if ((c.needs || []).includes('iface')) Object.assign(body, { iface: S.f.iface, count: S.f.count, filter: S.f.filter });
    S.busy = true;
    renderParams();
    const out = $('#nc-out');
    out.hidden = false;
    out.innerHTML = `<span class="nt"># ejecutando en ${esc(S.vm)}…${c.id === 'tcpdump' ? ' la captura dura hasta 15 s' : ''}</span>`;
    $('#nc-result').innerHTML = '';
    const d = await post('/network/cmd', body);
    S.busy = false;
    const res = d.data && d.data.steps ? d.data : null;
    if (res) {
      const lines = res.steps.flatMap(s => s.lines || []);
      if (c.id === 'addr') learnIfaces(lines);
      out.innerHTML = terminal(lines);
      const warn = res.steps.some(s => s.status === 'warn');
      if (res.message) $('#nc-result').innerHTML = `<div class="net-result ${warn ? 'warn' : res.ok ? 'ok' : 'bad'}">${esc(res.message)}</div>`;
    } else {
      out.hidden = true;
      $('#nc-result').innerHTML = `<div class="net-result bad"><b>No se ejecutó.</b> ${esc(d.error || 'error desconocido')}</div>`;
    }
    renderParams();
  }

  // ---------- abrir una VM ----------
  function open(name) {
    if (S.busy) return;
    showVmTab('cmds');
    S.vm = name;
    S.routing = null;
    S.ifaces = [];
    history.replaceState(null, '', '#vms/diagnostico/' + encodeURIComponent(name));
    document.querySelectorAll('#nc-vmlist .net-vm').forEach(b => b.setAttribute('aria-pressed', b.dataset.vm === name));
    $('#nc-title').textContent = 'Enrutamiento y diagnóstico · ' + name;
    $('#nc-sub').textContent = 'Lo que se ve aquí se lee de la VM en este momento; nada cambia sus direcciones.';
    ['#nc-out', '#nc-r-probe-out', '#nc-f-run'].forEach(s => { $(s).hidden = true; });
    $('#nc-result').innerHTML = '';
    showTab(S.tab);
    if (S.tab === 'cmds') loadRouting(); // para que Rutas y Reenvío estén listos al cambiar
  }

  async function show() {
    await loadCatalog();
    renderVmList();
    showTab(S.tab);
  }

  function bind() {
    if (!$('#nc-panel')) return;
    $('#nc-r-learn .net-lb').innerHTML =
      `<p>Para cada paquete, el kernel busca en la tabla la ruta cuyo destino contenga la IP de destino. Si varias sirven, gana la <b>más específica</b> (el prefijo más largo: /24 gana a /16, y cualquiera gana a <code>default</code>); si empatan, la de <b>menor métrica</b> ${RFC(1812, 'RFC 1812 §5.2.4.3')} ${RFC(4632)}.</p>` +
      `<p><b>Red conectada</b>: la crea el kernel por tener una IP en esa red; el paquete va directo, sin puerta de enlace. <b>Por puerta de enlace</b>: hacia esa red se pasa por un router (<code>via</code>). <b>default</b>: todo lo demás. Si hay varias <code>default</code>, se usa la de menor métrica y las otras quedan de respaldo.</p>` +
      `<p>«¿Por dónde sale?» usa <code>ip route get</code>, que pregunta al kernel qué ruta escogería, sin enviar nada. Manual: ${man('ip-route', 8)}.</p>`;
    $('#nc-f-learn .net-lb').innerHTML =
      `<p>Por defecto, Linux descarta los paquetes que llegan por una interfaz y no son para él. Con <code>net.ipv4.ip_forward = 1</code> los pasa a la interfaz que indique su tabla de rutas: la VM se comporta como un <b>router</b> ${RFC(1812)}. Manual: <a href="https://docs.kernel.org/networking/ip-sysctl.html" target="_blank" rel="noopener">ip-sysctl</a>.</p>` +
      `<p>Se necesita cuando una VM con dos tarjetas une dos redes. Activarlo no basta: cada equipo de las dos redes debe tener una ruta hacia la otra red que pase por la IP del router en su propia red (puerta de enlace o ruta «solo hacia ciertas redes», en Configuración de red). Para probar, use «¿Por dónde sale?» y luego Comandos → Ping.</p>`;
    document.querySelectorAll('.nc-tabs [data-ntab]').forEach(b => b.addEventListener('click', () => showTab(b.dataset.ntab)));
    $('#nc-vmlist').addEventListener('click', e => { const b = e.target.closest('.net-vm'); if (b && !b.disabled) open(b.dataset.vm); });
    $('#nc-catalog').addEventListener('click', e => {
      const b = e.target.closest('.nc-cmd'); if (!b || S.busy) return;
      readParams();
      S.cmd = b.dataset.cmd;
      $('#nc-out').hidden = true; $('#nc-result').innerHTML = '';
      renderCmds();
    });
    $('#nc-params').addEventListener('submit', run);
    $('#nc-params').addEventListener('input', () => { readParams(); $('#nc-c-line').textContent = cmdLine(S.cmd); });
    $('#nc-r-reload').addEventListener('click', () => { if (S.vm) loadRouting(); });
    $('#nc-r-probe').addEventListener('submit', probe);
    $('#nc-f-toggle').addEventListener('click', toggleFwd);
    $('#nc-channel').addEventListener('change', e => { S.channel = e.target.value; if (S.vm) { S.routing = null; showTab(S.tab); } });
  }

  window.netCmdsShow = show;
  window.openNetCmds = async (name, tab) => { await loadCatalog(); if (tab) S.tab = tab; open(name); };
  window.addEventListener('DOMContentLoaded', bind);
})();
