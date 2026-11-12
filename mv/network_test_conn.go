package vm

import (
	"fmt"
	"net"
	"regexp"
	"strings"
)

// ---------- Probar conectividad desde la VM ----------
// Despues de configurar la red (por ejemplo el escenario A - R - B) se
// comprueba desde dentro de la VM: ping al destino, por que interfaz y
// puerta de enlace saldria (ip route get) y por que routers pasa
// (tracepath). Todo va por el mismo canal del panel de red.

// ConnectivityProvider lo implementan los proveedores que pueden ejecutar
// pruebas de red dentro de una VM.
type ConnectivityProvider interface {
	TestConnectivity(name, target string) (*ApplyResult, error)
}

var reHostname = regexp.MustCompile(`^[a-zA-Z0-9]([a-zA-Z0-9.-]{0,251}[a-zA-Z0-9])?$`)

// validTarget acepta una IPv4 o un nombre de equipo (sin espacios ni
// caracteres de shell).
func validTarget(target string) bool {
	if ip := net.ParseIP(target); ip != nil {
		return ip.To4() != nil
	}
	return reHostname.MatchString(target)
}

func testConnectivity(p netBackend, name, target string) (*ApplyResult, error) {
	if err := ValidateName(name); err != nil {
		return nil, err
	}
	target = strings.TrimSpace(target)
	if !validTarget(target) {
		return nil, fmt.Errorf("destino no válido: escriba una IPv4 (192.168.20.10) o un nombre (debian.org)")
	}
	cmds := []NetCmd{
		{Cmd: "ping -c 3 -W 1 " + target, Note: "¿responde el destino?", mustOK: true},
		{Cmd: "ip route get " + target, Note: "por qué interfaz y puerta de enlace sale el paquete", soft: true},
		{Cmd: "tracepath -n -m 8 " + target, Note: "por qué routers pasa (si tracepath está instalado)", soft: true,
			run: "command -v tracepath >/dev/null && tracepath -n -m 8 " + target + " || echo 'tracepath no está instalado en la VM'"},
	}
	step := ApplyStep{Name: "Probar conectividad con " + target, Status: "ok"}
	result := &ApplyResult{OK: true}
	for _, c := range cmds {
		line, ok := runPlanCmd(p, name, c)
		step.Lines = append(step.Lines, line)
		if !ok && !c.soft {
			step.Status, result.OK = "bad", false
		}
	}
	result.Steps = []ApplyStep{step}
	if result.OK {
		result.Message = name + " llega a " + target + "."
	} else {
		result.Message = name + " NO llega a " + target + ": revise las rutas (ip route get) y que el router tenga el reenvío IPv4 activo."
	}
	return result, nil
}

func (p *LibvirtProvider) TestConnectivity(name, target string) (*ApplyResult, error) {
	return testConnectivity(p, name, target)
}

func (p *VBoxProvider) TestConnectivity(name, target string) (*ApplyResult, error) {
	return testConnectivity(p, name, target)
}
