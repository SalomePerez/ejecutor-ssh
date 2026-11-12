package vm

import (
	"encoding/base64"
	"errors"
	"strings"
	"testing"
)

// cmdResponse es la respuesta simulada para un comando SSH.
type cmdResponse struct {
	output []byte
	err    error
}

type commandMatcher struct {
	contains string
	resp     cmdResponse
}

// fakeSSHRunner implementa sshRunner sin abrir conexiones reales. Los
// comandos se resuelven por el primer patron (substring) que coincida; si
// ninguno coincide, se devuelve defaultResp (por defecto, exito sin salida).
// Esto evita depender de un hipervisor real para probar LibvirtProvider.
type fakeSSHRunner struct {
	calls       []string
	matchers    []commandMatcher
	defaultResp cmdResponse
}

func newFakeSSHRunner() *fakeSSHRunner {
	return &fakeSSHRunner{defaultResp: cmdResponse{output: []byte("")}}
}

func (f *fakeSSHRunner) on(contains, output string, err error) {
	f.matchers = append(f.matchers, commandMatcher{contains: contains, resp: cmdResponse{output: []byte(output), err: err}})
}

func (f *fakeSSHRunner) ExecuteCommand(command string) ([]byte, error) {
	f.calls = append(f.calls, command)
	for _, m := range f.matchers {
		if strings.Contains(command, m.contains) {
			return m.resp.output, m.resp.err
		}
	}
	return f.defaultResp.output, f.defaultResp.err
}

func (f *fakeSSHRunner) calledWith(substr string) bool {
	for _, c := range f.calls {
		if strings.Contains(c, substr) {
			return true
		}
	}
	return false
}

// ---------- List ----------

func TestLibvirtProvider_List(t *testing.T) {
	fake := newFakeSSHRunner()
	fake.on("virsh --connect qemu:///system list --all --name", "vm1\nvm2\n", nil)
	fake.on("domstate 'vm1'", "running\n", nil)
	fake.on("domstate 'vm2'", "shut off\n", nil)
	fake.on("dumpxml 'vm1'", `<domain><memory unit='KiB'>2097152</memory><currentMemory unit='KiB'>2097152</currentMemory><vcpu current='2'>4</vcpu></domain>`, nil)
	fake.on("dumpxml 'vm2'", `<domain><memory unit='KiB'>1048576</memory><currentMemory unit='KiB'>1048576</currentMemory><vcpu current='1'>1</vcpu></domain>`, nil)

	provider := NewLibvirtProvider(fake)
	vms, err := provider.List()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(vms) != 2 {
		t.Fatalf("expected 2 vms, got %d", len(vms))
	}
	if vms[0].Name != "vm1" || vms[0].Status != VMStatusRunning || vms[0].CPU != 2 || vms[0].MemoryMB != 2048 {
		t.Fatalf("unexpected vm1 data: %+v", vms[0])
	}
	if vms[1].Name != "vm2" || vms[1].Status != VMStatusStopped || vms[1].CPU != 1 || vms[1].MemoryMB != 1024 {
		t.Fatalf("unexpected vm2 data: %+v", vms[1])
	}
}

const domifaddrAgentOutput = ` Name       MAC address          Protocol     Address
-------------------------------------------------------------------------------
 lo         00:00:00:00:00:00    ipv4         127.0.0.1/8
 eth0       52:54:00:12:34:56    ipv4         192.168.122.45/24
 -          -                    ipv6         fe80::5054:ff:fe12:3456/64
`

func TestLibvirtProvider_List_IPFromGuestAgent(t *testing.T) {
	fake := newFakeSSHRunner()
	fake.on("list --all --name", "vm1\n", nil)
	fake.on("domstate 'vm1'", "running\n", nil)
	fake.on("dumpxml 'vm1'", `<domain><memory unit='KiB'>1048576</memory><currentMemory unit='KiB'>1048576</currentMemory><vcpu current='1'>1</vcpu></domain>`, nil)
	fake.on("domifaddr 'vm1' --source agent", domifaddrAgentOutput, nil)
	provider := NewLibvirtProvider(fake)

	vms, err := provider.List()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(vms) != 1 || vms[0].IP != "192.168.122.45" {
		t.Fatalf("expected ip from guest agent, got %+v", vms)
	}
}

func TestLibvirtProvider_List_IPFallsBackToDHCPLease(t *testing.T) {
	fake := newFakeSSHRunner()
	fake.on("list --all --name", "vm1\n", nil)
	fake.on("domstate 'vm1'", "running\n", nil)
	fake.on("dumpxml 'vm1'", `<domain><memory unit='KiB'>1048576</memory><currentMemory unit='KiB'>1048576</currentMemory><vcpu current='1'>1</vcpu></domain>`, nil)
	fake.on("domifaddr 'vm1' --source agent", "", errors.New("Guest agent is not responding: QEMU guest agent is not connected"))
	fake.on("domifaddr 'vm1' --source lease", domifaddrAgentOutput, nil)
	provider := NewLibvirtProvider(fake)

	vms, err := provider.List()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(vms) != 1 || vms[0].IP != "192.168.122.45" {
		t.Fatalf("expected ip from dhcp lease fallback, got %+v", vms)
	}
}

func TestLibvirtProvider_List_NoIPAvailable(t *testing.T) {
	fake := newFakeSSHRunner()
	fake.on("list --all --name", "vm1\n", nil)
	fake.on("domstate 'vm1'", "running\n", nil)
	fake.on("dumpxml 'vm1'", `<domain><memory unit='KiB'>1048576</memory><currentMemory unit='KiB'>1048576</currentMemory><vcpu current='1'>1</vcpu></domain>`, nil)
	fake.on("domifaddr 'vm1' --source agent", "", errors.New("agent not responding"))
	fake.on("domifaddr 'vm1' --source lease", "", errors.New("no leases"))
	provider := NewLibvirtProvider(fake)

	vms, err := provider.List()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(vms) != 1 || vms[0].IP != "" {
		t.Fatalf("expected no ip when neither source has data, got %+v", vms)
	}
}

func TestLibvirtProvider_List_StoppedVM_SkipsIPLookup(t *testing.T) {
	fake := newFakeSSHRunner()
	fake.on("list --all --name", "vm1\n", nil)
	fake.on("domstate 'vm1'", "shut off\n", nil)
	fake.on("dumpxml 'vm1'", `<domain><memory unit='KiB'>1048576</memory><currentMemory unit='KiB'>1048576</currentMemory><vcpu current='1'>1</vcpu></domain>`, nil)
	provider := NewLibvirtProvider(fake)

	vms, err := provider.List()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(vms) != 1 || vms[0].IP != "" {
		t.Fatalf("expected no ip for a stopped vm, got %+v", vms)
	}
	if fake.calledWith("domifaddr") {
		t.Fatal("should not query ip for a stopped vm")
	}
}

func TestParseDomifaddrOutput(t *testing.T) {
	if ip := parseDomifaddrOutput(domifaddrAgentOutput); ip != "192.168.122.45" {
		t.Fatalf("expected 192.168.122.45, got %q", ip)
	}
	if ip := parseDomifaddrOutput(""); ip != "" {
		t.Fatalf("expected empty string for empty output, got %q", ip)
	}
	if ip := parseDomifaddrOutput(" lo   00:00:00:00:00:00   ipv4   127.0.0.1/8\n"); ip != "" {
		t.Fatalf("expected loopback to be ignored, got %q", ip)
	}
}

func TestLibvirtProvider_List_Empty(t *testing.T) {
	fake := newFakeSSHRunner()
	fake.on("list --all --name", "", nil)
	provider := NewLibvirtProvider(fake)

	vms, err := provider.List()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(vms) != 0 {
		t.Fatalf("expected no vms, got %d", len(vms))
	}
}

func TestLibvirtProvider_List_Error(t *testing.T) {
	fake := newFakeSSHRunner()
	fake.on("list --all --name", "boom details", errors.New("exit status 1"))
	provider := NewLibvirtProvider(fake)

	if _, err := provider.List(); err == nil {
		t.Fatal("expected error")
	} else if !strings.Contains(err.Error(), "boom details") {
		t.Fatalf("expected wrapped ssh output in error, got %v", err)
	}
}

func TestLibvirtProvider_List_NilClient(t *testing.T) {
	var provider *LibvirtProvider
	if _, err := provider.List(); err == nil {
		t.Fatal("expected error for uninitialized provider")
	}
}

// ---------- Start / Stop ----------

func TestLibvirtProvider_Start_AlreadyRunning(t *testing.T) {
	fake := newFakeSSHRunner()
	fake.on("domstate 'vm1'", "running\n", nil)
	provider := NewLibvirtProvider(fake)

	if err := provider.Start("vm1"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if fake.calledWith("system start 'vm1'") {
		t.Fatal("did not expect a start command when the vm is already running")
	}
}

func TestLibvirtProvider_Start_FromStopped(t *testing.T) {
	fake := newFakeSSHRunner()
	fake.on("domstate 'vm1'", "shut off\n", nil)
	provider := NewLibvirtProvider(fake)

	if err := provider.Start("vm1"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !fake.calledWith("virsh --connect qemu:///system start 'vm1'") {
		t.Fatal("expected start command to be issued")
	}
}

func TestHideVMXInXML(t *testing.T) {
	cases := []struct {
		name, in, want string
		changed        bool
	}{
		{"self-closing", "<domain><cpu mode='host-passthrough' check='none' migratable='on'/></domain>",
			"<domain><cpu mode='host-passthrough' check='none' migratable='on'>" + vmxDisabled + "</cpu></domain>", true},
		{"with children", "<cpu mode='host-model'><topology sockets='1'/></cpu>",
			"<cpu mode='host-model'>" + vmxDisabled + "<topology sockets='1'/></cpu>", true},
		{"vmx required", "<cpu mode='custom'><feature policy='require' name='vmx'/></cpu>",
			"<cpu mode='custom'>" + vmxDisabled + "</cpu>", true},
		{"already disabled", "<cpu mode='host-passthrough'>" + vmxDisabled + "</cpu>",
			"<cpu mode='host-passthrough'>" + vmxDisabled + "</cpu>", false},
		{"no cpu element", "<domain><vcpu>2</vcpu></domain>", "<domain><vcpu>2</vcpu></domain>", false},
	}
	for _, c := range cases {
		got, changed := hideVMXInXML(c.in)
		if got != c.want || changed != c.changed {
			t.Errorf("%s: got (%q, %v), want (%q, %v)", c.name, got, changed, c.want, c.changed)
		}
	}
}

func TestLibvirtProvider_Start_HidesVMXBeforeStarting(t *testing.T) {
	fake := newFakeSSHRunner()
	fake.on("domstate 'vm1'", "shut off\n", nil)
	fake.on("dumpxml --inactive 'vm1'", "<domain><cpu mode='host-passthrough' check='none' migratable='on'/></domain>", nil)
	provider := NewLibvirtProvider(fake)

	if err := provider.Start("vm1"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	define, start := -1, -1
	for i, c := range fake.calls {
		if strings.Contains(c, "system define '/tmp/ejecutor-cpu-vm1.xml'") {
			define = i
		}
		if strings.Contains(c, "system start 'vm1'") {
			start = i
		}
	}
	if define < 0 || start < define {
		t.Fatalf("expected virsh define before start, calls: %v", fake.calls)
	}
}

func TestLibvirtProvider_Create_Emulated(t *testing.T) {
	fake := newFakeSSHRunner()
	provider := NewLibvirtProvider(fake)
	_, err := provider.Create(CreateSpec{Name: "vm-test", Distro: "debian-13", CPU: 1, MemoryMB: 1024, DiskGB: 10,
		ImagePath: "/var/lib/libvirt/templates/debian-13.qcow2", Emulated: true})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !fake.calledWith("--virt-type qemu --cpu mode=maximum") || fake.calledWith("host-passthrough") {
		t.Fatalf("expected an emulated vm without host-passthrough, calls: %v", fake.calls)
	}
}

func TestHideVMXInXML_SkipsEmulatedVM(t *testing.T) {
	in := "<domain type='qemu'><cpu mode='maximum' check='none'/></domain>"
	if got, changed := hideVMXInXML(in); changed || got != in {
		t.Fatalf("an emulated vm must not be redefined, got %q", got)
	}
}

func TestLibvirtProvider_Start_DoesNotStartIfVMXCannotBeHidden(t *testing.T) {
	fake := newFakeSSHRunner()
	fake.on("domstate 'vm1'", "shut off\n", nil)
	fake.on("dumpxml --inactive 'vm1'", "<cpu mode='host-passthrough'/>", nil)
	fake.on("system define", "error: permiso denegado", errors.New("exit 1"))
	provider := NewLibvirtProvider(fake)

	if err := provider.Start("vm1"); err == nil {
		t.Fatal("expected an error when vmx cannot be hidden")
	}
	if fake.calledWith("system start 'vm1'") {
		t.Fatal("a vm that still exposes vmx must not be started")
	}
}

func TestLibvirtProvider_Start_InvalidName(t *testing.T) {
	provider := NewLibvirtProvider(newFakeSSHRunner())
	if err := provider.Start("bad name"); err == nil {
		t.Fatal("expected validation error for invalid name")
	}
}

func TestLibvirtProvider_Stop_AlreadyStopped(t *testing.T) {
	fake := newFakeSSHRunner()
	fake.on("domstate 'vm1'", "shut off\n", nil)
	provider := NewLibvirtProvider(fake)

	if err := provider.Stop("vm1"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if fake.calledWith("system destroy 'vm1'") {
		t.Fatal("did not expect a destroy command when the vm is already stopped")
	}
}

func TestLibvirtProvider_Stop_FromRunning(t *testing.T) {
	fake := newFakeSSHRunner()
	fake.on("domstate 'vm1'", "running\n", nil)
	provider := NewLibvirtProvider(fake)

	if err := provider.Stop("vm1"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !fake.calledWith("virsh --connect qemu:///system destroy 'vm1'") {
		t.Fatal("expected destroy command to be issued")
	}
}

// ---------- Rename ----------

func TestLibvirtProvider_Rename(t *testing.T) {
	fake := newFakeSSHRunner()
	fake.on("domstate 'old'", "shut off\n", nil)
	provider := NewLibvirtProvider(fake)

	if err := provider.Rename("old", "new"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !fake.calledWith("domrename 'old' 'new'") {
		t.Fatal("expected domrename command")
	}
}

func TestLibvirtProvider_Rename_StopFails(t *testing.T) {
	fake := newFakeSSHRunner()
	fake.on("domstate 'old'", "", errors.New("ssh error"))
	provider := NewLibvirtProvider(fake)

	if err := provider.Rename("old", "new"); err == nil {
		t.Fatal("expected error when the vm cannot be stopped first")
	}
	if fake.calledWith("domrename") {
		t.Fatal("should not attempt rename if stopping the vm failed")
	}
}

// ---------- Update ----------

func TestLibvirtProvider_Update(t *testing.T) {
	fake := newFakeSSHRunner()
	fake.on("domstate 'vm1'", "shut off\n", nil)
	provider := NewLibvirtProvider(fake)

	err := provider.Update("vm1", UpdateSpec{Name: "vm1", CPU: 2, MemoryMB: 2048})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !fake.calledWith("setvcpus 'vm1' 2 --config") {
		t.Fatal("expected setvcpus command")
	}
	if !fake.calledWith("setmem 'vm1' 2048M --config") {
		t.Fatal("expected setmem command")
	}
	if fake.calledWith("domrename") {
		t.Fatal("should not rename when the name did not change")
	}
}

func TestLibvirtProvider_Update_Rename(t *testing.T) {
	fake := newFakeSSHRunner()
	fake.on("domstate 'vm1'", "shut off\n", nil)
	provider := NewLibvirtProvider(fake)

	if err := provider.Update("vm1", UpdateSpec{Name: "vm2", CPU: 2, MemoryMB: 2048}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !fake.calledWith("domrename 'vm1' 'vm2'") {
		t.Fatal("expected domrename command when the name changes")
	}
}

func TestLibvirtProvider_Update_InvalidResources(t *testing.T) {
	provider := NewLibvirtProvider(newFakeSSHRunner())
	err := provider.Update("vm1", UpdateSpec{Name: "vm1", CPU: 0, MemoryMB: 1024})
	if err == nil {
		t.Fatal("expected validation error for zero cpu")
	}
}

// ---------- Get ----------

func TestLibvirtProvider_Get_Found(t *testing.T) {
	fake := newFakeSSHRunner()
	fake.on("list --all --name", "vm1\n", nil)
	fake.on("domstate 'vm1'", "running\n", nil)
	fake.on("dumpxml 'vm1'", `<domain><memory unit='KiB'>1048576</memory><currentMemory unit='KiB'>1048576</currentMemory><vcpu current='1'>1</vcpu></domain>`, nil)
	provider := NewLibvirtProvider(fake)

	got, err := provider.Get("vm1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.Name != "vm1" {
		t.Fatalf("unexpected vm: %+v", got)
	}
}

func TestLibvirtProvider_Get_NotFound(t *testing.T) {
	fake := newFakeSSHRunner()
	fake.on("list --all --name", "", nil)
	provider := NewLibvirtProvider(fake)

	if _, err := provider.Get("missing"); err == nil {
		t.Fatal("expected not found error")
	}
}

// ---------- Delete ----------

const deleteTestDomainXML = `<domain>
	<devices>
		<disk type='file' device='disk'>
			<source file='/var/lib/libvirt/images/vm1-system.qcow2'/>
			<target dev='vda' bus='virtio'/>
		</disk>
		<disk type='file' device='disk'>
			<source file='/var/lib/libvirt/shared-disks/cluster-data.qcow2'/>
			<target dev='vdb' bus='virtio'/>
			<shareable/>
		</disk>
		<disk type='file' device='disk'>
			<source file='/var/lib/libvirt/boot/vm1-seed.iso'/>
			<target dev='vdc' bus='virtio'/>
			<readonly/>
		</disk>
	</devices>
</domain>`

func TestLibvirtProvider_Delete_RemovesOwnDisksOnly(t *testing.T) {
	fake := newFakeSSHRunner()
	fake.on("dumpxml 'vm1'", deleteTestDomainXML, nil)
	provider := NewLibvirtProvider(fake)

	if err := provider.Delete("vm1"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !fake.calledWith("virsh --connect qemu:///system undefine 'vm1'") {
		t.Fatal("expected undefine without --remove-all-storage")
	}
	if fake.calledWith("--remove-all-storage") {
		t.Fatal("should never rely on --remove-all-storage: it would delete shared disks too")
	}
	if !fake.calledWith("rm -f '/var/lib/libvirt/images/vm1-system.qcow2'") {
		t.Fatal("expected the vm's own system disk to be removed")
	}
	if fake.calledWith("rm -f '/var/lib/libvirt/shared-disks/cluster-data.qcow2'") {
		t.Fatal("a shareable disk must never be deleted just because one of its VMs was deleted")
	}
}

func TestLibvirtProvider_Delete_AbortsIfDisksCannotBeListed(t *testing.T) {
	fake := newFakeSSHRunner()
	fake.on("dumpxml 'vm1'", "", errors.New("virsh unreachable"))
	provider := NewLibvirtProvider(fake)

	if err := provider.Delete("vm1"); err == nil {
		t.Fatal("expected delete to abort when disks cannot be enumerated")
	}
	if fake.calledWith("undefine") {
		t.Fatal("should not undefine the domain if it is unclear which disks are shared")
	}
}

func TestLibvirtProvider_Delete_InvalidName(t *testing.T) {
	provider := NewLibvirtProvider(newFakeSSHRunner())
	if err := provider.Delete("bad name"); err == nil {
		t.Fatal("expected validation error")
	}
}

// ---------- ListDisks ----------

func TestLibvirtProvider_ListDisks(t *testing.T) {
	fake := newFakeSSHRunner()
	fake.on("dumpxml 'vm1'", deleteTestDomainXML, nil)
	fake.on("qemu-img info --output=json '/var/lib/libvirt/images/vm1-system.qcow2'", `{"virtual-size": 21474836480}`, nil)
	fake.on("qemu-img info --output=json '/var/lib/libvirt/shared-disks/cluster-data.qcow2'", `{"virtual-size": 5368709120}`, nil)
	provider := NewLibvirtProvider(fake)

	disks, err := provider.ListDisks("vm1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(disks) != 2 {
		t.Fatalf("expected 2 disks, got %d: %+v", len(disks), disks)
	}
	if disks[0].Name != "system" || disks[0].Shareable || disks[0].SizeGB != 20 {
		t.Fatalf("unexpected system disk: %+v", disks[0])
	}
	if disks[1].Name != "cluster-data" || !disks[1].Shareable || disks[1].SizeGB != 5 {
		t.Fatalf("unexpected shared disk: %+v", disks[1])
	}
}

func TestLibvirtProvider_ListDisks_SizeQueryFailsGracefully(t *testing.T) {
	fake := newFakeSSHRunner()
	fake.on("dumpxml 'vm1'", deleteTestDomainXML, nil)
	fake.on("qemu-img info", "", errors.New("permission denied"))
	provider := NewLibvirtProvider(fake)

	disks, err := provider.ListDisks("vm1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	for _, d := range disks {
		if d.SizeGB != 0 {
			t.Fatalf("expected size 0 when qemu-img info fails, got %+v", d)
		}
	}
}

func TestLibvirtProvider_ListDisks_InvalidName(t *testing.T) {
	provider := NewLibvirtProvider(newFakeSSHRunner())
	if _, err := provider.ListDisks("bad name"); err == nil {
		t.Fatal("expected validation error")
	}
}

// ---------- Create ----------

func TestLibvirtProvider_Create_Success(t *testing.T) {
	fake := newFakeSSHRunner()
	provider := NewLibvirtProvider(fake)

	spec := CreateSpec{
		Name:         "vm-test",
		Distro:       "ubuntu-24.04",
		CPU:          2,
		MemoryMB:     2048,
		DiskGB:       20,
		SSHUser:      "ubuntu",
		SSHPublicKey: "ssh-ed25519 AAAA... test",
		ImagePath:    "/var/lib/libvirt/templates/ubuntu.qcow2",
	}

	created, err := provider.Create(spec)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if created.Name != "vm-test" || created.Status != VMStatusRunning {
		t.Fatalf("unexpected created vm: %+v", created)
	}
	if !fake.calledWith("test -f '/var/lib/libvirt/templates/ubuntu.qcow2'") {
		t.Fatal("expected image existence check")
	}
	if !fake.calledWith("qemu-img create -f qcow2 -F qcow2 -b '/var/lib/libvirt/templates/ubuntu.qcow2'") {
		t.Fatal("expected derived system disk creation")
	}
	if !fake.calledWith("virt-install --connect qemu:///system --name 'vm-test'") {
		t.Fatal("expected virt-install invocation")
	}
	if !fake.calledWith("--cpu host-passthrough,disable=vmx") {
		t.Fatal("expected the new vm to be created without vmx")
	}
	if !fake.calledWith("cloud-localds") {
		t.Fatal("expected a persistent cloud-init seed")
	}
	// La semilla va como disco virtio y despues del disco del sistema, que es
	// el que arranca.
	for _, c := range fake.calls {
		if strings.Contains(c, "virt-install") {
			sys, seed := strings.Index(c, "vm-test-system.qcow2"), strings.Index(c, "vm-test-seed.iso',device=disk,bus=virtio")
			if seed < 0 || sys < 0 || seed < sys {
				t.Fatalf("expected the seed as a virtio disk after the system disk: %s", c)
			}
		}
	}
	if fake.calledWith("virsh --connect qemu:///system destroy 'vm-test'") {
		t.Fatal("the vm must keep running so cloud-init can finish its first boot")
	}
	if !fake.calledWith("dominfo 'vm-test'") {
		t.Fatal("expected post-create verification")
	}
}

func TestLibvirtProvider_Create_InstallsGuestAgent(t *testing.T) {
	fake := newFakeSSHRunner()
	provider := NewLibvirtProvider(fake)

	spec := CreateSpec{
		Name:         "vm-test",
		Distro:       "ubuntu-24.04",
		CPU:          1,
		MemoryMB:     1024,
		DiskGB:       10,
		SSHPublicKey: "ssh-ed25519 AAAA... test",
		ImagePath:    "/var/lib/libvirt/templates/base.qcow2",
	}
	if _, err := provider.Create(spec); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !fake.calledWith("--channel unix,target_type=virtio,name=org.qemu.guest_agent.0") {
		t.Fatal("expected the guest agent virtio channel to be attached to the vm")
	}

	var cloudInitCall string
	for _, call := range fake.calls {
		if strings.Contains(call, "printf %s") {
			cloudInitCall = call
			break
		}
	}
	if cloudInitCall == "" {
		t.Fatal("expected a cloud-init write command")
	}

	start := strings.Index(cloudInitCall, "printf %s '") + len("printf %s '")
	end := strings.Index(cloudInitCall[start:], "'")
	if start < 0 || end < 0 {
		t.Fatalf("could not locate encoded cloud-init payload in command: %s", cloudInitCall)
	}
	decoded, err := base64.StdEncoding.DecodeString(cloudInitCall[start : start+end])
	if err != nil {
		t.Fatalf("decode cloud-init payload: %v", err)
	}

	userData := string(decoded)
	if !strings.Contains(userData, "qemu-guest-agent") {
		t.Fatalf("expected cloud-init to install qemu-guest-agent inside the vm, got: %s", userData)
	}
	if !strings.Contains(userData, "systemctl enable --now qemu-guest-agent") {
		t.Fatalf("expected cloud-init to enable and start qemu-guest-agent, got: %s", userData)
	}
}

func TestLibvirtProvider_Create_ImageMissing(t *testing.T) {
	fake := newFakeSSHRunner()
	fake.on("test -f", "", errors.New("not found"))
	provider := NewLibvirtProvider(fake)

	spec := CreateSpec{Name: "vm-test", Distro: "ubuntu-24.04", CPU: 1, MemoryMB: 1024, DiskGB: 10, ImagePath: "/var/lib/libvirt/templates/missing.qcow2"}
	if _, err := provider.Create(spec); err == nil {
		t.Fatal("expected error when the image does not exist on the hypervisor")
	}
	if fake.calledWith("virt-install") {
		t.Fatal("should not attempt virt-install when the image check fails")
	}
}

func TestLibvirtProvider_Create_WindowsImagePath(t *testing.T) {
	provider := NewLibvirtProvider(newFakeSSHRunner())
	spec := CreateSpec{Name: "vm-test", Distro: "ubuntu-24.04", CPU: 1, MemoryMB: 1024, DiskGB: 10, ImagePath: `C:\images\foo.qcow2`}
	if _, err := provider.Create(spec); err == nil {
		t.Fatal("expected error for a windows-style image path")
	}
}

func TestLibvirtProvider_Create_InvalidResources(t *testing.T) {
	provider := NewLibvirtProvider(newFakeSSHRunner())
	spec := CreateSpec{Name: "vm-test", Distro: "ubuntu-24.04", CPU: 0, MemoryMB: 1024, DiskGB: 10}
	if _, err := provider.Create(spec); err == nil {
		t.Fatal("expected error for zero cpu")
	}
}

func TestLibvirtProvider_Create_VirtInstallFails_CleansUpDisk(t *testing.T) {
	fake := newFakeSSHRunner()
	fake.on("--import --disk", "", errors.New("virt-install failed"))
	provider := NewLibvirtProvider(fake)

	spec := CreateSpec{Name: "vm-test", Distro: "ubuntu-24.04", CPU: 1, MemoryMB: 1024, DiskGB: 10, ImagePath: "/var/lib/libvirt/templates/base.qcow2"}
	if _, err := provider.Create(spec); err == nil {
		t.Fatal("expected error when virt-install fails")
	}
	if !fake.calledWith("rm -f '/var/lib/libvirt/images/vm-test-system.qcow2'") {
		t.Fatal("expected the derived system disk to be cleaned up after a failed create")
	}
}

func TestLibvirtProvider_Create_AdditionalDisk_CreatedWhenMissing(t *testing.T) {
	fake := newFakeSSHRunner()
	dataDiskPath := "/var/lib/libvirt/images/vm-test-data1.qcow2"
	fake.on("test -f '"+dataDiskPath+"'", "", errors.New("missing"))
	provider := NewLibvirtProvider(fake)

	spec := CreateSpec{
		Name: "vm-test", Distro: "ubuntu-24.04", CPU: 1, MemoryMB: 1024, DiskGB: 10,
		ImagePath: "/var/lib/libvirt/templates/base.qcow2",
		Disks:     []DiskSpec{{Name: "data1", SizeGB: 10}},
	}
	created, err := provider.Create(spec)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !fake.calledWith("qemu-img create -f qcow2 '" + dataDiskPath + "' 10G") {
		t.Fatal("expected the additional disk to be created since it did not exist")
	}
	if len(created.Disks) != 1 || created.Disks[0].Path != dataDiskPath {
		t.Fatalf("unexpected disks: %+v", created.Disks)
	}
	if !fake.calledWith("--disk path='" + dataDiskPath + "',format=qcow2,bus=virtio") {
		t.Fatal("expected the additional disk to be attached in the virt-install command")
	}
}

func TestLibvirtProvider_Create_ShareableDisk(t *testing.T) {
	fake := newFakeSSHRunner()
	sharedPath := "/var/lib/libvirt/shared-disks/cluster-data.qcow2"
	provider := NewLibvirtProvider(fake)

	spec := CreateSpec{
		Name: "vm-test", Distro: "ubuntu-24.04", CPU: 1, MemoryMB: 1024, DiskGB: 10,
		ImagePath: "/var/lib/libvirt/templates/base.qcow2",
		Disks:     []DiskSpec{{Name: "cluster-data", SizeGB: 5, Shareable: true, ReadOnly: true}},
	}
	created, err := provider.Create(spec)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !fake.calledWith("pool-build ejecutor-compartidos") {
		t.Fatal("expected the shared disks directory to be prepared")
	}
	if !fake.calledWith("--disk path='" + sharedPath + "',format=qcow2,bus=virtio,readonly=on,shareable=on") {
		t.Fatal("expected readonly+shareable flags on the disk attach")
	}
	if len(created.Disks) != 1 || !created.Disks[0].Shareable {
		t.Fatalf("unexpected disk metadata: %+v", created.Disks)
	}
}

// ---------- domainResources ----------

func TestLibvirtProvider_DomainResources_InvalidXML(t *testing.T) {
	fake := newFakeSSHRunner()
	fake.on("dumpxml 'vm1'", "not xml", nil)
	provider := NewLibvirtProvider(fake)

	cpu, mem := provider.domainResources("vm1")
	if cpu != 0 || mem != 0 {
		t.Fatalf("expected zero values on malformed xml, got cpu=%d mem=%d", cpu, mem)
	}
}

func TestLibvirtProvider_RepairGuestAgent(t *testing.T) {
	fake := newFakeSSHRunner()
	provider := NewLibvirtProvider(fake)

	if err := provider.RepairGuestAgent("vm5", "ubuntu", "ssh-ed25519 AAAA test"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !fake.calledWith("cloud-localds") {
		t.Fatal("expected a new seed to be built")
	}
	if !fake.calledWith("detach-disk 'vm5'") {
		t.Fatal("expected the old seed (sata cdrom on older vms) to be detached")
	}
	if !fake.calledWith("attach-disk 'vm5' '/var/lib/libvirt/boot/vm5-seed.iso' vdz --targetbus virtio --type disk") {
		t.Fatal("expected the seed to be attached as a virtio disk")
	}
	if !fake.calledWith("system start 'vm5'") {
		t.Fatal("expected the vm to boot again with the seed")
	}
	// La VM encendida bloquea su semilla: hay que apagarla antes de reescribirla.
	destroyAt, seedAt := -1, -1
	for i, c := range fake.calls {
		if destroyAt < 0 && strings.Contains(c, "system destroy 'vm5'") {
			destroyAt = i
		}
		if seedAt < 0 && strings.Contains(c, "cloud-localds") {
			seedAt = i
		}
	}
	if destroyAt < 0 || destroyAt > seedAt {
		t.Fatalf("expected destroy before writing the seed, calls: %v", fake.calls)
	}
}

func TestLibvirtProvider_HostInfo(t *testing.T) {
	fake := newFakeSSHRunner()
	fake.on("free -m", "3915 991\n2\n", nil)
	info, err := NewLibvirtProvider(fake).HostInfo()
	if err != nil || info.MemoryMB != 3915 || info.AvailableMB != 991 || info.CPUs != 2 {
		t.Fatalf("unexpected host info: %+v %v", info, err)
	}
}
