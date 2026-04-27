package vfs

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"testing"
)

func BenchmarkBindingsForPath(b *testing.B) {
	pathIndex := make(map[string]FileID, 1536)
	for i := 0; i < 512; i++ {
		base := "/bench/file_" + strings.Repeat("x", i%7) + "_" + string(rune('a'+(i%26)))
		pathIndex[pathKindKey(base, FileKindText)] = FileID("text_" + base)
		pathIndex[pathKindKey(base, FileKindGenerated)] = FileID("generated_" + base)
		pathIndex[pathKindKey(base, FileKindBinary)] = FileID("binary_" + base)
	}
	target := "/bench/file_x_a"
	b.Run("optimized", func(b *testing.B) {
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			_ = bindingsForPath(pathIndex, target)
		}
	})
	b.Run("reference", func(b *testing.B) {
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			_ = referenceBindingsForPath(pathIndex, target)
		}
	})
}

func BenchmarkAppendCommittedEvents(b *testing.B) {
	existing := make([]Event, 2000)
	pending := []Event{{EventID: "evt_new"}}
	b.Run("optimized", func(b *testing.B) {
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			_ = appendCommittedEvents(existing, pending, -1)
		}
	})
	b.Run("reference", func(b *testing.B) {
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			_ = append(append([]Event(nil), existing...), pending...)
		}
	})
}

func BenchmarkBuildPathBindingSummaryForPaths(b *testing.B) {
	pathIndex := make(map[string]FileID, 2000)
	for i := 0; i < 1000; i++ {
		base := "/docs/file_" + string(rune('a'+(i%26))) + "_" + string(rune('A'+(i%26)))
		pathIndex[pathKindKey(base, FileKindText)] = FileID("text_" + base)
		pathIndex[pathKindKey(base, FileKindGenerated)] = FileID("generated_" + base)
	}
	paths := []string{"/docs/file_a_A", "/docs/file_b_B", "/docs/file_c_C"}
	b.Run("optimized_subset", func(b *testing.B) {
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			_ = buildPathBindingSummaryForPaths(pathIndex, paths)
		}
	})
	b.Run("reference_full", func(b *testing.B) {
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			_ = BuildPathBindingSummary(pathIndex)
		}
	})
}

func TestPathIndexHelpersMatchReferenceBehavior(t *testing.T) {
	pathIndex := map[string]FileID{
		pathKindKey("/a.txt", FileKindText):          "file_text",
		pathKindKey("/a.txt", FileKindGenerated):     "file_generated",
		pathKindKey("/dir", FileKindDirectory):       "dir",
		pathKindKey("/bin", FileKindBinary):          "file_binary",
		pathKindKey("/ephemeral", FileKindEphemeral): "file_ephemeral",
		pathKindKey("/nested/object", FileKindText):  "nested_text",
		"/invalid": "ignored",
	}

	paths := []string{"/a.txt", "/dir", "/missing", "/bin", "/ephemeral"}
	for _, virtualPath := range paths {
		gotBindings := bindingsForPath(pathIndex, virtualPath)
		wantBindings := referenceBindingsForPath(pathIndex, virtualPath)
		if !reflect.DeepEqual(gotBindings, wantBindings) {
			t.Fatalf("bindingsForPath(%q) mismatch: got %#v want %#v", virtualPath, gotBindings, wantBindings)
		}
		if got, want := hasAnyBinding(pathIndex, virtualPath), referenceHasAnyBinding(pathIndex, virtualPath); got != want {
			t.Fatalf("hasAnyBinding(%q) mismatch: got %v want %v", virtualPath, got, want)
		}
		if got, want := countNonDirectoryBindings(pathIndex, virtualPath), referenceCountNonDirectoryBindings(pathIndex, virtualPath); got != want {
			t.Fatalf("countNonDirectoryBindings(%q) mismatch: got %d want %d", virtualPath, got, want)
		}
	}

	left := map[string]FileID{
		pathKindKey("/a.txt", FileKindText):      "file_text",
		pathKindKey("/a.txt", FileKindGenerated): "file_generated",
	}
	right := map[string]FileID{
		pathKindKey("/a.txt", FileKindText):      "file_text",
		pathKindKey("/a.txt", FileKindGenerated): "file_generated",
		pathKindKey("/other", FileKindText):      "other",
	}
	if got, want := logicalBindingsEqual(left, right, "/a.txt"), referenceLogicalBindingsEqual(left, right, "/a.txt"); got != want {
		t.Fatalf("logicalBindingsEqual(/a.txt) mismatch: got %v want %v", got, want)
	}
	if got, want := logicalBindingsEqual(left, right, "/other"), referenceLogicalBindingsEqual(left, right, "/other"); got != want {
		t.Fatalf("logicalBindingsEqual(/other) mismatch: got %v want %v", got, want)
	}
}

func TestDiskServiceCommitRoundTripPreservesBehavior(t *testing.T) {
	ctx := context.Background()
	mountDir := t.TempDir()
	service, err := NewPersistentDiskService(DiskOptions{}, newTestPersistence(t, mountDir))
	if err != nil {
		t.Fatalf("new disk service: %v", err)
	}

	txn, err := service.BeginChunkTxn(ctx, ChunkID("chunk_a"))
	if err != nil {
		t.Fatalf("begin txn: %v", err)
	}
	created, err := service.Create(ctx, txn, CreateInput{
		VirtualPath:    "/docs/readme.txt",
		Kind:           FileKindText,
		CreatorChunkID: ChunkID("chunk_a"),
		InitialText:    "hello world",
	})
	if err != nil {
		t.Fatalf("create file: %v", err)
	}
	if err := service.CommitChunkTxn(ctx, txn); err != nil {
		t.Fatalf("commit txn: %v", err)
	}

	reloaded, err := NewPersistentDiskService(DiskOptions{}, newTestPersistence(t, mountDir))
	if err != nil {
		t.Fatalf("reload disk service: %v", err)
	}

	ref, err := reloaded.Resolve(ctx, ResolveInput{VirtualPath: "/docs/readme.txt", ExpectedKind: FileKindText})
	if err != nil {
		t.Fatalf("resolve after reload: %v", err)
	}
	if ref.FileID != created.FileID || ref.VirtualPath != created.VirtualPath || ref.Kind != created.Kind {
		t.Fatalf("resolved file changed after reload: got %#v created %#v", ref, created)
	}

	readTxn, err := reloaded.BeginChunkTxn(ctx, ChunkID("chunk_b"))
	if err != nil {
		t.Fatalf("begin read txn: %v", err)
	}
	readResult, err := reloaded.Read(ctx, readTxn, ReadInput{
		Target: ResolveInput{VirtualPath: "/docs/readme.txt", ExpectedKind: FileKindText},
	})
	if err != nil {
		t.Fatalf("read after reload: %v", err)
	}
	if readResult.Text != "hello world" {
		t.Fatalf("unexpected text after reload: %q", readResult.Text)
	}
	if err := reloaded.RollbackChunkTxn(ctx, readTxn); err != nil {
		t.Fatalf("rollback read txn: %v", err)
	}

	runtimePath := filepath.Join(mountDir, ".sati_vfs", "runtime.json")
	if _, err := os.Stat(runtimePath); err != nil {
		t.Fatalf("runtime snapshot missing: %v", err)
	}
}

func TestDiskServiceCommitRejectsConflictingExternalSnapshot(t *testing.T) {
	ctx := context.Background()
	mountDir := t.TempDir()

	serviceA, err := NewPersistentDiskService(DiskOptions{}, newTestPersistence(t, mountDir))
	if err != nil {
		t.Fatalf("new disk service A: %v", err)
	}
	serviceB, err := NewPersistentDiskService(DiskOptions{}, newTestPersistence(t, mountDir))
	if err != nil {
		t.Fatalf("new disk service B: %v", err)
	}

	txnA, err := serviceA.BeginChunkTxn(ctx, ChunkID("chunk_a"))
	if err != nil {
		t.Fatalf("begin txn A: %v", err)
	}
	if _, err := serviceA.Create(ctx, txnA, CreateInput{
		VirtualPath:    "/shared.txt",
		Kind:           FileKindText,
		CreatorChunkID: ChunkID("chunk_a"),
		InitialText:    "from A",
	}); err != nil {
		t.Fatalf("create A: %v", err)
	}

	txnB, err := serviceB.BeginChunkTxn(ctx, ChunkID("chunk_b"))
	if err != nil {
		t.Fatalf("begin txn B: %v", err)
	}
	if _, err := serviceB.Create(ctx, txnB, CreateInput{
		VirtualPath:    "/shared.txt",
		Kind:           FileKindText,
		CreatorChunkID: ChunkID("chunk_b"),
		InitialText:    "from B",
	}); err != nil {
		t.Fatalf("create B: %v", err)
	}

	if err := serviceB.CommitChunkTxn(ctx, txnB); err != nil {
		t.Fatalf("commit B: %v", err)
	}
	if err := serviceA.CommitChunkTxn(ctx, txnA); !errors.Is(err, ErrTxnMismatch) {
		t.Fatalf("commit A should conflict with ErrTxnMismatch, got %v", err)
	}
}

func TestAppendCommittedEventsMatchesReference(t *testing.T) {
	existing := []Event{{EventID: "evt_1"}, {EventID: "evt_2"}}
	pending := []Event{{EventID: "evt_3"}}
	got := appendCommittedEvents(existing, pending, 10)
	want := append(append([]Event(nil), existing...), pending...)
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("appendCommittedEvents mismatch: got %#v want %#v", got, want)
	}
}

func TestSortedFileIDChangesAndSummarySubset(t *testing.T) {
	prevFiles := map[FileID]*memoryFile{
		"file_1": {meta: FileMeta{FileID: "file_1"}},
		"file_3": {meta: FileMeta{FileID: "file_3"}},
	}
	changed := map[FileID]*memoryFile{
		"file_2": {meta: FileMeta{FileID: "file_2"}},
	}
	gotIDs := applySortedFileIDChanges([]FileID{"file_1", "file_3"}, prevFiles, changed, nil)
	wantIDs := []FileID{"file_1", "file_2", "file_3"}
	if !reflect.DeepEqual(gotIDs, wantIDs) {
		t.Fatalf("sorted file ids mismatch: got %v want %v", gotIDs, wantIDs)
	}

	pathIndex := map[string]FileID{
		pathKindKey("/a", FileKindText):      "file_1",
		pathKindKey("/a", FileKindGenerated): "file_2",
		pathKindKey("/b", FileKindDirectory): "dir_1",
	}
	gotSummary := buildPathBindingSummaryForPaths(pathIndex, []string{"/a"})
	if gotSummary.HasDirectory("/a") || gotSummary.NonDirectoryCount("/a") != 2 {
		t.Fatalf("unexpected subset summary for /a: %+v", gotSummary)
	}
	if gotSummary.HasDirectory("/b") || gotSummary.NonDirectoryCount("/b") != 0 {
		t.Fatalf("subset summary should exclude /b: %+v", gotSummary)
	}
}

func TestDiskPressureCommitAndImport(t *testing.T) {
	ctx := context.Background()
	mountDir := t.TempDir()
	service, err := NewPersistentDiskService(DiskOptions{}, newTestPersistence(t, mountDir))
	if err != nil {
		t.Fatalf("new disk service: %v", err)
	}
	for i := 0; i < 100; i++ {
		txn, err := service.BeginChunkTxn(ctx, ChunkID("chunk"))
		if err != nil {
			t.Fatalf("begin txn %d: %v", i, err)
		}
		path := "/docs/file_" + strconv.Itoa(i) + ".txt"
		if _, err := service.Create(ctx, txn, CreateInput{VirtualPath: path, Kind: FileKindText, CreatorChunkID: ChunkID("chunk"), InitialText: "payload"}); err != nil {
			t.Fatalf("create %d: %v", i, err)
		}
		if err := service.CommitChunkTxn(ctx, txn); err != nil {
			t.Fatalf("commit %d: %v", i, err)
		}
	}
}

func TestPersistSnapshotSkipsRemovedCachedFileIDs(t *testing.T) {
	mountDir := t.TempDir()
	service, err := NewPersistentDiskService(DiskOptions{}, newTestPersistence(t, mountDir))
	if err != nil {
		t.Fatalf("new disk service: %v", err)
	}
	service.sortedFileIDs = []FileID{"file_kept", "file_removed"}
	nextFiles := map[FileID]*memoryFile{
		"file_kept": {meta: FileMeta{FileID: "file_kept", Kind: FileKindText, VirtualPath: "/kept"}},
	}
	prevFiles := map[FileID]*memoryFile{
		"file_kept":    {meta: FileMeta{FileID: "file_kept", Kind: FileKindText, VirtualPath: "/kept"}},
		"file_removed": {meta: FileMeta{FileID: "file_removed", Kind: FileKindText, VirtualPath: "/removed"}},
	}
	if err := service.persistSnapshot(nextFiles, map[string]FileID{}, prevFiles, map[string]FileID{}, nil); err != nil {
		t.Fatalf("persistSnapshot should skip removed cached file ids, got %v", err)
	}
}

func referenceBindingsForPath(pathIndex map[string]FileID, virtualPath string) map[FileKind]FileID {
	cleaned := normalizeVirtualPath(virtualPath)
	out := make(map[FileKind]FileID)
	for key, fileID := range pathIndex {
		pathPart, kind, ok := splitPathKindKeyReference(key)
		if !ok || pathPart != cleaned {
			continue
		}
		out[kind] = fileID
	}
	return out
}

func referenceHasAnyBinding(pathIndex map[string]FileID, virtualPath string) bool {
	for range referenceBindingsForPath(pathIndex, virtualPath) {
		return true
	}
	return false
}

func referenceCountNonDirectoryBindings(pathIndex map[string]FileID, virtualPath string) int {
	count := 0
	for kind := range referenceBindingsForPath(pathIndex, virtualPath) {
		if kind != FileKindDirectory {
			count++
		}
	}
	return count
}

func referenceLogicalBindingsEqual(left map[string]FileID, right map[string]FileID, virtualPath string) bool {
	leftBindings := referenceBindingsForPath(left, virtualPath)
	rightBindings := referenceBindingsForPath(right, virtualPath)
	if len(leftBindings) != len(rightBindings) {
		return false
	}
	for kind, leftID := range leftBindings {
		if rightBindings[kind] != leftID {
			return false
		}
	}
	return true
}

func splitPathKindKeyReference(key string) (string, FileKind, bool) {
	idx := strings.LastIndex(key, pathKindSeparator)
	if idx <= 0 || idx == len(key)-1 {
		return "", "", false
	}
	return key[:idx], FileKind(key[idx+len(pathKindSeparator):]), true
}

type testPersistence struct {
	mountDir    string
	stateDir    string
	stateDirRel string
}

func newTestPersistence(t *testing.T, mountDir string) *testPersistence {
	t.Helper()
	stateDir := filepath.Join(mountDir, ".sati_vfs")
	if err := os.MkdirAll(filepath.Join(stateDir, "meta"), 0o755); err != nil {
		t.Fatalf("mkdir meta: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(stateDir, "versions"), 0o755); err != nil {
		t.Fatalf("mkdir versions: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(stateDir, "objects"), 0o755); err != nil {
		t.Fatalf("mkdir objects: %v", err)
	}
	return &testPersistence{
		mountDir:    mountDir,
		stateDir:    stateDir,
		stateDirRel: ".sati_vfs",
	}
}

func (p *testPersistence) EnsureStateLayout() error {
	for _, dir := range []string{
		p.stateDir,
		filepath.Join(p.stateDir, "meta"),
		filepath.Join(p.stateDir, "versions"),
		filepath.Join(p.stateDir, "objects"),
	} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return err
		}
	}
	return nil
}

func (p *testPersistence) LoadRuntimeData() ([]byte, error) {
	return os.ReadFile(filepath.Join(p.stateDir, "runtime.json"))
}

func (p *testPersistence) LoadVersionData(fileID FileID) ([]byte, error) {
	return os.ReadFile(filepath.Join(p.stateDir, "versions", string(fileID)+".json"))
}

func (p *testPersistence) LoadObject(fileID FileID, version VersionEntry, kind FileKind) ([]byte, error) {
	objectPath := objectPathForID(p.stateDir, fileID, version.Generation, version.ObjectID)
	data, err := os.ReadFile(objectPath)
	if err != nil {
		if os.IsNotExist(err) {
			data, err = os.ReadFile(legacyObjectPath(p.stateDir, fileID, version.Generation))
		}
		if os.IsNotExist(err) && kind == FileKindDirectory {
			return nil, err
		}
		return data, err
	}
	return data, nil
}

func (p *testPersistence) SaveMeta(fileID FileID, data []byte) error {
	return os.WriteFile(filepath.Join(p.stateDir, "meta", string(fileID)+".json"), data, 0o644)
}

func (p *testPersistence) SaveVersions(fileID FileID, data []byte) error {
	return os.WriteFile(filepath.Join(p.stateDir, "versions", string(fileID)+".json"), data, 0o644)
}

func (p *testPersistence) SaveObject(fileID FileID, generation Generation, objectID string, payload []byte) error {
	if err := os.MkdirAll(filepath.Join(p.stateDir, "objects", string(fileID)), 0o755); err != nil {
		return err
	}
	return os.WriteFile(objectPathForID(p.stateDir, fileID, generation, objectID), payload, 0o644)
}

func (p *testPersistence) SaveRuntimeData(data []byte) error {
	return os.WriteFile(filepath.Join(p.stateDir, "runtime.json"), data, 0o644)
}

func (p *testPersistence) MaterializedPath(virtualPath string, kind FileKind, summary PathBindingSummary) (string, error) {
	cleaned := normalizeVirtualPath(virtualPath)
	abs := filepath.Join(p.mountDir, strings.TrimPrefix(cleaned, "/"))
	if kind == FileKindDirectory {
		return abs, nil
	}
	if !summary.HasDirectory(cleaned) && summary.NonDirectoryCount(cleaned) == 1 {
		return abs, nil
	}
	return filepath.Join(p.mountDir, ".sati_shadow", strings.TrimPrefix(cleaned, "/")) + shadowFileSuffix(kind), nil
}

func (p *testPersistence) CheckMaterializedPath(path string) error { return nil }

func (p *testPersistence) RemoveMaterialized(path string) error { return os.Remove(path) }

func (p *testPersistence) EnsureMaterializedDir(path string) error { return os.MkdirAll(path, 0o755) }

func (p *testPersistence) WriteMaterialized(path string, payload []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, payload, 0o644)
}

func (p *testPersistence) ImportHostPath(virtualPath string) (fs.FileInfo, []byte, error) {
	abs := filepath.Join(p.mountDir, strings.TrimPrefix(normalizeVirtualPath(virtualPath), "/"))
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

func (p *testPersistence) ListDirHostEntries(virtualPath string) ([]DirEntry, error) {
	abs := filepath.Join(p.mountDir, strings.TrimPrefix(normalizeVirtualPath(virtualPath), "/"))
	hostEntries, err := os.ReadDir(abs)
	if err != nil {
		return nil, err
	}
	entries := make([]DirEntry, 0, len(hostEntries))
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
		kind := FileKindText
		if entry.IsDir() {
			kind = FileKindDirectory
		}
		entries = append(entries, DirEntry{Name: entry.Name(), VirtualPath: path, Kind: kind})
	}
	return entries, nil
}

func (p *testPersistence) StatVirtualPath(virtualPath string) (fs.FileInfo, error) {
	return os.Stat(filepath.Join(p.mountDir, strings.TrimPrefix(normalizeVirtualPath(virtualPath), "/")))
}

func (p *testPersistence) GlobVirtualPaths(pattern string) ([]string, error) {
	matches, err := filepath.Glob(filepath.Join(p.mountDir, strings.TrimPrefix(pattern, "/")))
	if err != nil {
		return nil, err
	}
	out := make([]string, 0, len(matches))
	for _, absPath := range matches {
		rel, err := filepath.Rel(p.mountDir, absPath)
		if err != nil {
			continue
		}
		if rel == ".sati_shadow" || strings.HasPrefix(rel, ".sati_shadow"+string(filepath.Separator)) {
			continue
		}
		if rel == p.stateDirRel || strings.HasPrefix(rel, p.stateDirRel+string(filepath.Separator)) {
			continue
		}
		out = append(out, "/"+filepath.ToSlash(rel))
	}
	sort.Strings(out)
	return out, nil
}

func (p *testPersistence) AcquirePathLocks(paths []string) (func(), error) {
	if len(paths) == 0 {
		return func() {}, nil
	}
	sortedPaths := append([]string(nil), paths...)
	sort.Strings(sortedPaths)
	lockFiles := make([]*os.File, 0, len(sortedPaths))
	lockPaths := make([]string, 0, len(sortedPaths))
	release := func() {
		for i := len(lockFiles) - 1; i >= 0; i-- {
			_ = syscall.Flock(int(lockFiles[i].Fd()), syscall.LOCK_UN)
			_ = lockFiles[i].Close()
			_ = os.Remove(lockPaths[i])
		}
	}
	for _, path := range sortedPaths {
		sum := sha256.Sum256([]byte(path))
		lockPath := filepath.Join(p.stateDir, hex.EncodeToString(sum[:])+".lock")
		file, err := os.OpenFile(lockPath, os.O_CREATE|os.O_RDWR, 0o644)
		if err != nil {
			release()
			return nil, err
		}
		if err := syscall.Flock(int(file.Fd()), syscall.LOCK_EX); err != nil {
			_ = file.Close()
			release()
			return nil, err
		}
		lockFiles = append(lockFiles, file)
		lockPaths = append(lockPaths, lockPath)
	}
	return release, nil
}

func (p *testPersistence) RemoveStateArtifacts(fileID FileID, versions []VersionEntry) error {
	_ = os.Remove(filepath.Join(p.stateDir, "meta", string(fileID)+".json"))
	_ = os.Remove(filepath.Join(p.stateDir, "versions", string(fileID)+".json"))
	for _, version := range versions {
		_ = os.Remove(objectPathForID(p.stateDir, fileID, version.Generation, version.ObjectID))
		_ = os.Remove(legacyObjectPath(p.stateDir, fileID, version.Generation))
	}
	_ = os.RemoveAll(filepath.Join(p.stateDir, "objects", string(fileID)))
	return nil
}
