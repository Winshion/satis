package vfs

import (
	"path"
	"sort"
)

const pathKindSeparator = "\x1f"

var indexedFileKinds = [...]FileKind{
	FileKindText,
	FileKindBinary,
	FileKindDirectory,
	FileKindGenerated,
	FileKindEphemeral,
}

func pathKindKey(virtualPath string, kind FileKind) string {
	return normalizeVirtualPath(virtualPath) + pathKindSeparator + string(kind)
}

func splitPathKindKey(key string) (string, FileKind, bool) {
	for _, kind := range indexedFileKinds {
		suffix := pathKindSeparator + string(kind)
		if len(key) <= len(suffix) || key[len(key)-len(suffix):] != suffix {
			continue
		}
		return key[:len(key)-len(suffix)], kind, true
	}
	return "", "", false
}

func clonePathIndex(src map[string]FileID) map[string]FileID {
	dst := make(map[string]FileID, len(src))
	for k, v := range src {
		dst[k] = v
	}
	return dst
}

func lookupPathBinding(pathIndex map[string]FileID, virtualPath string, kind FileKind) (FileID, bool) {
	fileID, ok := pathIndex[pathKindKey(virtualPath, kind)]
	return fileID, ok
}

func setPathBinding(pathIndex map[string]FileID, virtualPath string, kind FileKind, fileID FileID) {
	pathIndex[pathKindKey(virtualPath, kind)] = fileID
}

func deletePathBinding(pathIndex map[string]FileID, virtualPath string, kind FileKind) {
	delete(pathIndex, pathKindKey(virtualPath, kind))
}

func bindingsForPath(pathIndex map[string]FileID, virtualPath string) map[FileKind]FileID {
	cleaned := normalizeVirtualPath(virtualPath)
	out := make(map[FileKind]FileID, len(indexedFileKinds))
	for _, kind := range indexedFileKinds {
		if fileID, ok := lookupPathBinding(pathIndex, cleaned, kind); ok {
			out[kind] = fileID
		}
	}
	return out
}

func hasAnyBinding(pathIndex map[string]FileID, virtualPath string) bool {
	cleaned := normalizeVirtualPath(virtualPath)
	for _, kind := range indexedFileKinds {
		if _, ok := lookupPathBinding(pathIndex, cleaned, kind); ok {
			return true
		}
	}
	return false
}

func countNonDirectoryBindings(pathIndex map[string]FileID, virtualPath string) int {
	count := 0
	cleaned := normalizeVirtualPath(virtualPath)
	for _, kind := range indexedFileKinds {
		if kind != FileKindDirectory {
			if _, ok := lookupPathBinding(pathIndex, cleaned, kind); ok {
				count++
			}
		}
	}
	return count
}

func logicalBindingsEqual(left map[string]FileID, right map[string]FileID, virtualPath string) bool {
	cleaned := normalizeVirtualPath(virtualPath)
	for _, kind := range indexedFileKinds {
		leftID, leftOK := lookupPathBinding(left, cleaned, kind)
		rightID, rightOK := lookupPathBinding(right, cleaned, kind)
		if leftOK != rightOK || leftID != rightID {
			return false
		}
	}
	return true
}

func logicalPaths(pathIndex map[string]FileID) []string {
	seen := make(map[string]struct{})
	for key := range pathIndex {
		virtualPath, _, ok := splitPathKindKey(key)
		if !ok {
			continue
		}
		seen[virtualPath] = struct{}{}
	}
	out := make([]string, 0, len(seen))
	for virtualPath := range seen {
		out = append(out, virtualPath)
	}
	sort.Strings(out)
	return out
}

func logicalPathMatchesPattern(pattern string, virtualPath string) bool {
	ok, err := path.Match(pattern, virtualPath)
	return err == nil && ok
}
