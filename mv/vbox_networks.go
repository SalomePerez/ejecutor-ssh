package vm

import (
	"fmt"
	"net"
	"sort"
	"strconv"
	"strings"
)

// ---------- Redes de VirtualBox y tarjetas de las VMs ----------
// Cada tarjeta (hasta 8 por VM) se conecta a un tipo de red:
//   nat            NAT propio de la tarjeta: sale a Internet, nadie entra.
//   puente:<nic>   la red fisica del equipo (la VM es un equipo mas de la LAN).
//   interna:<x>    switch virtual solo entre VMs; sin anfitrion ni Internet.
//   solo-anfitrion:<nic>  red entre las VMs y el anfitrion.
//   red-nat:<x>    NAT compartido por varias VMs, que se ven entre si.
// Los nombres con prefijo son los mismos que muestra vboxNics.
// Referencia: https://www.virtualbox.org/manual/ch06.html

// natDefault es el nombre de la red NAT propia de cada tarjeta. "default"
// se acepta como sinonimo: es el nombre de la red por defecto en libvirt y
// el que envia el formulario de creacion.
const natDefault = "nat"

// nicArgs traduce un nombre de red a los argumentos de modifyvm de la
// tarjeta n.
func nicArgs(n int, network string) ([]string, error) {
	slot := strconv.Itoa(n)
	kind, value, _ := strings.Cut(network, ":")
	if network == natDefault || network == "default" {
		return []string{"--nic" + slot, "nat"}, nil
	}
	if strings.TrimSpace(value) == "" {
		return nil, fmt.Errorf("red %q no reconocida: use nat, puente:<adaptador>, interna:<nombre>, solo-anfitrion:<adaptador> o red-nat:<nombre>", network)
	}
	switch kind {
	case "puente":
		return []string{"--nic" + slot, "bridged", "--bridgeadapter" + slot, value}, nil
	case "interna":
		if err := ValidateName(value); err != nil {
			return nil, fmt.Errorf("red interna %q: %w", value, err)
		}
		return []string{"--nic" + slot, "intnet", "--intnet" + slot, value}, nil
	case "solo-anfitrion":
		return []string{"--nic" + slot, "hostonly", "--hostonlyadapter" + slot, value}, nil
	case "red-nat":
		return []string{"--nic" + slot, "natnetwork", "--nat-network" + slot, value}, nil
	}
	return nil, fmt.Errorf("tipo de red %q no reconocido", kind)
}

func (p *VBoxProvider) requireStopped(name, action string) (map[string]string, error) {
	values, err := p.info(name)
	if err != nil {
		return nil, err
	}
	if vboxStatus(values["VMState"]) == VMStatusRunning {
		return nil, fmt.Errorf("apague %s para %s: VirtualBox no agrega ni quita tarjetas con la VM encendida", name, action)
	}
	return values, nil
}

// AttachNic agrega una tarjeta (en el primer espacio libre de los 8) con
// la VM apagada.
func (p *VBoxProvider) AttachNic(name, network string) error {
	if err := ValidateName(name); err != nil {
		return err
	}
	values, err := p.requireStopped(name, "agregar una tarjeta")
	if err != nil {
		return err
	}
	slot := 0
	for n := 1; n <= 8; n++ {
		if kind := values[fmt.Sprintf("nic%d", n)]; kind == "" || kind == "none" {
			slot = n
			break
		}
	}
	if slot == 0 {
		return fmt.Errorf("%s ya tiene las 8 tarjetas que permite VirtualBox", name)
	}
	args, err := nicArgs(slot, network)
	if err != nil {
		return err
	}
	s := strconv.Itoa(slot)
	args = append([]string{"modifyvm", name}, args...)
	args = append(args, "--nictype"+s, "virtio", "--cableconnected"+s, "on", "--macaddress"+s, "auto")
	if _, err := p.vbox.Run(args...); err != nil {
		return fmt.Errorf("agregar tarjeta a %s: %w", name, err)
	}
	return nil
}

// DetachNic quita la tarjeta con esa MAC, con la VM apagada.
func (p *VBoxProvider) DetachNic(name, mac string) error {
	if err := ValidateName(name); err != nil {
		return err
	}
	values, err := p.requireStopped(name, "quitar una tarjeta")
	if err != nil {
		return err
	}
	mac = formatMAC(mac)
	for n := 1; n <= 8; n++ {
		if formatMAC(values[fmt.Sprintf("macaddress%d", n)]) == mac && values[fmt.Sprintf("nic%d", n)] != "none" {
			if _, err := p.vbox.Run("modifyvm", name, fmt.Sprintf("--nic%d", n), "none"); err != nil {
				return fmt.Errorf("quitar tarjeta %s de %s: %w", mac, name, err)
			}
			return nil
		}
	}
	return fmt.Errorf("%s no tiene una tarjeta con la MAC %s", name, mac)
}

func maskPrefix(mask string) int {
	bits, _ := maskToPrefix(mask)
	return bits
}

func subnetOf(ip, mask string) string {
	addr := net.ParseIP(ip).To4()
	m := net.IPMask(net.ParseIP(mask).To4())
	if addr == nil || len(m) != 4 {
		return ""
	}
	ones, _ := m.Size()
	return fmt.Sprintf("%s/%d", addr.Mask(m), ones)
}

// ListNetworks reune las redes que puede usar una tarjeta y que VMs las usan.
func (p *VBoxProvider) ListNetworks() ([]VirtualNetwork, error) {
	names, err := p.listVMNames()
	if err != nil {
		return nil, err
	}
	usedBy := map[string][]string{}
	for _, vmName := range names {
		values, err := p.info(vmName)
		if err != nil {
			continue
		}
		seen := map[string]bool{}
		for _, nic := range vboxNics(values) {
			if !seen[nic.Network] {
				usedBy[nic.Network] = append(usedBy[nic.Network], vmName)
				seen[nic.Network] = true
			}
		}
	}
	dhcp := p.dhcpServers()
	vms := func(name string) []string {
		if list := usedBy[name]; list != nil {
			return list
		}
		return []string{}
	}

	result := []VirtualNetwork{{Name: natDefault, Mode: "nat", Subnet: "10.0.2.0/24 (una por tarjeta)", HostIP: "10.0.2.2",
		DHCPStart: "10.0.2.15", DHCPEnd: "10.0.2.15", Active: true, Autostart: true, VMs: vms(natDefault)}}

	if out, err := p.vbox.Run("list", "bridgedifs"); err == nil {
		for _, b := range parseBlocks(string(out), "Name") {
			name := "puente:" + b["Name"]
			if b["Status"] != "Up" && len(usedBy[name]) == 0 {
				continue
			}
			result = append(result, VirtualNetwork{Name: name, Mode: "bridge", Bridge: b["Name"],
				Subnet: subnetOf(b["IPAddress"], b["NetworkMask"]), Active: b["Status"] == "Up", Autostart: true, VMs: vms(name)})
		}
	}

	if out, err := p.vbox.Run("list", "hostonlyifs"); err == nil {
		for _, b := range parseBlocks(string(out), "Name") {
			name := "solo-anfitrion:" + b["Name"]
			n := VirtualNetwork{Name: name, Mode: "hostonly", Bridge: b["Name"], HostIP: b["IPAddress"], Prefix: maskPrefix(b["NetworkMask"]),
				Subnet: subnetOf(b["IPAddress"], b["NetworkMask"]), Active: b["Status"] == "Up", Autostart: true, VMs: vms(name)}
			if d, ok := dhcp["HostInterfaceNetworking-"+b["Name"]]; ok && d.Enabled {
				n.DHCPStart, n.DHCPEnd = d.Lower, d.Upper
			}
			result = append(result, n)
		}
	}

	if out, err := p.vbox.Run("list", "natnets"); err == nil {
		for _, b := range parseBlocks(string(out), "Name") {
			name := "red-nat:" + b["Name"]
			n := VirtualNetwork{Name: name, Mode: "nat", Subnet: b["Network"], HostIP: b["Gateway"],
				Active: strings.EqualFold(b["Enabled"], "Yes"), Autostart: true, VMs: vms(name)}
			if d, ok := dhcp[b["Name"]]; ok && d.Enabled {
				n.DHCPStart, n.DHCPEnd = d.Lower, d.Upper
			}
			result = append(result, n)
		}
	}

	// Redes internas: las que usa alguna VM y las que la app registro con
	// un servidor DHCP (activo o no: guarda la subred de la red).
	internal := map[string]bool{}
	for name := range usedBy {
		if v, ok := strings.CutPrefix(name, "interna:"); ok {
			internal[v] = true
		}
	}
	for network := range dhcp {
		if !strings.HasPrefix(network, "HostInterfaceNetworking-") && !strings.HasPrefix(network, "NatNetwork") {
			internal[network] = true
		}
	}
	natNames := map[string]bool{}
	for _, n := range result {
		if v, ok := strings.CutPrefix(n.Name, "red-nat:"); ok {
			natNames[v] = true
		}
	}
	internalNames := []string{}
	for v := range internal {
		if !natNames[v] {
			internalNames = append(internalNames, v)
		}
	}
	sort.Strings(internalNames)
	for _, v := range internalNames {
		name := "interna:" + v
		n := VirtualNetwork{Name: name, Mode: "internal", Bridge: v, Active: true, Autostart: true, VMs: vms(name)}
		if d, ok := dhcp[v]; ok {
			n.Subnet = subnetOf(d.ServerIP, d.Mask)
			n.Prefix = maskPrefix(d.Mask)
			if d.Enabled {
				n.HostIP, n.DHCPStart, n.DHCPEnd = d.ServerIP, d.Lower, d.Upper
			}
		}
		result = append(result, n)
	}
	return result, nil
}

// CreateNetwork crea una red:
//   - isolated / internal: red interna. Su subred se guarda como un servidor
//     DHCP de VirtualBox (deshabilitado si no se pidio DHCP): una red interna
//     no existe hasta que una VM la usa y este es el unico lugar donde
//     VirtualBox guarda datos de ella.
//   - nat: red NAT compartida (natnetwork).
func (p *VBoxProvider) CreateNetwork(spec NetworkSpec) error {
	if err := ValidateName(spec.Name); err != nil {
		return err
	}
	_, cidr, err := net.ParseCIDR(strings.TrimSpace(spec.Subnet))
	if err != nil {
		return fmt.Errorf("subred no válida: %s", spec.Subnet)
	}
	base := ipToUint(cidr.IP)
	ones, bits := cidr.Mask.Size()
	size := uint32(1) << uint(bits-ones)
	switch spec.Mode {
	case "isolated", "internal":
		// .1 queda libre para el router de la red; el DHCP usa .2.
		args := []string{"dhcpserver", "add", "--network=" + spec.Name,
			"--server-ip=" + uintToIP(base+2), "--netmask=" + net.IP(cidr.Mask).String(),
			"--lower-ip=" + uintToIP(base+size/2), "--upper-ip=" + uintToIP(base+size-2)}
		if spec.DHCP {
			args = append(args, "--enable")
		} else {
			args = append(args, "--disable")
		}
		if _, err := p.vbox.Run(args...); err != nil {
			return fmt.Errorf("crear red interna %s: %w", spec.Name, err)
		}
	case "nat":
		dhcp := "off"
		if spec.DHCP {
			dhcp = "on"
		}
		if _, err := p.vbox.Run("natnetwork", "add", "--netname", spec.Name, "--network", cidr.String(), "--enable", "--dhcp", dhcp); err != nil {
			return fmt.Errorf("crear red NAT %s: %w", spec.Name, err)
		}
	default:
		return fmt.Errorf("en VirtualBox se pueden crear redes internas (aisladas) o NAT; el modo %q no aplica", spec.Mode)
	}
	return nil
}

// DeleteNetwork borra una red interna o NAT que ninguna VM use.
func (p *VBoxProvider) DeleteNetwork(name string) error {
	networks, err := p.ListNetworks()
	if err != nil {
		return err
	}
	for _, n := range networks {
		if n.Name != name {
			continue
		}
		if len(n.VMs) > 0 {
			return fmt.Errorf("la red %s la usan %s: quite primero esas tarjetas", name, strings.Join(n.VMs, ", "))
		}
		if v, ok := strings.CutPrefix(name, "interna:"); ok {
			if _, err := p.vbox.Run("dhcpserver", "remove", "--network="+v); err != nil {
				return fmt.Errorf("eliminar red interna %s: %w", v, err)
			}
			return nil
		}
		if v, ok := strings.CutPrefix(name, "red-nat:"); ok {
			if _, err := p.vbox.Run("natnetwork", "remove", "--netname", v); err != nil {
				return fmt.Errorf("eliminar red NAT %s: %w", v, err)
			}
			return nil
		}
		return fmt.Errorf("la red %s es de VirtualBox o del equipo y no se elimina desde la app", name)
	}
	return fmt.Errorf("no existe la red %s", name)
}

// HostInfo resume memoria y CPU del equipo anfitrion (list hostinfo).
func (p *VBoxProvider) HostInfo() (*HostInfo, error) {
	out, err := p.vbox.Run("list", "hostinfo")
	if err != nil {
		return nil, fmt.Errorf("leer el anfitrion: %w", err)
	}
	info := &HostInfo{}
	for _, line := range strings.Split(strings.ReplaceAll(string(out), "\r", ""), "\n") {
		key, value, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		fields := strings.Fields(value)
		if len(fields) == 0 {
			continue
		}
		number, _ := strconv.Atoi(fields[0])
		switch strings.TrimSpace(key) {
		case "Processor count":
			info.CPUs = number
		case "Memory size":
			info.MemoryMB = number
		case "Memory available":
			info.AvailableMB = number
		}
	}
	if info.MemoryMB == 0 {
		return nil, fmt.Errorf("salida inesperada de VBoxManage list hostinfo")
	}
	return info, nil
}
