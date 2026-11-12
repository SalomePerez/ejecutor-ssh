package vm

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func newCreateProvider(t *testing.T, f *fakeVBox) (*VBoxProvider, string) {
	dir := t.TempDir()
	tpl := filepath.Join(dir, "plantillas")
	_ = os.MkdirAll(tpl, 0o755)
	_ = os.WriteFile(filepath.Join(tpl, "debian-13.vdi"), []byte("x"), 0o644)
	iso := filepath.Join(dir, "VBoxGuestAdditions.iso")
	_ = os.WriteFile(iso, []byte("x"), 0o644)
	p := NewVBoxProvider(f, func(string) (GuestCredentials, error) { return GuestCredentials{"vboxuser", "clave"}, nil })
	p.SetOptions(VBoxOptions{TemplatesDir: tpl, SharedDir: filepath.Join(dir, "compartidos"), SeedsDir: filepath.Join(dir, "seeds"),
		SeedURL: "http://10.0.2.2:8081", AdditionsISO: iso})
	return p, dir
}

func createResponses() map[string]string {
	return map[string]string{
		"list vms":       "",
		"createvm":       "Virtual machine 'lab-a' is created and registered.\nSettings file: 'C:\\VMs\\lab-a\\lab-a.vbox'\n",
		"modifyvm":       "",
		"storagectl":     "",
		"clonemedium":    "",
		"showmediuminfo": "Capacity:       3072 MBytes\n",
		"modifymedium":   "",
		"storageattach":  "",
		"setextradata":   "",
		"startvm":        "",
		"unregistervm":   "",
	}
}

func TestVBoxCreateSteps(t *testing.T) {
	f := &fakeVBox{responses: createResponses()}
	p, dir := newCreateProvider(t, f)
	spec := CreateSpec{Name: "lab-a", Distro: "debian-13", CPU: 1, MemoryMB: 1024, DiskGB: 10,
		ImagePath: filepath.Join(dir, "plantillas", "debian-13.vdi"), SSHPublicKey: "ssh-ed25519 AAAA app",
		Networks: []string{"default", "interna:lan-a"}}
	created, err := p.Create(spec)
	if err != nil {
		t.Fatal(err)
	}
	if created.SSHUser != "vboxuser" || created.Status != VMStatusRunning {
		t.Fatalf("%+v", created)
	}
	all := []string{}
	for _, c := range f.calls {
		all = append(all, strings.Join(c, " "))
	}
	joined := strings.Join(all, "\n")
	for _, want := range []string{
		"createvm --name lab-a --ostype Debian_64 --register",
		"--description distro=debian-13",
		"--nic1 nat",
		"--nic2 intnet --intnet2 lan-a",
		"modifymedium disk " + filepath.Join(`C:\VMs\lab-a`, "lab-a-system.vdi") + " --resize 10240",
		"--type dvddrive --medium " + filepath.Join(dir, "VBoxGuestAdditions.iso"),
		"DmiSystemSerial ds=nocloud;s=http://10.0.2.2:8081/cloud-init/lab-a/",
		"startvm lab-a --type headless",
	} {
		if !strings.Contains(joined, want) {
			t.Errorf("falta %q en:\n%s", want, joined)
		}
	}
	userData, err := p.SeedFile("lab-a", "user-data")
	if err != nil || !strings.Contains(string(userData), `plain_text_passwd: "clave"`) || !strings.Contains(string(userData), "ssh-ed25519 AAAA app") {
		t.Fatalf("user-data: %v\n%s", err, userData)
	}
	if _, err := p.SeedFile("lab-a", "../../etc/passwd"); err == nil {
		t.Fatal("no debe servir archivos fuera de la semilla")
	}
}

func TestVBoxCreateRollsBackOnFailure(t *testing.T) {
	r := createResponses()
	delete(r, "startvm")
	f := &fakeVBox{responses: r}
	p, dir := newCreateProvider(t, f)
	_, err := p.Create(CreateSpec{Name: "lab-a", Distro: "debian-13", CPU: 1, MemoryMB: 1024, DiskGB: 2,
		ImagePath: filepath.Join(dir, "plantillas", "debian-13.vdi")})
	if err == nil {
		t.Fatal("debia fallar")
	}
	last := strings.Join(f.calls[len(f.calls)-1], " ")
	if last != "unregistervm lab-a --delete" {
		t.Fatalf("debe deshacer lo creado; ultima llamada: %s", last)
	}
	if _, err := os.Stat(filepath.Join(dir, "seeds", "lab-a")); !os.IsNotExist(err) {
		t.Fatal("debe borrar la semilla")
	}
}

func TestVBoxCreateRejects(t *testing.T) {
	p, dir := newCreateProvider(t, &fakeVBox{responses: createResponses()})
	base := CreateSpec{Name: "lab-a", Distro: "debian-13", CPU: 1, MemoryMB: 512, DiskGB: 5, ImagePath: filepath.Join(dir, "plantillas", "debian-13.vdi")}
	outside := base
	outside.ImagePath = filepath.Join(dir, "VBoxGuestAdditions.iso")
	bridged := base
	bridged.Networks = []string{"puente:Wi-Fi"}
	for name, spec := range map[string]CreateSpec{"fuera de plantillas": outside, "primera no NAT": bridged} {
		if _, err := p.Create(spec); err == nil {
			t.Errorf("%s: debia rechazarse", name)
		}
	}
}
