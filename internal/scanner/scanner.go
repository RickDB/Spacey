package scanner

import (
	"context"
	"os"
	"path/filepath"
	"spacey/internal/model"
	"strings"
	"syscall"
	"time"
)

type Options struct {
	Workers          int
	StayOnFilesystem bool
	Exclude          []string
}
type Scanner struct{ Options Options }
type result struct {
	path    string
	entries []os.DirEntry
	err     error
}

func (s Scanner) Scan(ctx context.Context, root string, emit func(model.Node) error, progress func(model.Progress)) (model.Progress, error) {
	root, err := filepath.Abs(root)
	if err != nil {
		return model.Progress{}, err
	}
	w := s.Options.Workers
	if w < 1 {
		w = 1
	}
	if w > 64 {
		w = 64
	}
	tasks := make(chan string, w*2)
	results := make(chan result, w*2)
	for i := 0; i < w; i++ {
		go func() {
			for p := range tasks {
				ds, e := os.ReadDir(p)
				select {
				case results <- result{p, ds, e}:
				case <-ctx.Done():
					return
				}
			}
		}()
	}
	p := model.Progress{Root: root, Status: "running", StartedAt: time.Now()}
	queue := []string{root}
	pending := 0
	ri, e := os.Lstat(root)
	if e != nil {
		close(tasks)
		return p, e
	}
	rootDev := device(ri)
	if e = emit(toNode(root, filepath.Dir(root), root, ri)); e != nil {
		close(tasks)
		return p, e
	}
	for len(queue) > 0 || pending > 0 {
		var out chan string
		var next string
		if len(queue) > 0 {
			out = tasks
			next = queue[0]
		}
		select {
		case <-ctx.Done():
			close(tasks)
			p.Status = "cancelled"
			return p, ctx.Err()
		case out <- next:
			queue = queue[1:]
			pending++
		case r := <-results:
			pending--
			p.CurrentPath = r.path
			if r.err != nil {
				p.Errors++
				if progress != nil {
					progress(p)
				}
				continue
			}
			p.Directories++
			for _, de := range r.entries {
				path := filepath.Join(r.path, de.Name())
				if excluded(path, s.Options.Exclude) {
					continue
				}
				info, x := os.Lstat(path)
				if x != nil {
					p.Errors++
					_ = emit(model.Node{CanonicalPath: path, ParentPath: r.path, RelativePath: relative(root, path), Name: de.Name(), Type: "other", ReadError: x.Error()})
					continue
				}
				n := toNode(path, r.path, root, info)
				if x = emit(n); x != nil {
					close(tasks)
					return p, x
				}
				if info.IsDir() {
					if !s.Options.StayOnFilesystem || device(info) == rootDev {
						queue = append(queue, path)
					}
				} else if info.Mode().IsRegular() {
					p.Files++
					p.LogicalBytes += n.LogicalSize
					p.AllocatedBytes += n.AllocatedSize
				}
			}
			if progress != nil {
				progress(p)
			}
		}
	}
	close(tasks)
	p.CurrentPath = ""
	if p.Errors > 0 {
		p.Status = "completed_with_errors"
	} else {
		p.Status = "completed"
	}
	if progress != nil {
		progress(p)
	}
	return p, nil
}
func toNode(path, parent, root string, info os.FileInfo) model.Node {
	n := model.Node{CanonicalPath: path, ParentPath: parent, RelativePath: relative(root, path), Name: info.Name(), Mode: uint32(info.Mode()), ModifiedAt: info.ModTime()}
	switch {
	case info.Mode().IsRegular():
		n.Type = "file"
		n.LogicalSize = info.Size()
		n.Extension = strings.ToLower(filepath.Ext(info.Name()))
	case info.IsDir():
		n.Type = "directory"
	case info.Mode()&os.ModeSymlink != 0:
		n.Type = "symlink"
		n.LinkTarget, _ = os.Readlink(path)
	default:
		n.Type = "other"
	}
	if st, ok := info.Sys().(*syscall.Stat_t); ok {
		n.AllocatedSize = st.Blocks * 512
		n.UID = uint64(st.Uid)
		n.GID = uint64(st.Gid)
		n.DeviceID = uint64(st.Dev)
		n.Inode = st.Ino
		n.AccessedAt = time.Unix(st.Atim.Sec, st.Atim.Nsec)
	}
	return n
}
func relative(root, path string) string {
	r, e := filepath.Rel(root, path)
	if e != nil {
		return path
	}
	if r == "." {
		return ""
	}
	return r
}
func device(i os.FileInfo) uint64 {
	if s, ok := i.Sys().(*syscall.Stat_t); ok {
		return uint64(s.Dev)
	}
	return 0
}
func excluded(path string, rules []string) bool {
	for _, r := range rules {
		if ok, _ := filepath.Match(r, filepath.Base(path)); ok || strings.HasPrefix(path, r) {
			return true
		}
	}
	return false
}
