// ============== PANEL DE RED DE UNA VM ==============
// El usuario decide interfaz, modo y datos; el servidor valida (POST .../plan)
// y aplica (POST .../apply). Todo lo que el panel muestra sale de la lectura
// real de la VM, hecha por el canal elegido: Guest Additions o SSH de gestión (GET .../network).

(function () {
  const MAN = 'https://manpages.debian.org/';
  // Bibliografia: una sola lista; el orden define el numero de cada fuente.
  const REFS = [
    { g: 'Estándares de Internet (RFC del IETF)', k: 'rfc791', s: 'RFC 791', t: 'J. Postel. <i>Internet Protocol</i>. RFC 791, IETF, septiembre de 1981.', u: 'https://www.rfc-editor.org/rfc/rfc791' },
    { k: 'rfc826', s: 'RFC 826', t: 'D. Plummer. <i>An Ethernet Address Resolution Protocol</i>. RFC 826, 1982.', u: 'https://www.rfc-editor.org/rfc/rfc826' },
    { k: 'rfc919', s: 'RFC 919', t: 'J. Mogul. <i>Broadcasting Internet Datagrams</i>. RFC 919, 1984.', u: 'https://www.rfc-editor.org/rfc/rfc919' },
    { k: 'rfc950', s: 'RFC 950', t: 'J. Mogul, J. Postel. <i>Internet Standard Subnetting Procedure</i>. RFC 950, 1985.', u: 'https://www.rfc-editor.org/rfc/rfc950' },
    { k: 'rfc1034', s: 'RFC 1034', t: 'P. Mockapetris. <i>Domain Names — Concepts and Facilities</i>. RFC 1034, 1987.', u: 'https://www.rfc-editor.org/rfc/rfc1034' },
    { k: 'rfc1035', s: 'RFC 1035', t: 'P. Mockapetris. <i>Domain Names — Implementation and Specification</i>. RFC 1035, 1987.', u: 'https://www.rfc-editor.org/rfc/rfc1035' },
    { k: 'rfc1122', s: 'RFC 1122', t: 'R. Braden (ed.). <i>Requirements for Internet Hosts — Communication Layers</i>. RFC 1122, 1989. §3.3.1 puerta de enlace; §3.3.4 varias interfaces.', u: 'https://www.rfc-editor.org/rfc/rfc1122' },
    { k: 'rfc1812', s: 'RFC 1812', t: 'F. Baker (ed.). <i>Requirements for IP Version 4 Routers</i>. RFC 1812, 1995. §5.2.4.3 elección de la ruta.', u: 'https://www.rfc-editor.org/rfc/rfc1812' },
    { k: 'rfc1918', s: 'RFC 1918', t: 'Y. Rekhter et al. <i>Address Allocation for Private Internets</i>. RFC 1918, 1996.', u: 'https://www.rfc-editor.org/rfc/rfc1918' },
    { k: 'rfc2131', s: 'RFC 2131', t: 'R. Droms. <i>Dynamic Host Configuration Protocol</i>. RFC 2131, 1997.', u: 'https://www.rfc-editor.org/rfc/rfc2131' },
    { k: 'rfc2132', s: 'RFC 2132', t: 'S. Alexander, R. Droms. <i>DHCP Options and BOOTP Vendor Extensions</i>. RFC 2132, 1997.', u: 'https://www.rfc-editor.org/rfc/rfc2132' },
    { k: 'rfc4632', s: 'RFC 4632', t: 'V. Fuller, T. Li. <i>Classless Inter-domain Routing (CIDR)</i>. RFC 4632, 2006.', u: 'https://www.rfc-editor.org/rfc/rfc4632' },
    { k: 'rfc5227', s: 'RFC 5227', t: 'S. Cheshire. <i>IPv4 Address Conflict Detection</i>. RFC 5227, 2008.', u: 'https://www.rfc-editor.org/rfc/rfc5227' },
    { g: 'Manuales de las distribuciones', k: 'dah', s: 'Manual del Administrador de Debian §8.2', t: 'R. Hertzog, R. Mas. <i>El manual del Administrador de Debian</i>, §8.2 «Configuración de red».', u: 'https://debian-handbook.info/browse/es-ES/stable/sect.network-config.html' },
    { k: 'debref', s: 'Debian Reference cap. 5', t: 'O. Aoki. <i>Debian Reference</i>, capítulo 5 «Network setup».', u: 'https://www.debian.org/doc/manuals/debian-reference/ch05.en.html' },
    { k: 'ubuntunet', s: 'Ubuntu Server: Configuring networks', t: 'Canonical. <i>Ubuntu Server documentation — Configuring networks</i>.', u: 'https://ubuntu.com/server/docs/configuring-networks' },
    { k: 'ubuntunetplan', s: 'Ubuntu Server: About Netplan', t: 'Canonical. <i>Ubuntu Server documentation — About Netplan</i>.', u: 'https://ubuntu.com/server/docs/explanation/networking/about-netplan/' },
    { k: 'rhelnet', s: 'RHEL 9: Configuración y gestión de redes', t: 'Red Hat. <i>Red Hat Enterprise Linux 9 — Configuración y gestión de redes</i> (también aplica a Rocky y Fedora: mismo NetworkManager).', u: 'https://docs.redhat.com/es/documentation/red_hat_enterprise_linux/9/html/configuring_and_managing_networking/index' },
    { k: 'fedoradocs', s: 'Fedora Docs', t: 'Proyecto Fedora. <i>Fedora Documentation</i>.', u: 'https://docs.fedoraproject.org/en-US/docs/' },
    { k: 'archnet', s: 'ArchWiki: Network configuration', t: 'ArchWiki. <i>Network configuration (Español)</i>.', u: 'https://wiki.archlinux.org/title/Network_configuration_(Espa%C3%B1ol)' },
    { k: 'archnetworkd', s: 'ArchWiki: systemd-networkd', t: 'ArchWiki. <i>systemd-networkd</i>.', u: 'https://wiki.archlinux.org/title/Systemd-networkd' },
    { g: 'Páginas de manual (en la VM: man <sección> <nombre>)', k: 'maninterfaces', s: 'interfaces(5)', t: '<i>interfaces(5)</i>: /etc/network/interfaces (ifupdown).', u: MAN + 'interfaces.5', man: 'man 5 interfaces' },
    { k: 'manipaddr', s: 'ip-address(8)', t: '<i>ip-address(8)</i>: direcciones de una interfaz.', u: MAN + 'ip-address.8', man: 'man 8 ip-address' },
    { k: 'maniproute', s: 'ip-route(8)', t: '<i>ip-route(8)</i>: tabla de rutas y métricas.', u: MAN + 'ip-route.8', man: 'man 8 ip-route' },
    { k: 'maniprule', s: 'ip-rule(8)', t: '<i>ip-rule(8)</i>: enrutamiento por políticas.', u: MAN + 'ip-rule.8', man: 'man 8 ip-rule' },
    { k: 'mannmcli', s: 'nmcli(1)', t: '<i>nmcli(1)</i>: NetworkManager desde la línea de comandos.', u: MAN + 'nmcli.1', man: 'man 1 nmcli' },
    { k: 'nmsettings', s: 'nm-settings-nmcli(5)', t: '<i>nm-settings-nmcli(5)</i>: propiedades de una conexión.', u: MAN + 'nm-settings-nmcli.5', man: 'man 5 nm-settings-nmcli' },
    { k: 'mannetworkd', s: 'systemd.network(5)', t: '<i>systemd.network(5)</i>: archivos .network.', u: MAN + 'systemd.network.5', man: 'man 5 systemd.network' },
    { k: 'mansystemdrun', s: 'systemd-run(1)', t: '<i>systemd-run(1)</i>: unidades transitorias y temporizadores.', u: MAN + 'systemd-run.1', man: 'man 1 systemd-run' },
    { k: 'manarping', s: 'arping(8)', t: '<i>arping(8)</i>: con -D, detección de direcciones duplicadas.', u: MAN + 'arping.8', man: 'man 8 arping' },
    { k: 'manresolv', s: 'resolv.conf(5)', t: '<i>resolv.conf(5)</i>: el resolver de nombres.', u: MAN + 'resolv.conf.5', man: 'man 5 resolv.conf' },
    { k: 'manresolvectl', s: 'resolvectl(1)', t: '<i>resolvectl(1)</i>: DNS por interfaz con systemd-resolved.', u: MAN + 'resolvectl.1', man: 'man 1 resolvectl' },
    { k: 'mannaming', s: 'systemd.net-naming-scheme(7)', t: '<i>systemd.net-naming-scheme(7)</i>: nombres como enp1s0.', u: MAN + 'systemd.net-naming-scheme.7', man: 'man 7 systemd.net-naming-scheme' },
    { g: 'Otras referencias técnicas', k: 'netplan', s: 'Netplan YAML', t: 'Canonical. <i>Netplan — YAML configuration reference</i>.', u: 'https://netplan.readthedocs.io/en/stable/netplan-yaml/' },
    { k: 'cloudinit', s: 'cloud-init: network config', t: 'Canonical. <i>cloud-init — Network configuration</i>: cómo desactivarla con <code>network: {config: disabled}</code>.', u: 'https://cloudinit.readthedocs.io/en/latest/reference/network-config.html' },
    { k: 'vboxga', s: 'VirtualBox: Guest Additions', t: 'Oracle. <i>VirtualBox User Manual</i>: Guest Additions y <code>VBoxManage guestcontrol</code>.', u: 'https://www.virtualbox.org/manual/' },
    { k: 'vboxnet', s: 'VirtualBox: Virtual Networking', t: 'Oracle. <i>VirtualBox User Manual</i>, «Virtual Networking»: NAT, puente, solo anfitrión, red interna y su DHCP.', u: 'https://www.virtualbox.org/manual/' },
    { k: 'kernsysctl', s: 'ip-sysctl', t: 'Linux Kernel. <i>IP Sysctl</i>: net.ipv4.ip_forward.', u: 'https://docs.kernel.org/networking/ip-sysctl.html' },
    { k: 'lartc', s: 'LARTC cap. 4', t: 'B. Hubert et al. <i>Linux Advanced Routing &amp; Traffic Control HOWTO</i>, §4.2.', u: 'https://lartc.org/howto/lartc.rpdb.multiple-links.html' }
  ];
  const RIDX = {};
  REFS.forEach((r, n) => { RIDX[r.k] = n + 1; r.n = n + 1; });

  // Manual oficial de cada distribucion y la herramienta de red que usa.
  const DISTROS = {
    ubuntu: { name: 'Ubuntu', c: '#e95420', tool: 'Netplan (YAML en <code>/etc/netplan/</code>), que genera la configuración de systemd-networkd. Es la plantilla con la que la app crea las VMs.', refs: [['ubuntunet', 'Ubuntu Server: Configuring networks'], ['ubuntunetplan', 'Ubuntu Server: About Netplan'], ['netplan', 'Netplan: referencia YAML']] },
    debian: { name: 'Debian', c: '#c70036', tool: 'ifupdown (<code>/etc/network/interfaces</code>) en servidores; NetworkManager en escritorios. Las imágenes de nube recientes usan Netplan.', refs: [['dah', 'Manual del Administrador de Debian, §8.2'], ['debref', 'Debian Reference, cap. 5'], ['maninterfaces', 'interfaces(5)']] },
    rhel: { name: 'Rocky / RHEL', c: '#10b981', tool: 'NetworkManager con <code>nmcli</code>; los perfiles se guardan en <code>/etc/NetworkManager/system-connections/</code>.', refs: [['rhelnet', 'RHEL 9: Configuración y gestión de redes'], ['mannmcli', 'nmcli(1)'], ['nmsettings', 'nm-settings-nmcli(5)']] },
    fedora: { name: 'Fedora', c: '#3c6eb4', tool: 'NetworkManager con <code>nmcli</code>, igual que RHEL (Fedora es su base).', refs: [['fedoradocs', 'Fedora Documentation'], ['rhelnet', 'RHEL 9: Configuración y gestión de redes']] },
    arch: { name: 'Arch Linux', c: '#1793d1', tool: 'No trae un gestor fijo; lo común es systemd-networkd con archivos <code>.network</code>.', refs: [['archnet', 'ArchWiki: Network configuration'], ['archnetworkd', 'ArchWiki: systemd-networkd']] },
    other: { name: 'esta distribución', refs: [['maniproute', 'ip-route(8)'], ['manipaddr', 'ip-address(8)']] }
  };

  const S = { channel: '', vm: null, state: null, revert: 120, iface: null, mode: 'static', f: {}, tab: 'file', lf: 'ip', plan: null, busy: false, seq: 0, timer: null };
  const $ = sel => document.querySelector(sel);
  const esc = s => String(s ?? '').replace(/&/g, '&amp;').replace(/</g, '&lt;').replace(/>/g, '&gt;');
  const isDef = r => r === 'main' || r === 'backup';

  function R(k, label) {
    const r = REFS[RIDX[k] - 1];
    return `<a class="net-rf" href="${r.u}" target="_blank" rel="noopener">${label || r.s}</a><a class="net-fn" href="#net-ref-${k}" data-ref="${k}" title="Ver en las fuentes">[${r.n}]</a>`;
  }
  const P = (title, html) => `<p><b>${title}</b> ${html}</p>`;
  const distro = () => DISTROS[S.state?.family] || DISTROS.other;
  const distroRefs = () => distro().refs.map(r => R(r[0], r[1])).join(' ');

  // ---------- utilidades IPv4 (solo para las pistas bajo los campos) ----------
  function ip2n(s) {
    const m = /^(\d{1,3})\.(\d{1,3})\.(\d{1,3})\.(\d{1,3})$/.exec((s || '').trim());
    if (!m) return null;
    const o = m.slice(1).map(Number);
    if (o.some(x => x > 255)) return null;
    return ((o[0] << 24) >>> 0) + (o[1] << 16) + (o[2] << 8) + o[3];
  }
  const n2ip = n => [n >>> 24, (n >>> 16) & 255, (n >>> 8) & 255, n & 255].join('.');
  const maskN = p => (p === 0 ? 0 : (0xFFFFFFFF << (32 - p)) >>> 0);
  function parseMask(s) {
    s = (s || '').trim();
    const m = /^\/?(\d{1,2})$/.exec(s);
    if (m) return +m[1] <= 32 ? +m[1] : null;
    const n = ip2n(s);
    if (n === null) return null;
    const inv = (~n) >>> 0;
    if (((inv + 1) & inv) !== 0) return null;
    let c = 0;
    for (let i = 31; i >= 0 && ((n >>> i) & 1); i--) c++;
    return c;
  }
  const netOf = (n, p) => (n & maskN(p)) >>> 0;

  // ---------- recuadros "Para entender" ----------
  function setL(id, title, html) {
    const d = $('#' + id);
    d.querySelector('.net-ls').textContent = title;
    d.querySelector('.net-lb').innerHTML = html;
  }
  function learnIface() {
    const st = S.state, multi = st.interfaces.filter(i => !i.virtual).length > 1, virt = st.interfaces.some(i => i.virtual);
    let h = P('Interfaz de red.', 'Es el nombre que el kernel le da a cada tarjeta. Los nombres predecibles salen de la ubicación física: <code>en</code> es Ethernet, <code>p1s0</code> el bus PCI 1, ranura 0. Así el nombre no cambia entre reinicios, como sí podía pasar con <code>eth0</code> ' + R('mannaming') + ' ' + R('dah', 'Manual de Debian §8.2.1') + '. La MAC identifica la tarjeta en la red local, y ARP la asocia con la IP ' + R('rfc826') + '.');
    if (virt) h += P('Interfaces virtuales.', 'Las marcadas como <i>virtual</i> no son tarjetas: las crea un programa (Docker, libvirt, una VPN). La app lo sabe porque en <code>/sys/class/net</code> apuntan a <code>/devices/virtual/</code>. Se gestionan desde el programa que las creó.');
    if (multi) h += P('Varias tarjetas.', 'Un equipo con dos o más interfaces es <i>multihomed</i>: cada una tiene su IP en su red y se configura por separado ' + R('rfc1122', 'RFC 1122 §3.3.4') + '. Si además pasa paquetes entre redes, se comporta como router ' + R('rfc1812') + '; eso lo decide <code>net.ipv4.ip_forward</code> ' + R('kernsysctl') + '.');
    h += P('Cómo llega la app a la VM.', (S.channel ? 'Entra por SSH a la interfaz de gestión, una tarjeta reservada que nunca se modifica: cambiar las demás no corta la sesión.' : 'No entra por SSH a la red que cambia: le pide a VirtualBox que ejecute los comandos con las Guest Additions, por un dispositivo interno ' + R('vboxga') + '. Ese canal no usa la red de la VM, así que cambiar la IP no lo corta.'));
    h += P('En ' + distro().name + '.', 'Manual oficial: ' + distroRefs() + '.');
    setL('net-L-iface', 'qué es una interfaz' + (multi ? ' · varias tarjetas' : '') + (virt ? ' · interfaces virtuales' : '') + ' · cómo llega la app', h);
  }
  function learnMode() {
    let h = '';
    if (S.mode === 'dhcp') h += P('DHCP.', 'Definido en el ' + R('rfc2131') + '. La VM pide su configuración y un servidor le presta una IP por un tiempo; las opciones del ' + R('rfc2132') + ' traen la máscara (1), la puerta de enlace (3) y el DNS (6). En la red NAT de VirtualBox, ese servidor es el propio VirtualBox ' + R('vboxnet') + '.');
    else if (S.mode === 'static') h += P('IP fija.', 'La configuración mínima de un host según el ' + R('rfc1122') + ': dirección, máscara y, si va a salir de su red, una puerta de enlace; el DNS se agrega para resolver nombres. Conviene elegirla <b>fuera del rango</b> que reparte el DHCP de la red, para que el servidor no se la entregue a otra VM ' + R('vboxnet') + '.');
    else h += P('IP adicional.', 'Una interfaz puede tener varias direcciones ' + R('rfc1122', 'RFC 1122 §3.3.4') + '. Se agrega con <code>ip addr add</code> ' + R('manipaddr') + ' y queda en el archivo para que sobreviva al reinicio. Así una sola tarjeta física tiene «varias interfaces».');
    h += P('Cómo se hace en ' + distro().name + ':', distroRefs() + '.');
    setL('net-L-mode', { dhcp: 'qué es DHCP', static: 'qué es una IP fija', extra: 'qué es una IP adicional' }[S.mode], h);
  }
  const LF = {
    ip: () => P('Dirección IP (IPv4).', '32 bits en cuatro números de 0 a 255 ' + R('rfc791') + '. 10/8, 172.16/12 y 192.168/16 son privadas ' + R('rfc1918') + '. La primera dirección de cada red la identifica y la última es el broadcast ' + R('rfc919') + ': no se asignan. Antes de aplicar, la app revisa las IP de las otras VMs del hipervisor y pregunta por ARP con arping si alguien ya la tiene ' + R('rfc5227') + ' ' + R('manarping') + '.'),
    mask: () => P('Máscara.', 'Cuántos bits son la red. El ' + R('rfc950') + ' introdujo 255.255.255.0; el ' + R('rfc4632') + ' la escribe como /24. Es lo mismo: Linux muestra /24 y Windows 255.255.255.0.'),
    gw: () => P('Puerta de enlace.', 'El router al que se envía lo que no está en la red local; debe estar en la misma subred ' + R('rfc1122', 'RFC 1122 §3.3.1') + '. En la red NAT de VirtualBox es 10.0.2.2, el propio VirtualBox.') +
      P('¿Puede haber más de una?', 'Sí: entre varias rutas por defecto el kernel usa la de menor métrica y las demás quedan de respaldo ' + R('maniproute') + '. También se puede usar una puerta de enlace solo hacia ciertas redes, o enrutamiento por políticas con <code>ip rule</code> ' + R('maniprule') + ' ' + R('lartc') + '. Las redes internas de un router van sin puerta de enlace ' + R('rfc1812') + '.'),
    dns: () => P('DNS.', 'Traduce nombres a IPs ' + R('rfc1034') + ' ' + R('rfc1035') + '. Con ifupdown hay un solo resolver para toda la VM (' + R('manresolv') + '); con systemd-resolved cada interfaz tiene el suyo (' + R('manresolvectl') + ').')
  };
  const LFN = { ip: 'IP', mask: 'Máscara', gw: 'Puerta de enlace', dns: 'DNS' };
  function learnField() {
    setL('net-L-field', 'qué significa cada dato', '<div class="net-ltabs">' + Object.keys(LF).map(k =>
      `<button type="button" data-lf="${k}" aria-pressed="${k === S.lf}">${LFN[k]}</button>`).join('') + '</div>' + LF[S.lf]());
  }
  function learnPrev() {
    const m = S.state.manager;
    const txt = {
      file: P('El archivo.', {
        netplan: 'La app no edita <code>50-cloud-init.yaml</code>: escribe su propio archivo <code>90-ejecutor-…yaml</code>. Netplan une los archivos en orden alfabético y el de número mayor manda ' + R('netplan') + '. Además apaga la red de cloud-init para que no la vuelva a escribir al reiniciar ' + R('cloudinit') + '.',
        networkd: 'Archivo <code>.network</code> de systemd-networkd ' + R('mannetworkd') + '. El prefijo <code>10-</code> hace que se lea antes que los demás.',
        ifupdown: 'Formato de <code>/etc/network/interfaces</code> ' + R('maninterfaces') + ' ' + R('dah', 'Manual de Debian §8.2') + '.',
        nm: 'NetworkManager guarda cada conexión en <code>/etc/NetworkManager/system-connections/</code>. La app no edita el archivo: lo cambia con ' + R('mannmcli') + ' ' + R('nmsettings') + '.'
      }[m] || '') + P('Manual de ' + distro().name + ':', distroRefs() + '.'),
      cmd: P('Por qué cada fase.', '<code>arping -D</code> comprueba que la IP esté libre ' + R('rfc5227') + '. ' + R('mansystemdrun') + ' programa la vuelta atrás dentro de la VM. La verificación lee el estado real con ' + R('manipaddr') + ' y ' + R('maniproute') + '. Las líneas marcadas <i>hipervisor</i> se ejecutan en el host, no en la VM.'),
      route: P('Cómo elige el kernel.', 'Gana la ruta más específica (prefijo más largo); si empatan, la de menor métrica ' + R('rfc1812', 'RFC 1812 §5.2.4.3') + ' ' + R('rfc4632') + '. Las líneas <code>proto kernel scope link</code> las crea el kernel por tener una IP en esa red.')
    }[S.tab];
    setL('net-L-prev', { file: 'cómo se lee este archivo', cmd: 'por qué cada fase', route: 'cómo elige el kernel la ruta (rutas resultantes)' }[S.tab], txt);
  }

  // ---------- render ----------
  function iface() { return S.state.interfaces.find(i => i.name === S.iface); }
  function roleTxt(i) {
    if (i.role === 'main') return 'principal' + (i.metric ? ' · métrica ' + i.metric : '');
    if (i.role === 'backup') return 'respaldo · métrica ' + i.metric;
    if (i.role === 'routes') return 'solo hacia ' + i.nets.join(', ');
    return 'sin puerta de enlace';
  }
  function renderHead() {
    const st = S.state;
    $('#net-title').textContent = 'Configuración de red de ' + S.vm;
    const channel = st.channel || (st.hypervisor === 'VirtualBox' ? 'el canal interno (Guest Additions)' : 'el canal interno (agente QEMU)');
    $('#net-detect').innerHTML = 'Detectado por ' + channel + ': ' +
      `<span class="net-chip">${esc(st.os || 'sistema desconocido')}</span><span class="net-chip">${esc(st.managerName || 'gestor no reconocido')}</span>` +
      (st.forwarding ? '<span class="net-chip warm">reenvío IPv4 activo (router)</span>' : '<span class="net-chip dim">sin reenvío IPv4</span>') +
      (st.cloudInit ? '<span class="net-chip dim">cloud-init</span>' : '') +
      ` <button type="button" class="btn-small" id="net-fwd" title="net.ipv4.ip_forward: la VM pasa paquetes entre sus interfaces">${st.forwarding ? 'Desactivar reenvío' : 'Activar reenvío (router)'}</button>`;
    $('#net-fwd').onclick = toggleForwarding;
    $('#net-distline').innerHTML = 'Manual de ' + esc(distro().name) + ': ' + distroRefs();
    $('#net-raw').innerHTML = st.raw.map(c => `<span class="k2">$ ${esc(c.cmd)}</span>\n<span class="o">${esc(c.output)}</span>`).join('\n');
  }
  function renderIfaces() {
    const st = S.state;
    $('#net-ifaces').innerHTML = st.interfaces.map(i => {
      const ln = st.libvirt && st.libvirt[i.mac.toLowerCase()];
      const off = i.virtual || i.type === 'wifi' || S.busy;
      return `<label class="net-iface${off ? ' off' : ''}"><input type="radio" name="net-iface" value="${esc(i.name)}"${i.name === S.iface ? ' checked' : ''}${off ? ' disabled' : ''}>` +
        `<span class="t"><span class="mono">${esc(i.name)}</span>` +
        (i.virtual ? '<span class="net-chip dim">virtual</span>' : i.type === 'wifi' ? '<span class="net-chip dim">Wi‑Fi</span>' : '<span class="net-chip dim">tarjeta</span>') +
        (i.role === 'main' ? '<span class="net-chip">ruta por defecto</span>' : '') + (i.role === 'backup' ? '<span class="net-chip warn">respaldo</span>' : '') + '</span>' +
        `<span class="s mono">${esc(i.mac)}</span>` +
        `<span class="s"><span class="mono">${i.ip ? esc(i.ip + '/' + i.prefix) : 'sin IPv4'}</span>${i.ip ? ' · ' + (i.mode === 'dhcp' ? 'DHCP (dynamic)' : 'fija') : ''}${i.extras.length ? ' · +' + i.extras.length + ' adicional' : ''}</span>` +
        `<span class="s">${i.role === 'none' ? 'sin puerta de enlace' : 'puerta de enlace <span class="mono">' + esc(i.gateway) + '</span> · ' + esc(roleTxt(i))}</span>` +
        (ln ? `<span class="s">red de ${esc(st.hypervisor || 'libvirt')} «${esc(ln.network)}» · DHCP ${esc(ln.dhcpStart || 'no')}${ln.dhcpEnd ? '–' + esc(ln.dhcpEnd) : ''}</span>` : '') +
        '</label>';
    }).join('');
    const real = st.interfaces.filter(i => !i.virtual);
    const defs = real.filter(i => isDef(i.role)).length;
    const n = $('#net-ifnote');
    n.innerHTML = real.length > 1
      ? `<b>${real.length} tarjetas.</b> Cada una se configura por separado. ` + (defs > 1 ? `Hay <b>${defs} rutas por defecto</b>; gana la de menor métrica.` : 'Regla práctica: una sola puerta de enlace principal.')
      : '<b>Una sola tarjeta.</b> La app no se desconecta al cambiarla: habla con la VM por las Guest Additions, no por su red.';
  }
  function renderActual() {
    const i = iface();
    $('#net-a-ip').innerHTML = i.ip ? esc(i.ip + '/' + i.prefix) + ` <span class="net-chip">${i.mode === 'dhcp' ? 'DHCP' : 'fija'}</span>` + i.extras.map(x => `<span class="net-chip dim" title="IP adicional en la misma tarjeta">+ ${esc(x)} adicional</span>`).join('') : '—';
    $('#net-a-mask').textContent = i.ip ? n2ip(maskN(i.prefix)) : '—';
    $('#net-a-gw').textContent = i.gateway || '—';
    $('#net-a-role').textContent = roleTxt(i);
    $('#net-a-nets').textContent = i.nets.join(', ') || '—';
    $('#net-a-dns').textContent = i.dns || '—';
  }
  const HELP = {
    dhcp: 'El servidor DHCP de la red entrega los datos. En la red NAT de VirtualBox, ese servidor es el propio VirtualBox.',
    static: 'Los datos se escriben a mano y no cambian. Es lo que necesita un servidor al que se entra por SSH.',
    extra: 'La interfaz conserva su IP y suma otra. Puerta de enlace y DNS no cambian.'
  };
  function suggestGw() {
    const ln = S.state.libvirt && S.state.libvirt[iface().mac.toLowerCase()];
    if (ln) return ln.hostIP;
    const n = ip2n(S.f.ip), p = parseMask(S.f.mask);
    return n !== null && p ? n2ip(netOf(n, p) + 1) : '';
  }
  function renderMode() {
    document.querySelectorAll('#net-modes button').forEach(b => { b.setAttribute('aria-pressed', b.dataset.mode === S.mode); b.disabled = S.busy; });
    $('#net-modehelp').textContent = HELP[S.mode];
    const st = S.mode === 'static', dh = S.mode === 'dhcp', i = iface();
    const dis = { ip: dh, mask: dh, gw: !st || S.f.role === 'none', dns: !st || (S.state.manager === 'ifupdown' && S.f.role !== 'main'), nets: !st };
    const ph = { ip: 'lo entrega el servidor DHCP', mask: 'lo entrega el servidor DHCP', gw: dh ? 'lo entrega el servidor DHCP' : st ? 'no aplica: sin puerta de enlace' : 'se conserva', dns: dh ? 'lo entrega el servidor DHCP' : st ? 'uno solo para la VM: lo pone la interfaz principal' : 'se conserva', nets: 'se conserva' };
    Object.keys(dis).forEach(k => {
      const el = $('#net-f-' + k);
      el.disabled = dis[k] || S.busy;
      if (dis[k]) { el.value = ''; el.placeholder = ph[k]; }
      else { el.value = S.f[k] || ''; el.placeholder = k === 'gw' ? 'p. ej. ' + suggestGw() : k === 'nets' ? '10.0.3.0/24, 10.0.4.0/24' : k === 'dns' && S.f.role !== 'main' ? 'opcional en esta interfaz' : ''; }
    });
    const sel = $('#net-f-role');
    sel.value = st ? S.f.role : i.role;
    sel.disabled = !st || S.busy;
    $('#net-r-nets').hidden = sel.value !== 'routes';
    hints();
  }
  function hints() {
    const p = parseMask(S.f.mask), n = ip2n(S.f.ip);
    $('#net-h-mask').textContent = S.mode !== 'dhcp' && p !== null && p >= 8 && p <= 30 ? '= /' + p + ' = ' + n2ip(maskN(p)) : '';
    $('#net-h-ip').textContent = S.mode !== 'dhcp' && n !== null && p !== null && p >= 8 && p <= 30 ? 'red ' + n2ip(netOf(n, p)) + '/' + p : '';
  }
  function prefill() {
    const i = iface();
    S.f = { gw: i.gateway || '', dns: i.dns || '', role: i.role === 'none' && S.mode === 'static' && !S.state.interfaces.some(o => o.name !== i.name && !o.virtual && o.role === 'main') ? 'main' : i.role, nets: i.nets.join(', ') };
    if (S.mode === 'extra') { S.f.ip = ''; S.f.mask = '/' + i.prefix; }
    else { S.f.ip = i.ip; S.f.mask = n2ip(maskN(i.prefix)); }
    if (S.mode === 'static' && !S.f.gw && isDef(S.f.role)) S.f.gw = suggestGw();
  }
  function renderAll() {
    renderHead(); renderIfaces(); renderActual(); renderMode();
    learnIface(); learnMode(); learnField(); learnPrev();
    requestPlan(0);
  }

  // ---------- plan (validacion del servidor) ----------
  function request() {
    return { interface: S.iface, mode: S.mode, ip: S.f.ip || '', mask: S.f.mask || '', gateway: S.f.gw || '', role: S.f.role || 'main', nets: S.f.nets || '', dns: S.f.dns || '' };
  }
  function requestPlan(delay) {
    clearTimeout(S.timer);
    S.timer = setTimeout(async () => {
      const seq = ++S.seq;
      try {
        const r = await fetch('/api/vms/' + encodeURIComponent(S.vm) + '/network/plan' + qs(), { method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify(request()) });
        const data = await r.json();
        if (seq !== S.seq) return;
        if (!data.success) { S.plan = { checks: [{ level: 'bad', text: data.error || 'No se pudo validar' }], valid: false }; }
        else S.plan = data.data;
      } catch (err) {
        if (seq !== S.seq) return;
        S.plan = { checks: [{ level: 'bad', text: 'No se pudo validar: ' + err.message }], valid: false };
      }
      renderChecks(); renderPreview();
    }, delay);
  }
  function renderChecks() {
    $('#net-checks').innerHTML = (S.plan.checks || []).map(c => `<li class="${esc(c.level)}">${esc(c.text)}</li>`).join('');
    const btn = $('#net-apply');
    btn.disabled = !S.plan.valid || S.busy;
    btn.textContent = 'Aplicar con reversión (' + (S.plan.revertSeconds || S.revert) + ' s)';
  }
  function renderPreview() {
    ['file', 'cmd', 'route'].forEach(t => $('#net-t-' + t).setAttribute('aria-selected', S.tab === t));
    learnPrev();
    const box = $('#net-preview');
    const pl = S.plan;
    if (!pl || !pl.valid) {
      box.innerHTML = '<div class="net-placeholder">Cuando los datos pasen la validación, aquí aparece ' + { file: 'el archivo que se escribirá.', cmd: 'la lista exacta de comandos.', route: 'cómo quedará la tabla de rutas.' }[S.tab] + '</div>';
      return;
    }
    if (S.tab === 'file') {
      box.innerHTML = pl.files.map(f => `<div class="net-file"><div class="fp">${esc(f.path)}${f.note ? '  (' + esc(f.note) + ')' : ''}</div><pre>` +
        f.lines.map(l => l.changed ? `<span class="chg">${esc(l.text)}</span>` : /^\s*#/.test(l.text) ? `<span class="cm">${esc(l.text)}</span>` : esc(l.text)).join('\n') + '</pre></div>').join('');
    } else if (S.tab === 'cmd') {
      box.innerHTML = '<pre class="net-term">' + pl.phases.map((ph, n) => `<span class="ph"># ${n + 1}. ${esc(ph.name)}</span>` +
        ph.cmds.map(c => (c.cmd ? (c.host ? '<span class="app">[hipervisor]</span> $ ' : '$ ') + esc(c.cmd) : '') + (c.note ? `${c.cmd ? '   ' : ''}<span class="nt"># ${esc(c.note)}</span>` : '')).join('\n')).join('\n') + '</pre>';
    } else {
      box.innerHTML = `<div class="net-file"><div class="fp">${esc(pl.routeSummary)}</div><pre><span class="cm"># ip r — así quedará la tabla de rutas</span>\n` +
        pl.routes.map(r => r.state === 'new' ? `<span class="chg">${esc(r.text)}</span>` : r.state === 'gone' ? `<span class="del">${esc(r.text)}</span>` : esc(r.text)).join('\n') + '</pre></div>';
    }
  }

  // ---------- aplicar ----------
  // Reenvio IPv4: lo que convierte a la VM en router entre sus redes.
  async function toggleForwarding() {
    const btn = $('#net-fwd');
    const enabled = !S.state.forwarding;
    btn.disabled = true;
    btn.textContent = enabled ? 'Activando…' : 'Desactivando…';
    let data;
    try {
      const r = await fetch('/api/vms/' + encodeURIComponent(S.vm) + '/network/forwarding' + qs(), { method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify({ enabled }) });
      data = await r.json();
    } catch (err) {
      data = { success: false, error: err.message };
    }
    const lines = ((data.data && data.data.steps) || []).flatMap(s => s.lines).map(l => '$ ' + l.cmd + (l.output ? '\n' + l.output : '')).join('\n');
    if (window.toast) window.toast(data.success ? 'success' : 'error', (data.message || data.error || 'error') + (lines ? '\n\n' + lines : ''), data.success ? 8000 : 0);
    if (data.data && data.data.state) { S.state = data.data.state; renderAll(); }
    else { btn.disabled = false; renderHead(); }
  }

  async function apply() {
    if (!S.plan || !S.plan.valid || S.busy) return;
    S.busy = true;
    const phases = S.plan.phases.map(p => p.name);
    $('#net-run').hidden = false;
    $('#net-stepper').innerHTML = phases.map((p, n) => `<li class="${n === 0 ? 'run' : ''}"><span>${n + 1}</span><b>${esc(p)}</b></li>`).join('');
    $('#net-log').textContent = 'Aplicando por ' + (S.channel ? 'SSH a la interfaz de gestión' : 'las Guest Additions') + '… (puede tardar hasta un minuto; la VM tiene programada su vuelta atrás)';
    $('#net-result').innerHTML = '';
    renderIfaces(); renderMode(); renderChecks();
    try {
      const r = await fetch('/api/vms/' + encodeURIComponent(S.vm) + '/network/apply' + qs(), { method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify(request()) });
      const data = await r.json();
      const res = data.data && data.data.steps ? data.data : null;
      if (!res) {
        $('#net-stepper').innerHTML = '';
        $('#net-log').textContent = '';
        $('#net-result').innerHTML = `<div class="net-result bad"><b>No se aplicó.</b> ${esc(data.error || 'error desconocido')}</div>`;
      } else {
        $('#net-stepper').innerHTML = res.steps.map((s, n) => `<li class="${esc(s.status)}"><span>${n + 1}</span><b>${esc(s.name)}</b></li>`).join('');
        $('#net-log').innerHTML = res.steps.map(s => `<span class="ph"># ${esc(s.name)}</span>` + (s.lines || []).map(l =>
          (l.cmd ? (l.host ? '<span class="app">[hipervisor]</span> $ ' : '$ ') + esc(l.cmd) + '\n' : '') +
          (l.output ? `<span class="${l.error ? 'er' : l.warn ? 'wr' : 'o'}">${esc(l.output)}</span>\n` : '') +
          (l.note ? `<span class="nt"># ${esc(l.note)}</span>\n` : '')).join('')).join('');
        $('#net-result').innerHTML = `<div class="net-result ${res.ok ? 'ok' : 'bad'}">${res.ok ? '<b>Listo.</b> ' : ''}${esc(res.message)}</div>`;
        if (res.state) {
          S.state = res.state;
          if (res.ok) { if (S.mode === 'extra') S.mode = 'static'; prefill(); }
        }
      }
    } catch (err) {
      $('#net-result').innerHTML = `<div class="net-result bad"><b>Error de conexión con la app.</b> ${esc(err.message)}. Si la VM quedó mal, volverá sola a la configuración anterior cuando venza el temporizador.</div>`;
    }
    S.busy = false;
    renderAll();
    renderVmList();
    if (typeof listVMs === 'function') listVMs();
  }

  // ---------- sub-pestanas del modulo de VMs ----------
  function showVmTab(name) {
    document.querySelectorAll('.vm-subtabs [data-vmtab]').forEach(b => b.setAttribute('aria-selected', b.dataset.vmtab === name));
    ['manage', 'nets', 'net', 'cmds', 'docs'].forEach(k => { $('#vm-pane-' + k).hidden = k !== name; });
    if (name === 'net') renderVmList();
    if (name === 'cmds' && typeof window.netCmdsShow === 'function') window.netCmdsShow();
    if (name === 'nets' && typeof window.loadVirtualNetworks === 'function') window.loadVirtualNetworks();
    if (name === 'docs') { renderDistros(); carousel(); }
  }

  // Lista de VMs a la izquierda del panel de Configuracion de red.
  async function renderVmList() {
    const box = $('#net-vmlist');
    try {
      const data = await (await fetch('/api/vms')).json();
      if (!data.success || !Array.isArray(data.data) || !data.data.length) {
        box.innerHTML = '<p class="placeholder">' + (data.success ? 'No hay máquinas virtuales' : esc(data.error || 'No se pudieron cargar')) + '</p>';
        return;
      }
      box.innerHTML = data.data.map(v => {
        const on = v.status === 'running';
        return `<button type="button" class="net-vm" data-vm="${esc(v.name)}" aria-pressed="${v.name === S.vm}"${on && !S.busy ? '' : ' disabled'}>` +
          `<span class="n"><span class="st${on ? ' on' : ''}"></span>${esc(v.name)}</span><span class="m">${on ? 'encendida' : 'apagada'}${v.ip ? ' · ' + esc(v.ip) : ''}</span>` +
          (on ? '' : '<span class="ex">encienda la VM para ver su red</span>') + '</button>';
      }).join('');
    } catch (err) {
      box.innerHTML = '<p class="placeholder">Error: ' + esc(err.message) + '</p>';
    }
  }

  // ---------- abrir una VM en el panel ----------
  // Si el agente QEMU aun no responde (VM arrancando), el panel reintenta solo
  // cada 15 s durante unos minutos en vez de dejar un error.
  const WAIT = { timer: null, tick: null, since: 0, tries: 0, max: 24 };
  function stopWaiting() { clearTimeout(WAIT.timer); clearInterval(WAIT.tick); WAIT.timer = WAIT.tick = null; }
  const agentDown = msg => /guest agent|agente QEMU|Guest Additions/i.test(msg || '');
  // Canal con el que se lee y configura la red: '' = hipervisor (Guest
  // Additions / agente QEMU), 'gestion' = SSH por la interfaz de gestión.
  function qs() { return S.channel ? '?canal=' + S.channel : ''; }
  const chSel = document.getElementById('net-channel');
  if (chSel) chSel.addEventListener('change', () => { S.channel = chSel.value; if (S.vm) open(S.vm); });

  async function open(name, { retry = false } = {}) {
    if (S.busy) return;
    if (!retry) {
      stopWaiting();
      WAIT.since = Date.now(); WAIT.tries = 0;
      showVmTab('net');
      S.vm = name; S.state = null; S.plan = null; S.mode = 'static'; S.tab = 'file';
      history.replaceState(null, '', '#vms/red/' + encodeURIComponent(name));
      document.querySelectorAll('#net-vmlist .net-vm').forEach(b => b.setAttribute('aria-pressed', b.dataset.vm === name));
      $('#net-title').textContent = 'Configuración de red de ' + name;
      $('#net-body').hidden = true;
      $('#net-run').hidden = true;
      $('#net-loading').hidden = false;
      $('#net-loading').innerHTML = '<div class="net-wait"><span class="spinner"></span> Leyendo la red de la VM ' + (S.channel ? 'por el canal de gestión (SSH)' : 'por el canal interno') + '…</div>';
      $('#net-panel').scrollIntoView({ block: 'start', behavior: 'smooth' });
    }
    if (S.vm !== name) return;
    let error = '';
    try {
      const r = await fetch('/api/vms/' + encodeURIComponent(name) + '/network' + qs());
      const data = await r.json();
      if (!data.success) throw new Error(data.error || 'no se pudo leer la red');
      S.state = data.data.state;
      S.revert = data.data.revertSeconds;
    } catch (err) {
      error = err.message;
    }
    if (S.vm !== name) return; // el usuario abrio otra VM mientras tanto
    if (error) {
      WAIT.tries++;
      if (agentDown(error)) return showWaiting(name, error);
      stopWaiting();
      $('#net-loading').innerHTML = `<div class="net-result bad"><b>No se pudo leer la red.</b> ${esc(error)}</div>` +
        `<div class="net-actions"><button type="button" class="btn-secondary" data-net-retry>Reintentar</button></div>`;
      return;
    }
    stopWaiting();
    const usable = S.state.interfaces.filter(i => !i.virtual && i.type !== 'wifi');
    if (!usable.length) {
      $('#net-loading').innerHTML = '<div class="net-result bad"><b>No hay tarjetas configurables.</b> La VM no tiene interfaces físicas con nombre reconocible.</div>';
      return;
    }
    S.iface = (usable.find(i => i.role === 'main') || usable[0]).name;
    prefill();
    $('#net-loading').hidden = true;
    $('#net-body').hidden = false;
    renderAll();
  }

  function showWaiting(name, error) {
    const giveUp = WAIT.tries >= WAIT.max;
    const elapsed = () => { const s = Math.round((Date.now() - WAIT.since) / 1000); return s < 60 ? s + ' s' : Math.floor(s / 60) + ' min ' + (s % 60) + ' s'; };
    $('#net-loading').innerHTML =
      `<div class="net-wait">${giveUp ? '' : '<span class="spinner"></span>'}<div><b>${giveUp ? 'El canal no respondió.' : 'Esperando el canal de ' + esc(name) + '…'}</b>` +
      `<div class="net-help">${giveUp ? 'Se dejó de reintentar.' : 'Se reintenta solo cada 15 s.'} Tiempo esperando: <span id="net-elapsed">${elapsed()}</span></div></div></div>` +
      `<div class="net-causes">
        <div><b>¿Acaba de encender o crear la VM?</b> Es normal: en el primer arranque cloud-init instala las Guest Additions y tarda de 3 a 7 minutos, más si el equipo anda corto de memoria.</div>
        <div><b>¿Tiene interfaz de gestión?</b> El canal de gestión (SSH) suele estar listo antes: elíjalo arriba, en <b>Canal</b>.</div>
      </div>
      <div class="net-actions">
        <button type="button" class="btn-secondary" data-net-retry>Reintentar ahora</button>
      </div>
      <details class="net-raw"><summary>Detalle técnico</summary><pre class="net-term">${esc(error)}</pre></details>`;
    stopWaiting();
    WAIT.tick = setInterval(() => { const el = $('#net-elapsed'); if (el) el.textContent = elapsed(); }, 1000);
    if (!giveUp) WAIT.timer = setTimeout(() => open(name, { retry: true }), 15000);
  }

  async function repair(btn) {
    const name = S.vm;
    const ok = window.ask ? await window.ask({
      title: 'Reparar agente de ' + name,
      html: '<p>Se le conectará de nuevo la configuración de cloud-init (usuario, llave <b>' + esc(name) + '-ssh</b> y <code>qemu-guest-agent</code>) y la VM se <b>reiniciará en frío</b>. Lo que tenga abierto dentro de la VM se pierde.</p>',
      ok: 'Reparar y reiniciar', danger: true
    }) : confirm('¿Reparar y reiniciar ' + name + '?');
    if (!ok) return;
    btn.disabled = true;
    btn.textContent = 'Reparando…';
    let data;
    try { data = await (await fetch('/api/vms/' + encodeURIComponent(name) + '/repair', { method: 'POST' })).json(); }
    catch (err) { data = { success: false, error: err.message }; }
    if (!data.success) {
      btn.disabled = false; btn.textContent = 'Reparar agente (reinicia la VM)';
      if (window.toast) window.toast('error', 'No se reparó: ' + (data.error || 'error')); return;
    }
    if (window.toast) window.toast('success', 'VM reiniciada con cloud-init. El agente estará listo en 3–4 minutos; el panel se actualiza solo.');
    WAIT.since = Date.now(); WAIT.tries = 0;
    showWaiting(name, 'Reiniciada con cloud-init: esperando el primer arranque.');
  }

  // ---------- documentacion: bibliografia, distribuciones y manual ----------
  function renderBib() {
    let html = '', count = 0, open = false;
    const close = () => { if (open) html = html.replace('%N%', count + (count === 1 ? ' fuente' : ' fuentes')) + '</ol></details>'; };
    REFS.forEach(r => {
      if (r.g) { close(); html += `<details><summary>${esc(r.g)}<span class="c">%N%</span></summary><ol>`; open = true; count = 0; }
      count++;
      html += `<li id="net-ref-${r.k}"><span class="num">[${r.n}]</span><span>${r.t}<span class="u"><a href="${r.u}" target="_blank" rel="noopener">${esc(r.u.replace(/^https?:\/\//, ''))}</a>${r.man ? ' · en la VM: <code>' + esc(r.man) + '</code>' : ''}</span></span></li>`;
    });
    close();
    $('#net-bib').innerHTML = html;
    $('#net-bib details').open = true;
    // Los enlaces escritos en el HTML (<a data-ref="...">) toman la URL y su numero.
    document.querySelectorAll('a[data-ref]:not(.net-fn)').forEach(a => {
      const r = REFS[RIDX[a.dataset.ref] - 1];
      a.href = r.u; a.target = '_blank'; a.rel = 'noopener'; a.className = 'net-rf';
      a.insertAdjacentHTML('afterend', `<a class="net-fn" href="#net-ref-${r.k}" data-ref="${r.k}" title="Ver en la bibliografía">[${r.n}]</a>`);
    });
  }
  function filterBib() {
    const q = $('#net-bibq').value.trim().toLowerCase();
    document.querySelectorAll('#net-bib details').forEach(d => {
      let any = false;
      d.querySelectorAll('li').forEach(li => { const ok = !q || li.textContent.toLowerCase().includes(q); li.hidden = !ok; any = any || ok; });
      d.hidden = !any;
      if (q && any) d.open = true;
    });
  }
  function renderDistros() {
    const cur = S.state && S.state.family;
    $('#net-distros').innerHTML = Object.keys(DISTROS).filter(k => k !== 'other').map(k => {
      const D = DISTROS[k];
      return `<article class="net-distro${k === cur ? ' on' : ''}" style="--c:${D.c}"><h4>${esc(D.name)}${k === cur ? '<span class="net-chip">VM abierta</span>' : ''}</h4>` +
        `<p>${D.tool}</p><ul>${D.refs.map(r => '<li>' + R(r[0], r[1]) + '</li>').join('')}</ul></article>`;
    }).join('');
  }
  function showDoc(t) {
    document.querySelectorAll('.net-dtabs [data-dtab]').forEach(b => b.setAttribute('aria-selected', b.dataset.dtab === t));
    ['manual', 'distro', 'bib'].forEach(k => { $('#net-d-' + k).hidden = k !== t; });
    if (t === 'manual') carousel();
    if (t === 'distro') renderDistros();
  }
  // Un [n] lleva a la Bibliografia, abre su grupo y resalta la fuente.
  function goRef(k) {
    showVmTab('docs'); showDoc('bib');
    $('#net-bibq').value = ''; filterBib();
    const li = document.getElementById('net-ref-' + k);
    if (!li) return;
    li.closest('details').open = true;
    $('#net-docs').scrollIntoView({ block: 'start' });
    const box = $('#net-bibscroll');
    box.scrollTop = li.offsetTop - box.offsetTop - 8;
    li.classList.add('hit');
    setTimeout(() => li.classList.remove('hit'), 2200);
  }
  function carousel() {
    const tr = $('#net-mtrack'), cards = tr.children, w = (cards[0].offsetWidth + 12) || 1;
    const per = Math.max(1, Math.round(tr.clientWidth / w)), pages = Math.max(1, cards.length - per + 1);
    const at = Math.min(pages - 1, Math.round(tr.scrollLeft / w));
    $('#net-mdots').innerHTML = Array.from({ length: pages }, (_, n) => `<button type="button" data-n="${n}" aria-label="Ir a la tarjeta ${n + 1}"${n === at ? ' aria-current="true"' : ''}></button>`).join('');
    $('#net-mprev').disabled = at <= 0;
    $('#net-mnext').disabled = at >= pages - 1;
    $('#net-mpos').textContent = 'Tarjetas ' + (at + 1) + '–' + Math.min(cards.length, at + per) + ' de ' + cards.length;
  }
  function slide(n) { const tr = $('#net-mtrack'); tr.scrollTo({ left: n * (tr.children[0].offsetWidth + 12), behavior: 'smooth' }); }
  const cardAt = () => { const tr = $('#net-mtrack'); return Math.round(tr.scrollLeft / (tr.children[0].offsetWidth + 12)); };

  function bind() {
    if (!$('#net-panel')) return;
    renderBib();
    document.querySelectorAll('.vm-subtabs [data-vmtab]').forEach(b => b.addEventListener('click', () => showVmTab(b.dataset.vmtab)));
    $('#net-vmlist').addEventListener('click', e => {
      const b = e.target.closest('.net-vm'); if (b && !b.disabled) open(b.dataset.vm);
    });
    $('#net-ifaces').addEventListener('change', e => {
      if (e.target.name !== 'net-iface') return;
      S.iface = e.target.value; prefill(); renderAll();
    });
    $('#net-modes').addEventListener('click', e => {
      const b = e.target.closest('button'); if (!b || S.busy) return;
      S.mode = b.dataset.mode; prefill(); renderAll();
    });
    ['ip', 'mask', 'gw', 'dns', 'nets'].forEach(k => $('#net-f-' + k).addEventListener('input', e => { S.f[k] = e.target.value; hints(); requestPlan(250); }));
    $('#net-f-role').addEventListener('change', e => {
      S.f.role = e.target.value;
      if (S.f.role !== 'none' && !S.f.gw) S.f.gw = suggestGw();
      renderMode(); requestPlan(0);
    });
    $('#net-fields').addEventListener('focusin', e => {
      const row = e.target.closest('[data-k]'); if (!row || row.dataset.k === S.lf) return;
      S.lf = row.dataset.k; learnField();
    });
    $('#net-L-field').addEventListener('click', e => {
      const b = e.target.closest('[data-lf]'); if (!b) return;
      S.lf = b.dataset.lf; learnField();
    });
    ['file', 'cmd', 'route'].forEach(t => $('#net-t-' + t).addEventListener('click', () => { S.tab = t; renderPreview(); }));
    $('#net-reset').addEventListener('click', () => { if (!S.busy) { prefill(); renderAll(); } });
    $('#net-apply').addEventListener('click', apply);
    $('#net-loading').addEventListener('click', e => {
      if (e.target.closest('[data-net-retry]')) { WAIT.tries = 0; open(S.vm, { retry: true }); }
      const rb = e.target.closest('[data-net-repair]'); if (rb) repair(rb);
    });
    // Documentacion
    document.addEventListener('click', e => {
      const fn = e.target.closest('a.net-fn');
      if (fn) { e.preventDefault(); goRef(fn.dataset.ref); return; }
      const t = e.target.closest('[data-dtab]');
      if (t && t.closest('#vms-tab')) { e.preventDefault(); showDoc(t.dataset.dtab); }
    });
    $('#net-bibq').addEventListener('input', filterBib);
    $('#net-bibopen').addEventListener('click', () => document.querySelectorAll('#net-bib details').forEach(d => { d.open = true; }));
    $('#net-bibclose').addEventListener('click', () => document.querySelectorAll('#net-bib details').forEach(d => { d.open = false; }));
    $('#net-mprev').addEventListener('click', () => slide(cardAt() - 1));
    $('#net-mnext').addEventListener('click', () => slide(cardAt() + 1));
    $('#net-mdots').addEventListener('click', e => { const b = e.target.closest('[data-n]'); if (b) slide(+b.dataset.n); });
    let ct;
    $('#net-mtrack').addEventListener('scroll', () => { clearTimeout(ct); ct = setTimeout(carousel, 80); });
    window.addEventListener('resize', () => { if (!$('#vm-pane-docs').hidden) carousel(); });
    renderDistros();
  }

  window.openNetwork = open;
  window.showVmTab = showVmTab;
  window.addEventListener('DOMContentLoaded', bind);
})();
