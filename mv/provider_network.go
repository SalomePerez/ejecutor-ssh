package vm

// NetworkProvider lo implementan los proveedores que pueden leer y cambiar la
// red DENTRO de una VM. Va aparte de Provider para que los dobles de prueba
// existentes no tengan que implementarlo.
type NetworkProvider interface {
	ReadNetwork(name string) (*NetState, error)
	ApplyNetwork(name string, req NetRequest, revertSeconds int) (*ApplyResult, error)
}

var _ NetworkProvider = (*LibvirtProvider)(nil)
