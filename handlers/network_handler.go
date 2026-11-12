package handlers

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"sync"
	"time"

	vm "ejecutor-ssh/mv"
)

// netCacheTTL es cuanto sirve una lectura de la red para calcular vistas
// previas. Aplicar SIEMPRE vuelve a leer la VM: la cache solo evita llamar al
// agente en cada tecla del formulario.
const netCacheTTL = 10 * time.Minute

type netCache struct {
	mu     sync.Mutex
	states map[string]*vm.NetState
}

func (c *netCache) get(name string) *vm.NetState {
	c.mu.Lock()
	defer c.mu.Unlock()
	st := c.states[name]
	if st == nil || time.Since(st.ReadAt) > netCacheTTL {
		return nil
	}
	return st
}

func (c *netCache) put(name string, st *vm.NetState) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.states == nil {
		c.states = map[string]*vm.NetState{}
	}
	c.states[name] = st
}

type networkReadResponse struct {
	State         *vm.NetState `json:"state"`
	RevertSeconds int          `json:"revertSeconds"`
}

// networkRoute reconoce /api/vms/{nombre}/network[/plan|/apply|/forwarding|/test].
func networkRoute(path string) (name, action string, ok bool) {
	rest := strings.TrimPrefix(path, "/api/vms/")
	if rest == path {
		return "", "", false
	}
	for _, a := range []string{"/network/plan", "/network/apply", "/network/forwarding", "/network/test", "/network"} {
		if strings.HasSuffix(rest, a) {
			return strings.TrimSuffix(rest, a), strings.TrimPrefix(a, "/network"), true
		}
	}
	return "", "", false
}

func (h *VMHandler) serveNetwork(w http.ResponseWriter, r *http.Request, name, action string) {
	if err := vm.ValidateName(name); err != nil {
		writeVMResponse(w, http.StatusBadRequest, vmResponse{Success: false, Error: err.Error()})
		return
	}
	// canal=gestion usa SSH por la interfaz de gestion (canal 3); si no, el
	// canal del hipervisor (Guest Additions o agente QEMU, canal 2).
	channel := r.URL.Query().Get("canal")
	np, err := h.networkOps(channel)
	if err != nil {
		writeVMResponse(w, http.StatusNotImplemented, vmResponse{Success: false, Error: err.Error()})
		return
	}
	cacheKey := name + "|" + channel
	switch {
	case action == "" && r.Method == http.MethodGet:
		st, err := np.ReadNetwork(name)
		if err != nil {
			writeVMResponse(w, http.StatusBadGateway, vmResponse{Success: false, Error: err.Error()})
			return
		}
		h.net.put(cacheKey, st)
		writeVMResponse(w, http.StatusOK, vmResponse{Success: true, Data: networkReadResponse{State: st, RevertSeconds: h.config.VMNetRevertSeconds}})

	case action == "/plan" && r.Method == http.MethodPost:
		req, ok := decodeNetRequest(w, r)
		if !ok {
			return
		}
		st := h.net.get(cacheKey)
		if st == nil {
			writeVMResponse(w, http.StatusConflict, vmResponse{Success: false, Error: "la lectura de la red venció: vuelva a abrir el panel de red"})
			return
		}
		writeVMResponse(w, http.StatusOK, vmResponse{Success: true, Data: vm.PlanNetwork(st, req, h.config.VMNetRevertSeconds)})

	case action == "/apply" && r.Method == http.MethodPost:
		req, ok := decodeNetRequest(w, r)
		if !ok {
			return
		}
		result, err := np.ApplyNetwork(name, req, h.config.VMNetRevertSeconds)
		var planErr *vm.PlanError
		if errors.As(err, &planErr) {
			writeVMResponse(w, http.StatusConflict, vmResponse{Success: false, Error: err.Error(), Data: planErr.Plan})
			return
		}
		if err != nil {
			writeVMResponse(w, http.StatusBadGateway, vmResponse{Success: false, Error: err.Error()})
			return
		}
		if result.State != nil {
			h.net.put(cacheKey, result.State)
		}
		writeVMResponse(w, http.StatusOK, vmResponse{Success: result.OK, Message: result.Message, Data: result})

	case action == "/test" && r.Method == http.MethodPost:
		var req struct {
			Target string `json:"target"`
		}
		r.Body = http.MaxBytesReader(w, r.Body, 512)
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeVMResponse(w, http.StatusBadRequest, vmResponse{Success: false, Error: "JSON inválido"})
			return
		}
		result, err := np.TestConnectivity(name, req.Target)
		if err != nil {
			writeVMResponse(w, http.StatusBadRequest, vmResponse{Success: false, Error: err.Error()})
			return
		}
		writeVMResponse(w, http.StatusOK, vmResponse{Success: result.OK, Message: result.Message, Data: result})

	case action == "/forwarding" && r.Method == http.MethodPost:
		var req struct {
			Enabled bool `json:"enabled"`
		}
		r.Body = http.MaxBytesReader(w, r.Body, 256)
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeVMResponse(w, http.StatusBadRequest, vmResponse{Success: false, Error: "JSON inválido"})
			return
		}
		result, err := np.SetForwarding(name, req.Enabled)
		if err != nil {
			writeVMResponse(w, http.StatusBadGateway, vmResponse{Success: false, Error: err.Error()})
			return
		}
		if result.State != nil {
			h.net.put(cacheKey, result.State)
		}
		writeVMResponse(w, http.StatusOK, vmResponse{Success: result.OK, Message: result.Message, Data: result})

	default:
		writeVMResponse(w, http.StatusMethodNotAllowed, vmResponse{Success: false, Error: "método no permitido"})
	}
}

func decodeNetRequest(w http.ResponseWriter, r *http.Request) (vm.NetRequest, bool) {
	r.Body = http.MaxBytesReader(w, r.Body, 4096)
	var req vm.NetRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeVMResponse(w, http.StatusBadRequest, vmResponse{Success: false, Error: "JSON inválido"})
		return req, false
	}
	return req, true
}

// managementProvider lo implementan los proveedores con canal de gestion.
type managementProvider interface {
	Management() vm.NetworkOps
}

// networkOps elige el canal con el que se lee y configura la red.
func (h *VMHandler) networkOps(channel string) (vm.NetworkOps, error) {
	if channel == "gestion" {
		mp, ok := h.provider.(managementProvider)
		if !ok {
			return nil, errors.New("el proveedor no tiene canal por interfaz de gestión")
		}
		return mp.Management(), nil
	}
	ops, ok := h.provider.(vm.NetworkOps)
	if !ok {
		return nil, errors.New("el proveedor de VMs no permite gestionar la red")
	}
	return ops, nil
}
