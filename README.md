# Spacey

Spacey is a browser based storage explorer and media cleanup tool for Linux and NAS systems. It indexes ordinary filesystem paths, shows folder rollups and large files, and keeps every completed scan as a historical inventory. ZFS discovery is optional and controlled in the web UI.

## Start

```sh
export POSTGRES_PASSWORD='choose-a-password'
docker compose up -d
```

The default Compose file builds the current checkout. After source changes, run `docker compose up --build -d`.

Open `http://localhost:8080`. The first-run wizard asks for an administrator password, a storage source, scan workers, and optional media integrations in that order. For a host folder `/mnt/tank/media`, enter:

- Host path: `/mnt/tank/media`
- Container path: `/host/mnt/tank/media`

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

Enable ZFS discovery in the wizard or Settings. The ZFS tab appears only after pools or datasets are discovered. No ZFS commands are needed to browse filesystem history.

## API and development

The API is under `/api/v1/`. Main resources: `storage`, `scans`, `scans/{id}/nodes`, `scans/{id}/events`, `overview`, `search`, `filters`, `schedules`, `trash`, `media`, `mdblist`, and `zfs`. `POST /api/v1/trash`, restore, and `PUT /api/v1/settings` require the `X-Spacey-Admin-Token` header with the administrator password. `POST /api/v1/setup` is available only before setup is complete. Run `go test ./...` for the Go checks and `node --check internal/api/static/app.js` for browser script syntax.
