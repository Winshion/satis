package vfs

import "errors"

var (
	ErrInvalidInput       = errors.New("vfs: invalid input")
	ErrPathAlreadyExists  = errors.New("vfs: virtual path already exists")
	ErrAmbiguousPath      = errors.New("vfs: virtual path is ambiguous")
	ErrFileNotFound       = errors.New("vfs: file not found")
	ErrFileDeleted        = errors.New("vfs: file is deleted")
	ErrInvalidKind        = errors.New("vfs: invalid file kind for operation")
	ErrInvalidWriteMode   = errors.New("vfs: invalid write mode")
	ErrPatchNoMatch       = errors.New("vfs: patch text matched zero locations")
	ErrPatchMultipleMatch = errors.New("vfs: patch text matched multiple locations")
	ErrTxnRequired        = errors.New("vfs: transaction required")
	ErrTxnMismatch        = errors.New("vfs: transaction mismatch")
)
