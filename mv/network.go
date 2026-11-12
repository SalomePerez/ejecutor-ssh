package vm

import (
	"encoding/xml"
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

// NetState es lo que el panel de red sabe de una VM. Todo sale de la salida
// de los comandos de deteccion (Raw), nunca de suposiciones: los nombres de
// las interfaces, el gestor de red y la columna "Actual" del formulario.
type NetState struct {
	VM      string `json:"vm"`
	OS      string `json:"os"`
	Family  string `json:"family"`
	Manager string `json:"manager"`
	// Hypervisor es quien da las redes de las tarjetas: "libvirt" (vacio) o "VirtualBox".
	Hypervisor string `json:"hypervisor,omitempty"`
	// Channel es por donde se leyo la VM (Guest Additions, agente QEMU o SSH
	// por la interfaz de gestion).
	Channel string `json:"channel,omitempty"`
	// Management es la MAC de la interfaz de gestion cuando se usa ese
	// canal: el plan no permite modificarla (cortaria el canal).
	Management  string                 `json:"management,omitempty"`
	ManagerName string                 `json:"managerName"`
	Forwarding  bool                   `json:"forwarding"`
	CloudInit   bool                   `json:"cloudInit"`
	HasArping   bool                   `json:"hasArping"`
	ResolvLink  bool                   `json:"resolvLink"`
	IfupdownD   []string               `json:"ifupdownD,omitempty"`
	Interfaces  []NetIface             `json:"interfaces"`
	Libvirt     map[string]*LibvirtNet `json:"libvirt,omitempty"`
	Raw         []RawCommand           `json:"raw"`
	ReadAt      time.Time              `json:"readAt"`
}

// NetIface es una interfaz tal como la entendio la aplicacion.
type NetIface struct {
	Name    string   `json:"name"`
	MAC     string   `json:"mac"`
	Type    string   `json:"type"` // eth | wifi
	Virtual bool     `json:"virtual"`
	SSID    string   `json:"ssid,omitempty"`
	IP      string   `json:"ip"`
	Prefix  int      `json:"prefix"`
	Mode    string   `json:"mode"` // dhcp | static | none
	Extras  []string `json:"extras"`
	Role    string   `json:"role"` // main | backup | routes | none
	Gateway string   `json:"gateway"`
	Metric  int      `json:"metric"`
	Nets    []string `json:"nets"`
	DNS     string   `json:"dns"`
	Conn    string   `json:"conn,omitempty"`
}

// LibvirtNet describe la red virtual de libvirt a la que esta conectada una
// tarjeta (por MAC): sirve para validar contra el rango DHCP y los leases.
type LibvirtNet struct {
	Network   string `json:"network"`
	HostIP    string `json:"hostIP"`
	Prefix    int    `json:"prefix"`
	DHCPStart string `json:"dhcpStart,omitempty"`
	DHCPEnd   string `json:"dhcpEnd,omitempty"`
	// Isolated indica una red sin <forward>: su DHCP no entrega puerta de
	// enlace, porque el hipervisor no saca el trafico de esa red.
	Isolated bool              `json:"isolated,omitempty"`
	Leases   map[string]string `json:"leases,omitempty"`   // ip -> mac
	Reserved map[string]string `json:"reserved,omitempty"` // ip -> mac
}

// RawCommand es un comando de deteccion y su salida, para mostrarlos tal cual.
type RawCommand struct {
	Cmd    string `json:"cmd"`
	Output string `json:"output"`
}

const (
	cmdOS        = "grep -E '^(PRETTY_NAME|ID|ID_LIKE)=' /etc/os-release"
	cmdActive    = "systemctl is-active networking NetworkManager systemd-networkd"
	cmdNetplan   = "ls /etc/netplan"
	cmdIfaceD    = "ls /etc/network/interfaces.d"
	cmdLink      = "ip -br link"
	cmdSysNet    = "ls -l /sys/class/net"
	cmdWireless  = "ls -d /sys/class/net/*/wireless"
	cmdAddr      = "ip -4 -o addr show scope global"
	cmdRoute     = "ip r"
	cmdForward   = "sysctl net.ipv4.ip_forward"
	cmdResolv    = "cat /etc/resolv.conf"
	cmdResolvLnk = "test -L /etc/resolv.conf && echo enlace"
	cmdResolvctl = "resolvectl dns"
	cmdNmcli     = "nmcli -t -f DEVICE,NAME con show --active"
	cmdArping    = "command -v arping"
	cmdCloud     = "test -d /etc/cloud && echo cloud-init"
)

var detectCommands = []string{cmdOS, cmdActive, cmdNetplan, cmdIfaceD, cmdLink, cmdSysNet, cmdWireless, cmdAddr, cmdRoute, cmdForward, cmdResolv, cmdResolvLnk, cmdResolvctl, cmdNmcli, cmdArping, cmdCloud}

// detectScript arma un solo script con todos los comandos separados por
// marcas, para leer la VM con una sola llamada al agente.
func detectScript() string {
	var b strings.Builder
	for _, c := range detectCommands {
		// La marca se cita con quoteShell: varios comandos llevan comillas
		// simples y, sin escaparlas, sh fallaba con "Syntax error".
		fmt.Fprintf(&b, "echo %s; %s 2>&1; ", quoteShell("@@@ "+c), c)
	}
	return b.String()
}

func splitSections(output string) []RawCommand {
	raw := []RawCommand{}
	var current *RawCommand
	for _, line := range strings.Split(output, "\n") {
		if strings.HasPrefix(line, "@@@ ") {
			raw = append(raw, RawCommand{Cmd: strings.TrimPrefix(line, "@@@ ")})
			current = &raw[len(raw)-1]
			continue
		}
		if current != nil {
			current.Output += line + "\n"
		}
	}
	for i := range raw {
		raw[i].Output = strings.TrimRight(raw[i].Output, "\n")
	}
	return raw
}

// ReadNetwork lee la red de una VM encendida: dentro de la VM por el agente
// y, desde el hipervisor, la red de libvirt de cada tarjeta.
func (p *LibvirtProvider) ReadNetwork(name string) (*NetState, error) {
	return readNetwork(p, name)
}

// readNetwork es comun a todos los proveedores (ver netBackend).
func readNetwork(p netBackend, name string) (*NetState, error) {
	// Mismo margen que las fases de aplicar: en una VM emulada (TCG) la
	// lectura tarda 25-35 s, y con 30 s fallaba una vez si y otra no.
	res, err := p.GuestExec(name, detectScript(), guestStepTimeout)
	if err != nil {
		return nil, err
	}
	sections := splitSections(res.Stdout)
	if len(sections) == 0 {
		return nil, fmt.Errorf("el script de detección falló en %s (código %d): %s", name, res.ExitCode, strings.TrimSpace(res.Output()))
	}
	state := ParseNetState(sections)
	state.VM = name
	state.ReadAt = time.Now()
	state.Libvirt = p.hostNetworks(name)
	state.Hypervisor = p.hypervisorName()
	state.Channel = "agente QEMU (qemu-guest-agent)"
	if state.Hypervisor == "VirtualBox" {
		state.Channel = "Guest Additions de VirtualBox"
	}
	return state, nil
}

var (
	reLink     = regexp.MustCompile(`^(\S+?)(?:@\S+)?\s+\S+\s+([0-9a-f]{2}(?::[0-9a-f]{2}){5})\b`)
	reAddr     = regexp.MustCompile(`^\d+:\s+(\S+)\s+inet\s+([\d.]+)/(\d+).*?scope global( dynamic)?`)
	reDefault  = regexp.MustCompile(`^default via (\S+) dev (\S+)(?: proto (\w+))?(?:.*? metric (\d+))?`)
	reVia      = regexp.MustCompile(`^(\d+\.\d+\.\d+\.\d+/\d+) via (\S+) dev (\S+)`)
	reResolvLk = regexp.MustCompile(`Link \d+ \(([^)]+)\):\s*([\d.]+)`)
	reNS       = regexp.MustCompile(`(?m)^nameserver\s+([\d.]+)`)
	reSysNet   = regexp.MustCompile(`\s(\S+) -> (\S+)$`)
)

// ParseNetState interpreta la salida de los comandos de deteccion. Es la
// misma logica del prototipo (funcion parse), ahora del lado del servidor.
func ParseNetState(raw []RawCommand) *NetState {
	get := func(cmd string) string {
		for _, r := range raw {
			if r.Cmd == cmd {
				return r.Output
			}
		}
		return ""
	}
	st := &NetState{Raw: raw, Interfaces: []NetIface{}}

	osVals := map[string]string{}
	for _, line := range strings.Split(get(cmdOS), "\n") {
		if k, v, ok := strings.Cut(line, "="); ok {
			osVals[k] = strings.Trim(v, `"`)
		}
	}
	st.OS = osVals["PRETTY_NAME"]
	st.Family = osFamily(osVals["ID"], osVals["ID_LIKE"])

	active := strings.Fields(get(cmdActive))
	isActive := func(i int) bool { return len(active) > i && active[i] == "active" }
	netplanFiles := 0
	for _, f := range strings.Fields(get(cmdNetplan)) {
		if strings.HasSuffix(f, ".yaml") || strings.HasSuffix(f, ".yml") {
			netplanFiles++
		}
	}
	switch {
	case netplanFiles > 0 && (isActive(2) || isActive(1)):
		st.Manager, st.ManagerName = "netplan", "Netplan → systemd-networkd"
		if !isActive(2) {
			st.ManagerName = "Netplan → NetworkManager"
		}
	case isActive(1):
		st.Manager, st.ManagerName = "nm", "NetworkManager"
	case isActive(2):
		st.Manager, st.ManagerName = "networkd", "systemd-networkd"
	case isActive(0):
		st.Manager, st.ManagerName = "ifupdown", "ifupdown"
	}
	if out := get(cmdIfaceD); !strings.Contains(out, "No such file") {
		st.IfupdownD = strings.Fields(out)
	}

	virtual := map[string]bool{}
	for _, line := range strings.Split(get(cmdSysNet), "\n") {
		if m := reSysNet.FindStringSubmatch(line); m != nil {
			virtual[m[1]] = strings.Contains(m[2], "/virtual/")
		}
	}
	wifi := map[string]bool{}
	for _, f := range strings.Fields(get(cmdWireless)) {
		if strings.HasPrefix(f, "/sys/class/net/") && strings.HasSuffix(f, "/wireless") {
			wifi[strings.TrimSuffix(strings.TrimPrefix(f, "/sys/class/net/"), "/wireless")] = true
		}
	}

	type addr struct {
		ip     string
		prefix int
		dyn    bool
	}
	order := []string{}
	macs := map[string]string{}
	for _, line := range strings.Split(get(cmdLink), "\n") {
		if m := reLink.FindStringSubmatch(line); m != nil && m[1] != "lo" {
			order = append(order, m[1])
			macs[m[1]] = m[2]
		}
	}
	addrs := map[string][]addr{}
	for _, line := range strings.Split(get(cmdAddr), "\n") {
		if m := reAddr.FindStringSubmatch(line); m != nil {
			p, _ := strconv.Atoi(m[3])
			addrs[m[1]] = append(addrs[m[1]], addr{m[2], p, m[4] != ""})
		}
	}

	type route struct {
		to, gw, dev string
		metric      int
	}
	var defs, vias []route
	for _, line := range strings.Split(get(cmdRoute), "\n") {
		line = strings.TrimSpace(line)
		if m := reDefault.FindStringSubmatch(line); m != nil {
			metric, _ := strconv.Atoi(m[4])
			defs = append(defs, route{"default", m[1], m[2], metric})
		} else if m := reVia.FindStringSubmatch(line); m != nil {
			vias = append(vias, route{m[1], m[2], m[3], 0})
		}
	}
	mainDev := ""
	if len(defs) > 0 {
		best := defs[0]
		for _, d := range defs[1:] {
			if d.metric < best.metric {
				best = d
			}
		}
		mainDev = best.dev
	}

	dns := map[string]string{}
	for _, m := range reResolvLk.FindAllStringSubmatch(get(cmdResolvctl), -1) {
		dns[m[1]] = m[2]
	}
	if len(dns) == 0 && mainDev != "" {
		if m := reNS.FindStringSubmatch(get(cmdResolv)); m != nil && !strings.HasPrefix(m[1], "127.") {
			dns[mainDev] = m[1]
		}
	}
	conns := map[string]string{}
	for _, line := range strings.Split(get(cmdNmcli), "\n") {
		if dev, name, ok := strings.Cut(line, ":"); ok && dev != "" {
			conns[dev] = name
		}
	}

	for _, name := range order {
		iface := NetIface{Name: name, MAC: macs[name], Type: "eth", Virtual: virtual[name], Mode: "none", Role: "none", Extras: []string{}, Nets: []string{}, DNS: dns[name], Conn: conns[name], Prefix: 24}
		if wifi[name] {
			iface.Type = "wifi"
		}
		if list := addrs[name]; len(list) > 0 {
			iface.IP, iface.Prefix = list[0].ip, list[0].prefix
			iface.Mode = "static"
			if list[0].dyn {
				iface.Mode = "dhcp"
			}
			for _, a := range list[1:] {
				iface.Extras = append(iface.Extras, fmt.Sprintf("%s/%d", a.ip, a.prefix))
			}
		}
		for _, d := range defs {
			if d.dev == name {
				iface.Gateway, iface.Metric = d.gw, d.metric
				iface.Role = "backup"
				if name == mainDev {
					iface.Role = "main"
				}
				break
			}
		}
		for _, v := range vias {
			if v.dev == name {
				iface.Nets = append(iface.Nets, v.to)
				if iface.Role == "none" {
					iface.Role, iface.Gateway = "routes", v.gw
				}
			}
		}
		st.Interfaces = append(st.Interfaces, iface)
	}

	st.Forwarding = strings.Contains(get(cmdForward), "= 1")
	st.CloudInit = strings.Contains(get(cmdCloud), "cloud-init")
	st.HasArping = strings.Contains(get(cmdArping), "arping")
	st.ResolvLink = strings.Contains(get(cmdResolvLnk), "enlace")
	return st
}

func osFamily(id, like string) string {
	all := " " + id + " " + like + " "
	switch {
	case strings.Contains(all, " ubuntu "):
		return "ubuntu"
	case strings.Contains(all, " debian "):
		return "debian"
	case strings.Contains(all, " fedora ") && id == "fedora":
		return "fedora"
	case strings.Contains(all, " rhel ") || strings.Contains(all, " fedora ") || strings.Contains(all, " centos "):
		return "rhel"
	case strings.Contains(all, " arch "):
		return "arch"
	}
	return "other"
}

// Iface busca una interfaz por nombre.
func (s *NetState) Iface(name string) *NetIface {
	for i := range s.Interfaces {
		if s.Interfaces[i].Name == name {
			return &s.Interfaces[i]
		}
	}
	return nil
}

// ---------- Red de libvirt (lado del hipervisor) ----------

type netXML struct {
	Forward *struct{} `xml:"forward"`
	IPs     []struct {
		Address string `xml:"address,attr"`
		Netmask string `xml:"netmask,attr"`
		Prefix  int    `xml:"prefix,attr"`
		Family  string `xml:"family,attr"`
		DHCP    struct {
			Range struct {
				Start string `xml:"start,attr"`
				End   string `xml:"end,attr"`
			} `xml:"range"`
			Hosts []struct {
				MAC string `xml:"mac,attr"`
				IP  string `xml:"ip,attr"`
			} `xml:"host"`
		} `xml:"dhcp"`
	} `xml:"ip"`
}

// libvirtNetworks devuelve, por MAC, la red de libvirt de cada tarjeta. Si
// algo falla devuelve lo que pudo: es informacion para validar, no critica.
func (p *LibvirtProvider) libvirtNetworks(name string) map[string]*LibvirtNet {
	result := map[string]*LibvirtNet{}
	out, err := p.sshClient.ExecuteCommand("sudo -n virsh --connect qemu:///system domiflist " + quoteShell(name))
	if err != nil {
		return result
	}
	cache := map[string]*LibvirtNet{}
	for _, line := range strings.Split(string(out), "\n") {
		f := strings.Fields(line)
		if len(f) < 5 || f[1] != "network" {
			continue
		}
		network, mac := f[2], strings.ToLower(f[4])
		if _, ok := cache[network]; !ok {
			cache[network] = p.libvirtNetwork(network)
		}
		if cache[network] != nil {
			result[mac] = cache[network]
		}
	}
	return result
}

func (p *LibvirtProvider) libvirtNetwork(network string) *LibvirtNet {
	out, err := p.sshClient.ExecuteCommand("sudo -n virsh --connect qemu:///system net-dumpxml " + quoteShell(network))
	if err != nil {
		return nil
	}
	var def netXML
	if xml.Unmarshal(out, &def) != nil {
		return nil
	}
	ln := &LibvirtNet{Network: network, Isolated: def.Forward == nil, Leases: map[string]string{}, Reserved: map[string]string{}}
	for _, ip := range def.IPs {
		if ip.Family == "ipv6" || ip.Address == "" {
			continue
		}
		ln.HostIP = ip.Address
		ln.Prefix = ip.Prefix
		if ip.Netmask != "" {
			if bits, ok := maskToPrefix(ip.Netmask); ok {
				ln.Prefix = bits
			}
		}
		ln.DHCPStart, ln.DHCPEnd = ip.DHCP.Range.Start, ip.DHCP.Range.End
		for _, h := range ip.DHCP.Hosts {
			ln.Reserved[h.IP] = strings.ToLower(h.MAC)
		}
		break
	}
	if ln.HostIP == "" {
		return nil
	}
	if out, err := p.sshClient.ExecuteCommand("sudo -n virsh --connect qemu:///system net-dhcp-leases " + quoteShell(network)); err == nil {
		ln.Leases = parseLeases(string(out))
	}
	return ln
}

// parseLeases lee la tabla de "virsh net-dhcp-leases":
//
//	Expiry Time           MAC address         Protocol   IP address          Hostname   Client ID or DUID
//	2026-10-20 10:00:00   52:54:00:aa:bb:cc   ipv4       192.168.122.45/24   lab-01     ...
func parseLeases(output string) map[string]string {
	leases := map[string]string{}
	for _, line := range strings.Split(output, "\n") {
		f := strings.Fields(line)
		if len(f) < 5 || f[3] != "ipv4" {
			continue
		}
		ip, _, _ := strings.Cut(f[4], "/")
		leases[ip] = strings.ToLower(f[2])
	}
	return leases
}

// sortedKeys ayuda a que la salida sea estable en pruebas y en pantalla.
func sortedKeys(m map[string]string) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
