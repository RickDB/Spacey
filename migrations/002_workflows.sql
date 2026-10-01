CREATE TABLE saved_filters (
 id bigserial PRIMARY KEY,
 name text NOT NULL,
 storage_source_id bigint REFERENCES storage_sources(id) ON DELETE CASCADE,
 criteria jsonb NOT NULL DEFAULT '{}'::jsonb,
 created_at timestamptz NOT NULL DEFAULT now()
);
CREATE TABLE scan_schedules (
 id bigserial PRIMARY KEY,
 storage_source_id bigint NOT NULL REFERENCES storage_sources(id) ON DELETE CASCADE,
 name text NOT NULL,
 interval_minutes integer NOT NULL CHECK (interval_minutes >= 15),
 filter_id bigint REFERENCES saved_filters(id) ON DELETE SET NULL,
 enabled boolean NOT NULL DEFAULT true,
 next_run_at timestamptz NOT NULL DEFAULT now(),
 last_run_at timestamptz,
 created_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX schedules_due_idx ON scan_schedules(next_run_at) WHERE enabled;
CREATE TABLE trash_items (
 id uuid PRIMARY KEY,
 storage_source_id bigint NOT NULL REFERENCES storage_sources(id),
 scan_id uuid NOT NULL REFERENCES scans(id),
 original_path text NOT NULL,
 trash_path text NOT NULL,
 logical_size bigint NOT NULL DEFAULT 0,
 moved_at timestamptz NOT NULL DEFAULT now(),
 restored_at timestamptz
);
CREATE INDEX trash_source_idx ON trash_items(storage_source_id,moved_at DESC);
