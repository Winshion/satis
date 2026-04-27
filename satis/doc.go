// Package satis contains the future SATI runtime and SatisIL-facing kernel
// layer built on top of the vfs package.
//
// The v0 goal of this package is to keep language/runtime concerns separate
// from the file object kernel, so that SatisIL maps onto stable VFS primitives
// instead of directly manipulating host filesystem behavior.
package satis
