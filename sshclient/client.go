package sshclient

import (
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"time"

	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/knownhosts"
)

// expandPath expande ~ a la carpeta del usuario y resuelve rutas relativas
func expandPath(path string) (string, error) {
	// Si comienza con ~, reemplaza con la carpeta del usuario
	if strings.HasPrefix(path, "~") {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", fmt.Errorf("no se puede obtener carpeta de usuario: %w", err)
		}
		return filepath.Join(home, strings.TrimPrefix(path, "~/")), nil
	}

	// Si es una ruta relativa, resuelve desde el directorio actual
	if !filepath.IsAbs(path) {
		absPath, err := filepath.Abs(path)
		if err != nil {
			return "", fmt.Errorf("no se puede resolver ruta relativa: %w", err)
		}
		return absPath, nil
	}

	// Si es una ruta absoluta, devuelve como está
	return path, nil
}

type Config struct {
	// Datos necesarios para localizar el servidor y autenticar la conexion.
	Host           string
	Port           string
	User           string
	KeyPath        string
	KnownHostsPath string
}

type Client struct {
	// La configuracion se conserva para reutilizar el cliente en varias solicitudes.
	config Config
}

func NewClient(config Config) *Client {
	// El constructor no abre conexiones: la conexion se crea por cada comando.
	return &Client{config: config}
}

func (client *Client) ExecuteCommand(command string) ([]byte, error) {
	// Expande la ruta de la llave (puede ser relativa, ~/, o absoluta)
	keyPath, err := expandPath(client.config.KeyPath)
	if err != nil {
		return nil, fmt.Errorf("expandir ruta de llave: %w", err)
	}

	// Lee la llave cada vez para evitar mantener material sensible abierto
	// durante toda la vida del proceso.
	privateKey, err := os.ReadFile(keyPath)
	if err != nil {
		return nil, fmt.Errorf("leer llave privada en '%s': %w", keyPath, err)
	}

	signer, err := ssh.ParsePrivateKey(privateKey)
	if err != nil {
		return nil, fmt.Errorf("parsear llave privada: %w", err)
	}

	if client.config.KnownHostsPath == "" {
		return nil, fmt.Errorf("SSH_KNOWN_HOSTS no está configurado")
	}

	// Expande la ruta de known_hosts
	knownHostsPath, err := expandPath(client.config.KnownHostsPath)
	if err != nil {
		return nil, fmt.Errorf("expandir ruta de known_hosts: %w", err)
	}

	// knownhosts verifica la identidad de la VM y evita aceptar cualquier llave.
	hostKeyCallback, err := knownhosts.New(knownHostsPath)
	if err != nil {
		return nil, fmt.Errorf("leer hosts conocidos en '%s': %w", knownHostsPath, err)
	}

	address := net.JoinHostPort(client.config.Host, client.config.Port)
	sshConfig := &ssh.ClientConfig{
		User:            client.config.User,
		Auth:            []ssh.AuthMethod{ssh.PublicKeys(signer)},
		HostKeyCallback: hostKeyCallback,
		// Pide solo los tipos de llave que known_hosts conoce para este host.
		// Sin esto el servidor puede presentar ECDSA cuando solo se confio en
		// su llave ED25519, y la conexion falla con "key mismatch".
		HostKeyAlgorithms: knownHostAlgorithms(hostKeyCallback, address),
		Timeout:           10 * time.Second,
	}

	// Abre una conexion TCP/SSH usando autenticacion por llave publica.
	connection, err := ssh.Dial("tcp", address, sshConfig)
	if err != nil {
		return nil, fmt.Errorf("conectar por SSH a %s:%s: %w", client.config.Host, client.config.Port, err)
	}
	defer connection.Close()

	// Una sesion representa la ejecucion de un comando dentro de la conexion SSH.
	session, err := connection.NewSession()
	if err != nil {
		return nil, fmt.Errorf("crear sesión SSH: %w", err)
	}
	defer session.Close()

	// CombinedOutput junta stdout y stderr para mostrar al usuario una respuesta unica.
	output, err := session.CombinedOutput(command)
	if err != nil {
		return output, fmt.Errorf("ejecutar comando remoto: %w", err)
	}
	return output, nil
}

// probeKey es una llave ficticia: al pasarla al callback de known_hosts, este
// responde con las llaves que si conoce para el host.
type probeKey struct{}

func (probeKey) Type() string                        { return "probe" }
func (probeKey) Marshal() []byte                     { return []byte("probe") }
func (probeKey) Verify([]byte, *ssh.Signature) error { return errors.New("probe") }

// knownHostAlgorithms lista los algoritmos de llave de host aceptables segun
// known_hosts. Devuelve nil (algoritmos por defecto) si el host no aparece.
func knownHostAlgorithms(callback ssh.HostKeyCallback, address string) []string {
	remote, err := net.ResolveTCPAddr("tcp", address)
	if err != nil {
		remote = &net.TCPAddr{}
	}
	var keyErr *knownhosts.KeyError
	if !errors.As(callback(address, remote, probeKey{}), &keyErr) {
		return nil
	}
	var algorithms []string
	seen := map[string]bool{}
	for _, known := range keyErr.Want {
		keyType := known.Key.Type()
		names := []string{keyType}
		if keyType == ssh.KeyAlgoRSA {
			names = []string{ssh.KeyAlgoRSASHA512, ssh.KeyAlgoRSASHA256, ssh.KeyAlgoRSA}
		}
		for _, name := range names {
			if !seen[name] {
				seen[name] = true
				algorithms = append(algorithms, name)
			}
		}
	}
	return algorithms
}
