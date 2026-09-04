package zfs

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"spacey/internal/model"
	"strconv"
	"strings"
	"time"
)

type Runner interface {
	Run(context.Context, string, ...string) ([]byte, error)
}
type ExecRunner struct{}

func (ExecRunner) Run(ctx context.Context, n string, a ...string) ([]byte, error) {
	return exec.CommandContext(ctx, n, a...).CombinedOutput()
}

type Discovery struct{ Runner Runner }
type Result struct {
	Detected   bool               `json:"detected"`
	Diagnostic string             `json:"diagnostic"`
	Pools      []model.ZFSPool    `json:"pools"`
	Datasets   []model.ZFSDataset `json:"datasets"`
}

func (d Discovery) Discover(ctx context.Context) Result {
	if d.Runner == nil {
		d.Runner = ExecRunner{}
	}
	r := Result{}
	if _, e := os.Stat("/dev/zfs"); e != nil {
		r.Diagnostic = "/dev/zfs missing: " + e.Error()
	}
	po, e := d.Runner.Run(ctx, "zpool", "list", "-H", "-p", "-o", "name,health,size,alloc,free,frag")
	if e != nil {
		r.Diagnostic = diagnose("zpool list", po, e)
		return r
	}
	r.Pools, e = ParsePools(string(po))
	if e != nil {
		r.Diagnostic = e.Error()
		return r
	}
	ds, e := d.Runner.Run(ctx, "zfs", "list", "-H", "-p", "-o", "name,type,used,available,referenced,logicalused,logicalreferenced,compressratio,mountpoint")
	if e != nil {
		r.Diagnostic = diagnose("zfs list", ds, e)
		return r
	}
	r.Datasets, e = ParseDatasets(string(ds))
	if e != nil {
		r.Diagnostic = e.Error()
		return r
	}
	r.Detected = true
	r.Diagnostic = "ZFS detected"
	return r
}
func ParsePools(s string) ([]model.ZFSPool, error) {
	var out []model.ZFSPool
	for i, l := range lines(s) {
		f := strings.Split(l, "\t")
		if len(f) != 6 {
			return nil, fmt.Errorf("zpool row %d: expected 6 fields", i+1)
		}
		out = append(out, model.ZFSPool{Name: f[0], Health: f[1], Available: true, Size: i64(f[2]), Allocated: i64(f[3]), Free: i64(f[4]), Fragmentation: ratio(f[5]), LastSeenAt: time.Now()})
	}
	return out, nil
}
func ParseDatasets(s string) ([]model.ZFSDataset, error) {
	var out []model.ZFSDataset
	for i, l := range lines(s) {
		f := strings.Split(l, "\t")
		if len(f) != 9 {
			return nil, fmt.Errorf("zfs row %d: expected 9 fields", i+1)
		}
		out = append(out, model.ZFSDataset{Name: f[0], Type: f[1], Used: i64(f[2]), Available: i64(f[3]), Referenced: i64(f[4]), LogicalUsed: i64(f[5]), LogicalReferenced: i64(f[6]), Compression: ratio(f[7]), Mountpoint: f[8], LastSeenAt: time.Now()})
	}
	return out, nil
}
func MatchDataset(path string, sets []model.ZFSDataset) *model.ZFSDataset {
	var best *model.ZFSDataset
	for i := range sets {
		m := strings.TrimSuffix(sets[i].Mountpoint, "/")
		if m == "" || m == "-" || m == "legacy" || m == "none" {
			continue
		}
		if path == m || strings.HasPrefix(path, m+"/") {
			if best == nil || len(m) > len(best.Mountpoint) {
				best = &sets[i]
			}
		}
	}
	return best
}
func lines(s string) []string {
	var o []string
	for _, l := range strings.Split(strings.TrimSpace(s), "\n") {
		if strings.TrimSpace(l) != "" {
			o = append(o, l)
		}
	}
	return o
}
func i64(s string) int64 { v, _ := strconv.ParseInt(s, 10, 64); return v }
func ratio(s string) float64 {
	s = strings.TrimSuffix(strings.TrimSuffix(s, "%"), "x")
	v, _ := strconv.ParseFloat(s, 64)
	return v
}
func diagnose(op string, b []byte, e error) string {
	m := strings.TrimSpace(string(b))
	if m == "" {
		m = e.Error()
	}
	return op + " failed: " + m
}
