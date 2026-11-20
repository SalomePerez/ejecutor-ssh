package handlers

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"ejecutor-ssh/config"
	vm "ejecutor-ssh/mv"
	"ejecutor-ssh/sshkeys"
)

type VMHandler struct {
	provider vm.Provider
	config   *config.Config
	// net guarda la ultima lectura de red de cada VM para las vistas previas.
	net netCache
}

type createVMRequest struct {
	Name      string        `json:"name"`
	Distro    string        `json:"distro"`
	CPU       int           `json:"cpu"`
	MemoryMB  int           `json:"memoryMB"`
	DiskGB    int           `json:"diskGB"`
	SSHUser   string        `json:"sshUser"`
	ImagePath string        `json:"imagePath,omitempty"`
	Disks     []diskRequest `json:"disks,omitempty"`
	Networks  []string      `json:"networks,omitempty"`
	Emulated  bool          `json:"emulated,omitempty"`
	// Management agrega la interfaz de gestion (canal de gestion).
	Management bool `json:"management,omitempty"`
}

type diskRequest struct {
	Name      string `json:"name"`
	SizeGB    int    `json:"sizeGB"`
	ReadOnly  bool   `json:"readOnly,omitempty"`
	Shareable bool   `json:"shareable,omitempty"`
}

type vmResponse struct {
	Success bool        `json:"success"`
	Message string      `json:"message,omitempty"`
	Data    interface{} `json:"data,omitempty"`
	Error   string      `json:"error,omitempty"`
	// Warning informa riesgos que no impiden la operacion (ej. discos
	// compartidos sin bloqueo entre VMs). Ver vm.ShareableDiskWarning.
	Warning string `json:"warning,omitempty"`
}

func NewVMHandler(cfg *config.Config, provider vm.Provider) *VMHandler {
	return &VMHandler{provider: provider, config: cfg}
}

func (h *VMHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	path := r.URL.Path

	if name, action, ok := networkRoute(path); ok {
		h.serveNetwork(w, r, name, action)
		return
	}

	switch {
	case r.Method == http.MethodGet && path == "/api/vms":
		h.listVMs(w, r)
	case r.Method == http.MethodGet && path == "/api/templates":
		h.listTemplates(w, r)
	case r.Method == http.MethodGet && path == "/api/hypervisor":
		h.hostInfo(w, r)
	case r.Method == http.MethodGet && path == "/api/netcmds":
		writeVMResponse(w, http.StatusOK, vmResponse{Success: true, Data: map[string]interface{}{"commands": vm.NetCommands(), "filters": vm.TcpdumpFilters()}})
	case strings.HasPrefix(path, "/api/networks"):
		h.serveNetworks(w, r, strings.Trim(strings.TrimPrefix(path, "/api/networks"), "/"))
	case strings.HasPrefix(path, "/api/vms/") && strings.Contains(path, "/interfaces"):
		rest := strings.TrimPrefix(path, "/api/vms/")
		name, mac, _ := strings.Cut(rest, "/interfaces")
		h.serveNics(w, r, name, strings.Trim(mac, "/"))
	case r.Method == http.MethodPost && strings.HasPrefix(path, "/api/vms/") && strings.HasSuffix(path, "/repair"):
		name := strings.TrimSuffix(strings.TrimPrefix(path, "/api/vms/"), "/repair")
		h.repairAgent(w, r, name)
	case r.Method == http.MethodGet && strings.HasPrefix(path, "/api/vms/") && strings.HasSuffix(path, "/disks"):
		name := strings.TrimSuffix(strings.TrimPrefix(path, "/api/vms/"), "/disks")
		h.listDisks(w, r, name)
	case r.Method == http.MethodPost && path == "/api/vms":
		h.createVM(w, r)
	case r.Method == http.MethodPost && strings.HasPrefix(path, "/api/vms/") && strings.HasSuffix(path, "/start"):
		name := strings.TrimSuffix(strings.TrimPrefix(path, "/api/vms/"), "/start")
		h.startVM(w, r, name)
	case r.Method == http.MethodPost && strings.HasPrefix(path, "/api/vms/") && strings.HasSuffix(path, "/console"):
		name := strings.TrimSuffix(strings.TrimPrefix(path, "/api/vms/"), "/console")
		h.openConsole(w, r, name)
	case r.Method == http.MethodPost && strings.HasPrefix(path, "/api/vms/") && strings.HasSuffix(path, "/stop"):
		name := strings.TrimSuffix(strings.TrimPrefix(path, "/api/vms/"), "/stop")
		h.stopVM(w, r, name)
	case r.Method == http.MethodPost && strings.HasPrefix(path, "/api/vms/") && strings.HasSuffix(path, "/rename"):
		name := strings.TrimSuffix(strings.TrimPrefix(path, "/api/vms/"), "/rename")
		h.renameVM(w, r, name)
	case r.Method == http.MethodPut && strings.HasPrefix(path, "/api/vms/"):
		name := strings.TrimPrefix(path, "/api/vms/")
		h.updateVM(w, r, name)
	case r.Method == http.MethodDelete && strings.HasPrefix(path, "/api/vms/"):
		name := strings.TrimPrefix(path, "/api/vms/")
		name = strings.TrimSuffix(name, "/")
		h.deleteVM(w, r, name)
	default:
		http.NotFound(w, r)
	}
}

// consoleProvider lo implementan los proveedores que pueden abrir la
// consola del hipervisor de una VM.
type consoleProvider interface {
	OpenConsole(name string) error
}

// openConsole maneja POST /api/vms/{vm}/console: abre la ventana de la VM en
// el hipervisor (primer mecanismo de acceso, sin red).
func (h *VMHandler) openConsole(w http.ResponseWriter, r *http.Request, name string) {
	cp, ok := h.provider.(consoleProvider)
	if !ok {
		writeVMResponse(w, http.StatusNotImplemented, vmResponse{Success: false, Error: "este hipervisor no abre consolas desde la app"})
		return
	}
	if err := cp.OpenConsole(name); err != nil {
		writeVMResponse(w, http.StatusBadGateway, vmResponse{Success: false, Error: err.Error()})
		return
	}
	writeVMResponse(w, http.StatusOK, vmResponse{Success: true, Message: "Consola de " + name + " abierta en el hipervisor."})
}

func (h *VMHandler) listTemplates(w http.ResponseWriter, r *http.Request) {
	templates, err := h.provider.ListTemplates()
	if err != nil {
		writeVMResponse(w, http.StatusBadGateway, vmResponse{Success: false, Error: err.Error()})
		return
	}
	writeVMResponse(w, http.StatusOK, vmResponse{Success: true, Data: templates})
}

func (h *VMHandler) listDisks(w http.ResponseWriter, r *http.Request, name string) {
	disks, err := h.provider.ListDisks(name)
	if err != nil {
		writeVMResponse(w, http.StatusBadGateway, vmResponse{Success: false, Error: err.Error()})
		return
	}
	writeVMResponse(w, http.StatusOK, vmResponse{Success: true, Data: disks})
}

func (h *VMHandler) listVMs(w http.ResponseWriter, r *http.Request) {
	vms, err := h.provider.List()
	if err != nil {
		writeVMResponse(w, http.StatusInternalServerError, vmResponse{Success: false, Error: err.Error()})
		return
	}
	// Ruta absoluta de la llave, para que el comando ssh que muestra la
	// interfaz funcione desde cualquier carpeta.
	for i := range vms {
		keyName := vms[i].SSHKeyName
		if keyName == "" {
			keyName = vms[i].Name + "-ssh"
		}
		if abs, err := filepath.Abs(filepath.Join(h.config.KeysStorePath, keyName)); err == nil {
			if _, err := os.Stat(abs); err == nil {
				vms[i].SSHPrivateKeyPath = abs
			}
		}
	}
	writeVMResponse(w, http.StatusOK, vmResponse{Success: true, Data: vms})
}

func (h *VMHandler) createVM(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 4096)
	var req createVMRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeVMResponse(w, http.StatusBadRequest, vmResponse{Success: false, Error: "JSON inválido"})
		return
	}

	if err := vm.ValidateName(req.Name); err != nil {
		writeVMResponse(w, http.StatusBadRequest, vmResponse{Success: false, Error: err.Error()})
		return
	}
	if err := vm.ValidateDistro(req.Distro); err != nil {
		writeVMResponse(w, http.StatusBadRequest, vmResponse{Success: false, Error: err.Error()})
		return
	}
	if req.CPU <= 0 || req.MemoryMB <= 0 || req.DiskGB <= 0 {
		writeVMResponse(w, http.StatusBadRequest, vmResponse{Success: false, Error: "CPU, memoria y disco deben ser mayores que cero"})
		return
	}
	if req.SSHUser == "" {
		req.SSHUser = "ubuntu"
	}
	disks := make([]vm.DiskSpec, 0, len(req.Disks))
	for index, disk := range req.Disks {
		if disk.SizeGB < 1 || disk.SizeGB > 1024 {
			writeVMResponse(w, http.StatusBadRequest, vmResponse{Success: false, Error: fmt.Sprintf("el disco adicional %d debe tener entre 1 y 1024 GB", index+1)})
			return
		}
		disks = append(disks, vm.DiskSpec{Name: disk.Name, SizeGB: disk.SizeGB, ReadOnly: disk.ReadOnly, Shareable: disk.Shareable})
	}

	keyName := req.Name + "-ssh"
	privateKeyPath := filepath.Join(h.config.KeysStorePath, keyName)
	publicKeyPath := privateKeyPath + ".pub"
	if _, err := os.Stat(privateKeyPath); err == nil {
		writeVMResponse(w, http.StatusConflict, vmResponse{Success: false, Error: fmt.Sprintf("ya existe una llave SSH para la VM '%s'", req.Name)})
		return
	} else if !os.IsNotExist(err) {
		writeVMResponse(w, http.StatusInternalServerError, vmResponse{Success: false, Error: fmt.Sprintf("no se puede validar la llave SSH de la VM: %v", err)})
		return
	}
	if _, err := os.Stat(publicKeyPath); err == nil {
		writeVMResponse(w, http.StatusConflict, vmResponse{Success: false, Error: fmt.Sprintf("ya existe una llave SSH para la VM '%s'", req.Name)})
		return
	} else if !os.IsNotExist(err) {
		writeVMResponse(w, http.StatusInternalServerError, vmResponse{Success: false, Error: fmt.Sprintf("no se puede validar la llave pública de la VM: %v", err)})
		return
	}

	keyPair, err := sshkeys.GenerateED25519()
	if err != nil {
		writeVMResponse(w, http.StatusInternalServerError, vmResponse{Success: false, Error: fmt.Sprintf("error generando llaves SSH para la VM: %v", err)})
		return
	}
	privatePath, publicPath, err := keyPair.SaveToFiles(h.config.KeysStorePath, keyName)
	if err != nil {
		writeVMResponse(w, http.StatusInternalServerError, vmResponse{Success: false, Error: fmt.Sprintf("error guardando llaves SSH para la VM: %v", err)})
		return
	}
	publicKey, err := os.ReadFile(publicPath)
	if err != nil {
		_ = sshkeys.DeleteKeyPair(h.config.KeysStorePath, keyName)
		writeVMResponse(w, http.StatusInternalServerError, vmResponse{Success: false, Error: fmt.Sprintf("error leyendo la llave pública para la VM: %v", err)})
		return
	}

	created, err := h.provider.Create(vm.CreateSpec{
		Name:         req.Name,
		Distro:       req.Distro,
		CPU:          req.CPU,
		MemoryMB:     req.MemoryMB,
		DiskGB:       req.DiskGB,
		SSHUser:      req.SSHUser,
		SSHPublicKey: string(publicKey),
		ImagePath:    req.ImagePath,
		Disks:        disks,
		Networks:     req.Networks,
		Emulated:     req.Emulated,
		Management:   req.Management,
	})
	if err != nil {
		_ = sshkeys.DeleteKeyPair(h.config.KeysStorePath, keyName)
		writeVMResponse(w, http.StatusBadGateway, vmResponse{Success: false, Error: err.Error()})
		return
	}
	created.SSHKeyName = keyName
	created.SSHPrivateKeyPath = filepath.Clean(privatePath)
	created.SSHPublicKeyPath = filepath.Clean(publicPath)

	warning := ""
	if vm.HasShareableDisk(disks) {
		warning = vm.ShareableDiskWarning
	}

	writeVMResponse(w, http.StatusCreated, vmResponse{Success: true, Message: "VM creada correctamente", Data: created, Warning: warning})
}

func (h *VMHandler) startVM(w http.ResponseWriter, r *http.Request, name string) {
	if err := h.provider.Start(name); err != nil {
		writeVMResponse(w, http.StatusBadGateway, vmResponse{Success: false, Error: err.Error()})
		return
	}
	writeVMResponse(w, http.StatusOK, vmResponse{Success: true, Message: fmt.Sprintf("VM '%s' iniciada", name)})
}

func (h *VMHandler) stopVM(w http.ResponseWriter, r *http.Request, name string) {
	if err := h.provider.Stop(name); err != nil {
		writeVMResponse(w, http.StatusBadGateway, vmResponse{Success: false, Error: err.Error()})
		return
	}
	writeVMResponse(w, http.StatusOK, vmResponse{Success: true, Message: fmt.Sprintf("VM '%s' detenida", name)})
}

func (h *VMHandler) deleteVM(w http.ResponseWriter, r *http.Request, name string) {
	// Se consulta antes de borrar solo para informar en la respuesta; el
	// proveedor hace su propia verificacion (autoritativa) para decidir que
	// discos preservar, asi que un fallo aqui no debe bloquear el borrado.
	preDeleteDisks, _ := h.provider.ListDisks(name)

	if err := h.provider.Delete(name); err != nil {
		writeVMResponse(w, http.StatusBadGateway, vmResponse{Success: false, Error: err.Error()})
		return
	}

	var preserved []string
	for _, disk := range preDeleteDisks {
		if disk.Shareable {
			preserved = append(preserved, disk.Name)
		}
	}
	warning := ""
	if len(preserved) > 0 {
		warning = fmt.Sprintf("Se preservaron %d disco(s) compartido(s) (%s): no se eliminan automáticamente porque otras VMs pueden seguir usándolos.", len(preserved), strings.Join(preserved, ", "))
	}

	// La llave de la VM ya no sirve para nada y, si se queda, impide crear
	// otra VM con el mismo nombre.
	_ = sshkeys.DeleteKeyPair(h.config.KeysStorePath, name+"-ssh")

	writeVMResponse(w, http.StatusOK, vmResponse{Success: true, Message: fmt.Sprintf("VM '%s' eliminada", name), Warning: warning})
}

func (h *VMHandler) renameVM(w http.ResponseWriter, r *http.Request, oldName string) {
	var req struct {
		NewName string `json:"newName"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeVMResponse(w, http.StatusBadRequest, vmResponse{Success: false, Error: "JSON inválido"})
		return
	}
	if req.NewName == "" {
		writeVMResponse(w, http.StatusBadRequest, vmResponse{Success: false, Error: "newName es requerido"})
		return
	}
	if err := h.provider.Rename(oldName, req.NewName); err != nil {
		writeVMResponse(w, http.StatusBadGateway, vmResponse{Success: false, Error: err.Error()})
		return
	}
	writeVMResponse(w, http.StatusOK, vmResponse{Success: true, Message: fmt.Sprintf("VM '%s' renombrada a '%s'", oldName, req.NewName)})
}

func (h *VMHandler) updateVM(w http.ResponseWriter, r *http.Request, name string) {
	var req struct {
		Name     string `json:"name"`
		CPU      int    `json:"cpu"`
		MemoryMB int    `json:"memoryMB"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeVMResponse(w, http.StatusBadRequest, vmResponse{Success: false, Error: "JSON inválido"})
		return
	}
	if req.Name == "" {
		req.Name = name
	}
	if req.Name != name {
		if err := vm.ValidateName(req.Name); err != nil {
			writeVMResponse(w, http.StatusBadRequest, vmResponse{Success: false, Error: err.Error()})
			return
		}
	}
	if err := h.provider.Update(name, vm.UpdateSpec{Name: req.Name, CPU: req.CPU, MemoryMB: req.MemoryMB}); err != nil {
		writeVMResponse(w, http.StatusBadGateway, vmResponse{Success: false, Error: err.Error()})
		return
	}
	if req.Name != name {
		// La llave sigue el nombre de la VM (<nombre>-ssh).
		oldKey := filepath.Join(h.config.KeysStorePath, name+"-ssh")
		newKey := filepath.Join(h.config.KeysStorePath, req.Name+"-ssh")
		if _, err := os.Stat(newKey); os.IsNotExist(err) {
			_ = os.Rename(oldKey, newKey)
			_ = os.Rename(oldKey+".pub", newKey+".pub")
		}
	}
	writeVMResponse(w, http.StatusOK, vmResponse{Success: true, Message: fmt.Sprintf("VM '%s' actualizada", name)})
}

// agentRepairer y hostInspector son capacidades opcionales del proveedor
// (las tiene LibvirtProvider); asi los dobles de prueba no las necesitan.
type agentRepairer interface {
	RepairGuestAgent(name, user, publicKey string) error
}

type hostInspector interface {
	HostInfo() (*vm.HostInfo, error)
}

// repairAgent maneja POST /api/vms/{nombre}/repair: vuelve a conectar la
// semilla de cloud-init y reinicia la VM, para las que nunca terminaron su
// primer arranque (sin usuario, sin llave y sin qemu-guest-agent).
func (h *VMHandler) repairAgent(w http.ResponseWriter, r *http.Request, name string) {
	repairer, ok := h.provider.(agentRepairer)
	if !ok {
		writeVMResponse(w, http.StatusNotImplemented, vmResponse{Success: false, Error: "el proveedor no permite reparar el agente"})
		return
	}
	if err := vm.ValidateName(name); err != nil {
		writeVMResponse(w, http.StatusBadRequest, vmResponse{Success: false, Error: err.Error()})
		return
	}
	var req struct {
		SSHUser string `json:"sshUser"`
	}
	r.Body = http.MaxBytesReader(w, r.Body, 1024)
	_ = json.NewDecoder(r.Body).Decode(&req)
	if req.SSHUser == "" {
		req.SSHUser = "ubuntu"
	}

	// Se reutiliza la llave de la VM; si no existe, se genera.
	keyName := name + "-ssh"
	publicPath := filepath.Join(h.config.KeysStorePath, keyName+".pub")
	publicKey, err := os.ReadFile(publicPath)
	if err != nil {
		keyPair, genErr := sshkeys.GenerateED25519()
		if genErr != nil {
			writeVMResponse(w, http.StatusInternalServerError, vmResponse{Success: false, Error: genErr.Error()})
			return
		}
		if _, publicPath, err = keyPair.SaveToFiles(h.config.KeysStorePath, keyName); err == nil {
			publicKey, err = os.ReadFile(publicPath)
		}
		if err != nil {
			writeVMResponse(w, http.StatusInternalServerError, vmResponse{Success: false, Error: fmt.Sprintf("preparar la llave de la VM: %v", err)})
			return
		}
	}

	if err := repairer.RepairGuestAgent(name, req.SSHUser, string(publicKey)); err != nil {
		writeVMResponse(w, http.StatusBadGateway, vmResponse{Success: false, Error: err.Error()})
		return
	}
	writeVMResponse(w, http.StatusOK, vmResponse{Success: true, Message: fmt.Sprintf("VM '%s' reiniciada con cloud-init; el agente estará listo en unos minutos", name)})
}

// hostInfo maneja GET /api/hypervisor: memoria y CPU del hipervisor.
func (h *VMHandler) hostInfo(w http.ResponseWriter, r *http.Request) {
	inspector, ok := h.provider.(hostInspector)
	if !ok {
		writeVMResponse(w, http.StatusNotImplemented, vmResponse{Success: false, Error: "no disponible"})
		return
	}
	info, err := inspector.HostInfo()
	if err != nil {
		writeVMResponse(w, http.StatusBadGateway, vmResponse{Success: false, Error: err.Error()})
		return
	}
	writeVMResponse(w, http.StatusOK, vmResponse{Success: true, Data: info})
}

// networkManager es la capacidad (opcional) de administrar redes de libvirt
// y las tarjetas de cada VM.
type networkManager interface {
	ListNetworks() ([]vm.VirtualNetwork, error)
	CreateNetwork(spec vm.NetworkSpec) error
	DeleteNetwork(name string) error
	AttachNic(name, network string) error
	DetachNic(name, mac string) error
}

// serveNetworks maneja GET/POST /api/networks y DELETE /api/networks/{nombre}.
func (h *VMHandler) serveNetworks(w http.ResponseWriter, r *http.Request, name string) {
	nm, ok := h.provider.(networkManager)
	if !ok {
		writeVMResponse(w, http.StatusNotImplemented, vmResponse{Success: false, Error: "el proveedor no administra redes"})
		return
	}
	switch {
	case r.Method == http.MethodGet && name == "":
		list, err := nm.ListNetworks()
		if err != nil {
			writeVMResponse(w, http.StatusBadGateway, vmResponse{Success: false, Error: err.Error()})
			return
		}
		writeVMResponse(w, http.StatusOK, vmResponse{Success: true, Data: list})
	case r.Method == http.MethodPost && name == "":
		r.Body = http.MaxBytesReader(w, r.Body, 2048)
		var spec vm.NetworkSpec
		if err := json.NewDecoder(r.Body).Decode(&spec); err != nil {
			writeVMResponse(w, http.StatusBadRequest, vmResponse{Success: false, Error: "JSON inválido"})
			return
		}
		if _, err := vm.ValidateNetworkSpec(spec); err != nil {
			writeVMResponse(w, http.StatusBadRequest, vmResponse{Success: false, Error: err.Error()})
			return
		}
		if err := nm.CreateNetwork(spec); err != nil {
			writeVMResponse(w, http.StatusBadGateway, vmResponse{Success: false, Error: err.Error()})
			return
		}
		writeVMResponse(w, http.StatusCreated, vmResponse{Success: true, Message: fmt.Sprintf("Red '%s' creada", spec.Name)})
	case r.Method == http.MethodDelete && name != "":
		if err := nm.DeleteNetwork(name); err != nil {
			writeVMResponse(w, http.StatusBadGateway, vmResponse{Success: false, Error: err.Error()})
			return
		}
		writeVMResponse(w, http.StatusOK, vmResponse{Success: true, Message: fmt.Sprintf("Red '%s' eliminada", name)})
	default:
		http.NotFound(w, r)
	}
}

// serveNics maneja POST /api/vms/{nombre}/interfaces {"network": "..."} y
// DELETE /api/vms/{nombre}/interfaces/{mac}.
func (h *VMHandler) serveNics(w http.ResponseWriter, r *http.Request, name, mac string) {
	nm, ok := h.provider.(networkManager)
	if !ok {
		writeVMResponse(w, http.StatusNotImplemented, vmResponse{Success: false, Error: "el proveedor no administra redes"})
		return
	}
	switch {
	case r.Method == http.MethodPost && mac == "":
		var req struct {
			Network string `json:"network"`
		}
		r.Body = http.MaxBytesReader(w, r.Body, 1024)
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Network == "" {
			writeVMResponse(w, http.StatusBadRequest, vmResponse{Success: false, Error: "falta la red"})
			return
		}
		if err := nm.AttachNic(name, req.Network); err != nil {
			writeVMResponse(w, http.StatusBadGateway, vmResponse{Success: false, Error: err.Error()})
			return
		}
		writeVMResponse(w, http.StatusOK, vmResponse{Success: true, Message: fmt.Sprintf("Tarjeta en '%s' agregada a '%s'", req.Network, name)})
	case r.Method == http.MethodDelete && mac != "":
		if err := nm.DetachNic(name, mac); err != nil {
			writeVMResponse(w, http.StatusBadGateway, vmResponse{Success: false, Error: err.Error()})
			return
		}
		writeVMResponse(w, http.StatusOK, vmResponse{Success: true, Message: fmt.Sprintf("Tarjeta %s quitada de '%s'", mac, name)})
	default:
		http.NotFound(w, r)
	}
}

func writeVMResponse(w http.ResponseWriter, status int, payload vmResponse) {
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(payload)
}
