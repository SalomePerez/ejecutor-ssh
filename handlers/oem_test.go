package handlers

import (
	"testing"

	"golang.org/x/text/encoding/charmap"
)

func TestDecodeCodePage_KnownCodePage(t *testing.T) {
	original := "El número de serie del volumen es: 4C1C-42AD"
	encoded, err := charmap.CodePage850.NewEncoder().Bytes([]byte(original))
	if err != nil {
		t.Fatalf("encode fixture: %v", err)
	}

	got := decodeCodePage(encoded, 850)
	if string(got) != original {
		t.Fatalf("expected %q, got %q", original, string(got))
	}
}

func TestDecodeCodePage_UnknownOrUTF8_PassesThrough(t *testing.T) {
	input := []byte("ya en utf-8: número")
	if got := decodeCodePage(input, 65001); string(got) != string(input) {
		t.Fatalf("expected passthrough for utf-8 codepage, got %q", got)
	}
	if got := decodeCodePage(input, 0); string(got) != string(input) {
		t.Fatalf("expected passthrough when codepage is unknown (0), got %q", got)
	}
}

func TestDecodeOEM_ValidUTF8_PassesThrough(t *testing.T) {
	input := []byte("El número de serie  27/09/2026 11:37 p. m.")
	if got := decodeOEM(input); string(got) != string(input) {
		t.Fatalf("expected utf-8 passthrough, got %q", got)
	}
}
