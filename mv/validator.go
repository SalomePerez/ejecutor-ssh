package vm

import (
	"fmt"
	"regexp"
	"strings"
)

func ValidateName(name string) error {
	if strings.TrimSpace(name) == "" {
		return fmt.Errorf("nombre vacío")
	}
	ok, _ := regexp.MatchString(`^[a-zA-Z0-9-]+$`, name)
	if !ok {
		return fmt.Errorf("solo letras, números y guiones")
	}
	return nil
}

func ValidateDistro(distro string) error {
	switch distro {
	case "ubuntu-24.04", "debian-12", "debian-13", "fedora-42", "fedora-45", "arch", "rocky-9":
		return nil
	default:
		return fmt.Errorf("distribución no soportada")
	}
}
