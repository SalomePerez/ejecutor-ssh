package vm

import (
	"encoding/base64"
	"encoding/xml"
	"fmt"
	"net"
	"strings"
)

// ---------- Redes virtuales de libvirt ----------
// Cada red de libvirt es un switch virtual (un bridge virbrN en el
// hipervisor). Su modo decide hasta donde llegan las VMs conectadas:
//   nat      -> salen a internet a traves del hipervisor (como "default")
//   route    -> sin NAT: otros equipos llegan si tienen una ruta hacia ella
//   isolated -> solo se ven entre si y con el hipervisor; sin internet
// Referencia: https://libvirt.org/formatnetwork.html

// VirtualNetwork describe una red de libvirt para la interfaz.
type VirtualNetwork struct {
	Name      string `json:"name"`
	Mode      string `json:"mode"` // nat, route, isolated, bridge, open
	Bridge    string `json:"bridge"`
	HostIP    string `json:"hostIP,omitempty"` // IP del hipervisor en esa red (puerta de enlace)
	Prefix    int    `json:"prefix,omitempty"`
	Subnet    string `json:"subnet,omitempty"`
	DHCPStart string `json:"dhcpStart,omitempty"`
	DHCPEnd   string `json:"dhcpEnd,omitempty"`
	Active    bool   `json:"active"`
	Autostart bool   `json:"autostart"`
	// VMs lista las maquinas que tienen una tarjeta conectada a esta red.
	VMs []string `json:"vms"`
}

// NetworkSpec es lo que se pide para crear una red.
type NetworkSpec struct {
	Name   string `json:"name"`
	Mode   string `json:"mode"`   // nat, route, isolated
	Subnet string `json:"subnet"` // ej. 192.168.100.0/24
	DHCP   bool   `json:"dhcp"`
}

// VMNic es una tarjeta de red de una VM.
type VMNic struct {
	Network string `json:"network"`
	MAC     string `json:"mac"`
	Model   string `json:"model,omitempty"`
}

type networkDefXML struct {
	Name string `xml:"name"`
	// Forward es nil cuando la red es aislada (no tiene <forward>).
	Forward *struct {
		Mode string `xml:"mode,attr"`
	} `xml:"forward"`
	Bridge struct {
		Name string `xml:"name,attr"`
	} `xml:"bridge"`
}

const virshCmd = "sudo -n virsh --connect qemu:///system "

// ListNetworks lee todas las redes de libvirt y que VMs usan cada una.
func (p *LibvirtProvider) ListNetworks() ([]VirtualNetwork, error) {
	if p == nil || p.sshClient == nil {
		return nil, fmt.Errorf("cliente SSH no inicializado")
	}
	out, err := p.sshClient.ExecuteCommand(virshCmd + "net-list --all")
	if err != nil {
		return nil, fmt.Errorf("listar redes de libvirt: %w", err)
	}
	networks := []VirtualNetwork{}
	for _, line := range strings.Split(string(out), "\n") {
		f := strings.Fields(line)
		// Name  State  Autostart  Persistent
		if len(f) < 3 || f[0] == "Name" || strings.HasPrefix(f[0], "---") {
			continue
		}
		n := VirtualNetwork{Name: f[0], Active: f[1] == "active", Autostart: f[2] == "yes", VMs: []string{}}
		if def, err := p.sshClient.ExecuteCommand(virshCmd + "net-dumpxml " + quoteShell(n.Name)); err == nil {
			var d networkDefXML
			if xml.Unmarshal(def, &d) == nil {
				n.Bridge = d.Bridge.Name
				n.Mode = "isolated"
				if d.Forward != nil {
					n.Mode = d.Forward.Mode
					if n.Mode == "" {
						n.Mode = "nat" // <forward/> sin modo es NAT
					}
				}
			}
		}
		if ln := p.libvirtNetwork(n.Name); ln != nil {
			n.HostIP, n.Prefix, n.DHCPStart, n.DHCPEnd = ln.HostIP, ln.Prefix, ln.DHCPStart, ln.DHCPEnd
			if _, cidr, err := net.ParseCIDR(fmt.Sprintf("%s/%d", ln.HostIP, ln.Prefix)); err == nil {
				n.Subnet = cidr.String()
			}
		}
		networks = append(networks, n)
	}

	// Que VMs estan conectadas a cada red (por la definicion, encendidas o no).
	if names, err := p.sshClient.ExecuteCommand(virshCmd + "list --all --name"); err == nil {
		for _, vmName := range strings.Fields(string(names)) {
			for _, nic := range p.domainNics(vmName) {
				for i := range networks {
					if networks[i].Name == nic.Network && !contains(networks[i].VMs, vmName) {
						networks[i].VMs = append(networks[i].VMs, vmName)
					}
				}
			}
		}
	}
	return networks, nil
}

func contains(list []string, value string) bool {
	for _, v := range list {
		if v == value {
			return true
		}
	}
	return false
}

// ValidateNetworkSpec revisa nombre, modo y subred (/16 a /28, IPv4 privada).
func ValidateNetworkSpec(spec NetworkSpec) (*net.IPNet, error) {
	if err := ValidateName(spec.Name); err != nil {
		return nil, fmt.Errorf("nombre de red: %w", err)
	}
	switch spec.Mode {
	case "nat", "route", "isolated":
	default:
		return nil, fmt.Errorf("modo de red no soportado: %q (use nat, route o isolated)", spec.Mode)
	}
	ip, cidr, err := net.ParseCIDR(strings.TrimSpace(spec.Subnet))
	if err != nil || ip.To4() == nil {
		return nil, fmt.Errorf("subred inválida: escríbala como 192.168.100.0/24")
	}
	ones, _ := cidr.Mask.Size()
	if ones < 16 || ones > 28 {
		return nil, fmt.Errorf("la máscara debe estar entre /16 y /28")
	}
	if !ip.Equal(cidr.IP) {
		return nil, fmt.Errorf("%s no es la dirección de red: use %s", spec.Subnet, cidr.String())
	}
	if !cidr.IP.IsPrivate() {
		return nil, fmt.Errorf("use una red privada (10/8, 172.16/12 o 192.168/16, RFC 1918)")
	}
	return cidr, nil
}

// networkXML arma la definicion de la red. El hipervisor toma la primera IP
// de la subred (sera la puerta de enlace de las VMs) y, si se pide, reparte
// por DHCP desde la mitad del rango hasta el penultimo equipo.
func networkXML(spec NetworkSpec, cidr *net.IPNet) string {
	base := ipToUint(cidr.IP.To4())
	ones, bits := cidr.Mask.Size()
	size := uint32(1) << uint(bits-ones)
	gateway := uintToIP(base + 1)
	mask := net.IP(cidr.Mask).String()
	var b strings.Builder
	fmt.Fprintf(&b, "<network>\n  <name>%s</name>\n", spec.Name)
	switch spec.Mode {
	case "nat":
		b.WriteString("  <forward mode='nat'/>\n")
	case "route":
		b.WriteString("  <forward mode='route'/>\n")
	}
	b.WriteString("  <bridge stp='on' delay='0'/>\n")
	fmt.Fprintf(&b, "  <ip address='%s' netmask='%s'>\n", gateway, mask)
	if spec.DHCP {
		fmt.Fprintf(&b, "    <dhcp>\n      <range start='%s' end='%s'/>\n    </dhcp>\n", uintToIP(base+size/2), uintToIP(base+size-2))
	}
	b.WriteString("  </ip>\n</network>\n")
	return b.String()
}

func ipToUint(ip net.IP) uint32 {
	return uint32(ip[0])<<24 | uint32(ip[1])<<16 | uint32(ip[2])<<8 | uint32(ip[3])
}

func uintToIP(n uint32) string {
	return fmt.Sprintf("%d.%d.%d.%d", n>>24, (n>>16)&255, (n>>8)&255, n&255)
}

// CreateNetwork define, enciende y marca para arranque automatico una red.
func (p *LibvirtProvider) CreateNetwork(spec NetworkSpec) error {
	if p == nil || p.sshClient == nil {
		return fmt.Errorf("cliente SSH no inicializado")
	}
	cidr, err := ValidateNetworkSpec(spec)
	if err != nil {
		return err
	}
	existing, err := p.ListNetworks()
	if err != nil {
		return err
	}
	for _, n := range existing {
		if n.Name == spec.Name {
			return fmt.Errorf("ya existe una red llamada %s", spec.Name)
		}
		if _, other, err := net.ParseCIDR(n.Subnet); err == nil && (other.Contains(cidr.IP) || cidr.Contains(other.IP)) {
			return fmt.Errorf("la subred %s se cruza con la red %s (%s)", cidr, n.Name, n.Subnet)
		}
	}
	tmp := "/tmp/ejecutor-red-" + spec.Name + ".xml"
	encoded := base64.StdEncoding.EncodeToString([]byte(networkXML(spec, cidr)))
	name := quoteShell(spec.Name)
	command := fmt.Sprintf("printf %%s %s | base64 --decode > %s && %snet-define %s && %snet-start %s && %snet-autostart %s; status=$?; rm -f %s; exit $status",
		quoteShell(encoded), quoteShell(tmp), virshCmd, quoteShell(tmp), virshCmd, name, virshCmd, name, quoteShell(tmp))
	if out, err := p.sshClient.ExecuteCommand(command); err != nil {
		// Si se definio pero no arranco (ej. la subred ya la usa el
		// hipervisor), se deshace para no dejar una red a medias.
		_, _ = p.sshClient.ExecuteCommand(virshCmd + "net-undefine " + name)
		return fmt.Errorf("crear la red %s: %w: %s", spec.Name, err, strings.TrimSpace(string(out)))
	}
	return nil
}

// DeleteNetwork apaga y borra una red que ninguna VM usa. "default" no se
// borra: es la red con la que se crean las VMs.
func (p *LibvirtProvider) DeleteNetwork(name string) error {
	if p == nil || p.sshClient == nil {
		return fmt.Errorf("cliente SSH no inicializado")
	}
	if err := ValidateName(name); err != nil {
		return err
	}
	if name == "default" {
		return fmt.Errorf("la red default no se elimina: es la red NAT con la que salen a internet las VMs")
	}
	networks, err := p.ListNetworks()
	if err != nil {
		return err
	}
	for _, n := range networks {
		if n.Name == name && len(n.VMs) > 0 {
			return fmt.Errorf("la red %s la usan: %s. Quíteles esa tarjeta primero", name, strings.Join(n.VMs, ", "))
		}
	}
	_, _ = p.sshClient.ExecuteCommand(virshCmd + "net-destroy " + quoteShell(name))
	if out, err := p.sshClient.ExecuteCommand(virshCmd + "net-undefine " + quoteShell(name)); err != nil {
		return fmt.Errorf("eliminar la red %s: %w: %s", name, err, strings.TrimSpace(string(out)))
	}
	return nil
}

// domainNics lee las tarjetas de una VM desde su definicion (domiflist
// --inactive incluye las de VMs apagadas).
func (p *LibvirtProvider) domainNics(name string) []VMNic {
	out, err := p.sshClient.ExecuteCommand(virshCmd + "domiflist " + quoteShell(name) + " --inactive")
	if err != nil {
		return nil
	}
	nics := []VMNic{}
	for _, line := range strings.Split(string(out), "\n") {
		// Interface  Type  Source  Model  MAC
		f := strings.Fields(line)
		if len(f) < 5 || f[1] != "network" {
			continue
		}
		nics = append(nics, VMNic{Network: f[2], Model: f[3], MAC: strings.ToLower(f[4])})
	}
	return nics
}

func (p *LibvirtProvider) isRunning(name string) bool {
	out, err := p.sshClient.ExecuteCommand(virshCmd + "domstate " + quoteShell(name))
	return err == nil && parseVMStatus(strings.ToLower(strings.TrimSpace(string(out)))) == VMStatusRunning
}

// AttachNic agrega una tarjeta virtio conectada a una red. Si la VM esta
// encendida aparece al instante (--live) y queda en la definicion (--config).
func (p *LibvirtProvider) AttachNic(name, network string) error {
	if p == nil || p.sshClient == nil {
		return fmt.Errorf("cliente SSH no inicializado")
	}
	if err := ValidateName(name); err != nil {
		return err
	}
	if err := ValidateName(network); err != nil {
		return fmt.Errorf("red: %w", err)
	}
	flags := " --config"
	if p.isRunning(name) {
		flags += " --live"
	}
	if out, err := p.sshClient.ExecuteCommand(virshCmd + "attach-interface " + quoteShell(name) + " network " + quoteShell(network) + " --model virtio" + flags); err != nil {
		return fmt.Errorf("agregar tarjeta en %s: %w: %s", network, err, strings.TrimSpace(string(out)))
	}
	return nil
}

// DetachNic quita la tarjeta con esa MAC.
func (p *LibvirtProvider) DetachNic(name, mac string) error {
	if p == nil || p.sshClient == nil {
		return fmt.Errorf("cliente SSH no inicializado")
	}
	if err := ValidateName(name); err != nil {
		return err
	}
	if _, err := net.ParseMAC(mac); err != nil {
		return fmt.Errorf("MAC inválida: %s", mac)
	}
	flags := " --config"
	if p.isRunning(name) {
		flags += " --live"
	}
	if out, err := p.sshClient.ExecuteCommand(virshCmd + "detach-interface " + quoteShell(name) + " network --mac " + quoteShell(strings.ToLower(mac)) + flags); err != nil {
		return fmt.Errorf("quitar la tarjeta %s: %w: %s", mac, err, strings.TrimSpace(string(out)))
	}
	return nil
}
