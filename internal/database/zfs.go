package database

import (
	"context"
	"spacey/internal/model"
)

func (d *DB) SaveZFS(ctx context.Context, host int64, pools []model.ZFSPool, sets []model.ZFSDataset) error {
	tx, e := d.Pool.Begin(ctx)
	if e != nil {
		return e
	}
	defer tx.Rollback(ctx)
	_, e = tx.Exec(ctx, "UPDATE zfs_pools SET available=false WHERE host_id=$1", host)
	if e != nil {
		return e
	}
	ids := map[string]int64{}
	for _, p := range pools {
		var id int64
		e = tx.QueryRow(ctx, `INSERT INTO zfs_pools(host_id,name,available,health,size_bytes,allocated_bytes,free_bytes,fragmentation_percent,last_seen_at) VALUES($1,$2,true,$3,$4,$5,$6,$7,now()) ON CONFLICT(host_id,name) DO UPDATE SET available=true,health=excluded.health,size_bytes=excluded.size_bytes,allocated_bytes=excluded.allocated_bytes,free_bytes=excluded.free_bytes,fragmentation_percent=excluded.fragmentation_percent,last_seen_at=now() RETURNING id`, host, p.Name, p.Health, p.Size, p.Allocated, p.Free, p.Fragmentation).Scan(&id)
		if e != nil {
			return e
		}
		ids[p.Name] = id
	}
	for _, s := range sets {
		pool := stringsBefore(s.Name, "/")
		id := ids[pool]
		_, e = tx.Exec(ctx, `INSERT INTO zfs_datasets(pool_id,name,type,mountpoint,used_bytes,available_bytes,referenced_bytes,logical_used_bytes,logical_referenced_bytes,compression_ratio,last_seen_at) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,now()) ON CONFLICT(name) DO UPDATE SET pool_id=excluded.pool_id,type=excluded.type,mountpoint=excluded.mountpoint,used_bytes=excluded.used_bytes,available_bytes=excluded.available_bytes,referenced_bytes=excluded.referenced_bytes,logical_used_bytes=excluded.logical_used_bytes,logical_referenced_bytes=excluded.logical_referenced_bytes,compression_ratio=excluded.compression_ratio,last_seen_at=now()`, id, s.Name, s.Type, s.Mountpoint, s.Used, s.Available, s.Referenced, s.LogicalUsed, s.LogicalReferenced, s.Compression)
		if e != nil {
			return e
		}
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
	pr.Close()
	dr, e := d.Pool.Query(ctx, `SELECT id,pool_id,name,type,COALESCE(mountpoint,''),COALESCE(used_bytes,0),COALESCE(available_bytes,0),COALESCE(referenced_bytes,0),COALESCE(logical_used_bytes,0),COALESCE(logical_referenced_bytes,0),COALESCE(compression_ratio,0),last_seen_at FROM zfs_datasets ORDER BY name`)
	if e != nil {
		return nil, nil, e
	}
	defer dr.Close()
	var ds []model.ZFSDataset
	for dr.Next() {
		var s model.ZFSDataset
		if e = dr.Scan(&s.ID, &s.PoolID, &s.Name, &s.Type, &s.Mountpoint, &s.Used, &s.Available, &s.Referenced, &s.LogicalUsed, &s.LogicalReferenced, &s.Compression, &s.LastSeenAt); e != nil {
			return nil, nil, e
		}
		ds = append(ds, s)
	}
	return ps, ds, dr.Err()
}
