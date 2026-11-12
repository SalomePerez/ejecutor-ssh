package handlers

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"ejecutor-ssh/config"
)

func newTestConfigHandler(t *testing.T) (*ConfigHandler, *config.Config) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.properties")
	if err := os.WriteFile(path, []byte("security.apiToken=super-secret\nssh.host=localhost\n"), 0644); err != nil {
		t.Fatalf("write fixture config: %v", err)
	}
	cfg, err := config.Load(path)
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	return NewConfigHandler(cfg), cfg
}

func TestConfigHandler_Get_NeverExposesAPIToken(t *testing.T) {
	handler, cfg := newTestConfigHandler(t)
	if cfg.APIToken != "super-secret" {
		t.Fatalf("expected the token to be loaded from the file, got %q", cfg.APIToken)
	}

	req := httptest.NewRequest(http.MethodGet, "/api/config", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	if strings.Contains(rec.Body.String(), "super-secret") {
		t.Fatalf("GET /api/config must never expose the api token, got body: %s", rec.Body.String())
	}
}

func TestConfigHandler_Put_CannotOverrideAPIToken(t *testing.T) {
	handler, cfg := newTestConfigHandler(t)

	body := `{"security.apiToken":"attacker-controlled","ssh.host":"192.168.1.50"}`
	req := httptest.NewRequest(http.MethodPut, "/api/config", bytes.NewBufferString(body))
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	if cfg.APIToken != "super-secret" {
		t.Fatalf("PUT /api/config must not be able to change the api token, got %q", cfg.APIToken)
	}
	if cfg.SSHHost != "192.168.1.50" {
		t.Fatalf("expected the unrelated ssh.host field to update normally, got %q", cfg.SSHHost)
	}
}

func TestConfigHandler_MethodNotAllowed(t *testing.T) {
	handler, _ := newTestConfigHandler(t)

	req := httptest.NewRequest(http.MethodDelete, "/api/config", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("expected 405, got %d", rec.Code)
	}
}
