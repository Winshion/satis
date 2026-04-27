package sandbox

import (
	"crypto/sha256"
	"encoding/json"
	"encoding/hex"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"syscall"

	"satis/vfs"
)

type localPersistence struct {
	mountDir    string
	stateDir    string
	stateDirRel string
}

func NewPersistence(cfg vfs.Config) (vfs.Persistence, error) {
	if cfg.MountDir == "" {
		return nil, vfs.ErrInvalidInput
	}
	stateDir := cfg.StateDir
	if stateDir == "" {
		stateDir = filepath.Join(cfg.MountDir, ".sati_vfs")
	}
	if err := os.MkdirAll(cfg.MountDir, 0o755); err != nil {
		return nil, err
	}
	if err := ensureStateLayout(stateDir); err != nil {
		return nil, err
	}
	stateDirRel := ""
	if rel, err := filepath.Rel(cfg.MountDir, stateDir); err == nil &&
		rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		stateDirRel = rel
	}
	return &localPersistence{
		mountDir:    cfg.MountDir,
		stateDir:    stateDir,
		stateDirRel: stateDirRel,
	}, nil
}

func NewDiskService(cfg vfs.Config) (*vfs.DiskService, error) {
	store, err := NewPersistence(cfg)
	if err != nil {
		return nil, err
	}
	return vfs.NewPersistentDiskService(vfs.DiskOptions{
		GC: cfg.GC,
	}, store)
}

func (p *localPersistence) EnsureStateLayout() error { return ensureStateLayout(p.stateDir) }

func (p *localPersistence) LoadRuntimeData() ([]byte, error) {
	return os.ReadFile(filepath.Join(p.stateDir, "runtime.json"))
}

func (p *localPersistence) LoadVersionData(fileID vfs.FileID) ([]byte, error) {
	return os.ReadFile(filepath.Join(p.stateDir, "versions", string(fileID)+".json"))
}

func (p *localPersistence) LoadObject(fileID vfs.FileID, version vfs.VersionEntry, kind vfs.FileKind) ([]byte, error) {
	objectPath := objectPathForID(p.stateDir, fileID, version.Generation, version.ObjectID)
	data, err := os.ReadFile(objectPath)
	if err != nil {
		if os.IsNotExist(err) {
			data, err = os.ReadFile(legacyObjectPath(p.stateDir, fileID, version.Generation))
		}
		if os.IsNotExist(err) && kind == vfs.FileKindDirectory {
			return nil, err
		}
		return data, err
	}
	return data, nil
}

func (p *localPersistence) SaveMeta(fileID vfs.FileID, data []byte) error {
	return writeFileAtomic(p.stateDir, filepath.Join(p.stateDir, "meta", string(fileID)+".json"), data)
}

func (p *localPersistence) SaveVersions(fileID vfs.FileID, data []byte) error {
	return writeFileAtomic(p.stateDir, filepath.Join(p.stateDir, "versions", string(fileID)+".json"), data)
}

func (p *localPersistence) SaveObject(fileID vfs.FileID, generation vfs.Generation, objectID string, payload []byte) error {
	if err := os.MkdirAll(filepath.Join(p.stateDir, "objects", string(fileID)), 0o755); err != nil {
		return err
	}
	return writeFileAtomic(p.stateDir, objectPathForID(p.stateDir, fileID, generation, objectID), payload)
}

func (p *localPersistence) SaveRuntimeData(data []byte) error {
	return writeFileAtomic(p.stateDir, filepath.Join(p.stateDir, "runtime.json"), data)
}

func (p *localPersistence) mountPath(virtualPath string) (string, error) {
	cleaned := normalizeVirtualPath(virtualPath)
	rel := strings.TrimPrefix(cleaned, "/")
	abs := filepath.Join(p.mountDir, rel)
	checkRel, err := filepath.Rel(p.mountDir, abs)
	if err != nil {
		return "", err
	}
	if checkRel == ".." || strings.HasPrefix(checkRel, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("vfs security: path escapes mount dir: %w", vfs.ErrInvalidInput)
	}
	return abs, nil
}

func (p *localPersistence) MaterializedPath(virtualPath string, kind vfs.FileKind, summary vfs.PathBindingSummary) (string, error) {
	cleaned := normalizeVirtualPath(virtualPath)
	abs, err := p.mountPath(cleaned)
	if err != nil {
		return "", err
	}
	if kind == vfs.FileKindDirectory {
		return abs, nil
	}
	if !summary.HasDirectory(cleaned) && summary.NonDirectoryCount(cleaned) == 1 {
		return abs, nil
	}
	trimmed := strings.TrimPrefix(cleaned, "/")
	return filepath.Join(p.mountDir, ".sati_shadow", trimmed) + ".__satis_" + string(kind) + "__", nil
}

func (p *localPersistence) CheckMaterializedPath(path string) error {
	return checkNoSymlinkChain(p.mountDir, path)
}

func (p *localPersistence) RemoveMaterialized(path string) error { return os.Remove(path) }

func (p *localPersistence) EnsureMaterializedDir(path string) error { return os.MkdirAll(path, 0o755) }

func (p *localPersistence) WriteMaterialized(path string, payload []byte) error {
	return writeFileAtomic(p.mountDir, path, payload)
}

func (p *localPersistence) ImportHostPath(virtualPath string) (fs.FileInfo, []byte, error) {
	abs, err := p.mountPath(virtualPath)
	if err != nil {
		return nil, nil, err
	}
	info, err := os.Stat(abs)
	if err != nil {
		return nil, nil, err
	}
	if info.IsDir() {
		return info, nil, nil
	}
	data, err := os.ReadFile(abs)
	return info, data, err
}

func (p *localPersistence) ListDirHostEntries(virtualPath string) ([]vfs.DirEntry, error) {
	abs, err := p.mountPath(virtualPath)
	if err != nil {
		return nil, err
	}
	hostEntries, err := os.ReadDir(abs)
	if err != nil {
		return nil, err
	}
	entries := make([]vfs.DirEntry, 0, len(hostEntries))
	for _, entry := range hostEntries {
		if virtualPath == "/" && entry.Name() == ".sati_shadow" {
			continue
		}
		path := virtualPath
		if path == "/" {
			path += entry.Name()
		} else {
			path += "/" + entry.Name()
		}
		kind := vfs.FileKindText
		if entry.IsDir() {
			kind = vfs.FileKindDirectory
		}
		entries = append(entries, vfs.DirEntry{Name: entry.Name(), VirtualPath: path, Kind: kind})
	}
	return entries, nil
}

func (p *localPersistence) StatVirtualPath(virtualPath string) (fs.FileInfo, error) {
	abs, err := p.mountPath(virtualPath)
	if err != nil {
		return nil, err
	}
	return os.Stat(abs)
}

func (p *localPersistence) GlobVirtualPaths(pattern string) ([]string, error) {
	trimmed := strings.TrimPrefix(pattern, "/")
	matches, err := filepath.Glob(filepath.Join(p.mountDir, trimmed))
	if err != nil {
		return nil, fmt.Errorf("vfs glob error for %q: %w", pattern, err)
	}
	out := make([]string, 0, len(matches))
	sep := string(filepath.Separator)
	for _, absPath := range matches {
		rel, err := filepath.Rel(p.mountDir, absPath)
		if err != nil {
			continue
		}
		if rel == ".sati_shadow" || strings.HasPrefix(rel, ".sati_shadow"+sep) {
			continue
		}
		if p.stateDirRel != "" && (rel == p.stateDirRel || strings.HasPrefix(rel, p.stateDirRel+sep)) {
			continue
		}
		out = append(out, "/"+filepath.ToSlash(rel))
	}
	sort.Strings(out)
	return out, nil
}

func (p *localPersistence) AcquirePathLocks(paths []string) (func(), error) {
	if len(paths) == 0 {
		return func() {}, nil
	}
	sortedPaths := append([]string(nil), paths...)
	sort.Strings(sortedPaths)
	lockFiles := make([]*os.File, 0, len(sortedPaths))
	lockPaths := make([]string, 0, len(sortedPaths))
	releaseOne := func(f *os.File, lockPath string) {
		if f != nil {
			_ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
			_ = f.Close()
		}
		_ = os.Remove(lockPath)
	}
	for _, path := range sortedPaths {
		sum := sha256.Sum256([]byte(path))
		lockPath := filepath.Join(p.stateDir, "locks", hex.EncodeToString(sum[:])+".lock")
		lockFile, err := os.OpenFile(lockPath, os.O_CREATE|os.O_RDWR, 0o644)
		if err != nil {
			for i := len(lockFiles) - 1; i >= 0; i-- {
				releaseOne(lockFiles[i], lockPaths[i])
			}
			return nil, err
		}
		if err := syscall.Flock(int(lockFile.Fd()), syscall.LOCK_EX); err != nil {
			releaseOne(lockFile, lockPath)
			for i := len(lockFiles) - 1; i >= 0; i-- {
				releaseOne(lockFiles[i], lockPaths[i])
			}
			return nil, err
		}
		lockFiles = append(lockFiles, lockFile)
		lockPaths = append(lockPaths, lockPath)
	}
	return func() {
		for i := len(lockFiles) - 1; i >= 0; i-- {
			releaseOne(lockFiles[i], lockPaths[i])
		}
	}, nil
}

func (p *localPersistence) RemoveStateArtifacts(fileID vfs.FileID, versions []vfs.VersionEntry) error {
	if err := os.Remove(filepath.Join(p.stateDir, "meta", string(fileID)+".json")); err != nil && !os.IsNotExist(err) {
		return err
	}
	if err := os.Remove(filepath.Join(p.stateDir, "versions", string(fileID)+".json")); err != nil && !os.IsNotExist(err) {
		return err
	}
	for _, version := range versions {
		objectID := version.ObjectID
		if objectID == "" {
			objectID = objectIDForVersion(fileID, version.Generation)
		}
		if err := os.Remove(objectPathForID(p.stateDir, fileID, version.Generation, objectID)); err != nil && !os.IsNotExist(err) {
			return err
		}
	}
	if err := os.RemoveAll(filepath.Join(p.stateDir, "objects", string(fileID))); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

func LoadRuntimeSnapshot(cfg vfs.Config) (*vfs.RuntimeSnapshot, error) {
	stateDir := cfg.StateDir
	if stateDir == "" && cfg.MountDir != "" {
		stateDir = filepath.Join(cfg.MountDir, ".sati_vfs")
	}
	data, err := os.ReadFile(filepath.Join(stateDir, "runtime.json"))
	if err != nil {
		return nil, err
	}
	var snapshot vfs.RuntimeSnapshot
	if err := json.Unmarshal(data, &snapshot); err != nil {
		return nil, err
	}
	return &snapshot, nil
}

func LoadVersionEntries(cfg vfs.Config, fileID vfs.FileID) ([]vfs.VersionEntry, error) {
	stateDir := cfg.StateDir
	if stateDir == "" && cfg.MountDir != "" {
		stateDir = filepath.Join(cfg.MountDir, ".sati_vfs")
	}
	data, err := os.ReadFile(filepath.Join(stateDir, "versions", string(fileID)+".json"))
	if err != nil {
		return nil, err
	}
	var versions []vfs.VersionEntry
	if err := json.Unmarshal(data, &versions); err != nil {
		return nil, err
	}
	return versions, nil
}

func LoadCurrentContent(cfg vfs.Config, meta vfs.FileMeta) ([]byte, error) {
	if meta.FileID == "" || meta.Kind == vfs.FileKindDirectory || meta.CurrentGeneration == 0 {
		return nil, nil
	}
	stateDir := cfg.StateDir
	if stateDir == "" && cfg.MountDir != "" {
		stateDir = filepath.Join(cfg.MountDir, ".sati_vfs")
	}
	versions, err := LoadVersionEntries(cfg, meta.FileID)
	if err != nil {
		return nil, err
	}
	for _, version := range versions {
		if version.Generation == meta.CurrentGeneration {
			return (&localPersistence{stateDir: stateDir}).LoadObject(meta.FileID, version, meta.Kind)
		}
	}
	return (&localPersistence{stateDir: stateDir}).LoadObject(meta.FileID, vfs.VersionEntry{Generation: meta.CurrentGeneration}, meta.Kind)
}

func normalizeVirtualPath(path string) string {
	path = strings.TrimSpace(path)
	if path == "" {
		return "/"
	}
	if !strings.HasPrefix(path, "/") {
		path = "/" + path
	}
	cleaned := filepath.Clean(path)
	if cleaned == "." {
		return "/"
	}
	if !strings.HasPrefix(cleaned, "/") {
		cleaned = "/" + cleaned
	}
	return cleaned
}

func ensureStateLayout(stateDir string) error {
	for _, dir := range []string{
		stateDir,
		filepath.Join(stateDir, "locks"),
		filepath.Join(stateDir, "meta"),
		filepath.Join(stateDir, "versions"),
		filepath.Join(stateDir, "objects"),
	} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return err
		}
	}
	return nil
}

func checkNoSymlinkChain(base, abs string) error {
	rel, err := filepath.Rel(base, abs)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return fmt.Errorf("vfs security: path escapes mount dir: %w", vfs.ErrInvalidInput)
	}
	parts := strings.Split(rel, string(filepath.Separator))
	current := base
	for _, part := range parts {
		if part == "" || part == "." {
			continue
		}
		current = filepath.Join(current, part)
		fi, lerr := os.Lstat(current)
		if lerr != nil {
			if os.IsNotExist(lerr) {
				break
			}
			return lerr
		}
		if fi.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("vfs security: path %q traverses symbolic link: %w", current, vfs.ErrInvalidInput)
		}
	}
	return nil
}

func writeFileAtomic(base, path string, data []byte) error {
	dir := filepath.Dir(path)
	if err := checkNoSymlinkChain(base, dir); err != nil {
		return err
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	tmpFile, err := os.CreateTemp(dir, filepath.Base(path)+".tmp-*")
	if err != nil {
		return err
	}
	tmpPath := tmpFile.Name()
	defer func() { _ = os.Remove(tmpPath) }()
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
	if err := checkNoSymlinkChain(base, path); err != nil {
		return err
	}
	return os.Rename(tmpPath, path)
}

func objectPathForID(stateDir string, fileID vfs.FileID, generation vfs.Generation, objectID string) string {
	if objectID == "" {
		return legacyObjectPath(stateDir, fileID, generation)
	}
	return filepath.Join(stateDir, "objects", objectID+".bin")
}

func legacyObjectPath(stateDir string, fileID vfs.FileID, generation vfs.Generation) string {
	return filepath.Join(stateDir, "objects", string(fileID), fmt.Sprintf("%d.bin", generation))
}

func objectIDForVersion(fileID vfs.FileID, generation vfs.Generation) string {
	return "obj_" + string(fileID) + "_" + fmt.Sprintf("%d", generation)
}
