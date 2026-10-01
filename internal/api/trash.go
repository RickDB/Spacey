package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"spacey/internal/database"
	"strings"
)

func (s *Server) authorized(w http.ResponseWriter, r *http.Request) bool {
	if !s.validAdmin(r.Header.Get("X-Spacey-Admin-Token")) {
		problem(w, 401, fmt.Errorf("invalid admin token"))
		return false
	}
	return true
}
func within(root, path string) bool {
	root = filepath.Clean(root)
	path = filepath.Clean(path)
	return path != root && strings.HasPrefix(path, root+string(os.PathSeparator))
}
func safeLivePath(srcRoot, canonicalRoot, canonical string) (string, error) {
	if !within(canonicalRoot, canonical) {
		return "", fmt.Errorf("path is outside source root")
	}
	rel, e := filepath.Rel(canonicalRoot, canonical)
	if e != nil {
		return "", e
	}
	live := filepath.Join(srcRoot, rel)
	if !within(srcRoot, live) {
		return "", fmt.Errorf("path is outside container root")
	}
	realRoot, e := filepath.EvalSymlinks(srcRoot)
	if e != nil {
		return "", e
	}
	parent := filepath.Dir(live)
	for {
		realParent, err := filepath.EvalSymlinks(parent)
		if err == nil {
			if realParent != realRoot && !within(realRoot, realParent) {
				return "", fmt.Errorf("path resolves outside source root")
			}
			break
		}
		if !os.IsNotExist(err) {
			return "", err
		}
		if parent == srcRoot || parent == filepath.Dir(parent) {
			return "", fmt.Errorf("source root unavailable")
		}
		parent = filepath.Dir(parent)
	}
	return live, nil
}
func (s *Server) trashList(w http.ResponseWriter, r *http.Request) {
	rows, e := s.DB.Trash(r.Context())
	if e != nil {
		problem(w, 500, e)
		return
	}
	jsonOut(w, rows)
}
func (s *Server) moveToTrash(w http.ResponseWriter, r *http.Request) {
	if !s.authorized(w, r) {
		return
	}
	var in struct {
		ScanID string `json:"scan_id"`
		NodeID int64  `json:"node_id"`
	}
	if e := json.NewDecoder(http.MaxBytesReader(w, r.Body, 16<<10)).Decode(&in); e != nil {
		problem(w, 400, e)
		return
	}
	n, e := s.DB.NodeForTrash(r.Context(), in.ScanID, in.NodeID)
	if e != nil {
		problem(w, 404, e)
		return
	}
	scans, e := s.DB.Scans(r.Context())
	if e != nil {
		problem(w, 500, e)
		return
	}
	var sourceID int64
	for _, scan := range scans {
		if scan.ID == in.ScanID {
			sourceID = scan.StorageSourceID
			break
		}
	}
	if sourceID == 0 {
		problem(w, 404, fmt.Errorf("scan not found"))
		return
	}
	latest, e := s.DB.LatestScanID(r.Context(), sourceID)
	if e != nil || latest != in.ScanID {
		problem(w, 409, fmt.Errorf("cleanup requires the latest completed scan"))
		return
	}
	src, e := s.DB.Source(r.Context(), sourceID)
	if e != nil || !src.Available {
		problem(w, 409, fmt.Errorf("source unavailable"))
		return
	}
	if strings.HasPrefix(n.CanonicalPath, filepath.Join(src.CanonicalRoot, ".spacey-trash")+"/") {
		problem(w, 400, fmt.Errorf("item is already in trash"))
		return
	}
	live, e := safeLivePath(src.ContainerRoot, src.CanonicalRoot, n.CanonicalPath)
	if e != nil {
		problem(w, 400, e)
		return
	}
	if _, e = os.Lstat(live); e != nil {
		problem(w, 409, e)
		return
	}
	id := uuid()
	trashDir := filepath.Join(src.ContainerRoot, ".spacey-trash", id)
	if e = os.MkdirAll(trashDir, 0700); e != nil {
		problem(w, 409, e)
		return
	}
	target := filepath.Join(trashDir, filepath.Base(live))
	rec := database.TrashItem{ID: id, StorageSourceID: sourceID, ScanID: in.ScanID, OriginalPath: n.CanonicalPath, TrashPath: translate(target, src.ContainerRoot, src.CanonicalRoot), LogicalSize: n.LogicalSize}
	if e = s.DB.AddTrash(r.Context(), rec); e != nil {
		_ = os.Remove(trashDir)
		problem(w, 500, e)
		return
	}
	if e = os.Rename(live, target); e != nil {
		s.DB.DeleteTrashRecord(r.Context(), id)
		_ = os.Remove(trashDir)
		problem(w, 409, e)
		return
	}
	jsonOutStatus(w, rec, 201)
}
func (s *Server) restoreTrash(w http.ResponseWriter, r *http.Request) {
	if !s.authorized(w, r) {
		return
	}
	id := r.PathValue("id")
	rec, e := s.DB.TrashOne(r.Context(), id)
	if e != nil {
		problem(w, 404, e)
		return
	}
	if rec.RestoredAt != nil {
		problem(w, 409, fmt.Errorf("already restored"))
		return
	}
	src, e := s.DB.Source(r.Context(), rec.StorageSourceID)
	if e != nil || !src.Available {
		problem(w, 409, fmt.Errorf("source unavailable"))
		return
	}
	original, e := safeLivePath(src.ContainerRoot, src.CanonicalRoot, rec.OriginalPath)
	if e != nil {
		problem(w, 400, e)
		return
	}
	trash, e := safeLivePath(src.ContainerRoot, src.CanonicalRoot, rec.TrashPath)
	if e != nil {
		problem(w, 400, e)
		return
	}
	if _, e = os.Lstat(original); e == nil {
		problem(w, 409, fmt.Errorf("original path already exists"))
		return
	}
	if _, e = os.Lstat(trash); e != nil {
		problem(w, 409, e)
		return
	}
	if e = os.MkdirAll(filepath.Dir(original), 0755); e != nil {
		problem(w, 409, e)
		return
	}
	if e = os.Rename(trash, original); e != nil {
		problem(w, 409, e)
		return
	}
	if e = s.DB.MarkRestored(r.Context(), id); e != nil {
		problem(w, 500, e)
		return
	}
	_ = os.Remove(filepath.Dir(trash))
	jsonOut(w, map[string]string{"status": "restored"})
}
func (s *Server) cleanupStatus(w http.ResponseWriter, r *http.Request) {
	s.mu.RLock()
	enabled := s.settings.AdminHash != ""
	s.mu.RUnlock()
	jsonOut(w, map[string]bool{"enabled": enabled})
}
