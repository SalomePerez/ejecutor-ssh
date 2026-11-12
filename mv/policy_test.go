package vm

import "testing"

func TestHasShareableDisk(t *testing.T) {
	if HasShareableDisk(nil) {
		t.Fatal("expected false for no disks")
	}
	if HasShareableDisk([]DiskSpec{{Name: "data", SizeGB: 5}}) {
		t.Fatal("expected false when no disk is shareable")
	}
	if !HasShareableDisk([]DiskSpec{{Name: "data", SizeGB: 5}, {Name: "cluster", SizeGB: 10, Shareable: true}}) {
		t.Fatal("expected true when at least one disk is shareable")
	}
}
