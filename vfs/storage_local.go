package vfs

import (
	"io/fs"
	"os"
	"path/filepath"
)

type localStorage interface {
	MkdirAll(path string, perm fs.FileMode) error
	ReadFile(path string) ([]byte, error)
	Stat(path string) (fs.FileInfo, error)
	Lstat(path string) (fs.FileInfo, error)
	ReadDir(path string) ([]fs.DirEntry, error)
	Remove(path string) error
	RemoveAll(path string) error
	Rename(oldPath string, newPath string) error
	CreateTemp(dir string, pattern string) (*os.File, error)
	OpenFile(name string, flag int, perm fs.FileMode) (*os.File, error)
	Glob(pattern string) ([]string, error)
}

type osLocalStorage struct{}

func (osLocalStorage) MkdirAll(path string, perm fs.FileMode) error { return os.MkdirAll(path, perm) }
func (osLocalStorage) ReadFile(path string) ([]byte, error)         { return os.ReadFile(path) }
func (osLocalStorage) Stat(path string) (fs.FileInfo, error)        { return os.Stat(path) }
func (osLocalStorage) Lstat(path string) (fs.FileInfo, error)       { return os.Lstat(path) }
func (osLocalStorage) ReadDir(path string) ([]fs.DirEntry, error)   { return os.ReadDir(path) }
func (osLocalStorage) Remove(path string) error                     { return os.Remove(path) }
func (osLocalStorage) RemoveAll(path string) error                  { return os.RemoveAll(path) }
func (osLocalStorage) Rename(oldPath string, newPath string) error  { return os.Rename(oldPath, newPath) }
func (osLocalStorage) CreateTemp(dir string, pattern string) (*os.File, error) {
	return os.CreateTemp(dir, pattern)
}
func (osLocalStorage) OpenFile(name string, flag int, perm fs.FileMode) (*os.File, error) {
	return os.OpenFile(name, flag, perm)
}
func (osLocalStorage) Glob(pattern string) ([]string, error) { return filepath.Glob(pattern) }
