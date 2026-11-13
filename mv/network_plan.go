package vm

import (
	"encoding/base64"
	"fmt"
	"sort"
	"strings"
)

// NetRequest es lo que el usuario decide en el panel: interfaz, modo y datos.
type NetRequest struct {
	Interface string `json:"interface"`
	Mode      string `json:"mode"` // dhcp | static | extra
	IP        string `json:"ip"`
	Mask      string `json:"mask"`
	Gateway   string `json:"gateway"`
	Role      string `json:"role"` // main | backup | routes | none
	Nets      string `json:"nets"`
	DNS       string `json:"dns"`
}

// NetCheck es una linea de la validacion: ok, warn, bad (bloquea) o info.
type NetCheck struct {
	Level string `json:"level"`
	Text  string `json:"text"`
}

// IfaceConfig es como queda la interfaz despues del cambio.
type IfaceConfig struct {
	Mode    string   `json:"mode"`
	IP      string   `json:"ip"`
	Prefix  int      `json:"prefix"`
	Gateway string   `json:"gateway"`
	Role    string   `json:"role"`
	Metric  int      `json:"metric"`
	Nets    []string `json:"nets"`
	DNS     string   `json:"dns"`
	Extras  []string `json:"extras"`
	Added   string   `json:"added,omitempty"`
}

type NetFileLine struct {
	Text    string `json:"text"`
	Changed bool   `json:"changed,omitempty"`
}

type NetFile struct {
	Path  string        `json:"path"`
	Note  string        `json:"note,omitempty"`
	Lines []NetFileLine `json:"lines"`
}

// NetCmd es un comando del plan. Cmd es lo que se muestra; run, si existe, es
// lo que se ejecuta de verdad (por ejemplo, el archivo viaja en base64).
type NetCmd struct {
	Cmd  string `json:"cmd,omitempty"`
	Note string `json:"note,omitempty"`
	Host bool   `json:"host,omitempty"` // se ejecuta en el hipervisor, no en la VM

	run       string
	mustOK    bool   // el codigo de salida debe ser 0
	expect    string // texto que debe aparecer en la salida
	soft      bool   // si falla solo se advierte
	retries   int    // reintentos de 1 s (DHCP tarda en entregar la IP)
	ignoreErr bool
}

type NetPhase struct {
	Name string   `json:"name"`
	Cmds []NetCmd `json:"cmds"`
	kind string   // check | backup | write | apply | verify | close
}

type RouteLine struct {
	Text  string `json:"text"`
	State string `json:"state,omitempty"` // new | gone
}

// NetPlan es todo lo que se va a hacer, calculado antes del clic.
type NetPlan struct {
	Checks        []NetCheck   `json:"checks"`
	Valid         bool         `json:"valid"`
	Changed       bool         `json:"changed"`
	Config        *IfaceConfig `json:"config,omitempty"`
	Files         []NetFile    `json:"files,omitempty"`
	Phases        []NetPhase   `json:"phases,omitempty"`
	Routes        []RouteLine  `json:"routes,omitempty"`
	RouteSummary  string       `json:"routeSummary,omitempty"`
	RevertSeconds int          `json:"revertSeconds"`
	revertUnit    string
}

const revertUnit = "ejecutor-revertir-red"

func isDefRole(role string) bool { return role == "main" || role == "backup" }

// PlanNetwork valida la solicitud contra lo que se leyo de la VM y arma el
// plan: archivos, comandos por fase y tabla de rutas. Es una funcion pura:
// no ejecuta nada, por eso el panel la puede llamar en cada tecla.
func PlanNetwork(st *NetState, req NetRequest, revertSeconds int) *NetPlan {
	plan := &NetPlan{RevertSeconds: clampRevert(revertSeconds), revertUnit: revertUnit}
	block := false
	add := func(level, text string) {
		plan.Checks = append(plan.Checks, NetCheck{level, text})
		if level == "bad" {
			block = true
		}
	}
	i := st.Iface(req.Interface)
	if i == nil {
		add("bad", "La interfaz "+req.Interface+" no existe en la VM. Vuelva a leer la red.")
		return plan
	}
	if !ifaceNameRe.MatchString(i.Name) {
		add("bad", "Nombre de interfaz no soportado: "+i.Name)
		return plan
	}
	if i.Virtual {
		add("bad", i.Name+" es una interfaz virtual: la creó un programa (Docker, libvirt, una VPN…). Se gestiona desde ese programa, no desde aquí.")
		return plan
	}
	if i.Type == "wifi" {
		add("bad", i.Name+" es inalámbrica. Esta versión de la app todavía no configura Wi‑Fi.")
		return plan
	}
	if st.Manager == "" {
		add("bad", "No se reconoció el gestor de red de la VM (ni Netplan, ni NetworkManager, ni systemd-networkd, ni ifupdown están activos).")
		return plan
	}
	if st.Manager == "nm" && i.Conn == "" {
		add("bad", "NetworkManager no tiene una conexión activa para "+i.Name+".")
		return plan
	}

	var others []NetIface
	var mainElse *NetIface
	defElse := 0
	for k := range st.Interfaces {
		o := st.Interfaces[k]
		if o.Name == i.Name || o.Virtual {
			continue
		}
		others = append(others, o)
		if o.Role == "main" && mainElse == nil {
			mainElse = &st.Interfaces[k]
		}
		if isDefRole(o.Role) {
			defElse++
		}
	}
	if st.Management != "" && strings.EqualFold(st.Management, i.MAC) {
		add("bad", i.Name+" es la interfaz de gestión: por ella está conectada la app ("+st.Channel+"). Cambiarla cortaría el canal. Configure las interfaces de datos o use el canal de Guest Additions.")
	}
	ln := st.Libvirt[strings.ToLower(i.MAC)]
	changed := true
	var cfg *IfaceConfig

	switch req.Mode {
	case "dhcp":
		role, metric := i.Role, i.Metric
		if i.Mode != "dhcp" {
			role, metric = "main", 0
			if mainElse != nil {
				role, metric = "backup", 600
			}
		}
		gw := i.Gateway
		if ln != nil {
			gw = ln.HostIP
		}
		if ln != nil && ln.Isolated {
			// En una red aislada el DHCP no entrega puerta de enlace: no habra
			// ruta por defecto por esta tarjeta y no se debe esperar una.
			role, metric, gw = "none", 0, ""
		}
		switch {
		case i.Mode == "dhcp":
			add("info", "La interfaz ya está en DHCP: no hay nada que cambiar.")
			changed = false
		case ln != nil && ln.DHCPStart == "":
			add("warn", "La red «"+ln.Network+"» de "+hvName(st)+" no tiene DHCP. La VM podría quedar sin IP en "+i.Name+".")
		case ln != nil && ln.Isolated:
			add("ok", fmt.Sprintf("El DHCP de "+hvName(st)+" (red aislada «%s», rango %s a %s) entregará IP y máscara. No entrega puerta de enlace: por %s solo se llega a esa red.", ln.Network, ln.DHCPStart, ln.DHCPEnd, i.Name))
		case ln != nil:
			add("ok", fmt.Sprintf("El DHCP de "+hvName(st)+" (red «%s», rango %s a %s) entregará IP, máscara, puerta de enlace y DNS.", ln.Network, ln.DHCPStart, ln.DHCPEnd))
		default:
			add("warn", i.Name+" no está en una red de "+hvName(st)+": la app no puede comprobar que en esa red haya un servidor DHCP.")
		}
		if changed && role == "backup" {
			add("info", mainElse.Name+" ya tiene la ruta por defecto principal, así que la puerta de enlace que entregue el DHCP se instala con métrica 600 (respaldo).")
		}
		cfg = &IfaceConfig{Mode: "dhcp", IP: i.IP, Prefix: i.Prefix, Gateway: gw, Role: role, Metric: metric, Nets: []string{}, Extras: append([]string{}, i.Extras...)}

	case "static", "extra":
		ip := strings.TrimSpace(req.IP)
		ipn, ipOK := ipToU32(ip)
		p, maskOK := parseMask(req.Mask)
		if ip == "" {
			add("bad", "Escriba la dirección IP.")
		} else if !ipOK {
			add("bad", "\""+ip+"\" no es una IP: deben ser cuatro números de 0 a 255 separados por puntos.")
		}
		if !maskOK || p < 8 || p > 30 {
			add("bad", "Máscara no válida. Escriba 255.255.255.0 o /24.")
			maskOK = false
		}
		if !ipOK || !maskOK {
			break
		}
		nn, bc := netOf(ipn, p), bcOf(ipn, p)
		netTxt := fmt.Sprintf("%s/%d", u32ToIP(nn), p)
		switch ipn {
		case nn:
			add("bad", ip+" es la dirección de red; no se puede asignar a un equipo.")
		case bc:
			add("bad", ip+" es la dirección de broadcast; no se puede asignar a un equipo.")
		default:
			add("ok", fmt.Sprintf("IP válida en la red %s (equipos de %s a %s).", netTxt, u32ToIP(nn+1), u32ToIP(bc-1)))
		}
		if ln != nil && ln.HostIP == "" {
			// Red del hipervisor sin subred conocida (p. ej. una red interna de
			// VirtualBox sin DHCP): no hay contra que comparar.
		} else if ln != nil {
			hn, _ := ipToU32(ln.HostIP)
			lnet := netOf(hn, ln.Prefix)
			if lnet != nn || p != ln.Prefix {
				add("warn", fmt.Sprintf("La tarjeta está conectada a la red «%s» de "+hvName(st)+" (%s/%d). Con %s la VM queda configurada pero sin salida, como pasó con 192.168.10.23.", ln.Network, u32ToIP(lnet), ln.Prefix, netTxt))
			}
		} else if cur, ok := ipToU32(i.IP); ok && (netOf(cur, i.Prefix) != nn || p != i.Prefix) {
			add("warn", fmt.Sprintf("La tarjeta está hoy en %s/%d. Con %s la VM queda configurada pero sin salida si la red física es la de antes.", u32ToIP(netOf(cur, i.Prefix)), i.Prefix, netTxt))
		}
		for _, o := range others {
			on, ok := ipToU32(o.IP)
			if !ok {
				continue
			}
			min := p
			if o.Prefix < min {
				min = o.Prefix
			}
			if netOf(on, min) == netOf(nn, min) {
				add("warn", fmt.Sprintf("%s ya está en %s/%d. Dos tarjetas en la misma red confunden al kernel sobre por cuál responder; cada interfaz debería ir a una red distinta.", o.Name, u32ToIP(netOf(on, o.Prefix)), o.Prefix))
			}
		}
		isCur := ip == i.IP
		inExtras := false
		for _, x := range i.Extras {
			if x == fmt.Sprintf("%s/%d", ip, p) || strings.HasPrefix(x, ip+"/") {
				inExtras = true
			}
		}
		if req.Mode == "extra" && (isCur || inExtras) {
			add("bad", ip+" ya está en esta interfaz.")
		} else if !isCur {
			if who := occupiedBy(st, ln, ip, i.MAC); who != "" {
				add("bad", ip+" ya está en uso: "+who+". Escoja otra libre.")
			} else if st.HasArping {
				add("ok", "Ninguna VM de "+hvName(st)+" tiene "+ip+". Al aplicar, la app lo confirma con arping desde la VM.")
			} else {
				add("ok", "Ninguna VM de "+hvName(st)+" tiene "+ip+". (arping no está instalado en la VM: no se puede confirmar en el cable.)")
			}
		}

		if req.Mode == "extra" {
			cfg = &IfaceConfig{Mode: i.Mode, IP: i.IP, Prefix: i.Prefix, Gateway: i.Gateway, Role: i.Role, Metric: i.Metric, Nets: append([]string{}, i.Nets...), DNS: i.DNS,
				Extras: append(append([]string{}, i.Extras...), fmt.Sprintf("%s/%d", ip, p)), Added: fmt.Sprintf("%s/%d", ip, p)}
			break
		}

		role := req.Role
		if role == "" {
			role = "main"
		}
		gw := strings.TrimSpace(req.Gateway)
		nets := []string{}
		metric := 0
		switch role {
		case "main":
			metric = 100
		case "backup":
			metric = 600
		}
		if role == "none" {
			gw = ""
			msg := fmt.Sprintf("Sin puerta de enlace: %s solo alcanza su propia red (%s).", i.Name, netTxt)
			if st.Forwarding {
				msg += " Como esta VM reenvía paquetes, los equipos de esa red deben usar " + ip + " como su puerta de enlace."
			}
			add("ok", msg)
			if i.Role == "main" && defElse == 0 {
				add("warn", "Era la única ruta por defecto: la VM se quedará sin salida fuera de sus redes (por ejemplo, sin Internet).")
			}
		} else {
			gwn, gwOK := ipToU32(gw)
			switch {
			case gw == "":
				add("bad", "Escriba la puerta de enlace, o escoja «Ninguna» si esta red no tiene salida.")
			case !gwOK:
				add("bad", "La puerta de enlace no es una IP válida.")
			case netOf(gwn, p) != nn:
				add("bad", "La puerta de enlace "+gw+" no está en "+netTxt+": el kernel no tendría cómo llegar a ella.")
			case gwn == ipn:
				add("bad", "La puerta de enlace no puede ser la misma IP de la VM.")
			}
			if ln != nil && role == "main" && gwOK && gw != ln.HostIP {
				add("warn", fmt.Sprintf("En la red «%s» de "+hvName(st)+" la salida a Internet es %s (el hipervisor). Con %s la VM puede quedar sin Internet.", ln.Network, ln.HostIP, gw))
			}
			switch role {
			case "main":
				if mainElse != nil {
					add("warn", mainElse.Name+" ya tiene la ruta por defecto principal. Con dos rutas por defecto de la misma prioridad, el tráfico sale por una sola sin que usted lo decida. Escoja «Respaldo» si esta es la salida alternativa.")
				} else if gw != "" {
					add("ok", "Ruta por defecto principal: todo lo que no sea de una red conocida sale por "+gw+".")
				}
			case "backup":
				if mainElse != nil {
					add("ok", fmt.Sprintf("Ruta por defecto de respaldo: mientras exista la de %s (métrica %d), el tráfico sale por allá; si esa ruta desaparece, entra esta.", mainElse.Name, mainElse.Metric))
				} else {
					add("info", "No hay otra ruta por defecto: aunque se llame respaldo, esta será la que use la VM.")
				}
			case "routes":
				parts := strings.FieldsFunc(req.Nets, func(r rune) bool { return r == ',' || r == ';' || r == ' ' })
				if len(parts) == 0 {
					add("bad", "Escriba al menos una red destino, por ejemplo 10.0.3.0/24.")
				}
				okAll := len(parts) > 0
				for _, s := range parts {
					a, b, found := strings.Cut(s, "/")
					n, ok := ipToU32(a)
					pp, okp := parseMask(b)
					switch {
					case !found || !ok || !okp:
						add("bad", "\""+s+"\" no es una red: escríbala como 10.0.3.0/24.")
						okAll = false
					case netOf(n, pp) != n:
						add("bad", fmt.Sprintf("%s no empieza en la dirección de red; sería %s/%d.", s, u32ToIP(netOf(n, pp)), pp))
						okAll = false
					default:
						min := pp
						if p < min {
							min = p
						}
						if netOf(n, min) == netOf(nn, min) {
							add("bad", s+" se cruza con la red de la propia interfaz ("+netTxt+"): esa no necesita puerta de enlace.")
							okAll = false
						} else {
							nets = append(nets, fmt.Sprintf("%s/%d", u32ToIP(n), pp))
						}
					}
				}
				if okAll {
					rest := ""
					if mainElse != nil {
						rest = " de " + mainElse.Name
					}
					add("ok", "El tráfico hacia "+strings.Join(nets, ", ")+" sale por "+i.Name+" vía "+gw+"; el resto sigue la ruta por defecto"+rest+".")
				}
			}
		}

		dnsOn := !(st.Manager == "ifupdown" && role != "main")
		dns := i.DNS
		if dnsOn {
			dns = strings.TrimSpace(req.DNS)
			switch {
			case dns == "" && role == "main":
				add("bad", "Escriba el DNS (resolver): sin él no se resuelven nombres.")
			case dns == "":
				add("info", "Sin DNS propio en esta interfaz: los nombres los resuelve el DNS de la interfaz principal.")
			default:
				if _, ok := ipToU32(dns); !ok {
					add("bad", "El DNS (resolver) no es una IP válida.")
				} else if st.Manager == "ifupdown" {
					if st.ResolvLink {
						add("warn", "/etc/resolv.conf es un enlace: lo maneja otro servicio, así que la app no lo sobrescribe.")
					} else {
						add("ok", "Resolver "+dns+": se escribe en /etc/resolv.conf.")
					}
				} else {
					add("ok", "Resolver "+dns+": lo toma systemd-resolved para "+i.Name+".")
				}
			}
		} else {
			who := ""
			if mainElse != nil {
				who = " (" + mainElse.Name + ")"
			}
			add("info", "Con ifupdown hay un solo DNS para toda la VM (/etc/resolv.conf) y lo pone la interfaz principal"+who+"; no se cambia.")
		}

		if ln != nil && ln.DHCPStart != "" && ipInRange(ip, ln.DHCPStart, ln.DHCPEnd) && (!isCur || i.Mode == "dhcp") {
			add("warn", fmt.Sprintf("%s está dentro del rango que reparte el DHCP de "+hvName(st)+" (%s a %s). La app la reservará para la MAC %s, para que el DHCP no se la entregue a otra VM.", ip, ln.DHCPStart, ln.DHCPEnd, i.MAC))
		}
		if i.Mode == "static" && isCur && p == i.Prefix && role == i.Role && (role == "none" || gw == i.Gateway) && dns == i.DNS && strings.Join(nets, ",") == strings.Join(i.Nets, ",") {
			add("info", "Los datos son iguales a los actuales: no hay nada que cambiar.")
			changed = false
		}
		if !isCur && i.IP != "" {
			add("info", "Las sesiones SSH abiertas hacia "+i.IP+" se cerrarán. La app no se afecta: habla con la VM por el agente QEMU, que no usa la red de la VM.")
		}
		cfg = &IfaceConfig{Mode: "static", IP: ip, Prefix: p, Gateway: gw, Role: role, Metric: metric, Nets: nets, DNS: dns, Extras: append([]string{}, i.Extras...)}

	default:
		add("bad", "Modo no válido: use dhcp, static o extra.")
	}

	if st.Manager == "ifupdown" && len(st.IfupdownD) > 0 {
		add("info", "El archivo generado no incluye /etc/network/interfaces.d ("+strings.Join(st.IfupdownD, ", ")+"): ahí suele estar la configuración de cloud-init, que de otro modo chocaría con esta.")
	}
	plan.Changed = changed
	plan.Valid = !block && changed && cfg != nil
	if plan.Valid {
		plan.Config = cfg
		plan.Files = buildFiles(st, i, cfg, req.Mode)
		plan.Phases = buildPhases(st, i, cfg, req.Mode, plan)
		plan.Routes, plan.RouteSummary = routeDiff(st, i, cfg)
	}
	return plan
}

func clampRevert(s int) int {
	switch {
	case s <= 0:
		return 120
	case s < 30:
		return 30
	case s > 600:
		return 600
	}
	return s
}

// occupiedBy dice quien usa ya una IP en la red del hipervisor (otra VM, una
// reserva o el propio hipervisor). Vacio si nadie la tiene.
func occupiedBy(st *NetState, ln *LibvirtNet, ip, mac string) string {
	if ln == nil {
		return ""
	}
	mac = strings.ToLower(mac)
	if ip == ln.HostIP {
		return "es la del hipervisor (la puerta de enlace de la red «" + ln.Network + "»)"
	}
	if m, ok := ln.Leases[ip]; ok && m != mac {
		return "el DHCP de " + hvName(st) + " se la prestó a la tarjeta " + m
	}
	if m, ok := ln.Reserved[ip]; ok && m != mac {
		return "está reservada en " + hvName(st) + " para la tarjeta " + m
	}
	return ""
}

// ---------- vista de la configuracion de cada interfaz ----------

type ifaceView struct {
	name, desc string
	sel        bool
	c          IfaceConfig
}

func currentConfig(i *NetIface) IfaceConfig {
	return IfaceConfig{Mode: i.Mode, IP: i.IP, Prefix: i.Prefix, Gateway: i.Gateway, Role: i.Role, Metric: i.Metric, Nets: i.Nets, DNS: i.DNS, Extras: i.Extras}
}

func views(st *NetState, sel *NetIface, cfg *IfaceConfig) []ifaceView {
	var out []ifaceView
	for k := range st.Interfaces {
		o := &st.Interfaces[k]
		if o.Virtual {
			continue
		}
		v := ifaceView{name: o.Name, desc: describe(o), c: currentConfig(o)}
		if o.Name == sel.Name && cfg != nil {
			v.sel, v.c = true, *cfg
		}
		out = append(out, v)
	}
	return out
}

func describe(i *NetIface) string {
	switch i.Role {
	case "main":
		return "ruta por defecto"
	case "backup":
		return "ruta por defecto de respaldo"
	case "routes":
		return "rutas hacia " + strings.Join(i.Nets, ", ")
	}
	return "sin puerta de enlace (red local)"
}

// ---------- archivos ----------

func netplanPath(iface string) string { return "/etc/netplan/90-ejecutor-" + iface + ".yaml" }
func networkdPath(iface string) string {
	// systemd-networkd usa el PRIMER archivo (en orden alfabetico) cuyo [Match]
	// coincide: con "05" gana a los de cloud-init (10-cloud-init-<iface>.network).
	return "/etc/systemd/network/05-ejecutor-" + iface + ".network"
}

const ifupdownPath = "/etc/network/interfaces"
const cloudDisablePath = "/etc/cloud/cloud.cfg.d/99-disable-network-config.cfg"

func lines(changed bool, texts ...string) []NetFileLine {
	out := make([]NetFileLine, len(texts))
	for k, t := range texts {
		out[k] = NetFileLine{Text: t, Changed: changed && !strings.HasPrefix(strings.TrimSpace(t), "#") && strings.TrimSpace(t) != ""}
	}
	return out
}

func buildFiles(st *NetState, i *NetIface, cfg *IfaceConfig, mode string) []NetFile {
	var files []NetFile
	switch st.Manager {
	case "netplan":
		files = append(files, NetFile{Path: netplanPath(i.Name), Note: "permisos 600, sangría con espacios", Lines: netplanLines(i.Name, cfg)})
		if st.CloudInit {
			files = append(files, NetFile{Path: cloudDisablePath, Note: "para que cloud-init no vuelva a escribir la red al reiniciar", Lines: lines(true, "# Escrito por Ejecutor SSH", "network: {config: disabled}")})
		}
	case "networkd":
		files = append(files, NetFile{Path: networkdPath(i.Name), Lines: networkdLines(i.Name, cfg)})
		if st.CloudInit {
			files = append(files, NetFile{Path: cloudDisablePath, Note: "para que cloud-init no vuelva a escribir la red al reiniciar", Lines: lines(true, "# Escrito por Ejecutor SSH", "network: {config: disabled}")})
		}
	case "ifupdown":
		files = append(files, NetFile{Path: ifupdownPath, Lines: ifupdownLines(st, i, cfg)})
		if mode == "static" && cfg.Role == "main" && cfg.DNS != "" && !st.ResolvLink {
			files = append(files, NetFile{Path: "/etc/resolv.conf", Lines: lines(true, "# Escrito por Ejecutor SSH", "nameserver "+cfg.DNS)})
		}
	case "nm":
		files = append(files, NetFile{Path: "/etc/NetworkManager/system-connections/" + i.Conn + ".nmconnection", Note: "vista de la conexión: la escribe nmcli, no la app", Lines: nmLines(i, cfg)})
	}
	return files
}

func netplanLines(name string, c *IfaceConfig) []NetFileLine {
	t := []string{
		"# Escrito por Ejecutor SSH. Netplan une todos los archivos de /etc/netplan",
		"# en orden alfabético: este (90-) reemplaza lo que diga 50-cloud-init.yaml.",
		"network:", "  version: 2", "  ethernets:", "    " + name + ":",
		fmt.Sprintf("      dhcp4: %t", c.Mode == "dhcp"),
	}
	if c.Mode == "dhcp" && c.Role == "backup" && c.Metric > 0 {
		t = append(t, "      dhcp4-overrides:", fmt.Sprintf("        route-metric: %d", c.Metric))
	}
	addrs := []string{}
	if c.Mode == "static" {
		addrs = append(addrs, fmt.Sprintf("%s/%d", c.IP, c.Prefix))
	}
	addrs = append(addrs, c.Extras...)
	if len(addrs) > 0 {
		t = append(t, "      addresses:")
		for _, a := range addrs {
			t = append(t, "        - "+a)
		}
	}
	var routes []string
	if c.Mode == "static" && isDefRole(c.Role) && c.Gateway != "" {
		routes = append(routes, "        - to: default", "          via: "+c.Gateway)
		if c.Metric > 0 {
			routes = append(routes, fmt.Sprintf("          metric: %d", c.Metric))
		}
	}
	if c.Role == "routes" {
		for _, n := range c.Nets {
			routes = append(routes, "        - to: "+n, "          via: "+c.Gateway)
		}
	}
	if len(routes) > 0 {
		t = append(append(t, "      routes:"), routes...)
	}
	if c.Mode == "static" && c.DNS != "" {
		t = append(t, "      nameservers:", "        addresses: ["+c.DNS+"]")
	}
	out := lines(true, t...)
	return out
}

func networkdLines(name string, c *IfaceConfig) []NetFileLine {
	t := []string{"# Escrito por Ejecutor SSH", "[Match]", "Name=" + name, "", "[Network]"}
	if c.Mode == "static" {
		t = append(t, fmt.Sprintf("Address=%s/%d", c.IP, c.Prefix))
	} else {
		t = append(t, "DHCP=ipv4")
	}
	for _, x := range c.Extras {
		t = append(t, "Address="+x)
	}
	if c.Mode == "static" && c.DNS != "" {
		t = append(t, "DNS="+c.DNS)
	}
	if c.Mode == "static" && isDefRole(c.Role) && c.Gateway != "" {
		t = append(t, "", "[Route]", "Gateway="+c.Gateway)
		if c.Metric > 0 {
			t = append(t, fmt.Sprintf("Metric=%d", c.Metric))
		}
	}
	if c.Role == "routes" {
		for _, n := range c.Nets {
			t = append(t, "", "[Route]", "Destination="+n, "Gateway="+c.Gateway)
		}
	}
	if c.Mode == "dhcp" && c.Role == "backup" && c.Metric > 0 {
		t = append(t, "", "[DHCPv4]", fmt.Sprintf("RouteMetric=%d", c.Metric))
	}
	return lines(true, t...)
}

func ifupdownLines(st *NetState, sel *NetIface, cfg *IfaceConfig) []NetFileLine {
	out := lines(false, "# Escrito por Ejecutor SSH; respaldo en interfaces.bak", "auto lo", "iface lo inet loopback")
	for _, v := range views(st, sel, cfg) {
		c := v.c
		out = append(out, lines(false, "", "# "+v.name+": "+v.desc, "allow-hotplug "+v.name)...)
		var t []string
		method := c.Mode
		if method == "none" {
			method = "manual"
		}
		if c.Mode == "static" {
			t = append(t, "auto "+v.name)
		}
		t = append(t, "iface "+v.name+" inet "+method)
		if c.Mode == "static" {
			t = append(t, fmt.Sprintf("    address %s/%d", c.IP, c.Prefix))
			if isDefRole(c.Role) && c.Gateway != "" {
				t = append(t, "    gateway "+c.Gateway)
			}
		}
		if isDefRole(c.Role) && c.Metric > 0 && (c.Mode == "static" || c.Role == "backup") {
			t = append(t, fmt.Sprintf("    metric %d", c.Metric))
		}
		if c.Role == "routes" {
			for _, n := range c.Nets {
				t = append(t, "    up ip route add "+n+" via "+c.Gateway+" dev "+v.name, "    down ip route del "+n+" via "+c.Gateway+" dev "+v.name)
			}
		}
		if len(c.Extras) > 0 {
			t = append(t, "    up "+promoteCmd(v.name))
		}
		for k, x := range c.Extras {
			t = append(t, "    up ip addr add "+x+" dev "+v.name+extraLabel(v.name, k+1), "    down ip addr del "+x+" dev "+v.name)
		}
		out = append(out, lines(v.sel, t...)...)
	}
	return out
}

// En ifupdown con DHCP, cuando el lease vence o dhclient se reinicia,
// /sbin/dhclient-script borra la tarjeta con
// "ip -4 addr flush dev enp1s0 label enp1s0". Para que la IP adicional
// sobreviva hacen falta dos cosas (probado en Debian 12):
//   - extraLabel: su propia etiqueta, para que el flush no la nombre.
//   - promoteCmd: al borrar la IP principal, el kernel borra tambien las
//     secundarias de la misma red salvo que promote_secondaries sea 1. Ubuntu
//     lo trae en 1 (sysctl.d de systemd); Debian lo deja en 0.

// extraLabel da a la k-esima IP adicional la etiqueta enp1s0:k. La :0 la usa
// dhclient para su alias, por eso se empieza en :1. Linux limita la etiqueta a
// 15 caracteres; si no cabe, la IP queda sin etiqueta.
func extraLabel(dev string, k int) string {
	label := fmt.Sprintf("%s:%d", dev, k)
	if len(label) > 15 {
		return ""
	}
	return " label " + label
}

func promoteCmd(dev string) string {
	return "sysctl -qw net.ipv4.conf." + dev + ".promote_secondaries=1"
}

func nmLines(i *NetIface, c *IfaceConfig) []NetFileLine {
	out := lines(false, "[connection]", "id="+i.Conn, "uuid=…   # se conserva", "type=ethernet", "interface-name="+i.Name, "", "[ipv4]")
	var t []string
	n := 1
	if c.Mode == "static" {
		t = append(t, "method=manual", fmt.Sprintf("address1=%s/%d", c.IP, c.Prefix))
		n = 2
		if isDefRole(c.Role) && c.Gateway != "" {
			t = append(t, "gateway="+c.Gateway)
		}
	} else {
		t = append(t, "method=auto")
	}
	for _, x := range c.Extras {
		t = append(t, fmt.Sprintf("address%d=%s", n, x))
		n++
	}
	if isDefRole(c.Role) && c.Metric > 0 {
		t = append(t, fmt.Sprintf("route-metric=%d", c.Metric))
	}
	for k, x := range c.Nets {
		if c.Role == "routes" {
			t = append(t, fmt.Sprintf("route%d=%s,%s", k+1, x, c.Gateway))
		}
	}
	if c.Role == "routes" || c.Role == "none" {
		t = append(t, "never-default=true")
	}
	if c.Mode == "static" && c.DNS != "" {
		t = append(t, "dns="+c.DNS+";")
	}
	out = append(out, lines(true, t...)...)
	return append(out, lines(false, "", "[ipv6]", "method=auto")...)
}

func fileText(f NetFile) string {
	var b strings.Builder
	for _, l := range f.Lines {
		b.WriteString(l.Text + "\n")
	}
	return b.String()
}

// writeFileCmd muestra un "tee" legible pero ejecuta la escritura en base64,
// para que ningun caracter del contenido se interprete en el shell.
func writeFileCmd(f NetFile, mode string) NetCmd {
	enc := base64.StdEncoding.EncodeToString([]byte(fileText(f)))
	run := fmt.Sprintf("mkdir -p %s && printf %%s %s | base64 -d > %s.ejecutor-nuevo && mv %s.ejecutor-nuevo %s", quoteShell(dirOf(f.Path)), enc, quoteShell(f.Path), quoteShell(f.Path), quoteShell(f.Path))
	if mode != "" {
		run += " && chmod " + mode + " " + quoteShell(f.Path)
	}
	return NetCmd{Cmd: "tee " + f.Path + " <<'EOF' … EOF", Note: "contenido de la pestaña Archivo", run: run, mustOK: true}
}

func dirOf(path string) string {
	if k := strings.LastIndex(path, "/"); k > 0 {
		return path[:k]
	}
	return "/"
}

// ---------- comandos por fase ----------

func nmArgs(c *IfaceConfig) string {
	q := func(s string) string { return quoteShell(s) }
	var b strings.Builder
	switch c.Mode {
	case "static":
		fmt.Fprintf(&b, " ipv4.method manual ipv4.addresses %s", q(strings.Join(append([]string{fmt.Sprintf("%s/%d", c.IP, c.Prefix)}, c.Extras...), ",")))
	case "dhcp":
		fmt.Fprintf(&b, " ipv4.method auto ipv4.addresses %s", q(strings.Join(c.Extras, ",")))
	default:
		if len(c.Extras) == 0 {
			b.WriteString(" ipv4.method disabled ipv4.addresses ''")
		} else {
			fmt.Fprintf(&b, " ipv4.method manual ipv4.addresses %s", q(strings.Join(c.Extras, ",")))
		}
	}
	routes := []string{}
	if c.Role == "routes" {
		for _, n := range c.Nets {
			routes = append(routes, n+" "+c.Gateway)
		}
	}
	gw := ""
	if c.Mode == "static" && isDefRole(c.Role) {
		gw = c.Gateway
	}
	fmt.Fprintf(&b, " ipv4.gateway %s ipv4.routes %s", q(gw), q(strings.Join(routes, ", ")))
	if isDefRole(c.Role) {
		metric := c.Metric
		if metric == 0 {
			metric = -1
		}
		fmt.Fprintf(&b, " ipv4.route-metric %d ipv4.never-default no", metric)
	} else {
		b.WriteString(" ipv4.never-default yes")
	}
	dns := ""
	if c.Mode == "static" {
		dns = c.DNS
	}
	fmt.Fprintf(&b, " ipv4.dns %s", q(dns))
	return b.String()
}

func buildPhases(st *NetState, i *NetIface, cfg *IfaceConfig, mode string, plan *NetPlan) []NetPhase {
	I := i.Name
	unit := plan.revertUnit
	files := buildFiles(st, i, cfg, mode)
	var phases []NetPhase

	target := cfg.IP
	if mode == "extra" {
		target, _, _ = strings.Cut(cfg.Added, "/")
	}
	check := NetPhase{Name: "Comprobar", kind: "check"}
	if mode != "dhcp" && target != i.IP {
		if st.HasArping {
			check.Cmds = append(check.Cmds, NetCmd{Cmd: "arping -D -c 2 -w 3 -I " + I + " " + target, Note: "detección de duplicados (RFC 5227): debe recibir 0 respuestas", mustOK: true})
		} else {
			check.Cmds = append(check.Cmds, NetCmd{Note: "arping no está en la VM: solo se revisaron los leases y reservas de " + hvName(st)})
		}
	} else {
		check.Cmds = append(check.Cmds, NetCmd{Note: "no hay IP nueva que comprobar"})
	}
	phases = append(phases, check)

	// Respaldo y vuelta atras programada dentro de la VM.
	var backup, restore string
	switch st.Manager {
	case "netplan":
		F := quoteShell(netplanPath(I))
		backup = fmt.Sprintf("if [ -f %s ]; then cp -p %s %s.bak; else rm -f %s.bak; fi", F, F, F, F)
		restore = fmt.Sprintf("if [ -f %s.bak ]; then cp -p %s.bak %s; else rm -f %s; fi; netplan apply", F, F, F, F)
	case "networkd":
		F := quoteShell(networkdPath(I))
		backup = fmt.Sprintf("if [ -f %s ]; then cp -p %s %s.bak; else rm -f %s.bak; fi", F, F, F, F)
		restore = fmt.Sprintf("if [ -f %s.bak ]; then cp -p %s.bak %s; else rm -f %s; fi; networkctl reload; networkctl reconfigure %s", F, F, F, F, I)
	case "ifupdown":
		backup = "cp -p /etc/network/interfaces /etc/network/interfaces.bak"
		restore = fmt.Sprintf("ifdown --force %s; cp -p /etc/network/interfaces.bak /etc/network/interfaces", I)
		if !st.ResolvLink {
			backup += " && cp -p /etc/resolv.conf /etc/resolv.conf.ejecutor.bak"
			restore += "; cp -p /etc/resolv.conf.ejecutor.bak /etc/resolv.conf"
		}
		restore += "; ifup " + I
	case "nm":
		old := currentConfig(i)
		backup = "nmcli -g ipv4.method,ipv4.addresses,ipv4.gateway,ipv4.dns con show " + quoteShell(i.Conn)
		restore = "nmcli con mod " + quoteShell(i.Conn) + nmArgs(&old) + " && nmcli con up " + quoteShell(i.Conn)
	}
	phases = append(phases, NetPhase{Name: "Respaldar", kind: "backup", Cmds: []NetCmd{
		{Cmd: backup, Note: "copia de lo que hay hoy", mustOK: true},
		{Cmd: fmt.Sprintf("systemd-run --unit=%s --on-active=%d /bin/sh -c '…'", unit, plan.RevertSeconds),
			Note: fmt.Sprintf("si nadie lo cancela en %d s, vuelve la configuración anterior", plan.RevertSeconds),
			run: fmt.Sprintf("systemctl stop %s.timer %s.service 2>/dev/null; systemctl reset-failed %s.timer %s.service 2>/dev/null; systemd-run --unit=%s --on-active=%d /bin/sh -c %s",
				unit, unit, unit, unit, unit, plan.RevertSeconds, quoteShell(restore)), mustOK: true},
	}})

	var W, A []NetCmd
	switch st.Manager {
	case "netplan":
		W = append(W, writeFileCmd(files[0], "600"))
		if len(files) > 1 {
			W = append(W, writeFileCmd(files[1], "644"))
		}
		W = append(W, NetCmd{Cmd: "netplan generate", Note: "valida el YAML antes de aplicar", mustOK: true})
		A = append(A, NetCmd{Cmd: "netplan apply", mustOK: true})
	case "networkd":
		W = append(W, writeFileCmd(files[0], "644"))
		A = append(A, NetCmd{Cmd: "networkctl reload", mustOK: true}, NetCmd{Cmd: "networkctl reconfigure " + I, mustOK: true})
	case "ifupdown":
		if mode != "extra" {
			W = append(W, NetCmd{Cmd: "ifdown " + I, Note: "se baja con la configuración vieja", run: "ifdown " + I + " || true"})
			if i.Mode == "dhcp" && mode == "static" {
				W = append(W, NetCmd{Cmd: "dhclient -r " + I + "; dhcpcd -k " + I, Note: "suelta el lease por si quedó un cliente DHCP (lección del paso 7)", run: "dhclient -r " + I + " 2>/dev/null; dhcpcd -k " + I + " 2>/dev/null; true"})
			}
		}
		for _, f := range files {
			W = append(W, writeFileCmd(f, "644"))
		}
		if mode == "extra" {
			A = append(A, NetCmd{Cmd: promoteCmd(I), Note: "si se borra la IP principal, la adicional no se va con ella", mustOK: true},
				NetCmd{Cmd: "ip addr add " + cfg.Added + " dev " + I + extraLabel(I, len(cfg.Extras)), Note: "en vivo; el archivo la hace persistente", mustOK: true})
		} else {
			A = append(A, NetCmd{Cmd: "ifup " + I, mustOK: true})
		}
	case "nm":
		W = append(W, NetCmd{Cmd: "nmcli con mod " + quoteShell(i.Conn) + nmArgs(cfg), Note: "nmcli valida y guarda; no cambia nada hasta «con up»", mustOK: true})
		A = append(A, NetCmd{Cmd: "nmcli con up " + quoteShell(i.Conn), mustOK: true})
	}
	phases = append(phases, NetPhase{Name: "Escribir", kind: "write", Cmds: W}, NetPhase{Name: "Aplicar", kind: "apply", Cmds: A})

	var V []NetCmd
	if mode == "dhcp" {
		V = append(V, NetCmd{Cmd: "ip -4 -o addr show dev " + I, Note: "el servidor DHCP entregó una IP", expect: "inet ", retries: 15})
	} else {
		V = append(V, NetCmd{Cmd: "ip -4 -o addr show dev " + I, Note: "la IP pedida está en la tarjeta", expect: "inet " + target + "/", retries: 5})
		if mode == "static" {
			// Con la misma IP, una direccion que sigue viniendo del DHCP pasaria
			// la verificacion anterior: el kernel marca esas como "dynamic".
			V = append(V, NetCmd{Cmd: "ip -4 -o addr show dev " + I + " (sin «dynamic»)", Note: "la IP es fija: ya no la entrega el DHCP",
				run: "ip -4 -o addr show dev " + I + " | grep " + quoteShell("inet "+target+"/") + " | grep -v dynamic", mustOK: true, retries: 5})
		}
	}
	if mode != "extra" {
		if isDefRole(cfg.Role) {
			exp := "dev " + I
			if mode == "static" {
				exp = "via " + cfg.Gateway + " dev " + I
			}
			V = append(V, NetCmd{Cmd: "ip r show default", Note: "la ruta por defecto quedó como se pidió", expect: exp, retries: 5})
		}
		if cfg.Role == "routes" {
			for _, n := range cfg.Nets {
				V = append(V, NetCmd{Cmd: "ip r show " + n, expect: "via " + cfg.Gateway, retries: 3})
			}
		}
		if cfg.Role != "none" && cfg.Gateway != "" {
			V = append(V, NetCmd{Cmd: "ping -c 2 -W 1 " + cfg.Gateway, Note: "la puerta de enlace responde", mustOK: true, retries: 2})
		}
		if cfg.Role == "main" {
			V = append(V, NetCmd{Cmd: "getent hosts debian.org", Note: "resuelve nombres (si falla, solo se advierte)", mustOK: true, soft: true})
		}
	} else {
		V = append(V, NetCmd{Cmd: "ping -c 2 -W 1 " + target, Note: "la IP adicional responde", mustOK: true, retries: 2})
	}
	phases = append(phases, NetPhase{Name: "Verificar", kind: "verify", Cmds: V})

	C := []NetCmd{{Cmd: "systemctl stop " + unit + ".timer", Note: "todo respondió: se cancela la reversión", run: "systemctl stop " + unit + ".timer; systemctl reset-failed " + unit + ".service 2>/dev/null; true"}}
	if ln := st.Libvirt[strings.ToLower(i.MAC)]; ln != nil && hvName(st) == "libvirt" && mode == "static" && ln.DHCPStart != "" && ipInRange(cfg.IP, ln.DHCPStart, ln.DHCPEnd) {
		host := fmt.Sprintf("<host mac='%s' ip='%s'/>", strings.ToLower(i.MAC), cfg.IP)
		C = append(C,
			NetCmd{Host: true, Cmd: "virsh net-update " + ln.Network + " delete ip-dhcp-host \"<host mac='" + strings.ToLower(i.MAC) + "'/>\" --live --config", Note: "quita una reserva vieja de esta tarjeta, si la había",
				run: "sudo -n virsh --connect qemu:///system net-update " + quoteShell(ln.Network) + " delete ip-dhcp-host " + quoteShell("<host mac='"+strings.ToLower(i.MAC)+"'/>") + " --live --config", ignoreErr: true},
			NetCmd{Host: true, Cmd: "virsh net-update " + ln.Network + " add ip-dhcp-host \"" + host + "\" --live --config", Note: "el DHCP de libvirt ya no entregará " + cfg.IP + " a otra VM",
				run: "sudo -n virsh --connect qemu:///system net-update " + quoteShell(ln.Network) + " add ip-dhcp-host " + quoteShell(host) + " --live --config", soft: true, mustOK: true})
	}
	phases = append(phases, NetPhase{Name: "Cerrar", kind: "close", Cmds: C})
	return phases
}

// ---------- tabla de rutas ----------

func routeLines(vs []ifaceView) []string {
	type def struct {
		gw, dev string
		dhcp    bool
		metric  int
	}
	var defs []def
	var out []string
	for _, v := range vs {
		if isDefRole(v.c.Role) && v.c.Gateway != "" {
			defs = append(defs, def{v.c.Gateway, v.name, v.c.Mode == "dhcp", v.c.Metric})
		}
	}
	sort.SliceStable(defs, func(a, b int) bool { return defs[a].metric < defs[b].metric })
	for _, d := range defs {
		s := "default via " + d.gw + " dev " + d.dev
		if d.dhcp {
			s += " proto dhcp"
		}
		if d.metric > 0 {
			s += fmt.Sprintf(" metric %d", d.metric)
		}
		out = append(out, s)
	}
	for _, v := range vs {
		if v.c.Role == "routes" {
			for _, n := range v.c.Nets {
				out = append(out, n+" via "+v.c.Gateway+" dev "+v.name)
			}
		}
	}
	seen := map[string]bool{}
	for _, v := range vs {
		addrs := []string{}
		if v.c.IP != "" {
			addrs = append(addrs, fmt.Sprintf("%s/%d", v.c.IP, v.c.Prefix))
		}
		addrs = append(addrs, v.c.Extras...)
		for _, a := range addrs {
			ip, ps, _ := strings.Cut(a, "/")
			n, ok := ipToU32(ip)
			if !ok {
				continue
			}
			var p int
			fmt.Sscanf(ps, "%d", &p)
			key := fmt.Sprintf("%s/%d %s", u32ToIP(netOf(n, p)), p, v.name)
			if seen[key] {
				continue
			}
			seen[key] = true
			out = append(out, fmt.Sprintf("%s/%d dev %s proto kernel scope link src %s", u32ToIP(netOf(n, p)), p, v.name, ip))
		}
	}
	return out
}

func routeDiff(st *NetState, i *NetIface, cfg *IfaceConfig) ([]RouteLine, string) {
	before := routeLines(views(st, i, nil))
	after := routeLines(views(st, i, cfg))
	in := func(list []string, s string) bool {
		for _, x := range list {
			if x == s {
				return true
			}
		}
		return false
	}
	var out []RouteLine
	defs := 0
	win := ""
	for _, l := range after {
		state := ""
		if !in(before, l) {
			state = "new"
		}
		out = append(out, RouteLine{l, state})
		if strings.HasPrefix(l, "default") {
			if defs == 0 {
				f := strings.Fields(l)
				win = f[4]
			}
			defs++
		}
	}
	for _, l := range before {
		if !in(after, l) {
			out = append(out, RouteLine{l, "gone"})
		}
	}
	switch defs {
	case 0:
		return out, "Sin ruta por defecto: la VM solo alcanza sus redes directamente conectadas."
	case 1:
		return out, "Una sola ruta por defecto, por " + win + "."
	}
	return out, fmt.Sprintf("%d rutas por defecto. Sale por %s (la de menor métrica); las demás quedan de respaldo.", defs, win)
}
