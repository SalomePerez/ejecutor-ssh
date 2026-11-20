package vm

import (
	"fmt"
	"regexp"
	"strings"
)

// ---------- Comandos de red (consulta y diagnostico) ----------
// Este modulo NO cambia la configuracion de la VM: solo consulta y
// diagnostica (tabla de rutas, ping, conexiones, puertos, ARP, captura de
// paquetes). Va por el mismo canal del panel de red (canal interno o canal
// de gestion). El usuario no escribe la linea de comandos: escoge un comando
// de este catalogo y, si el comando lo pide, un destino, una interfaz o un
// filtro, que se validan antes de armar la linea.

// NetCmdRequest es lo que pide el panel de Comandos de red.
type NetCmdRequest struct {
	Cmd    string `json:"cmd"`
	Target string `json:"target,omitempty"` // ping, tracepath
	Iface  string `json:"iface,omitempty"`  // tcpdump
	Count  int    `json:"count,omitempty"`  // tcpdump: paquetes (1-50)
	Filter string `json:"filter,omitempty"` // tcpdump: uno de tcpdumpFilters
}

// NetCmdInfo describe un comando del catalogo para la interfaz web.
type NetCmdInfo struct {
	ID      string   `json:"id"`
	Title   string   `json:"title"`
	Group   string   `json:"group"`
	Example string   `json:"example"`
	Package string   `json:"package"`
	Needs   []string `json:"needs,omitempty"` // "target", "iface"
}

// NetCommandProvider lo implementan los canales que pueden ejecutar los
// comandos de consulta.
type NetCommandProvider interface {
	RunNetCommand(name string, req NetCmdRequest) (*ApplyResult, error)
}

type netCmdDef struct {
	info NetCmdInfo
	bin  string // ejecutable que debe existir en la VM
	pkg  string // paquete que lo trae, por familia
	// build arma los comandos (lo que se muestra y lo que corre).
	build func(req NetCmdRequest) []NetCmd
}

// tcpdumpFilters son los filtros de captura permitidos (expresiones pcap
// fijas: el usuario no escribe la expresion).
var tcpdumpFilters = map[string]string{
	"":       "",
	"icmp":   "icmp",
	"arp":    "arp",
	"ssh":    "tcp port 22",
	"dns":    "port 53",
	"dhcp":   "port 67 or port 68",
	"no-ssh": "not tcp port 22",
}

// TcpdumpFilters devuelve los filtros permitidos en el orden de la interfaz.
func TcpdumpFilters() []string {
	return []string{"", "icmp", "arp", "ssh", "dns", "dhcp", "no-ssh"}
}

var reIfaceName = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9._-]{0,14}$`)

// pkgNote dice como instalar lo que falta en cada familia.
func pkgNote(bin, pkg string) string {
	return fmt.Sprintf("%s no está instalado en la VM. Viene en el paquete %s: apt install %s (Debian/Ubuntu), dnf install %s (Fedora) o pacman -S %s (Arch).", bin, pkg, pkg, pkg, pkg)
}

// guarded antepone la comprobacion de que el ejecutable existe; si falta,
// sale con 127 y la marca para que el panel lo muestre como advertencia.
func guarded(bin, cmd string) string {
	return "command -v " + bin + " >/dev/null 2>&1 || { echo '" + missingMarker + "'; exit 127; }; " + cmd
}

const missingMarker = "__EJECUTOR_FALTA__"

var netCmdCatalog = []netCmdDef{
	{info: NetCmdInfo{ID: "addr", Title: "Direcciones e interfaces", Group: "Consultar", Example: "ip addr", Package: "iproute2"},
		bin: "ip", pkg: "iproute2",
		build: func(NetCmdRequest) []NetCmd {
			return []NetCmd{{Cmd: "ip -br addr", Note: "resumen: una línea por interfaz"}, {Cmd: "ip addr", Note: "detalle: MAC, MTU, estado y direcciones"}}
		}},
	{info: NetCmdInfo{ID: "route", Title: "Tabla de rutas", Group: "Consultar", Example: "ip route", Package: "iproute2"},
		bin: "ip", pkg: "iproute2",
		build: func(NetCmdRequest) []NetCmd {
			return []NetCmd{{Cmd: "ip route", Note: "rutas IPv4 de la tabla principal"}}
		}},
	{info: NetCmdInfo{ID: "neigh", Title: "Vecinos (ARP)", Group: "Consultar", Example: "ip neigh", Package: "iproute2"},
		bin: "ip", pkg: "iproute2",
		build: func(NetCmdRequest) []NetCmd {
			return []NetCmd{{Cmd: "ip neigh", Note: "IP → MAC que la VM ya conoce en sus redes"}}
		}},
	{info: NetCmdInfo{ID: "arp", Title: "Tabla ARP (clásico)", Group: "Consultar", Example: "arp -a", Package: "net-tools"},
		bin: "arp", pkg: "net-tools",
		build: func(NetCmdRequest) []NetCmd {
			return []NetCmd{{Cmd: "arp -a", Note: "la misma tabla que ip neigh, con la herramienta antigua"}}
		}},
	{info: NetCmdInfo{ID: "ss", Title: "Puertos en escucha", Group: "Conexiones", Example: "ss -tulnp", Package: "iproute2"},
		bin: "ss", pkg: "iproute2",
		build: func(NetCmdRequest) []NetCmd {
			return []NetCmd{{Cmd: "ss -tulnp", Note: "TCP y UDP en escucha, con el proceso que los abre"}}
		}},
	{info: NetCmdInfo{ID: "conns", Title: "Conexiones TCP", Group: "Conexiones", Example: "ss -tan", Package: "iproute2"},
		bin: "ss", pkg: "iproute2",
		build: func(NetCmdRequest) []NetCmd {
			return []NetCmd{{Cmd: "ss -tan", Note: "todas las conexiones TCP y su estado"}}
		}},
	{info: NetCmdInfo{ID: "netstat", Title: "Conexiones (clásico)", Group: "Conexiones", Example: "netstat -ant", Package: "net-tools"},
		bin: "netstat", pkg: "net-tools",
		build: func(NetCmdRequest) []NetCmd {
			return []NetCmd{{Cmd: "netstat -ant", Note: "lo mismo que ss -tan, con la herramienta antigua"}}
		}},
	{info: NetCmdInfo{ID: "routeget", Title: "¿Por dónde sale?", Group: "Diagnosticar", Example: "ip route get <destino>", Package: "iproute2", Needs: []string{"target"}},
		bin: "ip", pkg: "iproute2",
		build: func(r NetCmdRequest) []NetCmd {
			return []NetCmd{{Cmd: "ip route get " + r.Target, Note: "la ruta que escoge el kernel: interfaz, puerta de enlace e IP de origen"}}
		}},
	{info: NetCmdInfo{ID: "ping", Title: "Ping", Group: "Diagnosticar", Example: "ping -c 4 <destino>", Package: "iputils", Needs: []string{"target"}},
		bin: "ping", pkg: "iputils-ping",
		build: func(r NetCmdRequest) []NetCmd {
			return []NetCmd{
				{Cmd: "ping -c 4 -W 1 " + r.Target, Note: "¿responde el destino?", mustOK: true},
				{Cmd: "ip route get " + r.Target, Note: "por qué interfaz y puerta de enlace sale el paquete", soft: true},
			}
		}},
	{info: NetCmdInfo{ID: "tracepath", Title: "Camino hasta el destino", Group: "Diagnosticar", Example: "tracepath -n <destino>", Package: "iputils", Needs: []string{"target"}},
		bin: "tracepath", pkg: "iputils-tracepath",
		build: func(r NetCmdRequest) []NetCmd {
			return []NetCmd{{Cmd: "tracepath -n -m 8 " + r.Target, Note: "por qué routers pasa (hasta 8 saltos)", soft: true}}
		}},
	{info: NetCmdInfo{ID: "tcpdump", Title: "Captura de paquetes", Group: "Diagnosticar", Example: "tcpdump -i <interfaz>", Package: "tcpdump", Needs: []string{"iface"}},
		bin: "tcpdump", pkg: "tcpdump",
		build: func(r NetCmdRequest) []NetCmd {
			cmd := fmt.Sprintf("tcpdump -i %s -n -l -c %d", r.Iface, r.Count)
			if f := tcpdumpFilters[r.Filter]; f != "" {
				cmd += " '" + f + "'"
			}
			// timeout corta la captura si no pasan suficientes paquetes.
			return []NetCmd{{Cmd: cmd, Note: fmt.Sprintf("hasta %d paquetes o %d s, lo que ocurra primero", r.Count, tcpdumpSeconds), soft: true,
				run: fmt.Sprintf("timeout %d %s 2>&1; [ $? -eq 124 ] && echo '(se acabó el tiempo de captura)'; true", tcpdumpSeconds, cmd)}}
		}},
}

const tcpdumpSeconds = 15

// NetCommands devuelve el catalogo para la interfaz web.
func NetCommands() []NetCmdInfo {
	out := make([]NetCmdInfo, len(netCmdCatalog))
	for i, d := range netCmdCatalog {
		out[i] = d.info
	}
	return out
}

func findNetCmd(id string) *netCmdDef {
	for i := range netCmdCatalog {
		if netCmdCatalog[i].info.ID == id {
			return &netCmdCatalog[i]
		}
	}
	return nil
}

// validateNetCmd revisa la solicitud y completa los valores por defecto.
func validateNetCmd(req *NetCmdRequest) (*netCmdDef, error) {
	d := findNetCmd(req.Cmd)
	if d == nil {
		return nil, fmt.Errorf("comando no reconocido: %q", req.Cmd)
	}
	for _, n := range d.info.Needs {
		switch n {
		case "target":
			req.Target = strings.TrimSpace(req.Target)
			if !validTarget(req.Target) {
				return nil, fmt.Errorf("destino no válido: escriba una IPv4 (192.168.20.10) o un nombre (debian.org)")
			}
		case "iface":
			req.Iface = strings.TrimSpace(req.Iface)
			if !reIfaceName.MatchString(req.Iface) {
				return nil, fmt.Errorf("interfaz no válida: %q", req.Iface)
			}
			if req.Count == 0 {
				req.Count = 10
			}
			if req.Count < 1 || req.Count > 50 {
				return nil, fmt.Errorf("la captura admite de 1 a 50 paquetes")
			}
			if _, ok := tcpdumpFilters[req.Filter]; !ok {
				return nil, fmt.Errorf("filtro de captura no reconocido: %q", req.Filter)
			}
		}
	}
	return d, nil
}

func runNetCommand(p netBackend, name string, req NetCmdRequest) (*ApplyResult, error) {
	if err := ValidateName(name); err != nil {
		return nil, err
	}
	d, err := validateNetCmd(&req)
	if err != nil {
		return nil, err
	}
	step := ApplyStep{Name: d.info.Title, Status: "ok"}
	result := &ApplyResult{OK: true}
	for _, c := range d.build(req) {
		run := c.run
		if run == "" {
			run = c.Cmd
		}
		c.run = guarded(d.bin, run)
		line, ok := runPlanCmd(p, name, c)
		if strings.Contains(line.Output, missingMarker) {
			line.Output = pkgNote(d.bin, d.pkg)
			step.Lines = append(step.Lines, line)
			step.Status = "warn"
			result.Message = pkgNote(d.bin, d.pkg)
			result.Steps = []ApplyStep{step}
			return result, nil
		}
		step.Lines = append(step.Lines, line)
		if !ok && !c.soft {
			step.Status, result.OK = "bad", false
		}
	}
	result.Steps = []ApplyStep{step}
	switch {
	case d.info.ID == "ping" && result.OK:
		result.Message = name + " llega a " + req.Target + "."
	case d.info.ID == "ping":
		result.Message = name + " NO llega a " + req.Target + ": revise las rutas (ip route get) y que el router tenga el reenvío IPv4 activo."
	case !result.OK:
		result.Message = "El comando terminó con error."
	}
	return result, nil
}

func (p *LibvirtProvider) RunNetCommand(name string, req NetCmdRequest) (*ApplyResult, error) {
	return runNetCommand(p, name, req)
}

func (p *VBoxProvider) RunNetCommand(name string, req NetCmdRequest) (*ApplyResult, error) {
	return runNetCommand(p, name, req)
}

func (o mgmtOps) RunNetCommand(name string, req NetCmdRequest) (*ApplyResult, error) {
	b, err := o.backend(name)
	if err != nil {
		return nil, err
	}
	defer b.Close()
	return runNetCommand(b, name, req)
}
