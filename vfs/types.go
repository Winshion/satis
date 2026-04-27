package vfs

import "time"

// FileID is the stable system-side identity of a file object.
type FileID string

// ChunkID identifies the chunk that created or mutated a file object.
type ChunkID string

// EventID identifies an audit event.
type EventID string

// Generation is the monotonic version number of a file object.
type Generation uint64

// FileKind controls which operations and views are valid for a file object.
type FileKind string

const (
	FileKindText      FileKind = "text"
	FileKindBinary    FileKind = "binary"
	FileKindDirectory FileKind = "directory"
	FileKindGenerated FileKind = "generated"
	FileKindEphemeral FileKind = "ephemeral"
)

// DeleteState models the current visibility/lifecycle state of a file object.
type DeleteState string

const (
	DeleteStateActive  DeleteState = "active"
	DeleteStateDeleted DeleteState = "deleted"
)

// WriteMode defines how a write operation transforms file content.
type WriteMode string

const (
	WriteModeReplaceFull WriteMode = "replace_full"
	WriteModeAppend      WriteMode = "append"
	WriteModePatchText   WriteMode = "patch_text"
)

// EventType names a logical audit event in the VFS.
type EventType string

const (
	EventTypeFileCreated  EventType = "file_created"
	EventTypeFileWritten  EventType = "file_written"
	EventTypeFileRenamed  EventType = "file_renamed"
	EventTypeFileDeleted  EventType = "file_deleted"
	EventTypeFileRestored EventType = "file_restored"
	EventTypeFileRead     EventType = "file_read"
)

// SyncStatus tracks the high-level persistence state of current file content.
type SyncStatus string

const (
	SyncStatusClean   SyncStatus = "clean"
	SyncStatusDirty   SyncStatus = "dirty"
	SyncStatusSyncing SyncStatus = "syncing"
)

// LineageRef points to a parent file object used to derive the current one.
type LineageRef struct {
	FileID     FileID     `json:"file_id"`
	Generation Generation `json:"generation,omitempty"`
	Role       string     `json:"role,omitempty"`
}

// FileMeta is the current-state metadata of a single file object.
//
// It should stay compact and reflect only the current view of the object.
// Historical versions and audit trails live in separate structures.
type FileMeta struct {
	FileID            FileID       `json:"file_id"`
	VirtualPath       string       `json:"virtual_path"`
	Kind              FileKind     `json:"kind"`
	ContentType       string       `json:"content_type,omitempty"`
	CurrentGeneration Generation   `json:"current_generation"`
	CurrentChecksum   string       `json:"current_checksum,omitempty"`
	Size              int64        `json:"size"`
	SyncStatus        SyncStatus   `json:"sync_status"`
	CreatorChunkID    ChunkID      `json:"creator_chunk_id"`
	LastWriterChunkID ChunkID      `json:"last_writer_chunk_id,omitempty"`
	ParentLineage     []LineageRef `json:"parent_lineage,omitempty"`
	DeleteState       DeleteState  `json:"delete_state"`
	CreatedAt         time.Time    `json:"created_at"`
	UpdatedAt         time.Time    `json:"updated_at"`
	LastReadAt        time.Time    `json:"last_read_at,omitempty"`
	LastEventID       EventID      `json:"last_event_id,omitempty"`
}

// FileRef is the runtime-facing handle returned by resolve-like operations.
//
// It intentionally includes both the stable identity and the current namespace
// view so that the scheduler can operate by FileID while language-facing code
// can still preserve path intuition.
type FileRef struct {
	FileID            FileID      `json:"file_id"`
	VirtualPath       string      `json:"virtual_path"`
	Kind              FileKind    `json:"kind"`
	ContentType       string      `json:"content_type,omitempty"`
	CurrentGeneration Generation  `json:"current_generation"`
	DeleteState       DeleteState `json:"delete_state"`
	ResolvedAt        time.Time   `json:"resolved_at"`
}

// VersionEntry is the recoverable content version record of a file object.
type VersionEntry struct {
	FileID         FileID     `json:"file_id"`
	Generation     Generation `json:"generation"`
	ObjectID       string     `json:"object_id,omitempty"`
	ContentRef     string     `json:"content_ref"`
	WriteMode      WriteMode  `json:"write_mode"`
	BaseGeneration Generation `json:"base_generation,omitempty"`
	WriterChunkID  ChunkID    `json:"writer_chunk_id"`
	Checksum       string     `json:"checksum,omitempty"`
	Size           int64      `json:"size"`
	CreatedAt      time.Time  `json:"created_at"`
}

// Event is the append-only audit record for actions performed in the VFS.
//
// Payload is intentionally left as a string-keyed map in v0 so the event stream
// can evolve without forcing a large type hierarchy too early.
type Event struct {
	EventID   EventID         `json:"event_id"`
	FileID    FileID          `json:"file_id"`
	Type      EventType       `json:"type"`
	ChunkID   ChunkID         `json:"chunk_id,omitempty"`
	Timestamp time.Time       `json:"timestamp"`
	Payload   map[string]any  `json:"payload,omitempty"`
}
