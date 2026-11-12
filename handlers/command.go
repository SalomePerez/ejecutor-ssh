package handlers

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os/exec"
	"runtime"
	"strings"

	"ejecutor-ssh/config"
	"ejecutor-ssh/sshclient"
)

type CommandExecutor interface {
	// Abstrae la ejecucion remota para que el handler no dependa directamente de SSH.
	// Tambien permite sustituir el cliente por un mock en pruebas.
	ExecuteCommand(command string) ([]byte, error)
}

type CommandHandler struct {
	// executor conserva el cliente SSH para el modo remoto.
	executor CommandExecutor
	// config contiene la configuracion de la aplicacion.
	config *config.Config
}

type LocalExecutor struct{}

type ConfiguredSSHExecutor struct {
	Config *config.Config
}

func (executor ConfiguredSSHExecutor) ExecuteCommand(command string) ([]byte, error) {
	if executor.Config == nil {
		return nil, errors.New("configuracion SSH no inicializada")
	}
	client := sshclient.NewClient(sshclient.Config{
		Host:           executor.Config.SSHHost,
		Port:           executor.Config.SSHPort,
		User:           executor.Config.SSHUser,
		KeyPath:        executor.Config.SSHKeyPath,
		KnownHostsPath: executor.Config.SSHKnownHostsPath,
	})
	return client.ExecuteCommand(command)
}

func (LocalExecutor) ExecuteCommand(command string) ([]byte, error) {
	var cmd *exec.Cmd
	shell := "cmd"
	if runtime.GOOS == "linux" || runtime.GOOS == "darwin" {
		shell = "bash"
	}

	switch shell {
	case "cmd":
		cmd = exec.Command("cmd", "/C", command)
	case "bash":
		cmd = exec.Command("bash", "-lc", command)
	default:
		cmd = exec.Command("sh", "-lc", command)
	}

	output, err := cmd.CombinedOutput()
	if shell == "cmd" {
		// cmd.exe escribe en la pagina de codigos OEM del sistema (ej. 850 en
		// Windows en espanol), no en UTF-8. Sin esta conversion, cualquier
		// caracter fuera de ASCII (acentos, el espacio angosto de "p. m.")
		// llega como bytes invalidos en UTF-8 y el navegador los muestra
		// como "�". Ver decodeOEM (oem_windows.go).
		output = decodeOEM(output)
	}
	if err != nil {
		return output, fmt.Errorf("ejecutar comando local: %w", err)
	}
	return output, nil
}

type commandRequest struct {
	// Command coincide con la propiedad JSON enviada por la interfaz web.
	Command       string `json:"command"`
	ExecutionMode string `json:"executionMode"`
	SSHHost       string `json:"sshHost"`
	SSHPort       string `json:"sshPort"`
	SSHUser       string `json:"sshUser"`
}

type commandResponse struct {
	// Output contiene la salida estandar y de error del comando remoto.
	Output string `json:"output,omitempty"`
	// Error se informa cuando falla la validacion, SSH o la ejecucion.
	Error string `json:"error,omitempty"`
}

func NewCommandHandler(executor CommandExecutor, cfg *config.Config) *CommandHandler {
	// Inyecta la dependencia para separar el protocolo HTTP de la conexion SSH.
	// La configuracion se usa para validacion y limites de seguridad.
	return &CommandHandler{executor: executor, config: cfg}
}

func (handler *CommandHandler) ServeHTTP(response http.ResponseWriter, request *http.Request) {
	// El endpoint solo acepta solicitudes POST con un cuerpo JSON.
	if request.Method != http.MethodPost {
		writeError(response, http.StatusMethodNotAllowed, "método no permitido")
		return
	}

	// Limita el cuerpo segun la configuracion para evitar solicitudes JSON excesivamente grandes.
	request.Body = http.MaxBytesReader(response, request.Body, int64(handler.config.RequestMaxSize))
	var payload commandRequest
	if err := json.NewDecoder(request.Body).Decode(&payload); err != nil {
		writeError(response, http.StatusBadRequest, "JSON inválido")
		return
	}

	// La validacion ocurre antes de abrir la conexion SSH y es la frontera
	// que evita que el usuario ejecute comandos arbitrarios en la VM.
	command, err := validateCommand(payload.Command, handler.config)
	if err != nil {
		writeError(response, http.StatusForbidden, err.Error())
		return
	}

	// La configuracion enviada por el formulario tiene prioridad para que el
	// destino cambie inmediatamente, sin depender del estado global anterior.
	executor := handler.executor
	mode := strings.TrimSpace(payload.ExecutionMode)
	if mode == "" {
		mode = handler.config.ExecutionMode
	}
	if strings.EqualFold(mode, "local") {
		executor = LocalExecutor{}
	} else if payload.SSHHost != "" || payload.SSHPort != "" || payload.SSHUser != "" {
		executor = ConfiguredSSHExecutor{Config: commandConfig(handler.config, payload)}
	}

	output, err := executor.ExecuteCommand(command)
	if err != nil {
		writeJSON(response, http.StatusBadGateway, commandResponse{Output: string(output), Error: err.Error()})
		return
	}
	writeJSON(response, http.StatusOK, commandResponse{Output: string(output)})
}

func commandConfig(base *config.Config, payload commandRequest) *config.Config {
	current := &config.Config{
		SSHHost:           base.SSHHost,
		SSHPort:           base.SSHPort,
		SSHUser:           base.SSHUser,
		SSHKeyPath:        base.SSHKeyPath,
		SSHKnownHostsPath: base.SSHKnownHostsPath,
	}
	if payload.SSHHost != "" {
		current.SSHHost = strings.TrimSpace(payload.SSHHost)
	}
	if payload.SSHPort != "" {
		current.SSHPort = strings.TrimSpace(payload.SSHPort)
	}
	if payload.SSHUser != "" {
		current.SSHUser = strings.TrimSpace(payload.SSHUser)
	}
	return current
}

// validateCommand normaliza el texto y rechaza sintaxis que podria encadenar
// comandos o redirigir entrada y salida del proceso remoto.
// Usa la lista de comandos permitidos de la configuracion.
func validateCommand(command string, cfg *config.Config) (string, error) {
	command = strings.TrimSpace(command)
	if command == "" {
		return "", errors.New("el comando no puede estar vacío")
	}
	if strings.ContainsAny(command, "&|;><`$()\r\n") {
		return "", errors.New("comando no permitido")
	}
	// Se valida el primer token contra la configuracion de comandos permitidos.
	parts := strings.Fields(command)
	if len(parts) == 0 {
		return "", errors.New("comando no permitido")
	}

	// Verifica si el comando esta en la lista blanca de la configuracion.
	if !cfg.IsCommandAllowed(command) {
		return "", errors.New("comando no permitido")
	}
	return command, nil
}

// writeError mantiene el formato JSON de los errores HTTP.
func writeError(response http.ResponseWriter, status int, message string) {
	writeJSON(response, status, commandResponse{Error: message})
}

// writeJSON centraliza las cabeceras y la serializacion de todas las respuestas.
func writeJSON(response http.ResponseWriter, status int, payload commandResponse) {
	response.Header().Set("Content-Type", "application/json")
	response.WriteHeader(status)
	_ = json.NewEncoder(response).Encode(payload)
}
