package sandbox

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"unicode/utf8"

	"satis/satis"
	"satis/vfs"
)

// SystemPort exposes a sandbox-managed, read-only host directory to the
// runtime's `load` instruction.
type SystemPort struct {
	root string
}

func NewSystemPort(root string) (*SystemPort, error) {
	root = strings.TrimSpace(root)
	if root == "" {
		return nil, nil
	}
	root = filepath.Clean(root)
	if !filepath.IsAbs(root) {
		return nil, fmt.Errorf("sandbox system_port error: root must be absolute")
	}
	if err := os.MkdirAll(root, 0o755); err != nil {
		return nil, err
	}
	return &SystemPort{root: root}, nil
}

func (p *SystemPort) Stat(_ context.Context, virtualPath string) (satis.LoadEntry, error) {
	abs, cleaned, err := p.absPath(virtualPath)
	if err != nil {
		return satis.LoadEntry{}, err
	}
	info, err := os.Stat(abs)
	if err != nil {
		return satis.LoadEntry{}, err
	}
	return satis.LoadEntry{
		Name:        filepath.Base(cleaned),
		VirtualPath: cleaned,
		IsDir:       info.IsDir(),
	}, nil
}

func (p *SystemPort) ListDir(_ context.Context, virtualPath string) ([]satis.LoadEntry, error) {
	abs, cleaned, err := p.absPath(virtualPath)
	if err != nil {
		return nil, err
	}
	info, err := os.Stat(abs)
	if err != nil {
		return nil, err
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("sandbox system_port error: %q is not a directory", cleaned)
	}
	items, err := os.ReadDir(abs)
	if err != nil {
		return nil, err
	}
	entries := make([]satis.LoadEntry, 0, len(items))
	for _, item := range items {
		childPath := cleaned
		if childPath == "/" {
			childPath += item.Name()
		} else {
			childPath += "/" + item.Name()
		}
		entries = append(entries, satis.LoadEntry{
			Name:        item.Name(),
			VirtualPath: childPath,
			IsDir:       item.IsDir(),
		})
	}
	sort.Slice(entries, func(i, j int) bool {
		if entries[i].Name != entries[j].Name {
			return entries[i].Name < entries[j].Name
		}
		return entries[i].VirtualPath < entries[j].VirtualPath
	})
	return entries, nil
}

func (p *SystemPort) Glob(_ context.Context, pattern string) ([]satis.LoadEntry, error) {
	absPattern, cleanedPattern, err := p.absPath(pattern)
	if err != nil {
		return nil, err
	}
	matches, err := filepath.Glob(absPattern)
	if err != nil {
		return nil, fmt.Errorf("sandbox system_port error for %q: %w", cleanedPattern, err)
	}
	entries := make([]satis.LoadEntry, 0, len(matches))
	for _, absMatch := range matches {
		if err := checkNoSymlinkChain(p.root, absMatch); err != nil {
			return nil, err
		}
		rel, err := filepath.Rel(p.root, absMatch)
		if err != nil {
			continue
		}
		if rel == "." || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			return nil, fmt.Errorf("sandbox system_port error: path escapes root")
		}
		info, err := os.Stat(absMatch)
		if err != nil {
			return nil, err
		}
		virtual := "/" + filepath.ToSlash(rel)
		entries = append(entries, satis.LoadEntry{
			Name:        filepath.Base(virtual),
			VirtualPath: virtual,
			IsDir:       info.IsDir(),
		})
	}
	sort.Slice(entries, func(i, j int) bool {
		return entries[i].VirtualPath < entries[j].VirtualPath
	})
	return entries, nil
}

func (p *SystemPort) ReadText(_ context.Context, virtualPath string) (string, error) {
	abs, cleaned, err := p.absPath(virtualPath)
	if err != nil {
		return "", err
	}
	info, err := os.Stat(abs)
	if err != nil {
		return "", err
	}
	if info.IsDir() {
		return "", fmt.Errorf("sandbox system_port error: %q is a directory", cleaned)
	}
	data, err := os.ReadFile(abs)
	if err != nil {
		return "", err
	}
	if looksBinary(data) {
		return "", fmt.Errorf("sandbox system_port error: binary file is not supported: %q", cleaned)
	}
	return string(data), nil
}

func (p *SystemPort) absPath(virtualPath string) (string, string, error) {
	if p == nil || p.root == "" {
		return "", "", fmt.Errorf("sandbox system_port error: system_port is not configured")
	}
	cleaned := normalizeVirtualPath(virtualPath)
	rel := strings.TrimPrefix(cleaned, "/")
	abs := filepath.Join(p.root, rel)
	checkRel, err := filepath.Rel(p.root, abs)
	if err != nil {
		return "", "", err
	}
	if checkRel == ".." || strings.HasPrefix(checkRel, ".."+string(filepath.Separator)) {
		return "", "", fmt.Errorf("sandbox system_port error: path escapes root: %w", vfs.ErrInvalidInput)
	}
	if err := checkNoSymlinkChain(p.root, abs); err != nil {
		return "", "", err
	}
	return abs, cleaned, nil
}

func looksBinary(data []byte) bool {
	if !utf8.Valid(data) {
		return true
	}
	for _, b := range data {
		if b == 0 {
			return true
		}
	}
	return false
}
