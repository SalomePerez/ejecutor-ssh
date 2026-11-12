package vm

type VMStatus string

const (
	VMStatusCreating VMStatus = "creating"
	VMStatusRunning  VMStatus = "running"
	VMStatusStopped  VMStatus = "stopped"
	VMStatusError    VMStatus = "error"
	VMStatusDeleting VMStatus = "deleting"
)

type VM struct {
	Name              string   `json:"name"`
	Distro            string   `json:"distro"`
	CPU               int      `json:"cpu"`
	MemoryMB          int      `json:"memoryMB"`
	DiskGB            int      `json:"diskGB"`
	SSHUser           string   `json:"sshUser"`
	SSHPort           int      `json:"sshPort"`
	SSHKeyName        string   `json:"sshKeyName,omitempty"`
	SSHPrivateKeyPath string   `json:"sshPrivateKeyPath,omitempty"`
	SSHPublicKeyPath  string   `json:"sshPublicKeyPath,omitempty"`
	IP                string   `json:"ip,omitempty"`
	Status            VMStatus `json:"status"`
	CreatedAt         string   `json:"createdAt,omitempty"`
	ImagePath         string   `json:"imagePath,omitempty"`
	Disks             []Disk   `json:"disks,omitempty"`
	// Networks son las tarjetas de red de la VM, en orden (la primera es la
	// principal).
	Networks []VMNic `json:"networks,omitempty"`
}

type Disk struct {
	Name      string `json:"name"`
	Path      string `json:"path"`
	SizeGB    int    `json:"sizeGB"`
	ReadOnly  bool   `json:"readOnly,omitempty"`
	Shareable bool   `json:"shareable,omitempty"`
}
