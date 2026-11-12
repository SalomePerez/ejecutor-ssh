package vm

import (
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// ---------- Crear VMs en VirtualBox ----------
// Cada distribucion es una PLANTILLA: el disco de su imagen oficial de nube
// (con cloud-init) convertido a VDI, en VBoxOptions.TemplatesDir. Crear una
// VM es:
//   1. createvm + modifyvm (CPU, memoria, tarjetas, descripcion "distro=...")
//   2. clonemedium de la plantilla (y agrandarlo al tamano pedido)
//   3. conectar el disco, los discos adicionales y el ISO de Guest Additions
//   4. decirle a cloud-init donde esta su configuracion: por el numero de
//      serie DMI, "ds=nocloud;s=http://10.0.2.2:8081/cloud-init/<vm>/". Desde
//      la tarjeta NAT, 10.0.2.2 es este mismo equipo, donde la aplicacion
//      sirve user-data y meta-data (ver handlers/cloudinit.go). Asi no hace
//      falta crear un ISO de semilla en Windows.
//   5. encenderla: cloud-init crea el usuario (con contrasena, para
//      guestcontrol, y con la llave SSH) e instala las Guest Additions desde
//      el ISO, que dan el canal del panel de red.
// Referencias: https://www.virtualbox.org/manual/ch08.html
// https://docs.cloud-init.io/en/latest/reference/datasources/nocloud.html

// VBoxOptions son las rutas y datos que necesita el proveedor para crear VMs.
type VBoxOptions struct {
	TemplatesDir string // plantillas .vdi/.vmdk
	SharedDir    string // discos compartidos (shareable)
	SeedsDir     string // user-data y meta-data que sirve la app
	SeedURL      string // como ve la VM a la app, ej. http://10.0.2.2:8081
	AdditionsISO string // VBoxGuestAdditions.iso
	KeysDir      string // llaves SSH de las VMs (<vm>-ssh), para el canal de gestion
}

func (p *VBoxProvider) SetOptions(o VBoxOptions) { p.opts = o }

// ListTemplates lista los discos de plantilla (.vdi o .vmdk).
func (p *VBoxProvider) ListTemplates() ([]Template, error) {
	entries, err := os.ReadDir(p.opts.TemplatesDir)
	if err != nil {
		if os.IsNotExist(err) {
			return []Template{}, nil
		}
		return nil, fmt.Errorf("leer plantillas en %s: %w", p.opts.TemplatesDir, err)
	}
	templates := []Template{}
	for _, e := range entries {
		ext := strings.ToLower(filepath.Ext(e.Name()))
		if e.IsDir() || (ext != ".vdi" && ext != ".vmdk") {
			continue
		}
		templates = append(templates, Template{
			Name:   strings.TrimSuffix(e.Name(), filepath.Ext(e.Name())),
			Path:   filepath.Join(p.opts.TemplatesDir, e.Name()),
			Format: strings.TrimPrefix(ext, "."),
		})
	}
	return templates, nil
}

// vboxOSType es el tipo de sistema de VirtualBox (VBoxManage list ostypes).
func vboxOSType(distro string) string {
	switch {
	case strings.HasPrefix(distro, "ubuntu"):
		return "Ubuntu_64"
	case strings.HasPrefix(distro, "debian"):
		return "Debian_64"
	case strings.HasPrefix(distro, "fedora"):
		return "Fedora_64"
	case distro == "arch":
		return "ArchLinux_64"
	case strings.HasPrefix(distro, "rocky"):
		return "RedHat_64"
	}
	return "Linux_64"
}

var reSettingsFile = regexp.MustCompile(`Settings file: '([^']+)'`)

// insideDir dice si path esta dentro de dir (evita usar como plantilla un
// archivo cualquiera del equipo).
func insideDir(path, dir string) bool {
	rel, err := filepath.Rel(filepath.Clean(dir), filepath.Clean(path))
	return err == nil && rel != "." && !strings.HasPrefix(rel, "..") && !filepath.IsAbs(rel)
}

func isNAT(network string) bool { return network == natDefault || network == "default" }

func (p *VBoxProvider) Create(spec CreateSpec) (*VM, error) {
	if err := ValidateName(spec.Name); err != nil {
		return nil, err
	}
	if err := ValidateDistro(spec.Distro); err != nil {
		return nil, err
	}
	if spec.CPU <= 0 || spec.MemoryMB <= 0 || spec.DiskGB <= 0 {
		return nil, fmt.Errorf("CPU, memoria y disco deben ser mayores que cero")
	}
	if !insideDir(spec.ImagePath, p.opts.TemplatesDir) {
		return nil, fmt.Errorf("la plantilla debe estar en %s", p.opts.TemplatesDir)
	}
	if _, err := os.Stat(spec.ImagePath); err != nil {
		return nil, fmt.Errorf("no existe la plantilla %s", spec.ImagePath)
	}
	for _, d := range spec.Disks {
		if d.ReadOnly {
			return nil, fmt.Errorf("VirtualBox no tiene discos duros de solo lectura: quite esa opción del disco %s", d.Name)
		}
	}
	networks := spec.Networks
	if len(networks) == 0 {
		networks = []string{natDefault}
	}
	if !isNAT(networks[0]) {
		return nil, fmt.Errorf("la primera tarjeta debe ser NAT: por ella la VM pide su configuración (cloud-init) a la app en el primer arranque")
	}
	if len(networks) > 8 {
		return nil, fmt.Errorf("VirtualBox permite hasta 8 tarjetas")
	}
	creds := GuestCredentials{User: spec.SSHUser}
	if p.credentials != nil {
		if c, err := p.credentials(spec.Name); err == nil {
			creds = c
		}
	}
	if creds.User == "" || creds.Password == "" {
		return nil, fmt.Errorf("configure vbox.guest.user y vbox.guest.password: son el usuario con el que la app entra a las VMs")
	}
	if _, err := os.Stat(p.opts.AdditionsISO); err != nil {
		return nil, fmt.Errorf("no se encuentra el ISO de Guest Additions en %s", p.opts.AdditionsISO)
	}
	if names, err := p.listVMNames(); err == nil {
		for _, n := range names {
			if strings.EqualFold(n, spec.Name) {
				return nil, fmt.Errorf("ya existe una VM llamada %s", n)
			}
		}
	}

	// A partir de aqui, cualquier error deshace lo creado.
	registered := false
	cleanup := func() {
		if registered {
			_, _ = p.vbox.Run("unregistervm", spec.Name, "--delete")
		}
		_ = os.RemoveAll(filepath.Join(p.opts.SeedsDir, spec.Name))
	}
	fail := func(step string, err error) (*VM, error) {
		cleanup()
		return nil, fmt.Errorf("crear VM %s: %s: %w", spec.Name, step, err)
	}

	out, err := p.vbox.Run("createvm", "--name", spec.Name, "--ostype", vboxOSType(spec.Distro), "--register")
	if err != nil {
		return fail("createvm", err)
	}
	registered = true
	m := reSettingsFile.FindStringSubmatch(string(out))
	if m == nil {
		return fail("createvm", fmt.Errorf("no se encontró la carpeta de la VM en: %s", strings.TrimSpace(string(out))))
	}
	folder := filepath.Dir(m[1])

	modify := []string{"modifyvm", spec.Name,
		"--memory", strconv.Itoa(spec.MemoryMB), "--cpus", strconv.Itoa(spec.CPU),
		"--description", distroMarker + spec.Distro,
		"--firmware", "bios", "--ioapic", "on", "--rtc-use-utc", "on",
		"--graphicscontroller", "vmsvga", "--vram", "16", "--audio-enabled", "off", "--usb-ohci", "off",
		"--boot1", "disk", "--boot2", "none", "--boot3", "none", "--boot4", "none"}
	for i, network := range networks {
		args, err := nicArgs(i+1, network)
		if err != nil {
			return fail("tarjetas de red", err)
		}
		s := strconv.Itoa(i + 1)
		modify = append(modify, args...)
		modify = append(modify, "--nictype"+s, "virtio", "--cableconnected"+s, "on")
		if i == 0 {
			// Desde VirtualBox 7 la NAT no deja llegar al localhost del
			// anfitrion por 10.0.2.2 salvo que se active: sin esto cloud-init
			// no alcanza a la app (DataSourceNoCloudNet falla por tiempo).
			modify = append(modify, "--nat-localhostreachable1", "on")
		}
	}
	// Canal 3: tarjeta de gestion en solo anfitrion, con una MAC conocida
	// para que cloud-init la encuentre y la configure por DHCP.
	mgmtMAC := ""
	if spec.Management {
		if len(networks) >= 8 {
			return fail("interfaz de gestión", fmt.Errorf("no queda una tarjeta libre (máximo 8)"))
		}
		adapter, err := p.firstHostOnly()
		if err != nil {
			return fail("interfaz de gestión", err)
		}
		slot := strconv.Itoa(len(networks) + 1)
		mgmtMAC = randomVBoxMAC()
		modify = append(modify, "--nic"+slot, "hostonly", "--hostonlyadapter"+slot, adapter,
			"--nictype"+slot, "virtio", "--cableconnected"+slot, "on", "--macaddress"+slot, strings.ReplaceAll(mgmtMAC, ":", ""))
	}
	if _, err := p.vbox.Run(modify...); err != nil {
		return fail("modifyvm", err)
	}

	if _, err := p.vbox.Run("storagectl", spec.Name, "--name", "SATA", "--add", "sata", "--controller", "IntelAhci",
		"--portcount", strconv.Itoa(2+len(spec.Disks)), "--hostiocache", "on"); err != nil {
		return fail("controlador SATA", err)
	}

	system := filepath.Join(folder, spec.Name+"-system.vdi")
	if _, err := p.vbox.Run("clonemedium", "disk", spec.ImagePath, system, "--format", "VDI"); err != nil {
		return fail("copiar la plantilla", err)
	}
	if size, _ := p.mediumInfo(system); size < spec.DiskGB {
		// cloud-init agranda la particion raiz en el primer arranque (growpart).
		if _, err := p.vbox.Run("modifymedium", "disk", system, "--resize", strconv.Itoa(spec.DiskGB*1024)); err != nil {
			_, _ = p.vbox.Run("closemedium", "disk", system, "--delete")
			return fail("agrandar el disco", err)
		}
	}
	if _, err := p.vbox.Run("storageattach", spec.Name, "--storagectl", "SATA", "--port", "0", "--device", "0", "--type", "hdd", "--medium", system, "--nonrotational", "on"); err != nil {
		_, _ = p.vbox.Run("closemedium", "disk", system, "--delete")
		return fail("conectar el disco", err)
	}
	if _, err := p.vbox.Run("storageattach", spec.Name, "--storagectl", "SATA", "--port", "1", "--device", "0", "--type", "dvddrive", "--medium", p.opts.AdditionsISO); err != nil {
		return fail("conectar el ISO de Guest Additions", err)
	}

	created := make([]Disk, 0, len(spec.Disks))
	for index, d := range spec.Disks {
		diskName := strings.TrimSpace(d.Name)
		if diskName == "" {
			diskName = fmt.Sprintf("data-%d", index+1)
		}
		if err := ValidateName(diskName); err != nil {
			return fail("disco adicional "+diskName, err)
		}
		if d.SizeGB <= 0 {
			return fail("disco adicional "+diskName, fmt.Errorf("tamaño inválido"))
		}
		path := filepath.Join(folder, spec.Name+"-"+diskName+".vdi")
		if d.Shareable {
			// Un disco compartido debe ser de tamano fijo y vive fuera de la
			// carpeta de la VM (no se borra con ella).
			path = filepath.Join(p.opts.SharedDir, diskName+".vdi")
			if err := os.MkdirAll(p.opts.SharedDir, 0o755); err != nil {
				return fail("carpeta de discos compartidos", err)
			}
		}
		if _, statErr := os.Stat(path); statErr != nil {
			args := []string{"createmedium", "disk", "--filename", path, "--size", strconv.Itoa(d.SizeGB * 1024), "--format", "VDI"}
			if d.Shareable {
				args = append(args, "--variant", "Fixed")
			}
			if _, err := p.vbox.Run(args...); err != nil {
				return fail("crear disco "+diskName, err)
			}
			if d.Shareable {
				if _, err := p.vbox.Run("modifymedium", "disk", path, "--type", "shareable"); err != nil {
					return fail("marcar disco compartido "+diskName, err)
				}
			}
		}
		if _, err := p.vbox.Run("storageattach", spec.Name, "--storagectl", "SATA", "--port", strconv.Itoa(2+index), "--device", "0", "--type", "hdd", "--medium", path); err != nil {
			return fail("conectar disco "+diskName, err)
		}
		created = append(created, Disk{Name: diskName, Path: path, SizeGB: d.SizeGB, Shareable: d.Shareable})
	}

	if err := p.writeVBoxSeed(spec.Name, creds, spec.SSHPublicKey, mgmtMAC); err != nil {
		return fail("configuración de cloud-init", err)
	}
	seed := "ds=nocloud;s=" + strings.TrimRight(p.opts.SeedURL, "/") + "/cloud-init/" + spec.Name + "/"
	if _, err := p.vbox.Run("setextradata", spec.Name, "VBoxInternal/Devices/pcbios/0/Config/DmiSystemSerial", seed); err != nil {
		return fail("número de serie DMI para cloud-init", err)
	}
	if _, err := p.vbox.Run("startvm", spec.Name, "--type", "headless"); err != nil {
		return fail("encender", err)
	}

	return &VM{
		Name: spec.Name, Distro: spec.Distro, CPU: spec.CPU, MemoryMB: spec.MemoryMB, DiskGB: spec.DiskGB,
		SSHUser: creds.User, SSHPort: 22, Status: VMStatusRunning, ImagePath: system, Disks: created,
		CreatedAt: time.Now().Format(time.RFC3339),
	}, nil
}

// vboxUserData es la configuracion de cloud-init para VirtualBox: el usuario
// de la app (con contrasena, porque guestcontrol autentica con PAM, y con la
// llave SSH) puede usar sudo con su contrasena; e instala las Guest
// Additions desde el ISO conectado. Sin compilador no se construyen los
// modulos propios, pero el kernel ya trae vboxguest y el servicio funciona.
// Si mgmtMAC no esta vacia, primero configura la interfaz de gestion (canal 3)
// por DHCP, antes de instalar las Guest Additions, que tardan mas.
func vboxUserData(name string, creds GuestCredentials, publicKey, mgmtMAC string) string {
	key := ""
	if k := strings.TrimSpace(publicKey); k != "" {
		key = "\n    ssh_authorized_keys:\n      - " + strconv.Quote(k)
	}
	files, mgmtCmd := "", ""
	if mgmtMAC != "" {
		// El script va en base64 para no pelear con las comillas de YAML.
		files = "write_files:\n  - path: " + mgmtScriptPath + "\n    permissions: '0755'\n    encoding: b64\n    content: " +
			base64.StdEncoding.EncodeToString([]byte(mgmtScript(mgmtMAC))) + "\n"
		mgmtCmd = "  - [sh, " + mgmtScriptPath + "]\n"
	}
	return fmt.Sprintf(`#cloud-config
hostname: %s
users:
  - default
  - name: %s
    sudo: "ALL=(ALL) ALL"
    shell: /bin/bash
    lock_passwd: false
    plain_text_passwd: %s%s
ssh_pwauth: false
%spackage_update: true
packages:
  - bzip2
  - tar
runcmd:
%s  - [sh, -c, "mkdir -p /mnt/ga && (mount -o ro /dev/sr0 /mnt/ga || mount -o ro /dev/cdrom /mnt/ga) && sh /mnt/ga/VBoxLinuxAdditions.run --nox11; umount /mnt/ga; true"]
  - [sh, -c, "systemctl enable --now vboxadd-service || true"]
`, name, strconv.Quote(creds.User), strconv.Quote(creds.Password), key, files, mgmtCmd)
}

const mgmtScriptPath = "/usr/local/sbin/ejecutor-gestion.sh"

// mgmtScript configura la interfaz de gestion (la de esa MAC) por DHCP, sin
// ruta por defecto ni DNS, con el gestor de red que tenga la distribucion:
// Netplan (Debian/Ubuntu de nube), NetworkManager (Fedora), systemd-networkd
// (Arch) o ifupdown.
func mgmtScript(mac string) string {
	return `#!/bin/sh
# Escrito por Ejecutor SSH: interfaz de gestion (canal 3) por DHCP, sin ruta por defecto.
MAC=` + strings.ToLower(mac) + `
IF=""
for d in /sys/class/net/*; do [ "$(cat "$d/address" 2>/dev/null)" = "$MAC" ] && IF=$(basename "$d"); done
[ -n "$IF" ] || { echo "no se encontro la tarjeta $MAC"; exit 1; }
if command -v netplan >/dev/null 2>&1 && [ -d /etc/netplan ]; then
  cat > /etc/netplan/91-ejecutor-gestion.yaml <<EOF
network:
  version: 2
  ethernets:
    $IF:
      dhcp4: true
      dhcp4-overrides:
        use-routes: false
        use-dns: false
EOF
  chmod 600 /etc/netplan/91-ejecutor-gestion.yaml
  netplan apply
elif command -v nmcli >/dev/null 2>&1 && systemctl is-active --quiet NetworkManager; then
  nmcli con delete gestion >/dev/null 2>&1
  nmcli con add type ethernet ifname "$IF" con-name gestion ipv4.method auto ipv4.never-default yes ipv4.ignore-auto-dns yes ipv6.method ignore
  nmcli con up gestion
elif [ -d /etc/systemd/network ]; then
  cat > /etc/systemd/network/91-ejecutor-gestion.network <<EOF
[Match]
MACAddress=$MAC

[Network]
DHCP=ipv4

[DHCPv4]
UseRoutes=false
UseDNS=false
EOF
  systemctl enable systemd-networkd
  systemctl restart systemd-networkd
elif [ -f /etc/network/interfaces ]; then
  printf '\nauto %s\niface %s inet dhcp\n' "$IF" "$IF" >> /etc/network/interfaces
  ifup "$IF"
fi
`
}

// firstHostOnly devuelve el primer adaptador "solo anfitrion" de VirtualBox.
func (p *VBoxProvider) firstHostOnly() (string, error) {
	out, err := p.vbox.Run("list", "hostonlyifs")
	if err != nil {
		return "", err
	}
	for _, b := range parseBlocks(string(out), "Name") {
		if b["Name"] != "" {
			return b["Name"], nil
		}
	}
	return "", fmt.Errorf("VirtualBox no tiene una red «solo anfitrión»: créela en Archivo → Herramientas → Administrador de red")
}

// randomVBoxMAC genera una MAC con el prefijo de VirtualBox (08:00:27).
func randomVBoxMAC() string {
	b := make([]byte, 3)
	_, _ = rand.Read(b)
	return fmt.Sprintf("08:00:27:%02x:%02x:%02x", b[0], b[1], b[2])
}

func (p *VBoxProvider) writeVBoxSeed(name string, creds GuestCredentials, publicKey, mgmtMAC string) error {
	dir := filepath.Join(p.opts.SeedsDir, name)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	meta := fmt.Sprintf("instance-id: %s-%d\nlocal-hostname: %s\n", name, time.Now().Unix(), name)
	if err := os.WriteFile(filepath.Join(dir, "meta-data"), []byte(meta), 0o600); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, "user-data"), []byte(vboxUserData(name, creds, publicKey, mgmtMAC)), 0o600)
}

// SeedFile devuelve un archivo de cloud-init de una VM (meta-data,
// user-data o vendor-data, este ultimo vacio).
func (p *VBoxProvider) SeedFile(name, file string) ([]byte, error) {
	if err := ValidateName(name); err != nil {
		return nil, err
	}
	switch file {
	case "vendor-data", "network-config":
		if file == "vendor-data" {
			return []byte{}, nil
		}
		return nil, os.ErrNotExist
	case "meta-data", "user-data":
		return os.ReadFile(filepath.Join(p.opts.SeedsDir, name, file))
	}
	return nil, os.ErrNotExist
}
