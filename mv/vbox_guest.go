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
	// El script va como argumento ($1) de un envoltorio que lo ejecuta como
	// root, igual que qemu-guest-agent: si el usuario ya es root, directo; si
	// tiene sudo sin contrasena, con sudo -n; si no, con sudo -S y la
	// contrasena del invitado, que llega por una variable de entorno del
	// proceso (no queda en la linea de comandos de la VM).
	// stderr se une a stdout dentro de la VM: VBoxManage mezcla sus propios
	// mensajes en stderr y asi no se confunden con los del script. El
	// subshell de "sh -c" hace que un "exit N" del script no se salte la marca.
	inner := guestPath + script
	wrapper := `if [ "$(id -u)" = 0 ]; then sh -c "$1"; ` +
		`elif sudo -n true 2>/dev/null; then sudo -n sh -c "$1"; ` +
		`else printf '%s\n' "$EJECUTOR_PW" | sudo -S -p '' sh -c "$1"; fi 2>&1; ` +
		`echo "` + exitMarker + `$?"`
	output, runErr := p.vbox.Run("guestcontrol", name, "run",
		"--username", creds.User, "--password", creds.Password,
		"--putenv", "EJECUTOR_PW="+creds.Password,
		"--timeout", strconv.FormatInt(timeout.Milliseconds(), 10),
		// En VirtualBox 7 lo que sigue a "--" son solo los argumentos (sin
		// argv[0]): "-- sh -c" le pasaria "sh" como script a /bin/sh. El
		// "ejecutor" que sigue al envoltorio es el $0 de ese sh.
		"--wait-stdout", "--exe", "/bin/sh", "--", "-c", wrapper, "ejecutor", inner)
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
