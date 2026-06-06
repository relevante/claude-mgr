package watch

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestFileWatchesExistingPath(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state_5.sqlite-wal")
	if err := os.WriteFile(path, []byte("a"), 0o644); err != nil {
		t.Fatal(err)
	}
	f, err := NewFile(path)
	if err != nil {
		t.Fatalf("NewFile: %v", err)
	}
	defer f.Close()
	drain(f.Events())

	if err := os.WriteFile(path, []byte("b"), 0o644); err != nil {
		t.Fatal(err)
	}
	if !waitSignal(f.Events(), 3*time.Second) {
		t.Fatal("no signal on write")
	}
}

func TestFileWatchesFuturePath(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state_5.sqlite-wal")
	f, err := NewFile(path)
	if err != nil {
		t.Fatalf("NewFile: %v", err)
	}
	defer f.Close()
	drain(f.Events())

	if err := os.WriteFile(path, []byte("a"), 0o644); err != nil {
		t.Fatal(err)
	}
	if !waitSignal(f.Events(), 3*time.Second) {
		t.Fatal("no signal on create")
	}
	drain(f.Events())
	if err := os.WriteFile(path, []byte("b"), 0o644); err != nil {
		t.Fatal(err)
	}
	if !waitSignal(f.Events(), 3*time.Second) {
		t.Fatal("no signal on write after create")
	}
}
