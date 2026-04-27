package vfs

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

// DiskService persists committed VFS state through an injected persistence layer.
type DiskService struct {
	*MemoryService
	gc                   GCConfig
	store                Persistence
	sortedFileIDs        []FileID
	runtimeSnapshotData  []byte
	runtimeSnapshotDirty bool
}

func NewPersistentDiskService(opts DiskOptions, store Persistence) (*DiskService, error) {
	if store == nil {
		return nil, ErrInvalidInput
	}
	opts.GC = normalizeGCConfig(opts.GC)

	svc := &DiskService{
		MemoryService:        NewMemoryService(),
		gc:                   opts.GC,
		store:                store,
		runtimeSnapshotDirty: true,
	}
	if err := svc.loadFromDisk(); err != nil {
		return nil, err
	}
	return svc, nil
}

// Resolve only reads the committed VFS namespace.
func (s *DiskService) Resolve(ctx context.Context, input ResolveInput) (FileRef, error) {
	return s.MemoryService.Resolve(ctx, input)
}

// CommitChunkTxn persists the committed snapshot to disk before exposing it as
// the new committed in-memory state.
func (s *DiskService) CommitChunkTxn(_ context.Context, txn Txn) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	state, ok := s.txns[txn.ID]
	if !ok {
		return ErrTxnRequired
	}
	if state.txn.ChunkID != txn.ChunkID {
		return ErrTxnMismatch
	}

	touchedPaths := collectTouchedPaths(state)
	unlock, err := s.acquirePathLocks(touchedPaths)
	if err != nil {
		return err
	}
	defer unlock()

	localSeq := s.seq
	if err := s.loadFromDisk(); err != nil {
		return err
	}
	if s.seq < localSeq {
		s.seq = localSeq
	}
	if state.baseRev != s.rev {
		if !canRebaseTxnState(state, s.files, s.pathIndex) {
			return ErrTxnMismatch
		}
		if hasFileIDCollision(state, s.files) {
			return ErrTxnMismatch
		}
	}
	if s.seq < maxTxnSeq(state) {
		s.seq = maxTxnSeq(state)
	}
	prevFiles := shallowCloneFilesMap(s.files)
	prevPathIndex := clonePathIndex(s.pathIndex)
	nextFiles := shallowCloneFilesMap(s.files)
	nextPathIndex := clonePathIndex(s.pathIndex)
	for path, deleted := range state.deletedPaths {
		if deleted {
			delete(nextPathIndex, path)
		}
	}
	for path, fileID := range state.pathIndex {
		nextPathIndex[path] = fileID
	}
	for fileID, file := range state.files {
		nextFiles[fileID] = file
	}
	nextEvents := appendCommittedEvents(s.events, state.pendingEvts, s.gc.MaxEvents)
	nextFiles, nextEvents, prunedIDs := s.applyRetentionPolicy(nextFiles, nextPathIndex, nextEvents)

	if err := s.persistSnapshot(nextFiles, nextPathIndex, prevFiles, prevPathIndex, touchedPaths); err != nil {
		return err
	}
	if err := s.removeStateArtifacts(prunedIDs); err != nil {
		return err
	}

	s.files = nextFiles
	s.pathIndex = nextPathIndex
	s.events = nextEvents
	s.rev++
	s.sortedFileIDs = applySortedFileIDChanges(s.sortedFileIDs, prevFiles, state.files, prunedIDs)
	s.runtimeSnapshotDirty = true
	delete(s.txns, txn.ID)
	return s.persistRuntimeState()
}

func (s *DiskService) loadFromDisk() error {
	snapshot, err := s.loadRuntimeSnapshot()
	if err != nil || snapshot == nil {
		return err
	}

	if snapshot.Rev == s.rev {
		if snapshot.Seq > s.seq {
			s.seq = snapshot.Seq
		}
		return nil
	}

	files, err := s.loadCommittedFiles(snapshot)
	if err != nil {
		return err
	}
	pathIndex, err := buildCommittedPathIndex(snapshot, files)
	if err != nil {
		return err
	}

	s.seq = snapshot.Seq
	s.rev = snapshot.Rev
	s.events = append([]Event(nil), snapshot.Events...)
	s.files = files
	s.pathIndex = pathIndex
	s.sortedFileIDs = collectSortedFileIDs(files)
	s.runtimeSnapshotData = nil
	s.runtimeSnapshotDirty = false
	return nil
}

func (s *DiskService) loadRuntimeSnapshot() (*diskRuntimeSnapshot, error) {
	data, err := s.store.LoadRuntimeData()
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}

	var snapshot diskRuntimeSnapshot
	if err := json.Unmarshal(data, &snapshot); err != nil {
		return nil, err
	}
	return &snapshot, nil
}

func (s *DiskService) loadCommittedFiles(snapshot *diskRuntimeSnapshot) (map[FileID]*memoryFile, error) {
	files := make(map[FileID]*memoryFile, len(snapshot.Files))
	for _, meta := range snapshot.Files {
		if current := s.files[meta.FileID]; current != nil && fileMetaEqual(current.meta, meta) {
			files[meta.FileID] = current
			continue
		}

		file, err := s.loadCommittedFile(meta)
		if err != nil {
			return nil, err
		}
		files[meta.FileID] = file
	}
	return files, nil
}

func (s *DiskService) loadCommittedFile(meta FileMeta) (*memoryFile, error) {
	file := &memoryFile{
		meta:       meta,
		contentByG: make(map[Generation]memoryContent),
	}

	versionBytes, err := s.store.LoadVersionData(meta.FileID)
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal(versionBytes, &file.versions); err != nil {
		return nil, err
	}

	for _, version := range file.versions {
		content, err := s.loadVersionContent(meta.Kind, meta.FileID, version)
		if err != nil {
			return nil, err
		}
		file.contentByG[version.Generation] = content
	}

	return file, nil
}

func buildCommittedPathIndex(snapshot *diskRuntimeSnapshot, files map[FileID]*memoryFile) (map[string]FileID, error) {
	pathIndex := make(map[string]FileID, len(snapshot.PathIndex))
	for key, id := range snapshot.PathIndex {
		if _, _, ok := splitPathKindKey(key); ok {
			pathIndex[key] = id
			continue
		}
		file := files[id]
		if file == nil {
			return nil, ErrFileNotFound
		}
		pathIndex[pathKindKey(key, file.meta.Kind)] = id
	}
	return pathIndex, nil
}

func (s *DiskService) persistSnapshot(nextFiles map[FileID]*memoryFile, nextPathIndex map[string]FileID, prevFiles map[FileID]*memoryFile, prevPathIndex map[string]FileID, touchedPaths []string) error {
	if err := s.store.EnsureStateLayout(); err != nil {
		return err
	}

	if err := s.syncMountedFiles(nextFiles, nextPathIndex, prevFiles, prevPathIndex, touchedPaths); err != nil {
		return err
	}

	for _, id := range applySortedFileIDChanges(s.sortedFileIDs, prevFiles, nextFiles, nil) {
		file := nextFiles[id]
		if file == nil {
			continue
		}
		prevFile := prevFiles[id]

		if prevFile == nil || !fileMetaEqual(prevFile.meta, file.meta) {
			metaBytes, err := json.Marshal(file.meta)
			if err != nil {
				return err
			}
			if err := s.store.SaveMeta(id, metaBytes); err != nil {
				return err
			}
		}

		if prevFile == nil || !versionEntriesEqual(prevFile.versions, file.versions) {
			versionBytes, err := json.Marshal(file.versions)
			if err != nil {
				return err
			}
			if err := s.store.SaveVersions(id, versionBytes); err != nil {
				return err
			}
		}

		for generation, content := range file.contentByG {
			if prevFile != nil {
				if _, exists := prevFile.contentByG[generation]; exists {
					continue
				}
			}
			var payload []byte
			switch file.meta.Kind {
			case FileKindBinary:
				payload = cloneBytes(content.blob)
			case FileKindDirectory:
				payload = nil
			default:
				payload = []byte(content.text)
			}
			objectID := objectIDForVersion(file.meta.FileID, generation)
			if err := s.store.SaveObject(file.meta.FileID, generation, objectID, payload); err != nil {
				return err
			}
		}
	}

	return nil
}

func (s *DiskService) persistRuntimeState() error {
	if !s.runtimeSnapshotDirty && s.runtimeSnapshotData != nil {
		return s.store.SaveRuntimeData(s.runtimeSnapshotData)
	}
	files := make([]FileMeta, 0, len(s.sortedFileIDs))
	for _, id := range s.sortedFileIDs {
		if file := s.files[id]; file != nil {
			files = append(files, file.meta)
		}
	}

	snapshot := diskRuntimeSnapshot{
		Seq:       s.seq,
		Rev:       s.rev,
		PathIndex: s.pathIndex,
		Files:     files,
		Events:    s.events,
	}

	data, err := json.Marshal(snapshot)
	if err != nil {
		return err
	}
	s.runtimeSnapshotData = data
	s.runtimeSnapshotDirty = false
	return s.store.SaveRuntimeData(data)
}

func (s *DiskService) syncMountedFiles(nextFiles map[FileID]*memoryFile, nextPathIndex map[string]FileID, prevFiles map[FileID]*memoryFile, prevPathIndex map[string]FileID, touchedPaths []string) error {
	prevSummary := buildPathBindingSummaryForPaths(prevPathIndex, touchedPaths)
	nextSummary := buildPathBindingSummaryForPaths(nextPathIndex, touchedPaths)
	prevMaterialized := make(map[string]FileKind)
	for _, virtualPath := range touchedPaths {
		for kind, fileID := range bindingsForPath(prevPathIndex, virtualPath) {
			file := prevFiles[fileID]
			if file == nil || file.meta.DeleteState == DeleteStateDeleted {
				continue
			}
			abs, err := s.store.MaterializedPath(virtualPath, kind, prevSummary)
			if err != nil {
				return err
			}
			prevMaterialized[abs] = kind
		}
	}

	nextMaterialized := make(map[string]FileKind)
	nextKeys := make([]string, 0, len(touchedPaths)*len(indexedFileKinds))
	for _, virtualPath := range touchedPaths {
		for kind, fileID := range bindingsForPath(nextPathIndex, virtualPath) {
			file := nextFiles[fileID]
			if file == nil {
				return ErrFileNotFound
			}
			key := pathKindKey(virtualPath, kind)
			abs, err := s.store.MaterializedPath(virtualPath, kind, nextSummary)
			if err != nil {
				return err
			}
			nextMaterialized[abs] = kind
			nextKeys = append(nextKeys, key)
		}
	}

	removedPaths := make([]string, 0, len(prevMaterialized))
	for abs, prevKind := range prevMaterialized {
		nextKind, stillActive := nextMaterialized[abs]
		if !stillActive || nextKind != prevKind {
			removedPaths = append(removedPaths, abs)
		}
	}
	sort.Slice(removedPaths, func(i, j int) bool {
		depthI := strings.Count(removedPaths[i], string(filepath.Separator))
		depthJ := strings.Count(removedPaths[j], string(filepath.Separator))
		if depthI != depthJ {
			return depthI > depthJ
		}
		return removedPaths[i] > removedPaths[j]
	})
	for _, abs := range removedPaths {
		if err := s.store.CheckMaterializedPath(abs); err != nil {
			return err
		}
		if err := s.store.RemoveMaterialized(abs); err != nil && !os.IsNotExist(err) {
			return err
		}
	}

	sort.Strings(nextKeys)
	for _, key := range nextKeys {
		virtualPath, kind, ok := splitPathKindKey(key)
		if !ok {
			continue
		}
		fileID := nextPathIndex[key]
		file := nextFiles[fileID]
		if file == nil {
			return ErrFileNotFound
		}

		prevID, hadPrevPath := prevPathIndex[key]
		prevFile := prevFiles[prevID]
		abs, err := s.store.MaterializedPath(virtualPath, kind, nextSummary)
		if err != nil {
			return err
		}
		prevAbs := ""
		if hadPrevPath {
			prevAbs, _ = s.store.MaterializedPath(virtualPath, kind, prevSummary)
		}
		needsWrite := !hadPrevPath || prevID != fileID || materializedFileChanged(prevFile, file) || prevAbs != abs
		if !needsWrite {
			continue
		}

		// P0: reject any write whose path chain contains a symlink.
		if err := s.store.CheckMaterializedPath(abs); err != nil {
			return err
		}
		switch kind {
		case FileKindDirectory:
			if err := s.store.EnsureMaterializedDir(abs); err != nil {
				return err
			}
		case FileKindBinary:
			payload := file.contentByG[file.meta.CurrentGeneration].blob
			if err := s.store.WriteMaterialized(abs, payload); err != nil {
				return err
			}
		default:
			payload := []byte(file.contentByG[file.meta.CurrentGeneration].text)
			if err := s.store.WriteMaterialized(abs, payload); err != nil {
				return err
			}
		}
	}

	return nil
}

func (s *DiskService) loadVersionContent(kind FileKind, fileID FileID, version VersionEntry) (memoryContent, error) {
	data, err := s.store.LoadObject(fileID, version, kind)
	if err != nil {
		if os.IsNotExist(err) && kind == FileKindDirectory {
			return memoryContent{}, nil
		}
		return memoryContent{}, err
	}

	switch kind {
	case FileKindBinary:
		return memoryContent{blob: data}, nil
	case FileKindDirectory:
		return memoryContent{}, nil
	default:
		return memoryContent{text: string(data)}, nil
	}
}

func (s *DiskService) importHostPath(virtualPath string) (FileRef, error) {
	info, data, err := s.store.ImportHostPath(virtualPath)
	if err != nil {
		if os.IsNotExist(err) {
			return FileRef{}, ErrFileNotFound
		}
		return FileRef{}, err
	}

	now := timeNowUTC()
	s.mu.Lock()
	defer s.mu.Unlock()

	prevFiles := shallowCloneFilesMap(s.files)
	prevPathIndex := clonePathIndex(s.pathIndex)

	cleaned := normalizeVirtualPath(virtualPath)
	for kind, fileID := range bindingsForPath(s.pathIndex, cleaned) {
		if info.IsDir() && kind != FileKindDirectory {
			continue
		}
		if !info.IsDir() && kind == FileKindDirectory {
			continue
		}
		file := s.files[fileID]
		if file == nil {
			return FileRef{}, ErrFileNotFound
		}
		return makeFileRef(file.meta, now), nil
	}

	fileID := FileID(s.nextID("file"))
	meta := FileMeta{
		FileID:            fileID,
		VirtualPath:       cleaned,
		CurrentGeneration: 1,
		DeleteState:       DeleteStateActive,
		CreatedAt:         now,
		UpdatedAt:         now,
		SyncStatus:        SyncStatusClean,
		CreatorChunkID:    ChunkID("HOST_IMPORT"),
		LastWriterChunkID: ChunkID("HOST_IMPORT"),
	}

	file := &memoryFile{
		meta:       meta,
		contentByG: make(map[Generation]memoryContent),
	}

	if info.IsDir() {
		file.meta.Kind = FileKindDirectory
		file.meta.Size = 0
		file.meta.CurrentChecksum = ""
	} else {
		file.meta.Kind = inferFileKind(data)
		file.meta.Size = int64(len(data))
		file.meta.CurrentChecksum = checksumBytes(data)
		if file.meta.Kind == FileKindBinary {
			file.contentByG[1] = memoryContent{blob: cloneBytes(data)}
		} else {
			file.contentByG[1] = memoryContent{text: string(data)}
		}
		file.versions = append(file.versions, VersionEntry{
			FileID:        fileID,
			Generation:    1,
			ObjectID:      objectIDForVersion(fileID, 1),
			ContentRef:    contentRef(fileID, 1),
			WriteMode:     WriteModeReplaceFull,
			WriterChunkID: ChunkID("HOST_IMPORT"),
			Checksum:      file.meta.CurrentChecksum,
			Size:          file.meta.Size,
			CreatedAt:     now,
		})
	}

	event := Event{
		EventID:   EventID(s.nextID("evt")),
		FileID:    fileID,
		Type:      EventTypeFileCreated,
		ChunkID:   ChunkID("HOST_IMPORT"),
		Timestamp: now,
		Payload: map[string]any{
			"virtual_path": cleaned,
			"imported":     true,
		},
	}
	file.meta.LastEventID = event.EventID

	s.files[fileID] = file
	setPathBinding(s.pathIndex, cleaned, file.meta.Kind, fileID)
	s.events = append(s.events, event)
	s.events = s.trimEvents(s.events)
	for _, txn := range s.txns {
		txn.files[fileID] = cloneFile(file)
		setPathBinding(txn.pathIndex, cleaned, file.meta.Kind, fileID)
	}

	touchedPaths := []string{cleaned}
	if err := s.persistSnapshot(s.files, s.pathIndex, prevFiles, prevPathIndex, touchedPaths); err != nil {
		return FileRef{}, err
	}
	s.sortedFileIDs = applySortedFileIDChanges(s.sortedFileIDs, prevFiles, map[FileID]*memoryFile{fileID: file}, nil)
	s.runtimeSnapshotDirty = true
	if err := s.persistRuntimeState(); err != nil {
		return FileRef{}, err
	}

	return makeFileRef(file.meta, now), nil
}

func shadowFileSuffix(kind FileKind) string {
	return ".__satis_" + string(kind) + "__"
}

func (s *DiskService) ListDir(_ context.Context, txn Txn, virtualPath string) ([]DirEntry, error) {
	cleaned := normalizeVirtualPath(virtualPath)
	state, err := s.requireTxn(txn)
	if err != nil {
		return nil, err
	}

	entriesByBinding := make(map[string]DirEntry)
	memoryEntries, memErr := state.listDir(cleaned)
	switch memErr {
	case nil:
		for _, entry := range memoryEntries {
			entriesByBinding[pathKindKey(entry.VirtualPath, entry.Kind)] = entry
		}
	case ErrFileNotFound, ErrInvalidKind:
		// Fall through to host inspection for lazily imported paths.
	default:
		return nil, memErr
	}

	hostEntries, hostErr := s.store.ListDirHostEntries(cleaned)
	switch {
	case hostErr == nil:
		for _, entry := range hostEntries {
			entriesByBinding[pathKindKey(entry.VirtualPath, entry.Kind)] = entry
		}
	case os.IsNotExist(hostErr):
		// Keep memory-derived result if present.
	default:
		return nil, hostErr
	}

	if len(entriesByBinding) > 0 {
		keys := make([]string, 0, len(entriesByBinding))
		for key := range entriesByBinding {
			keys = append(keys, key)
		}
		sort.Slice(keys, func(i, j int) bool {
			left := entriesByBinding[keys[i]]
			right := entriesByBinding[keys[j]]
			if left.Name != right.Name {
				return left.Name < right.Name
			}
			return left.Kind < right.Kind
		})
		entries := make([]DirEntry, 0, len(keys))
		for _, key := range keys {
			entries = append(entries, entriesByBinding[key])
		}
		return entries, nil
	}

	if memErr == ErrInvalidKind {
		return nil, ErrInvalidKind
	}
	if hostErr != nil && !os.IsNotExist(hostErr) {
		return nil, hostErr
	}

	info, statErr := s.store.StatVirtualPath(cleaned)
	if statErr == nil && !info.IsDir() {
		return nil, ErrInvalidKind
	}
	if statErr == nil && info.IsDir() {
		return []DirEntry{}, nil
	}
	if memErr != nil {
		return nil, memErr
	}
	return nil, ErrFileNotFound
}

// Glob expands a virtual-path glob pattern into a list of matching virtual paths.
// It converts the virtual pattern to a mount-dir-relative path, runs filepath.Glob,
// then converts the results back to virtual paths.
func (s *DiskService) Glob(ctx context.Context, pattern string) ([]string, error) {
	cleaned := normalizeVirtualPath(pattern)
	virtualSet := make(map[string]struct{})

	s.mu.Lock()
	for key := range s.pathIndex {
		virtualPath, _, ok := splitPathKindKey(key)
		if ok && logicalPathMatchesPattern(cleaned, virtualPath) {
			virtualSet[virtualPath] = struct{}{}
		}
	}
	s.mu.Unlock()

	matches, err := s.store.GlobVirtualPaths(cleaned)
	if err != nil {
		return nil, err
	}
	for _, vp := range matches {
		virtualSet[vp] = struct{}{}
	}
	virtualPaths := make([]string, 0, len(virtualSet))
	for virtualPath := range virtualSet {
		virtualPaths = append(virtualPaths, virtualPath)
	}
	sort.Strings(virtualPaths)
	return virtualPaths, nil
}

func (s *DiskService) acquirePathLocks(paths []string) (func(), error) {
	return s.store.AcquirePathLocks(paths)
}

func collectTouchedPaths(state *memoryTxnState) []string {
	paths := make(map[string]struct{})
	for key, deleted := range state.deletedPaths {
		if deleted {
			if path, _, ok := splitPathKindKey(key); ok {
				paths[path] = struct{}{}
			}
		}
	}
	for key := range state.pathIndex {
		if path, _, ok := splitPathKindKey(key); ok {
			paths[path] = struct{}{}
		}
	}
	for fileID, file := range state.files {
		base := state.baseFiles[fileID]
		if base == nil || materializedFileChanged(base, file) || base.meta.VirtualPath != file.meta.VirtualPath || base.meta.DeleteState != file.meta.DeleteState {
			paths[file.meta.VirtualPath] = struct{}{}
			if base != nil {
				paths[base.meta.VirtualPath] = struct{}{}
			}
		}
	}

	out := make([]string, 0, len(paths))
	for path := range paths {
		out = append(out, path)
	}
	return out
}

func canRebaseTxnState(state *memoryTxnState, currentFiles map[FileID]*memoryFile, currentPathIndex map[string]FileID) bool {
	for _, path := range collectTouchedPaths(state) {
		if !logicalBindingsEqual(state.basePathIndex, currentPathIndex, path) {
			return false
		}
	}

	for fileID, txnFile := range state.files {
		baseFile := state.baseFiles[fileID]
		if baseFile == nil {
			continue
		}
		currentFile := currentFiles[fileID]
		if currentFile == nil {
			return false
		}
		if baseFile.meta.CurrentGeneration != currentFile.meta.CurrentGeneration ||
			baseFile.meta.CurrentChecksum != currentFile.meta.CurrentChecksum ||
			baseFile.meta.DeleteState != currentFile.meta.DeleteState ||
			baseFile.meta.VirtualPath != currentFile.meta.VirtualPath {
			return false
		}
		if txnFile.meta.VirtualPath != currentFile.meta.VirtualPath && txnFile.meta.VirtualPath != baseFile.meta.VirtualPath {
			return false
		}
	}
	return true
}

func hasFileIDCollision(state *memoryTxnState, currentFiles map[FileID]*memoryFile) bool {
	for fileID := range state.files {
		if state.baseFiles[fileID] != nil {
			continue
		}
		if currentFiles[fileID] != nil {
			return true
		}
	}
	return false
}

func maxTxnSeq(state *memoryTxnState) uint64 {
	var max uint64
	update := func(raw string) {
		idx := strings.LastIndex(raw, "_")
		if idx < 0 || idx == len(raw)-1 {
			return
		}
		n, err := strconv.ParseUint(raw[idx+1:], 10, 64)
		if err == nil && n > max {
			max = n
		}
	}

	update(string(state.txn.ID))
	for fileID := range state.files {
		update(string(fileID))
	}
	for _, evt := range state.pendingEvts {
		update(string(evt.EventID))
	}
	return max
}

func normalizeGCConfig(cfg GCConfig) GCConfig {
	if cfg.MaxEvents == 0 {
		cfg.MaxEvents = 2048
	}
	if cfg.DeletedFileRetention < -1 {
		cfg.DeletedFileRetention = -1
	}
	if cfg.MaxEvents < -1 {
		cfg.MaxEvents = -1
	}
	return cfg
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

func inferFileKind(data []byte) FileKind {
	for _, b := range data {
		if b == 0 {
			return FileKindBinary
		}
	}
	return FileKindText
}

func timeNowUTC() time.Time {
	return time.Now().UTC()
}

func materializedFileChanged(prev *memoryFile, next *memoryFile) bool {
	if prev == nil || next == nil {
		return true
	}
	return prev.meta.Kind != next.meta.Kind ||
		prev.meta.CurrentGeneration != next.meta.CurrentGeneration ||
		prev.meta.CurrentChecksum != next.meta.CurrentChecksum ||
		prev.meta.DeleteState != next.meta.DeleteState
}

func (s *DiskService) applyRetentionPolicy(nextFiles map[FileID]*memoryFile, nextPathIndex map[string]FileID, events []Event) (map[FileID]*memoryFile, []Event, []FileID) {
	prunedIDs := s.collectPrunableFileIDs(nextFiles, nextPathIndex)
	for _, fileID := range prunedIDs {
		delete(nextFiles, fileID)
	}
	return nextFiles, s.trimEvents(events), prunedIDs
}

func (s *DiskService) collectPrunableFileIDs(files map[FileID]*memoryFile, pathIndex map[string]FileID) []FileID {
	if s.gc.DeletedFileRetention < 0 {
		return nil
	}

	activeIDs := make(map[FileID]bool, len(pathIndex))
	for _, fileID := range pathIndex {
		activeIDs[fileID] = true
	}

	var deleted []FileID
	for fileID, file := range files {
		if file == nil {
			continue
		}
		if file.meta.DeleteState != DeleteStateDeleted || activeIDs[fileID] {
			continue
		}
		deleted = append(deleted, fileID)
	}

	sort.Slice(deleted, func(i, j int) bool {
		left := files[deleted[i]]
		right := files[deleted[j]]
		if left.meta.UpdatedAt.Equal(right.meta.UpdatedAt) {
			return deleted[i] > deleted[j]
		}
		return left.meta.UpdatedAt.After(right.meta.UpdatedAt)
	})

	keep := s.gc.DeletedFileRetention
	if keep >= len(deleted) {
		return nil
	}
	return append([]FileID(nil), deleted[keep:]...)
}

func (s *DiskService) trimEvents(events []Event) []Event {
	if s.gc.MaxEvents < 0 || len(events) <= s.gc.MaxEvents {
		return events
	}
	return append([]Event(nil), events[len(events)-s.gc.MaxEvents:]...)
}

func (s *DiskService) removeStateArtifacts(fileIDs []FileID) error {
	for _, fileID := range fileIDs {
		var versions []VersionEntry
		if file, ok := s.files[fileID]; ok && file != nil {
			versions = append(versions, file.versions...)
		}
		if err := s.store.RemoveStateArtifacts(fileID, versions); err != nil && !os.IsNotExist(err) {
			return err
		}
	}
	return nil
}

func objectPathForID(stateDir string, fileID FileID, generation Generation, objectID string) string {
	if objectID == "" {
		return legacyObjectPath(stateDir, fileID, generation)
	}
	return filepath.Join(stateDir, "objects", objectID+".bin")
}

func legacyObjectPath(stateDir string, fileID FileID, generation Generation) string {
	return filepath.Join(stateDir, "objects", string(fileID), fmt.Sprintf("%d.bin", generation))
}

func shallowCloneFilesMap(src map[FileID]*memoryFile) map[FileID]*memoryFile {
	dst := make(map[FileID]*memoryFile, len(src))
	for k, v := range src {
		dst[k] = v
	}
	return dst
}

func appendCommittedEvents(existing []Event, pending []Event, maxEvents int) []Event {
	if len(pending) == 0 {
		if maxEvents >= 0 && len(existing) > maxEvents {
			return append([]Event(nil), existing[len(existing)-maxEvents:]...)
		}
		return existing
	}
	events := make([]Event, len(existing)+len(pending))
	copy(events, existing)
	copy(events[len(existing):], pending)
	if maxEvents >= 0 && len(events) > maxEvents {
		return append([]Event(nil), events[len(events)-maxEvents:]...)
	}
	return events
}

func collectSortedFileIDs(files map[FileID]*memoryFile) []FileID {
	out := make([]FileID, 0, len(files))
	for id := range files {
		out = append(out, id)
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i] < out[j]
	})
	return out
}

func applySortedFileIDChanges(existing []FileID, prevFiles map[FileID]*memoryFile, changed map[FileID]*memoryFile, removed []FileID) []FileID {
	if len(existing) == 0 {
		base := prevFiles
		if len(changed) > len(base) {
			base = changed
		}
		out := collectSortedFileIDs(base)
		if len(removed) == 0 {
			return out
		}
		existing = out
	}
	seen := make(map[FileID]struct{}, len(existing)+len(changed))
	out := make([]FileID, 0, len(existing)+len(changed))
	removedSet := make(map[FileID]struct{}, len(removed))
	for _, id := range removed {
		removedSet[id] = struct{}{}
	}
	for _, id := range existing {
		if _, drop := removedSet[id]; drop {
			continue
		}
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		out = append(out, id)
	}
	for id := range changed {
		if _, ok := prevFiles[id]; ok {
			continue
		}
		if _, drop := removedSet[id]; drop {
			continue
		}
		idx := sort.Search(len(out), func(i int) bool { return out[i] >= id })
		if idx < len(out) && out[idx] == id {
			continue
		}
		out = append(out, "")
		copy(out[idx+1:], out[idx:])
		out[idx] = id
	}
	return out
}

func buildPathBindingSummaryForPaths(pathIndex map[string]FileID, paths []string) PathBindingSummary {
	if len(paths) == 0 {
		return BuildPathBindingSummary(pathIndex)
	}
	allowed := make(map[string]struct{}, len(paths))
	for _, p := range paths {
		allowed[normalizeVirtualPath(p)] = struct{}{}
	}
	summary := PathBindingSummary{
		hasDirectory:  make(map[string]bool, len(paths)),
		nonDirectoryN: make(map[string]int, len(paths)),
	}
	for key := range pathIndex {
		virtualPath, kind, ok := splitPathKindKey(key)
		if !ok {
			continue
		}
		if _, ok := allowed[virtualPath]; !ok {
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

type diskRuntimeSnapshot struct {
	Seq       uint64            `json:"seq"`
	Rev       uint64            `json:"rev"`
	PathIndex map[string]FileID `json:"path_index"`
	Files     []FileMeta        `json:"files"`
	Events    []Event           `json:"events"`
}

func fileMetaEqual(left FileMeta, right FileMeta) bool {
	return left.FileID == right.FileID &&
		left.VirtualPath == right.VirtualPath &&
		left.Kind == right.Kind &&
		left.ContentType == right.ContentType &&
		left.CurrentGeneration == right.CurrentGeneration &&
		left.CurrentChecksum == right.CurrentChecksum &&
		left.Size == right.Size &&
		left.SyncStatus == right.SyncStatus &&
		left.CreatorChunkID == right.CreatorChunkID &&
		left.LastWriterChunkID == right.LastWriterChunkID &&
		lineageEqual(left.ParentLineage, right.ParentLineage) &&
		left.DeleteState == right.DeleteState &&
		left.CreatedAt.Equal(right.CreatedAt) &&
		left.UpdatedAt.Equal(right.UpdatedAt) &&
		left.LastReadAt.Equal(right.LastReadAt) &&
		left.LastEventID == right.LastEventID
}

func lineageEqual(left []LineageRef, right []LineageRef) bool {
	if len(left) != len(right) {
		return false
	}
	for i := range left {
		if left[i] != right[i] {
			return false
		}
	}
	return true
}

func versionEntriesEqual(left []VersionEntry, right []VersionEntry) bool {
	if len(left) != len(right) {
		return false
	}
	for i := range left {
		if !versionEntryEqual(left[i], right[i]) {
			return false
		}
	}
	return true
}

func versionEntryEqual(left VersionEntry, right VersionEntry) bool {
	return left.FileID == right.FileID &&
		left.Generation == right.Generation &&
		left.ObjectID == right.ObjectID &&
		left.ContentRef == right.ContentRef &&
		left.WriteMode == right.WriteMode &&
		left.BaseGeneration == right.BaseGeneration &&
		left.WriterChunkID == right.WriterChunkID &&
		left.Checksum == right.Checksum &&
		left.Size == right.Size &&
		left.CreatedAt.Equal(right.CreatedAt)
}
