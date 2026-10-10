package api

import (
	"context"
	"crypto/rand"
	"embed"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"spacey/internal/database"
	"spacey/internal/model"
	"spacey/internal/scanner"
	"spacey/internal/zfs"
	"strconv"
	"strings"
	"sync"
	"time"
)

type Server struct {
	DB        *database.DB
	HostID    int64
	Workers   int
	Discovery zfs.Discovery
	mu        sync.RWMutex
	progress  map[string]model.Progress
	cancels   map[string]context.CancelFunc
	mdblist   *mediaState
	zfsMu     sync.Mutex
	settings  appSettings
}

func New(db *database.DB, host int64, workers int) *Server {
	s := &Server{DB: db, HostID: host, Workers: workers, Discovery: zfs.Discovery{}, progress: map[string]model.Progress{}, cancels: map[string]context.CancelFunc{}, mdblist: newMediaState()}
	if found, err := db.AppSettings(context.Background(), &s.settings); err != nil {
		panic(err)
	} else if found {
		s.Workers = s.settings.ScanWorkers
		s.mdblist.applySettings(s.settings)
	}
	return s
}
func (s *Server) StartDiscovery(ctx context.Context) {
	go s.scheduleLoop(ctx)
	go s.mediaSyncLoop(ctx)
	go func() {
		run := func() {
			s.mu.RLock()
			enabled := s.settings.EnableZFS
			s.mu.RUnlock()
			if !enabled {
				return
			}
			c, cancel := context.WithTimeout(ctx, 2*time.Minute)
			defer cancel()
			s.runZFSDiscovery(c)
		}
		run()
		ticker := time.NewTicker(15 * time.Minute)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				run()
			}
		}
	}()

}
func (s *Server) Handler() http.Handler {
	m := http.NewServeMux()
	m.HandleFunc("GET /healthz", s.health)
	m.HandleFunc("GET /", s.index)
	m.Handle("GET /static/", http.StripPrefix("/static/", s.assets()))
	m.HandleFunc("GET /api/v1/settings", s.settingsStatus)
	m.HandleFunc("POST /api/v1/setup", s.saveSettings)
	m.HandleFunc("PUT /api/v1/settings", s.saveSettings)
	m.HandleFunc("GET /api/v1/storage", s.listStorage)
	m.HandleFunc("POST /api/v1/storage", s.addStorage)
	m.HandleFunc("POST /api/v1/storage/{id}/scan", s.startScan)
	m.HandleFunc("GET /api/v1/storage/{id}/nodes", s.currentNodes)
	m.HandleFunc("GET /api/v1/scans", s.scans)
	m.HandleFunc("GET /api/v1/scans/{id}/events", s.events)
	m.HandleFunc("GET /api/v1/scans/{id}/nodes", s.nodes)
	m.HandleFunc("GET /api/v1/files/largest", s.largest)
	m.HandleFunc("GET /api/v1/overview", s.overview)
	m.HandleFunc("GET /api/v1/search", s.search)
	m.HandleFunc("GET /api/v1/filters", s.filters)
	m.HandleFunc("POST /api/v1/filters", s.addFilter)
	m.HandleFunc("DELETE /api/v1/filters/{id}", s.deleteFilter)
	m.HandleFunc("GET /api/v1/schedules", s.schedules)
	m.HandleFunc("POST /api/v1/schedules", s.addSchedule)
	m.HandleFunc("DELETE /api/v1/schedules/{id}", s.deleteSchedule)
	m.HandleFunc("GET /api/v1/mdblist", s.mdblistStatus)
	m.HandleFunc("GET /api/v1/media", s.mediaStatus)
	m.HandleFunc("POST /api/v1/media/sync", s.mediaSync)
	m.HandleFunc("GET /api/v1/trash", s.trashList)
	m.HandleFunc("POST /api/v1/trash", s.moveToTrash)
	m.HandleFunc("POST /api/v1/trash/{id}/restore", s.restoreTrash)
	m.HandleFunc("GET /api/v1/cleanup/status", s.cleanupStatus)
	m.HandleFunc("POST /api/v1/mdblist/sync", s.mdblistSync)
	m.HandleFunc("GET /api/v1/zfs", s.zfsOverview)
	m.HandleFunc("GET /api/v1/zfs/history", s.zfsHistory)
	m.HandleFunc("POST /api/v1/zfs/discover", s.discover)
	return logging(m)
}
func (s *Server) health(w http.ResponseWriter, r *http.Request) {
	if e := s.DB.Pool.Ping(r.Context()); e != nil {
		problem(w, 503, e)
		return
	}
	jsonOut(w, map[string]string{"status": "ok"})
}

//go:embed static/*
var staticFiles embed.FS

func (s *Server) index(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}
	b, e := staticFiles.ReadFile("static/index.html")
	if e != nil {
		problem(w, 500, e)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = w.Write(b)
}
func (s *Server) assets() http.Handler {
	sub, _ := fs.Sub(staticFiles, "static")
	return http.FileServer(http.FS(sub))
}
func (s *Server) listStorage(w http.ResponseWriter, r *http.Request) {
	x, e := s.DB.Sources(r.Context())
	if e != nil {
		problem(w, 500, e)
		return
	}
	jsonOut(w, x)
}
func (s *Server) addStorage(w http.ResponseWriter, r *http.Request) {
	if !s.authorized(w, r) {
		return
	}
	var in struct{ Name, CanonicalRoot, ContainerRoot string }
	if e := json.NewDecoder(r.Body).Decode(&in); e != nil {
		problem(w, 400, e)
		return
	}
	if in.Name == "" || !filepath.IsAbs(in.CanonicalRoot) || !filepath.IsAbs(in.ContainerRoot) {
		problem(w, 400, fmt.Errorf("name and absolute canonical/container roots required"))
		return
	}
	x, e := s.DB.CreateSource(r.Context(), s.HostID, in.Name, filepath.Clean(in.CanonicalRoot), filepath.Clean(in.ContainerRoot))
	if e != nil {
		problem(w, 500, e)
		return
	}
	jsonOutStatus(w, x, 201)
}
func (s *Server) startScan(w http.ResponseWriter, r *http.Request) {
	sid, e := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if e != nil {
		problem(w, 400, e)
		return
	}
	src, e := s.DB.Source(r.Context(), sid)
	if e != nil {
		problem(w, 404, e)
		return
	}
	if !src.Available {
		problem(w, 409, fmt.Errorf("storage unavailable; last inventory remains browsable"))
		return
	}
	id := uuid()
	if e = s.DB.CreateScan(r.Context(), id, src.ID, src.CanonicalRoot); e != nil {
		problem(w, 500, e)
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	s.mu.Lock()
	s.cancels[id] = cancel
	s.progress[id] = model.Progress{ScanID: id, Root: src.CanonicalRoot, Status: "running", StartedAt: time.Now()}
	s.mu.Unlock()
	go s.runScan(ctx, id, src)
	jsonOutStatus(w, map[string]string{"id": id, "status": "running"}, 202)
}
func (s *Server) runScan(ctx context.Context, id string, src model.StorageSource) {
	dataset, e := s.DB.DatasetForPath(ctx, src.CanonicalRoot)
	if e == nil {
		e = s.DB.LinkAndCaptureZFS(ctx, id, dataset)
	}
	if e != nil {
		s.DB.FailScan(context.Background(), id, "failed", e)
		return
	}
	writer, e := s.DB.NewWriter(ctx, id, src.ID, dataset)
	if e != nil {
		s.DB.FailScan(context.Background(), id, "failed", e)
		return
	}
	mapNode := func(n model.Node) error {
		n.CanonicalPath = translate(n.CanonicalPath, src.ContainerRoot, src.CanonicalRoot)
		n.ParentPath = translate(n.ParentPath, src.ContainerRoot, src.CanonicalRoot)
		return writer.Add(n)
	}
	update := func(p model.Progress) {
		p.ScanID = id
		p.Root = src.CanonicalRoot
		s.mu.Lock()
		s.progress[id] = p
		s.mu.Unlock()
	}
	s.mu.RLock()
	workers := s.Workers
	s.mu.RUnlock()
	p, e := (scanner.Scanner{Options: scanner.Options{Workers: workers, Exclude: []string{".spacey-trash"}}}).Scan(ctx, src.ContainerRoot, mapNode, update)
	if e != nil {
		writer.Rollback()
		status := "failed"
		if ctx.Err() != nil {
			status = "cancelled"
		}
		s.DB.FailScan(context.Background(), id, status, e)
		p.Status = status
		p.Error = e.Error()
		update(p)
	} else if e = writer.Commit(p); e != nil {
		s.DB.FailScan(context.Background(), id, "failed", e)
		p.Status = "failed"
		p.Error = e.Error()
		update(p)
	}
	s.mu.Lock()
	delete(s.cancels, id)
	s.mu.Unlock()
}
func (s *Server) currentNodes(w http.ResponseWriter, r *http.Request) {
	source, e := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if e != nil {
		problem(w, 400, e)
		return
	}
	scan, e := s.DB.LatestScanID(r.Context(), source)
	if e != nil {
		problem(w, 404, fmt.Errorf("no completed inventory for this storage source"))
		return
	}
	x, e := s.DB.Nodes(r.Context(), scan, r.URL.Query().Get("parent"), r.URL.Query().Get("q"), intParam(r, "limit", 250))
	if e != nil {
		problem(w, 500, e)
		return
	}
	jsonOut(w, map[string]any{"scan_id": scan, "nodes": x})
}
func (s *Server) scans(w http.ResponseWriter, r *http.Request) {
	x, e := s.DB.Scans(r.Context())
	if e != nil {
		problem(w, 500, e)
		return
	}
	jsonOut(w, x)
}
func (s *Server) events(w http.ResponseWriter, r *http.Request) {
	f, ok := w.(http.Flusher)
	if !ok {
		problem(w, 500, fmt.Errorf("streaming unavailable"))
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	id := r.PathValue("id")
	t := time.NewTicker(time.Second)
	defer t.Stop()
	for {
		s.mu.RLock()
		p, found := s.progress[id]
		s.mu.RUnlock()
		if !found {
			p.ScanID = id
			p.Status = "not_running"
		}
		b, _ := json.Marshal(p)
		fmt.Fprintf(w, "event: progress\ndata: %s\n\n", b)
		f.Flush()
		if p.Status != "running" {
			return
		}
		select {
		case <-r.Context().Done():
			return
		case <-t.C:
		}
	}
}
func (s *Server) nodes(w http.ResponseWriter, r *http.Request) {
	x, e := s.DB.Nodes(r.Context(), r.PathValue("id"), r.URL.Query().Get("parent"), r.URL.Query().Get("q"), intParam(r, "limit", 250))
	if e != nil {
		problem(w, 500, e)
		return
	}
	jsonOut(w, x)
}
func (s *Server) largest(w http.ResponseWriter, r *http.Request) {
	scan := r.URL.Query().Get("scan_id")
	if scan == "" {
		problem(w, 400, fmt.Errorf("scan_id required"))
		return
	}
	x, e := s.DB.Largest(r.Context(), scan, r.URL.Query().Get("type"), intParam(r, "limit", 100))
	if e != nil {
		problem(w, 500, e)
		return
	}
	jsonOut(w, x)
}
func (s *Server) zfsOverview(w http.ResponseWriter, r *http.Request) {
	p, d, e := s.DB.ZFS(r.Context())
	if e != nil {
		problem(w, 500, e)
		return
	}
	status, e := s.DB.ZFSStatus(r.Context(), s.HostID)
	if e != nil {
		problem(w, 500, e)
		return
	}
	jsonOut(w, map[string]any{"pools": p, "datasets": d, "monitor": status})
}
func (s *Server) zfsHistory(w http.ResponseWriter, r *http.Request) {
	kind := r.URL.Query().Get("kind")
	id, e := strconv.ParseInt(r.URL.Query().Get("id"), 10, 64)
	if e != nil || id < 1 {
		problem(w, 400, fmt.Errorf("valid pool or dataset id required"))
		return
	}
	span, e := database.ZFSRange(r.URL.Query().Get("range"), time.Now())
	if e != nil {
		problem(w, 400, e)
		return
	}
	points, e := s.DB.ZFSHistory(r.Context(), kind, id, span)
	if e != nil {
		if kind != "pool" && kind != "dataset" {
			problem(w, 400, e)
		} else {
			problem(w, 500, e)
		}
		return
	}
	jsonOut(w, map[string]any{"kind": kind, "id": id, "points": points})
}
func (s *Server) runZFSDiscovery(ctx context.Context) zfs.Result {
	parent := ctx
	ctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	s.zfsMu.Lock()
	defer s.zfsMu.Unlock()
	result := s.Discovery.Discover(ctx)
	if result.Detected {
		if e := s.DB.SaveZFS(ctx, s.HostID, result.Pools, result.Datasets); e != nil {
			result.Detected = false
			result.Diagnostic = "Save ZFS samples failed: " + e.Error()
		}
	}
	if !result.Detected && parent.Err() == nil {
		writeCtx, stop := context.WithTimeout(context.Background(), 5*time.Second)
		defer stop()
		_ = s.DB.SaveZFSFailure(writeCtx, s.HostID, result.Diagnostic)
	}
	return result
}
func (s *Server) discover(w http.ResponseWriter, r *http.Request) {
	x := s.runZFSDiscovery(r.Context())
	if !x.Detected {
		problem(w, 502, fmt.Errorf("%s", x.Diagnostic))
		return
	}
	jsonOut(w, x)
}
func translate(p, from, to string) string {
	from = filepath.Clean(from)
	p = filepath.Clean(p)
	if p == from {
		return filepath.Clean(to)
	}
	if strings.HasPrefix(p, from+string(os.PathSeparator)) {
		return filepath.Join(to, strings.TrimPrefix(p, from))
	}
	return p
}
func uuid() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	b[6] = (b[6] & 15) | 64
	b[8] = (b[8] & 63) | 128
	s := hex.EncodeToString(b)
	return s[:8] + "-" + s[8:12] + "-" + s[12:16] + "-" + s[16:20] + "-" + s[20:]
}
func intParam(r *http.Request, k string, d int) int {
	v, e := strconv.Atoi(r.URL.Query().Get(k))
	if e != nil {
		return d
	}
	return v
}
func jsonOut(w http.ResponseWriter, v any) { jsonOutStatus(w, v, 200) }
func jsonOutStatus(w http.ResponseWriter, v any, status int) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
func problem(w http.ResponseWriter, status int, e error) {
	jsonOutStatus(w, map[string]string{"error": e.Error()}, status)
}
func logging(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		next.ServeHTTP(w, r)
		fmt.Printf("%s %s %s\n", r.Method, r.URL.Path, time.Since(start).Round(time.Millisecond))
	})
}
func human(n int64) string {
	u := []string{"B", "KiB", "MiB", "GiB", "TiB", "PiB"}
	v := float64(n)
	i := 0
	for v >= 1024 && i < len(u)-1 {
		v /= 1024
		i++
	}
	return fmt.Sprintf("%.1f %s", v, u[i])
}
