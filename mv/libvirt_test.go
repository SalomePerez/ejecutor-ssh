package vm

import "testing"

func TestValidateName(t *testing.T) {
	if err := ValidateName("vm-linux-01"); err != nil {
		t.Fatalf("expected valid name, got %v", err)
	}

	if err := ValidateName("bad name"); err == nil {
		t.Fatal("expected invalid name with spaces")
	}
}

func TestValidateDistro(t *testing.T) {
	for _, distro := range []string{"ubuntu-24.04", "debian-12", "rocky-9"} {
		if err := ValidateDistro(distro); err != nil {
			t.Fatalf("expected valid distro %q, got %v", distro, err)
		}
	}

	if err := ValidateDistro("fedora-40"); err == nil {
		t.Fatal("expected invalid distro")
	}
}

func TestParseVMStatus(t *testing.T) {
	if got := parseVMStatus("running"); got != VMStatusRunning {
		t.Fatalf("expected running, got %q", got)
	}

	if got := parseVMStatus("shut off"); got != VMStatusStopped {
		t.Fatalf("expected stopped, got %q", got)
	}
}

func TestOSVariantForDistro(t *testing.T) {
	if got := osVariantForDistro("ubuntu-24.04"); got != "ubuntu24.04" {
		t.Fatalf("expected ubuntu24.04, got %q", got)
	}
	if got := osVariantForDistro("debian-12"); got != "debian12" {
		t.Fatalf("expected debian12, got %q", got)
	}
}
