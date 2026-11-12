package handlers

import (
	"net/http"
	"strings"

	"ejecutor-ssh/config"
	vm "ejecutor-ssh/mv"
)

// APIRouter enruta todas las solicitudes /api/* a los handlers correctos.
type APIRouter struct {
	keysHandler   *KeysHandler
	usersHandler  *UsersHandler
	configHandler *ConfigHandler
	vmHandler     *VMHandler
}

// NewAPIRouter crea un nuevo enrutador para la API.
func NewAPIRouter(cfg *config.Config, provider vm.Provider) *APIRouter {
	return &APIRouter{
		keysHandler:   NewKeysHandler(cfg),
		usersHandler:  NewUsersHandler(cfg),
		configHandler: NewConfigHandler(cfg),
		vmHandler:     NewVMHandler(cfg, provider),
	}
}

// ServeHTTP implementa http.Handler para enrutar todas las solicitudes /api.
func (router *APIRouter) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	path := r.URL.Path

	// Enruta según el prefijo de la ruta.
	if strings.HasPrefix(path, "/api/config") {
		router.configHandler.ServeHTTP(w, r)
	} else if strings.HasPrefix(path, "/api/keys") {
		router.keysHandler.ServeHTTP(w, r)
	} else if strings.HasPrefix(path, "/api/users") {
		router.usersHandler.ServeHTTP(w, r)
	} else if strings.HasPrefix(path, "/api/vms") || strings.HasPrefix(path, "/api/networks") || path == "/api/templates" || path == "/api/hypervisor" {
		router.vmHandler.ServeHTTP(w, r)
	} else {
		http.NotFound(w, r)
	}
}
