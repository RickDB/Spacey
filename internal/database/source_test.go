package database

import (
	"os"
	"path/filepath"
	"testing"
)

func TestSourceAvailability(t *testing.T) {
	root := t.TempDir()
	if ok, why := sourceAvailability(root); !ok || why != "" {
		t.Fatalf("empty directory: %v %s", ok, why)
	}
	if ok, why := sourceAvailability(filepath.Join(root, "missing")); ok || why == "" {
		t.Fatal("missing path must report a reason")
	}
	file := filepath.Join(root, "file")
	if err := os.WriteFile(file, []byte("test"), 0600); err != nil {
		t.Fatal(err)
	}
	if ok, why := sourceAvailability(file); ok || why != "container path is not a directory" {
		t.Fatalf("file: %v %s", ok, why)
	}
}
