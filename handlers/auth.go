package handlers

import (
	"crypto/subtle"
	"fmt"
	"net/http"

	"ejecutor-ssh/config"
)

// apiKeyHeader es el header usado para enviar el token de API en rutas protegidas.
const apiKeyHeader = "X-API-Key"

// requireAPIToken protege rutas sensibles con un token compartido configurado
// en security.apiToken (o la variable de entorno API_TOKEN). Si no hay token
// configurado, la ruta queda bloqueada por defecto (falla segura) en vez de abierta.
func requireAPIToken(cfg *config.Config, r *http.Request) error {
	if cfg == nil || cfg.APIToken == "" {
		return fmt.Errorf("ruta deshabilitada: configura security.apiToken en config.properties para habilitarla")
	}

	provided := r.Header.Get(apiKeyHeader)
	if provided == "" {
		return fmt.Errorf("falta el header %s", apiKeyHeader)
	}

	// Comparacion en tiempo constante para no filtrar el token por temporizacion.
	if subtle.ConstantTimeCompare([]byte(provided), []byte(cfg.APIToken)) != 1 {
		return fmt.Errorf("token de API inválido")
	}

	return nil
}
