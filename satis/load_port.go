package satis

import "context"

// LoadEntry describes one item under the sandbox-managed system_port tree.
type LoadEntry struct {
	Name        string
	VirtualPath string
	IsDir       bool
}

// LoadPort provides read-only access to the sandbox-managed system_port tree.
// Paths are logical absolute paths rooted at "/".
type LoadPort interface {
	Stat(ctx context.Context, virtualPath string) (LoadEntry, error)
	ListDir(ctx context.Context, virtualPath string) ([]LoadEntry, error)
	Glob(ctx context.Context, pattern string) ([]LoadEntry, error)
	ReadText(ctx context.Context, virtualPath string) (string, error)
}
