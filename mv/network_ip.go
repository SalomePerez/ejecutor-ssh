package vm

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

// Utilidades IPv4 con enteros de 32 bits: la misma aritmetica del prototipo
// (red = ip AND mascara, broadcast = red OR NOT mascara).

func ipToU32(s string) (uint32, bool) {
	parts := strings.Split(strings.TrimSpace(s), ".")
	if len(parts) != 4 {
		return 0, false
	}
	var n uint32
	for _, part := range parts {
		if part == "" || len(part) > 3 {
			return 0, false
		}
		v, err := strconv.Atoi(part)
		if err != nil || v < 0 || v > 255 {
			return 0, false
		}
		n = n<<8 | uint32(v)
	}
	return n, true
}

func u32ToIP(n uint32) string {
	return fmt.Sprintf("%d.%d.%d.%d", n>>24, (n>>16)&255, (n>>8)&255, n&255)
}

func maskBits(p int) uint32 {
	if p <= 0 {
		return 0
	}
	return ^uint32(0) << (32 - p)
}

func netOf(n uint32, p int) uint32 { return n & maskBits(p) }
func bcOf(n uint32, p int) uint32  { return netOf(n, p) | ^maskBits(p) }

// maskToPrefix convierte 255.255.255.0 en 24; falla si los unos no son contiguos.
func maskToPrefix(mask string) (int, bool) {
	n, ok := ipToU32(mask)
	if !ok {
		return 0, false
	}
	inv := ^n
	if (inv+1)&inv != 0 {
		return 0, false
	}
	bits := 0
	for i := 31; i >= 0 && (n>>uint(i))&1 == 1; i-- {
		bits++
	}
	return bits, true
}

// parseMask acepta las dos notaciones: "/24", "24" o "255.255.255.0".
func parseMask(s string) (int, bool) {
	s = strings.TrimSpace(s)
	if m := regexp.MustCompile(`^/?(\d{1,2})$`).FindStringSubmatch(s); m != nil {
		p, _ := strconv.Atoi(m[1])
		return p, p <= 32
	}
	return maskToPrefix(s)
}

func ipInRange(ip, start, end string) bool {
	n, ok1 := ipToU32(ip)
	a, ok2 := ipToU32(start)
	b, ok3 := ipToU32(end)
	return ok1 && ok2 && ok3 && n >= a && n <= b
}

var ifaceNameRe = regexp.MustCompile(`^[A-Za-z0-9_.:-]{1,15}$`)
