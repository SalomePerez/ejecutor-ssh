package users

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"runtime"
)

// User representa un usuario local del sistema.
type User struct {
	// Username es el nombre del usuario.
	Username string
	// Password es la contraseña del usuario.
	Password string
	// FullName es el nombre completo del usuario (opcional).
	FullName string
	// IsSSHUser indica si este usuario puede conectar por SSH.
	IsSSHUser bool
}

// Create crea un nuevo usuario local en Windows.
// Solo funciona en Windows con permisos de administrador.
func (u *User) Create() error {
	// Verifica que estemos en Windows.
	if runtime.GOOS != "windows" {
		return fmt.Errorf("crear usuarios solo está soportado en Windows")
	}

	// Verifica primero si el usuario ya existe
	exists, err := Exists(u.Username)
	if err != nil {
		return fmt.Errorf("error verificando si usuario existe: %w", err)
	}
	if exists {
		return fmt.Errorf("el usuario '%s' ya existe en el sistema", u.Username)
	}

	// Construye el comando NET USER para crear el usuario.
	// Formato: net user <username> <password> /add /active:yes
	cmd := exec.Command(
		"net",
		"user",
		u.Username,
		u.Password,
		"/add",
		"/active:yes",
	)

	// Si se proporciona nombre completo, lo agregamos.
	if u.FullName != "" {
		cmd.Args = append(cmd.Args, "/comment:"+u.FullName)
	}

	// Ejecuta el comando (requiere permisos de administrador).
	if err := cmd.Run(); err != nil {
		// Verifica si es un error de permisos
		if exitErr, ok := err.(*exec.ExitError); ok {
			switch exitErr.ExitCode() {
			case 2:
				return fmt.Errorf("el usuario '%s' ya existe o no tienes permisos de administrador", u.Username)
			case 1:
				return fmt.Errorf("error: verifica la contraseña (puede tener caracteres inválidos o ser muy corta)")
			default:
				return fmt.Errorf("error creando usuario (código %d): puede ser falta de permisos de administrador o contraseña inválida", exitErr.ExitCode())
			}
		}
		return fmt.Errorf("error creando usuario '%s': %w", u.Username, err)
	}

	return nil
}

// Delete elimina un usuario local del sistema.
// Solo funciona en Windows con permisos de administrador.
func (u *User) Delete() error {
	// Verifica que estemos en Windows.
	if runtime.GOOS != "windows" {
		return fmt.Errorf("eliminar usuarios solo está soportado en Windows")
	}

	// Construye el comando NET USER para eliminar el usuario.
	cmd := exec.Command("net", "user", u.Username, "/delete")

	// Ejecuta el comando.
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("eliminar usuario '%s': %w", u.Username, err)
	}

	return nil
}

// ChangePassword cambia la contraseña de un usuario.
func (u *User) ChangePassword(newPassword string) error {
	// Verifica que estemos en Windows.
	if runtime.GOOS != "windows" {
		return fmt.Errorf("cambiar contraseña solo está soportado en Windows")
	}

	// Construye el comando NET USER para cambiar contraseña.
	cmd := exec.Command("net", "user", u.Username, newPassword)

	// Ejecuta el comando.
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("cambiar contraseña para '%s': %w", u.Username, err)
	}

	u.Password = newPassword
	return nil
}

// AddToGroup agrega el usuario a un grupo local (por ejemplo, "Administrators").
func (u *User) AddToGroup(groupName string) error {
	// Verifica que estemos en Windows.
	if runtime.GOOS != "windows" {
		return fmt.Errorf("agregar a grupo solo está soportado en Windows")
	}

	// Construye el comando LOCALGROUP para agregar el usuario.
	cmd := exec.Command(
		"net",
		"localgroup",
		groupName,
		u.Username,
		"/add",
	)

	// Ejecuta el comando.
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("agregar '%s' al grupo '%s': %w", u.Username, groupName, err)
	}

	return nil
}

// Exists verifica si un usuario existe en el sistema.
func Exists(username string) (bool, error) {
	// Valida que el nombre no esté vacío
	if username == "" {
		return false, fmt.Errorf("nombre de usuario no puede estar vacío")
	}

	// En Windows, usamos `net user` para verificar existencia.
	cmd := exec.Command("net", "user", username)

	// Si el comando sale sin error, el usuario existe.
	err := cmd.Run()
	if err == nil {
		return true, nil
	}

	// En Windows, si el usuario no existe, sale con un código de error específico.
	if exitErr, ok := err.(*exec.ExitError); ok {
		// Código de salida 2 significa usuario no encontrado.
		// Ver: https://docs.microsoft.com/en-us/windows-server/administration/windows-commands/net-user
		if exitErr.ExitCode() == 2 {
			return false, nil
		}
		// Otros códigos de error (1, 5, etc.) indican problemas del sistema
		return false, fmt.Errorf("error del sistema verificando usuario '%s' (código: %d)", username, exitErr.ExitCode())
	}

	return false, fmt.Errorf("error verificando usuario '%s': %w", username, err)
}

// LocalUser es una cuenta local tal como la reporta Windows.
type LocalUser struct {
	Name        string `json:"name"`
	FullName    string `json:"fullName"`
	Description string `json:"description"`
	Enabled     bool   `json:"enabled"`
}

// List lista las cuentas locales con Get-LocalUser. No requiere permisos de
// administrador (solo crear y eliminar los requieren).
func List() ([]LocalUser, error) {
	if runtime.GOOS != "windows" {
		return nil, fmt.Errorf("listar usuarios solo está soportado en Windows")
	}
	script := "[Console]::OutputEncoding=[Text.Encoding]::UTF8; @(Get-LocalUser | Select-Object Name,FullName,Description,Enabled) | ConvertTo-Json -Compress"
	output, err := exec.Command("powershell", "-NoProfile", "-NonInteractive", "-Command", script).Output()
	if err != nil {
		return nil, fmt.Errorf("listar usuarios con Get-LocalUser: %w", err)
	}
	var raw []struct {
		Name        string
		FullName    string
		Description string
		Enabled     bool
	}
	if err := json.Unmarshal(bytes.TrimSpace(output), &raw); err != nil {
		return nil, fmt.Errorf("leer la lista de usuarios: %w", err)
	}
	list := make([]LocalUser, 0, len(raw))
	for _, u := range raw {
		list = append(list, LocalUser{Name: u.Name, FullName: u.FullName, Description: u.Description, Enabled: u.Enabled})
	}
	return list, nil
}

// GetHomeDirectory retorna el directorio home del usuario.
// En Windows, esto típicamente es C:\Users\<username>
func GetHomeDirectory(username string) (string, error) {
	// Búsqueda simple en C:\Users\
	userDir := fmt.Sprintf("C:\\Users\\%s", username)

	// Verifica que el directorio exista.
	if _, err := os.Stat(userDir); err != nil {
		if os.IsNotExist(err) {
			return "", fmt.Errorf("directorio de usuario no encontrado: %s", userDir)
		}
		return "", err
	}

	return userDir, nil
}

// SetupSSHFolder crea la estructura de carpetas necesaria para SSH.
// Crea: C:\Users\<username>\.ssh con los permisos correctos.
func SetupSSHFolder(username string) (string, error) {
	// Obtiene el directorio home del usuario.
	homeDir, err := GetHomeDirectory(username)
	if err != nil {
		return "", err
	}

	// Crea la carpeta .ssh si no existe.
	sshDir := fmt.Sprintf("%s\\.ssh", homeDir)
	if err := os.MkdirAll(sshDir, 0700); err != nil {
		return "", fmt.Errorf("crear carpeta .ssh: %w", err)
	}

	return sshDir, nil
}
