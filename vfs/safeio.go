package vfs

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

func safeEnsureStateLayout(storage localStorage, stateDir string) error {
	for _, dir := range []string{
		stateDir,
		filepath.Join(stateDir, "locks"),
		filepath.Join(stateDir, "meta"),
		filepath.Join(stateDir, "versions"),
		filepath.Join(stateDir, "objects"),
	} {
		if err := storage.MkdirAll(dir, 0o755); err != nil {
			return err
		}
	}
	return nil
}

func safeCheckNoSymlinkChain(storage localStorage, base, abs string) error {
	rel, err := filepath.Rel(base, abs)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return fmt.Errorf("vfs security: path escapes mount dir: %w", ErrInvalidInput)
	}
	parts := strings.Split(rel, string(filepath.Separator))
	current := base
	for _, part := range parts {
		if part == "" || part == "." {
			continue
		}
		current = filepath.Join(current, part)
		fi, lerr := storage.Lstat(current)
		if lerr != nil {
			if os.IsNotExist(lerr) {
				break
			}
			return lerr
		}
		if fi.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("vfs security: path %q traverses symbolic link: %w", current, ErrInvalidInput)
		}
	}
	return nil
}

func safeWriteFileAtomic(storage localStorage, base, path string, data []byte) error {
	dir := filepath.Dir(path)
	if err := safeCheckNoSymlinkChain(storage, base, dir); err != nil {
		return err
	}
	if err := storage.MkdirAll(dir, 0o755); err != nil {
		return err
	}

	tmpFile, err := storage.CreateTemp(dir, filepath.Base(path)+".tmp-*")
	if err != nil {
		return err
	}
	tmpPath := tmpFile.Name()
	defer func() {
		_ = storage.Remove(tmpPath)
	}()

	if _, err := tmpFile.Write(data); err != nil {
		_ = tmpFile.Close()
		return err
	}
	if err := tmpFile.Chmod(0o644); err != nil {
		_ = tmpFile.Close()
		return err
	}
	if err := tmpFile.Close(); err != nil {
		return err
	}
	if err := safeCheckNoSymlinkChain(storage, base, path); err != nil {
		return err
	}
	return storage.Rename(tmpPath, path)
}
