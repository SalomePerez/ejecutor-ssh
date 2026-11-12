package config

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

// Config contiene todas las configuraciones de la aplicacion.
// Los valores se leen del archivo config.properties y pueden ser
// sobrescritos por variables de entorno.
type Config struct {
	// Configuracion del servidor web
	WebAddr string
	WebPort string

	// Configuracion SSH por defecto
	SSHHost           string
	SSHPort           string
	SSHUser           string
	SSHKeyPath        string
	SSHKnownHostsPath string

	// Configuracion SSH del hipervisor de maquinas virtuales.
	VMSSHHost           string
	VMSSHPort           string
	VMSSHUser           string
	VMSSHKeyPath        string
	VMSSHKnownHostsPath string

	// VMNetRevertSeconds es cuanto espera la VM antes de volver sola a la
	// configuracion de red anterior si nadie confirma el cambio (30 a 600 s).
	VMNetRevertSeconds int

	// VMProvider elige el hipervisor: "virtualbox" (VBoxManage local) o
	// "libvirt" (por SSH, el diseno anterior con virtualizacion anidada).
	VMProvider string
	// VBoxManagePath es la ruta de VBoxManage.exe.
	VBoxManagePath string
	// Usuario y contrasena DENTRO de las VMs para guestcontrol. Igual que el
	// token, la contrasena no se expone por la API de configuracion.
	VBoxGuestUser     string
	VBoxGuestPassword string
	// Plantillas (discos .vdi/.vmdk), discos compartidos, archivos de
	// cloud-init, URL con la que las VMs ven a la app y ISO de Guest Additions.
	VBoxTemplatesDir string
	VBoxSharedDir    string
	VBoxSeedsDir     string
	VBoxSeedURL      string
	VBoxAdditionsISO string

	// Configuracion del shell remoto
	ShellType      string // cmd, powershell, both
	ShellStartPath string
	ExecutionMode  string // local o remote

	// Rutas para usuarios y llaves
	KeysStorePath  string
	UsersStorePath string
	KeysAlgorithm  string
	KeysBits       int

	// Configuracion de seguridad
	SSHTimeout      int
	RequestMaxSize  int
	AllowedCommands []string
	// APIToken protege rutas sensibles (ej. descarga de llaves privadas).
	// Solo se lee de config.properties o variable de entorno: nunca se expone
	// via ToJSON ni se acepta en UpdateFromJSON para evitar que la propia API
	// de configuracion (sin autenticacion) permita fijar el token de otro.
	APIToken string

	// Metadata
	AppName    string
	AppVersion string

	// configPath y mu se usan para guardar cambios en el archivo
	configPath string
	mu         sync.RWMutex
}

// propiedades almacena los valores del archivo config.properties.
var propiedades map[string]string

// Load lee el archivo config.properties y retorna una estructura Config.
func Load(filePath string) (*Config, error) {
	// Si el archivo no existe, usamos valores por defecto.
	propiedades = make(map[string]string)

	if _, err := os.Stat(filePath); err == nil {
		if err := leerArchivo(filePath); err != nil {
			return nil, fmt.Errorf("leer config.properties: %w", err)
		}
	}

	// Se crea la estructura con valores del archivo y variables de entorno.
	cfg := &Config{
		WebAddr:             getEnv("WEB_ADDR", get("web.addr", ":8080")),
		WebPort:             getEnv("WEB_PORT", get("web.port", "8080")),
		SSHHost:             getEnv("SSH_HOST", get("ssh.host", "localhost")),
		SSHPort:             getEnv("SSH_PORT", get("ssh.port", "22")),
		SSHUser:             getEnv("SSH_USER", get("ssh.user", "sshweb")),
		SSHKeyPath:          getEnv("SSH_KEY_PATH", get("ssh.keyPath", ".ssh/id_ed25519")),
		SSHKnownHostsPath:   getEnv("SSH_KNOWN_HOSTS", get("ssh.knownHostsPath", ".ssh/known_hosts")),
		VMSSHHost:           get("vm.ssh.host", get("ssh.host", "localhost")),
		VMSSHPort:           get("vm.ssh.port", get("ssh.port", "22")),
		VMSSHUser:           get("vm.ssh.user", get("ssh.user", "sshweb")),
		VMSSHKeyPath:        get("vm.ssh.keyPath", get("ssh.keyPath", ".ssh/id_ed25519")),
		VMSSHKnownHostsPath: get("vm.ssh.knownHostsPath", get("ssh.knownHostsPath", ".ssh/known_hosts")),
		VMNetRevertSeconds:  clampRevert(getInt("vm.network.revertSeconds", 120)),
		VMProvider:          strings.ToLower(get("vm.provider", "virtualbox")),
		VBoxManagePath:      get("vbox.manage", `C:\Program Files\Oracle\VirtualBox\VBoxManage.exe`),
		VBoxGuestUser:       getEnv("VBOX_GUEST_USER", get("vbox.guest.user", "")),
		VBoxGuestPassword:   getEnv("VBOX_GUEST_PASSWORD", get("vbox.guest.password", "")),
		VBoxTemplatesDir:    get("vbox.templates", filepath.Join(os.Getenv("USERPROFILE"), "VirtualBox VMs", "plantillas")),
		VBoxSharedDir:       get("vbox.sharedDisks", filepath.Join(os.Getenv("USERPROFILE"), "VirtualBox VMs", "discos-compartidos")),
		VBoxSeedsDir:        get("vbox.seeds", "./seeds"),
		ShellType:           get("shell.type", "cmd"),
		ShellStartPath:      get("shell.startingPath", "C:\\"),
		ExecutionMode:       getEnv("EXECUTION_MODE", get("execution.mode", "remote")),
		KeysStorePath:       get("keys.storePath", "./keys"),
		UsersStorePath:      get("users.storePath", "./users"),
		KeysAlgorithm:       get("keys.algorithm", "ed25519"),
		SSHTimeout:          getInt("ssh.timeout", 10),
		RequestMaxSize:      getInt("request.maxSize", 4096),
		AllowedCommands:     getCommands("commands.allowed", "dir,hostname,whoami,ipconfig,systeminfo"),
		APIToken:            getEnv("API_TOKEN", get("security.apiToken", "")),
		AppName:             get("app.name", "Ejecutor SSH v2"),
		AppVersion:          get("app.version", "2.0.0"),
		configPath:          filePath,
	}
	cfg.VBoxSeedURL = get("vbox.seedURL", "http://10.0.2.2:"+cfg.WebPort)
	cfg.VBoxAdditionsISO = get("vbox.additionsISO", filepath.Join(filepath.Dir(cfg.VBoxManagePath), "VBoxGuestAdditions.iso"))

	return cfg, nil
}

// leerArchivo parsea el archivo config.properties.
func leerArchivo(filePath string) error {
	archivo, err := os.Open(filePath)
	if err != nil {
		return err
	}
	defer archivo.Close()

	scanner := bufio.NewScanner(archivo)
	for scanner.Scan() {
		linea := strings.TrimSpace(scanner.Text())

		// Ignora comentarios y lineas vacias.
		if linea == "" || strings.HasPrefix(linea, "#") {
			continue
		}

		// Parsea el par clave=valor.
		partes := strings.SplitN(linea, "=", 2)
		if len(partes) == 2 {
			clave := strings.TrimSpace(partes[0])
			valor := strings.TrimSpace(partes[1])
			propiedades[clave] = valor
		}
	}

	return scanner.Err()
}

// get retorna el valor de una propiedad o el valor por defecto.
func get(clave, defecto string) string {
	if valor, existe := propiedades[clave]; existe {
		return valor
	}
	return defecto
}

// getEnv retorna el valor de una variable de entorno o la propiedad por defecto.
func getEnv(varEnv, defecto string) string {
	if valor := os.Getenv(varEnv); valor != "" {
		return valor
	}
	return defecto
}

// getInt retorna una propiedad como entero.
func getInt(clave string, defecto int) int {
	valor := get(clave, "")
	if valor == "" {
		return defecto
	}
	var resultado int
	_, _ = fmt.Sscanf(valor, "%d", &resultado)
	return resultado
}

// getCommands retorna la lista de comandos permitidos.
func getCommands(clave, defecto string) []string {
	valor := get(clave, defecto)
	comandos := strings.Split(valor, ",")
	for i, cmd := range comandos {
		comandos[i] = strings.TrimSpace(strings.ToLower(cmd))
	}
	return comandos
}

// IsCommandAllowed verifica si un comando esta en la lista permitida.
func (cfg *Config) IsCommandAllowed(comando string) bool {
	cmd := strings.ToLower(strings.Fields(comando)[0])
	for _, permitido := range cfg.AllowedCommands {
		if cmd == permitido {
			return true
		}
	}
	return false
}

// ToJSON serializa la configuracion a JSON para las APIs REST.
func (cfg *Config) ToJSON() ([]byte, error) {
	cfg.mu.RLock()
	defer cfg.mu.RUnlock()

	// Convertimos la configuracion a un mapa JSON.
	data := map[string]interface{}{
		"web.addr":                 cfg.WebAddr,
		"web.port":                 cfg.WebPort,
		"ssh.host":                 cfg.SSHHost,
		"ssh.port":                 cfg.SSHPort,
		"ssh.user":                 cfg.SSHUser,
		"ssh.keyPath":              cfg.SSHKeyPath,
		"ssh.knownHostsPath":       cfg.SSHKnownHostsPath,
		"shell.type":               cfg.ShellType,
		"shell.startingPath":       cfg.ShellStartPath,
		"execution.mode":           cfg.ExecutionMode,
		"keys.storePath":           cfg.KeysStorePath,
		"users.storePath":          cfg.UsersStorePath,
		"keys.algorithm":           cfg.KeysAlgorithm,
		"ssh.timeout":              cfg.SSHTimeout,
		"request.maxSize":          cfg.RequestMaxSize,
		"commands.allowed":         strings.Join(cfg.AllowedCommands, ","),
		"vm.provider":              cfg.VMProvider,
		"vbox.guest.user":          cfg.VBoxGuestUser,
		"vbox.templates":           cfg.VBoxTemplatesDir,
		"vm.ssh.host":              cfg.VMSSHHost,
		"vm.ssh.port":              cfg.VMSSHPort,
		"vm.ssh.user":              cfg.VMSSHUser,
		"vm.ssh.keyPath":           cfg.VMSSHKeyPath,
		"vm.ssh.knownHostsPath":    cfg.VMSSHKnownHostsPath,
		"vm.network.revertSeconds": cfg.VMNetRevertSeconds,
		"app.name":                 cfg.AppName,
		"app.version":              cfg.AppVersion,
	}

	return json.Marshal(data)
}

// UpdateFromJSON actualiza la configuracion desde un mapa JSON y guarda los cambios.
func (cfg *Config) UpdateFromJSON(data map[string]interface{}) error {
	cfg.mu.Lock()
	defer cfg.mu.Unlock()

	// Actualizamos los campos segun los valores del mapa.
	for key, raw := range data {
		// Los formularios envian texto, pero un cliente de la API puede mandar
		// numeros (ej. puertos); ambos se aceptan como texto.
		value := raw
		if n, ok := raw.(float64); ok {
			value = fmt.Sprint(int(n))
		}
		switch key {
		case "web.addr":
			if v, ok := value.(string); ok {
				cfg.WebAddr = v
				propiedades["web.addr"] = v
			}
		case "web.port":
			if v, ok := value.(string); ok {
				cfg.WebPort = v
				propiedades["web.port"] = v
			}
		case "ssh.host":
			if v, ok := value.(string); ok {
				cfg.SSHHost = v
				propiedades["ssh.host"] = v
			}
		case "ssh.port":
			if v, ok := value.(string); ok {
				cfg.SSHPort = v
				propiedades["ssh.port"] = v
			}
		case "ssh.user":
			if v, ok := value.(string); ok {
				cfg.SSHUser = v
				propiedades["ssh.user"] = v
			}
		case "ssh.keyPath":
			if v, ok := value.(string); ok {
				cfg.SSHKeyPath = v
				propiedades["ssh.keyPath"] = v
			}
		case "ssh.knownHostsPath":
			if v, ok := value.(string); ok {
				cfg.SSHKnownHostsPath = v
				propiedades["ssh.knownHostsPath"] = v
			}
		case "shell.type":
			if v, ok := value.(string); ok {
				cfg.ShellType = v
				propiedades["shell.type"] = v
			}
		case "shell.startingPath":
			if v, ok := value.(string); ok {
				cfg.ShellStartPath = v
				propiedades["shell.startingPath"] = v
			}
		case "execution.mode":
			if v, ok := value.(string); ok {
				cfg.ExecutionMode = strings.ToLower(v)
				propiedades["execution.mode"] = cfg.ExecutionMode
			}
		case "keys.storePath":
			if v, ok := value.(string); ok {
				cfg.KeysStorePath = v
				propiedades["keys.storePath"] = v
			}
		case "users.storePath":
			if v, ok := value.(string); ok {
				cfg.UsersStorePath = v
				propiedades["users.storePath"] = v
			}
		case "keys.algorithm":
			if v, ok := value.(string); ok {
				cfg.KeysAlgorithm = v
				propiedades["keys.algorithm"] = v
			}
		case "vm.ssh.host":
			if v, ok := value.(string); ok {
				cfg.VMSSHHost = strings.TrimSpace(v)
				propiedades["vm.ssh.host"] = cfg.VMSSHHost
			}
		case "vm.ssh.port":
			if v, ok := value.(string); ok {
				cfg.VMSSHPort = strings.TrimSpace(v)
				propiedades["vm.ssh.port"] = cfg.VMSSHPort
			}
		case "vm.ssh.user":
			if v, ok := value.(string); ok {
				cfg.VMSSHUser = strings.TrimSpace(v)
				propiedades["vm.ssh.user"] = cfg.VMSSHUser
			}
		case "vm.ssh.keyPath":
			if v, ok := value.(string); ok {
				cfg.VMSSHKeyPath = strings.TrimSpace(v)
				propiedades["vm.ssh.keyPath"] = cfg.VMSSHKeyPath
			}
		case "vm.ssh.knownHostsPath":
			if v, ok := value.(string); ok {
				cfg.VMSSHKnownHostsPath = strings.TrimSpace(v)
				propiedades["vm.ssh.knownHostsPath"] = cfg.VMSSHKnownHostsPath
			}
		case "vm.network.revertSeconds":
			var n int
			if v, ok := value.(string); ok {
				_, _ = fmt.Sscanf(strings.TrimSpace(v), "%d", &n)
			}
			if n > 0 {
				cfg.VMNetRevertSeconds = clampRevert(n)
				propiedades["vm.network.revertSeconds"] = fmt.Sprint(cfg.VMNetRevertSeconds)
			}
		case "commands.allowed":
			if v, ok := value.(string); ok {
				cfg.AllowedCommands = getCommands("", v)
				propiedades["commands.allowed"] = v
			}
		}
	}

	// Guarda los cambios en el archivo.
	return cfg.saveToFile()
}

// saveToFile guarda la configuracion actualizada en el archivo.
func (cfg *Config) saveToFile() error {
	if cfg.configPath == "" {
		return fmt.Errorf("ruta de configuracion no establecida")
	}

	archivo, err := os.Create(cfg.configPath)
	if err != nil {
		return fmt.Errorf("crear archivo config: %w", err)
	}
	defer archivo.Close()

	writer := bufio.NewWriter(archivo)

	// Escribimos los comentarios y propiedades organizadas.
	secciones := []struct {
		titulo string
		claves []string
	}{
		{
			titulo: "Configuracion del Servidor Web",
			claves: []string{"web.addr", "web.port"},
		},
		{
			titulo: "Configuracion SSH por Defecto",
			claves: []string{"ssh.host", "ssh.port", "ssh.user", "ssh.keyPath", "ssh.knownHostsPath"},
		},
		{
			titulo: "Configuracion SSH del Hipervisor",
			claves: []string{"vm.ssh.host", "vm.ssh.port", "vm.ssh.user", "vm.ssh.keyPath", "vm.ssh.knownHostsPath", "vm.network.revertSeconds"},
		},
		{
			titulo: "Hipervisor: virtualbox (VBoxManage local) o libvirt (SSH, diseno anterior)",
			claves: []string{"vm.provider", "vbox.manage", "vbox.guest.user", "vbox.guest.password", "vbox.templates", "vbox.sharedDisks", "vbox.seeds", "vbox.seedURL", "vbox.additionsISO"},
		},
		{
			titulo: "Configuracion de Shell Remoto",
			claves: []string{"shell.type", "shell.startingPath", "execution.mode"},
		},
		{
			titulo: "Configuracion de Usuarios y Llaves",
			claves: []string{"keys.storePath", "users.storePath", "keys.algorithm"},
		},
		{
			titulo: "Configuracion de Seguridad",
			claves: []string{"ssh.timeout", "request.maxSize", "commands.allowed", "security.apiToken"},
		},
		{
			titulo: "Metadata",
			claves: []string{"app.name", "app.version"},
		},
	}

	for _, seccion := range secciones {
		fmt.Fprintf(writer, "\n# ============================================\n")
		fmt.Fprintf(writer, "# %s\n", seccion.titulo)
		fmt.Fprintf(writer, "# ============================================\n\n")

		for _, clave := range seccion.claves {
			if valor, existe := propiedades[clave]; existe {
				fmt.Fprintf(writer, "%s=%s\n", clave, valor)
			}
		}
	}

	return writer.Flush()
}

// HypervisorSSH devuelve los datos de conexion al hipervisor en este momento.
// Se lee en cada operacion para que un cambio desde Configuracion se aplique
// sin reiniciar la aplicacion.
func (cfg *Config) HypervisorSSH() (host, port, user, keyPath, knownHostsPath string) {
	cfg.mu.RLock()
	defer cfg.mu.RUnlock()
	return cfg.VMSSHHost, cfg.VMSSHPort, cfg.VMSSHUser, cfg.VMSSHKeyPath, cfg.VMSSHKnownHostsPath
}

// clampRevert mantiene el tiempo de reversion entre 30 s (para que alcance a
// aplicar y verificar, sobre todo con DHCP) y 600 s (para no dejar una VM
// mal configurada inaccesible demasiado tiempo).
func clampRevert(seconds int) int {
	switch {
	case seconds <= 0:
		return 120
	case seconds < 30:
		return 30
	case seconds > 600:
		return 600
	}
	return seconds
}
