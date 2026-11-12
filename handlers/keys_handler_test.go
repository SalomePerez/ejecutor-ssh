package handlers

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"ejecutor-ssh/config"
	"ejecutor-ssh/users"
)

var errAlreadyExists = errors.New("el usuario 'tester' ya existe en el sistema")

func newTestKeysHandler(t *testing.T, token string) (*KeysHandler, *config.Config) {
	t.Helper()
	cfg := &config.Config{KeysStorePath: t.TempDir(), APIToken: token}
	return NewKeysHandler(cfg), cfg
}

func decodeKeyResponse(t *testing.T, body *bytes.Buffer) KeyResponse {
	t.Helper()
	var resp KeyResponse
	if err := json.Unmarshal(body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v (body=%s)", err, body.String())
	}
	return resp
}

func TestKeysHandler_GenerateAndList(t *testing.T) {
	handler, _ := newTestKeysHandler(t, "")

	body := `{"keyName":"test-key","username":"tester"}`
	req := httptest.NewRequest(http.MethodPost, "/api/keys", bytes.NewBufferString(body))
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d: %s", rec.Code, rec.Body.String())
	}

	listReq := httptest.NewRequest(http.MethodGet, "/api/keys", nil)
	listRec := httptest.NewRecorder()
	handler.ServeHTTP(listRec, listReq)

	resp := decodeKeyResponse(t, listRec.Body)
	keys, ok := resp.Data.([]interface{})
	if !ok || len(keys) != 1 {
		t.Fatalf("expected 1 key listed, got %+v", resp.Data)
	}
}

func TestKeysHandler_Generate_MissingFields(t *testing.T) {
	handler, _ := newTestKeysHandler(t, "")

	req := httptest.NewRequest(http.MethodPost, "/api/keys", bytes.NewBufferString(`{"keyName":""}`))
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", rec.Code)
	}
}

func TestKeysHandler_Download_RequiresToken(t *testing.T) {
	handler, cfg := newTestKeysHandler(t, "secret")
	writeTestKeyFiles(t, cfg.KeysStorePath, "test-key")

	// Sin header.
	req := httptest.NewRequest(http.MethodGet, "/api/keys/test-key/download", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 without token, got %d", rec.Code)
	}

	// Token incorrecto.
	req2 := httptest.NewRequest(http.MethodGet, "/api/keys/test-key/download", nil)
	req2.Header.Set(apiKeyHeader, "wrong")
	rec2 := httptest.NewRecorder()
	handler.ServeHTTP(rec2, req2)
	if rec2.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 with wrong token, got %d", rec2.Code)
	}

	// Token correcto.
	req3 := httptest.NewRequest(http.MethodGet, "/api/keys/test-key/download", nil)
	req3.Header.Set(apiKeyHeader, "secret")
	rec3 := httptest.NewRecorder()
	handler.ServeHTTP(rec3, req3)
	if rec3.Code != http.StatusOK {
		t.Fatalf("expected 200 with correct token, got %d: %s", rec3.Code, rec3.Body.String())
	}
	if rec3.Body.String() != "fake-private-key" {
		t.Fatalf("unexpected downloaded content: %q", rec3.Body.String())
	}
	if disposition := rec3.Header().Get("Content-Disposition"); disposition == "" {
		t.Fatal("expected a Content-Disposition header on download")
	}
}

func TestKeysHandler_Download_DisabledWithoutConfiguredToken(t *testing.T) {
	handler, cfg := newTestKeysHandler(t, "")
	writeTestKeyFiles(t, cfg.KeysStorePath, "test-key")

	req := httptest.NewRequest(http.MethodGet, "/api/keys/test-key/download", nil)
	req.Header.Set(apiKeyHeader, "anything")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected download to stay disabled without a configured token, got %d", rec.Code)
	}
}

func TestKeysHandler_Delete_RequiresToken(t *testing.T) {
	handler, cfg := newTestKeysHandler(t, "secret")
	writeTestKeyFiles(t, cfg.KeysStorePath, "test-key")

	req := httptest.NewRequest(http.MethodDelete, "/api/keys/test-key", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 without token, got %d", rec.Code)
	}
	if _, err := os.Stat(filepath.Join(cfg.KeysStorePath, "test-key")); err != nil {
		t.Fatal("key should not be deleted without a valid token")
	}

	req2 := httptest.NewRequest(http.MethodDelete, "/api/keys/test-key", nil)
	req2.Header.Set(apiKeyHeader, "secret")
	rec2 := httptest.NewRecorder()
	handler.ServeHTTP(rec2, req2)
	if rec2.Code != http.StatusOK {
		t.Fatalf("expected 200 with correct token, got %d: %s", rec2.Code, rec2.Body.String())
	}
	if _, err := os.Stat(filepath.Join(cfg.KeysStorePath, "test-key")); !os.IsNotExist(err) {
		t.Fatal("expected the key file to be removed")
	}
}

// writeTestKeyFiles crea un par de archivos de llave falsos para probar
// descarga/eliminacion sin depender de sshkeys.GenerateED25519.
func writeTestKeyFiles(t *testing.T, dir, name string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte("fake-private-key"), 0600); err != nil {
		t.Fatalf("write private key fixture: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, name+".pub"), []byte("fake-public-key"), 0644); err != nil {
		t.Fatalf("write public key fixture: %v", err)
	}
}

// ---------- UsersHandler ----------

type fakeUserAccount struct {
	createErr error
	deleteErr error
	created   bool
	deleted   bool
}

func (f *fakeUserAccount) Create() error {
	f.created = true
	return f.createErr
}

func (f *fakeUserAccount) Delete() error {
	f.deleted = true
	return f.deleteErr
}

func newTestUsersHandler(fake *fakeUserAccount) *UsersHandler {
	return &UsersHandler{
		config: &config.Config{APIToken: "token-de-prueba"},
		newUser: func(username, password, fullName string) userAccount {
			return fake
		},
	}
}

func TestUsersHandler_CreateUser_Success(t *testing.T) {
	fake := &fakeUserAccount{}
	handler := newTestUsersHandler(fake)

	body := `{"username":"tester","password":"password123","fullName":"Test User"}`
	req := httptest.NewRequest(http.MethodPost, "/api/users", bytes.NewBufferString(body))
	req.Header.Set("X-API-Key", "token-de-prueba")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d: %s", rec.Code, rec.Body.String())
	}
	if !fake.created {
		t.Fatal("expected the account factory's Create to be called")
	}
}

func TestUsersHandler_CreateUser_MissingFields(t *testing.T) {
	fake := &fakeUserAccount{}
	handler := newTestUsersHandler(fake)

	req := httptest.NewRequest(http.MethodPost, "/api/users", bytes.NewBufferString(`{"username":"tester"}`))
	req.Header.Set("X-API-Key", "token-de-prueba")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", rec.Code)
	}
	if fake.created {
		t.Fatal("should not create an account without a password")
	}
}

func TestUsersHandler_CreateUser_ShortPassword(t *testing.T) {
	fake := &fakeUserAccount{}
	handler := newTestUsersHandler(fake)

	body := `{"username":"tester","password":"short"}`
	req := httptest.NewRequest(http.MethodPost, "/api/users", bytes.NewBufferString(body))
	req.Header.Set("X-API-Key", "token-de-prueba")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for a short password, got %d", rec.Code)
	}
}

func TestUsersHandler_CreateUser_AlreadyExists(t *testing.T) {
	fake := &fakeUserAccount{createErr: errAlreadyExists}
	handler := newTestUsersHandler(fake)

	body := `{"username":"tester","password":"password123"}`
	req := httptest.NewRequest(http.MethodPost, "/api/users", bytes.NewBufferString(body))
	req.Header.Set("X-API-Key", "token-de-prueba")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusConflict {
		t.Fatalf("expected 409 when the user already exists, got %d", rec.Code)
	}
}

func TestUsersHandler_DeleteUser_Success(t *testing.T) {
	fake := &fakeUserAccount{}
	handler := newTestUsersHandler(fake)

	req := httptest.NewRequest(http.MethodDelete, "/api/users/tester", nil)
	req.Header.Set("X-API-Key", "token-de-prueba")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	if !fake.deleted {
		t.Fatal("expected the account factory's Delete to be called")
	}
}

func TestUsersHandler_DeleteUser_MissingUsername(t *testing.T) {
	fake := &fakeUserAccount{}
	handler := newTestUsersHandler(fake)

	req := httptest.NewRequest(http.MethodDelete, "/api/users/", nil)
	req.Header.Set("X-API-Key", "token-de-prueba")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", rec.Code)
	}
	if fake.deleted {
		t.Fatal("should not attempt to delete without a username")
	}
}

func TestUsersHandler_CreateUser_RequiresToken(t *testing.T) {
	fake := &fakeUserAccount{}
	handler := newTestUsersHandler(fake)

	body := `{"username":"tester","password":"password123"}`
	req := httptest.NewRequest(http.MethodPost, "/api/users", bytes.NewBufferString(body))
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 without token, got %d", rec.Code)
	}
	if fake.created {
		t.Fatal("must not create an account without the API token")
	}
}

func TestUsersHandler_ListUsers(t *testing.T) {
	handler := newTestUsersHandler(&fakeUserAccount{})
	handler.listUsers = func() ([]users.LocalUser, error) {
		return []users.LocalUser{{Name: "sshweb", Enabled: true}}, nil
	}

	req := httptest.NewRequest(http.MethodGet, "/api/users", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"sshweb"`) {
		t.Fatalf("expected the user list, got %d: %s", rec.Code, rec.Body.String())
	}
}
