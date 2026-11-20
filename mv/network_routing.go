package vm

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

// ---------- Enrutamiento: tabla de rutas real y reenvio IPv4 ----------
// El modulo «Enrutamiento y diagnóstico» muestra la tabla de rutas que tiene
// HOY la VM (ip -4 route), interpretada linea por linea, y el estado del
// reenvio IPv4. Es solo lectura: activar o desactivar el reenvio va por
// SetForwarding.

// RouteEntry es una linea de "ip -4 route", interpretada.
type RouteEntry struct {
	Raw    string `json:"raw"`
	Dest   string `json:"dest"`           // "default" o una red (10.0.2.0/24)
	Kind   string `json:"kind"`           // default | connected | via | special
	Type   string `json:"type,omitempty"` // unreachable, blackhole… (Kind special)
	Via    string `json:"via,omitempty"`
	Dev    string `json:"dev,omitempty"`
	Proto  string `json:"proto,omitempty"` // kernel, dhcp, static, boot…
	Src    string `json:"src,omitempty"`
	Metric int    `json:"metric"`
	Used   bool   `json:"used,omitempty"` // la ruta por defecto que gana
}

// RoutingInfo es la respuesta del modulo de enrutamiento.
type RoutingInfo struct {
	Routes     []RouteEntry `json:"routes"`
	Summary    string       `json:"summary"`
	Warning    string       `json:"warning,omitempty"`
	Forwarding bool         `json:"forwarding"`
	ReadAt     time.Time    `json:"readAt"`
}

// RoutingProvider lo implementan los canales que pueden leer el enrutamiento.
type RoutingProvider interface {
	ReadRouting(name string) (*RoutingInfo, error)
}

const routingSep = "__EJECUTOR_FWD__"

// ParseRoutes interpreta la salida de "ip -4 route" y marca la ruta por
// defecto que usa el kernel (la de menor metrica).
func ParseRoutes(out string) ([]RouteEntry, string, string) {
	var routes []RouteEntry
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		f := strings.Fields(line)
		r := RouteEntry{Raw: line}
		k := 0
		switch f[0] {
		case "unreachable", "blackhole", "prohibit", "throw", "local", "broadcast", "multicast", "nat":
			r.Type, k = f[0], 1
		}
		if k < len(f) {
			r.Dest = f[k]
		}
		for j := k + 1; j+1 < len(f); j++ {
			switch f[j] {
			case "via":
				r.Via = f[j+1]
			case "dev":
				r.Dev = f[j+1]
			case "proto":
				r.Proto = f[j+1]
			case "src":
				r.Src = f[j+1]
			case "metric":
				r.Metric, _ = strconv.Atoi(f[j+1])
			}
		}
		switch {
		case r.Type != "":
			r.Kind = "special"
		case r.Dest == "default" || r.Dest == "0.0.0.0/0":
			r.Dest, r.Kind = "default", "default"
		case r.Via == "":
			r.Kind = "connected"
		default:
			r.Kind = "via"
		}
		routes = append(routes, r)
	}

	win, defs, ties := -1, 0, 0
	for k, r := range routes {
		if r.Kind != "default" {
			continue
		}
		defs++
		if win == -1 || r.Metric < routes[win].Metric {
			win, ties = k, 0
		} else if r.Metric == routes[win].Metric {
			ties++
		}
	}
	summary, warning := "", ""
	switch defs {
	case 0:
		summary = "Sin ruta por defecto: la VM solo alcanza las redes de esta tabla."
	case 1:
		summary = "Una sola ruta por defecto: lo que no es de una red de la tabla sale por " + describeDefault(routes[win]) + "."
	default:
		summary = fmt.Sprintf("%d rutas por defecto. Gana la de menor métrica: sale por %s; las demás quedan de respaldo.", defs, describeDefault(routes[win]))
	}
	if win >= 0 {
		routes[win].Used = true
	}
	if ties > 0 {
		warning = fmt.Sprintf("Hay rutas por defecto con la misma métrica (%d): el kernel usa la primera y el resultado puede cambiar al reiniciar. Dé a cada una una métrica distinta.", routes[win].Metric)
	}
	return routes, summary, warning
}

func describeDefault(r RouteEntry) string {
	s := r.Dev
	if r.Via != "" {
		s += " (puerta de enlace " + r.Via + ")"
	}
	return s
}

func readRouting(p netBackend, name string) (*RoutingInfo, error) {
	if err := ValidateName(name); err != nil {
		return nil, err
	}
	res, err := p.GuestExec(name, "ip -4 route show; echo "+routingSep+"; cat /proc/sys/net/ipv4/ip_forward", guestStepTimeout)
	if err != nil {
		return nil, err
	}
	out := res.Output()
	routesOut, fwd, _ := strings.Cut(out, routingSep)
	info := &RoutingInfo{Forwarding: strings.TrimSpace(fwd) == "1", ReadAt: time.Now()}
	info.Routes, info.Summary, info.Warning = ParseRoutes(routesOut)
	if info.Routes == nil {
		info.Routes = []RouteEntry{}
	}
	return info, nil
}

func (p *LibvirtProvider) ReadRouting(name string) (*RoutingInfo, error) {
	return readRouting(p, name)
}

func (p *VBoxProvider) ReadRouting(name string) (*RoutingInfo, error) {
	return readRouting(p, name)
}

func (o mgmtOps) ReadRouting(name string) (*RoutingInfo, error) {
	b, err := o.backend(name)
	if err != nil {
		return nil, err
	}
	defer b.Close()
	return readRouting(b, name)
}
