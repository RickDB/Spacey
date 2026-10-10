package database

import (
	"context"
	"github.com/jackc/pgx/v5"
	"os"
	"spacey/internal/model"
	"testing"
	"time"
)

// Uses a transaction-local temporary table; never changes persisted inventories.
func TestWriterTimestampsPostgres(t *testing.T) {
	url := os.Getenv("SPACEY_TEST_DATABASE_URL")
	if url == "" {
		t.Skip("set SPACEY_TEST_DATABASE_URL for PostgreSQL regression test")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	conn, err := pgx.Connect(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close(context.Background())
	tx, err := conn.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(context.Background())
	_, err = tx.Exec(ctx, `CREATE TEMP TABLE filesystem_nodes (
 scan_id uuid,storage_source_id bigint,zfs_dataset_id bigint,name text,canonical_path text,parent_path text,relative_path text,node_type text,
 logical_size bigint,allocated_size bigint,extension text,uid bigint,gid bigint,mode bigint,device_id bigint,inode bigint,
 modified_at timestamptz,accessed_at timestamptz,link_target text,read_error text) ON COMMIT DROP`)
	if err != nil {
		t.Fatal(err)
	}
	w := &Writer{ctx: ctx, tx: tx, scan: "00000000-0000-0000-0000-000000000001", source: 1, batch: &pgx.Batch{}}
	stamp := time.Date(2026, 10, 10, 12, 30, 0, 123456000, time.UTC)
	if err = w.Add(model.Node{Name: "normal", Type: "file", ModifiedAt: stamp, AccessedAt: stamp}); err != nil {
		t.Fatal(err)
	}
	if err = w.Add(model.Node{Name: "unreadable", Type: "other"}); err != nil {
		t.Fatal(err)
	}
	if err = w.flush(); err != nil {
		t.Fatal(err)
	}
	var modified, accessed time.Time
	if err = tx.QueryRow(ctx, `SELECT modified_at,accessed_at FROM filesystem_nodes WHERE name='normal'`).Scan(&modified, &accessed); err != nil {
		t.Fatal(err)
	}
	if !modified.Equal(stamp) || !accessed.Equal(stamp) {
		t.Fatalf("timestamps changed: %v %v", modified, accessed)
	}
	var nulls bool
	if err = tx.QueryRow(ctx, `SELECT modified_at IS NULL AND accessed_at IS NULL FROM filesystem_nodes WHERE name='unreadable'`).Scan(&nulls); err != nil {
		t.Fatal(err)
	}
	if !nulls {
		t.Fatal("unknown timestamps must be SQL NULL")
	}
}
