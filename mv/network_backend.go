package vm

import "time"

// netBackend es lo que necesita el panel de red de un proveedor: ejecutar
// un script DENTRO de la VM por un canal que no dependa de su red
// (qemu-guest-agent en libvirt, guestcontrol en VirtualBox), ejecutar un
// comando en el hipervisor (reservas DHCP) y describir las redes del
// hipervisor a las que esta conectada cada tarjeta (por MAC).
// La deteccion, la validacion, los planes por gestor de red y la aplicacion
// con reversion son los mismos para todos los proveedores.
type netBackend interface {
	GuestExec(name, script string, timeout time.Duration) (*GuestExecResult, error)
	runHost(command string) ([]byte, error)
	hostNetworks(name string) map[string]*LibvirtNet
	wait(d time.Duration)
	// hypervisorName aparece en los mensajes del plan ("libvirt", "VirtualBox").
	hypervisorName() string
}

var (
	_ netBackend = (*LibvirtProvider)(nil)
	_ netBackend = (*VBoxProvider)(nil)
)

func (p *LibvirtProvider) runHost(command string) ([]byte, error) {
	return p.sshClient.ExecuteCommand(command)
}

func (p *LibvirtProvider) hypervisorName() string { return "libvirt" }

func (p *LibvirtProvider) hostNetworks(name string) map[string]*LibvirtNet {
	return p.libvirtNetworks(name)
}

// hvName es el nombre del hipervisor para los mensajes del plan.
func hvName(st *NetState) string {
	if st == nil || st.Hypervisor == "" {
		return "libvirt"
	}
	return st.Hypervisor
}
