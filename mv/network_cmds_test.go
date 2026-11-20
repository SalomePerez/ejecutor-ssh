package vm

import (
	"strings"
	"testing"
	"time"
)

// scriptBackend responde a GuestExec con salidas fijas y guarda los scripts.
type scriptBackend struct {
	scripts []string
	respond func(script string) (string, int)
}

func (b *scriptBackend) GuestExec(name, script string, timeout time.Duration) (*GuestExecResult, error) {
	b.scripts = append(b.scripts, script)
	out, code := "", 0
	if b.respond != nil {
		out, code = b.respond(script)
	}
	return &GuestExecResult{Stdout: out, ExitCode: code}, nil
}
func (b *scriptBackend) runHost(string) ([]byte, error)             { return nil, nil }
func (b *scriptBackend) hostNetworks(string) map[string]*LibvirtNet { return nil }
func (b *scriptBackend) wait(time.Duration)                         {}
func (b *scriptBackend) hypervisorName() string                     { return "VirtualBox" }

func TestNetCommandRoute(t *testing.T) {
	b := &scriptBackend{respond: func(string) (string, int) { return "default via 10.0.2.2 dev enp0s3", 0 }}
	res, err := runNetCommand(b, "lab-a", NetCmdRequest{Cmd: "route"})
	if err != nil || !res.OK {
		t.Fatalf("route: %v %+v", err, res)
	}
	if len(b.scripts) != 1 || !strings.Contains(b.scripts[0], "command -v ip") || !strings.HasSuffix(b.scripts[0], "ip route") {
		t.Fatalf("script inesperado: %q", b.scripts)
	}
	if got := res.Steps[0].Lines[0]; got.Cmd != "ip route" || !strings.Contains(got.Output, "10.0.2.2") {
		t.Fatalf("linea: %+v", got)
	}
}

func TestNetCommandMissingTool(t *testing.T) {
	b := &scriptBackend{respond: func(string) (string, int) { return missingMarker + "\n", 127 }}
	res, err := runNetCommand(b, "lab-a", NetCmdRequest{Cmd: "netstat"})
	if err != nil {
		t.Fatal(err)
	}
	if !res.OK || res.Steps[0].Status != "warn" || !strings.Contains(res.Message, "net-tools") {
		t.Fatalf("debe advertir que falta net-tools: %+v", res)
	}
}

func TestNetCommandPingFails(t *testing.T) {
	b := &scriptBackend{respond: func(s string) (string, int) {
		if strings.Contains(s, "ping") {
			return "100% packet loss", 1
		}
		return "", 0
	}}
	res, err := runNetCommand(b, "lab-a", NetCmdRequest{Cmd: "ping", Target: "192.168.20.10"})
	if err != nil {
		t.Fatal(err)
	}
	if res.OK || !strings.Contains(res.Message, "NO llega") {
		t.Fatalf("el ping fallido debe reportarse: %+v", res)
	}
}

func TestNetCommandTcpdump(t *testing.T) {
	b := &scriptBackend{}
	res, err := runNetCommand(b, "lab-a", NetCmdRequest{Cmd: "tcpdump", Iface: "enp0s8", Filter: "icmp"})
	if err != nil || !res.OK {
		t.Fatalf("tcpdump: %v %+v", err, res)
	}
	if got := res.Steps[0].Lines[0].Cmd; got != "tcpdump -i enp0s8 -n -l -c 10 'icmp'" {
		t.Fatalf("comando: %q", got)
	}
	if !strings.Contains(b.scripts[0], "timeout 15 tcpdump") {
		t.Fatalf("la captura debe tener limite de tiempo: %q", b.scripts[0])
	}
}

func TestNetCommandRejects(t *testing.T) {
	cases := []NetCmdRequest{
		{Cmd: "rm"},
		{Cmd: "ping", Target: "1.1.1.1; reboot"},
		{Cmd: "ping"},
		{Cmd: "tcpdump", Iface: "enp0s3;id"},
		{Cmd: "tcpdump", Iface: "enp0s3", Count: 500},
		{Cmd: "tcpdump", Iface: "enp0s3", Filter: "host 1.1.1.1"},
	}
	for _, c := range cases {
		b := &scriptBackend{}
		if _, err := runNetCommand(b, "lab-a", c); err == nil {
			t.Errorf("%+v debio rechazarse", c)
		}
		if len(b.scripts) != 0 {
			t.Errorf("%+v no debio ejecutar nada", c)
		}
	}
}

func TestNeighHint(t *testing.T) {
	cases := []struct {
		out, want string
	}{
		{"192.168.20.1 dev enp0s9 FAILED \n", "192.168.20.1 (por enp0s9)"},
		{"10.0.2.2 dev enp0s3 lladdr 52:54:00:12:35:00 STALE\n192.168.10.1 dev enp0s8 INCOMPLETE\nfe80::2 dev enp0s3 FAILED\n", "192.168.10.1 (por enp0s8)"},
		{"? (192.168.20.1) at <incomplete> on enp0s9\n", "192.168.20.1 (por enp0s9)"},
		{"", "vacía"},
		{"10.0.2.2 dev enp0s3 lladdr 52:54:00:12:35:00 REACHABLE\n", ""},
	}
	for _, c := range cases {
		got := neighHint([]ApplyLine{{Output: c.out}})
		if c.want == "" && got != "" || c.want != "" && !strings.Contains(got, c.want) {
			t.Errorf("%q: %q (se esperaba %q)", c.out, got, c.want)
		}
	}
	b := &scriptBackend{respond: func(string) (string, int) { return "192.168.20.1 dev enp0s9 FAILED", 0 }}
	res, err := runNetCommand(b, "lab-a", NetCmdRequest{Cmd: "neigh"})
	if err != nil || !res.OK || res.Steps[0].Status != "warn" || !strings.Contains(res.Message, "ARP") {
		t.Fatalf("neigh con FAILED: %v %+v", err, res)
	}
}
