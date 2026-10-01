package api

import (
	"os"
	"path/filepath"
	"spacey/internal/model"
	"testing"
)

func TestSafeLivePath(t *testing.T) {
	root := t.TempDir()
	if e := os.Mkdir(filepath.Join(root, "Movies"), 0755); e != nil {
		t.Fatal(e)
	}
	p, e := safeLivePath(root, "/media", "/media/Movies/Alien.mkv")
	if e != nil || p != filepath.Join(root, "Movies/Alien.mkv") {
		t.Fatalf("path=%q error=%v", p, e)
	}
	if _, e = safeLivePath(root, "/media", "/media2/other"); e == nil {
		t.Fatal("accepted path outside source")
	}
	if e = os.Symlink(t.TempDir(), filepath.Join(root, "escape")); e != nil {
		t.Fatal(e)
	}
	if _, e = safeLivePath(root, "/media", "/media/escape/file.mkv"); e == nil {
		t.Fatal("accepted symlink escape")
	}
}
func TestParseEpisode(t *testing.T) {
	m := parseMedia(model.Node{Name: "Silo.S01E02.1080p.mkv", Extension: ".mkv", Type: "file", CanonicalPath: "/media/TV/Silo/Silo.S01E02.1080p.mkv"})
	if m.Kind != "episode" || m.Title != "Silo" || m.Season != 1 || m.Episode != 2 {
		t.Fatalf("media=%+v", m)
	}
}
