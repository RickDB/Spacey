package scanner

import (
	"context"
	"os"
	"path/filepath"
	"spacey/internal/model"
	"testing"
)

func TestScanDoesNotFollowSymlinks(t *testing.T) {
	root := t.TempDir()
	_ = os.Mkdir(filepath.Join(root, "sub"), 0755)
	_ = os.WriteFile(filepath.Join(root, "sub", "x.mkv"), []byte("abc"), 0644)
	_ = os.Symlink(root, filepath.Join(root, "loop"))
	var got []model.Node
	p, e := (Scanner{Options: Options{Workers: 2}}).Scan(context.Background(), root, func(n model.Node) error { got = append(got, n); return nil }, nil)
	if e != nil {
		t.Fatal(e)
	}
	if p.Files != 1 || p.LogicalBytes != 3 || len(got) != 4 {
		t.Fatalf("progress=%+v nodes=%d", p, len(got))
	}
}
