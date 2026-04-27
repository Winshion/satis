// Package vfs contains the SATI virtual file system kernel.
//
// The v0 goal of this package is to provide a small, stable runtime layer
// around file objects, versions, events, and chunk-scoped transactional
// behavior. It should not depend on SatisIL syntax details.
package vfs
