package model

import "time"

type Node struct {
	ID, ParentID                                                                          int64
	CanonicalPath, ParentPath, RelativePath, Name, Type, Extension, LinkTarget, ReadError string
	LogicalSize, AllocatedSize                                                            int64
	Mode                                                                                  uint32
	UID, GID, DeviceID, Inode                                                             uint64
	ModifiedAt, AccessedAt                                                                time.Time
}
type Progress struct {
	ScanID         string    `json:"scan_id,omitempty"`
	Root           string    `json:"root,omitempty"`
	Status         string    `json:"status,omitempty"`
	CurrentPath    string    `json:"current_path,omitempty"`
	Error          string    `json:"error,omitempty"`
	Files          int64     `json:"files"`
	Directories    int64     `json:"directories"`
	LogicalBytes   int64     `json:"logical_bytes"`
	AllocatedBytes int64     `json:"allocated_bytes"`
	Errors         int64     `json:"errors"`
	StartedAt      time.Time `json:"started_at"`
}
type StorageSource struct {
	ID                                 int64
	Name, CanonicalRoot, ContainerRoot string
	Enabled, Available                 bool
	LastAvailableAt                    *time.Time
}
type Scan struct {
	ID, Status, ScannerVersion, RootPath                                    string
	StorageSourceID                                                         int64
	StartedAt, CompletedAt                                                  *time.Time
	TotalFiles, TotalDirectories, LogicalBytes, AllocatedBytes, ErrorsCount int64
	ErrorMessage                                                            *string
}
type ZFSPool struct {
	ID                    int64
	Name, Health          string
	Available             bool
	Size, Allocated, Free int64
	Fragmentation         float64
	LastSeenAt            time.Time
}
type ZFSDataset struct {
	ID, PoolID                                                  int64
	Name, Type, Mountpoint                                      string
	Used, Available, Referenced, LogicalUsed, LogicalReferenced int64
	Compression                                                 float64
	LastSeenAt                                                  time.Time
}
