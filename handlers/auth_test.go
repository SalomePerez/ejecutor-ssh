package handlers

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"ejecutor-ssh/config"
)

func TestRequireAPIToken_NotConfigured(t *testing.T) {
	cfg := &config.Config{}
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set(apiKeyHeader, "anything")

	if err := requireAPIToken(cfg, req); err == nil {
		t.Fatal("expected the route to stay disabled when no token is configured")
	}
}

func TestRequireAPIToken_MissingHeader(t *testing.T) {
	cfg := &config.Config{APIToken: "secret"}
	req := httptest.NewRequest(http.MethodGet, "/", nil)

	if err := requireAPIToken(cfg, req); err == nil {
		t.Fatal("expected an error when the header is missing")
	}
}

func TestRequireAPIToken_WrongToken(t *testing.T) {
	cfg := &config.Config{APIToken: "secret"}
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set(apiKeyHeader, "wrong")

	if err := requireAPIToken(cfg, req); err == nil {
		t.Fatal("expected an error for a wrong token")
	}
}

func TestRequireAPIToken_CorrectToken(t *testing.T) {
	cfg := &config.Config{APIToken: "secret"}
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set(apiKeyHeader, "secret")

	if err := requireAPIToken(cfg, req); err != nil {
		t.Fatalf("unexpected error for the correct token: %v", err)
	}
}

func TestRequireAPIToken_NilConfig(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set(apiKeyHeader, "secret")

	if err := requireAPIToken(nil, req); err == nil {
		t.Fatal("expected an error for a nil config")
	}
}
