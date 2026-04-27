package vfs

import (
	"context"
	"reflect"
	"sort"
	"strings"
	"testing"
)

func BenchmarkMemoryTxnResolvePathInput(b *testing.B) {
	state := buildBenchmarkTxnState()
	input := ResolveInput{VirtualPath: "/docs/shared", ExpectedKind: ""}
	b.Run("optimized", func(b *testing.B) {
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			_, _ = state.resolvePathInput(input)
		}
	})
	b.Run("reference", func(b *testing.B) {
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			_, _ = resolvePathInputReference(state, input)
		}
	})
}

func BenchmarkMemoryTxnListDir(b *testing.B) {
	state := buildBenchmarkTxnState()
	b.Run("optimized", func(b *testing.B) {
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			_, _ = state.listDir("/docs")
		}
	})
	b.Run("reference", func(b *testing.B) {
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			_, _ = listDirReference(state, "/docs")
		}
	})
}

func TestMemoryTxnOverlayQueriesMatchReference(t *testing.T) {
	state := buildBenchmarkTxnState()

	for _, path := range []string{"/docs/shared", "/docs/deleted", "/docs/new", "/docs", "/missing"} {
		gotBindings := state.bindingsForPath(path)
		wantBindings := bindingsForPath(currentPathIndexReference(state), path)
		if !reflect.DeepEqual(gotBindings, wantBindings) {
			t.Fatalf("bindingsForPath(%q) mismatch: got %#v want %#v", path, gotBindings, wantBindings)
		}
		if got, want := state.hasAnyBinding(path), hasAnyBinding(currentPathIndexReference(state), path); got != want {
			t.Fatalf("hasAnyBinding(%q) mismatch: got %v want %v", path, got, want)
		}
	}

	for _, path := range []string{"/docs", "/docs/nested", "/missing"} {
		if got, want := state.hasVisibleChild(path), hasVisibleChildReference(currentPathIndexReference(state), path); got != want {
			t.Fatalf("hasVisibleChild(%q) mismatch: got %v want %v", path, got, want)
		}
	}

	for _, path := range []string{"/", "/docs", "/docs/nested"} {
		got, err := state.listDir(path)
		want, wantErr := listDirReference(state, path)
		if !sameError(err, wantErr) {
			t.Fatalf("listDir(%q) error mismatch: got %v want %v", path, err, wantErr)
		}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("listDir(%q) mismatch: got %#v want %#v", path, got, want)
		}
	}

	for _, input := range []ResolveInput{
		{VirtualPath: "/docs/shared"},
		{VirtualPath: "/docs/new"},
		{VirtualPath: "/docs/deleted"},
		{VirtualPath: "/docs/shared", ExpectedKind: FileKindText},
	} {
		got, err := state.resolvePathInput(input)
		want, wantErr := resolvePathInputReference(state, input)
		if !sameError(err, wantErr) {
			t.Fatalf("resolvePathInput(%#v) error mismatch: got %v want %v", input, err, wantErr)
		}
		if got != want {
			t.Fatalf("resolvePathInput(%#v) mismatch: got %q want %q", input, got, want)
		}
	}
}

func TestMemoryServiceTransactionBehaviorUnchanged(t *testing.T) {
	ctx := context.Background()
	svc := NewMemoryService()
	txn, err := svc.BeginChunkTxn(ctx, ChunkID("chunk"))
	if err != nil {
		t.Fatalf("begin txn: %v", err)
	}
	if _, err := svc.Create(ctx, txn, CreateInput{
		VirtualPath:    "/docs",
		Kind:           FileKindDirectory,
		CreatorChunkID: ChunkID("chunk"),
	}); err != nil {
		t.Fatalf("create docs dir: %v", err)
	}
	if _, err := svc.Create(ctx, txn, CreateInput{
		VirtualPath:    "/docs/readme.txt",
		Kind:           FileKindText,
		CreatorChunkID: ChunkID("chunk"),
		InitialText:    "hello",
	}); err != nil {
		t.Fatalf("create file: %v", err)
	}
	if _, err := svc.Create(ctx, txn, CreateInput{
		VirtualPath:    "/docs/nested",
		Kind:           FileKindDirectory,
		CreatorChunkID: ChunkID("chunk"),
	}); err != nil {
		t.Fatalf("create nested dir: %v", err)
	}
	if _, err := svc.Create(ctx, txn, CreateInput{
		VirtualPath:    "/docs/nested/child.txt",
		Kind:           FileKindText,
		CreatorChunkID: ChunkID("chunk"),
		InitialText:    "child",
	}); err != nil {
		t.Fatalf("create child file: %v", err)
	}
	if _, err := svc.Delete(ctx, txn, DeleteInput{
		Target:  ResolveInput{VirtualPath: "/docs/readme.txt", ExpectedKind: FileKindText},
		ChunkID: ChunkID("chunk"),
	}); err != nil {
		t.Fatalf("delete file: %v", err)
	}
	if _, err := svc.Create(ctx, txn, CreateInput{
		VirtualPath:    "/docs/readme.txt",
		Kind:           FileKindGenerated,
		CreatorChunkID: ChunkID("chunk"),
		InitialText:    "regen",
	}); err != nil {
		t.Fatalf("recreate generated file: %v", err)
	}

	entries, err := svc.ListDir(ctx, txn, "/docs")
	if err != nil {
		t.Fatalf("list dir: %v", err)
	}
	gotNames := make([]string, 0, len(entries))
	for _, entry := range entries {
		gotNames = append(gotNames, entry.Name+":"+string(entry.Kind))
	}
	sort.Strings(gotNames)
	wantNames := []string{"nested:directory", "readme.txt:generated"}
	if !reflect.DeepEqual(gotNames, wantNames) {
		t.Fatalf("unexpected entries: got %v want %v", gotNames, wantNames)
	}
}

func buildBenchmarkTxnState() *memoryTxnState {
	baseFiles := make(map[FileID]*memoryFile)
	basePathIndex := make(map[string]FileID)
	addFile := func(id FileID, virtualPath string, kind FileKind) {
		baseFiles[id] = &memoryFile{meta: FileMeta{FileID: id, VirtualPath: virtualPath, Kind: kind, DeleteState: DeleteStateActive}}
		setPathBinding(basePathIndex, virtualPath, kind, id)
	}
	addFile("dir_docs", "/docs", FileKindDirectory)
	addFile("shared_text", "/docs/shared", FileKindText)
	addFile("shared_generated", "/docs/shared", FileKindGenerated)
	addFile("deleted_text", "/docs/deleted", FileKindText)
	addFile("nested_dir", "/docs/nested", FileKindDirectory)
	addFile("nested_child", "/docs/nested/child.txt", FileKindText)
	for i := 0; i < 512; i++ {
		path := "/bulk/file_" + strings.Repeat("x", i%5) + "_" + string(rune('a'+(i%26))) + ".txt"
		addFile(FileID("bulk_"+path), path, FileKindText)
	}

	txnFiles := map[FileID]*memoryFile{
		"new_text": {meta: FileMeta{FileID: "new_text", VirtualPath: "/docs/new", Kind: FileKindText, DeleteState: DeleteStateActive}},
	}
	txnPathIndex := make(map[string]FileID)
	setPathBinding(txnPathIndex, "/docs/new", FileKindText, "new_text")
	deletedPaths := map[string]bool{
		pathKindKey("/docs/deleted", FileKindText): true,
	}

	return &memoryTxnState{
		baseFiles:     baseFiles,
		basePathIndex: basePathIndex,
		files:         txnFiles,
		pathIndex:     txnPathIndex,
		deletedPaths:  deletedPaths,
	}
}

func currentPathIndexReference(t *memoryTxnState) map[string]FileID {
	out := clonePathIndex(t.basePathIndex)
	for key, deleted := range t.deletedPaths {
		if deleted {
			delete(out, key)
		}
	}
	for key, fileID := range t.pathIndex {
		out[key] = fileID
	}
	return out
}

func hasVisibleChildReference(pathIndex map[string]FileID, dirPath string) bool {
	for key := range pathIndex {
		logicalPath, _, ok := splitPathKindKey(key)
		if !ok {
			continue
		}
		_, _, _, ok = directChild(dirPath, logicalPath)
		if ok {
			return true
		}
	}
	return false
}

func resolvePathInputReference(t *memoryTxnState, input ResolveInput) (FileID, error) {
	if input.ExpectedKind != "" {
		fileID, exists := t.resolvePath(input.VirtualPath, input.ExpectedKind)
		if !exists {
			return "", ErrFileNotFound
		}
		return fileID, nil
	}
	bindings := bindingsForPath(currentPathIndexReference(t), input.VirtualPath)
	switch len(bindings) {
	case 0:
		return "", ErrFileNotFound
	case 1:
		for _, fileID := range bindings {
			return fileID, nil
		}
	default:
		return "", ErrAmbiguousPath
	}
	return "", ErrFileNotFound
}

func listDirReference(t *memoryTxnState, virtualPath string) ([]DirEntry, error) {
	cleaned := normalizeVirtualPath(virtualPath)
	current := currentPathIndexReference(t)
	if cleaned != "/" {
		if fileID, ok := lookupPathBinding(current, cleaned, FileKindDirectory); ok {
			file := t.lookupFile(fileID)
			if file == nil || file.meta.DeleteState == DeleteStateDeleted {
				return nil, ErrFileNotFound
			}
		} else if !hasVisibleChildReference(current, cleaned) {
			if hasAnyBinding(current, cleaned) {
				return nil, ErrInvalidKind
			}
			return nil, ErrFileNotFound
		}
	}

	entriesByBinding := make(map[string]DirEntry)
	for key, fileID := range current {
		logicalPath, kind, ok := splitPathKindKey(key)
		if !ok {
			continue
		}
		name, childPath, descends, ok := directChild(cleaned, logicalPath)
		if !ok {
			continue
		}
		if descends {
			entryKey := pathKindKey(childPath, FileKindDirectory)
			entriesByBinding[entryKey] = DirEntry{Name: name, VirtualPath: childPath, Kind: FileKindDirectory}
			continue
		}
		file := t.lookupFile(fileID)
		if file == nil || file.meta.DeleteState == DeleteStateDeleted {
			continue
		}
		entryKey := pathKindKey(childPath, kind)
		entriesByBinding[entryKey] = DirEntry{Name: name, VirtualPath: childPath, Kind: kind}
	}
	entryKeys := make([]string, 0, len(entriesByBinding))
	for key := range entriesByBinding {
		entryKeys = append(entryKeys, key)
	}
	sort.Slice(entryKeys, func(i, j int) bool {
		left := entriesByBinding[entryKeys[i]]
		right := entriesByBinding[entryKeys[j]]
		if left.Name != right.Name {
			return left.Name < right.Name
		}
		return left.Kind < right.Kind
	})
	entries := make([]DirEntry, 0, len(entryKeys))
	for _, key := range entryKeys {
		entries = append(entries, entriesByBinding[key])
	}
	return entries, nil
}

func sameError(left error, right error) bool {
	if left == nil || right == nil {
		return left == right
	}
	return left.Error() == right.Error()
}
