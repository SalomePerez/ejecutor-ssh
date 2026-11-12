package handlers

import (
	"encoding/json"
	"fmt"
	"net/http"
	"path/filepath"
	"strings"

	"ejecutor-ssh/config"
	"ejecutor-ssh/sshkeys"
	"ejecutor-ssh/users"
)

// KeysHandler maneja las solicitudes para gestión de llaves SSH.
type KeysHandler struct {
	config *config.Config
}

// NewKeysHandler crea un nuevo handler para la API de llaves.
func NewKeysHandler(cfg *config.Config) *KeysHandler {
	return &KeysHandler{config: cfg}
}

// KeyRequest estructura para solicitudes de creación de llaves.
type KeyRequest struct {
	// KeyName es el nombre de la llave (sin extensión).
	KeyName string `json:"keyName"`
	// Username es el usuario local para el que se genera la llave.
	Username string `json:"username"`
}

// KeyResponse estructura para respuestas de la API.
type KeyResponse struct {
	// Success indica si la operación fue exitosa.
	Success bool `json:"success"`
	// Message es un mensaje descriptivo.
	Message string `json:"message"`
	// Data contiene información adicional.
	Data interface{} `json:"data,omitempty"`
	// Error contiene detalles del error si ocurrió.
	Error string `json:"error,omitempty"`
}

// KeyInfo estructura para información de una llave.
type KeyInfo struct {
	Name      string `json:"name"`
	Algorithm string `json:"algorithm"`
	Path      string `json:"path"`
}

// ServeHTTP implementa http.Handler para gestionar rutas de llaves.
func (h *KeysHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	// Extrae la ruta y método.
	path := r.URL.Path
	method := r.Method

	// Enruta según el método y la ruta.
	if method == http.MethodPost && path == "/api/keys" {
		h.handleGenerateKeys(w, r)
	} else if method == http.MethodGet && path == "/api/keys" {
		h.handleListKeys(w, r)
	} else if method == http.MethodGet && strings.HasPrefix(path, "/api/keys/") {
		// Extrae el nombre de la llave y la acción.
		rest := strings.TrimPrefix(path, "/api/keys/")

		// Verifica si es una descarga.
		if strings.HasSuffix(rest, "/download") {
			keyName := strings.TrimSuffix(rest, "/download")
			h.handleDownloadKey(w, r, keyName)
		} else if strings.HasSuffix(rest, "/public") {
			h.handlePublicKey(w, r, strings.TrimSuffix(rest, "/public"))
		} else {
			http.Error(w, "ruta no encontrada", http.StatusNotFound)
		}
	} else if method == http.MethodDelete && strings.HasPrefix(path, "/api/keys/") {
		keyName := strings.TrimPrefix(path, "/api/keys/")
		keyName = strings.TrimSuffix(keyName, "/") // Elimina barra final si existe.
		h.handleDeleteKey(w, r, keyName)
	} else {
		http.Error(w, "ruta no encontrada", http.StatusNotFound)
	}
}

// handleGenerateKeys maneja POST /api/keys - genera un nuevo par de llaves.
func (h *KeysHandler) handleGenerateKeys(w http.ResponseWriter, r *http.Request) {
	// Limita el tamaño de la solicitud.
	r.Body = http.MaxBytesReader(w, r.Body, 1024)

	// Decodifica el JSON.
	var req KeyRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeKeyResponse(w, http.StatusBadRequest, KeyResponse{
			Success: false,
			Error:   "JSON inválido",
		})
		return
	}

	// Valida los datos.
	if req.KeyName == "" || req.Username == "" {
		writeKeyResponse(w, http.StatusBadRequest, KeyResponse{
			Success: false,
			Error:   "keyName y username son requeridos",
		})
		return
	}

	// Genera el par de llaves ED25519.
	keyPair, err := sshkeys.GenerateED25519()
	if err != nil {
		writeKeyResponse(w, http.StatusInternalServerError, KeyResponse{
			Success: false,
			Error:   fmt.Sprintf("error generando llaves: %v", err),
		})
		return
	}

	// Guarda las llaves en archivos.
	privatePath, publicPath, err := keyPair.SaveToFiles(h.config.KeysStorePath, req.KeyName)
	if err != nil {
		writeKeyResponse(w, http.StatusInternalServerError, KeyResponse{
			Success: false,
			Error:   fmt.Sprintf("error guardando llaves: %v", err),
		})
		return
	}

	// Respuesta exitosa.
	writeKeyResponse(w, http.StatusCreated, KeyResponse{
		Success: true,
		Message: fmt.Sprintf("Llaves generadas para %s", req.Username),
		Data: map[string]string{
			"keyName":     req.KeyName,
			"privatePath": privatePath,
			"publicPath":  publicPath,
			"algorithm":   keyPair.Algorithm,
		},
	})
}

// handleListKeys maneja GET /api/keys - lista todas las llaves disponibles.
func (h *KeysHandler) handleListKeys(w http.ResponseWriter, r *http.Request) {
	// Lista las llaves del directorio.
	keyNames, err := sshkeys.ListKeys(h.config.KeysStorePath)
	if err != nil {
		writeKeyResponse(w, http.StatusInternalServerError, KeyResponse{
			Success: false,
			Error:   fmt.Sprintf("error listando llaves: %v", err),
		})
		return
	}

	// Convierte a KeyInfo.
	var keys []KeyInfo
	for _, name := range keyNames {
		keys = append(keys, KeyInfo{
			Name:      name,
			Algorithm: "ssh-ed25519",
			Path:      filepath.Join(h.config.KeysStorePath, name),
		})
	}

	// Respuesta con la lista de llaves.
	writeKeyResponse(w, http.StatusOK, KeyResponse{
		Success: true,
		Message: fmt.Sprintf("%d llaves encontradas", len(keys)),
		Data:    keys,
	})
}

// handlePublicKey maneja GET /api/keys/{nombre}/public - devuelve la llave
// publica, que no es secreta: es la que se copia a authorized_keys.
func (h *KeysHandler) handlePublicKey(w http.ResponseWriter, r *http.Request, keyName string) {
	if keyName == "" || strings.ContainsAny(keyName, "/\\") || strings.Contains(keyName, "..") {
		writeKeyResponse(w, http.StatusBadRequest, KeyResponse{Success: false, Error: "nombre de llave inválido"})
		return
	}
	content, err := sshkeys.ReadPublicKey(filepath.Join(h.config.KeysStorePath, keyName+".pub"))
	if err != nil {
		writeKeyResponse(w, http.StatusNotFound, KeyResponse{Success: false, Error: "llave pública no encontrada"})
		return
	}
	writeKeyResponse(w, http.StatusOK, KeyResponse{Success: true, Message: "Llave pública", Data: map[string]string{"name": keyName, "publicKey": strings.TrimSpace(string(content))}})
}

// handleDownloadKey maneja GET /api/keys/{nombre}/download - descarga la llave privada.
// Protegida con token de API: sin este control cualquiera en la red podria
// descargar credenciales SSH sin autenticarse.
func (h *KeysHandler) handleDownloadKey(w http.ResponseWriter, r *http.Request, keyName string) {
	if err := requireAPIToken(h.config, r); err != nil {
		http.Error(w, err.Error(), http.StatusUnauthorized)
		return
	}

	// Valida el nombre de la llave.
	if keyName == "" {
		http.Error(w, "nombre de llave no especificado", http.StatusBadRequest)
		return
	}

	// Lee la llave privada.
	keyPath := filepath.Join(h.config.KeysStorePath, keyName)
	keyData, err := sshkeys.ReadPrivateKey(keyPath)
	if err != nil {
		http.Error(w, fmt.Sprintf("error leyendo llave: %v", err), http.StatusNotFound)
		return
	}

	// Envía la llave como descarga.
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%s", keyName))
	w.WriteHeader(http.StatusOK)
	w.Write(keyData)
}

// handleDeleteKey maneja DELETE /api/keys/{nombre} - elimina un par de llaves.
// Misma proteccion que la descarga: borrar una llave es tan sensible como
// exponerla (deja al usuario SSH correspondiente sin acceso o abre la puerta
// a que alguien borre llaves ajenas sin autorizacion).
func (h *KeysHandler) handleDeleteKey(w http.ResponseWriter, r *http.Request, keyName string) {
	if err := requireAPIToken(h.config, r); err != nil {
		writeKeyResponse(w, http.StatusUnauthorized, KeyResponse{
			Success: false,
			Error:   err.Error(),
		})
		return
	}

	// Valida el nombre de la llave.
	if keyName == "" {
		writeKeyResponse(w, http.StatusBadRequest, KeyResponse{
			Success: false,
			Error:   "nombre de llave no especificado",
		})
		return
	}

	// Elimina el par de llaves.
	if err := sshkeys.DeleteKeyPair(h.config.KeysStorePath, keyName); err != nil {
		writeKeyResponse(w, http.StatusInternalServerError, KeyResponse{
			Success: false,
			Error:   fmt.Sprintf("error eliminando llaves: %v", err),
		})
		return
	}

	// Respuesta exitosa.
	writeKeyResponse(w, http.StatusOK, KeyResponse{
		Success: true,
		Message: fmt.Sprintf("Llaves '%s' eliminadas", keyName),
	})
}

// writeKeyResponse escribe una respuesta JSON para la API de llaves.
func writeKeyResponse(w http.ResponseWriter, status int, resp KeyResponse) {
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(resp)
}

// userAccount abstrae las operaciones de users.User que necesita UsersHandler.
// Permite inyectar un doble de prueba y evitar que los tests unitarios
// ejecuten "net user" contra el sistema operativo real.
type userAccount interface {
	Create() error
	Delete() error
}

// UsersHandler maneja las solicitudes para crear usuarios locales.
type UsersHandler struct {
	config *config.Config
	// newUser construye la cuenta a crear/eliminar. Por defecto delega en
	// users.User; los tests sustituyen este campo con un doble.
	newUser func(username, password, fullName string) userAccount
	// listUsers devuelve las cuentas locales; los tests lo sustituyen.
	listUsers func() ([]users.LocalUser, error)
}

// NewUsersHandler crea un nuevo handler para la API de usuarios.
func NewUsersHandler(cfg *config.Config) *UsersHandler {
	return &UsersHandler{
		config: cfg,
		newUser: func(username, password, fullName string) userAccount {
			return &users.User{Username: username, Password: password, FullName: fullName, IsSSHUser: true}
		},
		listUsers: users.List,
	}
}

// UserRequest estructura para solicitudes de creación de usuarios.
type UserRequest struct {
	Username string `json:"username"`
	Password string `json:"password"`
	FullName string `json:"fullName,omitempty"`
}

// UserResponse estructura para respuestas de la API de usuarios.
type UserResponse struct {
	Success bool        `json:"success"`
	Message string      `json:"message"`
	Data    interface{} `json:"data,omitempty"`
	Error   string      `json:"error,omitempty"`
}

// ServeHTTP implementa http.Handler para gestionar rutas de usuarios.
func (h *UsersHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	// Enruta según el método.
	path := r.URL.Path
	method := r.Method

	// Crear o eliminar cuentas de Windows exige el token: sin el, cualquiera
	// que alcance el puerto de la app podria crearse un usuario local.
	if method == http.MethodPost || method == http.MethodDelete {
		if err := requireAPIToken(h.config, r); err != nil {
			writeUserResponse(w, http.StatusUnauthorized, UserResponse{Success: false, Error: err.Error()})
			return
		}
	}

	if method == http.MethodGet && path == "/api/users" {
		h.handleListUsers(w, r)
	} else if method == http.MethodPost && path == "/api/users" {
		h.handleCreateUser(w, r)
	} else if method == http.MethodDelete && strings.HasPrefix(path, "/api/users/") {
		username := strings.TrimPrefix(path, "/api/users/")
		username = strings.TrimSuffix(username, "/") // Elimina barra final si existe.
		h.handleDeleteUser(w, r, username)
	} else {
		http.Error(w, "ruta no encontrada", http.StatusNotFound)
	}
}

// handleCreateUser maneja POST /api/users - crea un nuevo usuario local.
func (h *UsersHandler) handleCreateUser(w http.ResponseWriter, r *http.Request) {
	// Limita el tamaño de la solicitud.
	r.Body = http.MaxBytesReader(w, r.Body, 2048)

	// Decodifica el JSON.
	var req UserRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeUserResponse(w, http.StatusBadRequest, UserResponse{
			Success: false,
			Error:   "JSON inválido",
		})
		return
	}

	// Valida los datos.
	if req.Username == "" || req.Password == "" {
		writeUserResponse(w, http.StatusBadRequest, UserResponse{
			Success: false,
			Error:   "username y password son requeridos",
		})
		return
	}

	// Validación adicional: verifica que la contraseña sea suficientemente segura
	if len(req.Password) < 8 {
		writeUserResponse(w, http.StatusBadRequest, UserResponse{
			Success: false,
			Error:   "La contraseña debe tener al menos 8 caracteres",
		})
		return
	}

	// Crea el usuario.
	user := h.newUser(req.Username, req.Password, req.FullName)

	if err := user.Create(); err != nil {
		// Determina el código de estado HTTP basado en el tipo de error
		errorMsg := err.Error()

		// Si el usuario ya existe, devuelve 409 Conflict
		if strings.Contains(errorMsg, "ya existe") {
			writeUserResponse(w, http.StatusConflict, UserResponse{
				Success: false,
				Error:   errorMsg,
			})
			return
		}

		// Si es un error de permisos o contraseña, devuelve 400 Bad Request
		if strings.Contains(errorMsg, "permisos") || strings.Contains(errorMsg, "contraseña") {
			writeUserResponse(w, http.StatusBadRequest, UserResponse{
				Success: false,
				Error:   errorMsg,
			})
			return
		}

		// Otros errores devuelven 500 Internal Server Error
		writeUserResponse(w, http.StatusInternalServerError, UserResponse{
			Success: false,
			Error:   fmt.Sprintf("error creando usuario: %v", err),
		})
		return
	}

	// Respuesta exitosa.
	writeUserResponse(w, http.StatusCreated, UserResponse{
		Success: true,
		Message: fmt.Sprintf("Usuario '%s' creado exitosamente", req.Username),
		Data: map[string]string{
			"username": req.Username,
		},
	})
}

// handleListUsers maneja GET /api/users - lista las cuentas locales.
func (h *UsersHandler) handleListUsers(w http.ResponseWriter, r *http.Request) {
	list, err := h.listUsers()
	if err != nil {
		writeUserResponse(w, http.StatusInternalServerError, UserResponse{Success: false, Error: err.Error()})
		return
	}
	writeUserResponse(w, http.StatusOK, UserResponse{Success: true, Message: fmt.Sprintf("%d usuarios", len(list)), Data: list})
}

// handleDeleteUser maneja DELETE /api/users/{username} - elimina un usuario.
func (h *UsersHandler) handleDeleteUser(w http.ResponseWriter, r *http.Request, username string) {
	// Valida el nombre de usuario.
	if username == "" {
		writeUserResponse(w, http.StatusBadRequest, UserResponse{
			Success: false,
			Error:   "nombre de usuario no especificado",
		})
		return
	}

	// Crea la estructura del usuario y lo elimina.
	user := h.newUser(username, "", "")
	if err := user.Delete(); err != nil {
		writeUserResponse(w, http.StatusInternalServerError, UserResponse{
			Success: false,
			Error:   fmt.Sprintf("error eliminando usuario: %v", err),
		})
		return
	}

	// Respuesta exitosa.
	writeUserResponse(w, http.StatusOK, UserResponse{
		Success: true,
		Message: fmt.Sprintf("Usuario '%s' eliminado", username),
	})
}

// writeUserResponse escribe una respuesta JSON para la API de usuarios.
func writeUserResponse(w http.ResponseWriter, status int, resp UserResponse) {
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(resp)
}
