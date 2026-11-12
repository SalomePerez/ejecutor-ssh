package handlers

import (
	"encoding/json"
	"net/http"

	"ejecutor-ssh/config"
)

// ConfigHandler maneja las solicitudes a la API de configuracion.
type ConfigHandler struct {
	config *config.Config
}

// NewConfigHandler crea un nuevo handler para la API de configuracion.
func NewConfigHandler(cfg *config.Config) *ConfigHandler {
	return &ConfigHandler{config: cfg}
}

// ServeHTTP implementa http.Handler para gestionar rutas de configuracion.
func (h *ConfigHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	// Solo maneja rutas exactas /api/config
	if r.URL.Path != "/api/config" {
		http.NotFound(w, r)
		return
	}

	switch r.Method {
	case http.MethodGet:
		h.handleGetConfig(w, r)
	case http.MethodPut:
		h.handleUpdateConfig(w, r)
	default:
		http.Error(w, "metodo no permitido", http.StatusMethodNotAllowed)
	}
}

// handleGetConfig maneja GET /api/config - devuelve todas las configuraciones.
func (h *ConfigHandler) handleGetConfig(w http.ResponseWriter, r *http.Request) {
	// Serializa la configuracion a JSON.
	jsonData, err := h.config.ToJSON()
	if err != nil {
		http.Error(w, "error serializando config", http.StatusInternalServerError)
		return
	}

	w.WriteHeader(http.StatusOK)
	w.Write(jsonData)
}

// handleUpdateConfig maneja PUT /api/config - actualiza la configuracion.
func (h *ConfigHandler) handleUpdateConfig(w http.ResponseWriter, r *http.Request) {
	// Limita el tamano de la solicitud.
	r.Body = http.MaxBytesReader(w, r.Body, 4096)

	// Decodifica el JSON del cuerpo.
	var updateData map[string]interface{}
	if err := json.NewDecoder(r.Body).Decode(&updateData); err != nil {
		http.Error(w, "JSON invalido", http.StatusBadRequest)
		return
	}

	// Actualiza la configuracion y guarda los cambios.
	if err := h.config.UpdateFromJSON(updateData); err != nil {
		http.Error(w, "error actualizando config: "+err.Error(), http.StatusInternalServerError)
		return
	}

	// Devuelve la configuracion actualizada.
	jsonData, _ := h.config.ToJSON()
	w.WriteHeader(http.StatusOK)
	w.Write(jsonData)
}
