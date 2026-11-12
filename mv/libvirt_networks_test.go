package vm

import (
	"strings"
	"testing"
)

func TestValidateNetworkSpec(t *testing.T) {
	cases := []struct {
		spec NetworkSpec
		ok   bool
	}{
		{NetworkSpec{Name: "aislada", Mode: "isolated", Subnet: "192.168.100.0/24"}, true},
		{NetworkSpec{Name: "lab", Mode: "nat", Subnet: "10.10.0.0/16"}, true},
		{NetworkSpec{Name: "lab", Mode: "bridge", Subnet: "192.168.100.0/24"}, false}, // modo no soportado
		{NetworkSpec{Name: "lab", Mode: "nat", Subnet: "192.168.100.5/24"}, false},    // no es la direccion de red
		{NetworkSpec{Name: "lab", Mode: "nat", Subnet: "8.8.8.0/24"}, false},          // publica
		{NetworkSpec{Name: "lab", Mode: "nat", Subnet: "192.168.100.0/30"}, false},    // muy pequena
		{NetworkSpec{Name: "mi red", Mode: "nat", Subnet: "192.168.100.0/24"}, false}, // nombre invalido
	}
	for _, c := range cases {
		if _, err := ValidateNetworkSpec(c.spec); (err == nil) != c.ok {
			t.Errorf("%+v: ok=%v, err=%v", c.spec, c.ok, err)
		}
	}
}

func TestNetworkXML(t *testing.T) {
	spec := NetworkSpec{Name: "aislada", Mode: "isolated", Subnet: "192.168.100.0/24", DHCP: true}
	cidr, _ := ValidateNetworkSpec(spec)
	x := networkXML(spec, cidr)
	if strings.Contains(x, "<forward") {
		t.Fatalf("an isolated network must not forward: %s", x)
	}
	for _, want := range []string{"<name>aislada</name>", "address='192.168.100.1' netmask='255.255.255.0'", "start='192.168.100.128' end='192.168.100.254'"} {
		if !strings.Contains(x, want) {
			t.Fatalf("missing %q in %s", want, x)
		}
	}
	spec.Mode, spec.DHCP = "nat", false
	if x := networkXML(spec, cidr); !strings.Contains(x, "<forward mode='nat'/>") || strings.Contains(x, "<dhcp>") {
		t.Fatalf("unexpected nat xml: %s", x)
	}
}

func TestLibvirtProvider_Create_WithNetworks(t *testing.T) {
	fake := newFakeSSHRunner()
	provider := NewLibvirtProvider(fake)
	_, err := provider.Create(CreateSpec{Name: "vm-test", Distro: "fedora-42", CPU: 1, MemoryMB: 1024, DiskGB: 10,
		ImagePath: "/var/lib/libvirt/templates/fedora.qcow2", Networks: []string{"default", "aislada"}})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !fake.calledWith("--os-variant fedora42 --network network='default',model=virtio --network network='aislada',model=virtio") {
		t.Fatalf("expected one virtio nic per network, calls: %v", fake.calls)
	}
}

func TestLibvirtProvider_ListNetworks(t *testing.T) {
	fake := newFakeSSHRunner()
	fake.on("net-list --all", " Name      State    Autostart   Persistent\n--------------------------------------------\n default   active   yes         yes\n aislada   active   yes         yes\n", nil)
	fake.on("net-dumpxml 'default'", "<network><name>default</name><forward mode='nat'/><bridge name='virbr0'/><ip address='192.168.122.1' netmask='255.255.255.0'><dhcp><range start='192.168.122.2' end='192.168.122.254'/></dhcp></ip></network>", nil)
	fake.on("net-dumpxml 'aislada'", "<network><name>aislada</name><bridge name='virbr1'/><ip address='192.168.100.1' netmask='255.255.255.0'/></network>", nil)
	fake.on("list --all --name", "vm1\n", nil)
	fake.on("domiflist 'vm1'", " Interface   Type      Source    Model    MAC\n-------------------------------------------------\n -           network   aislada   virtio   52:54:00:aa:bb:cc\n", nil)
	nets, err := NewLibvirtProvider(fake).ListNetworks()
	if err != nil || len(nets) != 2 {
		t.Fatalf("unexpected: %+v %v", nets, err)
	}
	if nets[0].Mode != "nat" || nets[0].Subnet != "192.168.122.0/24" {
		t.Fatalf("default parsed wrong: %+v", nets[0])
	}
	if nets[1].Mode != "isolated" || len(nets[1].VMs) != 1 || nets[1].VMs[0] != "vm1" {
		t.Fatalf("aislada parsed wrong: %+v", nets[1])
	}
}
