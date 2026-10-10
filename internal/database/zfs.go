package database

import (
	"context"
	"errors"
	"fmt"
	"github.com/jackc/pgx/v5"
	"spacey/internal/model"
	"time"
)

func (d *DB) SaveZFS(ctx context.Context, host int64, pools []model.ZFSPool, sets []model.ZFSDataset) error {
	tx, e := d.Pool.Begin(ctx)
	if e != nil {
		return e
	}
	defer tx.Rollback(ctx)
	captured := time.Now().UTC()
	if _, e = tx.Exec(ctx, "UPDATE zfs_pools SET available=false WHERE host_id=$1", host); e != nil {
		return e
	}
	if _, e = tx.Exec(ctx, "UPDATE zfs_datasets SET present=false WHERE pool_id IN (SELECT id FROM zfs_pools WHERE host_id=$1)", host); e != nil {
		return e
	}
	ids := map[string]int64{}
	for _, p := range pools {
		var id int64
		e = tx.QueryRow(ctx, `INSERT INTO zfs_pools(host_id,name,available,health,size_bytes,allocated_bytes,free_bytes,fragmentation_percent,last_seen_at) VALUES($1,$2,true,$3,$4,$5,$6,$7,$8) ON CONFLICT(host_id,name) DO UPDATE SET available=true,health=excluded.health,size_bytes=excluded.size_bytes,allocated_bytes=excluded.allocated_bytes,free_bytes=excluded.free_bytes,fragmentation_percent=excluded.fragmentation_percent,last_seen_at=excluded.last_seen_at RETURNING id`, host, p.Name, p.Health, p.Size, p.Allocated, p.Free, p.Fragmentation, captured).Scan(&id)
		if e != nil {
			return e
		}
		ids[p.Name] = id
		_, e = tx.Exec(ctx, `INSERT INTO zfs_pool_snapshots(pool_id,captured_at,health,size_bytes,allocated_bytes,free_bytes,fragmentation_percent) VALUES($1,$2,$3,$4,$5,$6,$7)`, id, captured, p.Health, p.Size, p.Allocated, p.Free, p.Fragmentation)
		if e != nil {
			return e
		}
	}
	for _, s := range sets {
		pool := stringsBefore(s.Name, "/")
		poolID := ids[pool]
		if poolID == 0 {
			return fmt.Errorf("dataset %q has no discovered pool", s.Name)
		}
		var id int64
		e = tx.QueryRow(ctx, `INSERT INTO zfs_datasets(pool_id,name,type,mountpoint,used_bytes,available_bytes,referenced_bytes,logical_used_bytes,logical_referenced_bytes,compression_ratio,last_seen_at,present) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,true) ON CONFLICT(name) DO UPDATE SET pool_id=excluded.pool_id,type=excluded.type,mountpoint=excluded.mountpoint,used_bytes=excluded.used_bytes,available_bytes=excluded.available_bytes,referenced_bytes=excluded.referenced_bytes,logical_used_bytes=excluded.logical_used_bytes,logical_referenced_bytes=excluded.logical_referenced_bytes,compression_ratio=excluded.compression_ratio,last_seen_at=excluded.last_seen_at,present=true RETURNING id`, poolID, s.Name, s.Type, s.Mountpoint, s.Used, s.Available, s.Referenced, s.LogicalUsed, s.LogicalReferenced, s.Compression, captured).Scan(&id)
		if e != nil {
			return e
		}
		_, e = tx.Exec(ctx, `INSERT INTO zfs_dataset_snapshots(dataset_id,captured_at,used_bytes,available_bytes,referenced_bytes,logical_used_bytes,logical_referenced_bytes,compression_ratio) VALUES($1,$2,$3,$4,$5,$6,$7,$8)`, id, captured, s.Used, s.Available, s.Referenced, s.LogicalUsed, s.LogicalReferenced, s.Compression)
		if e != nil {
			return e
		}
	}
	_, e = tx.Exec(ctx, `INSERT INTO zfs_monitor_status(host_id,checked_at,detected,diagnostic) VALUES($1,$2,true,'ZFS detected') ON CONFLICT(host_id) DO UPDATE SET checked_at=excluded.checked_at,detected=true,diagnostic=excluded.diagnostic`, host, captured)
	if e != nil {
		return e
	}
	return tx.Commit(ctx)
}
func stringsBefore(s, sep string) string {
	for i := 0; i < len(s); i++ {
		if s[i:i+1] == sep {
			return s[:i]
		}
	}
	return s
}
func (d *DB) SaveZFSFailure(ctx context.Context, host int64, diagnostic string) error {
	_, e := d.Pool.Exec(ctx, `INSERT INTO zfs_monitor_status(host_id,checked_at,detected,diagnostic) VALUES($1,now(),false,$2) ON CONFLICT(host_id) DO UPDATE SET checked_at=excluded.checked_at,detected=false,diagnostic=excluded.diagnostic`, host, diagnostic)
	return e
}

type ZFSMonitorStatus struct {
	CheckedAt  time.Time `json:"checked_at"`
	Detected   bool      `json:"detected"`
	Diagnostic string    `json:"diagnostic"`
}

func (d *DB) ZFSStatus(ctx context.Context, host int64) (*ZFSMonitorStatus, error) {
	var s ZFSMonitorStatus
	e := d.Pool.QueryRow(ctx, `SELECT checked_at,detected,diagnostic FROM zfs_monitor_status WHERE host_id=$1`, host).Scan(&s.CheckedAt, &s.Detected, &s.Diagnostic)
	if e != nil {
		if errors.Is(e, pgx.ErrNoRows) {
			return nil, nil
		}
		return nil, e
	}
	return &s, nil
}
func (d *DB) ZFS(ctx context.Context) ([]model.ZFSPool, []model.ZFSDataset, error) {
	pr, e := d.Pool.Query(ctx, `SELECT id,name,COALESCE(health,''),available,COALESCE(size_bytes,0),COALESCE(allocated_bytes,0),COALESCE(free_bytes,0),COALESCE(fragmentation_percent,0),last_seen_at FROM zfs_pools ORDER BY name`)
	if e != nil {
		return nil, nil, e
	}
	var ps []model.ZFSPool
	for pr.Next() {
		var p model.ZFSPool
		if e = pr.Scan(&p.ID, &p.Name, &p.Health, &p.Available, &p.Size, &p.Allocated, &p.Free, &p.Fragmentation, &p.LastSeenAt); e != nil {
			pr.Close()
			return nil, nil, e
		}
		ps = append(ps, p)
	}
	e = pr.Err()
	pr.Close()
	if e != nil {
		return nil, nil, e
	}
	dr, e := d.Pool.Query(ctx, `SELECT id,pool_id,name,type,COALESCE(mountpoint,''),COALESCE(used_bytes,0),COALESCE(available_bytes,0),COALESCE(referenced_bytes,0),COALESCE(logical_used_bytes,0),COALESCE(logical_referenced_bytes,0),COALESCE(compression_ratio,0),last_seen_at,present FROM zfs_datasets ORDER BY name`)
	if e != nil {
		return nil, nil, e
	}
	defer dr.Close()
	var ds []model.ZFSDataset
	for dr.Next() {
		var s model.ZFSDataset
		if e = dr.Scan(&s.ID, &s.PoolID, &s.Name, &s.Type, &s.Mountpoint, &s.Used, &s.Available, &s.Referenced, &s.LogicalUsed, &s.LogicalReferenced, &s.Compression, &s.LastSeenAt, &s.Present); e != nil {
			return nil, nil, e
		}
		ds = append(ds, s)
	}
	return ps, ds, dr.Err()
}
