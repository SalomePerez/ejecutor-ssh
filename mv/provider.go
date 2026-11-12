package vm

type Provider interface {
	Create(spec CreateSpec) (*VM, error)
	ListTemplates() ([]Template, error)
	List() ([]VM, error)
	ListDisks(name string) ([]Disk, error)
	Start(name string) error
	Stop(name string) error
	Delete(name string) error
	Rename(oldName, newName string) error
	Update(name string, spec UpdateSpec) error
	Get(name string) (*VM, error)
}

type Template struct {
	Name   string `json:"name"`
	Path   string `json:"path"`
	Format string `json:"format"`
}

type CreateSpec struct {
	Name         string
	Distro       string
	CPU          int
	MemoryMB     int
	DiskGB       int
	SSHUser      string
	SSHPublicKey string
	ImagePath    string
	Disks        []DiskSpec
	// Networks son las redes de libvirt a las que se conecta cada tarjeta,
	// en orden. Vacio = una tarjeta en "default".
	Networks []string
	// Emulated crea la VM con emulacion de QEMU (TCG) en vez de KVM: mucho
	// mas lenta, pero no usa VT-x anidado (ver emulatedFlags).
	Emulated bool
}

type DiskSpec struct {
	Name      string
	SizeGB    int
	ReadOnly  bool
	Shareable bool
}

type UpdateSpec struct {
	Name     string
	CPU      int
	MemoryMB int
}
