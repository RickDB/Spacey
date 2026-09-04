# SPACEY
## Master Product & Implementation Specification for Codex

Treat this document as the **authoritative master specification** for the project.

Do not simplify away the recovery, historical inventory, ZFS, cleanup, or Docker requirements described here.

If implementation choices conflict with this specification, preserve the intent of this specification first.

---

# 1. Product Definition

Build a web-based storage management application written primarily in **Go**.

The application is inspired by TreeSize, but should be designed for:

- Linux servers
- TrueNAS
- ZFS storage
- media libraries
- large HDD arrays
- NAS environments
- long-term filesystem inventory
- storage cleanup
- disaster reconstruction

The product should ultimately combine:

```text
TreeSize-style Storage Analyzer
        +
Web-based Cleanup Manager
        +
Permanent Filesystem Catalog
        +
Data-Loss Reconstruction Inventory
        +
ZFS Pool / Dataset Monitor
```

The application is called:

```text
Spacey
```

unless renamed later.

---

# 2. Core Purpose

Spacey has four main responsibilities:

1. Analyze storage usage.
2. Make files and folders easy to clean up.
3. Permanently remember historical filesystem layouts.
4. Automatically discover and monitor ZFS pools and datasets.

The historical filesystem inventory is a first-class feature.

This is not merely:

```text
"show me how much disk space I used last week"
```

It must support:

```text
"If this HDD or entire ZPOOL dies, show me exactly what was on it so I know what I need to re-download or rebuild."
```

That recovery-reference logic is a non-negotiable requirement.

---

# 3. Critical Recovery Philosophy

Historical filesystem scans are **not backups**.

They do not contain the actual file contents.

They must contain enough metadata to reconstruct exactly:

- which directories existed
- which files existed
- exact hierarchy
- exact filenames
- exact paths
- file sizes
- allocated sizes where available
- modification timestamps
- ownership and permissions where useful
- storage source
- ZFS dataset if applicable
- optional media metadata
- optional hashes

The application must remain useful even when the original storage is completely gone.

Examples:

```text
dead HDD
failed vdev
destroyed dataset
destroyed ZPOOL
corrupt filesystem
hardware failure
accidental pool destruction
unrecoverable administrative error
```

If the storage disappears, the historical database remains.

The user must still be able to browse the last known inventory.

---

# 4. Catastrophic Loss Example

Assume this existed:

```text
/mnt/tank/media/
├── Movies/
│   ├── Alien (1979)/
│   │   └── Alien.1979.2160p.UHD.BluRay.mkv
│   ├── Arrival (2016)/
│   │   └── Arrival.2016.2160p.mkv
│   └── Dune (2021)/
│       └── Dune.2021.2160p.mkv
│
└── TV/
    ├── The Expanse/
    │   ├── Season 01/
    │   │   ├── The.Expanse.S01E01.mkv
    │   │   ├── The.Expanse.S01E02.mkv
    │   │   └── ...
    │   └── Season 02/
    │
    └── Silo/
        └── Season 01/
```

Later:

```text
tank/media
```

is completely lost.

The user should still be able to open:

```text
History
  → Storage: tank/media
  → Scan: 2026-08-30 03:00
```

and browse the exact historical hierarchy.

The user can then determine:

```text
which movies are gone
which TV shows are gone
which seasons are gone
which episodes are gone
what filenames were used
what folder structure existed
how much data was lost
```

This allows re-downloading and rebuilding.

---

# 5. Historical Scan Rules

Completed filesystem scans must be immutable.

Example:

```text
Scan 141
2026-08-29 03:00

Scan 142
2026-08-30 03:00

Scan 143
2026-08-31 03:00
```

Each scan is a point-in-time filesystem inventory.

A new scan must never modify or overwrite previous scans.

Deleting a live file must never delete that historical record from older scans.

Historical scans remain browsable unless the user explicitly deletes them.

Default retention:

```text
Forever
```

---

# 6. Historical Browsing Must Be Storage-Independent

This rule is critical:

> Historical browsing must never require the original filesystem to still exist.

The historical browser must work entirely from PostgreSQL.

The application must assume:

```text
A storage source that existed yesterday may be completely absent today.
```

Do not write historical views that depend on:

```text
os.Stat()
filesystem mounts
ZFS availability
original paths existing
```

Historical views must read only from stored inventory data.

---

# 7. Database Placement

The historical inventory database is valuable specifically when storage fails.

Therefore:

```text
Do not assume PostgreSQL lives on the same ZPOOL being inventoried.
```

Strong recommendation:

- database on another pool
- database on another disk
- database on another server
- database backed up elsewhere

Spacey should eventually warn if it detects that the database resides on the same storage source it is supposed to document.

This warning should be advisory, not blocking.

---

# 8. Primary Navigation

Suggested navigation:

```text
Dashboard

Storage
├── Filesystems
├── Space Cleanup
├── Largest Files
├── File Types
└── Duplicates

History
├── Scan History
├── Changes
├── Missing Files
├── Recovery Inventory
└── Storage Growth

ZFS
├── Pools
├── Datasets
├── Disks
├── Scrubs
└── History

Jobs
├── Scans
├── Duplicate Scans
└── Cleanup Jobs

Settings
├── Storage Sources
├── Scanner
├── Trash
├── ZFS
├── Scheduling
├── Notifications
└── Users
```

The ZFS section should appear only when ZFS is detected.

---

# 9. Filesystem Scanner

The application must recursively scan configured filesystem roots.

Example:

```text
/mnt/tank/media
/mnt/tank/backups
/mnt/storage
```

For every object collect at least:

```text
name
full path
relative path
parent
node type
logical size
allocated size
modified time
access time where available
birth/creation time where available
permissions
UID
GID
owner
group
inode
device ID
extension
```

Recommended optional metadata:

```text
MIME type
hardlink count
filesystem type
ZFS dataset
hash state
media metadata state
```

---

# 10. Scanner Architecture

Do not implement the scanner as a naive single-threaded:

```go
filepath.Walk(...)
```

Use bounded parallel scanning.

Concept:

```text
Root
 │
 ▼
Directory Queue
 │
 ├──── Worker
 ├──── Worker
 ├──── Worker
 ├──── Worker
 │
 ▼
Aggregator
 │
 ▼
Database Writer
```

Requirements:

- configurable worker count
- bounded queues
- cancellation
- progress reporting
- pause support later if practical
- permission error handling
- exclusion rules
- symlink policy
- filesystem boundary policy
- avoid runaway memory
- avoid excessive HDD seeking
- avoid 100+ goroutine insanity by default

The scanner should be fast but friendly to spinning disks.

Reliability matters more than benchmark throughput.

---

# 11. Scan Progress

Show scanning progress live.

Prefer Server-Sent Events initially.

Example:

```http
GET /api/v1/scans/{scanID}/events
```

Example event:

```json
{
  "type": "progress",
  "directories": 58342,
  "files": 1934821,
  "bytes": 48737129103360
}
```

Example completed directory event:

```json
{
  "type": "directory_complete",
  "path": "/mnt/tank/media/Movies",
  "bytes": 18472382910464
}
```

---

# 12. Scan Failure Semantics

If a scan fails halfway:

```text
do not overwrite
do not modify
do not corrupt
```

the previous completed scan.

Possible statuses:

```text
pending
running
completed
completed_with_errors
cancelled
failed
```

Only completed or explicitly accepted `completed_with_errors` scans should become normal historical reference points.

Partial scans may be retained internally for diagnostics.

---

# 13. Folder Browser

The current-storage browser should feel similar to TreeSize.

Example:

```text
Name                  Size       Files      Modified

Movies                18.7 TB     14,829
TV                    22.1 TB     98,182
Anime                  8.4 TB     83,817
Downloads            621.2 GB     18,428
```

Support:

- expandable hierarchy
- breadcrumbs
- sorting
- searching
- filtering
- multi-selection
- size percentages
- logical size
- allocated size
- file counts
- folder counts
- last modified
- current storage view
- historical storage view

---

# 14. Historical Browser UX

Historical views must be clearly marked.

Example:

```text
Historical Inventory

Storage:
tank/media

Scan:
2026-08-30 03:00

⚠ Archived inventory
Files shown here may no longer exist.
```

Users must still be able to:

- browse folders
- expand tree
- inspect files
- search
- filter
- sort
- export
- compare
- view sizes
- view metadata

---

# 15. Scan Comparison

Allow comparisons between any two scans.

Example:

```text
FROM
2026-08-01

TO
2026-08-31
```

Classify entries as:

```text
Added
Removed
Modified
Moved/Renamed
Unchanged
```

Example:

```text
REMOVED

/Movies/Arrival (2016)                61.4 GB
/TV/The Expanse/Season 02            183.7 GB

ADDED

/Movies/Dune Part Two (2024)          84.2 GB
```

---

# 16. Recovery Inventory

Create a dedicated page:

```text
History → Recovery Inventory
```

This is not file-content recovery.

It is a reconstruction checklist.

Example:

```text
Reference scan:
2026-08-30 03:00

Current target:
tank/media-restored

Missing:
Movies          128 titles
TV              423 episodes
Anime            92 episodes

Estimated missing:
7.82 TB
```

Possible item states:

```text
Missing
Found again
Re-downloaded
Ignored
Restored
```

Later this can become a media recovery checklist.

---

# 17. Export Historical Inventory

Support:

```text
CSV
JSON
TXT
```

Recommended later:

```text
HTML
PDF
```

Example TXT export:

```text
/mnt/tank/media/TV/The Expanse/Season 01/The.Expanse.S01E01.mkv
/mnt/tank/media/TV/The Expanse/Season 01/The.Expanse.S01E02.mkv
```

---

# 18. Cleanup Is a Primary Workflow

This application will often be used specifically to free disk space.

Make cleanup fast.

Users should be able to find:

```text
largest folders
largest files
largest movies
largest TV shows
largest seasons
old files
old downloads
duplicates
```

and bulk-select items.

Example:

```text
☐ The Expanse            1.21 TB
☐ Game of Thrones        1.06 TB
☐ Marvel Movies         842.1 GB
☐ Downloads             617.4 GB

Selected:
2.27 TB
```

Actions:

```text
Move to Trash
Delete Permanently
Move
```

---

# 19. Managed Trash

Normal deletion should default to a managed trash system where possible.

Original:

```text
/mnt/tank/media/TV/The Expanse/Season 01
```

Trash:

```text
/mnt/tank/.spacey-trash/<deletion-id>/
```

Store:

```text
deletion ID
original path
trash path
timestamp
size
user
retention expiry
```

---

# 20. Restore Without ZFS Snapshots

Managed trash must support:

```text
Restore to Original Location
```

Restore flow:

1. verify original storage is available
2. recreate parent directories
3. detect conflicts
4. restore file/folder
5. restore metadata where possible
6. audit the operation

Historical scan records alone cannot restore file contents.

This distinction must remain clear everywhere.

---

# 21. Accidental Delete vs Catastrophic Loss

Two separate systems:

## Accidental deletion

Recover from:

```text
Managed Trash
ZFS snapshot
External backup
```

## Catastrophic storage loss

Use:

```text
Historical Scan Inventory
```

to determine what must be restored or re-downloaded.

---

# 22. Permanent Delete Safety

Permanent deletion should require clear confirmation.

For bulk operations show:

```text
Selected items
total files
total folders
total size
affected storage source
```

Do not make destructive actions visually easy to trigger accidentally.

All permanent deletions must be audited.

---

# 23. Media-Aware Design

Media storage is a major use case.

Core scanning must work without media parsing.

However, design the schema so later versions can expose:

```text
movie title
year
TV show
season
episode
episode title
resolution
codec
HDR format
audio codec
duration
bitrate
release group
```

Potential sources:

```text
filename parsing
ffprobe
NFO files
media server APIs
```

Always retain the original filename.

---

# 24. Media Recovery Use Case

Historical media inventory should later support:

```text
The Expanse
Season 01     10/10 present
Season 02      9/13 present
Season 03     13/13 present
```

Possible states:

```text
Present
Missing
Possible renamed match
Unknown
```

This is specifically useful after rebuilding a failed pool.

---

# 25. Largest Files

Dedicated page.

Filters:

```text
> 1 GB
> 10 GB
> 100 GB

older than X days

extension
dataset
storage source
```

Sort descending by default.

---

# 26. File Type Statistics

Aggregate by:

```text
extension
MIME type
category
```

Example:

```text
Video        41.2 TB
Archives      4.8 TB
Images        2.1 TB
Backups       1.7 TB
Other       820.4 GB
```

---

# 27. Treemap

Provide a TreeSize-style treemap.

Recommended:

```text
Apache ECharts
```

Support:

- directory treemap
- file treemap
- drill-down
- hover metadata
- current scan
- historical scan
- filters

---

# 28. Duplicate Detection

Do not blindly hash every byte of every file.

Suggested pipeline:

```text
Stage 1
Group by exact file size

Stage 2
Partial BLAKE3 hash

Stage 3
Full BLAKE3 hash only for candidates
```

Persist hashes.

Avoid re-hashing unchanged files.

Possible actions later:

```text
Keep selected
Move duplicates to Trash
Delete selected
Hardlink identical files
```

Only offer hardlinks when safe and supported.

---

# 29. ZFS Is a First-Class Subsystem

If ZFS is available, Spacey must provide a dedicated ZFS area.

This is not just another filesystem column.

Show:

```text
Pools
Datasets
Disks
Scrubs
History
```

If ZFS is not detected:

- hide normal ZFS navigation
- continue operating normally
- show diagnostics under Settings

---

# 30. Automatic ZFS Discovery

The user must not manually configure every pool or dataset.

Automatically discover:

```text
all accessible ZFS pools
all accessible ZFS datasets
mountpoints
hierarchy
pool membership
dataset usage
scan-source associations
```

Discovery should happen:

```text
on startup
periodically
on manual refresh
after relevant change where detectable
```

New datasets should appear automatically.

---

# 31. ZFS Detection

Detect:

```text
/dev/zfs
```

and commands:

```text
zpool
zfs
```

Safe discovery examples:

```bash
zpool list -H -p
```

```bash
zfs list -H -p   -o name,type,used,available,referenced,logicalused,logicalreferenced,compressratio,mountpoint
```

Failure to detect ZFS must not prevent Spacey from running.

Provide diagnostic reasons.

---

# 32. ZPOOL Dashboard

For each pool show:

```text
name
health
capacity
allocated
free
fragmentation
dedup ratio
expansion state
scrub state
resilver state
error state
```

Example:

```text
tank

Health          ONLINE
Capacity        87.3 TB
Allocated       61.8 TB
Free            25.5 TB
Fragmentation       18%
Dedup             1.00x
```

---

# 33. ZPOOL Topology

Display topology:

```text
tank
└── RAIDZ2
    ├── disk1 ONLINE
    ├── disk2 ONLINE
    ├── disk3 ONLINE
    ├── disk4 ONLINE
    ├── disk5 ONLINE
    └── disk6 ONLINE
```

Show when possible:

```text
device path
/dev/disk/by-id
serial
model
capacity
health
vdev role
```

Prefer stable identifiers over:

```text
/dev/sda
/dev/sdb
```

---

# 34. Dataset Dashboard

For every dataset show:

```text
name
mountpoint
used
available
referenced
logical used
logical referenced
compression ratio
quota
reservation
recordsize
```

Example:

```text
Dataset                    Used        Available

tank/media                44.2 TB        18.1 TB
tank/media/movies         18.7 TB        18.1 TB
tank/media/tv             22.1 TB        18.1 TB
tank/media/anime           3.4 TB        18.1 TB
```

---

# 35. Filesystem Usage vs ZFS Usage

Do not treat recursive filesystem size and ZFS usage as identical.

Display both.

Example:

```text
tank/media/movies

Filesystem inventory
Logical file size       18.12 TB

ZFS
Referenced              18.71 TB
Logical used            22.94 TB
Compression                 1.23x
Available               18.10 TB
```

Differences can come from:

```text
compression
snapshots
metadata
clones
reservations
record size
block allocation
```

---

# 36. Dataset Auto-Linking

If ZFS reports:

```text
tank/media
mountpoint=/mnt/tank/media
```

and Spacey scans:

```text
/mnt/tank/media/Movies
```

associate that scan source with:

```text
tank/media
```

automatically.

Use longest matching mountpoint.

Allow manual override.

---

# 37. ZFS History

Persist ZFS state over time.

At minimum capture ZFS state whenever a filesystem scan runs.

Store:

```text
pool health
pool allocated
pool free
dataset used
dataset available
dataset referenced
logical usage
compression ratio
scrub state
device health
```

This allows storage-growth views.

---

# 38. Scrub and Resilver Monitoring

Parse:

```bash
zpool status
```

Expose:

```text
scrub running
scrub completed
scrub errors
bytes scanned
bytes repaired
resilver running
resilver progress
status
```

Initial version is monitoring-only.

---

# 39. Missing ZPOOL Behavior

If a previously detected pool disappears:

Do not remove it from history.

Show:

```text
tank

⚠ Previously discovered ZPOOL unavailable

Last seen:
2026-08-30 03:01

Last known health:
ONLINE

Last known allocated:
61.8 TB

[Browse Last Inventory]
[View ZFS History]
```

Never auto-delete historical pool/dataset records because live discovery fails.

---

# 40. Missing Storage Behavior

If a scan root disappears:

```text
Storage unavailable
```

Show:

```text
Last successful scan
last known file count
last known size
last known dataset
```

Provide:

```text
Browse Last Inventory
```

This behavior is essential after hardware failure.

---

# 41. ZFS Security

Initial ZFS access must be read-only.

Allowed:

```text
zpool list
zpool status
zfs list
zfs get
```

Do not expose:

```text
zpool destroy
zpool detach
zpool replace
zpool offline
zfs destroy
```

If later implementing snapshot creation, use a narrow helper with limited privilege.

Do not give the entire web process unrestricted ZFS power.

---

# 42. Docker Requirements

Spacey must be deployable with Docker Compose.

The container must be able to discover host ZFS where possible.

Baseline concept:

```yaml
services:
  app:
    image: ghcr.io/example/spacey:latest
    container_name: spacey
    restart: unless-stopped

    ports:
      - "8080:8080"

    environment:
      TZ: Europe/Amsterdam
      DATABASE_URL: postgres://spacey:${POSTGRES_PASSWORD}@postgres:5432/spacey
      ZFS_DISCOVERY: "auto"
      ZFS_READ_ONLY: "true"

    devices:
      - /dev/zfs:/dev/zfs

    volumes:
      - /mnt:/host/mnt
      - /proc/spl:/host/proc/spl:ro
      - ./data:/app/data

    depends_on:
      postgres:
        condition: service_healthy

  postgres:
    image: postgres:18
    container_name: spacey-db
    restart: unless-stopped

    environment:
      POSTGRES_DB: spacey
      POSTGRES_USER: spacey
      POSTGRES_PASSWORD: ${POSTGRES_PASSWORD}

    volumes:
      - ./postgres:/var/lib/postgresql/data

    healthcheck:
      test: ["CMD-SHELL", "pg_isready -U spacey -d spacey"]
      interval: 10s
      timeout: 5s
      retries: 5
```

Do not default to:

```yaml
privileged: true
```

Only add capabilities after proving they are required.

---

# 43. Docker ZFS Tooling

The application image should include compatible ZFS userland tooling where required.

The app should diagnose:

```text
/dev/zfs missing
zpool command missing
zfs command missing
permission denied
host ZFS unavailable
```

rather than simply showing “ZFS failed”.

---

# 44. Host / Container Path Translation

Inside the container, host paths may be:

```text
/host/mnt/tank/media
```

while canonical host paths are:

```text
/mnt/tank/media
```

Support explicit translation:

```text
Host prefix:
/mnt

Container prefix:
/host/mnt
```

Historical records should store canonical host-visible paths.

Do not permanently store only transient container paths.

---

# 45. PostgreSQL Schema

Suggested entities:

```text
hosts

storage_sources

scans
filesystem_nodes
scan_statistics

zfs_pools
zfs_pool_snapshots
zfs_vdevs
zfs_devices

zfs_datasets
zfs_dataset_snapshots

file_hashes

deletions
trash_items

jobs

users
audit_log
```

---

# 46. Filesystem Node Model

Suggested fields:

```text
id
scan_id
parent_id
storage_source_id
zfs_dataset_id

name
canonical_path
relative_path

node_type

logical_size
allocated_size

file_count
directory_count

extension
mime_type

uid
gid
owner
group

mode

device_id
inode

modified_at
accessed_at
created_at

hash_state

created_in_database_at
```

---

# 47. Scan Model

Suggested fields:

```text
id
storage_source_id

started_at
completed_at

status

scanner_version

root_path

total_files
total_directories

logical_bytes
allocated_bytes

errors_count

zfs_snapshot_reference_id
```

---

# 48. Path Identity

Do not use a filesystem path as the database primary key.

Paths can change.

Use internal IDs.

Path strings are metadata.

Future rename detection may use combinations of:

```text
device
inode
size
timestamp
hash
```

---

# 49. PostgreSQL Indexing

Important indexes:

```text
scan_id
parent_id
storage_source_id
zfs_dataset_id

logical_size
allocated_size

modified_at
extension
owner

canonical_path

hash
```

The design must scale to:

```text
millions
tens of millions
```

of filesystem entries.

---

# 50. Search

Support simple search:

```text
Dune
```

and advanced filters later:

```text
size:>50GB ext:mkv
```

Potential fields:

```text
name
path
extension
size
allocated size
modified age
owner
dataset
storage source
file/directory
duplicate state
```

Allow search against:

```text
Current filesystem
Specific scan
All historical scans
```

---

# 51. API

Suggested routes:

```http
GET    /api/v1/storage
POST   /api/v1/storage

POST   /api/v1/storage/{id}/scan

GET    /api/v1/scans
GET    /api/v1/scans/{id}
GET    /api/v1/scans/{id}/events

GET    /api/v1/scans/{id}/nodes/{nodeID}
GET    /api/v1/scans/{id}/nodes/{nodeID}/children

GET    /api/v1/history/compare

GET    /api/v1/files/largest
GET    /api/v1/statistics/extensions

POST   /api/v1/trash
POST   /api/v1/trash/{id}/restore
DELETE /api/v1/trash/{id}

GET    /api/v1/zfs
GET    /api/v1/zfs/pools
GET    /api/v1/zfs/pools/{name}
GET    /api/v1/zfs/datasets

POST   /api/v1/zfs/discover
```

API structure may evolve, but preserve the capabilities.

---

# 52. Go Project Architecture

Suggested structure:

```text
cmd/
  server/

internal/
  api/
  auth/
  config/
  database/

  scanner/
    filesystem/
    coordinator/
    progress/

  history/
  search/
  cleanup/
  trash/
  duplicates/

  zfs/
    discovery/
    pools/
    datasets/
    parser/

  jobs/
  notifications/
  audit/

  web/

migrations/

web/
  static/
  templates/
```

Keep:

```text
filesystem scanning
```

and:

```text
ZFS discovery
```

separate subsystems.

---

# 53. Storage Provider Abstraction

Initial provider:

```text
LocalFilesystemProvider
```

Prepare for future providers:

```text
RemoteAgentProvider
SSHProvider
S3Provider
SMBProvider
```

Do not over-engineer v0.1 around hypothetical providers.

---

# 54. Frontend Architecture

Preferred stack:

```text
Go templates or templ
HTMX
small amounts of Alpine.js if needed
Apache ECharts
```

Avoid a heavy SPA unless clearly justified later.

Goals:

```text
fast
simple
server-rendered
minimal frontend tooling
easy Docker deployment
```

---

# 55. Authentication / Authorization

Eventually support:

```text
Admin
Operator
Read-only
```

Permissions should distinguish:

```text
view storage
scan storage
view history
delete files
restore trash
permanent deletion
configure storage
view ZFS
administer app
```

---

# 56. Audit Log

Audit all important and destructive actions.

Examples:

```text
scan started
scan cancelled
file moved to trash
file permanently deleted
folder deleted
trash restored
storage source changed
historical scan deleted
user login
```

Store:

```text
timestamp
user
operation
object
original path
destination
size
result
```

---

# 57. Scheduling

Support scheduled scans.

Example:

```text
Storage:
tank/media

Schedule:
Daily at 03:00

Options:
☑ Filesystem inventory
☑ Capture ZFS state
☑ Compare previous scan
```

---

# 58. Dashboard

Main dashboard should show:

```text
total storage
used space
free space

largest storage sources
recent scans
recent growth

ZFS pool health if available

warnings
failed disks
scrub errors
low space
failed scans
```

---

# 59. ZFS Discovery UI

Example:

```text
ZFS Discovery

Status:
Detected

Pools:
2

Datasets:
14

Automatically discovered:
✓ tank
✓ backup

[Refresh Discovery]
```

---

# 60. Recovery Checklist Future Feature

Compare a rebuilt storage source against a historical scan.

States:

```text
✓ Present
✗ Missing
~ Possible renamed match
? Unknown
```

Example:

```text
The Expanse

Season 01       10/10 ✓
Season 02        9/13 ✗
Season 03       13/13 ✓
```

This is important for rebuilding media after pool failure.

---

# 61. Initial Development Milestones

## v0.1

Build the reliable foundation:

```text
Go application structure

PostgreSQL
migrations

Dockerfile
Docker Compose

filesystem source configuration

bounded parallel recursive scanner

scan jobs
live progress

immutable historical scan persistence

current filesystem browser

historical filesystem browser

largest directories
largest files

basic search

basic ZFS detection

automatic ZPOOL discovery

automatic dataset discovery

dataset mountpoint discovery

ZPOOL health display

dataset usage display

dataset ↔ filesystem auto-linking

missing-storage state

missing-ZPOOL state
```

## v0.2

```text
scan comparison

Added / Removed / Changed

Space Cleanup UI

managed Trash

Restore to Original Location

permanent delete flow

audit log

treemap

file type analysis
```

## v0.3

```text
scheduled scans

storage growth history

ZFS history

Recovery Inventory workflow

CSV export
JSON export
TXT export
```

## v0.4

```text
duplicate detection
BLAKE3 hashing
advanced search
cleanup filters
```

## v0.5

```text
media filename parsing
ffprobe integration

Movies view
TV Shows view
Seasons view

recovery checklist
```

## Later

```text
remote Go agents
SSH storage
S3 storage
SMB provider

notifications

SMART disk monitoring

optional ZFS snapshot integration

multi-user RBAC
```

---

# 62. Explicit Initial Non-Goals

Do not initially build:

```text
full NAS administration
ZPOOL creation
ZPOOL destruction
disk replacement
arbitrary ZFS commands
snapshot replication
backup software
file-content backups
media downloading
Sonarr replacement
Radarr replacement
```

Spacey's recovery purpose is initially:

```text
remember exactly what existed
```

not:

```text
store a second copy of everything
```

---

# 63. Core Product Principles

Preserve these rules throughout implementation:

1. Never silently destroy historical inventory.
2. Historical browsing must never depend on live storage.
3. Completed scans are immutable.
4. ZFS discovery is automatic.
5. Missing pools remain in history.
6. Missing storage roots remain in history.
7. Cleanup should be quick.
8. Deletion should be reversible by default where practical.
9. Managed Trash and historical inventory are separate recovery mechanisms.
10. ZFS monitoring starts read-only.
11. Docker deployment must support ZFS discovery.
12. Store canonical host-visible paths.
13. Prefer Go-native/simple architecture.
14. Do not introduce unnecessary frontend complexity.
15. Do not confuse metadata inventory with actual backups.
16. The app must remain useful after catastrophic storage loss.

---

# 64. Central User Stories

## Storage cleanup

> As a user, I want to immediately identify the largest movies, shows, seasons, folders and files so I can free storage quickly.

## Safe deletion

> As a user, I want normal deletion to be recoverable so an accidental cleanup does not immediately destroy my data.

## Historical inventory

> As a user, I want each completed scan to permanently remember my full filesystem hierarchy.

## HDD failure

> As a user, if an HDD dies, I want to browse the last known file and folder inventory for that disk.

## ZPOOL failure

> As a user, if an entire ZPOOL dies, I want to browse exactly what was on it before failure.

## Re-download workflow

> As a user, after catastrophic storage loss, I want to know exactly which movies, TV shows, seasons and episodes need to be downloaded again.

## ZFS discovery

> As a user, I want Spacey to automatically detect all available pools and datasets without manual registration.

## ZFS capacity

> As a user, I want to see storage usage and free space broken down per ZFS dataset.

## Missing ZPOOL

> As a user, if a previously detected pool disappears, I want Spacey to preserve its last known state and inventory.

---

# 65. Codex Implementation Instructions

When working on this project:

- Treat this file as the master product specification.
- Do not discard requirements because they are not yet implemented.
- Mark future requirements clearly rather than deleting them.
- Build v0.1 first.
- Prefer working, testable increments.
- Keep Go packages reasonably small and focused.
- Add migrations instead of manually changing schema state.
- Include Docker Compose from the beginning.
- Add health checks.
- Add application diagnostics.
- Make failure states visible in the UI.
- Avoid `privileged: true` by default.
- Keep ZFS read-only initially.
- Never make old scans dependent on live mounts.
- Never replace an older scan with a new one.
- Never automatically delete history because a pool disappears.
- Preserve enough metadata to reconstruct exact old directory layouts.
- Assume millions of filesystem records are normal.
- Optimize only where measurements justify it.
- Keep media parsing optional and layered on top of filesystem inventory.
- Keep cleanup workflows easy and obvious.
- Add tests for historical inventory behavior.
- Add tests for missing-storage behavior.
- Add tests for ZFS discovery parsing.
- Add tests proving completed scans remain immutable.

---

# 66. First Build Task

Start implementing the **v0.1 foundation now**.

Create or update the project with:

```text
Go module
cmd/server

configuration loading

HTTP server

PostgreSQL connection

database migrations

Dockerfile

docker-compose.yml

storage source model

scan model

filesystem node model

ZFS pool model

ZFS dataset model

filesystem scanner

bounded worker pool

scan progress tracking

historical scan persistence

basic API

basic HTML UI

live scan page

historical browser

largest files page

automatic ZFS detection

automatic ZPOOL discovery

automatic ZFS dataset discovery

dataset mountpoint matching

ZFS overview page
```

Do not spend time polishing advanced v0.5 features before the v0.1 architecture is sound.

---

# 67. Acceptance Criteria for v0.1

A successful v0.1 should allow this workflow:

```text
1. Start Spacey using Docker Compose.

2. Spacey connects to PostgreSQL.

3. Spacey detects whether host ZFS is available.

4. If ZFS exists:
   - pools are discovered automatically
   - datasets are discovered automatically
   - mountpoints are discovered automatically
   - health and usage are displayed

5. Configure or auto-detect a filesystem scan root.

6. Start a scan.

7. Watch live progress.

8. Scan completes.

9. Browse the current inventory.

10. Browse the completed scan later without reading the filesystem.

11. Run another scan.

12. The first scan remains unchanged.

13. Remove or unmount the original storage.

14. Spacey reports the storage as unavailable.

15. The old inventory remains fully browsable.

16. If a ZPOOL disappears, Spacey preserves:
    - its historical ZFS information
    - its historical filesystem scan
    - last known status

17. Search for files within a historical scan.

18. View largest files and directories from stored scan data.
```

If this sequence works, the foundation is correct.

---

# 68. Most Important Rule

The most important behavior of Spacey is:

```text
This is exactly what you had.
This is exactly where it was.
This is how large it was.
This is what disappeared.
This is what you still need to replace.
```

That must remain true even if:

```text
the HDD is dead
the filesystem will not mount
the dataset is gone
the ZPOOL no longer exists
```

Build the architecture around that assumption.
