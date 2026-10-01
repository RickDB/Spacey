# Spacey product direction

Spacey helps a person understand and clean up storage, especially movies and series. Ordinary filesystem paths are the core source type. ZFS is an optional discovery and monitoring integration.

## Core workflow

1. Add a readable storage path.
2. Scan it and watch progress live.
3. Explore folder sizes and search for large files, movies, shows, and episodes.
4. Save a useful cleanup view or schedule recurring full scans.
5. Review exact paths, then optionally move selected items to managed trash and restore them if needed.
6. Browse old inventories even when the source is missing.

Completed inventories are immutable metadata snapshots and are never presented as file content backups. Cleanup actions must target the latest completed scan and validate the current path. Default deployment access to storage is read only; writable access is explicitly scoped to one host path and protected by a server configured admin token.

MDBList is optional. Watched status comes from its watched-history API. Radarr, Sonarr, and Silo optionally map exact scanned paths to provider IDs. An unresolved media path has Unknown watch status, never an inferred Unwatched state. The app works without an API key, list, ZFS pool, or dataset.

## Next steps

Current media recognition is filename based. Future improvements can add media server IDs, more accurate season and movie matching, disk allocation views, treemaps, duplicate detection, scan comparison, export, configurable exclusion rules, and multi-user authentication. Keep the scan database independent of any drive it inventories.
