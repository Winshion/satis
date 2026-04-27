package vfs

import (
	"context"
	"time"
)

// TxnID identifies a chunk-scoped logical transaction.
type TxnID string

// ReadView describes which logical projection of content the caller wants.
//
// V0 keeps this intentionally small. Full content reads should be the default.
// Text-specific ranged/line views can be added without breaking the base shape.
type ReadView struct {
	Mode      string `json:"mode,omitempty"`
	StartLine int    `json:"start_line,omitempty"`
	EndLine   int    `json:"end_line,omitempty"`
}

// ReadResult is the runtime-facing result of a read operation.
type ReadResult struct {
	FileRef     FileRef    `json:"file_ref"`
	Generation  Generation `json:"generation"`
	ContentType string     `json:"content_type,omitempty"`
	Text        string     `json:"text,omitempty"`
	Blob        []byte     `json:"blob,omitempty"`
}

// PatchTextInput models the strict text replacement payload used by
// WriteModePatchText. The old text is expected to match exactly once.
type PatchTextInput struct {
	OldText string `json:"old_text"`
	NewText string `json:"new_text"`
}

// CreateInput defines the parameters needed to create a new file object.
type CreateInput struct {
	VirtualPath    string       `json:"virtual_path"`
	Kind           FileKind     `json:"kind"`
	ContentType    string       `json:"content_type,omitempty"`
	CreatorChunkID ChunkID      `json:"creator_chunk_id"`
	ParentLineage  []LineageRef `json:"parent_lineage,omitempty"`
	InitialText    string       `json:"initial_text,omitempty"`
	InitialBlob    []byte       `json:"initial_blob,omitempty"`
}

// ResolveInput accepts exactly one stable locator for a file object.
type ResolveInput struct {
	VirtualPath  string   `json:"virtual_path,omitempty"`
	ExpectedKind FileKind `json:"expected_kind,omitempty"`
	FileID       FileID   `json:"file_id,omitempty"`
}

// ReadInput describes a read against a resolved or directly-located object.
type ReadInput struct {
	Target     ResolveInput `json:"target"`
	Generation Generation   `json:"generation,omitempty"`
	View       ReadView     `json:"view,omitempty"`
}

// WriteInput describes a content mutation against an existing file object.
type WriteInput struct {
	Target        ResolveInput    `json:"target"`
	WriterChunkID ChunkID         `json:"writer_chunk_id"`
	Mode          WriteMode       `json:"mode"`
	Text          string          `json:"text,omitempty"`
	Blob          []byte          `json:"blob,omitempty"`
	PatchText     *PatchTextInput `json:"patch_text,omitempty"`
}

// DeleteInput marks a file object as deleted within the current transaction.
type DeleteInput struct {
	Target  ResolveInput `json:"target"`
	ChunkID ChunkID      `json:"chunk_id"`
	Reason  string       `json:"reason,omitempty"`
}

// RenameInput updates the current namespace binding of a file object.
type RenameInput struct {
	Target         ResolveInput `json:"target"`
	NewVirtualPath string       `json:"new_virtual_path"`
	ChunkID        ChunkID      `json:"chunk_id"`
}

// DirEntry describes a direct child inside a virtual directory.
type DirEntry struct {
	Name        string   `json:"name"`
	VirtualPath string   `json:"virtual_path"`
	Kind        FileKind `json:"kind"`
}

// Txn captures the v0 chunk-scoped transactional identity.
type Txn struct {
	ID        TxnID     `json:"id"`
	ChunkID   ChunkID   `json:"chunk_id"`
	StartedAt time.Time `json:"started_at"`
}

// Service is the stable VFS primitive boundary.
//
// The future Satis runtime should depend on this interface rather than on a
// particular in-memory or on-disk implementation.
type Service interface {
	BeginChunkTxn(ctx context.Context, chunkID ChunkID) (Txn, error)
	CommitChunkTxn(ctx context.Context, txn Txn) error
	RollbackChunkTxn(ctx context.Context, txn Txn) error

	Create(ctx context.Context, txn Txn, input CreateInput) (FileRef, error)
	Resolve(ctx context.Context, input ResolveInput) (FileRef, error)
	Read(ctx context.Context, txn Txn, input ReadInput) (ReadResult, error)
	ListDir(ctx context.Context, txn Txn, virtualPath string) ([]DirEntry, error)
	Write(ctx context.Context, txn Txn, input WriteInput) (FileRef, error)
	Delete(ctx context.Context, txn Txn, input DeleteInput) (FileRef, error)
	Rename(ctx context.Context, txn Txn, input RenameInput) (FileRef, error)

	// Glob expands a virtual-path glob pattern (e.g. "/docs/*.txt") into a list
	// of matching virtual paths. Only DiskService can produce meaningful results;
	// other implementations may return an empty list or an error.
	Glob(ctx context.Context, pattern string) ([]string, error)
}
