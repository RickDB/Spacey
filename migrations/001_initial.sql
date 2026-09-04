CREATE EXTENSION IF NOT EXISTS pg_trgm;
CREATE TABLE hosts (id bigserial PRIMARY KEY, name text NOT NULL UNIQUE, created_at timestamptz NOT NULL DEFAULT now());
CREATE TABLE storage_sources (id bigserial PRIMARY KEY, host_id bigint REFERENCES hosts(id), name text NOT NULL, canonical_root text NOT NULL, container_root text NOT NULL, provider text NOT NULL DEFAULT 'local', enabled boolean NOT NULL DEFAULT true, last_available_at timestamptz, created_at timestamptz NOT NULL DEFAULT now(), UNIQUE(host_id,canonical_root));
CREATE TABLE zfs_pools (id bigserial PRIMARY KEY, host_id bigint REFERENCES hosts(id), name text NOT NULL, available boolean NOT NULL DEFAULT true, health text, size_bytes bigint, allocated_bytes bigint, free_bytes bigint, fragmentation_percent numeric, last_seen_at timestamptz NOT NULL DEFAULT now(), UNIQUE(host_id,name));
CREATE TABLE zfs_datasets (id bigserial PRIMARY KEY, pool_id bigint REFERENCES zfs_pools(id), name text NOT NULL UNIQUE, type text NOT NULL, mountpoint text, used_bytes bigint, available_bytes bigint, referenced_bytes bigint, logical_used_bytes bigint, logical_referenced_bytes bigint, compression_ratio numeric, last_seen_at timestamptz NOT NULL DEFAULT now());
CREATE TABLE scans (id uuid PRIMARY KEY, storage_source_id bigint NOT NULL REFERENCES storage_sources(id), zfs_dataset_id bigint REFERENCES zfs_datasets(id), status text NOT NULL CHECK(status IN ('pending','running','completed','completed_with_errors','cancelled','failed')), scanner_version text NOT NULL, root_path text NOT NULL, started_at timestamptz, completed_at timestamptz, total_files bigint NOT NULL DEFAULT 0, total_directories bigint NOT NULL DEFAULT 0, logical_bytes bigint NOT NULL DEFAULT 0, allocated_bytes bigint NOT NULL DEFAULT 0, errors_count bigint NOT NULL DEFAULT 0, error_message text, created_at timestamptz NOT NULL DEFAULT now());
CREATE TABLE filesystem_nodes (
 id bigserial PRIMARY KEY, scan_id uuid NOT NULL REFERENCES scans(id) ON DELETE RESTRICT, parent_id bigint REFERENCES filesystem_nodes(id) ON DELETE RESTRICT, storage_source_id bigint NOT NULL REFERENCES storage_sources(id), zfs_dataset_id bigint REFERENCES zfs_datasets(id),
 name text NOT NULL, canonical_path text NOT NULL, parent_path text, relative_path text NOT NULL, node_type text NOT NULL CHECK(node_type IN ('file','directory','symlink','other')),
 logical_size bigint NOT NULL DEFAULT 0, allocated_size bigint NOT NULL DEFAULT 0, file_count bigint NOT NULL DEFAULT 0, directory_count bigint NOT NULL DEFAULT 0, extension text, mime_type text,
 uid bigint, gid bigint, owner_name text, group_name text, mode bigint, device_id bigint, inode bigint, modified_at timestamptz, accessed_at timestamptz, created_fs_at timestamptz,
 link_target text, read_error text, created_in_database_at timestamptz NOT NULL DEFAULT now(), UNIQUE(scan_id,canonical_path));
CREATE INDEX nodes_parent_idx ON filesystem_nodes(scan_id,parent_id,name);
CREATE INDEX nodes_parent_path_idx ON filesystem_nodes(scan_id,parent_path,name);
CREATE INDEX nodes_size_idx ON filesystem_nodes(scan_id,logical_size DESC);
CREATE INDEX nodes_allocated_idx ON filesystem_nodes(scan_id,allocated_size DESC);
CREATE INDEX nodes_modified_idx ON filesystem_nodes(scan_id,modified_at);
CREATE INDEX nodes_extension_idx ON filesystem_nodes(scan_id,extension);
CREATE INDEX nodes_path_trgm_idx ON filesystem_nodes USING gin(canonical_path gin_trgm_ops);
CREATE TABLE zfs_pool_snapshots (id bigserial PRIMARY KEY, scan_id uuid REFERENCES scans(id), pool_id bigint NOT NULL REFERENCES zfs_pools(id), captured_at timestamptz NOT NULL DEFAULT now(), health text, size_bytes bigint, allocated_bytes bigint, free_bytes bigint, fragmentation_percent numeric);
CREATE TABLE zfs_dataset_snapshots (id bigserial PRIMARY KEY, scan_id uuid REFERENCES scans(id), dataset_id bigint NOT NULL REFERENCES zfs_datasets(id), captured_at timestamptz NOT NULL DEFAULT now(), used_bytes bigint, available_bytes bigint, referenced_bytes bigint, logical_used_bytes bigint, logical_referenced_bytes bigint, compression_ratio numeric);
CREATE OR REPLACE FUNCTION protect_completed_scan() RETURNS trigger AS $$ BEGIN
 IF TG_TABLE_NAME='scans' THEN IF OLD.status IN ('completed','completed_with_errors') THEN RAISE EXCEPTION 'completed scans are immutable'; END IF;
 ELSE IF EXISTS(SELECT 1 FROM scans WHERE id=COALESCE(OLD.scan_id,NEW.scan_id) AND status IN ('completed','completed_with_errors')) THEN RAISE EXCEPTION 'completed scan inventory is immutable'; END IF; END IF;
 RETURN COALESCE(NEW,OLD); END $$ LANGUAGE plpgsql;
CREATE TRIGGER scans_immutable BEFORE UPDATE OR DELETE ON scans FOR EACH ROW EXECUTE FUNCTION protect_completed_scan();
CREATE TRIGGER nodes_immutable BEFORE UPDATE OR DELETE ON filesystem_nodes FOR EACH ROW EXECUTE FUNCTION protect_completed_scan();
