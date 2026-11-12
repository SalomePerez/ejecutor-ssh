package vm

import (
	"bufio"
	"fmt"
	"os/exec"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

// ---------- Proveedor VirtualBox ----------
// VirtualBox es el hipervisor real: corre en el mismo equipo que la
// aplicacion y se controla con VBoxManage. A diferencia del proveedor de
// libvirt, aqui no hay una VM intermedia ni SSH hacia el hipervisor: cada
// operacion es una llamada local a VBoxManage con sus argumentos separados
// (sin shell), asi que no hace falta escapar comillas.
// Referencia: https://www.virtualbox.org/manual/ch08.html

// vboxRunner ejecuta VBoxManage. En las pruebas se reemplaza por un doble.
type vboxRunner interface {
	Run(args ...string) ([]byte, error)
}

// VBoxManageRunner ejecuta el VBoxManage instalado en el equipo.
type VBoxManageRunner struct {
	// Path es la ruta del ejecutable; vacio = "VBoxManage" (en el PATH).
	Path string
}

func (r VBoxManageRunner) Run(args ...string) ([]byte, error) {
	path := r.Path
	if path == "" {
		path = "VBoxManage"
	}
	output, err := exec.Command(path, args...).CombinedOutput()
	if err != nil {
		return output, fmt.Errorf("VBoxManage %s: %w: %s", firstArg(args), err, strings.TrimSpace(string(output)))
	}
	return output, nil
}

func firstArg(args []string) string {
	if len(args) == 0 {
		return ""
	}
	return args[0]
}

// GuestCredentials son el usuario y la contrasena DENTRO de la VM con los
// que guestcontrol ejecuta procesos (VBoxService los valida con PAM).
type GuestCredentials struct {
	User     string
	Password string
}

type VBoxProvider struct {
	vbox vboxRunner
	// credentials devuelve las credenciales de invitado de una VM.
	credentials func(vmName string) (GuestCredentials, error)
	sleep       func(time.Duration)
}

func NewVBoxProvider(runner vboxRunner, credentials func(string) (GuestCredentials, error)) *VBoxProvider {
	return &VBoxProvider{vbox: runner, credentials: credentials}
}

var _ Provider = (*VBoxProvider)(nil)

// parseMachineReadable interpreta la salida de "showvminfo --machinereadable"
// (lineas clave=valor, con las claves o los valores entre comillas).
func parseMachineReadable(output string) map[string]string {
	values := map[string]string{}
	scanner := bufio.NewScanner(strings.NewReader(output))
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for scanner.Scan() {
		line := strings.TrimRight(scanner.Text(), "\r")
		key, value, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		key = strings.Trim(key, `"`)
		value = strings.Trim(value, `"`)
		values[key] = strings.ReplaceAll(value, `\\`, `\`)
	}
	return values
}

var reListVM = regexp.MustCompile(`^"(.*)" \{([0-9a-fA-F-]+)\}$`)

// listVMNames lee "VBoxManage list vms".
func (p *VBoxProvider) listVMNames() ([]string, error) {
	output, err := p.vbox.Run("list", "vms")
	if err != nil {
		return nil, fmt.Errorf("listar VMs de VirtualBox: %w", err)
	}
	names := []string{}
	for _, line := range strings.Split(string(output), "\n") {
		if m := reListVM.FindStringSubmatch(strings.TrimSpace(line)); m != nil {
			names = append(names, m[1])
		}
	}
	return names, nil
}

func (p *VBoxProvider) info(name string) (map[string]string, error) {
	output, err := p.vbox.Run("showvminfo", name, "--machinereadable")
	if err != nil {
		return nil, fmt.Errorf("leer VM %s: %w", name, err)
	}
	return parseMachineReadable(string(output)), nil
}

// vboxStatus traduce VMState de VirtualBox al estado de la aplicacion.
func vboxStatus(state string) VMStatus {
	switch state {
	case "running":
		return VMStatusRunning
	case "poweroff", "saved", "aborted", "paused":
		return VMStatusStopped
	case "starting", "restoring":
		return VMStatusCreating
	default:
		return VMStatusError
	}
}

// distroMarker es el prefijo con el que la aplicacion guarda la
// distribucion en la descripcion de la VM (modifyvm --description).
const distroMarker = "distro="

func distroFromDescription(description string) string {
	for _, line := range strings.Split(description, "\n") {
		if d, ok := strings.CutPrefix(strings.TrimSpace(line), distroMarker); ok && ValidateDistro(d) == nil {
			return d
		}
	}
	return ""
}

// vboxNics lee las tarjetas configuradas (nic1..nic8 distintas de "none").
func vboxNics(values map[string]string) []VMNic {
	nics := []VMNic{}
	for n := 1; n <= 8; n++ {
		kind := values[fmt.Sprintf("nic%d", n)]
		if kind == "" || kind == "none" {
			continue
		}
		network := kind
		switch kind {
		case "bridged":
			network = "puente:" + values[fmt.Sprintf("bridgeadapter%d", n)]
		case "intnet":
			network = "interna:" + values[fmt.Sprintf("intnet%d", n)]
		case "hostonly":
			network = "solo-anfitrion:" + values[fmt.Sprintf("hostonlyadapter%d", n)]
		case "natnetwork":
			network = "red-nat:" + values[fmt.Sprintf("nat-network%d", n)]
		}
		nics = append(nics, VMNic{
			Network: network,
			MAC:     formatMAC(values[fmt.Sprintf("macaddress%d", n)]),
			Model:   values[fmt.Sprintf("nictype%d", n)],
		})
	}
	return nics
}

// formatMAC convierte 080027BD31FD en 08:00:27:bd:31:fd.
func formatMAC(raw string) string {
	raw = strings.ToLower(strings.ReplaceAll(raw, ":", ""))
	if len(raw) != 12 {
		return raw
	}
	parts := make([]string, 6)
	for i := range parts {
		parts[i] = raw[2*i : 2*i+2]
	}
	return strings.Join(parts, ":")
}

// attachedDisks devuelve las rutas de los discos duros conectados, en orden
// de controlador y puerto. Los DVD (ISO) se ignoran.
func attachedDisks(values map[string]string) []string {
	type slot struct{ key, path string }
	slots := []slot{}
	for key, value := range values {
		if strings.Contains(key, "ImageUUID") || !strings.Contains(key, "-") {
			continue
		}
		// Claves de medio: "<controlador>-<puerto>-<dispositivo>".
		parts := strings.Split(key, "-")
		if len(parts) < 3 || values[strings.Join(parts[:len(parts)-2], "-")+"-ImageUUID-"+strings.Join(parts[len(parts)-2:], "-")] == "" {
			continue
		}
		lower := strings.ToLower(value)
		if value == "none" || value == "emptydrive" || strings.HasSuffix(lower, ".iso") {
			continue
		}
		slots = append(slots, slot{key, value})
	}
	sort.Slice(slots, func(i, j int) bool { return slots[i].key < slots[j].key })
	paths := make([]string, len(slots))
	for i, s := range slots {
		paths[i] = s.path
	}
	return paths
}

// mediumInfo lee capacidad y tipo de un disco con "showmediuminfo".
func (p *VBoxProvider) mediumInfo(path string) (sizeGB int, kind string) {
	output, err := p.vbox.Run("showmediuminfo", "disk", path)
	if err != nil {
		return 0, ""
	}
	for _, line := range strings.Split(string(output), "\n") {
		key, value, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		value = strings.TrimSpace(value)
		switch strings.TrimSpace(key) {
		case "Capacity":
			if fields := strings.Fields(value); len(fields) > 0 {
				if mb, err := strconv.Atoi(fields[0]); err == nil {
					sizeGB = (mb + 1023) / 1024
				}
			}
		case "Type":
			kind = strings.Fields(value + " ")[0]
		}
	}
	return sizeGB, kind
}

// guestIP lee la primera IPv4 que reportan las Guest Additions. Sin Guest
// Additions (o con la VM apagada) devuelve "".
func (p *VBoxProvider) guestIP(name string) string {
	output, err := p.vbox.Run("guestproperty", "enumerate", name, "--no-timestamp", "--no-flags", "/VirtualBox/GuestInfo/Net/*/V4/IP")
	if err != nil {
		return ""
	}
	return parseGuestIPs(string(output))
}

var reGuestIP = regexp.MustCompile(`/VirtualBox/GuestInfo/Net/(\d+)/V4/IP\s*=\s*'([\d.]+)'`)

func parseGuestIPs(output string) string {
	best, bestIndex := "", -1
	for _, m := range reGuestIP.FindAllStringSubmatch(output, -1) {
		index, _ := strconv.Atoi(m[1])
		if strings.HasPrefix(m[2], "127.") {
			continue
		}
		if bestIndex == -1 || index < bestIndex {
			best, bestIndex = m[2], index
		}
	}
	return best
}

func (p *VBoxProvider) vmFromInfo(name string, values map[string]string) VM {
	status := vboxStatus(values["VMState"])
	cpu, _ := strconv.Atoi(values["cpus"])
	memory, _ := strconv.Atoi(values["memory"])
	distro := distroFromDescription(values["description"])
	diskGB := 0
	if disks := attachedDisks(values); len(disks) > 0 {
		diskGB, _ = p.mediumInfo(disks[0])
	}
	ip := ""
	if status == VMStatusRunning {
		ip = p.guestIP(name)
	}
	return VM{
		Name:       name,
		Distro:     distro,
		CPU:        cpu,
		MemoryMB:   memory,
		DiskGB:     diskGB,
		SSHPort:    22,
		SSHKeyName: name + "-ssh",
		IP:         ip,
		Status:     status,
		Networks:   vboxNics(values),
	}
}

func (p *VBoxProvider) List() ([]VM, error) {
	names, err := p.listVMNames()
	if err != nil {
		return nil, err
	}
	vms := make([]VM, 0, len(names))
	for _, name := range names {
		values, err := p.info(name)
		if err != nil {
			vms = append(vms, VM{Name: name, Status: VMStatusError})
			continue
		}
		vms = append(vms, p.vmFromInfo(name, values))
	}
	return vms, nil
}

func (p *VBoxProvider) Get(name string) (*VM, error) {
	values, err := p.info(name)
	if err != nil {
		return nil, fmt.Errorf("VM %s no encontrada: %w", name, err)
	}
	vm := p.vmFromInfo(name, values)
	return &vm, nil
}

func (p *VBoxProvider) state(name string) (VMStatus, error) {
	values, err := p.info(name)
	if err != nil {
		return VMStatusError, err
	}
	return vboxStatus(values["VMState"]), nil
}

func (p *VBoxProvider) Start(name string) error {
	if err := ValidateName(name); err != nil {
		return err
	}
	values, err := p.info(name)
	if err != nil {
		return err
	}
	switch values["VMState"] {
	case "running":
		return nil
	case "paused":
		_, err = p.vbox.Run("controlvm", name, "resume")
	default:
		// headless: la VM corre sin ventana; se administra desde la app.
		_, err = p.vbox.Run("startvm", name, "--type", "headless")
	}
	if err != nil {
		return fmt.Errorf("iniciar VM %s: %w", name, err)
	}
	return nil
}

// Stop apaga la VM de golpe (como desconectar la corriente), igual que
// "virsh destroy" en el proveedor de libvirt.
func (p *VBoxProvider) Stop(name string) error {
	if err := ValidateName(name); err != nil {
		return err
	}
	status, err := p.state(name)
	if err != nil {
		return err
	}
	if status == VMStatusStopped {
		return nil
	}
	if _, err := p.vbox.Run("controlvm", name, "poweroff"); err != nil {
		return fmt.Errorf("detener VM %s: %w", name, err)
	}
	// La sesion tarda un momento en liberarse; modifyvm falla mientras tanto.
	p.waitUnlocked(name)
	return nil
}

// waitUnlocked espera a que VirtualBox libere la sesion de la VM tras
// apagarla (SessionState=unlocked), hasta 10 s.
func (p *VBoxProvider) waitUnlocked(name string) {
	for i := 0; i < 20; i++ {
		values, err := p.info(name)
		if err != nil || values["SessionState"] == "" || values["SessionState"] == "unlocked" {
			return
		}
		p.wait(500 * time.Millisecond)
	}
}

func (p *VBoxProvider) wait(d time.Duration) {
	if p.sleep != nil {
		p.sleep(d)
		return
	}
	time.Sleep(d)
}

func (p *VBoxProvider) Update(name string, spec UpdateSpec) error {
	if err := ValidateName(name); err != nil {
		return err
	}
	if err := ValidateName(spec.Name); err != nil {
		return err
	}
	if spec.CPU <= 0 || spec.MemoryMB <= 0 {
		return fmt.Errorf("CPU y memoria deben ser mayores que cero")
	}
	if err := p.Stop(name); err != nil {
		return fmt.Errorf("apagar VM %s antes de editarla: %w", name, err)
	}
	args := []string{"modifyvm", name, "--cpus", strconv.Itoa(spec.CPU), "--memory", strconv.Itoa(spec.MemoryMB)}
	if spec.Name != name {
		args = append(args, "--name", spec.Name)
	}
	if _, err := p.vbox.Run(args...); err != nil {
		return fmt.Errorf("actualizar VM %s: %w", name, err)
	}
	return nil
}

func (p *VBoxProvider) Rename(oldName, newName string) error {
	if err := ValidateName(oldName); err != nil {
		return err
	}
	if err := ValidateName(newName); err != nil {
		return err
	}
	if err := p.Stop(oldName); err != nil {
		return fmt.Errorf("apagar VM %s antes de renombrar: %w", oldName, err)
	}
	if _, err := p.vbox.Run("modifyvm", oldName, "--name", newName); err != nil {
		return fmt.Errorf("renombrar VM %s a %s: %w", oldName, newName, err)
	}
	return nil
}

// ListDisks lee los discos conectados en vivo (showvminfo), no lo que se
// pidio al crear la VM.
func (p *VBoxProvider) ListDisks(name string) ([]Disk, error) {
	if err := ValidateName(name); err != nil {
		return nil, err
	}
	values, err := p.info(name)
	if err != nil {
		return nil, err
	}
	disks := []Disk{}
	for _, path := range attachedDisks(values) {
		size, kind := p.mediumInfo(path)
		disks = append(disks, Disk{
			Name:      diskDisplayName(name, path),
			Path:      path,
			SizeGB:    size,
			ReadOnly:  kind == "readonly" || kind == "immutable",
			Shareable: kind == "shareable",
		})
	}
	return disks, nil
}

// Delete apaga la VM, desconecta los discos compartidos (para que
// "unregistervm --delete" no los borre: otras VMs pueden usarlos, ver
// ShareableDiskWarning) y elimina la VM con el resto de sus discos.
func (p *VBoxProvider) Delete(name string) error {
	if err := ValidateName(name); err != nil {
		return err
	}
	values, err := p.info(name)
	if err != nil {
		return fmt.Errorf("leer VM %s antes de eliminarla: %w", name, err)
	}
	if err := p.Stop(name); err != nil {
		return err
	}
	for key, value := range values {
		if strings.Contains(key, "ImageUUID") || value == "none" || value == "emptydrive" {
			continue
		}
		parts := strings.Split(key, "-")
		if len(parts) < 3 {
			continue
		}
		controller := strings.Join(parts[:len(parts)-2], "-")
		if values[controller+"-ImageUUID-"+strings.Join(parts[len(parts)-2:], "-")] == "" {
			continue
		}
		if _, kind := p.mediumInfo(value); kind != "shareable" {
			continue
		}
		port, device := parts[len(parts)-2], parts[len(parts)-1]
		if _, err := p.vbox.Run("storageattach", name, "--storagectl", controller, "--port", port, "--device", device, "--medium", "none"); err != nil {
			return fmt.Errorf("desconectar disco compartido %s antes de eliminar %s (no se borro nada): %w", value, name, err)
		}
	}
	if _, err := p.vbox.Run("unregistervm", name, "--delete"); err != nil {
		return fmt.Errorf("eliminar VM %s: %w", name, err)
	}
	return nil
}

// ListTemplates y Create se implementan cuando se defina el aprovisionamiento
// con plantillas e imagenes de nube (cloud-init) para VirtualBox.
func (p *VBoxProvider) ListTemplates() ([]Template, error) {
	return []Template{}, nil
}

func (p *VBoxProvider) Create(spec CreateSpec) (*VM, error) {
	return nil, fmt.Errorf("crear VMs en VirtualBox todavia no esta implementado")
}
