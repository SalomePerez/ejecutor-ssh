package handlers

import (
	"log"
	"net"
	"net/http"
	"strings"
)

// SeedSource entrega los archivos de cloud-init de una VM (ver
// vm.VBoxProvider.SeedFile).
type SeedSource interface {
	SeedFile(name, file string) ([]byte, error)
}

// CloudInitHandler sirve GET /cloud-init/{vm}/{meta-data|user-data|vendor-data}
// a las VMs de VirtualBox en su primer arranque (datasource NoCloud por
// HTTP). Desde la tarjeta NAT de la VM, 10.0.2.2 es este equipo y la
// conexion llega desde 127.0.0.1: por eso solo se responde a conexiones
// locales. user-data lleva la contrasena del usuario de la app y no debe
// salir del equipo.
type CloudInitHandler struct{ Source SeedSource }

func (h CloudInitHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if ip := net.ParseIP(host); err != nil || ip == nil || !ip.IsLoopback() {
		http.Error(w, "solo para las VMs de este equipo", http.StatusForbidden)
		return
	}
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		http.Error(w, "método no permitido", http.StatusMethodNotAllowed)
		return
	}
	parts := strings.Split(strings.Trim(strings.TrimPrefix(r.URL.Path, "/cloud-init/"), "/"), "/")
	if len(parts) != 2 {
		http.NotFound(w, r)
		return
	}
	data, err := h.Source.SeedFile(parts[0], parts[1])
	if err != nil {
		http.NotFound(w, r)
		return
	}
	log.Printf("cloud-init: %s pidió %s", parts[0], parts[1])
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	_, _ = w.Write(data)
}
