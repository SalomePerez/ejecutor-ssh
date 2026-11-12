package sshkeys

import (
	"crypto/rand"
	"encoding/pem"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/crypto/ed25519"
	"golang.org/x/crypto/ssh"
)

// KeyPair representa un par de llaves SSH (privada y pública).
type KeyPair struct {
	// PrivateKey es la llave privada en formato PEM.
	PrivateKey []byte
	// PublicKey es la llave pública en formato autorizado.
	PublicKey []byte
	// Algorithm es el algoritmo usado (ej: "ssh-ed25519").
	Algorithm string
}

// GenerateED25519 genera un nuevo par de llaves ED25519.
// Este es el algoritmo más seguro y moderno para SSH.
func GenerateED25519() (*KeyPair, error) {
	// Genera la llave privada usando ED25519.
	publicKey, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return nil, fmt.Errorf("generar llave ed25519: %w", err)
	}

	// Convierte la llave privada a formato PEM para SSH.
	privateBlock, err := ssh.MarshalPrivateKey(privateKey, "")
	if err != nil {
		return nil, fmt.Errorf("marshaling llave privada: %w", err)
	}

	// Codifica el bloque PEM a bytes.
	privateKeyPEM := pem.EncodeToMemory(privateBlock)

	// Convierte la llave pública a formato autorizado.
	publicKeySigner, err := ssh.NewPublicKey(publicKey)
	if err != nil {
		return nil, fmt.Errorf("crear public key signer: %w", err)
	}

	// Formato autorizado: "ssh-ed25519 AAAA... usuario@servidor"
	authorizedKey := ssh.MarshalAuthorizedKey(publicKeySigner)

	return &KeyPair{
		PrivateKey: privateKeyPEM,
		PublicKey:  authorizedKey,
		Algorithm:  "ssh-ed25519",
	}, nil
}

// SaveToFiles guarda el par de llaves en archivos separados.
// Returns: (private_key_path, public_key_path, error)
func (kp *KeyPair) SaveToFiles(dirPath, baseName string) (string, string, error) {
	// Crea el directorio si no existe.
	if err := os.MkdirAll(dirPath, 0700); err != nil {
		return "", "", fmt.Errorf("crear directorio: %w", err)
	}

	privatePath := filepath.Join(dirPath, baseName)
	publicPath := filepath.Join(dirPath, baseName+".pub")

	// Guarda la llave privada (permisos restrictivos: solo lectura del propietario).
	if err := os.WriteFile(privatePath, kp.PrivateKey, 0600); err != nil {
		return "", "", fmt.Errorf("guardar llave privada: %w", err)
	}

	// Guarda la llave pública.
	if err := os.WriteFile(publicPath, kp.PublicKey, 0644); err != nil {
		return "", "", fmt.Errorf("guardar llave pública: %w", err)
	}

	return privatePath, publicPath, nil
}

// ReadPrivateKey lee una llave privada desde un archivo.
func ReadPrivateKey(filePath string) ([]byte, error) {
	data, err := os.ReadFile(filePath)
	if err != nil {
		return nil, fmt.Errorf("leer llave privada: %w", err)
	}
	return data, nil
}

// ReadPublicKey lee una llave pública desde un archivo.
func ReadPublicKey(filePath string) ([]byte, error) {
	data, err := os.ReadFile(filePath)
	if err != nil {
		return nil, fmt.Errorf("leer llave pública: %w", err)
	}
	return data, nil
}

// DeleteKeyPair elimina ambos archivos de llave.
func DeleteKeyPair(dirPath, baseName string) error {
	privatePath := filepath.Join(dirPath, baseName)
	publicPath := filepath.Join(dirPath, baseName+".pub")

	// Intenta eliminar la llave privada.
	if err := os.Remove(privatePath); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("eliminar llave privada: %w", err)
	}

	// Intenta eliminar la llave pública.
	if err := os.Remove(publicPath); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("eliminar llave pública: %w", err)
	}

	return nil
}

// ListKeys lista todas las llaves en un directorio.
func ListKeys(dirPath string) ([]string, error) {
	var keys []string

	entries, err := os.ReadDir(dirPath)
	if err != nil {
		if os.IsNotExist(err) {
			return keys, nil // Directorio no existe, lista vacía.
		}
		return nil, fmt.Errorf("leer directorio: %w", err)
	}

	// Busca archivos sin extensión (las llaves privadas).
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := entry.Name()
		// Ignora archivos .pub, solo devuelve el nombre base.
		if !strings.HasSuffix(name, ".pub") {
			keys = append(keys, name)
		}
	}

	return keys, nil
}
