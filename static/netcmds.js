// ============== COMANDOS DE RED DE UNA VM ==============
// Consulta y diagnostico: tabla de rutas, ping, conexiones, puertos, ARP y
// captura de paquetes. NO cambia la configuracion de la VM (eso es el panel
// «Configuración de red»). El usuario escoge un comando del catalogo
// (GET /api/netcmds) y el servidor arma la linea y la ejecuta por el canal
// elegido (POST /api/vms/{vm}/network/cmd).

(function () {
  const $ = s => document.querySelector(s);
  const esc = s => String(s == null ? '' : s).replace(/[&<>"']/g, c => ({ '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#39;' }[c]));
  const MAN = 'https://manpages.debian.org/';
  const man = (page, sec) => `<a href="${MAN}${page}.${sec}" target="_blank" rel="noopener"><code>${page}(${sec})</code></a>`;

  const S = { vm: '', channel: '', cmd: 'route', catalog: [], filters: [], busy: false, ifaces: [] };

  // Para entender: que hace el comando y como leer su salida.
  const LEARN = {
    addr: `<p><code>ip addr</code> (paquete <b>iproute2</b>) lista cada interfaz con su estado (<code>UP</code>/<code>DOWN</code>), su MAC (<code>link/ether</code>), su MTU y sus direcciones <code>inet</code> con el prefijo (<code>/24</code>). La palabra <code>dynamic</code> indica que la dirección llegó por DHCP; sin ella, es fija.</p><p><code>ip -br addr</code> resume todo en una línea por interfaz. Reemplaza a <code>ifconfig</code> (net-tools). Manual: ${man('ip-address', 8)}.</p>`,
    route: `<p>Cada línea es una ruta: <b>destino</b>, <code>via</code> la puerta de enlace, <code>dev</code> la interfaz de salida, <code>proto</code> quién la creó (<code>kernel</code> por tener una IP en esa red, <code>dhcp</code>, <code>static</code>) y <code>metric</code> la prioridad.</p><p><code>default</code> es 0.0.0.0/0: lo que no coincide con ninguna otra ruta. Si hay varias, gana el prefijo más largo; si empatan, la de menor métrica. Manual: ${man('ip-route', 8)}.</p>`,
    neigh: `<p>La tabla de vecinos es la caché ARP: para cada IP de la red local, la MAC que le respondió (<code>lladdr</code>) y el estado (<code>REACHABLE</code>, <code>STALE</code>, <code>FAILED</code>).</p><p>Solo aparecen los equipos con los que la VM ya habló: si está vacía, haga primero un ping. Una entrada <code>FAILED</code> significa que nadie respondió a la pregunta ARP (IP inexistente o en otra red). Manual: ${man('ip-neighbour', 8)}.</p>`,
    arp: `<p><code>arp -a</code> muestra la misma tabla que <code>ip neigh</code>, con la herramienta antigua del paquete <b>net-tools</b>, que muchas distribuciones ya no instalan. Se incluye para comparar: hoy se recomienda <code>ip neigh</code>. Manual: ${man('arp', 8)}.</p>`,
    ss: `<p><code>ss -tulnp</code>: <code>-t</code> TCP, <code>-u</code> UDP, <code>-l</code> solo los que escuchan, <code>-n</code> números en vez de nombres, <code>-p</code> el proceso dueño.</p><p>En <i>Local Address:Port</i>, <code>0.0.0.0:22</code> es un servicio (sshd) que escucha en todas las interfaces; <code>127.0.0.1:…</code> solo se alcanza desde la misma VM. Manual: ${man('ss', 8)}.</p>`,
    conns: `<p><code>ss -tan</code> lista todas las conexiones TCP y su estado: <code>LISTEN</code> (esperando), <code>ESTAB</code> (abierta), <code>TIME-WAIT</code> (recién cerrada). Si usa el canal de gestión, aquí verá la propia conexión SSH de la app. Manual: ${man('ss', 8)}.</p>`,
    netstat: `<p><code>netstat -ant</code> muestra lo mismo que <code>ss -tan</code> con la herramienta clásica (<b>net-tools</b>). Está en desuso: <code>ss</code>, de iproute2, la reemplaza y lee la información directamente del kernel, más rápido. Manual: ${man('netstat', 8)}.</p>`,
    ping: `<p><code>ping</code> envía mensajes ICMP <i>echo request</i> y espera el <i>echo reply</i>. En la salida, <code>time</code> es el tiempo de ida y vuelta y la última línea cuenta los paquetes perdidos.</p><p>La app agrega <code>ip route get</code>, que dice por qué interfaz y puerta de enlace saldría el paquete. Si el ping falla, revise esa ruta, que el router tenga el reenvío IPv4 activo y que el destino tenga ruta de regreso. Manual: ${man('ping', 8)}.</p>`,
    tracepath: `<p><code>tracepath</code> muestra cada router por el que pasa el paquete (un salto por línea) y el MTU del camino. A diferencia de <code>traceroute</code>, no necesita privilegios. En el escenario A – R – B debe aparecer la IP del router R. Manual: ${man('tracepath', 8)}.</p>`,
    tcpdump: `<p><code>tcpdump</code> captura los paquetes que pasan por una interfaz: <code>-i</code> la interfaz (<code>any</code> = todas), <code>-n</code> sin resolver nombres, <code>-c</code> cuántos paquetes, <code>-l</code> salida línea a línea. La app corta la captura a los 15 s.</p><p>Para ver algo, provoque tráfico mientras captura (por ejemplo, un ping desde otra VM). Con el canal de gestión use el filtro <i>sin SSH</i>, o verá sobre todo la conexión de la propia app. Manual: ${man('tcpdump', 8)}.</p>`
  };
  const FILTER_LABEL = { '': 'todo el tráfico', icmp: 'solo ICMP (ping)', arp: 'solo ARP', ssh: 'solo SSH (puerto 22)', dns: 'solo DNS (puerto 53)', dhcp: 'solo DHCP', 'no-ssh': 'todo menos SSH' };

  async function loadCatalog() {
    if (S.catalog.length) return;
    try {
      const d = await (await fetch('/api/netcmds')).json();
      if (d.success) { S.catalog = d.data.commands; S.filters = d.data.filters; }
    } catch (_) { /* se muestra vacio */ }
  }

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

  function current() { return S.catalog.find(c => c.id === S.cmd) || S.catalog[0]; }

  function renderCatalog() {
    const groups = [];
    S.catalog.forEach(c => { let g = groups.find(x => x.name === c.group); if (!g) groups.push(g = { name: c.group, items: [] }); g.items.push(c); });
    $('#nc-catalog').innerHTML = groups.map(g => `<div class="nc-group"><h4>${esc(g.name)}</h4><div class="nc-cmds">` +
      g.items.map(c => `<button type="button" class="nc-cmd" data-cmd="${esc(c.id)}" aria-pressed="${c.id === S.cmd}"><b>${esc(c.title)}</b><code>${esc(c.example)}</code>` +
        (c.package === 'net-tools' ? '<span class="net-chip dim">net-tools</span>' : '') + '</button>').join('') + '</div></div>').join('');
  }

  function renderParams() {
    const c = current();
    if (!c) return;
    const needs = c.needs || [];
    const p = [];
    if (needs.includes('target')) {
      p.push(`<label class="nc-param">Destino <input id="nc-target" value="${esc(S.target || '')}" placeholder="192.168.20.10 o debian.org" autocomplete="off" spellcheck="false" required></label>`);
    }
    if (needs.includes('iface')) {
      p.push(`<label class="nc-param">Interfaz <input id="nc-iface" list="nc-ifaces" value="${esc(S.iface || 'any')}" autocomplete="off" spellcheck="false" required>` +
        `<datalist id="nc-ifaces"><option value="any">${S.ifaces.map(i => `<option value="${esc(i)}">`).join('')}</datalist></label>`);
      p.push(`<label class="nc-param">Paquetes <input id="nc-count" type="number" min="1" max="50" value="${S.count || 10}"></label>`);
      p.push(`<label class="nc-param">Filtro <select id="nc-filter">${S.filters.map(f => `<option value="${esc(f)}"${f === (S.filter || '') ? ' selected' : ''}>${esc(FILTER_LABEL[f] || f)}</option>`).join('')}</select></label>`);
    }
    $('#nc-params').innerHTML = p.join('') + `<button type="submit" class="btn-primary" id="nc-run"${S.vm && !S.busy ? '' : ' disabled'}>${S.busy ? 'Ejecutando…' : 'Ejecutar'}</button>` +
      (S.vm ? '' : '<span class="net-help">Escoja una VM encendida a la izquierda.</span>');
    const l = $('#nc-learn');
    l.querySelector('.net-ls').textContent = c.title.toLowerCase();
    l.querySelector('.net-lb').innerHTML = LEARN[c.id] || '';
  }

  function renderAll() { renderCatalog(); renderParams(); }

  function open(name) {
    if (S.busy) return;
    showVmTab('cmds');
    S.vm = name;
    S.ifaces = [];
    history.replaceState(null, '', '#vms/comandos/' + encodeURIComponent(name));
    document.querySelectorAll('#nc-vmlist .net-vm').forEach(b => b.setAttribute('aria-pressed', b.dataset.vm === name));
    $('#nc-title').textContent = 'Comandos de red en ' + name;
    $('#nc-out').hidden = true;
    $('#nc-result').innerHTML = '';
    renderAll();
  }

  // Lee las interfaces de la salida de "ip -br addr" para sugerirlas en tcpdump.
  function learnIfaces(lines) {
    const l = lines.find(x => x.cmd === 'ip -br addr');
    if (!l || !l.output) return;
    S.ifaces = l.output.split('\n').map(x => x.trim().split(/\s+/)[0]).filter(x => x && x !== 'lo').map(x => x.replace(/@.*/, ''));
  }

  async function run(e) {
    e.preventDefault();
    const c = current();
    if (!S.vm || S.busy || !c) return;
    const body = { cmd: c.id };
    if ((c.needs || []).includes('target')) body.target = S.target = $('#nc-target').value.trim();
    if ((c.needs || []).includes('iface')) {
      body.iface = S.iface = $('#nc-iface').value.trim();
      body.count = S.count = +$('#nc-count').value || 10;
      body.filter = S.filter = $('#nc-filter').value;
    }
    S.busy = true;
    renderParams();
    const out = $('#nc-out');
    out.hidden = false;
    out.innerHTML = `<span class="nt"># ${esc(c.title)} en ${esc(S.vm)} por ${S.channel ? 'el canal de gestión (SSH)' : 'el canal interno (Guest Additions)'}…${c.id === 'tcpdump' ? ' la captura dura hasta 15 s' : ''}</span>`;
    $('#nc-result').innerHTML = '';
    let data;
    try {
      const r = await fetch('/api/vms/' + encodeURIComponent(S.vm) + '/network/cmd' + (S.channel ? '?canal=' + S.channel : ''),
        { method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify(body) });
      data = await r.json();
    } catch (err) {
      data = { success: false, error: err.message };
    }
    S.busy = false;
    const res = data.data && data.data.steps ? data.data : null;
    if (res) {
      const lines = res.steps.flatMap(s => s.lines || []);
      if (c.id === 'addr') learnIfaces(lines);
      out.innerHTML = lines.map(l => `<span class="k2">$ ${esc(l.cmd)}</span>\n` +
        (l.note ? `<span class="nt"># ${esc(l.note)}</span>\n` : '') +
        `<span class="${l.error ? 'er' : l.warn ? 'wr' : 'o'}">${esc(l.output || '(sin salida)')}</span>`).join('\n\n');
      const warn = res.steps.some(s => s.status === 'warn');
      if (res.message) $('#nc-result').innerHTML = `<div class="net-result ${warn ? 'warn' : res.ok ? 'ok' : 'bad'}">${esc(res.message)}</div>`;
    } else {
      out.hidden = true;
      $('#nc-result').innerHTML = `<div class="net-result bad"><b>No se ejecutó.</b> ${esc(data.error || 'error desconocido')}</div>`;
    }
    renderParams();
  }

  async function show() {
    await loadCatalog();
    renderVmList();
    renderAll();
  }

  function bind() {
    if (!$('#nc-panel')) return;
    $('#nc-vmlist').addEventListener('click', e => { const b = e.target.closest('.net-vm'); if (b && !b.disabled) open(b.dataset.vm); });
    $('#nc-catalog').addEventListener('click', e => {
      const b = e.target.closest('.nc-cmd'); if (!b || S.busy) return;
      S.cmd = b.dataset.cmd; renderAll();
    });
    $('#nc-params').addEventListener('submit', run);
    $('#nc-channel').addEventListener('change', e => { S.channel = e.target.value; });
  }

  window.netCmdsShow = show;
  window.openNetCmds = async name => { await loadCatalog(); open(name); };
  window.addEventListener('DOMContentLoaded', bind);
})();
