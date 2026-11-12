package vm

import (
	"encoding/json"
	"os"
	"testing"
)

// TestVBoxLiveList lee las VMs reales de VirtualBox (solo lectura).
// Se ejecuta solo con VBOX_LIVE=1.
func TestVBoxLiveList(t *testing.T) {
	if os.Getenv("VBOX_LIVE") == "" {
		t.Skip("define VBOX_LIVE=1 para probar contra VirtualBox")
	}
	p := NewVBoxProvider(VBoxManageRunner{Path: `C:\Program Files\Oracle\VirtualBox\VBoxManage.exe`}, nil)
	vms, err := p.List()
	if err != nil {
		t.Fatal(err)
	}
	out, _ := json.MarshalIndent(vms, "", "  ")
	t.Log(string(out))
	disks, err := p.ListDisks("debian-red")
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("discos de debian-red: %+v", disks)
}

// TestVBoxLiveGuestExec ejecuta comandos dentro de una VM por guestcontrol.
// Uso: VBOX_LIVE=1 VBOX_VM=debian-red VBOX_GUEST_USER=... VBOX_GUEST_PASS=... go test ./mv -run GuestExec -v
func TestVBoxLiveGuestExec(t *testing.T) {
	if os.Getenv("VBOX_LIVE") == "" || os.Getenv("VBOX_GUEST_USER") == "" {
		t.Skip("define VBOX_LIVE, VBOX_VM, VBOX_GUEST_USER y VBOX_GUEST_PASS")
	}
	creds := GuestCredentials{User: os.Getenv("VBOX_GUEST_USER"), Password: os.Getenv("VBOX_GUEST_PASS")}
	p := NewVBoxProvider(VBoxManageRunner{Path: `C:\Program Files\Oracle\VirtualBox\VBoxManage.exe`},
		func(string) (GuestCredentials, error) { return creds, nil })
	res, err := p.GuestExec(os.Getenv("VBOX_VM"), `id -un; echo "comillas 'simples' y $HOME"; ip -br a; ls /nada`, 30e9)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("codigo %d\n%s", res.ExitCode, res.Stdout)
	if res.ExitCode == 0 {
		t.Error("ls /nada deberia fallar: el codigo de salida no se esta propagando")
	}
}
