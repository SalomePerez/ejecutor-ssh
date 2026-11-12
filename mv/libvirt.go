package vm

import (
	"encoding/base64"
	"encoding/json"
	"encoding/xml"
	"fmt"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// sharedDisksPool es el pool de libvirt que crea la carpeta de discos compartidos.
const sharedDisksPool = "ejecutor-compartidos"

// sshRunner abstrae la ejecucion remota que necesita LibvirtProvider.
// *sshclient.Client la satisface sin cambios; permite inyectar un doble de
// prueba para no depender de una conexion SSH real en los tests.
type sshRunner interface {
	ExecuteCommand(command string) ([]byte, error)
}

type LibvirtProvider struct {
	sshClient sshRunner
	// sleep reemplaza time.Sleep en las pruebas (ver wait en guestagent.go).
	sleep func(time.Duration)
}

type domainXML struct {
	// Description guarda la distribucion con la que se creo la VM
	// (virt-install --metadata description=...).
	Description   string       `xml:"description"`
	Memory        domainMemory `xml:"memory"`
	CurrentMemory domainMemory `xml:"currentMemory"`
	VCPU          domainVCPU   `xml:"vcpu"`
}

type domainMemory struct {
	Unit  string `xml:"unit,attr"`
	Value int    `xml:",chardata"`
}

type domainVCPU struct {
	Current int `xml:"current,attr"`
	Value   int `xml:",chardata"`
}

// domainDisksXML mapea la lista de discos de <devices> en un dumpxml.
type domainDisksXML struct {
	Disks []domainDiskXML `xml:"devices>disk"`
}

type domainDiskXML struct {
	Device string `xml:"device,attr"`
	Source struct {
		File string `xml:"file,attr"`
	} `xml:"source"`
	// ReadOnly/Shareable son punteros para poder distinguir "el elemento no
	// esta presente" (nil) de "esta presente pero vacio" (no nil): libvirt
	// los representa como elementos vacios (<readonly/>, <shareable/>).
	ReadOnly  *struct{} `xml:"readonly"`
	Shareable *struct{} `xml:"shareable"`
}

func NewLibvirtProvider(sshClient sshRunner) *LibvirtProvider {
	return &LibvirtProvider{sshClient: sshClient}
}

func (p *LibvirtProvider) ListTemplates() ([]Template, error) {
	if p == nil || p.sshClient == nil {
		return nil, fmt.Errorf("cliente SSH no inicializado")
	}
	output, err := p.sshClient.ExecuteCommand("find /var/lib/libvirt/templates -maxdepth 1 -type f \\( -name '*.qcow2' -o -name '*.img' \\) -printf '%f\\n'")
	if err != nil {
		return nil, fmt.Errorf("listar plantillas: %w", err)
	}
	templates := []Template{}
	for _, name := range strings.Fields(string(output)) {
		extension := strings.ToLower(filepath.Ext(name))
		if extension != ".qcow2" && extension != ".img" {
			continue
		}
		templates = append(templates, Template{
			Name:   strings.TrimSuffix(name, extension),
			Path:   "/var/lib/libvirt/templates/" + name,
			Format: strings.TrimPrefix(extension, "."),
		})
	}
	return templates, nil
}

func (p *LibvirtProvider) List() ([]VM, error) {
	if p == nil || p.sshClient == nil {
		return nil, fmt.Errorf("cliente SSH no inicializado")
	}

	output, err := p.sshClient.ExecuteCommand("sudo -n virsh --connect qemu:///system list --all --name")
	if err != nil {
		details := strings.TrimSpace(string(output))
		if details != "" {
			return nil, fmt.Errorf("listar VMs con virsh: %w: %s", err, details)
		}
		return nil, fmt.Errorf("listar VMs con virsh: %w", err)
	}

	lines := strings.Fields(string(output))
	if len(lines) == 0 {
		return []VM{}, nil
	}

	vms := make([]VM, 0, len(lines))
	for _, name := range lines {
		name = strings.TrimSpace(name)
		if name == "" {
			continue
		}

		stateOutput, stateErr := p.sshClient.ExecuteCommand("sudo -n virsh --connect qemu:///system domstate " + quoteShell(name))
		status := VMStatusStopped
		if stateErr == nil {
			stateText := strings.ToLower(strings.TrimSpace(string(stateOutput)))
			status = parseVMStatus(stateText)
		}

		cpu, memoryMB := p.domainResources(name)
		distro := p.domainDistro(name)
		nics := p.domainNics(name)
		diskGB := p.systemDiskGB(name)
		ip := ""
		if status == VMStatusRunning {
			// Solo tiene sentido preguntar por IP si la VM esta corriendo:
			// el agente no responde y los leases DHCP de una VM apagada
			// suelen estar vencidos o ser de una sesion anterior.
			ip = p.lookupIP(name)
		}
		vms = append(vms, VM{
			Name:       name,
			Distro:     distro,
			CPU:        cpu,
			MemoryMB:   memoryMB,
			DiskGB:     diskGB,
			SSHUser:    "ubuntu",
			SSHPort:    22,
			SSHKeyName: name + "-ssh",
			IP:         ip,
			Status:     status,
			Networks:   nics,
		})
	}

	return vms, nil
}

func (p *LibvirtProvider) Create(spec CreateSpec) (*VM, error) {
	if p == nil || p.sshClient == nil {
		return nil, fmt.Errorf("cliente SSH no inicializado")
	}
	if err := ValidateName(spec.Name); err != nil {
		return nil, err
	}
	if err := ValidateDistro(spec.Distro); err != nil {
		return nil, err
	}
	if spec.CPU <= 0 {
		return nil, fmt.Errorf("CPU debe ser mayor que cero")
	}
	if spec.MemoryMB <= 0 {
		return nil, fmt.Errorf("memoria debe ser mayor que cero")
	}
	if spec.DiskGB <= 0 {
		return nil, fmt.Errorf("disco debe ser mayor que cero")
	}
	if spec.SSHUser == "" {
		spec.SSHUser = "ubuntu"
	}
	if spec.Distro == "" {
		spec.Distro = "ubuntu-24.04"
	}
	if spec.ImagePath == "" {
		spec.ImagePath = "/var/lib/libvirt/templates/ubuntu-24.04-server-cloudimg-amd64.qcow2"
	}
	if strings.Contains(spec.ImagePath, ":\\") || strings.Contains(spec.ImagePath, ":/") || strings.HasPrefix(spec.ImagePath, "C:") || strings.HasPrefix(spec.ImagePath, "c:") {
		return nil, fmt.Errorf("la ruta de la imagen debe ser absoluta en el host Linux del hipervisor, no una ruta local de Windows: %s", spec.ImagePath)
	}
	if !strings.HasPrefix(spec.ImagePath, "/") {
		return nil, fmt.Errorf("la ruta de la imagen debe empezar con / y apuntar al host Linux del hipervisor: %s", spec.ImagePath)
	}
	if _, err := p.sshClient.ExecuteCommand("test -f " + quoteShell(spec.ImagePath)); err != nil {
		return nil, fmt.Errorf("la imagen del host no existe o no es accesible por el usuario SSH: %s: %w", spec.ImagePath, err)
	}

	name := quoteShell(spec.Name)
	cpu := fmt.Sprintf("%d", spec.CPU)
	memory := fmt.Sprintf("%d", spec.MemoryMB)
	osVariant := osVariantForDistro(spec.Distro)
	// Una tarjeta virtio por red pedida; la primera es la principal y debe
	// tener salida a internet para que cloud-init instale el agente.
	networks := spec.Networks
	if len(networks) == 0 {
		networks = []string{"default"}
	}
	networkFlags := ""
	for _, network := range networks {
		if err := ValidateName(network); err != nil {
			return nil, fmt.Errorf("red %q: %w", network, err)
		}
		networkFlags += " --network network=" + quoteShell(network) + ",model=virtio"
	}
	// El canal virtio org.qemu.guest_agent.0 es el transporte que usa
	// qemu-guest-agent (instalado dentro de la VM via cloud-init mas abajo)
	// para responder a "virsh domifaddr --source agent". Sin este canal el
	// agente no tiene por donde hablar con el host, aunque este instalado.
	// Con KVM, --cpu oculta VT-x a la VM (ver hideVMXInXML).
	accel := " --cpu host-passthrough,disable=vmx"
	if spec.Emulated {
		accel = emulatedFlags
	}
	baseCommand := fmt.Sprintf("sudo -n virt-install --connect qemu:///system --name %s --memory %s --vcpus %s --os-variant %s%s%s --graphics none --serial pty --console pty,target_type=serial --channel unix,target_type=virtio,name=org.qemu.guest_agent.0 --metadata description=%s --noautoconsole", name, memory, cpu, osVariant, networkFlags, accel, quoteShell(spec.Distro))
	// cloud-init lee su configuracion de un disco semilla (NoCloud) que queda
	// conectado (ver seedDiskFlag). Antes se usaba "virt-install --cloud-init",
	// que solo lo conecta durante el primer arranque: si la VM se apagaba antes
	// de terminar, quedaba sin usuario, sin llave y sin qemu-guest-agent.
	seedPath := ""
	if strings.TrimSpace(spec.SSHPublicKey) != "" {
		path, err := p.writeSeed(spec.Name, spec.SSHUser, spec.SSHPublicKey)
		if err != nil {
			return nil, err
		}
		seedPath = path
	}

	imagePath := quoteShell(spec.ImagePath)
	command := ""
	createdDiskPath := ""
	createdDataDisks := make([]Disk, 0, len(spec.Disks))
	cleanupDisks := make([]string, 0, len(spec.Disks)+1)
	if strings.HasSuffix(strings.ToLower(spec.ImagePath), ".iso") {
		command = fmt.Sprintf("%s --cdrom %s --disk size=%d,format=qcow2,bus=virtio", baseCommand, imagePath, spec.DiskGB)
	} else {
		diskPath := "/var/lib/libvirt/images/" + spec.Name + "-system.qcow2"
		createdDiskPath = diskPath
		cleanupDisks = append(cleanupDisks, diskPath)
		// El tamano agranda el disco derivado; cloud-init extiende la
		// particion raiz en el primer arranque (growpart).
		createDisk := fmt.Sprintf("sudo -n qemu-img create -f qcow2 -F qcow2 -b %s %s %dG", imagePath, quoteShell(diskPath), spec.DiskGB)
		if output, err := p.sshClient.ExecuteCommand(createDisk); err != nil {
			details := strings.TrimSpace(string(output))
			if details != "" {
				return nil, fmt.Errorf("crear disco derivado para VM %s: %w: %s", spec.Name, err, details)
			}
			return nil, fmt.Errorf("crear disco derivado para VM %s: %w", spec.Name, err)
		}
		command = fmt.Sprintf("%s --import --disk path=%s,format=qcow2,bus=virtio", baseCommand, quoteShell(diskPath))
	}
	for index, disk := range spec.Disks {
		diskName := strings.TrimSpace(disk.Name)
		if diskName == "" {
			diskName = fmt.Sprintf("data-%d", index+1)
		}
		if err := ValidateName(diskName); err != nil {
			return nil, fmt.Errorf("nombre de disco adicional %s: %w", diskName, err)
		}
		if disk.SizeGB <= 0 {
			return nil, fmt.Errorf("tamaño inválido para disco adicional %s", diskName)
		}
		diskDirectory := "/var/lib/libvirt/images"
		if disk.Shareable {
			diskDirectory = "/var/lib/libvirt/shared-disks"
			// sudo en el hipervisor solo permite virsh, virt-install, qemu-img y
			// rm; la carpeta se crea como un pool de libvirt (pool-build).
			prepare := fmt.Sprintf("test -d %[1]s || { sudo -n virsh --connect qemu:///system pool-define-as %[2]s dir --target %[1]s && sudo -n virsh --connect qemu:///system pool-build %[2]s && sudo -n virsh --connect qemu:///system pool-start %[2]s && sudo -n virsh --connect qemu:///system pool-autostart %[2]s; }", quoteShell(diskDirectory), sharedDisksPool)
			if _, err := p.sshClient.ExecuteCommand(prepare); err != nil {
				return nil, fmt.Errorf("preparar directorio de discos compartidos: %w", err)
			}
		}
		diskPath := diskDirectory + "/" + spec.Name + "-" + diskName + ".qcow2"
		if disk.Shareable {
			diskPath = diskDirectory + "/" + diskName + ".qcow2"
		}
		if _, err := p.sshClient.ExecuteCommand("test -f " + quoteShell(diskPath)); err != nil {
			createDisk := fmt.Sprintf("sudo -n qemu-img create -f qcow2 %s %dG", quoteShell(diskPath), disk.SizeGB)
			if output, createErr := p.sshClient.ExecuteCommand(createDisk); createErr != nil {
				cleanupCreatedDisks(p, cleanupDisks)
				details := strings.TrimSpace(string(output))
				if details != "" {
					return nil, fmt.Errorf("crear disco adicional %s: %w: %s", diskName, createErr, details)
				}
				return nil, fmt.Errorf("crear disco adicional %s: %w", diskName, createErr)
			}
		}
		if !disk.Shareable {
			cleanupDisks = append(cleanupDisks, diskPath)
		}
		diskFlag := fmt.Sprintf(" --disk path=%s,format=qcow2,bus=virtio", quoteShell(diskPath))
		if disk.ReadOnly {
			diskFlag += ",readonly=on"
		}
		if disk.Shareable {
			// shareable=on solo desactiva el locking de imagen de QEMU para
			// permitir el attach simultaneo; no coordina el acceso entre VMs.
			// Ver ShareableDiskWarning (policy.go) para la politica completa.
			diskFlag += ",shareable=on"
		}
		command += diskFlag
		createdDataDisks = append(createdDataDisks, Disk{Name: diskName, Path: diskPath, SizeGB: disk.SizeGB, ReadOnly: disk.ReadOnly, Shareable: disk.Shareable})
	}
	// La semilla va de ultima: el disco del sistema queda primero (vda, el que
	// arranca) y los de datos conservan sus nombres (vdb, vdc...).
	if seedPath != "" {
		command += seedDiskFlag(seedPath)
	}
	if output, err := p.sshClient.ExecuteCommand(command); err != nil {
		cleanupCreatedDisks(p, cleanupDisks)
		details := strings.TrimSpace(string(output))
		if details != "" {
			return nil, fmt.Errorf("crear VM %s desde imagen %s con libvirt: %w: %s", spec.Name, spec.ImagePath, err, details)
		}
		return nil, fmt.Errorf("crear VM %s desde imagen %s con libvirt: %w", spec.Name, spec.ImagePath, err)
	}
	// La VM queda encendida: su primer arranque ejecuta cloud-init. Si alguien
	// la apaga antes de que termine, la semilla sigue conectada y cloud-init
	// continua en el siguiente arranque.
	if _, err := p.sshClient.ExecuteCommand("sudo -n virsh --connect qemu:///system dominfo " + quoteShell(spec.Name)); err != nil {
		return nil, fmt.Errorf("verificar VM %s despues de crearla: %w", spec.Name, err)
	}

	vm := &VM{
		Name:      spec.Name,
		Distro:    spec.Distro,
		CPU:       spec.CPU,
		MemoryMB:  spec.MemoryMB,
		DiskGB:    spec.DiskGB,
		SSHUser:   spec.SSHUser,
		SSHPort:   22,
		Status:    VMStatusRunning,
		ImagePath: createdDiskPath,
		Disks:     createdDataDisks,
	}
	return vm, nil
}

func cleanupCreatedDisks(provider *LibvirtProvider, paths []string) {
	for _, path := range paths {
		_, _ = provider.sshClient.ExecuteCommand("sudo -n rm -f " + quoteShell(path))
	}
}

func (p *LibvirtProvider) Start(name string) error {
	if p == nil || p.sshClient == nil {
		return fmt.Errorf("cliente SSH no inicializado")
	}
	if err := ValidateName(name); err != nil {
		return err
	}
	stateOutput, err := p.sshClient.ExecuteCommand("sudo -n virsh --connect qemu:///system domstate " + quoteShell(name))
	if err != nil {
		return fmt.Errorf("consultar estado de VM %s: %w", name, err)
	}
	if parseVMStatus(strings.ToLower(strings.TrimSpace(string(stateOutput)))) == VMStatusRunning {
		return nil
	}
	if err := p.hideVMX(name); err != nil {
		return err
	}
	if _, err := p.sshClient.ExecuteCommand("sudo -n virsh --connect qemu:///system start " + quoteShell(name)); err != nil {
		return fmt.Errorf("iniciar VM %s: %w", name, err)
	}
	return nil
}

// vmxFeature encuentra la linea de VT-x dentro de <cpu> tal como la imprime
// virsh dumpxml; cpuOpenTag, la etiqueta <cpu ...> o <cpu .../>.
var (
	vmxFeature = regexp.MustCompile(`<feature policy='([a-z]+)' name='vmx'/>`)
	cpuOpenTag = regexp.MustCompile(`<cpu\b[^>]*?(/?)>`)
)

const vmxDisabled = "<feature policy='disable' name='vmx'/>"

// emulatedFlags crea la VM con TCG: QEMU traduce las instrucciones por
// software y el hipervisor no entra en VT-x por esa VM. El hipervisor es una
// VM de VirtualBox, y su VT-x anidado (7.2, Skylake) se cae con
// VINF_EM_TRIPLE_FAULT al arrancar Debian 13 con KVM, aun sin vmx en la VM.
// "maximum" es el modelo de CPU mas completo que TCG puede emular.
const emulatedFlags = " --virt-type qemu --cpu mode=maximum"

// hideVMXInXML le quita VT-x (vmx) a la CPU de la VM. Si la VM ve vmx, su
// kernel carga kvm_intel, y desde Linux 6.12 kvm activa VT-x al cargar el
// modulo (kvm.enable_virt_at_load): un tercer nivel de anidamiento sobre el
// VT-x emulado de VirtualBox. Las VMs de la app no virtualizan, asi que no
// pierden nada. No aplica a VMs emuladas (domain type='qemu'). Devuelve
// false si no habia nada que cambiar.
func hideVMXInXML(xml string) (string, bool) {
	if strings.Contains(xml, "<domain type='qemu'") {
		return xml, false
	}
	if m := vmxFeature.FindStringSubmatch(xml); m != nil {
		if m[1] == "disable" || m[1] == "forbid" {
			return xml, false
		}
		return strings.Replace(xml, m[0], vmxDisabled, 1), true
	}
	loc := cpuOpenTag.FindStringSubmatchIndex(xml)
	if loc == nil {
		// Sin <cpu>, QEMU usa su modelo generico, que no trae vmx.
		return xml, false
	}
	tag := xml[loc[0]:loc[1]]
	if loc[2] != loc[3] { // <cpu .../>
		open := strings.TrimSuffix(tag, "/>") + ">"
		return xml[:loc[0]] + open + vmxDisabled + "</cpu>" + xml[loc[1]:], true
	}
	return xml[:loc[1]] + vmxDisabled + xml[loc[1]:], true
}

// hideVMX aplica hideVMXInXML a una VM apagada (las creadas antes de que
// virt-install recibiera --cpu ...,disable=vmx). El XML se sube como en
// writeSeed: base64 a un archivo temporal y virsh define, que si permite sudo.
func (p *LibvirtProvider) hideVMX(name string) error {
	virsh := "sudo -n virsh --connect qemu:///system "
	output, err := p.sshClient.ExecuteCommand(virsh + "dumpxml --inactive " + quoteShell(name))
	if err != nil {
		return fmt.Errorf("leer la definicion de %s: %w", name, err)
	}
	patched, changed := hideVMXInXML(string(output))
	if !changed {
		return nil
	}
	tmp := quoteShell("/tmp/ejecutor-cpu-" + name + ".xml")
	command := fmt.Sprintf("printf %%s %s | base64 --decode > %s && %sdefine %s; status=$?; rm -f %s; exit $status",
		quoteShell(base64.StdEncoding.EncodeToString([]byte(patched))), tmp, virsh, tmp, tmp)
	if output, err := p.sshClient.ExecuteCommand(command); err != nil {
		return fmt.Errorf("ocultar VT-x a %s antes de encenderla (sin esto puede tumbar el hipervisor): %w: %s", name, err, strings.TrimSpace(string(output)))
	}
	return nil
}

func (p *LibvirtProvider) Stop(name string) error {
	if p == nil || p.sshClient == nil {
		return fmt.Errorf("cliente SSH no inicializado")
	}
	if err := ValidateName(name); err != nil {
		return err
	}
	stateOutput, err := p.sshClient.ExecuteCommand("sudo -n virsh --connect qemu:///system domstate " + quoteShell(name))
	if err != nil {
		return fmt.Errorf("consultar estado de VM %s: %w", name, err)
	}
	if parseVMStatus(strings.ToLower(strings.TrimSpace(string(stateOutput)))) == VMStatusStopped {
		return nil
	}
	if _, err := p.sshClient.ExecuteCommand("sudo -n virsh --connect qemu:///system destroy " + quoteShell(name)); err != nil {
		return fmt.Errorf("detener VM %s: %w", name, err)
	}
	return nil
}

// Delete apaga y desvincula la VM, y borra solo los discos que le pertenecen
// en exclusiva. Los discos marcados como shareable NUNCA se borran aqui:
// otras VMs pueden seguir usandolos (ver ShareableDiskWarning en policy.go),
// asi que la aplicacion no puede asumir que es seguro eliminarlos solo porque
// esta VM se elimina.
func (p *LibvirtProvider) Delete(name string) error {
	if p == nil || p.sshClient == nil {
		return fmt.Errorf("cliente SSH no inicializado")
	}
	if err := ValidateName(name); err != nil {
		return err
	}

	// Se listan los discos ANTES de undefine: dumpxml solo funciona mientras
	// el dominio sigue definido. Si no se puede determinar que discos son
	// compartidos, se aborta en vez de arriesgarse a borrar de mas.
	disks, err := p.ListDisks(name)
	if err != nil {
		return fmt.Errorf("listar discos de VM %s antes de eliminarla: %w", name, err)
	}

	cmd := fmt.Sprintf("sudo -n virsh --connect qemu:///system destroy %s >/dev/null 2>&1 || true; sudo -n virsh --connect qemu:///system undefine %s", quoteShell(name), quoteShell(name))
	if _, err := p.sshClient.ExecuteCommand(cmd); err != nil {
		return fmt.Errorf("eliminar VM %s: %w", name, err)
	}

	for _, disk := range disks {
		if disk.Shareable {
			continue
		}
		if _, err := p.sshClient.ExecuteCommand("sudo -n rm -f " + quoteShell(disk.Path)); err != nil {
			return fmt.Errorf("eliminar disco %s de VM %s: %w", disk.Path, name, err)
		}
	}
	// La semilla de cloud-init no aparece en ListDisks.
	_, _ = p.sshClient.ExecuteCommand("sudo -n rm -f " + quoteShell(seedPathFor(name)))
	return nil
}

// ListDisks devuelve los discos definidos actualmente para una VM, leyendo
// la definicion en vivo de libvirt (dumpxml) en vez de confiar en lo que se
// le pidio a Create: asi refleja discos agregados/quitados fuera de esta
// aplicacion o entre reinicios.
func (p *LibvirtProvider) ListDisks(name string) ([]Disk, error) {
	if p == nil || p.sshClient == nil {
		return nil, fmt.Errorf("cliente SSH no inicializado")
	}
	if err := ValidateName(name); err != nil {
		return nil, err
	}

	output, err := p.sshClient.ExecuteCommand("sudo -n virsh --connect qemu:///system dumpxml " + quoteShell(name))
	if err != nil {
		details := strings.TrimSpace(string(output))
		if details != "" {
			return nil, fmt.Errorf("consultar discos de VM %s: %w: %s", name, err, details)
		}
		return nil, fmt.Errorf("consultar discos de VM %s: %w", name, err)
	}

	var definition domainDisksXML
	if err := xml.Unmarshal(output, &definition); err != nil {
		return nil, fmt.Errorf("parsear discos de VM %s: %w", name, err)
	}

	disks := make([]Disk, 0, len(definition.Disks))
	for _, d := range definition.Disks {
		// La semilla de cloud-init tambien es un disco, pero no es de datos.
		if d.Device != "disk" || d.Source.File == "" || strings.HasPrefix(d.Source.File, seedDir+"/") {
			continue
		}
		disks = append(disks, Disk{
			Name:      diskDisplayName(name, d.Source.File),
			Path:      d.Source.File,
			SizeGB:    p.diskSizeGB(d.Source.File),
			ReadOnly:  d.ReadOnly != nil,
			Shareable: d.Shareable != nil,
		})
	}
	return disks, nil
}

// diskDisplayName reconstruye el nombre "logico" del disco (el que se recibio
// en CreateSpec.Disks) a partir de la ruta del archivo, ya que libvirt solo
// conoce la ruta, no la etiqueta original. Sigue la misma convencion de
// nombres usada en Create: "<vm>-<disco>.qcow2" para discos propios,
// "<disco>.qcow2" para discos compartidos.
func diskDisplayName(vmName, path string) string {
	base := strings.TrimSuffix(filepath.Base(path), filepath.Ext(path))
	prefix := vmName + "-"
	if strings.HasPrefix(base, prefix) {
		return strings.TrimPrefix(base, prefix)
	}
	return base
}

// diskSizeGB consulta el tamaño virtual de un disco con qemu-img info. Si la
// consulta falla (permisos, disco removido, etc.) se devuelve 0 en vez de
// fallar toda la operacion: el tamaño es informativo, no critico.
func (p *LibvirtProvider) diskSizeGB(path string) int {
	output, err := p.sshClient.ExecuteCommand("sudo -n qemu-img info --output=json " + quoteShell(path))
	if err != nil {
		return 0
	}
	var info struct {
		VirtualSize int64 `json:"virtual-size"`
	}
	if err := json.Unmarshal(output, &info); err != nil {
		return 0
	}
	return int(info.VirtualSize / (1024 * 1024 * 1024))
}

func (p *LibvirtProvider) Rename(oldName, newName string) error {
	if p == nil || p.sshClient == nil {
		return fmt.Errorf("cliente SSH no inicializado")
	}
	if err := ValidateName(oldName); err != nil {
		return err
	}
	if err := ValidateName(newName); err != nil {
		return err
	}
	if err := p.Stop(oldName); err != nil {
		return fmt.Errorf("apagar VM %s antes de renombrar: %w", oldName, err)
	}
	cmd := fmt.Sprintf("sudo -n virsh --connect qemu:///system domrename %s %s", quoteShell(oldName), quoteShell(newName))
	if _, err := p.sshClient.ExecuteCommand(cmd); err != nil {
		return fmt.Errorf("renombrar VM %s a %s: %w", oldName, newName, err)
	}
	return nil
}

func (p *LibvirtProvider) Update(name string, spec UpdateSpec) error {
	if p == nil || p.sshClient == nil {
		return fmt.Errorf("cliente SSH no inicializado")
	}
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
	commands := []string{
		// Primero el maximo: sin el, subir los vCPU por encima de los que
		// tenia la VM al crearse falla con "greater than max allowable".
		fmt.Sprintf("sudo -n virsh --connect qemu:///system setvcpus %s %d --maximum --config", quoteShell(name), spec.CPU),
		fmt.Sprintf("sudo -n virsh --connect qemu:///system setvcpus %s %d --config", quoteShell(name), spec.CPU),
		fmt.Sprintf("sudo -n virsh --connect qemu:///system setmaxmem %s %dM --config", quoteShell(name), spec.MemoryMB),
		fmt.Sprintf("sudo -n virsh --connect qemu:///system setmem %s %dM --config", quoteShell(name), spec.MemoryMB),
	}
	for _, command := range commands {
		if _, err := p.sshClient.ExecuteCommand(command); err != nil {
			return fmt.Errorf("actualizar VM %s: %w", name, err)
		}
	}
	if name != spec.Name {
		if _, err := p.sshClient.ExecuteCommand(fmt.Sprintf("sudo -n virsh --connect qemu:///system domrename %s %s", quoteShell(name), quoteShell(spec.Name))); err != nil {
			return fmt.Errorf("renombrar VM %s a %s: %w", name, spec.Name, err)
		}
	}
	return nil
}

func (p *LibvirtProvider) Get(name string) (*VM, error) {
	if p == nil || p.sshClient == nil {
		return nil, fmt.Errorf("cliente SSH no inicializado")
	}
	vms, err := p.List()
	if err != nil {
		return nil, err
	}
	for i := range vms {
		if vms[i].Name == name {
			return &vms[i], nil
		}
	}
	return nil, fmt.Errorf("VM %s no encontrada", name)
}

func (p *LibvirtProvider) domainResources(name string) (int, int) {
	output, err := p.sshClient.ExecuteCommand("sudo -n virsh --connect qemu:///system dumpxml " + quoteShell(name))
	if err != nil {
		return 0, 0
	}
	var definition domainXML
	if err := xml.Unmarshal(output, &definition); err != nil {
		return 0, 0
	}
	cpu := definition.VCPU.Current
	if cpu <= 0 {
		cpu = definition.VCPU.Value
	}
	memory := definition.CurrentMemory
	if memory.Value <= 0 {
		memory = definition.Memory
	}
	memoryMB := memory.Value
	if strings.EqualFold(memory.Unit, "KiB") {
		memoryMB = (memory.Value + 1023) / 1024
	} else if strings.EqualFold(memory.Unit, "GiB") {
		memoryMB = memory.Value * 1024
	}
	return cpu, memoryMB
}

// domainDistro lee la distribucion guardada en la descripcion de la VM. Las
// VMs creadas antes de guardarla salieron de la plantilla Ubuntu 24.04.
func (p *LibvirtProvider) domainDistro(name string) string {
	output, err := p.sshClient.ExecuteCommand("sudo -n virsh --connect qemu:///system dumpxml " + quoteShell(name))
	if err == nil {
		var definition domainXML
		if xml.Unmarshal(output, &definition) == nil && ValidateDistro(strings.TrimSpace(definition.Description)) == nil {
			return strings.TrimSpace(definition.Description)
		}
	}
	return "ubuntu-24.04"
}

// systemDiskGB devuelve la capacidad del disco del sistema (vda) en GB, o 0
// si no se puede leer.
func (p *LibvirtProvider) systemDiskGB(name string) int {
	output, err := p.sshClient.ExecuteCommand("sudo -n virsh --connect qemu:///system domblkinfo " + quoteShell(name) + " vda")
	if err != nil {
		return 0
	}
	for _, line := range strings.Split(string(output), "\n") {
		fields := strings.Fields(line)
		if len(fields) == 2 && fields[0] == "Capacity:" {
			bytes, convErr := strconv.ParseInt(fields[1], 10, 64)
			if convErr == nil {
				return int((bytes + (1 << 30) - 1) >> 30)
			}
		}
	}
	return 0
}

// lookupIP intenta detectar la IP de una VM en dos pasos: primero via
// qemu-guest-agent (mas confiable, reporta lo que el SO de la VM ve
// realmente), y si no responde, via los leases DHCP de la red de libvirt
// (funciona sin necesitar nada instalado dentro de la VM, pero depende de que
// libvirt gestione la red, ej. "network=default").
func (p *LibvirtProvider) lookupIP(name string) string {
	if ip := p.domifaddr(name, "agent"); ip != "" {
		return ip
	}
	return p.domifaddr(name, "lease")
}

// domifaddr ejecuta "virsh domifaddr --source <source>" y devuelve la
// primera IPv4 no-loopback encontrada, o "" si no hay ninguna o el comando
// falla (agente no instalado/no responde, sin leases, etc.). Un fallo aqui
// nunca debe romper List()/Get(): la IP es informativa.
func (p *LibvirtProvider) domifaddr(name, source string) string {
	output, err := p.sshClient.ExecuteCommand("sudo -n virsh --connect qemu:///system domifaddr " + quoteShell(name) + " --source " + source)
	if err != nil {
		return ""
	}
	return parseDomifaddrOutput(string(output))
}

// parseDomifaddrOutput parsea la tabla que imprime "virsh domifaddr", con
// forma:
//
//	 Name       MAC address          Protocol     Address
//	-------------------------------------------------------------------------------
//	 lo         00:00:00:00:00:00    ipv4         127.0.0.1/8
//	 eth0       52:54:00:12:34:56    ipv4         192.168.122.45/24
func parseDomifaddrOutput(output string) string {
	for _, line := range strings.Split(output, "\n") {
		fields := strings.Fields(line)
		if len(fields) < 4 {
			continue
		}
		protocol := fields[len(fields)-2]
		address := fields[len(fields)-1]
		if protocol != "ipv4" {
			continue
		}
		ip := address
		if idx := strings.Index(ip, "/"); idx != -1 {
			ip = ip[:idx]
		}
		if ip == "" || strings.HasPrefix(ip, "127.") {
			continue
		}
		return ip
	}
	return ""
}

func parseVMStatus(state string) VMStatus {
	switch {
	case strings.Contains(state, "running"):
		return VMStatusRunning
	case strings.Contains(state, "shut off") || strings.Contains(state, "stopped") || strings.Contains(state, "off"):
		return VMStatusStopped
	case strings.Contains(state, "creating"):
		return VMStatusCreating
	case strings.Contains(state, "paused"):
		return VMStatusStopped
	default:
		return VMStatusError
	}
}

func quoteShell(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "'\\''") + "'"
}

func osVariantForDistro(distro string) string {
	switch strings.TrimSpace(distro) {
	case "ubuntu-24.04":
		return "ubuntu24.04"
	case "debian-12":
		return "debian12"
	case "debian-13":
		return "debian13"
	case "fedora-42":
		return "fedora42"
	case "arch":
		return "archlinux"
	case "rocky-9":
		return "rocky9"
	default:
		return "ubuntu24.04"
	}
}

// seedDir es donde quedan los discos semilla de cloud-init (pool "boot").
const seedDir = "/var/lib/libvirt/boot"

func seedPathFor(name string) string { return seedDir + "/" + name + "-seed.iso" }

// seedDiskFlag conecta la semilla como disco virtio de solo lectura. Como
// CD-ROM SATA no sirve para todas: el kernel "cloud" de las imagenes
// genericcloud de Debian no trae el controlador SATA (ahci), no ve la semilla
// y la VM arranca sin usuario, sin llave, sin agente y sin red. virtio-blk lo
// traen todos los kernels de nube, y cloud-init busca la semilla por su
// etiqueta (cidata) en cualquier disco.
func seedDiskFlag(path string) string {
	return " --disk path=" + quoteShell(path) + ",device=disk,bus=virtio,format=raw,readonly=on"
}

// seedRepairTarget es el nombre que recibe la semilla al reconectarla en
// RepairGuestAgent: el ultimo de virtio, para no chocar con discos de datos.
const seedRepairTarget = "vdz"

// cloudInitUserData es la configuracion que cloud-init aplica dentro de la VM:
// usuario con la llave de la app y qemu-guest-agent, que es el canal del
// panel de Red y de la deteccion de IP.
func cloudInitUserData(user, publicKey string) string {
	if strings.TrimSpace(user) == "" {
		user = "ubuntu"
	}
	return fmt.Sprintf(`#cloud-config
users:
  - default
  - name: %s
    sudo: ALL=(ALL) NOPASSWD:ALL
    shell: /bin/bash
    ssh_authorized_keys:
      - %s
package_update: true
packages:
  - qemu-guest-agent
runcmd:
  - systemctl enable --now serial-getty@ttyS0.service
  - [sh, -c, "if [ -f /etc/sysconfig/qemu-ga ]; then sed -i -E '/^(FILTER_RPC_ARGS|BLACKLIST_RPC)=/d' /etc/sysconfig/qemu-ga; fi"]
  - [sh, -c, "command -v setsebool >/dev/null && setsebool -P virt_qemu_ga_run_unconfined on || true"]
  - systemctl enable --now qemu-guest-agent
  - systemctl restart qemu-guest-agent
`, strconv.Quote(user), strconv.Quote(strings.TrimSpace(publicKey)))
}

// writeSeed arma el ISO NoCloud con cloud-localds y lo deja en seedDir. El
// usuario SSH no puede escribir ahi; qemu-img (permitido con sudo) copia el
// archivo. Cada semilla lleva un instance-id nuevo para que cloud-init la
// trate como primera vez y vuelva a aplicar usuario, llave y paquetes.
func (p *LibvirtProvider) writeSeed(name, user, publicKey string) (string, error) {
	tmp := "/tmp/ejecutor-seed-" + name
	seed := seedPathFor(name)
	metaData := fmt.Sprintf("instance-id: %s-%d\nlocal-hostname: %s\n", name, time.Now().Unix(), name)
	userData := base64.StdEncoding.EncodeToString([]byte(cloudInitUserData(user, publicKey)))
	meta := base64.StdEncoding.EncodeToString([]byte(metaData))
	command := fmt.Sprintf("rm -rf %[1]s && mkdir -p %[1]s && printf %%s %[2]s | base64 --decode > %[1]s/user-data && printf %%s %[3]s | base64 --decode > %[1]s/meta-data && cloud-localds %[1]s/seed.iso %[1]s/user-data %[1]s/meta-data && sudo -n qemu-img convert -f raw -O raw %[1]s/seed.iso %[4]s; status=$?; rm -rf %[1]s; exit $status",
		quoteShell(tmp), quoteShell(userData), quoteShell(meta), quoteShell(seed))
	if output, err := p.sshClient.ExecuteCommand(command); err != nil {
		details := strings.TrimSpace(string(output))
		return "", fmt.Errorf("preparar cloud-init para VM %s (¿esta instalado cloud-image-utils en el hipervisor?): %w: %s", name, err, details)
	}
	return seed, nil
}

// RepairGuestAgent vuelve a conectar una semilla de cloud-init y reinicia la
// VM para que instale qemu-guest-agent y la llave. Sirve para VMs cuyo primer
// arranque se corto (las creadas antes de usar la semilla persistente).
func (p *LibvirtProvider) RepairGuestAgent(name, user, publicKey string) error {
	if p == nil || p.sshClient == nil {
		return fmt.Errorf("cliente SSH no inicializado")
	}
	if err := ValidateName(name); err != nil {
		return err
	}
	virsh := "sudo -n virsh --connect qemu:///system "
	// Se apaga antes de reescribir la semilla: si la VM ya la tiene conectada,
	// QEMU guarda el bloqueo del archivo y qemu-img no puede escribirlo.
	_, _ = p.sshClient.ExecuteCommand(virsh + "destroy " + quoteShell(name))
	seed, err := p.writeSeed(name, user, publicKey)
	if err != nil {
		return err
	}
	// Se quita la semilla de donde este (las VMs viejas la tienen como CD-ROM
	// SATA) y se vuelve a conectar como disco virtio, igual que en Create.
	command := fmt.Sprintf("for t in $(%[1]sdomblklist %[2]s | awk -v s=%[3]s '$2 == s {print $1}'); do %[1]sdetach-disk %[2]s \"$t\" --config || exit 1; done; %[1]sattach-disk %[2]s %[3]s %[4]s --targetbus virtio --type disk --subdriver raw --mode readonly --config",
		virsh, quoteShell(name), quoteShell(seed), seedRepairTarget)
	if output, err := p.sshClient.ExecuteCommand(command); err != nil {
		return fmt.Errorf("conectar la semilla de cloud-init a %s: %w: %s", name, err, strings.TrimSpace(string(output)))
	}
	// Encendido en frio: los cambios --config se aplican al arrancar.
	if err := p.hideVMX(name); err != nil {
		return err
	}
	if _, err := p.sshClient.ExecuteCommand(virsh + "start " + quoteShell(name)); err != nil {
		return fmt.Errorf("encender %s con la semilla: %w", name, err)
	}
	return nil
}

// HostInfo resume la memoria y los CPU del hipervisor, para avisar cuando las
// VMs encendidas piden mas memoria de la que hay.
type HostInfo struct {
	MemoryMB    int `json:"memoryMB"`
	AvailableMB int `json:"availableMB"`
	CPUs        int `json:"cpus"`
}

func (p *LibvirtProvider) HostInfo() (*HostInfo, error) {
	output, err := p.sshClient.ExecuteCommand("free -m | awk '/^Mem:/{print $2, $7}'; nproc")
	if err != nil {
		return nil, fmt.Errorf("leer memoria del hipervisor: %w", err)
	}
	info := &HostInfo{}
	if _, err := fmt.Sscan(string(output), &info.MemoryMB, &info.AvailableMB, &info.CPUs); err != nil {
		return nil, fmt.Errorf("leer memoria del hipervisor: salida inesperada %q", strings.TrimSpace(string(output)))
	}
	return info, nil
}
