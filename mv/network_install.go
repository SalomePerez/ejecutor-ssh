package vm

import (
	"fmt"
	"strings"
	"time"
)

// ---------- Instalar una herramienta de consulta que falta ----------
// Algunas herramientas del catalogo ya no vienen en todas las instalaciones
// (net-tools, con arp y netstat, falta en Debian 13 y Fedora). Desde el
// mismo panel se puede instalar con el gestor de paquetes de la VM. Solo se
// instalan los paquetes de esta lista: el usuario no escribe el nombre.

type installable struct {
	bin            string // ejecutable que debe quedar disponible
	title          string // lo que ve el usuario ("net-tools")
	deb, rpm, arch string // nombre del paquete en cada familia
}

var installables = map[string]installable{
	"arp":       {bin: "arp", title: "net-tools", deb: "net-tools", rpm: "net-tools", arch: "net-tools"},
	"netstat":   {bin: "netstat", title: "net-tools", deb: "net-tools", rpm: "net-tools", arch: "net-tools"},
	"tcpdump":   {bin: "tcpdump", title: "tcpdump", deb: "tcpdump", rpm: "tcpdump", arch: "tcpdump"},
	"tracepath": {bin: "tracepath", title: "iputils (tracepath)", deb: "iputils-tracepath", rpm: "iputils", arch: "iputils"},
	"ping":      {bin: "ping", title: "iputils (ping)", deb: "iputils-ping", rpm: "iputils", arch: "iputils"},
}

// installTimeout: apt puede tener que actualizar sus listas antes de instalar.
const installTimeout = 5 * time.Minute

// Installable dice si la herramienta que falta se puede instalar desde la app
// y con que nombre se muestra.
func Installable(bin string) (string, bool) {
	t, ok := installables[bin]
	return t.title, ok
}

func installScript(t installable) string {
	return `if command -v apt-get >/dev/null 2>&1; then
  export DEBIAN_FRONTEND=noninteractive
  echo "# apt-get install -y ` + t.deb + `"
  apt-get install -y -q -o Dpkg::Use-Pty=0 ` + t.deb + ` || { echo "# apt-get update (las listas de paquetes estaban viejas)"; apt-get update -q && apt-get install -y -q -o Dpkg::Use-Pty=0 ` + t.deb + `; }
elif command -v dnf >/dev/null 2>&1; then
  echo "# dnf install -y ` + t.rpm + `"
  dnf install -y -q ` + t.rpm + `
elif command -v pacman >/dev/null 2>&1; then
  echo "# pacman -Sy --needed ` + t.arch + `"
  pacman -Sy --noconfirm --needed ` + t.arch + `
else
  echo "No se reconoce el gestor de paquetes de la VM (apt, dnf o pacman)."; exit 2
fi
command -v ` + t.bin + ` >/dev/null 2>&1 && echo "` + t.bin + ` quedó instalado: ` + "`command -v " + t.bin + "`" + `"`
}

func installTool(p netBackend, name, tool string) (*ApplyResult, error) {
	if err := ValidateName(name); err != nil {
		return nil, err
	}
	t, ok := installables[tool]
	if !ok {
		return nil, fmt.Errorf("no se puede instalar %q desde la app", tool)
	}
	step := ApplyStep{Name: "Instalar " + t.title, Status: "ok"}
	result := &ApplyResult{OK: true}
	res, err := p.GuestExec(name, installScript(t), installTimeout)
	line := ApplyLine{Cmd: "instalar " + t.title + " (apt, dnf o pacman, según la VM)", Note: "necesita que la VM tenga salida a Internet"}
	if err != nil {
		line.Output, line.Error = err.Error(), true
	} else {
		line.Output = strings.TrimRight(res.Output(), "\n")
	}
	ok = err == nil && res.ExitCode == 0 && strings.Contains(line.Output, "quedó instalado")
	if !ok {
		line.Error = true
		step.Status, result.OK = "bad", false
		result.Message = "No se pudo instalar " + t.title + ". Revise la salida: lo más común es que la VM no tenga salida a Internet (pruebe Ping a 8.8.8.8)."
	} else {
		result.Message = t.title + " quedó instalado en " + name + "."
	}
	step.Lines = []ApplyLine{line}
	result.Steps = []ApplyStep{step}
	return result, nil
}
