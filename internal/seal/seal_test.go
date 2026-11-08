package seal

import (
	"path/filepath"
	"testing"
)

func TestSealOpen(t *testing.T) {
	path := filepath.Join(t.TempDir(), "remote.key")
	b, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	v, _ := b.Seal("hunter2")
	if v == "hunter2" || v[:3] != "v1:" {
		t.Fatalf("sealed = %q", v)
	}
	again, _ := Load(path) // the same key from the file
	if got, err := again.Open(v); err != nil || got != "hunter2" {
		t.Fatalf("open = %q %v", got, err)
	}
	other, _ := Load(filepath.Join(t.TempDir(), "other.key"))
	if _, err := other.Open(v); err == nil {
		t.Fatal("another key opened it")
	}
	if e, _ := b.Seal(""); e != "" {
		t.Fatal("empty should stay empty")
	}
}
