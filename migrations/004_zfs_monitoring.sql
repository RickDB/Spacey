ALTER TABLE zfs_datasets ADD COLUMN present boolean NOT NULL DEFAULT true;
CREATE TABLE zfs_monitor_status (
  host_id bigint PRIMARY KEY REFERENCES hosts(id),
  checked_at timestamptz NOT NULL,
  detected boolean NOT NULL,
  diagnostic text NOT NULL
);
CREATE INDEX zfs_pool_snapshots_history_idx ON zfs_pool_snapshots(pool_id,captured_at DESC);
CREATE INDEX zfs_dataset_snapshots_history_idx ON zfs_dataset_snapshots(dataset_id,captured_at DESC);
