package handlers

import (
	"bytes"
	"os/exec"
	"regexp"
	"strconv"
	"unicode/utf8"

	"golang.org/x/text/encoding"
	"golang.org/x/text/encoding/charmap"
)

var codePageDigits = regexp.MustCompile(`(\d+)\D*$`)

// activeOEMCodePage consulta la pagina de codigos activa de la consola con
// "chcp" (el mismo mecanismo que ya usa el resto de la app para ejecutar
// comandos, sin llamadas directas a la API de Windows). No hace falta
// decodificar el mensaje completo de chcp para leer el numero: solo se busca
// la secuencia final de digitos, que es ASCII en cualquier pagina de
// codigos. Devuelve 0 si no se puede determinar.
func activeOEMCodePage() int {
	output, err := exec.Command("cmd", "/C", "chcp").CombinedOutput()
	if err != nil {
		return 0
	}
	match := codePageDigits.FindSubmatch(bytes.TrimSpace(output))
	if match == nil {
		return 0
	}
	cp, err := strconv.Atoi(string(match[1]))
	if err != nil {
		return 0
	}
	return cp
}

// decodeOEM convierte bytes escritos en la pagina de codigos activa de la
// consola (la que usan cmd.exe y sus comandos internos como "dir") a UTF-8.
// Se consulta la pagina de codigos real en vez de forzarla con chcp antes
// del comando: ese forzado depende de si el proceso hijo hereda o no la
// consola del padre (varia segun como se arranco la aplicacion) y no es
// confiable. Si la pagina de codigos no esta en la tabla conocida (incluye
// 65001=UTF-8, donde no hay nada que convertir) o la conversion falla, se
// devuelven los bytes originales sin tocar en vez de perder la salida.
func decodeOEM(b []byte) []byte {
	// Si la salida ya es UTF-8 valido (p. ej. Windows con la opcion "Beta:
	// UTF-8" activa, o consola heredada en 65001) no se toca: reinterpretarla
	// como CP850 convierte "ú" en "├║" y el espacio angosto de "p. m." en "Ô Ç»".
	// Un texto CP850 con acentos casi nunca es UTF-8 valido, y el ASCII puro
	// es identico en ambas codificaciones.
	if utf8.Valid(b) {
		return b
	}
	return decodeCodePage(b, activeOEMCodePage())
}

// decodeCodePage hace la conversion real dado un numero de pagina de codigos
// ya conocido; separada de decodeOEM para poder probarla sin depender de
// invocar "chcp".
func decodeCodePage(b []byte, codePage int) []byte {
	var enc encoding.Encoding
	switch codePage {
	case 437:
		enc = charmap.CodePage437
	case 850:
		enc = charmap.CodePage850
	case 852:
		enc = charmap.CodePage852
	case 855:
		enc = charmap.CodePage855
	case 860:
		enc = charmap.CodePage860
	case 862:
		enc = charmap.CodePage862
	case 863:
		enc = charmap.CodePage863
	case 865:
		enc = charmap.CodePage865
	case 866:
		enc = charmap.CodePage866
	case 1047:
		enc = charmap.CodePage1047
	case 1140:
		enc = charmap.CodePage1140
	default:
		return b
	}

	decoded, err := enc.NewDecoder().Bytes(b)
	if err != nil {
		return b
	}
	return decoded
}
