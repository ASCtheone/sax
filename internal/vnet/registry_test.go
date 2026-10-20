package vnet

import (
	"os"
	"path/filepath"
	"testing"
)

func TestRegistry_RoundTrip(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, portsDir), 0o755); err != nil {
		t.Fatal(err)
	}
	if _, ok := LookupPort(dir, "10.12.12.4", 3200); ok {
		t.Fatal("empty registry should miss")
	}
	if err := RegisterPort(dir, "10.12.12.4", 3200, 54012, os.Getpid()); err != nil {
		t.Fatal(err)
	}
	real, ok := LookupPort(dir, "10.12.12.4", 3200)
	if !ok || real != 54012 {
		t.Errorf("LookupPort = %d, %v", real, ok)
	}
	// Same format the C shim writes: "<real> <pid>\n" at ports/<ip>_<port>.
	data, _ := os.ReadFile(filepath.Join(dir, portsDir, "10.12.12.4_3200"))
	if string(data) != "54012 "+itoa(os.Getpid())+"\n" {
		t.Errorf("file = %q", data)
	}
	UnregisterPort(dir, "10.12.12.4", 3200)
	if _, ok := LookupPort(dir, "10.12.12.4", 3200); ok {
		t.Error("entry still present after unregister")
	}
}

func TestRegistry_DeadOwnerIsIgnored(t *testing.T) {
	dir := t.TempDir()
	_ = os.MkdirAll(filepath.Join(dir, portsDir), 0o755)
	const deadPID = 999999 // beyond the default pid range
	if err := RegisterPort(dir, "10.12.12.4", 3200, 54012, deadPID); err != nil {
		t.Fatal(err)
	}
	if _, ok := LookupPort(dir, "10.12.12.4", 3200); ok {
		t.Error("entry owned by a dead process must not resolve")
	}
}

func TestRegistry_Garbage(t *testing.T) {
	dir := t.TempDir()
	_ = os.MkdirAll(filepath.Join(dir, portsDir), 0o755)
	_ = os.WriteFile(filepath.Join(dir, portsDir, "10.12.12.4_3200"), []byte("nope"), 0o644)
	if _, ok := LookupPort(dir, "10.12.12.4", 3200); ok {
		t.Error("garbage entry must not resolve")
	}
}
