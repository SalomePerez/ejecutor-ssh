package vm

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"
	"testing"
	"time"
)

// fakeAgent simula el hipervisor: los comandos virsh normales responden por
// substring (como fakeSSHRunner) y los de qemu-agent-command ejecutan una
// funcion que recibe el script que la app mando a la VM.
type fakeAgent struct {
	calls   []string
	scripts []string
	host    []commandMatcher
	guest   func(script string) (string, int)
	results map[int][2]interface{}
	nextPID int
}

func newFakeAgent(guest func(string) (string, int)) *fakeAgent {
	return &fakeAgent{guest: guest, results: map[int][2]interface{}{}, nextPID: 100}
}

func (f *fakeAgent) on(contains, output string, err error) {
	f.host = append(f.host, commandMatcher{contains: contains, resp: cmdResponse{output: []byte(output), err: err}})
}

func (f *fakeAgent) ExecuteCommand(command string) ([]byte, error) {
	f.calls = append(f.calls, command)
	if strings.Contains(command, "qemu-agent-command") {
		k := strings.Index(command, " '{")
		payload := strings.ReplaceAll(strings.Trim(command[k+1:], "'"), `'\''`, `'`)
		var req struct {
			Execute   string `json:"execute"`
			Arguments struct {
				Arg []string `json:"arg"`
				PID int      `json:"pid"`
			} `json:"arguments"`
		}
		if err := json.Unmarshal([]byte(payload), &req); err != nil {
			return nil, fmt.Errorf("payload inválido: %v: %s", err, payload)
		}
		if req.Execute == "guest-exec" {
			script := strings.TrimPrefix(req.Arguments.Arg[1], guestPath)
			f.scripts = append(f.scripts, script)
			out, code := f.guest(script)
			f.nextPID++
			f.results[f.nextPID] = [2]interface{}{out, code}
			return []byte(fmt.Sprintf(`{"return":{"pid":%d}}`, f.nextPID)), nil
		}
		r := f.results[req.Arguments.PID]
		enc := base64.StdEncoding.EncodeToString([]byte(r[0].(string)))
		return []byte(fmt.Sprintf(`{"return":{"exited":true,"exitcode":%d,"out-data":"%s"}}`, r[1].(int), enc)), nil
	}
	for _, m := range f.host {
		if strings.Contains(command, m.contains) {
			return m.resp.output, m.resp.err
		}
	}
	return nil, nil
}

func (f *fakeAgent) ran(substr string) bool {
	for _, s := range append(append([]string{}, f.scripts...), f.calls...) {
		if strings.Contains(s, substr) {
			return true
		}
	}
	return false
}

// Salida real de una VM Ubuntu 24.04 creada por la app (KVM, red "default"),
// con Docker instalado para que aparezca una interfaz virtual.
const ubuntuDetect = `@@@ grep -E '^(PRETTY_NAME|ID|ID_LIKE)=' /etc/os-release
PRETTY_NAME="Ubuntu 24.04.1 LTS"
ID=ubuntu
ID_LIKE=debian
@@@ systemctl is-active networking NetworkManager systemd-networkd
inactive
inactive
active
@@@ ls /etc/netplan
50-cloud-init.yaml
@@@ ls /etc/network/interfaces.d
ls: cannot access '/etc/network/interfaces.d': No such file or directory
@@@ ip -br link
lo               UNKNOWN        00:00:00:00:00:00 <LOOPBACK,UP,LOWER_UP>
enp1s0           UP             52:54:00:07:00:11 <BROADCAST,MULTICAST,UP,LOWER_UP>
docker0          DOWN           02:42:ac:11:00:01 <NO-CARRIER,BROADCAST,MULTICAST,UP>
@@@ ls -l /sys/class/net
total 0
lrwxrwxrwx 1 root root 0 Oct 20 10:00 docker0 -> ../../devices/virtual/net/docker0
lrwxrwxrwx 1 root root 0 Oct 20 10:00 enp1s0 -> ../../devices/pci0000:00/0000:00:02.0/0000:01:00.0/virtio0/net/enp1s0
lrwxrwxrwx 1 root root 0 Oct 20 10:00 lo -> ../../devices/virtual/net/lo
@@@ ls -d /sys/class/net/*/wireless
ls: cannot access '/sys/class/net/*/wireless': No such file or directory
@@@ ip -4 -o addr show scope global
2: enp1s0    inet 192.168.122.45/24 metric 100 brd 192.168.122.255 scope global dynamic enp1s0\       valid_lft 3388sec preferred_lft 3388sec
3: docker0    inet 172.17.0.1/16 brd 172.17.255.255 scope global docker0\       valid_lft forever preferred_lft forever
@@@ ip r
default via 192.168.122.1 dev enp1s0 proto dhcp src 192.168.122.45 metric 100
172.17.0.0/16 dev docker0 proto kernel scope link src 172.17.0.1 linkdown
192.168.122.0/24 dev enp1s0 proto kernel scope link src 192.168.122.45 metric 100
192.168.122.1 dev enp1s0 proto dhcp scope link src 192.168.122.45 metric 100
@@@ sysctl net.ipv4.ip_forward
net.ipv4.ip_forward = 1
@@@ cat /etc/resolv.conf
nameserver 127.0.0.53
options edns0 trust-ad
@@@ test -L /etc/resolv.conf && echo enlace
enlace
@@@ resolvectl dns
Global:
Link 2 (enp1s0): 192.168.122.1
Link 3 (docker0):
@@@ nmcli -t -f DEVICE,NAME con show --active
sh: 1: nmcli: not found
@@@ command -v arping
/usr/sbin/arping
@@@ test -d /etc/cloud && echo cloud-init
cloud-init`

const domiflist = ` Interface   Type      Source    Model    MAC
------------------------------------------------------------
 vnet0       network   default   virtio   52:54:00:07:00:11
`

const netDumpXML = `<network><name>default</name><forward mode='nat'/><ip address='192.168.122.1' netmask='255.255.255.0'><dhcp><range start='192.168.122.2' end='192.168.122.254'/><host mac='52:54:00:99:99:99' ip='192.168.122.70'/></dhcp></ip></network>`

const netLeases = ` Expiry Time           MAC address         Protocol   IP address          Hostname   Client ID or DUID
-----------------------------------------------------------------------------------------------------------
 2026-10-20 11:00:00   52:54:00:07:00:11   ipv4       192.168.122.45/24   lab-01     ff:00
 2026-10-20 11:00:00   52:54:00:aa:bb:cc   ipv4       192.168.122.60/24   lab-02     ff:01
`

func ubuntuState(t *testing.T) *NetState {
	t.Helper()
	st := ParseNetState(splitSections(ubuntuDetect))
	st.Libvirt = map[string]*LibvirtNet{"52:54:00:07:00:11": {
		Network: "default", HostIP: "192.168.122.1", Prefix: 24, DHCPStart: "192.168.122.2", DHCPEnd: "192.168.122.254",
		Leases:   map[string]string{"192.168.122.45": "52:54:00:07:00:11", "192.168.122.60": "52:54:00:aa:bb:cc"},
		Reserved: map[string]string{"192.168.122.70": "52:54:00:99:99:99"},
	}}
	return st
}

func staticReq(ip string) NetRequest {
	return NetRequest{Interface: "enp1s0", Mode: "static", IP: ip, Mask: "255.255.255.0", Gateway: "192.168.122.1", Role: "main", DNS: "192.168.122.1"}
}

func hasCheck(plan *NetPlan, level, substr string) bool {
	for _, c := range plan.Checks {
		if c.Level == level && strings.Contains(c.Text, substr) {
			return true
		}
	}
	return false
}

func fileBody(plan *NetPlan, path string) string {
	for _, f := range plan.Files {
		if f.Path == path {
			return fileText(f)
		}
	}
	return ""
}

// ---------- deteccion ----------

func TestParseNetState_UbuntuKVM(t *testing.T) {
	st := ParseNetState(splitSections(ubuntuDetect))
	if st.Manager != "netplan" || st.Family != "ubuntu" || st.OS != "Ubuntu 24.04.1 LTS" {
		t.Fatalf("sistema mal detectado: %+v", st)
	}
	if !st.CloudInit || !st.HasArping || !st.ResolvLink || !st.Forwarding {
		t.Fatalf("banderas mal detectadas: %+v", st)
	}
	if len(st.Interfaces) != 2 {
		t.Fatalf("esperaba enp1s0 y docker0, hay %d", len(st.Interfaces))
	}
	e := st.Iface("enp1s0")
	if e.Virtual || e.Mode != "dhcp" || e.IP != "192.168.122.45" || e.Prefix != 24 || e.Role != "main" || e.Gateway != "192.168.122.1" || e.Metric != 100 || e.DNS != "192.168.122.1" {
		t.Fatalf("enp1s0 mal interpretada: %+v", e)
	}
	if d := st.Iface("docker0"); !d.Virtual || d.Mode != "static" || d.Role != "none" {
		t.Fatalf("docker0 debería ser virtual y sin ruta: %+v", d)
	}
}

func TestParseNetState_DebianIfupdownTwoCards(t *testing.T) {
	raw := []RawCommand{
		{Cmd: cmdOS, Output: "PRETTY_NAME=\"Debian GNU/Linux 12 (bookworm)\"\nID=debian"},
		{Cmd: cmdActive, Output: "active\ninactive\ninactive"},
		{Cmd: cmdNetplan, Output: "ls: cannot access '/etc/netplan': No such file or directory"},
		{Cmd: cmdIfaceD, Output: "50-cloud-init"},
		{Cmd: cmdLink, Output: "lo UNKNOWN 00:00:00:00:00:00 <LOOPBACK>\nenp1s0 UP 52:54:00:00:00:01 <UP>\nenp7s0 UP 52:54:00:00:00:02 <UP>"},
		{Cmd: cmdAddr, Output: "2: enp1s0    inet 192.168.122.20/24 brd 192.168.122.255 scope global dynamic enp1s0\n3: enp7s0    inet 10.0.1.1/24 brd 10.0.1.255 scope global enp7s0"},
		{Cmd: cmdRoute, Output: "default via 192.168.122.1 dev enp1s0\n10.0.3.0/24 via 10.0.1.254 dev enp7s0"},
		{Cmd: cmdResolv, Output: "nameserver 192.168.122.1"},
	}
	st := ParseNetState(raw)
	if st.Manager != "ifupdown" || st.Family != "debian" || len(st.IfupdownD) != 1 {
		t.Fatalf("debian mal detectado: %+v", st)
	}
	if a := st.Iface("enp1s0"); a.Role != "main" || a.DNS != "192.168.122.1" {
		t.Fatalf("enp1s0: %+v", a)
	}
	if b := st.Iface("enp7s0"); b.Role != "routes" || b.Gateway != "10.0.1.254" || b.Mode != "static" || len(b.Nets) != 1 {
		t.Fatalf("enp7s0: %+v", b)
	}
}

func TestReadNetwork_ReadsLibvirtNetwork(t *testing.T) {
	fake := newFakeAgent(func(string) (string, int) { return ubuntuDetect, 0 })
	fake.on("domiflist", domiflist, nil)
	fake.on("net-dumpxml 'default'", netDumpXML, nil)
	fake.on("net-dhcp-leases 'default'", netLeases, nil)
	p := NewLibvirtProvider(fake)
	p.sleep = func(time.Duration) {}

	st, err := p.ReadNetwork("lab-01")
	if err != nil {
		t.Fatalf("error inesperado: %v", err)
	}
	ln := st.Libvirt["52:54:00:07:00:11"]
	if ln == nil || ln.HostIP != "192.168.122.1" || ln.Prefix != 24 || ln.DHCPStart != "192.168.122.2" || ln.Leases["192.168.122.60"] != "52:54:00:aa:bb:cc" || ln.Reserved["192.168.122.70"] == "" {
		t.Fatalf("red de libvirt mal leída: %+v", ln)
	}
	if len(st.Raw) != len(detectCommands) {
		t.Fatalf("esperaba %d comandos crudos, hay %d", len(detectCommands), len(st.Raw))
	}
}

func TestGuestExec_AgentMissing(t *testing.T) {
	fake := newFakeSSHRunner()
	fake.on("qemu-agent-command", "error: Guest agent is not responding", fmt.Errorf("exit 1"))
	_, err := NewLibvirtProvider(fake).GuestExec("lab-01", "true", time.Second)
	if err == nil || !strings.Contains(err.Error(), "qemu-guest-agent") {
		t.Fatalf("esperaba un error que nombre al agente, fue %v", err)
	}
}

// ---------- validacion y plan ----------

func TestPlan_StaticNetplan(t *testing.T) {
	plan := PlanNetwork(ubuntuState(t), staticReq("192.168.122.50"), 120)
	if !plan.Valid {
		t.Fatalf("debería ser válido: %+v", plan.Checks)
	}
	body := fileBody(plan, "/etc/netplan/90-ejecutor-enp1s0.yaml")
	for _, want := range []string{"dhcp4: false", "- 192.168.122.50/24", "- to: default", "via: 192.168.122.1", "metric: 100", "addresses: [192.168.122.1]"} {
		if !strings.Contains(body, want) {
			t.Fatalf("falta %q en el YAML:\n%s", want, body)
		}
	}
	if fileBody(plan, cloudDisablePath) == "" {
		t.Fatalf("con cloud-init debe escribir %s", cloudDisablePath)
	}
	if !hasCheck(plan, "warn", "rango que reparte el DHCP") {
		t.Fatalf("debe avisar que la IP está en el rango DHCP: %+v", plan.Checks)
	}
	last := plan.Phases[len(plan.Phases)-1]
	if last.Name != "Cerrar" || len(last.Cmds) != 3 || !last.Cmds[2].Host || !strings.Contains(last.Cmds[2].run, "add ip-dhcp-host") {
		t.Fatalf("debe reservar la IP en libvirt al cerrar: %+v", last)
	}
	if plan.RouteSummary != "Una sola ruta por defecto, por enp1s0." {
		t.Fatalf("resumen de rutas: %q", plan.RouteSummary)
	}
}

func TestPlan_Rejections(t *testing.T) {
	cases := []struct {
		name string
		req  NetRequest
		want string
	}{
		{"ip de otra vm", staticReq("192.168.122.60"), "prestó a la tarjeta 52:54:00:aa:bb:cc"},
		{"ip reservada", staticReq("192.168.122.70"), "reservada en libvirt"},
		{"ip del hipervisor", staticReq("192.168.122.1"), "es la del hipervisor"},
		{"broadcast", staticReq("192.168.122.255"), "broadcast"},
		{"dirección de red", staticReq("192.168.122.0"), "dirección de red"},
		{"ip inválida", staticReq("192.168.122.300"), "no es una IP"},
		{"interfaz virtual", NetRequest{Interface: "docker0", Mode: "static", IP: "172.17.0.2", Mask: "/16", Role: "none"}, "interfaz virtual"},
		{"interfaz inexistente", NetRequest{Interface: "eth9", Mode: "dhcp"}, "no existe"},
	}
	for _, c := range cases {
		plan := PlanNetwork(ubuntuState(t), c.req, 120)
		if plan.Valid || !hasCheck(plan, "bad", c.want) {
			t.Errorf("%s: esperaba rechazo con %q, checks: %+v", c.name, c.want, plan.Checks)
		}
	}
	req := staticReq("192.168.122.50")
	req.Gateway = "10.0.0.1"
	if plan := PlanNetwork(ubuntuState(t), req, 120); plan.Valid || !hasCheck(plan, "bad", "no está en 192.168.122.0/24") {
		t.Errorf("puerta de enlace fuera de la subred debe rechazarse: %+v", plan.Checks)
	}
	req = staticReq("192.168.10.23")
	req.Gateway = "192.168.10.1"
	if plan := PlanNetwork(ubuntuState(t), req, 120); !hasCheck(plan, "warn", "como pasó con 192.168.10.23") {
		t.Errorf("otra red debe advertir: %+v", plan.Checks)
	}
}

func TestPlan_RolesAndRoutes(t *testing.T) {
	req := staticReq("192.168.122.50")
	req.Role, req.Nets = "routes", "10.0.3.0/24, 10.0.4.5/24"
	if plan := PlanNetwork(ubuntuState(t), req, 120); plan.Valid || !hasCheck(plan, "bad", "sería 10.0.4.0/24") {
		t.Fatalf("red mal alineada debe rechazarse: %+v", plan.Checks)
	}
	req.Nets = "10.0.3.0/24"
	plan := PlanNetwork(ubuntuState(t), req, 120)
	if !plan.Valid {
		t.Fatalf("rutas estáticas deberían ser válidas: %+v", plan.Checks)
	}
	body := fileBody(plan, "/etc/netplan/90-ejecutor-enp1s0.yaml")
	if !strings.Contains(body, "- to: 10.0.3.0/24") || strings.Contains(body, "to: default") {
		t.Fatalf("solo debe haber la ruta a 10.0.3.0/24:\n%s", body)
	}
	if !strings.Contains(plan.RouteSummary, "Sin ruta por defecto") {
		t.Fatalf("sin ruta por defecto: %q", plan.RouteSummary)
	}

	req = staticReq("192.168.122.50")
	req.Role = "none"
	plan = PlanNetwork(ubuntuState(t), req, 120)
	if !hasCheck(plan, "warn", "única ruta por defecto") || !hasCheck(plan, "ok", "reenvía paquetes") {
		t.Fatalf("sin puerta de enlace debe advertir y explicar el reenvío: %+v", plan.Checks)
	}
}

func TestPlan_NothingToChange(t *testing.T) {
	plan := PlanNetwork(ubuntuState(t), NetRequest{Interface: "enp1s0", Mode: "dhcp"}, 120)
	if plan.Valid || plan.Changed || !hasCheck(plan, "info", "ya está en DHCP") {
		t.Fatalf("DHCP sobre DHCP no debe cambiar nada: %+v", plan)
	}
}

func TestPlan_ExtraAddress(t *testing.T) {
	plan := PlanNetwork(ubuntuState(t), NetRequest{Interface: "enp1s0", Mode: "extra", IP: "192.168.122.51", Mask: "/24"}, 120)
	if !plan.Valid {
		t.Fatalf("IP adicional válida: %+v", plan.Checks)
	}
	body := fileBody(plan, "/etc/netplan/90-ejecutor-enp1s0.yaml")
	if !strings.Contains(body, "dhcp4: true") || !strings.Contains(body, "- 192.168.122.51/24") {
		t.Fatalf("debe conservar DHCP y sumar la dirección:\n%s", body)
	}
	if PlanNetwork(ubuntuState(t), NetRequest{Interface: "enp1s0", Mode: "extra", IP: "192.168.122.45", Mask: "/24"}, 120).Valid {
		t.Fatalf("la IP actual no puede agregarse como adicional")
	}
}

// En ifupdown con DHCP, dhclient-script borra "label enp1s0" cuando vence el
// lease: cada IP adicional necesita su propia etiqueta para sobrevivir.
func TestPlan_ExtraAddressIfupdownGetsOwnLabel(t *testing.T) {
	raw := []RawCommand{
		{Cmd: cmdOS, Output: "PRETTY_NAME=\"Debian GNU/Linux 12 (bookworm)\"\nID=debian"},
		{Cmd: cmdActive, Output: "active\ninactive\ninactive"},
		{Cmd: cmdNetplan, Output: "ls: cannot access '/etc/netplan': No such file or directory"},
		{Cmd: cmdLink, Output: "lo UNKNOWN 00:00:00:00:00:00 <LOOPBACK>\nenp1s0 UP 52:54:00:00:00:01 <UP>"},
		{Cmd: cmdAddr, Output: "2: enp1s0    inet 192.168.122.112/24 brd 192.168.122.255 scope global dynamic enp1s0\n2: enp1s0    inet 192.168.122.18/24 scope global secondary enp1s0:1"},
		{Cmd: cmdRoute, Output: "default via 192.168.122.1 dev enp1s0"},
		{Cmd: cmdResolv, Output: "nameserver 192.168.122.1"},
	}
	st := ParseNetState(raw)
	if i := st.Iface("enp1s0"); len(i.Extras) != 1 || i.Extras[0] != "192.168.122.18/24" {
		t.Fatalf("la IP con etiqueta enp1s0:1 es una adicional de enp1s0: %+v", i)
	}
	plan := PlanNetwork(st, NetRequest{Interface: "enp1s0", Mode: "extra", IP: "192.168.122.19", Mask: "/24"}, 120)
	if !plan.Valid {
		t.Fatalf("IP adicional válida: %+v", plan.Checks)
	}
	body := fileBody(plan, "/etc/network/interfaces")
	for _, want := range []string{
		"up sysctl -qw net.ipv4.conf.enp1s0.promote_secondaries=1",
		"up ip addr add 192.168.122.18/24 dev enp1s0 label enp1s0:1",
		"up ip addr add 192.168.122.19/24 dev enp1s0 label enp1s0:2",
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("falta %q en:\n%s", want, body)
		}
	}
	live := false
	for _, ph := range plan.Phases {
		for _, c := range ph.Cmds {
			if c.Cmd == "ip addr add 192.168.122.19/24 dev enp1s0 label enp1s0:2" {
				live = true
			}
		}
	}
	if !live {
		t.Fatalf("la IP en vivo debe llevar la misma etiqueta que el archivo: %+v", plan.Phases)
	}
	if extraLabel("enx00112233445566", 1) != "" {
		t.Fatal("una etiqueta de más de 15 caracteres no se usa")
	}
}

func TestPlan_RevertSecondsClamped(t *testing.T) {
	for in, want := range map[int]int{0: 120, 5: 30, 90: 90, 9999: 600} {
		plan := PlanNetwork(ubuntuState(t), staticReq("192.168.122.50"), in)
		if plan.RevertSeconds != want || !strings.Contains(plan.Phases[1].Cmds[1].run, fmt.Sprintf("--on-active=%d ", want)) {
			t.Errorf("revert %d: esperaba %d, fue %d", in, want, plan.RevertSeconds)
		}
	}
}

func TestPlan_NetworkManagerUsesNmcli(t *testing.T) {
	st := ubuntuState(t)
	st.Manager, st.Interfaces[0].Conn = "nm", "System enp1s0"
	plan := PlanNetwork(st, staticReq("192.168.122.50"), 120)
	write := plan.Phases[2].Cmds[0].Cmd
	if !strings.Contains(write, "nmcli con mod 'System enp1s0' ipv4.method manual ipv4.addresses '192.168.122.50/24'") || !strings.Contains(write, "ipv4.route-metric 100") {
		t.Fatalf("comando nmcli inesperado: %s", write)
	}
	if !strings.Contains(plan.Phases[1].Cmds[1].run, "ipv4.method auto") {
		t.Fatalf("la reversión debe volver a DHCP: %s", plan.Phases[1].Cmds[1].run)
	}
}

// ---------- aplicar ----------

// vmAfter responde como la VM despues de aplicar 192.168.122.50 estatica.
func vmAfter(pingCode int) func(string) (string, int) {
	return func(script string) (string, int) {
		switch {
		case strings.Contains(script, "@@@ "):
			return ubuntuDetect, 0
		case strings.HasPrefix(script, "arping"):
			return "Sent 2 probes\nReceived 0 response(s)", 0
		case strings.HasPrefix(script, "ip -4 -o addr show dev enp1s0"):
			return "2: enp1s0    inet 192.168.122.50/24 brd 192.168.122.255 scope global enp1s0", 0
		case strings.HasPrefix(script, "ip r show default"):
			return "default via 192.168.122.1 dev enp1s0 proto static metric 100", 0
		case strings.HasPrefix(script, "ping"):
			return "2 packets transmitted", pingCode
		}
		return "", 0
	}
}

func newApplyProvider(guest func(string) (string, int)) (*LibvirtProvider, *fakeAgent) {
	fake := newFakeAgent(guest)
	fake.on("domiflist", domiflist, nil)
	fake.on("net-dumpxml 'default'", netDumpXML, nil)
	fake.on("net-dhcp-leases 'default'", netLeases, nil)
	p := NewLibvirtProvider(fake)
	p.sleep = func(time.Duration) {}
	return p, fake
}

func TestApplyNetwork_Success(t *testing.T) {
	p, fake := newApplyProvider(vmAfter(0))
	res, err := p.ApplyNetwork("lab-01", staticReq("192.168.122.50"), 120)
	if err != nil {
		t.Fatalf("error inesperado: %v", err)
	}
	if !res.OK || res.Reverted {
		t.Fatalf("debería aplicarse sin revertir: %+v", res)
	}
	for _, want := range []string{"arping -D", "systemd-run --unit=ejecutor-revertir-red --on-active=120", "netplan generate", "netplan apply", "systemctl stop ejecutor-revertir-red.timer", "add ip-dhcp-host"} {
		if !fake.ran(want) {
			t.Errorf("no se ejecutó %q", want)
		}
	}
	if fake.ran("systemctl start ejecutor-revertir-red.service") {
		t.Fatalf("no debe revertir si todo respondió")
	}
	if len(res.Steps) != 6 || res.State == nil {
		t.Fatalf("esperaba 6 fases y el estado releído: %+v", res)
	}
}

func TestApplyNetwork_VerifyFailsReverts(t *testing.T) {
	p, fake := newApplyProvider(vmAfter(1))
	res, err := p.ApplyNetwork("lab-01", staticReq("192.168.122.50"), 120)
	if err != nil {
		t.Fatalf("error inesperado: %v", err)
	}
	if res.OK || !res.Reverted || !fake.ran("systemctl start ejecutor-revertir-red.service") {
		t.Fatalf("debe disparar la reversión: %+v", res)
	}
	if fake.ran("add ip-dhcp-host") {
		t.Fatalf("no debe reservar la IP si falló")
	}
	if res.Steps[len(res.Steps)-1].Name != "Revertir" {
		t.Fatalf("la última fase debe ser Revertir: %+v", res.Steps)
	}
}

func TestApplyNetwork_DuplicateIPStopsBeforeChanging(t *testing.T) {
	p, fake := newApplyProvider(func(script string) (string, int) {
		if strings.HasPrefix(script, "arping") {
			return "Unicast reply from 192.168.122.50 [52:54:00:12:34:56]\nReceived 1 response(s)", 1
		}
		return vmAfter(0)(script)
	})
	res, err := p.ApplyNetwork("lab-01", staticReq("192.168.122.50"), 120)
	if err != nil {
		t.Fatalf("error inesperado: %v", err)
	}
	if res.OK || res.Reverted || !strings.Contains(res.Message, "ya responde") {
		t.Fatalf("debe detenerse en Comprobar: %+v", res)
	}
	if fake.ran("systemd-run") || fake.ran("netplan apply") {
		t.Fatalf("no debe tocar la VM si la IP está ocupada")
	}
}

func TestApplyNetwork_InvalidRequestIsRejected(t *testing.T) {
	p, fake := newApplyProvider(vmAfter(0))
	_, err := p.ApplyNetwork("lab-01", staticReq("192.168.122.60"), 120)
	if _, ok := err.(*PlanError); !ok {
		t.Fatalf("esperaba PlanError, fue %v", err)
	}
	if fake.ran("systemd-run") {
		t.Fatalf("no debe ejecutar nada si la validación falla")
	}
}

// El script de deteccion debe ser sh valido: una comilla mal anidada hacia
// que la VM respondiera "Syntax error" y el panel de Red quedara vacio.
func TestDetectScript_IsValidShell(t *testing.T) {
	sh, err := exec.LookPath("sh")
	if err != nil {
		t.Skip("sh no disponible")
	}
	if out, err := exec.Command(sh, "-n", "-c", guestPath+detectScript()).CombinedOutput(); err != nil {
		t.Fatalf("sintaxis invalida: %v: %s", err, out)
	}
	out, err := exec.Command(sh, "-c", "echo "+quoteShell("@@@ "+cmdOS)).Output()
	if err != nil || strings.TrimSpace(string(out)) != "@@@ "+cmdOS {
		t.Fatalf("la marca no se imprime literal: %q %v", out, err)
	}
}
