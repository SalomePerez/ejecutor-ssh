package vm

import (
	"strings"
	"testing"
)

func TestParseRoutes(t *testing.T) {
	out := `default via 192.168.1.1 dev enp0s3 proto dhcp src 192.168.1.8 metric 100
default via 10.0.2.2 dev enp0s8 proto static metric 600
10.0.2.0/24 dev enp0s8 proto kernel scope link src 10.0.2.15
192.168.20.0/24 via 192.168.10.1 dev enp0s9 proto static
unreachable 10.9.0.0/16
`
	routes, summary, warning := ParseRoutes(out)
	if len(routes) != 5 {
		t.Fatalf("5 rutas: %+v", routes)
	}
	want := []struct{ dest, kind, via, dev string }{
		{"default", "default", "192.168.1.1", "enp0s3"},
		{"default", "default", "10.0.2.2", "enp0s8"},
		{"10.0.2.0/24", "connected", "", "enp0s8"},
		{"192.168.20.0/24", "via", "192.168.10.1", "enp0s9"},
		{"10.9.0.0/16", "special", "", ""},
	}
	for k, w := range want {
		r := routes[k]
		if r.Dest != w.dest || r.Kind != w.kind || r.Via != w.via || r.Dev != w.dev {
			t.Errorf("ruta %d: %+v, se esperaba %+v", k, r, w)
		}
	}
	if !routes[0].Used || routes[1].Used {
		t.Error("gana la ruta por defecto de menor métrica (100)")
	}
	if routes[0].Metric != 100 || routes[0].Proto != "dhcp" || routes[0].Src != "192.168.1.8" {
		t.Errorf("campos: %+v", routes[0])
	}
	if !strings.Contains(summary, "2 rutas por defecto") || !strings.Contains(summary, "enp0s3") || warning != "" {
		t.Errorf("resumen: %q / %q", summary, warning)
	}
}

func TestParseRoutesTieAndNone(t *testing.T) {
	_, _, warning := ParseRoutes("default via 10.0.0.1 dev a\ndefault via 10.0.1.1 dev b\n")
	if !strings.Contains(warning, "misma métrica") {
		t.Errorf("dos rutas por defecto con la misma métrica deben advertirse: %q", warning)
	}
	_, summary, _ := ParseRoutes("10.0.2.0/24 dev enp0s3 proto kernel scope link src 10.0.2.15\n")
	if !strings.Contains(summary, "Sin ruta por defecto") {
		t.Errorf("sin ruta por defecto: %q", summary)
	}
}

func TestReadRouting(t *testing.T) {
	b := &scriptBackend{respond: func(string) (string, int) {
		return "default via 10.0.2.2 dev enp0s3 proto dhcp metric 100\n" + routingSep + "\n1\n", 0
	}}
	info, err := readRouting(b, "lab-a")
	if err != nil {
		t.Fatal(err)
	}
	if !info.Forwarding || len(info.Routes) != 1 || !info.Routes[0].Used {
		t.Fatalf("info: %+v", info)
	}
}
