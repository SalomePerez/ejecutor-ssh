package handlers

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"ejecutor-ssh/config"
	vm "ejecutor-ssh/mv"
)

// fakeVMProvider implementa vm.Provider en memoria para no depender de un
// hipervisor real al probar VMHandler.
type fakeVMProvider struct {
	vms          []vm.VM
	listErr      error
	templates    []vm.Template
	templatesErr error
	createErr    error
	created      *vm.VM
	startErr     error
	stopErr      error
	deleteErr    error
	renameErr    error
	updateErr    error
	disks        map[string][]vm.Disk
	listDisksErr error
	lastCreate   vm.CreateSpec
	started      []string
	stopped      []string
	deleted      []string
	renamed      [][2]string
	updated      []vm.UpdateSpec
}

func (f *fakeVMProvider) Create(spec vm.CreateSpec) (*vm.VM, error) {
	f.lastCreate = spec
	if f.createErr != nil {
		return nil, f.createErr
	}
	if f.created != nil {
		return f.created, nil
	}
	return &vm.VM{Name: spec.Name, Distro: spec.Distro, CPU: spec.CPU, MemoryMB: spec.MemoryMB, DiskGB: spec.DiskGB, SSHUser: spec.SSHUser, Status: vm.VMStatusStopped}, nil
}

func (f *fakeVMProvider) ListTemplates() ([]vm.Template, error) { return f.templates, f.templatesErr }
func (f *fakeVMProvider) List() ([]vm.VM, error)                { return f.vms, f.listErr }

func (f *fakeVMProvider) ListDisks(name string) ([]vm.Disk, error) {
	if f.listDisksErr != nil {
		return nil, f.listDisksErr
	}
	return f.disks[name], nil
}

func (f *fakeVMProvider) Start(name string) error {
	f.started = append(f.started, name)
	return f.startErr
}

func (f *fakeVMProvider) Stop(name string) error {
	f.stopped = append(f.stopped, name)
	return f.stopErr
}

func (f *fakeVMProvider) Delete(name string) error {
	f.deleted = append(f.deleted, name)
	return f.deleteErr
}

func (f *fakeVMProvider) Rename(oldName, newName string) error {
	f.renamed = append(f.renamed, [2]string{oldName, newName})
	return f.renameErr
}

func (f *fakeVMProvider) Update(name string, spec vm.UpdateSpec) error {
	f.updated = append(f.updated, spec)
	return f.updateErr
}

func (f *fakeVMProvider) Get(name string) (*vm.VM, error) {
	for i := range f.vms {
		if f.vms[i].Name == name {
			return &f.vms[i], nil
		}
	}
	return nil, nil
}

func newTestVMHandler(t *testing.T, provider *fakeVMProvider) *VMHandler {
	t.Helper()
	cfg := &config.Config{KeysStorePath: t.TempDir()}
	return NewVMHandler(cfg, provider)
}

func decodeVMResponse(t *testing.T, body *bytes.Buffer) vmResponse {
	t.Helper()
	var resp vmResponse
	if err := json.Unmarshal(body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v (body=%s)", err, body.String())
	}
	return resp
}

func TestVMHandler_ListVMs(t *testing.T) {
	provider := &fakeVMProvider{vms: []vm.VM{{Name: "vm1", Status: vm.VMStatusRunning}}}
	handler := newTestVMHandler(t, provider)

	req := httptest.NewRequest(http.MethodGet, "/api/vms", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	resp := decodeVMResponse(t, rec.Body)
	if !resp.Success {
		t.Fatalf("expected success response, got %+v", resp)
	}
}

func TestVMHandler_ListVMs_ProviderError(t *testing.T) {
	provider := &fakeVMProvider{listErr: errors.New("virsh unavailable")}
	handler := newTestVMHandler(t, provider)

	req := httptest.NewRequest(http.MethodGet, "/api/vms", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("expected 500 when the provider fails, got %d", rec.Code)
	}
	resp := decodeVMResponse(t, rec.Body)
	if resp.Success {
		t.Fatal("expected success=false on provider error")
	}
}

func TestVMHandler_ListTemplates_ProviderError(t *testing.T) {
	provider := &fakeVMProvider{templatesErr: errors.New("cannot reach hypervisor")}
	handler := newTestVMHandler(t, provider)

	req := httptest.NewRequest(http.MethodGet, "/api/templates", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadGateway {
		t.Fatalf("expected 502 when templates cannot be listed, got %d", rec.Code)
	}
}

func TestVMHandler_CreateVM_Success(t *testing.T) {
	provider := &fakeVMProvider{}
	handler := newTestVMHandler(t, provider)

	body := `{"name":"vm-test","distro":"ubuntu-24.04","cpu":2,"memoryMB":2048,"diskGB":20,"sshUser":"ubuntu"}`
	req := httptest.NewRequest(http.MethodPost, "/api/vms", bytes.NewBufferString(body))
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d: %s", rec.Code, rec.Body.String())
	}
	if provider.lastCreate.Name != "vm-test" {
		t.Fatalf("expected provider.Create to be called with name vm-test, got %+v", provider.lastCreate)
	}
	if provider.lastCreate.SSHPublicKey == "" {
		t.Fatal("expected a generated ssh public key to be passed to the provider")
	}
}

func TestVMHandler_CreateVM_ShareableDiskWarning(t *testing.T) {
	provider := &fakeVMProvider{}
	handler := newTestVMHandler(t, provider)

	body := `{"name":"vm-test","distro":"ubuntu-24.04","cpu":1,"memoryMB":1024,"diskGB":10,"disks":[{"name":"cluster-data","sizeGB":5,"shareable":true}]}`
	req := httptest.NewRequest(http.MethodPost, "/api/vms", bytes.NewBufferString(body))
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d: %s", rec.Code, rec.Body.String())
	}
	resp := decodeVMResponse(t, rec.Body)
	if resp.Warning == "" {
		t.Fatal("expected a warning when creating a vm with a shareable disk")
	}
}

func TestVMHandler_CreateVM_NoWarningWithoutSharedDisks(t *testing.T) {
	provider := &fakeVMProvider{}
	handler := newTestVMHandler(t, provider)

	body := `{"name":"vm-test","distro":"ubuntu-24.04","cpu":1,"memoryMB":1024,"diskGB":10,"disks":[{"name":"data","sizeGB":5}]}`
	req := httptest.NewRequest(http.MethodPost, "/api/vms", bytes.NewBufferString(body))
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d: %s", rec.Code, rec.Body.String())
	}
	resp := decodeVMResponse(t, rec.Body)
	if resp.Warning != "" {
		t.Fatalf("expected no warning without shareable disks, got %q", resp.Warning)
	}
}

func TestVMHandler_CreateVM_InvalidName(t *testing.T) {
	provider := &fakeVMProvider{}
	handler := newTestVMHandler(t, provider)

	body := `{"name":"bad name","distro":"ubuntu-24.04","cpu":1,"memoryMB":1024,"diskGB":10}`
	req := httptest.NewRequest(http.MethodPost, "/api/vms", bytes.NewBufferString(body))
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for invalid name, got %d", rec.Code)
	}
	if len(provider.started) != 0 || provider.lastCreate.Name != "" {
		t.Fatal("provider should not be invoked when validation fails")
	}
}

func TestVMHandler_CreateVM_InvalidDistro(t *testing.T) {
	provider := &fakeVMProvider{}
	handler := newTestVMHandler(t, provider)

	body := `{"name":"vm-test","distro":"fedora-40","cpu":1,"memoryMB":1024,"diskGB":10}`
	req := httptest.NewRequest(http.MethodPost, "/api/vms", bytes.NewBufferString(body))
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for invalid distro, got %d", rec.Code)
	}
}

func TestVMHandler_CreateVM_InvalidResources(t *testing.T) {
	provider := &fakeVMProvider{}
	handler := newTestVMHandler(t, provider)

	body := `{"name":"vm-test","distro":"ubuntu-24.04","cpu":0,"memoryMB":1024,"diskGB":10}`
	req := httptest.NewRequest(http.MethodPost, "/api/vms", bytes.NewBufferString(body))
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for zero cpu, got %d", rec.Code)
	}
}

func TestVMHandler_CreateVM_DuplicateSSHKey(t *testing.T) {
	provider := &fakeVMProvider{}
	cfg := &config.Config{KeysStorePath: t.TempDir()}
	handler := NewVMHandler(cfg, provider)

	body := `{"name":"vm-test","distro":"ubuntu-24.04","cpu":1,"memoryMB":1024,"diskGB":10}`

	// Primera creacion: exitosa, deja la llave 'vm-test-ssh' en disco.
	req := httptest.NewRequest(http.MethodPost, "/api/vms", bytes.NewBufferString(body))
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("expected first create to succeed, got %d: %s", rec.Code, rec.Body.String())
	}

	// Segunda creacion con el mismo nombre: la llave ya existe en disco.
	req2 := httptest.NewRequest(http.MethodPost, "/api/vms", bytes.NewBufferString(body))
	rec2 := httptest.NewRecorder()
	handler.ServeHTTP(rec2, req2)
	if rec2.Code != http.StatusConflict {
		t.Fatalf("expected 409 for duplicate ssh key, got %d: %s", rec2.Code, rec2.Body.String())
	}
}

func TestVMHandler_CreateVM_ProviderError_CleansUpKey(t *testing.T) {
	provider := &fakeVMProvider{createErr: errors.New("virt-install failed")}
	cfg := &config.Config{KeysStorePath: t.TempDir()}
	handler := NewVMHandler(cfg, provider)

	body := `{"name":"vm-test","distro":"ubuntu-24.04","cpu":1,"memoryMB":1024,"diskGB":10}`
	req := httptest.NewRequest(http.MethodPost, "/api/vms", bytes.NewBufferString(body))
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadGateway {
		t.Fatalf("expected 502 when the provider fails, got %d", rec.Code)
	}

	// La llave generada antes de llamar al provider debe limpiarse.
	req2 := httptest.NewRequest(http.MethodPost, "/api/vms", bytes.NewBufferString(body))
	rec2 := httptest.NewRecorder()
	provider.createErr = nil
	handler.ServeHTTP(rec2, req2)
	if rec2.Code != http.StatusCreated {
		t.Fatalf("expected retry to succeed after cleanup, got %d: %s", rec2.Code, rec2.Body.String())
	}
}

func TestVMHandler_StartStopDeleteVM(t *testing.T) {
	provider := &fakeVMProvider{}
	handler := newTestVMHandler(t, provider)

	start := httptest.NewRequest(http.MethodPost, "/api/vms/vm1/start", nil)
	recStart := httptest.NewRecorder()
	handler.ServeHTTP(recStart, start)
	if recStart.Code != http.StatusOK || len(provider.started) != 1 || provider.started[0] != "vm1" {
		t.Fatalf("unexpected start result: code=%d started=%v", recStart.Code, provider.started)
	}

	stop := httptest.NewRequest(http.MethodPost, "/api/vms/vm1/stop", nil)
	recStop := httptest.NewRecorder()
	handler.ServeHTTP(recStop, stop)
	if recStop.Code != http.StatusOK || len(provider.stopped) != 1 || provider.stopped[0] != "vm1" {
		t.Fatalf("unexpected stop result: code=%d stopped=%v", recStop.Code, provider.stopped)
	}

	del := httptest.NewRequest(http.MethodDelete, "/api/vms/vm1", nil)
	recDel := httptest.NewRecorder()
	handler.ServeHTTP(recDel, del)
	if recDel.Code != http.StatusOK || len(provider.deleted) != 1 || provider.deleted[0] != "vm1" {
		t.Fatalf("unexpected delete result: code=%d deleted=%v", recDel.Code, provider.deleted)
	}
}

func TestVMHandler_ListDisks(t *testing.T) {
	provider := &fakeVMProvider{disks: map[string][]vm.Disk{
		"vm1": {
			{Name: "system", Path: "/var/lib/libvirt/images/vm1-system.qcow2", SizeGB: 20},
			{Name: "cluster-data", Path: "/var/lib/libvirt/shared-disks/cluster-data.qcow2", SizeGB: 5, Shareable: true},
		},
	}}
	handler := newTestVMHandler(t, provider)

	req := httptest.NewRequest(http.MethodGet, "/api/vms/vm1/disks", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	resp := decodeVMResponse(t, rec.Body)
	disks, ok := resp.Data.([]interface{})
	if !ok || len(disks) != 2 {
		t.Fatalf("expected 2 disks in response, got %+v", resp.Data)
	}
}

func TestVMHandler_ListDisks_ProviderError(t *testing.T) {
	provider := &fakeVMProvider{listDisksErr: errors.New("virsh unreachable")}
	handler := newTestVMHandler(t, provider)

	req := httptest.NewRequest(http.MethodGet, "/api/vms/vm1/disks", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadGateway {
		t.Fatalf("expected 502, got %d", rec.Code)
	}
}

func TestVMHandler_DeleteVM_WarnsAboutPreservedSharedDisks(t *testing.T) {
	provider := &fakeVMProvider{disks: map[string][]vm.Disk{
		"vm1": {
			{Name: "system", Shareable: false},
			{Name: "cluster-data", Shareable: true},
		},
	}}
	handler := newTestVMHandler(t, provider)

	req := httptest.NewRequest(http.MethodDelete, "/api/vms/vm1", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	resp := decodeVMResponse(t, rec.Body)
	if resp.Warning == "" {
		t.Fatal("expected a warning about the preserved shared disk")
	}
}

func TestVMHandler_DeleteVM_NoWarningWithoutSharedDisks(t *testing.T) {
	provider := &fakeVMProvider{disks: map[string][]vm.Disk{
		"vm1": {{Name: "system", Shareable: false}},
	}}
	handler := newTestVMHandler(t, provider)

	req := httptest.NewRequest(http.MethodDelete, "/api/vms/vm1", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	resp := decodeVMResponse(t, rec.Body)
	if resp.Warning != "" {
		t.Fatalf("expected no warning, got %q", resp.Warning)
	}
}

func TestVMHandler_RenameVM(t *testing.T) {
	provider := &fakeVMProvider{}
	handler := newTestVMHandler(t, provider)

	body := `{"newName":"vm2"}`
	req := httptest.NewRequest(http.MethodPost, "/api/vms/vm1/rename", bytes.NewBufferString(body))
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	if len(provider.renamed) != 1 || provider.renamed[0] != [2]string{"vm1", "vm2"} {
		t.Fatalf("unexpected rename call: %v", provider.renamed)
	}
}

func TestVMHandler_RenameVM_MissingNewName(t *testing.T) {
	provider := &fakeVMProvider{}
	handler := newTestVMHandler(t, provider)

	req := httptest.NewRequest(http.MethodPost, "/api/vms/vm1/rename", bytes.NewBufferString(`{}`))
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 when newName is missing, got %d", rec.Code)
	}
	if len(provider.renamed) != 0 {
		t.Fatal("provider should not be called without a newName")
	}
}

func TestVMHandler_UpdateVM(t *testing.T) {
	provider := &fakeVMProvider{}
	handler := newTestVMHandler(t, provider)

	body := `{"cpu":4,"memoryMB":4096}`
	req := httptest.NewRequest(http.MethodPut, "/api/vms/vm1", bytes.NewBufferString(body))
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	if len(provider.updated) != 1 || provider.updated[0].CPU != 4 || provider.updated[0].MemoryMB != 4096 {
		t.Fatalf("unexpected update call: %v", provider.updated)
	}
}

func TestVMHandler_UnknownRoute(t *testing.T) {
	provider := &fakeVMProvider{}
	handler := newTestVMHandler(t, provider)

	req := httptest.NewRequest(http.MethodGet, "/api/vms/vm1/unknown", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected 404 for unknown route, got %d", rec.Code)
	}
}
