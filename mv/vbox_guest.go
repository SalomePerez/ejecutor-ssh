package vm

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// exitMarker se imprime al final del script para conocer su codigo de
// salida sin depender de como lo traduce VBoxManage.
const exitMarker = "@@EJECUTOR-EXIT="

var reExitMarker = regexp.MustCompile(`(?m)^` + regexp.QuoteMeta(exitMarker) + `(\d+)\s*$`)

// GuestExec ejecuta un script de shell DENTRO de la VM con
// "VBoxManage guestcontrol run". El canal va del hipervisor al servicio de
// las Guest Additions (VBoxService) por el dispositivo VMMDev, NO por la red
// de la VM: por eso no se corta aunque cambie la IP. Es el equivalente de
// guest-exec de qemu-guest-agent en el proveedor de libvirt.
// Requiere la VM encendida, Guest Additions instaladas y un usuario con
// contrasena dentro de la VM (VBoxService autentica con PAM).
func (p *VBoxProvider) GuestExec(name, script string, timeout time.Duration) (*GuestExecResult, error) {
	if err := ValidateName(name); err != nil {
		return nil, err
	}
	if p.credentials == nil {
		return nil, fmt.Errorf("no hay credenciales de invitado configuradas para %s", name)
	}
	creds, err := p.credentials(name)
	if err != nil {
		return nil, err
	}
	// stderr se une a stdout dentro de la VM: VBoxManage mezcla sus propios
	// mensajes en stderr y asi no se confunden con los del script. El
	// subshell ( ) hace que un "exit N" del script no se salte la marca.
	wrapped := guestPath + "( " + script + "\n) 2>&1; echo \"" + exitMarker + "$?\""
	output, runErr := p.vbox.Run("guestcontrol", name, "run",
		"--username", creds.User, "--password", creds.Password,
		"--timeout", strconv.FormatInt(timeout.Milliseconds(), 10),
		"--wait-stdout", "--exe", "/bin/sh", "--", "sh", "-c", wrapped)
	text := strings.ReplaceAll(string(output), "\r\n", "\n")
	m := reExitMarker.FindStringSubmatchIndex(text)
	if m == nil {
		if runErr != nil {
			return nil, fmt.Errorf("las Guest Additions de %s no ejecutaron el script (¿VM encendida, Guest Additions instaladas, usuario y contraseña correctos?): %w", name, runErr)
		}
		return nil, fmt.Errorf("el script en %s no terminó (sin código de salida): %s", name, strings.TrimSpace(text))
	}
	code, _ := strconv.Atoi(text[m[2]:m[3]])
	return &GuestExecResult{ExitCode: code, Stdout: text[:m[0]]}, nil
}
