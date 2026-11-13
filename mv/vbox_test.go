package vm

import (
	"fmt"
	"strings"
	"testing"
)

// fakeVBox responde a VBoxManage segun el primer argumento y registra las llamadas.
type fakeVBox struct {
	responses map[string]string
	calls     [][]string
}

func (f *fakeVBox) Run(args ...string) ([]byte, error) {
	f.calls = append(f.calls, args)
	key := strings.Join(args, " ")
	// Gana el prefijo mas largo: el orden de un mapa es aleatorio y una
	// respuesta generica no debe tapar a una especifica.
	best, found := "", false
	for prefix := range f.responses {
		if strings.HasPrefix(key, prefix) && (!found || len(prefix) > len(best)) {
			best, found = prefix, true
		}
	}
	if found {
		return []byte(f.responses[best]), nil
	}
	return nil, fmt.Errorf("sin respuesta para %q", key)
}

const debianInfo = `name="debian-red"
description="distro=debian-13"
memory=2048
cpus=2
VMState="running"
storagecontrollername0="IDE"
"IDE-0-0"="C:\Program Files\Oracle\VirtualBox\VBoxGuestAdditions.iso"
"IDE-ImageUUID-0-0"="aaaa"
"SATA-0-0"="C:\VMs\debian-red\debian-red.vdi"
"SATA-ImageUUID-0-0"="bbbb"
"SATA-1-0"="C:\VMs\compartido.vdi"
"SATA-ImageUUID-1-0"="cccc"
"SATA-hot-pluggable-0-0"="off"
nested-hw-virt="off"
nic1="bridged"
bridgeadapter1="Intel Wi-Fi"
macaddress1="080027BD31FD"
nictype1="82540EM"
nic2="intnet"
intnet2="lan-a"
macaddress2="080027000002"
nic3="none"
`

func TestParseMachineReadableAndNics(t *testing.T) {
	values := parseMachineReadable(debianInfo)
	if values["SATA-0-0"] != `C:\VMs\debian-red\debian-red.vdi` {
		t.Fatalf("ruta mal leida: %q", values["SATA-0-0"])
	}
	disks := attachedDisks(values)
	if len(disks) != 2 || disks[0] != `C:\VMs\debian-red\debian-red.vdi` {
		t.Fatalf("discos: %v (el ISO no debe contar)", disks)
	}
	nics := vboxNics(values)
	if len(nics) != 2 || nics[0].MAC != "08:00:27:bd:31:fd" || nics[1].Network != "interna:lan-a" {
		t.Fatalf("tarjetas: %+v", nics)
	}
	if d := distroFromDescription(values["description"]); d != "debian-13" {
		t.Fatalf("distro: %q", d)
	}
}

func TestParseGuestIPs(t *testing.T) {
	out := "/VirtualBox/GuestInfo/Net/1/V4/IP = '10.0.0.5'\n/VirtualBox/GuestInfo/Net/0/V4/IP = '192.168.1.29'\n"
	if ip := parseGuestIPs(out); ip != "192.168.1.29" {
		t.Fatalf("ip = %q", ip)
	}
}

func TestDeleteDetachesShareableDisks(t *testing.T) {
	f := &fakeVBox{responses: map[string]string{
		"showvminfo": "VMState=\"poweroff\"\n" + strings.ReplaceAll(debianInfo, `VMState="running"`, ""),
		`showmediuminfo disk C:\VMs\compartido.vdi`: "Capacity:       1024 MBytes\nType:           shareable\n",
		"showmediuminfo": "Capacity:       20480 MBytes\nType:           normal (base)\n",
		"storageattach":  "",
		"unregistervm":   "",
	}}
	p := NewVBoxProvider(f, nil)
	if err := p.Delete("debian-red"); err != nil {
		t.Fatal(err)
	}
	var detached, deleted bool
	for _, c := range f.calls {
		joined := strings.Join(c, " ")
		if strings.HasPrefix(joined, "storageattach debian-red --storagectl SATA --port 1 --device 0 --medium none") {
			detached = true
		}
		if joined == "unregistervm debian-red --delete" {
			if !detached {
				t.Fatal("se borro la VM antes de desconectar el disco compartido")
			}
			deleted = true
		}
	}
	if !detached || !deleted {
		t.Fatalf("llamadas: %v", f.calls)
	}
}

func TestGuestExecParsesExitMarker(t *testing.T) {
	f := &fakeVBox{responses: map[string]string{"guestcontrol": "hola\r\n" + exitMarker + "3\r\n"}}
	p := NewVBoxProvider(f, func(string) (GuestCredentials, error) { return GuestCredentials{"u", "p"}, nil })
	res, err := p.GuestExec("debian-red", "echo hola; exit 3", 1e9)
	if err != nil {
		t.Fatal(err)
	}
	if res.ExitCode != 3 || res.Stdout != "hola\n" {
		t.Fatalf("%+v", res)
	}
}

func TestNicArgs(t *testing.T) {
	cases := map[string]string{
		"nat":                     "--nic2 nat",
		"default":                 "--nic2 nat",
		"puente:Intel Wi-Fi":      "--nic2 bridged --bridgeadapter2 Intel Wi-Fi",
		"interna:lan-a":           "--nic2 intnet --intnet2 lan-a",
		"solo-anfitrion:VBox Net": "--nic2 hostonly --hostonlyadapter2 VBox Net",
		"red-nat:labnat":          "--nic2 natnetwork --nat-network2 labnat",
	}
	for network, want := range cases {
		args, err := nicArgs(2, network)
		if err != nil || strings.Join(args, " ") != want {
			t.Errorf("%s: %v %v", network, args, err)
		}
	}
	for _, bad := range []string{"interna:", "interna:a b", "otra:x", "algo"} {
		if _, err := nicArgs(2, bad); err == nil {
			t.Errorf("%q deberia rechazarse", bad)
		}
	}
}

func TestAttachNicNeedsStoppedVMAndUsesFreeSlot(t *testing.T) {
	running := &fakeVBox{responses: map[string]string{"showvminfo": debianInfo}}
	if err := NewVBoxProvider(running, nil).AttachNic("debian-red", "interna:lan-b"); err == nil || !strings.Contains(err.Error(), "apague") {
		t.Fatalf("con la VM encendida debe pedir apagarla: %v", err)
	}
	stopped := &fakeVBox{responses: map[string]string{
		"showvminfo": strings.Replace(debianInfo, `VMState="running"`, `VMState="poweroff"`, 1),
		"modifyvm":   "",
	}}
	if err := NewVBoxProvider(stopped, nil).AttachNic("debian-red", "interna:lan-b"); err != nil {
		t.Fatal(err)
	}
	last := strings.Join(stopped.calls[len(stopped.calls)-1], " ")
	if !strings.HasPrefix(last, "modifyvm debian-red --nic3 intnet --intnet3 lan-b") {
		t.Fatalf("debe usar la tarjeta 3 (1 y 2 ocupadas): %s", last)
	}
}

func TestValidTarget(t *testing.T) {
	for _, ok := range []string{"192.168.20.10", "debian.org", "lab-ubuntu"} {
		if !validTarget(ok) {
			t.Errorf("%q deberia aceptarse", ok)
		}
	}
	for _, bad := range []string{"", "1.2.3.4; rm -rf /", "$(id)", "a b", "::1", "-c 100 x"} {
		if validTarget(bad) {
			t.Errorf("%q deberia rechazarse", bad)
		}
	}
}

func TestPlanInternalNetWithoutSubnetHasNoFalseWarning(t *testing.T) {
	st := &NetState{Manager: "netplan", Hypervisor: "VirtualBox", Interfaces: []NetIface{
		{Name: "enp0s3", MAC: "08:00:27:00:00:01", Type: "eth", IP: "10.0.2.15", Prefix: 24, Mode: "dhcp", Role: "main", Gateway: "10.0.2.2", Metric: 100, Extras: []string{}, Nets: []string{}},
		{Name: "enp0s8", MAC: "08:00:27:00:00:02", Type: "eth", Mode: "none", Role: "none", Extras: []string{}, Nets: []string{}},
	}, Libvirt: map[string]*LibvirtNet{"08:00:27:00:00:02": {Network: "interna: lan-a", Isolated: true, Leases: map[string]string{}, Reserved: map[string]string{}}}}
	plan := PlanNetwork(st, NetRequest{Interface: "enp0s8", Mode: "static", IP: "192.168.10.10", Mask: "/24", Gateway: "192.168.10.1", Role: "routes", Nets: "192.168.20.0/24"}, 120)
	for _, c := range plan.Checks {
		if strings.Contains(c.Text, "0.0.0.0/0") {
			t.Fatalf("aviso falso: %s", c.Text)
		}
	}
	if !plan.Valid {
		t.Fatalf("debia ser valido: %+v", plan.Checks)
	}
}
