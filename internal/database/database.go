package database

import (
	"context"
	"errors"
	"fmt"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"io"
	"os"
	"spacey/internal/model"
	"sync"
)

type DB struct{ Pool *pgxpool.Pool }

func Open(ctx context.Context, url string) (*DB, error) {
	p, e := pgxpool.New(ctx, url)
	if e != nil {
		return nil, e
	}
	if e = p.Ping(ctx); e != nil {
		p.Close()
		return nil, e
	}
	d := &DB{p}
	return d, d.Migrate(ctx)
}
func (d *DB) Migrate(ctx context.Context) error {
	if _, e := d.Pool.Exec(ctx, "CREATE TABLE IF NOT EXISTS schema_migrations(version text PRIMARY KEY, applied_at timestamptz NOT NULL DEFAULT now())"); e != nil {
		return e
	}
	for _, version := range []string{"001_initial", "002_workflows", "003_settings", "004_zfs_monitoring"} {
		var done bool
		if e := d.Pool.QueryRow(ctx, "SELECT EXISTS(SELECT 1 FROM schema_migrations WHERE version=$1)", version).Scan(&done); e != nil {
			return e
		}
		if done {
			continue
		}
		b, e := os.ReadFile("migrations/" + version + ".sql")
		if e != nil {
			return fmt.Errorf("load migration %s: %w", version, e)
		}
		tx, e := d.Pool.Begin(ctx)
		if e != nil {
			return e
		}
		if _, e = tx.Exec(ctx, string(b)); e == nil {
			_, e = tx.Exec(ctx, "INSERT INTO schema_migrations(version) VALUES($1)", version)
		}
		if e != nil {
			_ = tx.Rollback(ctx)
			return e
		}
		if e = tx.Commit(ctx); e != nil {
			return e
		}
	}
	return nil
}
func (d *DB) EnsureHost(ctx context.Context, name string) (int64, error) {
	var id int64
	e := d.Pool.QueryRow(ctx, "INSERT INTO hosts(name) VALUES($1) ON CONFLICT(name) DO UPDATE SET name=excluded.name RETURNING id", name).Scan(&id)
	return id, e
}
func (d *DB) CreateSource(ctx context.Context, host int64, name, canonical, container string) (model.StorageSource, error) {
	var s model.StorageSource
	e := d.Pool.QueryRow(ctx, `INSERT INTO storage_sources(host_id,name,canonical_root,container_root,last_available_at) VALUES($1,$2,$3,$4,CASE WHEN $5 THEN now() END) ON CONFLICT(host_id,canonical_root) DO UPDATE SET name=excluded.name,container_root=excluded.container_root RETURNING id,name,canonical_root,container_root,enabled,last_available_at`, host, name, canonical, container, pathExists(container)).Scan(&s.ID, &s.Name, &s.CanonicalRoot, &s.ContainerRoot, &s.Enabled, &s.LastAvailableAt)
	s.Available, s.AvailabilityError = sourceAvailability(container)
	return s, e
}
func (d *DB) UpdateSource(ctx context.Context, host, id int64, name, canonical, container string) (model.StorageSource, error) {
	var s model.StorageSource
	available := pathExists(container)
	e := d.Pool.QueryRow(ctx, `UPDATE storage_sources
		SET name=$3,canonical_root=$4,container_root=$5,last_available_at=CASE WHEN $6 THEN now() ELSE last_available_at END
		WHERE id=$2 AND host_id=$1
		RETURNING id,name,canonical_root,container_root,enabled,last_available_at`,
		host, id, name, canonical, container, available,
	).Scan(&s.ID, &s.Name, &s.CanonicalRoot, &s.ContainerRoot, &s.Enabled, &s.LastAvailableAt)
	s.Available, s.AvailabilityError = sourceAvailability(container)
	return s, e
}
func (d *DB) Sources(ctx context.Context) ([]model.StorageSource, error) {
	rows, e := d.Pool.Query(ctx, "SELECT id,name,canonical_root,container_root,enabled,last_available_at FROM storage_sources ORDER BY name")
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	var out []model.StorageSource
	for rows.Next() {
		var s model.StorageSource
		if e = rows.Scan(&s.ID, &s.Name, &s.CanonicalRoot, &s.ContainerRoot, &s.Enabled, &s.LastAvailableAt); e != nil {
			return nil, e
		}
		s.Available, s.AvailabilityError = sourceAvailability(s.ContainerRoot)
		out = append(out, s)
	}
	return out, rows.Err()
}
func (d *DB) Source(ctx context.Context, id int64) (model.StorageSource, error) {
	var s model.StorageSource
	e := d.Pool.QueryRow(ctx, "SELECT id,name,canonical_root,container_root,enabled,last_available_at FROM storage_sources WHERE id=$1", id).Scan(&s.ID, &s.Name, &s.CanonicalRoot, &s.ContainerRoot, &s.Enabled, &s.LastAvailableAt)
	s.Available, s.AvailabilityError = sourceAvailability(s.ContainerRoot)
	return s, e
}
func pathExists(p string) bool { ok, _ := sourceAvailability(p); return ok }
func sourceAvailability(p string) (bool, string) {
	f, err := os.Open(p)
	if err != nil {
		return false, err.Error()
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return false, err.Error()
	}
	if !info.IsDir() {
		return false, "container path is not a directory"
	}
	if _, err = f.Readdirnames(1); err != nil && err != io.EOF {
		return false, err.Error()
	}
	return true, ""
}
func (d *DB) CreateScan(ctx context.Context, id string, source int64, root string) error {
	_, e := d.Pool.Exec(ctx, "INSERT INTO scans(id,storage_source_id,status,scanner_version,root_path,started_at) VALUES($1,$2,'running','0.1.0',$3,now())", id, source, root)
	return e
}
func (d *DB) DatasetForPath(ctx context.Context, path string) (int64, error) {
	var id int64
	e := d.Pool.QueryRow(ctx, `SELECT id FROM zfs_datasets WHERE present AND mountpoint NOT IN ('','-','none','legacy') AND ($1=mountpoint OR $1 LIKE mountpoint||'/%') ORDER BY length(mountpoint) DESC LIMIT 1`, path).Scan(&id)
	if errors.Is(e, pgx.ErrNoRows) {
		return 0, nil
	}
	return id, e
}
func (d *DB) LinkAndCaptureZFS(ctx context.Context, scan string, datasetID int64) error {
	tx, e := d.Pool.Begin(ctx)
	if e != nil {
		return e
	}
	defer tx.Rollback(ctx)
	if datasetID != 0 {
		if _, e = tx.Exec(ctx, "UPDATE scans SET zfs_dataset_id=$2 WHERE id=$1", scan, datasetID); e != nil {
			return e
		}
	}
	if _, e = tx.Exec(ctx, `INSERT INTO zfs_pool_snapshots(scan_id,pool_id,health,size_bytes,allocated_bytes,free_bytes,fragmentation_percent) SELECT $1,id,health,size_bytes,allocated_bytes,free_bytes,fragmentation_percent FROM zfs_pools WHERE available`, scan); e != nil {
		return e
	}
	if _, e = tx.Exec(ctx, `INSERT INTO zfs_dataset_snapshots(scan_id,dataset_id,used_bytes,available_bytes,referenced_bytes,logical_used_bytes,logical_referenced_bytes,compression_ratio) SELECT $1,id,used_bytes,available_bytes,referenced_bytes,logical_used_bytes,logical_referenced_bytes,compression_ratio FROM zfs_datasets`, scan); e != nil {
		return e
	}
	return tx.Commit(ctx)
}
func (d *DB) LatestScanID(ctx context.Context, source int64) (string, error) {
	var id string
	e := d.Pool.QueryRow(ctx, `SELECT id::text FROM scans WHERE storage_source_id=$1 AND status IN ('completed','completed_with_errors') ORDER BY completed_at DESC LIMIT 1`, source).Scan(&id)
	return id, e
}

type Writer struct {
	ctx             context.Context
	tx              pgx.Tx
	scan            string
	source, dataset int64
	batch           *pgx.Batch
	n               int
	mu              sync.Mutex
}

func (d *DB) NewWriter(ctx context.Context, scan string, source, dataset int64) (*Writer, error) {
	tx, e := d.Pool.Begin(ctx)
	if e != nil {
		return nil, e
	}
	return &Writer{ctx: ctx, tx: tx, scan: scan, source: source, dataset: dataset, batch: &pgx.Batch{}}, nil
}
func (w *Writer) Add(n model.Node) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.batch.Queue(`INSERT INTO filesystem_nodes(scan_id,storage_source_id,zfs_dataset_id,name,canonical_path,parent_path,relative_path,node_type,logical_size,allocated_size,extension,uid,gid,mode,device_id,inode,modified_at,accessed_at,link_target,read_error) VALUES($1,$2,NULLIF($3,0),$4,$5,$6,$7,$8,$9,$10,NULLIF($11,''),$12,$13,$14,$15,$16,NULLIF($17,'0001-01-01'),NULLIF($18,'0001-01-01'),NULLIF($19,''),NULLIF($20,''))`, w.scan, w.source, w.dataset, n.Name, n.CanonicalPath, n.ParentPath, n.RelativePath, n.Type, n.LogicalSize, n.AllocatedSize, n.Extension, n.UID, n.GID, n.Mode, n.DeviceID, n.Inode, n.ModifiedAt, n.AccessedAt, n.LinkTarget, n.ReadError)
	w.n++
	if w.n >= 500 {
		return w.flush()
	}
	return nil
}
func (w *Writer) flush() error {
	if w.n == 0 {
		return nil
	}
	br := w.tx.SendBatch(w.ctx, w.batch)
	e := br.Close()
	w.batch = &pgx.Batch{}
	w.n = 0
	return e
}
func (w *Writer) Commit(p model.Progress) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if e := w.flush(); e != nil {
		_ = w.tx.Rollback(w.ctx)
		return e
	}
	_, e := w.tx.Exec(w.ctx, `UPDATE filesystem_nodes n SET parent_id=p.id FROM filesystem_nodes p WHERE n.scan_id=$1 AND p.scan_id=n.scan_id AND n.parent_path=p.canonical_path`, w.scan)
	if e == nil {
		_, e = w.tx.Exec(w.ctx, `WITH RECURSIVE rollup AS (
		 SELECT parent_id AS ancestor,logical_size,allocated_size,(node_type='file')::int AS files,(node_type='directory')::int AS dirs
		 FROM filesystem_nodes WHERE scan_id=$1 AND parent_id IS NOT NULL
		 UNION ALL SELECT p.parent_id,r.logical_size,r.allocated_size,r.files,r.dirs
		 FROM rollup r JOIN filesystem_nodes p ON p.id=r.ancestor WHERE p.parent_id IS NOT NULL
		), totals AS (
		 SELECT ancestor,sum(logical_size) logical,sum(allocated_size) allocated,sum(files) files,sum(dirs) dirs
		 FROM rollup GROUP BY ancestor
		) UPDATE filesystem_nodes n SET logical_size=t.logical,allocated_size=t.allocated,file_count=t.files,directory_count=t.dirs
		FROM totals t WHERE n.id=t.ancestor AND n.scan_id=$1 AND n.node_type='directory'`, w.scan)
	}
	if e == nil {
		_, e = w.tx.Exec(w.ctx, `UPDATE scans SET status=$2,completed_at=now(),total_files=$3,total_directories=$4,logical_bytes=$5,allocated_bytes=$6,errors_count=$7 WHERE id=$1`, w.scan, p.Status, p.Files, p.Directories, p.LogicalBytes, p.AllocatedBytes, p.Errors)
	}
	if e != nil {
		_ = w.tx.Rollback(w.ctx)
		return e
	}
	return w.tx.Commit(w.ctx)
}
func (w *Writer) Rollback() { _ = w.tx.Rollback(w.ctx) }
func (d *DB) FailScan(ctx context.Context, id, status string, err error) {
	_, _ = d.Pool.Exec(ctx, "UPDATE scans SET status=$2,completed_at=now(),error_message=$3 WHERE id=$1", id, status, err.Error())
}
func (d *DB) Scans(ctx context.Context) ([]model.Scan, error) {
	rows, e := d.Pool.Query(ctx, `SELECT id::text,storage_source_id,status,scanner_version,root_path,started_at,completed_at,total_files,total_directories,logical_bytes,allocated_bytes,errors_count,error_message FROM scans ORDER BY created_at DESC`)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	var out []model.Scan
	for rows.Next() {
		var s model.Scan
		if e = rows.Scan(&s.ID, &s.StorageSourceID, &s.Status, &s.ScannerVersion, &s.RootPath, &s.StartedAt, &s.CompletedAt, &s.TotalFiles, &s.TotalDirectories, &s.LogicalBytes, &s.AllocatedBytes, &s.ErrorsCount, &s.ErrorMessage); e != nil {
			return nil, e
		}
		out = append(out, s)
	}
	return out, rows.Err()
}
func (d *DB) Nodes(ctx context.Context, scan, parent, q string, limit int) ([]model.Node, error) {
	if limit < 1 || limit > 1000 {
		limit = 250
	}
	sql := `SELECT id,COALESCE(parent_id,0),name,canonical_path,COALESCE(parent_path,''),relative_path,node_type,logical_size,allocated_size,COALESCE(extension,''),COALESCE(mode,0),COALESCE(modified_at,'epoch') FROM filesystem_nodes WHERE scan_id=$1`
	args := []any{scan}
	if q != "" {
		sql += " AND canonical_path ILIKE $2"
		args = append(args, "%"+q+"%")
	} else if parent != "" {
		sql += " AND parent_path=$2"
		args = append(args, parent)
	} else {
		sql += " AND parent_id IS NULL"
	}
	sql += fmt.Sprintf(" ORDER BY node_type='directory' DESC, logical_size DESC, name LIMIT $%d", len(args)+1)
	args = append(args, limit)
	rows, e := d.Pool.Query(ctx, sql, args...)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	var out []model.Node
	for rows.Next() {
		var n model.Node
		if e = rows.Scan(&n.ID, &n.ParentID, &n.Name, &n.CanonicalPath, &n.ParentPath, &n.RelativePath, &n.Type, &n.LogicalSize, &n.AllocatedSize, &n.Extension, &n.Mode, &n.ModifiedAt); e != nil {
			return nil, e
		}
		out = append(out, n)
	}
	return out, rows.Err()
}
func (d *DB) Largest(ctx context.Context, scan, kind string, limit int) ([]model.Node, error) {
	if limit < 1 || limit > 500 {
		limit = 100
	}
	if kind != "directory" {
		kind = "file"
	}
	rows, e := d.Pool.Query(ctx, `SELECT id,COALESCE(parent_id,0),name,canonical_path,node_type,logical_size,allocated_size,COALESCE(extension,''),COALESCE(modified_at,'epoch') FROM filesystem_nodes WHERE scan_id=$1 AND node_type=$2 ORDER BY logical_size DESC LIMIT $3`, scan, kind, limit)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	var out []model.Node
	for rows.Next() {
		var n model.Node
		if e = rows.Scan(&n.ID, &n.ParentID, &n.Name, &n.CanonicalPath, &n.Type, &n.LogicalSize, &n.AllocatedSize, &n.Extension, &n.ModifiedAt); e != nil {
			return nil, e
		}
		out = append(out, n)
	}
	return out, rows.Err()
}
