package main

import (
	"fmt"
	"html/template"
	"log"
	"net/http"
	"os"
	"path/filepath"

	"ejecutor-ssh/config"
	"ejecutor-ssh/handlers"
	vm "ejecutor-ssh/mv"
	"ejecutor-ssh/sshclient"
)

func main() {
	// Carga la configuracion desde config.properties y variables de entorno.
	cfg, err := config.Load("config.properties")
	if err != nil {
		log.Fatalf("cargar configuracion: %v", err)
	}

	// Carga la plantilla HTML una sola vez al iniciar la aplicacion.
	// Must detiene el proceso si la plantilla no existe o tiene errores.
	page := template.Must(template.ParseFiles(filepath.Join("templates", "index.html")))
	commandHandler := handlers.NewCommandHandler(handlers.ConfiguredSSHExecutor{Config: cfg}, cfg)
	// Las VMs siempre se administran en el hipervisor Linux por SSH,
	// independientemente del modo usado por el modulo de comandos.
	// La conexion se arma en cada comando para tomar los cambios hechos en
	// la pestana Configuracion sin reiniciar.
	var vmProvider vm.Provider
	if cfg.VMProvider == "libvirt" {
		vmProvider = vm.NewLibvirtProvider(hypervisorRunner{cfg: cfg})
	} else {
		// VirtualBox es el hipervisor real, en este mismo equipo.
		vmProvider = vm.NewVBoxProvider(vm.VBoxManageRunner{Path: cfg.VBoxManagePath}, func(string) (vm.GuestCredentials, error) {
			if cfg.VBoxGuestUser == "" {
				return vm.GuestCredentials{}, fmt.Errorf("falta vbox.guest.user en config.properties")
			}
			return vm.GuestCredentials{User: cfg.VBoxGuestUser, Password: cfg.VBoxGuestPassword}, nil
		})
	}
	log.Printf("hipervisor: %s", cfg.VMProvider)

	// Sirve la pagina principal y evita que este handler responda a otras rutas.
	http.HandleFunc("/", func(response http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/" {
			http.NotFound(response, request)
			return
		}
		if err := page.Execute(response, nil); err != nil {
			log.Printf("rendering index: %v", err)
		}
	})
	// Publica los archivos CSS bajo /static/ sin exponer la ruta fisica del proyecto.
	http.Handle("/static/", http.StripPrefix("/static/", http.FileServer(http.Dir("static"))))
	// El handler contiene la validacion y la ejecucion de comandos remotos.
	http.Handle("/execute", commandHandler)
	// Router centralizado para todas las rutas /api
	apiRouter := handlers.NewAPIRouter(cfg, vmProvider)
	http.Handle("/api/", apiRouter)

	// Usa la configuracion para determinar donde escuchar.
	log.Printf("servidor web %s escuchando en http://localhost%s", cfg.AppVersion, cfg.WebAddr)
	log.Fatal(http.ListenAndServe(cfg.WebAddr, nil))
}

// hypervisorRunner ejecuta comandos en el hipervisor con la configuracion vigente.
type hypervisorRunner struct{ cfg *config.Config }

func (runner hypervisorRunner) ExecuteCommand(command string) ([]byte, error) {
	host, port, user, keyPath, knownHosts := runner.cfg.HypervisorSSH()
	return sshclient.NewClient(sshclient.Config{
		Host:           host,
		Port:           port,
		User:           user,
		KeyPath:        keyPath,
		KnownHostsPath: knownHosts,
	}).ExecuteCommand(command)
}

func envOrDefault(name string, fallback string) string {
	// Las variables vacias se consideran ausentes y usan el valor por defecto.
	if value := os.Getenv(name); value != "" {
		return value
	}
	return fallback
}
