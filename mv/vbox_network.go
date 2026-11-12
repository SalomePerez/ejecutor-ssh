package vm

import (
	"fmt"
	"strings"
)

// ---------- Panel de red sobre VirtualBox ----------
// La deteccion, la validacion, los planes por gestor de red y la aplicacion
// con reversion son los mismos que en libvirt (ver netBackend). Aqui solo se
// describe que sabe VirtualBox de la red de cada tarjeta.

var _ NetworkProvider = (*VBoxProvider)(nil)

func (p *VBoxProvider) ReadNetwork(name string) (*NetState, error) {
	return readNetwork(p, name)
}

func (p *VBoxProvider) ApplyNetwork(name string, req NetRequest, revertSeconds int) (*ApplyResult, error) {
	return applyNetwork(p, name, req, revertSeconds)
}

func (p *VBoxProvider) hypervisorName() string { return "VirtualBox" }

// runHost: en VirtualBox el plan no genera comandos para el hipervisor (la
// reserva DHCP con virsh es solo de libvirt).
func (p *VBoxProvider) runHost(command string) ([]byte, error) {
	return nil, fmt.Errorf("comando de hipervisor no soportado en VirtualBox: %s", command)
}

// dhcpServer es un bloque de "VBoxManage list dhcpservers".
type dhcpServer struct {
	ServerIP, Lower, Upper, Mask string
	Enabled                      bool
}

// parseBlocks separa salidas de "VBoxManage list ..." en bloques
// "Clave: valor" que empiezan con la clave first.
func parseBlocks(output, first string) []map[string]string {
	blocks := []map[string]string{}
	var cur map[string]string
	for _, line := range strings.Split(strings.ReplaceAll(output, "\r", ""), "\n") {
		if strings.HasPrefix(line, " ") || strings.HasPrefix(line, "\t") {
			continue // opciones anidadas del DHCP
		}
		key, value, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		key, value = strings.TrimSpace(key), strings.TrimSpace(value)
		if key == first {
			cur = map[string]string{}
			blocks = append(blocks, cur)
		}
		if cur != nil {
			cur[key] = value
		}
	}
	return blocks
}

func (p *VBoxProvider) dhcpServers() map[string]dhcpServer {
	result := map[string]dhcpServer{}
	out, err := p.vbox.Run("list", "dhcpservers")
	if err != nil {
		return result
	}
	for _, b := range parseBlocks(string(out), "NetworkName") {
		result[b["NetworkName"]] = dhcpServer{
			ServerIP: b["Dhcpd IP"], Lower: b["LowerIPAddress"], Upper: b["UpperIPAddress"],
			Mask: b["NetworkMask"], Enabled: strings.EqualFold(b["Enabled"], "Yes"),
		}
	}
	return result
}

// hostNetworks describe, por MAC, la red de VirtualBox de cada tarjeta.
// Las tarjetas en puente no aparecen: estan en la red fisica y VirtualBox
// no sabe nada de su DHCP ni de sus puertas de enlace.
func (p *VBoxProvider) hostNetworks(name string) map[string]*LibvirtNet {
	result := map[string]*LibvirtNet{}
	values, err := p.info(name)
	if err != nil {
		return result
	}
	var dhcp map[string]dhcpServer
	getDHCP := func() map[string]dhcpServer {
		if dhcp == nil {
			dhcp = p.dhcpServers()
		}
		return dhcp
	}
	for n := 1; n <= 8; n++ {
		mac := formatMAC(values[fmt.Sprintf("macaddress%d", n)])
		var ln *LibvirtNet
		switch values[fmt.Sprintf("nic%d", n)] {
		case "nat":
			// NAT propio de cada tarjeta: 10.0.(n+1).0/24, VirtualBox es la
			// puerta de enlace (.2) y su DHCP siempre entrega la .15.
			base := fmt.Sprintf("10.0.%d.", n+1)
			ln = &LibvirtNet{Network: fmt.Sprintf("NAT (tarjeta %d)", n), HostIP: base + "2", Prefix: 24, DHCPStart: base + "15", DHCPEnd: base + "15"}
		case "hostonly":
			adapter := values[fmt.Sprintf("hostonlyadapter%d", n)]
			ln = &LibvirtNet{Network: "solo anfitrión: " + adapter, Isolated: true}
			if d, ok := getDHCP()["HostInterfaceNetworking-"+adapter]; ok && d.Enabled {
				ln.HostIP, ln.DHCPStart, ln.DHCPEnd = d.ServerIP, d.Lower, d.Upper
				ln.Prefix, _ = maskToPrefix(d.Mask)
			}
		case "intnet":
			network := values[fmt.Sprintf("intnet%d", n)]
			ln = &LibvirtNet{Network: "interna: " + network, Isolated: true}
			if d, ok := getDHCP()[network]; ok && d.Enabled {
				ln.HostIP, ln.DHCPStart, ln.DHCPEnd = d.ServerIP, d.Lower, d.Upper
				ln.Prefix, _ = maskToPrefix(d.Mask)
			}
		case "natnetwork":
			network := values[fmt.Sprintf("nat-network%d", n)]
			ln = &LibvirtNet{Network: "red NAT: " + network}
			if d, ok := getDHCP()[network]; ok && d.Enabled {
				ln.HostIP, ln.DHCPStart, ln.DHCPEnd = d.ServerIP, d.Lower, d.Upper
				ln.Prefix, _ = maskToPrefix(d.Mask)
			}
		}
		if ln != nil && mac != "" {
			ln.Leases, ln.Reserved = map[string]string{}, map[string]string{}
			result[mac] = ln
		}
	}
	return result
}
