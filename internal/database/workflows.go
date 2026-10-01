package database

import (
	"context"
	"encoding/json"
	"fmt"
	"spacey/internal/model"
	"time"
)

type Filter struct {
	ID              int64           `json:"id"`
	Name            string          `json:"name"`
	StorageSourceID *int64          `json:"storage_source_id"`
	Criteria        json.RawMessage `json:"criteria"`
}
type Schedule struct {
	ID              int64      `json:"id"`
	StorageSourceID int64      `json:"storage_source_id"`
	Name            string     `json:"name"`
	IntervalMinutes int        `json:"interval_minutes"`
	FilterID        *int64     `json:"filter_id"`
	Enabled         bool       `json:"enabled"`
	NextRunAt       time.Time  `json:"next_run_at"`
	LastRunAt       *time.Time `json:"last_run_at"`
}

func (d *DB) Filters(ctx context.Context) ([]Filter, error) {
	rows, e := d.Pool.Query(ctx, `SELECT id,name,storage_source_id,criteria FROM saved_filters ORDER BY name`)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	out := []Filter{}
	for rows.Next() {
		var v Filter
		if e = rows.Scan(&v.ID, &v.Name, &v.StorageSourceID, &v.Criteria); e != nil {
			return nil, e
		}
		out = append(out, v)
	}
	return out, rows.Err()
}
func (d *DB) AddFilter(ctx context.Context, v Filter) (Filter, error) {
	e := d.Pool.QueryRow(ctx, `INSERT INTO saved_filters(name,storage_source_id,criteria) VALUES($1,$2,$3) RETURNING id`, v.Name, v.StorageSourceID, v.Criteria).Scan(&v.ID)
	return v, e
}
func (d *DB) DeleteFilter(ctx context.Context, id int64) error {
	tag, e := d.Pool.Exec(ctx, `DELETE FROM saved_filters WHERE id=$1`, id)
	if e != nil {
		return e
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("filter not found")
	}
	return nil
}
func (d *DB) Schedules(ctx context.Context) ([]Schedule, error) {
	rows, e := d.Pool.Query(ctx, `SELECT id,storage_source_id,name,interval_minutes,filter_id,enabled,next_run_at,last_run_at FROM scan_schedules ORDER BY id`)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	out := []Schedule{}
	for rows.Next() {
		var v Schedule
		if e = rows.Scan(&v.ID, &v.StorageSourceID, &v.Name, &v.IntervalMinutes, &v.FilterID, &v.Enabled, &v.NextRunAt, &v.LastRunAt); e != nil {
			return nil, e
		}
		out = append(out, v)
	}
	return out, rows.Err()
}
func (d *DB) AddSchedule(ctx context.Context, v Schedule) (Schedule, error) {
	e := d.Pool.QueryRow(ctx, `INSERT INTO scan_schedules(storage_source_id,name,interval_minutes,filter_id,enabled,next_run_at) VALUES($1,$2,$3,$4,$5,now()+$3*interval '1 minute') RETURNING id,next_run_at`, v.StorageSourceID, v.Name, v.IntervalMinutes, v.FilterID, v.Enabled).Scan(&v.ID, &v.NextRunAt)
	return v, e
}
func (d *DB) DeleteSchedule(ctx context.Context, id int64) error {
	tag, e := d.Pool.Exec(ctx, `DELETE FROM scan_schedules WHERE id=$1`, id)
	if e != nil {
		return e
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("schedule not found")
	}
	return nil
}
func (d *DB) DueSchedules(ctx context.Context) ([]Schedule, error) {
	rows, e := d.Pool.Query(ctx, `SELECT id,storage_source_id,name,interval_minutes,filter_id,enabled,next_run_at,last_run_at FROM scan_schedules WHERE enabled AND next_run_at<=now() ORDER BY next_run_at LIMIT 20`)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	out := []Schedule{}
	for rows.Next() {
		var v Schedule
		if e = rows.Scan(&v.ID, &v.StorageSourceID, &v.Name, &v.IntervalMinutes, &v.FilterID, &v.Enabled, &v.NextRunAt, &v.LastRunAt); e != nil {
			return nil, e
		}
		out = append(out, v)
	}
	return out, rows.Err()
}
func (d *DB) AdvanceSchedule(ctx context.Context, id int64, minutes int) error {
	_, e := d.Pool.Exec(ctx, `UPDATE scan_schedules SET last_run_at=now(),next_run_at=now()+$2*interval '1 minute' WHERE id=$1`, id, minutes)
	return e
}

type Overview struct {
	TotalBytes  int64        `json:"total_bytes"`
	Files       int64        `json:"files"`
	Directories int64        `json:"directories"`
	VideoBytes  int64        `json:"video_bytes"`
	VideoFiles  int64        `json:"video_files"`
	Largest     []model.Node `json:"largest"`
	Folders     []model.Node `json:"folders"`
	Types       []TypeSize   `json:"types"`
}
type TypeSize struct {
	Extension string `json:"extension"`
	Bytes     int64  `json:"bytes"`
	Files     int64  `json:"files"`
}

func (d *DB) Overview(ctx context.Context, scan string) (Overview, error) {
	var v Overview
	e := d.Pool.QueryRow(ctx, `SELECT coalesce(sum(logical_size),0),count(*),coalesce(sum(logical_size) FILTER (WHERE extension IN ('.mkv','.mp4','.avi','.mov','.m4v','.ts','.wmv')),0),count(*) FILTER (WHERE extension IN ('.mkv','.mp4','.avi','.mov','.m4v','.ts','.wmv')) FROM filesystem_nodes WHERE scan_id=$1 AND node_type='file'`, scan).Scan(&v.TotalBytes, &v.Files, &v.VideoBytes, &v.VideoFiles)
	if e != nil {
		return v, e
	}
	e = d.Pool.QueryRow(ctx, `SELECT count(*) FROM filesystem_nodes WHERE scan_id=$1 AND node_type='directory'`, scan).Scan(&v.Directories)
	if e != nil {
		return v, e
	}
	var root string
	if e = d.Pool.QueryRow(ctx, `SELECT root_path FROM scans WHERE id=$1`, scan).Scan(&root); e != nil {
		return v, e
	}
	v.Folders, e = d.Nodes(ctx, scan, root, "", 50)
	if e != nil {
		return v, e
	}
	v.Largest, e = d.Largest(ctx, scan, "file", 8)
	if e != nil {
		return v, e
	}
	rows, e := d.Pool.Query(ctx, `SELECT coalesce(nullif(extension,''),'(none)'),sum(logical_size),count(*) FROM filesystem_nodes WHERE scan_id=$1 AND node_type='file' GROUP BY 1 ORDER BY 2 DESC LIMIT 8`, scan)
	if e != nil {
		return v, e
	}
	defer rows.Close()
	v.Types = []TypeSize{}
	for rows.Next() {
		var t TypeSize
		if e = rows.Scan(&t.Extension, &t.Bytes, &t.Files); e != nil {
			return v, e
		}
		v.Types = append(v.Types, t)
	}
	return v, rows.Err()
}
func (d *DB) SearchNodesPage(ctx context.Context, scan, q, kind string, minBytes, afterSize, afterID int64, limit int) ([]model.Node, error) {
	if limit < 1 || limit > 1000 {
		limit = 1000
	}
	if kind != "file" && kind != "directory" {
		kind = ""
	}
	rows, e := d.Pool.Query(ctx, `SELECT id,coalesce(parent_id,0),name,canonical_path,coalesce(parent_path,''),relative_path,node_type,logical_size,allocated_size,coalesce(extension,''),coalesce(mode,0),coalesce(modified_at,'epoch') FROM filesystem_nodes WHERE scan_id=$1 AND ($2='' OR canonical_path ILIKE '%'||$2||'%') AND ($3='' OR node_type=$3) AND logical_size >= $4 AND ($5=0 OR (logical_size,id)<($6,$5)) ORDER BY logical_size DESC,id DESC LIMIT $7`, scan, q, kind, minBytes, afterID, afterSize, limit)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	out := []model.Node{}
	for rows.Next() {
		var n model.Node
		if e = rows.Scan(&n.ID, &n.ParentID, &n.Name, &n.CanonicalPath, &n.ParentPath, &n.RelativePath, &n.Type, &n.LogicalSize, &n.AllocatedSize, &n.Extension, &n.Mode, &n.ModifiedAt); e != nil {
			return nil, e
		}
		out = append(out, n)
	}
	return out, rows.Err()
}

type TrashItem struct {
	ID              string     `json:"id"`
	StorageSourceID int64      `json:"storage_source_id"`
	ScanID          string     `json:"scan_id"`
	OriginalPath    string     `json:"original_path"`
	TrashPath       string     `json:"trash_path"`
	LogicalSize     int64      `json:"logical_size"`
	MovedAt         time.Time  `json:"moved_at"`
	RestoredAt      *time.Time `json:"restored_at"`
}

func (d *DB) NodeForTrash(ctx context.Context, scan string, id int64) (model.Node, error) {
	var n model.Node
	e := d.Pool.QueryRow(ctx, `SELECT id,name,canonical_path,node_type,logical_size FROM filesystem_nodes WHERE scan_id=$1 AND id=$2`, scan, id).Scan(&n.ID, &n.Name, &n.CanonicalPath, &n.Type, &n.LogicalSize)
	return n, e
}
func (d *DB) AddTrash(ctx context.Context, t TrashItem) error {
	_, e := d.Pool.Exec(ctx, `INSERT INTO trash_items(id,storage_source_id,scan_id,original_path,trash_path,logical_size) VALUES($1,$2,$3,$4,$5,$6)`, t.ID, t.StorageSourceID, t.ScanID, t.OriginalPath, t.TrashPath, t.LogicalSize)
	return e
}
func (d *DB) DeleteTrashRecord(ctx context.Context, id string) {
	_, _ = d.Pool.Exec(ctx, `DELETE FROM trash_items WHERE id=$1`, id)
}
func (d *DB) Trash(ctx context.Context) ([]TrashItem, error) {
	rows, e := d.Pool.Query(ctx, `SELECT id::text,storage_source_id,scan_id::text,original_path,trash_path,logical_size,moved_at,restored_at FROM trash_items ORDER BY moved_at DESC LIMIT 500`)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	out := []TrashItem{}
	for rows.Next() {
		var t TrashItem
		if e = rows.Scan(&t.ID, &t.StorageSourceID, &t.ScanID, &t.OriginalPath, &t.TrashPath, &t.LogicalSize, &t.MovedAt, &t.RestoredAt); e != nil {
			return nil, e
		}
		out = append(out, t)
	}
	return out, rows.Err()
}
func (d *DB) TrashOne(ctx context.Context, id string) (TrashItem, error) {
	var t TrashItem
	e := d.Pool.QueryRow(ctx, `SELECT id::text,storage_source_id,scan_id::text,original_path,trash_path,logical_size,moved_at,restored_at FROM trash_items WHERE id=$1`, id).Scan(&t.ID, &t.StorageSourceID, &t.ScanID, &t.OriginalPath, &t.TrashPath, &t.LogicalSize, &t.MovedAt, &t.RestoredAt)
	return t, e
}
func (d *DB) MarkRestored(ctx context.Context, id string) error {
	_, e := d.Pool.Exec(ctx, `UPDATE trash_items SET restored_at=now() WHERE id=$1 AND restored_at IS NULL`, id)
	return e
}
