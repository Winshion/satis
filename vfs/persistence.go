package vfs

import "io/fs"

type PathBindingSummary struct {
	hasDirectory   map[string]bool
	nonDirectoryN  map[string]int
}

func BuildPathBindingSummary(pathIndex map[string]FileID) PathBindingSummary {
	summary := PathBindingSummary{
		hasDirectory:  make(map[string]bool),
		nonDirectoryN: make(map[string]int),
	}
	for key := range pathIndex {
		virtualPath, kind, ok := splitPathKindKey(key)
		if !ok {
			continue
		}
		if kind == FileKindDirectory {
			summary.hasDirectory[virtualPath] = true
			continue
		}
		summary.nonDirectoryN[virtualPath]++
	}
	return summary
}

func (s PathBindingSummary) HasDirectory(virtualPath string) bool {
	return s.hasDirectory[virtualPath]
}

func (s PathBindingSummary) NonDirectoryCount(virtualPath string) int {
	return s.nonDirectoryN[virtualPath]
}

type Persistence interface {
	EnsureStateLayout() error
	LoadRuntimeData() ([]byte, error)
	LoadVersionData(fileID FileID) ([]byte, error)
	LoadObject(fileID FileID, version VersionEntry, kind FileKind) ([]byte, error)
	SaveMeta(fileID FileID, data []byte) error
	SaveVersions(fileID FileID, data []byte) error
	SaveObject(fileID FileID, generation Generation, objectID string, payload []byte) error
	SaveRuntimeData(data []byte) error

	MaterializedPath(virtualPath string, kind FileKind, summary PathBindingSummary) (string, error)
	CheckMaterializedPath(path string) error
	RemoveMaterialized(path string) error
	EnsureMaterializedDir(path string) error
	WriteMaterialized(path string, payload []byte) error

	ImportHostPath(virtualPath string) (fs.FileInfo, []byte, error)
	ListDirHostEntries(virtualPath string) ([]DirEntry, error)
	StatVirtualPath(virtualPath string) (fs.FileInfo, error)
	GlobVirtualPaths(pattern string) ([]string, error)

	AcquirePathLocks(paths []string) (func(), error)
	RemoveStateArtifacts(fileID FileID, versions []VersionEntry) error
}

type DiskOptions struct {
	GC GCConfig
}
