package handlers

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"ejecutor-ssh/config"
	vm "ejecutor-ssh/mv"
)

// fakeNetProvider agrega la gestion de red al proveedor falso de VMs.
type fakeNetProvider struct {
	fakeVMProvider
	state   *vm.NetState
	applied *vm.NetRequest
	revert  int
}

func (f *fakeNetProvider) ReadNetwork(name string) (*vm.NetState, error) {
	st := *f.state
	st.VM, st.ReadAt = name, time.Now()
	return &st, nil
}

func (f *fakeNetProvider) ApplyNetwork(name string, req vm.NetRequest, revertSeconds int) (*vm.ApplyResult, error) {
	f.applied, f.revert = &req, revertSeconds
	return &vm.ApplyResult{OK: true, Message: "Aplicado y verificado."}, nil
}

func newNetHandler() (*VMHandler, *fakeNetProvider) {
	p := &fakeNetProvider{state: &vm.NetState{Manager: "netplan", Interfaces: []vm.NetIface{
		{Name: "enp1s0", MAC: "52:54:00:07:00:11", Type: "eth", IP: "192.168.122.45", Prefix: 24, Mode: "dhcp", Role: "main", Gateway: "192.168.122.1", Metric: 100, DNS: "192.168.122.1", Extras: []string{}, Nets: []string{}},
	}}}
	return NewVMHandler(&config.Config{VMNetRevertSeconds: 90}, p), p
}

func doJSON(h http.Handler, method, path string, body interface{}) (*httptest.ResponseRecorder, vmResponse) {
	var buf bytes.Buffer
	if body != nil {
		_ = json.NewEncoder(&buf).Encode(body)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(method, path, &buf))
	var resp vmResponse
	_ = json.Unmarshal(rec.Body.Bytes(), &resp)
	return rec, resp
}

func TestNetworkRoute(t *testing.T) {
	cases := map[string][2]string{
		"/api/vms/lab-01/network":            {"lab-01", ""},
		"/api/vms/lab-01/network/plan":       {"lab-01", "/plan"},
		"/api/vms/lab-01/network/apply":      {"lab-01", "/apply"},
		"/api/vms/lab-01/network/forwarding": {"lab-01", "/forwarding"},
		"/api/vms/lab-01/network/test":       {"lab-01", "/test"},
	}
	for path, want := range cases {
		name, action, ok := networkRoute(path)
		if !ok || name != want[0] || action != want[1] {
			t.Errorf("%s: %q %q %v", path, name, action, ok)
		}
	}
	if _, _, ok := networkRoute("/api/vms/lab-01/disks"); ok {
		t.Errorf("/disks no es una ruta de red")
	}
}

func TestNetworkHandler_ReadThenPlan(t *testing.T) {
	h, _ := newNetHandler()
	req := vm.NetRequest{Interface: "enp1s0", Mode: "static", IP: "192.168.122.50", Mask: "/24", Gateway: "192.168.122.1", Role: "main", DNS: "192.168.122.1"}

	if rec, _ := doJSON(h, http.MethodPost, "/api/vms/lab-01/network/plan", req); rec.Code != http.StatusConflict {
		t.Fatalf("sin lectura previa la vista previa debe pedir leer de nuevo, fue %d", rec.Code)
	}
	rec, resp := doJSON(h, http.MethodGet, "/api/vms/lab-01/network", nil)
	if rec.Code != http.StatusOK || !resp.Success {
		t.Fatalf("leer la red: %d %+v", rec.Code, resp)
	}
	rec, resp = doJSON(h, http.MethodPost, "/api/vms/lab-01/network/plan", req)
	if rec.Code != http.StatusOK {
		t.Fatalf("plan: %d %+v", rec.Code, resp)
	}
	data, _ := json.Marshal(resp.Data)
	var plan vm.NetPlan
	_ = json.Unmarshal(data, &plan)
	if !plan.Valid || plan.RevertSeconds != 90 || len(plan.Files) == 0 {
		t.Fatalf("plan inesperado: %+v", plan)
	}
}

func TestNetworkHandler_ApplyUsesConfiguredRevert(t *testing.T) {
	h, p := newNetHandler()
	rec, resp := doJSON(h, http.MethodPost, "/api/vms/lab-01/network/apply", vm.NetRequest{Interface: "enp1s0", Mode: "dhcp"})
	if rec.Code != http.StatusOK || !resp.Success || p.applied == nil || p.revert != 90 {
		t.Fatalf("apply: %d %+v revert=%d", rec.Code, resp, p.revert)
	}
}

func TestNetworkHandler_RejectsBadName(t *testing.T) {
	h, _ := newNetHandler()
	if rec, _ := doJSON(h, http.MethodGet, "/api/vms/la;b/network", nil); rec.Code != http.StatusBadRequest {
		t.Fatalf("nombre inválido debe rechazarse, fue %d", rec.Code)
	}
}
