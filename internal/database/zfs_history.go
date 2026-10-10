package database

import (
	"context"
	"fmt"
	"time"
)

type ZFSHistoryPoint struct {
	CapturedAt      time.Time `json:"captured_at"`
	UsedBytes       int64     `json:"used_bytes"`
	FreeBytes       int64     `json:"free_bytes"`
	CapacityBytes   int64     `json:"capacity_bytes"`
	Health          string    `json:"health,omitempty"`
	Fragmentation   float64   `json:"fragmentation_percent,omitempty"`
	ReferencedBytes int64     `json:"referenced_bytes,omitempty"`
	Compression     float64   `json:"compression_ratio,omitempty"`
}
type ZFSHistoryRange struct {
	Since  time.Time
	Bucket string
}

func ZFSRange(value string, now time.Time) (ZFSHistoryRange, error) {
	var days int
	var bucket string
	switch value {
	case "24h":
		days = 1
		bucket = "15 minutes"
	case "7d":
		days = 7
		bucket = "1 hour"
	case "30d":
		days = 30
		bucket = "4 hours"
	case "90d":
		days = 90
		bucket = "12 hours"
	case "1y":
		days = 365
		bucket = "2 days"
	default:
		return ZFSHistoryRange{}, fmt.Errorf("range must be 24h, 7d, 30d, 90d, or 1y")
	}
	return ZFSHistoryRange{Since: now.Add(-time.Duration(days) * 24 * time.Hour), Bucket: bucket}, nil
}
func (d *DB) ZFSHistory(ctx context.Context, kind string, id int64, span ZFSHistoryRange) ([]ZFSHistoryPoint, error) {
	var query string
	switch kind {
	case "pool":
		query = `SELECT captured_at,COALESCE(allocated_bytes,0),COALESCE(free_bytes,0),COALESCE(size_bytes,0),COALESCE(health,''),COALESCE(fragmentation_percent,0),0::bigint,0::numeric FROM (SELECT DISTINCT ON (date_bin($3::interval,captured_at,TIMESTAMPTZ '2000-01-01')) captured_at,allocated_bytes,free_bytes,size_bytes,health,fragmentation_percent FROM zfs_pool_snapshots WHERE pool_id=$1 AND captured_at >= $2 ORDER BY date_bin($3::interval,captured_at,TIMESTAMPTZ '2000-01-01'),captured_at DESC,id DESC) sampled ORDER BY captured_at`
	case "dataset":
		query = `SELECT captured_at,COALESCE(used_bytes,0),COALESCE(available_bytes,0),COALESCE(used_bytes,0)+COALESCE(available_bytes,0),''::text,0::numeric,COALESCE(referenced_bytes,0),COALESCE(compression_ratio,0) FROM (SELECT DISTINCT ON (date_bin($3::interval,captured_at,TIMESTAMPTZ '2000-01-01')) captured_at,used_bytes,available_bytes,referenced_bytes,compression_ratio FROM zfs_dataset_snapshots WHERE dataset_id=$1 AND captured_at >= $2 ORDER BY date_bin($3::interval,captured_at,TIMESTAMPTZ '2000-01-01'),captured_at DESC,id DESC) sampled ORDER BY captured_at`
	default:
		return nil, fmt.Errorf("kind must be pool or dataset")
	}
	rows, e := d.Pool.Query(ctx, query, id, span.Since, span.Bucket)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	points := make([]ZFSHistoryPoint, 0, 200)
	for rows.Next() {
		var p ZFSHistoryPoint
		if e = rows.Scan(&p.CapturedAt, &p.UsedBytes, &p.FreeBytes, &p.CapacityBytes, &p.Health, &p.Fragmentation, &p.ReferencedBytes, &p.Compression); e != nil {
			return nil, e
		}
		points = append(points, p)
	}
	return points, rows.Err()
}
