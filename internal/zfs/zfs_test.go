package zfs

import (
	"spacey/internal/model"
	"testing"
)

func TestParsersAndLongestMount(t *testing.T) {
	p, e := ParsePools("tank\tONLINE\t1000\t400\t600\t12%\n")
	if e != nil || len(p) != 1 || p[0].Allocated != 400 {
		t.Fatalf("%+v %v", p, e)
	}
	d, e := ParseDatasets("tank\tfilesystem\t400\t600\t350\t450\t390\t1.20x\t/mnt/tank\ntank/media\tfilesystem\t300\t600\t280\t340\t300\t1.10x\t/mnt/tank/media\n")
	if e != nil {
		t.Fatal(e)
	}
	m := MatchDataset("/mnt/tank/media/movies", d)
	if m == nil || m.Name != "tank/media" {
		t.Fatalf("%+v", m)
	}
	if MatchDataset("/other", []model.ZFSDataset{{Mountpoint: "/mnt"}}) != nil {
		t.Fatal("unexpected match")
	}
}
