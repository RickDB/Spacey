<p align="center"><img src="docs/assets/spacey-logo.png" width="112" alt="Spacey logo"></p>

# Spacey

![Spacey storage intelligence banner](docs/assets/spacey-banner.webp)

Spacey is a browser based storage explorer and media cleanup tool for Linux and NAS systems. It indexes ordinary filesystem paths, shows folder rollups and large files, and keeps every completed scan as a historical inventory. ZFS discovery is optional and controlled in the web UI.

## Docker Compose installation

The production Compose deployment pulls the published image from `ghcr.io/rickdb/spacey`. A Git checkout is not needed if you save the Compose file yourself.

Create a directory and download the deployment files:

```sh
mkdir -p spacey && cd spacey
curl -O https://raw.githubusercontent.com/RickDB/Spacey/main/compose.yaml
curl -O https://raw.githubusercontent.com/RickDB/Spacey/main/.env.example
cp .env.example .env
```

Edit `.env` and replace `POSTGRES_PASSWORD` with a long random value. If the password contains characters such as `@`, `:`, `/`, `?`, `#`, or `%`, URL-encode them because the password is placed in a PostgreSQL connection URL.

Start Spacey:

```sh
docker compose up -d
```

The default `.env.example` publishes Spacey at `http://localhost:18087`. Change `SPACEY_PORT` when another host port is preferred. Check startup with:

```sh
docker compose ps
docker compose logs -f app
```

Update to the newest published image with:

```sh
docker compose pull app
docker compose up -d --force-recreate app
```

To pin a release or immutable image tag, set `SPACEY_TAG` in `.env` instead of using `latest`.

### Complete production Compose file

```yaml
services:
  db:
    image: postgres:18-alpine
    restart: unless-stopped
    environment:
      POSTGRES_USER: spacey
      POSTGRES_PASSWORD: ${POSTGRES_PASSWORD:?Set POSTGRES_PASSWORD in .env}
      POSTGRES_DB: spacey
    volumes:
      - postgres-data:/var/lib/postgresql/18/docker
    healthcheck:
      test: ["CMD-SHELL", "pg_isready -U spacey -d spacey"]
      interval: 3s
      timeout: 3s
      retries: 20
      start_period: 10s
    security_opt:
      - no-new-privileges:true

  app:
    image: ghcr.io/rickdb/spacey:${SPACEY_TAG:-latest}
    pull_policy: always
    restart: unless-stopped
    depends_on:
      db:
        condition: service_healthy
    environment:
      TZ: ${TZ:-Europe/Amsterdam}
      SPACEY_LISTEN: :8080
      SPACEY_DATABASE_URL: postgres://spacey:${POSTGRES_PASSWORD}@db:5432/spacey?sslmode=disable
    ports:
      - "${SPACEY_PORT:-18087}:8080"
    volumes:
      - /:/host:ro,rslave
    security_opt:
      - no-new-privileges:true
    stop_grace_period: 30s

volumes:
  postgres-data:
    name: ${SPACEY_POSTGRES_VOLUME:-spacey-postgres-data}
```

The checked-in `compose.yaml` also applies bounded container log rotation.

## Storage path mapping

Open `http://localhost:18087` (or the port selected through `SPACEY_PORT`). The first-run wizard asks for an administrator password, a storage source, scan workers, and optional media integrations in that order. For a host folder `/mnt/tank/media`, enter:

- Host path: `/mnt/tank/media`
- Container path: `/host/mnt/tank/media`

These fields describe the same directory from two viewpoints:

- **Host path** is the permanent, canonical TrueNAS/Linux path saved in historical inventories.
- **Container path** is the location Spacey can read inside its container.

The default `/:/host:ro,rslave` mount means every host path gains a `/host` prefix inside the container. For example:

| Dataset on the host | Host path field | Container path field |
| --- | --- | --- |
| `/data-media/tv` | `/data-media/tv` | `/host/data-media/tv` |
| `/mnt/data-temp/backup/data-media/anime` | `/mnt/data-temp/backup/data-media/anime` | `/host/mnt/data-temp/backup/data-media/anime` |

Do not enter a path that exists only on the host as the container path. To use a narrower explicit mount instead of exposing the host tree, replace the default volume with, for example:

```yaml
volumes:
  - /mnt/data-temp/backup/data-media/anime:/storage/anime:ro
```

Then use `/mnt/data-temp/backup/data-media/anime` as the host path and `/storage/anime` as the container path.

Verify visibility from the Docker host with:

```sh
docker compose exec app ls -ld /host/mnt/data-temp/backup/data-media/anime
```

The default container mount is read only. Spacey can scan any readable path. A completed scan stays available if its source disappears. It is an inventory of names and metadata, not a backup of file contents.

## Explore and clean up

- **Overview:** total indexed size, video size, file types, largest files, and live scan progress.
- **Explorer:** hierarchical folder browsing, size bars, sorting, and path search.
- **Cleanup:** large file and folder search, media type and watched filters, saved views, bulk selection, and managed trash when enabled.
- **History:** open completed inventories without touching the live filesystem.
- **Automations:** recurring scans every hour to every week, with an optional saved view associated for review. Every scheduled run scans the full source so the historical inventory remains complete.

Search and media matching currently use filenames and directory paths. Review the exact path before cleanup. A rescan updates the current inventory after files have been moved.

### Enable managed trash for one source

The normal deployment stays read only. To enable moving items to a recoverable `.spacey-trash` folder, mount **one chosen source** writable:

```sh
export SPACEY_CLEANUP_HOST_PATH=/mnt/tank/media
export SPACEY_UID=$(id -u)
export SPACEY_GID=$(id -g)
docker compose -f compose.yaml -f compose.cleanup.yaml up -d
```

Add that source with container path `/host/cleanup`. The selected UID/GID must have write permission on that host directory. Spacey asks for the administrator password from the first-run wizard when moving or restoring items. It does not retain that password in browser storage. The UI provides **move to trash** and **restore**; it does not permanently delete or empty the trash. The source root itself cannot be moved. The latest completed scan is required for cleanup, while older scans remain immutable.

Do not expose this single-user instance to an untrusted network. Put it behind your own authentication and HTTPS reverse proxy if it needs remote access.

## Optional media matching and watched history

Spacey can resolve scanned paths through Radarr, Sonarr, and Silo, then match TMDB, TVDB, or IMDb IDs against [MDBList watched history](https://api.mdblist.com/docs/). This avoids treating a similar filename as the same movie or episode. Unresolved paths show **Unknown** watch status.

Add the services in the first-run wizard or **Settings**. Enter their URLs and API keys there. URLs must be reachable from the Spacey container. If a service sees different file paths from Spacey's host paths, enter both its **Service path root** and the matching **Spacey host path root**. For example, Sonarr `/tv` can map to `/mnt/tank/media/TV`.

Leave a service URL empty to disconnect it. Stored API keys are never sent back to the browser. Silo API keys need access to catalog item file paths; Spacey reads Silo's v2 catalog and item details. The [Silo server](https://github.com/Silo-Server/silo-server) uses bearer API keys for its v2 catalog.

For an existing installation, the wizard appears once after upgrading. Existing storage sources and scan history remain in PostgreSQL. Enter a new administrator password and re-enter any media service credentials formerly supplied through Docker environment variables. Docker still supplies startup infrastructure such as the database connection, port, and filesystem mounts; those cannot be changed by the running web app.

Spacey refreshes configured catalogs and MDBList history at startup and every six hours. **Settings** also provides manual sync buttons. Radarr supplies movie files and IDs, Sonarr supplies series and episode files, and Silo can fill catalog matches. MDBList supplies watched state through `/sync/watched`, including movie, show, season, and episode records when present. A file containing multiple Sonarr episodes is shown as watched only when every mapped episode is watched. If a provider does not expose an ID or path, Spacey leaves its watch status Unknown. API credentials remain on the server.

## Optional ZFS

Spacey works without ZFS. On a ZFS host, use the read only discovery overlay:

```sh
docker compose -f compose.yaml -f compose.zfs.yaml up -d
```

Download `compose.zfs.yaml` when deploying without a Git checkout:

```sh
curl -O https://raw.githubusercontent.com/RickDB/Spacey/main/compose.zfs.yaml
```

The overlay passes `/dev/zfs` and mounts the host ZFS metadata read-only. It does not enable destructive ZFS operations. If `/dev/zfs` or `/proc/spl` does not exist, use only the base Compose file.

## Backup and removal

The PostgreSQL volume contains the historical inventory and should be backed up independently from the storage being inventoried:

```sh
docker compose exec -T db pg_dump -U spacey -d spacey -Fc > spacey-database.dump
```

Stop the application without deleting its database:

```sh
docker compose down
```

`docker compose down -v` permanently deletes the PostgreSQL inventory volume; do not use `-v` unless that loss is intentional.

## Local source build

Repository contributors can build the current checkout with the development overlay:

```sh
docker compose -f compose.yaml -f compose.local-build.yaml up --build -d
```

Enable ZFS discovery in the wizard or Settings. Spacey samples pool capacity, allocation, free space, health, and fragmentation, plus dataset usage, available space, references, and compression every 15 minutes. A manual **Refresh now** records an immediate sample. The ZFS page shows current usage and selectable 24-hour, 7-day, 30-day, 90-day, and 1-year graphs with recent sample history. Discovery failures remain visible while the last successful readings are preserved. Samples are retained in PostgreSQL; space usage grows with the number of pools and datasets. No ZFS commands are needed to browse filesystem history.

## API and development

The API is under `/api/v1/`. Main resources: `storage`, `scans`, `scans/{id}/nodes`, `scans/{id}/events`, `overview`, `search`, `filters`, `schedules`, `trash`, `media`, `mdblist`, `zfs`, and `zfs/history`. `POST /api/v1/trash`, restore, and `PUT /api/v1/settings` require the `X-Spacey-Admin-Token` header with the administrator password. `POST /api/v1/setup` is available only before setup is complete. Run `go test ./...` for the Go checks and `node --check internal/api/static/app.js` for browser script syntax.
