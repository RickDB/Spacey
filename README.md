# Spacey

Spacey is a Go and PostgreSQL storage inventory for Linux, NAS, and ZFS systems. A completed scan is an immutable metadata inventory—not a backup—and remains browsable when its disk, filesystem, dataset, or complete pool no longer exists.

## Start

```sh
docker compose up --build
```

On a ZFS host, add the read-only discovery overlay:

```sh
docker compose -f compose.yaml -f compose.zfs.yaml up --build
```

Open http://localhost:8080. Add a storage source with both its canonical host path (for example `/mnt/tank/media`) and its in-container path (`/host/mnt/tank/media`). Spacey stores canonical host-visible paths.

Set `POSTGRES_PASSWORD` in `.env` before exposing the service. Keep PostgreSQL on storage independent from the pool being inventoried and back it up separately.

## v0.1 API

- `GET/POST /api/v1/storage`
- `POST /api/v1/storage/{id}/scan`
- `GET /api/v1/scans`
- `GET /api/v1/scans/{id}/events` (SSE)
- `GET /api/v1/scans/{id}/nodes?parent=...&q=...`
- `GET /api/v1/files/largest?scan_id=...&type=file|directory`
- `GET /api/v1/zfs`
- `POST /api/v1/zfs/discover`

All inventory browse, search, and largest-item queries use PostgreSQL only. They never inspect historical paths on live storage.
