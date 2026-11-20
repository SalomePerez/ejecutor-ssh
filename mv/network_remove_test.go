package vm

import (
	"strings"
	"testing"
)

// allCmds junta lo que se ve y lo que se ejecuta de cada comando del plan.
func allCmds(plan *NetPlan) string {
	var b strings.Builder
	for _, ph := range plan.Phases {
		for _, c := range ph.Cmds {
			b.WriteString(c.Cmd + " | " + c.run + "\n")
		}
	}
	return b.String()
}

func withExtras(t *testing.T) *NetState {
	st := ubuntuState(t)
	st.Iface("enp1s0").Extras = []string{"192.168.122.51/24", "192.168.122.52/24"}
	return st
}

func TestPlan_RemoveExtraNetplan(t *testing.T) {
	plan := PlanNetwork(withExtras(t), NetRequest{Interface: "enp1s0", Mode: "remove", IP: "192.168.122.51"}, 120)
	if !plan.Valid {
		t.Fatalf("quitar una adicional existente es válido: %+v", plan.Checks)
	}
	body := fileBody(plan, "/etc/netplan/90-ejecutor-enp1s0.yaml")
	if strings.Contains(body, "192.168.122.51") || !strings.Contains(body, "- 192.168.122.52/24") || !strings.Contains(body, "dhcp4: true") {
		t.Fatalf("el archivo debe conservar DHCP y la otra adicional, sin la quitada:\n%s", body)
	}
	cmds := allCmds(plan)
	for _, want := range []string{"netplan apply", "ip addr del 192.168.122.51/24 dev enp1s0", "! ip -4 -o addr show dev enp1s0 | grep -q 'inet 192.168.122.51/'"} {
		if !strings.Contains(cmds, want) {
			t.Errorf("falta %q en el plan:\n%s", want, cmds)
		}
	}
	if strings.Contains(cmds, "arping") {
		t.Errorf("quitar no debe buscar duplicados:\n%s", cmds)
	}
	if plan.Config.Removed != "192.168.122.51/24" || len(plan.Config.Extras) != 1 {
		t.Fatalf("config: %+v", plan.Config)
	}
}

func TestPlan_RemoveExtraRejects(t *testing.T) {
	for _, c := range []struct{ name, ip string }{
		{"la principal", "192.168.122.45"},
		{"una que no está", "192.168.122.99"},
		{"vacía", ""},
	} {
		if PlanNetwork(withExtras(t), NetRequest{Interface: "enp1s0", Mode: "remove", IP: c.ip}, 120).Valid {
			t.Errorf("%s (%q) debe rechazarse", c.name, c.ip)
		}
	}
	if PlanNetwork(ubuntuState(t), NetRequest{Interface: "enp1s0", Mode: "remove", IP: "192.168.122.51"}, 120).Valid {
		t.Error("sin adicionales no hay nada que quitar")
	}
}

func TestPlan_RemoveExtraIfupdownDoesNotBringIfaceDown(t *testing.T) {
	raw := []RawCommand{
		{Cmd: cmdOS, Output: "PRETTY_NAME=\"Debian GNU/Linux 12 (bookworm)\"\nID=debian"},
		{Cmd: cmdActive, Output: "active\ninactive\ninactive"},
		{Cmd: cmdNetplan, Output: "ls: cannot access '/etc/netplan': No such file or directory"},
		{Cmd: cmdLink, Output: "lo UNKNOWN 00:00:00:00:00:00 <LOOPBACK>\nenp1s0 UP 52:54:00:00:00:01 <UP>"},
		{Cmd: cmdAddr, Output: "2: enp1s0    inet 192.168.122.112/24 brd 192.168.122.255 scope global dynamic enp1s0\n2: enp1s0    inet 192.168.122.18/24 scope global secondary enp1s0:1"},
		{Cmd: cmdRoute, Output: "default via 192.168.122.1 dev enp1s0"},
		{Cmd: cmdResolv, Output: "nameserver 192.168.122.1"},
	}
	plan := PlanNetwork(ParseNetState(raw), NetRequest{Interface: "enp1s0", Mode: "remove", IP: "192.168.122.18/24"}, 120)
	if !plan.Valid {
		t.Fatalf("válido: %+v", plan.Checks)
	}
	if body := fileBody(plan, "/etc/network/interfaces"); strings.Contains(body, "192.168.122.18") {
		t.Fatalf("la IP quitada no debe quedar en el archivo:\n%s", body)
	}
	cmds := allCmds(plan)
	// La reversion programada (fase Respaldar) si baja y sube la interfaz,
	// pero solo corre si algo falla; el camino normal no debe hacerlo.
	for _, ph := range plan.Phases {
		if ph.kind == "backup" {
			continue
		}
		for _, c := range ph.Cmds {
			if strings.Contains(c.Cmd+c.run, "ifdown enp1s0") || strings.Contains(c.Cmd+c.run, "ifup enp1s0") {
				t.Fatalf("quitar una adicional no debe bajar la interfaz (fase %s): %s", ph.Name, c.Cmd)
			}
		}
	}
	if !strings.Contains(cmds, "ip addr del 192.168.122.18/24 dev enp1s0") {
		t.Fatalf("ifupdown no la borra en vivo: la app debe hacerlo:\n%s", cmds)
	}
}

func TestPlan_RemoveExtraNetworkManager(t *testing.T) {
	st := withExtras(t)
	st.Manager, st.Interfaces[0].Conn = "nm", "System enp1s0"
	plan := PlanNetwork(st, NetRequest{Interface: "enp1s0", Mode: "remove", IP: "192.168.122.52"}, 120)
	if !plan.Valid {
		t.Fatalf("válido: %+v", plan.Checks)
	}
	cmds := allCmds(plan)
	if !strings.Contains(cmds, "ipv4.addresses '192.168.122.51/24'") || strings.Contains(cmds, "ipv4.addresses '192.168.122.51/24,192.168.122.52/24'") {
		t.Fatalf("nmcli debe quedar solo con la adicional que sigue:\n%s", cmds)
	}
}
