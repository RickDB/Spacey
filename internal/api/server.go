package api

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"html/template"
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
	page      *template.Template
}

func New(db *database.DB, host int64, workers int) *Server {
	return &Server{DB: db, HostID: host, Workers: workers, Discovery: zfs.Discovery{}, progress: map[string]model.Progress{}, cancels: map[string]context.CancelFunc{}, page: template.Must(template.New("page").Funcs(template.FuncMap{"bytes": human}).Parse(pageHTML))}
}
func (s *Server) StartDiscovery(ctx context.Context) {
	go func() {
		run := func() {
			result := s.Discovery.Discover(ctx)
			if result.Detected {
				_ = s.DB.SaveZFS(ctx, s.HostID, result.Pools, result.Datasets)
			}
		}
		run()
		ticker := time.NewTicker(5 * time.Minute)
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
	m.HandleFunc("GET /api/v1/storage", s.listStorage)
	m.HandleFunc("POST /api/v1/storage", s.addStorage)
	m.HandleFunc("POST /api/v1/storage/{id}/scan", s.startScan)
	m.HandleFunc("GET /api/v1/storage/{id}/nodes", s.currentNodes)
	m.HandleFunc("GET /api/v1/scans", s.scans)
	m.HandleFunc("GET /api/v1/scans/{id}/events", s.events)
	m.HandleFunc("GET /api/v1/scans/{id}/nodes", s.nodes)
	m.HandleFunc("GET /api/v1/files/largest", s.largest)
	m.HandleFunc("GET /api/v1/zfs", s.zfsOverview)
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
func (s *Server) index(w http.ResponseWriter, r *http.Request) {
	sources, _ := s.DB.Sources(r.Context())
	scans, _ := s.DB.Scans(r.Context())
	pools, sets, _ := s.DB.ZFS(r.Context())
	_ = s.page.Execute(w, map[string]any{"Sources": sources, "Scans": scans, "Pools": pools, "Datasets": sets})
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
	p, e := (scanner.Scanner{Options: scanner.Options{Workers: s.Workers}}).Scan(ctx, src.ContainerRoot, mapNode, update)
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
	jsonOut(w, map[string]any{"pools": p, "datasets": d})
}
func (s *Server) discover(w http.ResponseWriter, r *http.Request) {
	x := s.Discovery.Discover(r.Context())
	if x.Detected {
		if e := s.DB.SaveZFS(r.Context(), s.HostID, x.Pools, x.Datasets); e != nil {
			problem(w, 500, e)
			return
		}
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

const pageHTML = `<!doctype html><html><head><meta charset="utf-8"><meta name="viewport" content="width=device-width"><title>Spacey</title><style>
:root{color-scheme:dark;--bg:#0b1220;--card:#151f31;--line:#2a3850;--text:#e7edf6;--muted:#91a0b7;--green:#4ed09b}*{box-sizing:border-box}body{margin:0;background:var(--bg);color:var(--text);font:15px system-ui}header{padding:22px 4vw;border-bottom:1px solid var(--line)}h1{margin:0;font-size:24px}main{max-width:1200px;margin:auto;padding:28px 4vw}.grid{display:grid;grid-template-columns:repeat(auto-fit,minmax(310px,1fr));gap:18px}.card{background:var(--card);border:1px solid var(--line);border-radius:12px;padding:18px}table{width:100%;border-collapse:collapse}th,td{text-align:left;padding:9px;border-bottom:1px solid var(--line)}th,.muted{color:var(--muted)}button{background:var(--green);color:#062116;border:0;border-radius:7px;padding:8px 12px;font-weight:700}.warn{color:#ffcc66}.ok{color:var(--green)}code{font-size:12px}</style></head><body>
<header><h1>Spacey</h1><span class="muted">Storage inventory that survives the storage</span></header><main><div class="grid">
<section class="card"><h2>Storage</h2><form onsubmit="addSource(event)"><input name="name" placeholder="Name" required> <input name="canonical" placeholder="/mnt/tank/media" required> <input name="container" placeholder="/host/mnt/tank/media" required> <button>Add</button></form>{{if .Sources}}<table><tr><th>Name</th><th>Root</th><th>State</th><th></th></tr>{{range .Sources}}<tr><td>{{.Name}}</td><td><code>{{.CanonicalRoot}}</code></td><td>{{if .Available}}<span class="ok">Available{{else}}<span class="warn">Unavailable{{end}}</span></td><td>{{if .Available}}<button onclick="scan({{.ID}},this)">Scan</button>{{else}}Browse last inventory{{end}}</td></tr>{{end}}</table>{{else}}<p class="muted">Add a storage source to begin.</p>{{end}}<pre id="progress"></pre></section>
<section class="card"><h2>ZFS</h2><button onclick="discover()">Refresh discovery</button>{{if .Pools}}<table><tr><th>Pool</th><th>Health</th><th>Allocated</th><th>Free</th></tr>{{range .Pools}}<tr><td>{{.Name}}{{if not .Available}} <span class="warn">missing</span>{{end}}</td><td>{{.Health}}</td><td>{{bytes .Allocated}}</td><td>{{bytes .Free}}</td></tr>{{end}}</table>{{else}}<p class="muted">Not detected yet. Spacey continues normally without ZFS.</p>{{end}}</section>
</div><section class="card" style="margin-top:18px"><h2>Historical inventories</h2><p class="warn">Archived inventory — files shown may no longer exist. This view is PostgreSQL-only.</p><table><tr><th>Scan</th><th>Root</th><th>Status</th><th>Files</th><th>Logical size</th></tr>{{range .Scans}}<tr><td><a href="#" onclick="browse('{{.ID}}');return false"><code>{{.ID}}</code></a></td><td>{{.RootPath}}</td><td>{{.Status}}</td><td>{{.TotalFiles}}</td><td>{{bytes .LogicalBytes}}</td></tr>{{end}}</table><div id="browser"></div></section></main><script>
async function addSource(e){e.preventDefault();let f=new FormData(e.target),r=await fetch('/api/v1/storage',{method:'POST',headers:{'content-type':'application/json'},body:JSON.stringify({Name:f.get('name'),CanonicalRoot:f.get('canonical'),ContainerRoot:f.get('container')})}),x=await r.json();if(!r.ok)alert(x.error);else location.reload()}
async function scan(id,b){b.disabled=true;let r=await fetch('/api/v1/storage/'+id+'/scan',{method:'POST'}),x=await r.json();if(!r.ok){alert(x.error);b.disabled=false;return}let es=new EventSource('/api/v1/scans/'+x.id+'/events');es.addEventListener('progress',e=>{let p=JSON.parse(e.data);document.querySelector('#progress').textContent=JSON.stringify(p,null,2);if(p.status!=='running'){es.close();location.reload()}})}
async function discover(){let r=await fetch('/api/v1/zfs/discover',{method:'POST'}),x=await r.json();if(!r.ok)alert(x.error);else location.reload()}
async function browse(id,parent=''){let u='/api/v1/scans/'+id+'/nodes'+(parent?'?parent='+encodeURIComponent(parent):'');let x=await(await fetch(u)).json();document.querySelector('#browser').innerHTML='<h3>Stored inventory</h3><table><tr><th>Name</th><th>Type</th><th>Size</th></tr>'+x.map(n=>'<tr><td><a href="#" onclick="browse(\''+id+'\',\''+n.CanonicalPath.replaceAll("'","\\'")+'\');return false">'+n.Name+'</a></td><td>'+n.Type+'</td><td>'+n.LogicalSize+'</td></tr>').join('')+'</table>'}
</script></body></html>`
