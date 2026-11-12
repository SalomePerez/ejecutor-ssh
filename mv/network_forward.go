package vm

import (
	"fmt"
	"strings"
)

// ---------- Reenvio IPv4 (la VM como router) ----------
// Con net.ipv4.ip_forward=1 el kernel pasa paquetes de una interfaz a otra:
// es lo que convierte a una VM con dos tarjetas en el router entre dos redes
// (escenario A - R - B). Se aplica en caliente con sysctl -w y se deja en
// /etc/sysctl.d/ para que sobreviva a un reinicio.
// Referencia: https://docs.kernel.org/networking/ip-sysctl.html

// ForwardingProvider lo implementan los proveedores que pueden activar el
// reenvio IPv4 dentro de una VM.
type ForwardingProvider interface {
	SetForwarding(name string, enabled bool) (*ApplyResult, error)
}

const forwardFile = "/etc/sysctl.d/90-ejecutor-forward.conf"

func forwardingCmds(enabled bool) []NetCmd {
	if enabled {
		return []NetCmd{
			{Cmd: "sysctl -w net.ipv4.ip_forward=1", Note: "activa el reenvío ya (en memoria)", mustOK: true},
			{Cmd: "tee " + forwardFile, Note: "lo deja escrito para que sobreviva a un reinicio",
				run: "printf '%s\\n' '# Escrito por Ejecutor SSH: la VM reenvia paquetes entre sus interfaces (router)' 'net.ipv4.ip_forward = 1' > " + forwardFile + " && cat " + forwardFile, mustOK: true},
			{Cmd: "sysctl net.ipv4.ip_forward", Note: "verifica", expect: "= 1"},
		}
	}
	return []NetCmd{
		{Cmd: "rm -f " + forwardFile, Note: "quita el archivo que escribió la app", mustOK: true},
		{Cmd: "sysctl -w net.ipv4.ip_forward=0", Note: "desactiva el reenvío ya (en memoria)", mustOK: true},
		{Cmd: "sysctl net.ipv4.ip_forward", Note: "verifica", expect: "= 0"},
		{Cmd: "grep -rs ip_forward /etc/sysctl.conf /etc/sysctl.d/", Note: "si aparece otro archivo con ip_forward = 1, volverá a activarse al reiniciar", soft: true},
	}
}

func setForwarding(p netBackend, name string, enabled bool) (*ApplyResult, error) {
	if err := ValidateName(name); err != nil {
		return nil, err
	}
	title := "Desactivar reenvío IPv4"
	if enabled {
		title = "Activar reenvío IPv4"
	}
	step := ApplyStep{Name: title, Status: "ok"}
	result := &ApplyResult{OK: true}
	for _, c := range forwardingCmds(enabled) {
		line, ok := runPlanCmd(p, name, c)
		if c.soft {
			// Solo informativo: grep sin coincidencias no es un error.
			line.Warn = strings.Contains(line.Output, "= 1")
			line.Error = false
			ok = true
		}
		step.Lines = append(step.Lines, line)
		if !ok {
			step.Status, result.OK = "bad", false
			break
		}
	}
	result.Steps = []ApplyStep{step}
	switch {
	case !result.OK:
		result.Message = "No se pudo cambiar el reenvío IPv4: revise la salida de los comandos."
	case enabled:
		result.Message = "Reenvío IPv4 activo: la VM pasa paquetes entre sus interfaces (funciona como router)."
	default:
		result.Message = "Reenvío IPv4 desactivado."
	}
	if fresh, err := readNetwork(p, name); err == nil {
		result.State = fresh
	} else if result.OK {
		result.Message += fmt.Sprintf(" (no se pudo releer la red: %v)", err)
	}
	return result, nil
}

func (p *LibvirtProvider) SetForwarding(name string, enabled bool) (*ApplyResult, error) {
	return setForwarding(p, name, enabled)
}

func (p *VBoxProvider) SetForwarding(name string, enabled bool) (*ApplyResult, error) {
	return setForwarding(p, name, enabled)
}
