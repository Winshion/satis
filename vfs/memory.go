package vfs

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

type memoryContent struct {
	text string
	blob []byte
}

type memoryFile struct {
	meta       FileMeta
	versions   []VersionEntry
	contentByG map[Generation]memoryContent
}

type memoryTxnState struct {
	txn           Txn
	baseRev       uint64
	baseFiles     map[FileID]*memoryFile
	basePathIndex map[string]FileID
	files         map[FileID]*memoryFile
	pathIndex     map[string]FileID
	deletedPaths  map[string]bool
	pendingEvts   []Event
}

// MemoryService is the v0 in-memory implementation of Service.
//
// It favors clarity and semantic correctness over performance. Transactions
// operate on cloned state and commit optimistically against a base revision.
type MemoryService struct {
	mu        sync.Mutex
	seq       uint64
	rev       uint64
	files     map[FileID]*memoryFile
	pathIndex map[string]FileID
	events    []Event
	txns      map[TxnID]*memoryTxnState
}

// NewMemoryService creates a minimal in-memory VFS suitable for early runtime
// development and semantic validation.
func NewMemoryService() *MemoryService {
	return &MemoryService{
		files:     make(map[FileID]*memoryFile),
		pathIndex: make(map[string]FileID),
		txns:      make(map[TxnID]*memoryTxnState),
	}
}

func (s *MemoryService) BeginChunkTxn(_ context.Context, chunkID ChunkID) (Txn, error) {
	if chunkID == "" {
		return Txn{}, ErrInvalidInput
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	txn := Txn{
		ID:        TxnID(s.nextID("txn")),
		ChunkID:   chunkID,
		StartedAt: time.Now().UTC(),
	}

	s.txns[txn.ID] = &memoryTxnState{
		txn:           txn,
		baseRev:       s.rev,
		baseFiles:     s.files,
		basePathIndex: s.pathIndex,
		files:         make(map[FileID]*memoryFile),
		pathIndex:     make(map[string]FileID),
		deletedPaths:  make(map[string]bool),
	}

	return txn, nil
}

func (s *MemoryService) CommitChunkTxn(_ context.Context, txn Txn) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	state, ok := s.txns[txn.ID]
	if !ok {
		return ErrTxnRequired
	}
	if state.txn.ChunkID != txn.ChunkID {
		return ErrTxnMismatch
	}
	if state.baseRev != s.rev {
		return ErrTxnMismatch
	}

	for path, deleted := range state.deletedPaths {
		if deleted {
			delete(s.pathIndex, path)
		}
	}
	for path, fileID := range state.pathIndex {
		s.pathIndex[path] = fileID
	}
	for fileID, file := range state.files {
		s.files[fileID] = file
	}
	s.events = append(s.events, state.pendingEvts...)
	s.rev++
	delete(s.txns, txn.ID)
	return nil
}

func (s *MemoryService) ListDir(_ context.Context, txn Txn, virtualPath string) ([]DirEntry, error) {
	state, err := s.requireTxn(txn)
	if err != nil {
		return nil, err
	}
	return state.listDir(virtualPath)
}

func (s *MemoryService) RollbackChunkTxn(_ context.Context, txn Txn) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if _, ok := s.txns[txn.ID]; !ok {
		return ErrTxnRequired
	}
	delete(s.txns, txn.ID)
	return nil
}

func (s *MemoryService) Create(_ context.Context, txn Txn, input CreateInput) (FileRef, error) {
	state, err := s.requireTxn(txn)
	if err != nil {
		return FileRef{}, err
	}
	if input.VirtualPath == "" || input.Kind == "" || input.CreatorChunkID == "" {
		return FileRef{}, ErrInvalidInput
	}
	if _, exists := state.resolvePath(input.VirtualPath, input.Kind); exists {
		return FileRef{}, ErrPathAlreadyExists
	}

	now := time.Now().UTC()
	fileID := FileID(s.nextID("file"))

	meta := FileMeta{
		FileID:            fileID,
		VirtualPath:       input.VirtualPath,
		Kind:              input.Kind,
		ContentType:       input.ContentType,
		CreatorChunkID:    input.CreatorChunkID,
		LastWriterChunkID: input.CreatorChunkID,
		ParentLineage:     cloneLineage(input.ParentLineage),
		DeleteState:       DeleteStateActive,
		CreatedAt:         now,
		UpdatedAt:         now,
		SyncStatus:        SyncStatusClean,
	}

	file := &memoryFile{
		meta:       meta,
		contentByG: make(map[Generation]memoryContent),
	}

	switch input.Kind {
	case FileKindDirectory:
		// Directories carry namespace meaning but no content snapshot in v0.
	case FileKindText, FileKindGenerated, FileKindEphemeral:
		file.meta.CurrentGeneration = 1
		file.contentByG[1] = memoryContent{text: input.InitialText}
		file.meta.Size = int64(len(input.InitialText))
		file.meta.CurrentChecksum = checksumText(input.InitialText)
		file.versions = append(file.versions, VersionEntry{
			FileID:        fileID,
			Generation:    1,
			ObjectID:      objectIDForVersion(fileID, 1),
			ContentRef:    contentRef(fileID, 1),
			WriteMode:     WriteModeReplaceFull,
			WriterChunkID: input.CreatorChunkID,
			Checksum:      file.meta.CurrentChecksum,
			Size:          file.meta.Size,
			CreatedAt:     now,
		})
	case FileKindBinary:
		file.meta.CurrentGeneration = 1
		file.contentByG[1] = memoryContent{blob: cloneBytes(input.InitialBlob)}
		file.meta.Size = int64(len(input.InitialBlob))
		file.meta.CurrentChecksum = checksumBytes(input.InitialBlob)
		file.versions = append(file.versions, VersionEntry{
			FileID:        fileID,
			Generation:    1,
			ObjectID:      objectIDForVersion(fileID, 1),
			ContentRef:    contentRef(fileID, 1),
			WriteMode:     WriteModeReplaceFull,
			WriterChunkID: input.CreatorChunkID,
			Checksum:      file.meta.CurrentChecksum,
			Size:          file.meta.Size,
			CreatedAt:     now,
		})
	default:
		return FileRef{}, ErrInvalidKind
	}

	event := Event{
		EventID:   EventID(s.nextID("evt")),
		FileID:    fileID,
		Type:      EventTypeFileCreated,
		ChunkID:   input.CreatorChunkID,
		Timestamp: now,
		Payload: map[string]any{
			"virtual_path": input.VirtualPath,
			"kind":         input.Kind,
		},
	}
	file.meta.LastEventID = event.EventID

	state.files[fileID] = file
	setPathBinding(state.pathIndex, input.VirtualPath, input.Kind, fileID)
	delete(state.deletedPaths, pathKindKey(input.VirtualPath, input.Kind))
	state.pendingEvts = append(state.pendingEvts, event)

	return makeFileRef(file.meta, now), nil
}

func (s *MemoryService) Resolve(_ context.Context, input ResolveInput) (FileRef, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	file, now, err := s.resolveCommitted(input, false)
	if err != nil {
		return FileRef{}, err
	}
	return makeFileRef(file.meta, now), nil
}

func (s *MemoryService) Read(_ context.Context, txn Txn, input ReadInput) (ReadResult, error) {
	state, err := s.requireTxn(txn)
	if err != nil {
		return ReadResult{}, err
	}

	file, err := state.resolve(input.Target, true)
	if err != nil {
		return ReadResult{}, err
	}
	file, err = state.mutableFile(file.meta.FileID)
	if err != nil {
		return ReadResult{}, err
	}

	generation := input.Generation
	if generation == 0 {
		generation = file.meta.CurrentGeneration
	}

	content, ok := file.contentByG[generation]
	if !ok && file.meta.Kind != FileKindDirectory {
		return ReadResult{}, ErrFileNotFound
	}

	now := time.Now().UTC()
	file.meta.LastReadAt = now

	event := Event{
		EventID:   EventID(s.nextID("evt")),
		FileID:    file.meta.FileID,
		Type:      EventTypeFileRead,
		ChunkID:   txn.ChunkID,
		Timestamp: now,
		Payload: map[string]any{
			"generation": generation,
		},
	}
	file.meta.LastEventID = event.EventID
	state.pendingEvts = append(state.pendingEvts, event)

	result := ReadResult{
		FileRef:     makeFileRef(file.meta, now),
		Generation:  generation,
		ContentType: file.meta.ContentType,
	}

	switch file.meta.Kind {
	case FileKindBinary:
		result.Blob = cloneBytes(content.blob)
	default:
		result.Text = applyReadView(content.text, input.View)
	}

	return result, nil
}

func (s *MemoryService) Write(_ context.Context, txn Txn, input WriteInput) (FileRef, error) {
	state, err := s.requireTxn(txn)
	if err != nil {
		return FileRef{}, err
	}
	if input.WriterChunkID == "" {
		return FileRef{}, ErrInvalidInput
	}

	file, err := state.resolve(input.Target, false)
	if err != nil {
		return FileRef{}, err
	}
	file, err = state.mutableFile(file.meta.FileID)
	if err != nil {
		return FileRef{}, err
	}

	now := time.Now().UTC()
	nextGen := file.meta.CurrentGeneration + 1
	baseGen := file.meta.CurrentGeneration
	currentContent := file.contentByG[file.meta.CurrentGeneration]
	var (
		nextText string
		nextBlob []byte
		size     int64
		checksum string
	)

	switch input.Mode {
	case WriteModeReplaceFull:
		switch file.meta.Kind {
		case FileKindBinary:
			nextBlob = cloneBytes(input.Blob)
			size = int64(len(nextBlob))
			checksum = checksumBytes(nextBlob)
		case FileKindText, FileKindGenerated, FileKindEphemeral:
			nextText = input.Text
			size = int64(len(nextText))
			checksum = checksumText(nextText)
		default:
			return FileRef{}, ErrInvalidKind
		}
	case WriteModeAppend:
		switch file.meta.Kind {
		case FileKindText, FileKindGenerated, FileKindEphemeral:
			current := file.contentByG[file.meta.CurrentGeneration]
			nextText = current.text + input.Text
			size = int64(len(nextText))
			checksum = checksumText(nextText)
		default:
			return FileRef{}, ErrInvalidKind
		}
	case WriteModePatchText:
		if input.PatchText == nil || input.PatchText.OldText == "" {
			return FileRef{}, ErrInvalidInput
		}
		switch file.meta.Kind {
		case FileKindText, FileKindGenerated, FileKindEphemeral:
			current := file.contentByG[file.meta.CurrentGeneration].text
			matches := strings.Count(current, input.PatchText.OldText)
			switch matches {
			case 0:
				return FileRef{}, ErrPatchNoMatch
			case 1:
				nextText = strings.Replace(current, input.PatchText.OldText, input.PatchText.NewText, 1)
				size = int64(len(nextText))
				checksum = checksumText(nextText)
			default:
				return FileRef{}, ErrPatchMultipleMatch
			}
		default:
			return FileRef{}, ErrInvalidKind
		}
	default:
		return FileRef{}, ErrInvalidWriteMode
	}

	if file.meta.Kind == FileKindBinary {
		if bytesEqual(currentContent.blob, nextBlob) {
			return makeFileRef(file.meta, now), nil
		}
	} else {
		if currentContent.text == nextText {
			return makeFileRef(file.meta, now), nil
		}
	}

	file.meta.CurrentGeneration = nextGen
	file.meta.LastWriterChunkID = input.WriterChunkID
	file.meta.UpdatedAt = now
	file.meta.SyncStatus = SyncStatusClean
	file.meta.Size = size
	file.meta.CurrentChecksum = checksum

	if file.meta.Kind == FileKindBinary {
		file.contentByG[nextGen] = memoryContent{blob: cloneBytes(nextBlob)}
	} else {
		file.contentByG[nextGen] = memoryContent{text: nextText}
	}

	file.versions = append(file.versions, VersionEntry{
		FileID:         file.meta.FileID,
		Generation:     nextGen,
		ObjectID:       objectIDForVersion(file.meta.FileID, nextGen),
		ContentRef:     contentRef(file.meta.FileID, nextGen),
		WriteMode:      input.Mode,
		BaseGeneration: baseGen,
		WriterChunkID:  input.WriterChunkID,
		Checksum:       checksum,
		Size:           size,
		CreatedAt:      now,
	})

	event := Event{
		EventID:   EventID(s.nextID("evt")),
		FileID:    file.meta.FileID,
		Type:      EventTypeFileWritten,
		ChunkID:   input.WriterChunkID,
		Timestamp: now,
		Payload: map[string]any{
			"write_mode":      input.Mode,
			"base_generation": baseGen,
			"generation":      nextGen,
		},
	}
	file.meta.LastEventID = event.EventID
	state.pendingEvts = append(state.pendingEvts, event)

	return makeFileRef(file.meta, now), nil
}

func (s *MemoryService) Delete(_ context.Context, txn Txn, input DeleteInput) (FileRef, error) {
	state, err := s.requireTxn(txn)
	if err != nil {
		return FileRef{}, err
	}

	file, err := state.resolve(input.Target, false)
	if err != nil {
		return FileRef{}, err
	}
	file, err = state.mutableFile(file.meta.FileID)
	if err != nil {
		return FileRef{}, err
	}

	now := time.Now().UTC()
	deletePathBinding(state.pathIndex, file.meta.VirtualPath, file.meta.Kind)
	state.deletedPaths[pathKindKey(file.meta.VirtualPath, file.meta.Kind)] = true
	file.meta.DeleteState = DeleteStateDeleted
	file.meta.UpdatedAt = now

	event := Event{
		EventID:   EventID(s.nextID("evt")),
		FileID:    file.meta.FileID,
		Type:      EventTypeFileDeleted,
		ChunkID:   input.ChunkID,
		Timestamp: now,
		Payload: map[string]any{
			"reason":       input.Reason,
			"virtual_path": file.meta.VirtualPath,
		},
	}
	file.meta.LastEventID = event.EventID
	state.pendingEvts = append(state.pendingEvts, event)

	return makeFileRef(file.meta, now), nil
}

func (s *MemoryService) Rename(_ context.Context, txn Txn, input RenameInput) (FileRef, error) {
	state, err := s.requireTxn(txn)
	if err != nil {
		return FileRef{}, err
	}
	if input.NewVirtualPath == "" {
		return FileRef{}, ErrInvalidInput
	}

	file, err := state.resolve(input.Target, false)
	if err != nil {
		return FileRef{}, err
	}
	file, err = state.mutableFile(file.meta.FileID)
	if err != nil {
		return FileRef{}, err
	}

	if existing, ok := state.resolvePath(input.NewVirtualPath, file.meta.Kind); ok && existing != file.meta.FileID {
		return FileRef{}, ErrPathAlreadyExists
	}

	oldPath := file.meta.VirtualPath
	deletePathBinding(state.pathIndex, oldPath, file.meta.Kind)
	state.deletedPaths[pathKindKey(oldPath, file.meta.Kind)] = true
	setPathBinding(state.pathIndex, input.NewVirtualPath, file.meta.Kind, file.meta.FileID)
	delete(state.deletedPaths, pathKindKey(input.NewVirtualPath, file.meta.Kind))

	now := time.Now().UTC()
	file.meta.VirtualPath = input.NewVirtualPath
	file.meta.UpdatedAt = now

	event := Event{
		EventID:   EventID(s.nextID("evt")),
		FileID:    file.meta.FileID,
		Type:      EventTypeFileRenamed,
		ChunkID:   input.ChunkID,
		Timestamp: now,
		Payload: map[string]any{
			"old_virtual_path": oldPath,
			"new_virtual_path": input.NewVirtualPath,
		},
	}
	file.meta.LastEventID = event.EventID
	state.pendingEvts = append(state.pendingEvts, event)

	return makeFileRef(file.meta, now), nil
}

func (s *MemoryService) requireTxn(txn Txn) (*memoryTxnState, error) {
	if txn.ID == "" || txn.ChunkID == "" {
		return nil, ErrTxnRequired
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	state, ok := s.txns[txn.ID]
	if !ok {
		return nil, ErrTxnRequired
	}
	if state.txn.ChunkID != txn.ChunkID {
		return nil, ErrTxnMismatch
	}
	return state, nil
}

func (s *MemoryService) resolveCommitted(input ResolveInput, allowDeleted bool) (*memoryFile, time.Time, error) {
	file, err := resolveFromState(s.files, s.pathIndex, input, allowDeleted)
	if err != nil {
		return nil, time.Time{}, err
	}
	return file, time.Now().UTC(), nil
}

func (t *memoryTxnState) resolve(input ResolveInput, allowDeleted bool) (*memoryFile, error) {
	hasPath := input.VirtualPath != ""
	hasID := input.FileID != ""
	if hasPath == hasID {
		return nil, ErrInvalidInput
	}

	var file *memoryFile
	if hasPath {
		fileID, err := t.resolvePathInput(input)
		if err != nil {
			return nil, err
		}
		file = t.lookupFile(fileID)
	} else {
		file = t.lookupFile(input.FileID)
	}
	if file == nil {
		return nil, ErrFileNotFound
	}
	if !allowDeleted && file.meta.DeleteState == DeleteStateDeleted {
		return nil, ErrFileDeleted
	}
	return file, nil
}

func (t *memoryTxnState) resolvePath(path string, kind FileKind) (FileID, bool) {
	key := pathKindKey(path, kind)
	if t.deletedPaths[key] {
		return "", false
	}
	if fileID, ok := t.pathIndex[key]; ok {
		return fileID, true
	}
	fileID, ok := t.basePathIndex[key]
	return fileID, ok
}

func (t *memoryTxnState) resolvePathInput(input ResolveInput) (FileID, error) {
	if input.ExpectedKind != "" {
		fileID, exists := t.resolvePath(input.VirtualPath, input.ExpectedKind)
		if !exists {
			return "", ErrFileNotFound
		}
		return fileID, nil
	}

	bindings := t.bindingsForPath(input.VirtualPath)
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

func (t *memoryTxnState) lookupFile(fileID FileID) *memoryFile {
	if file, ok := t.files[fileID]; ok {
		return file
	}
	return t.baseFiles[fileID]
}

func (t *memoryTxnState) mutableFile(fileID FileID) (*memoryFile, error) {
	if file, ok := t.files[fileID]; ok {
		return file, nil
	}
	base := t.baseFiles[fileID]
	if base == nil {
		return nil, ErrFileNotFound
	}
	cloned := cloneFile(base)
	t.files[fileID] = cloned
	return cloned, nil
}

func (t *memoryTxnState) listDir(virtualPath string) ([]DirEntry, error) {
	cleaned := normalizeVirtualPath(virtualPath)
	if cleaned != "/" {
		if fileID, ok := t.resolvePath(cleaned, FileKindDirectory); ok {
			file := t.lookupFile(fileID)
			if file == nil || file.meta.DeleteState == DeleteStateDeleted {
				return nil, ErrFileNotFound
			}
		} else if !t.hasVisibleChild(cleaned) {
			if t.hasAnyBinding(cleaned) {
				return nil, ErrInvalidKind
			}
			return nil, ErrFileNotFound
		}
	}

	entriesByBinding := make(map[string]DirEntry)
	t.forEachVisiblePathBinding(func(logicalPath string, kind FileKind, fileID FileID) {
		name, childPath, descends, ok := directChild(cleaned, logicalPath)
		if !ok {
			return
		}
		if descends {
			entryKey := pathKindKey(childPath, FileKindDirectory)
			entriesByBinding[entryKey] = DirEntry{Name: name, VirtualPath: childPath, Kind: FileKindDirectory}
			return
		}
		file := t.lookupFile(fileID)
		if file == nil || file.meta.DeleteState == DeleteStateDeleted {
			return
		}
		entryKey := pathKindKey(childPath, kind)
		entriesByBinding[entryKey] = DirEntry{Name: name, VirtualPath: childPath, Kind: kind}
	})

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

func (t *memoryTxnState) bindingsForPath(virtualPath string) map[FileKind]FileID {
	cleaned := normalizeVirtualPath(virtualPath)
	out := make(map[FileKind]FileID, len(indexedFileKinds))
	for _, kind := range indexedFileKinds {
		if fileID, ok := t.resolvePath(cleaned, kind); ok {
			out[kind] = fileID
		}
	}
	return out
}

func (t *memoryTxnState) hasAnyBinding(virtualPath string) bool {
	cleaned := normalizeVirtualPath(virtualPath)
	for _, kind := range indexedFileKinds {
		if _, ok := t.resolvePath(cleaned, kind); ok {
			return true
		}
	}
	return false
}

func (t *memoryTxnState) hasVisibleChild(dirPath string) bool {
	found := false
	t.forEachVisiblePathBinding(func(logicalPath string, kind FileKind, fileID FileID) {
		if found {
			return
		}
		_, _, _, ok := directChild(dirPath, logicalPath)
		if ok {
			found = true
		}
	})
	return found
}

func (t *memoryTxnState) forEachVisiblePathBinding(fn func(logicalPath string, kind FileKind, fileID FileID)) {
	seen := make(map[string]struct{}, len(t.basePathIndex)+len(t.pathIndex))
	for key, fileID := range t.pathIndex {
		if t.deletedPaths[key] {
			continue
		}
		logicalPath, kind, ok := splitPathKindKey(key)
		if !ok {
			continue
		}
		seen[key] = struct{}{}
		fn(logicalPath, kind, fileID)
	}
	for key, fileID := range t.basePathIndex {
		if t.deletedPaths[key] {
			continue
		}
		if _, ok := seen[key]; ok {
			continue
		}
		logicalPath, kind, ok := splitPathKindKey(key)
		if !ok {
			continue
		}
		fn(logicalPath, kind, fileID)
	}
}

func directChild(dirPath string, childPath string) (name string, fullPath string, descends bool, ok bool) {
	if childPath == dirPath {
		return "", "", false, false
	}
	var suffix string
	if dirPath == "/" {
		if !strings.HasPrefix(childPath, "/") || childPath == "/" {
			return "", "", false, false
		}
		suffix = strings.TrimPrefix(childPath, "/")
	} else {
		prefix := dirPath + "/"
		if !strings.HasPrefix(childPath, prefix) {
			return "", "", false, false
		}
		suffix = strings.TrimPrefix(childPath, prefix)
	}
	if suffix == "" {
		return "", "", false, false
	}
	parts := strings.SplitN(suffix, "/", 2)
	name = parts[0]
	if name == "" {
		return "", "", false, false
	}
	if dirPath == "/" {
		fullPath = "/" + name
	} else {
		fullPath = dirPath + "/" + name
	}
	return name, fullPath, len(parts) == 2, true
}

func resolveFromState(files map[FileID]*memoryFile, pathIndex map[string]FileID, input ResolveInput, allowDeleted bool) (*memoryFile, error) {
	hasPath := input.VirtualPath != ""
	hasID := input.FileID != ""
	if hasPath == hasID {
		return nil, ErrInvalidInput
	}

	var (
		file *memoryFile
		ok   bool
	)

	if hasPath {
		var fileID FileID
		if input.ExpectedKind != "" {
			var exists bool
			fileID, exists = lookupPathBinding(pathIndex, input.VirtualPath, input.ExpectedKind)
			if !exists {
				return nil, ErrFileNotFound
			}
		} else {
			bindings := bindingsForPath(pathIndex, input.VirtualPath)
			switch len(bindings) {
			case 0:
				return nil, ErrFileNotFound
			case 1:
				for _, id := range bindings {
					fileID = id
				}
			default:
				return nil, ErrAmbiguousPath
			}
		}
		file, ok = files[fileID]
	} else {
		file, ok = files[input.FileID]
	}
	if !ok || file == nil {
		return nil, ErrFileNotFound
	}
	if !allowDeleted && file.meta.DeleteState == DeleteStateDeleted {
		return nil, ErrFileDeleted
	}
	return file, nil
}

func cloneFiles(src map[FileID]*memoryFile) map[FileID]*memoryFile {
	dst := make(map[FileID]*memoryFile, len(src))
	for id, file := range src {
		dst[id] = cloneFile(file)
	}
	return dst
}

func cloneFile(src *memoryFile) *memoryFile {
	if src == nil {
		return nil
	}

	dst := &memoryFile{
		meta:       src.meta,
		versions:   append([]VersionEntry(nil), src.versions...),
		contentByG: make(map[Generation]memoryContent, len(src.contentByG)),
	}
	dst.meta.ParentLineage = cloneLineage(src.meta.ParentLineage)

	for g, c := range src.contentByG {
		dst.contentByG[g] = memoryContent{
			text: c.text,
			blob: cloneBytes(c.blob),
		}
	}

	return dst
}

func cloneLineage(src []LineageRef) []LineageRef {
	if len(src) == 0 {
		return nil
	}
	dst := make([]LineageRef, len(src))
	copy(dst, src)
	return dst
}

func cloneBytes(src []byte) []byte {
	if src == nil {
		return nil
	}
	dst := make([]byte, len(src))
	copy(dst, src)
	return dst
}

func bytesEqual(a []byte, b []byte) bool {
	return bytes.Equal(a, b)
}

func checksumText(text string) string {
	h := sha256.New()
	_, _ = io.WriteString(h, text)
	return hex.EncodeToString(h.Sum(nil))
}

func checksumBytes(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func contentRef(fileID FileID, generation Generation) string {
	return "mem://" + string(fileID) + "/" + strconv.FormatUint(uint64(generation), 10)
}

func objectIDForVersion(fileID FileID, generation Generation) string {
	return "obj_" + string(fileID) + "_" + strconv.FormatUint(uint64(generation), 10)
}

func (s *MemoryService) nextID(prefix string) string {
	s.seq++
	return fmt.Sprintf("%s_%d", prefix, s.seq)
}

func makeFileRef(meta FileMeta, resolvedAt time.Time) FileRef {
	return FileRef{
		FileID:            meta.FileID,
		VirtualPath:       meta.VirtualPath,
		Kind:              meta.Kind,
		ContentType:       meta.ContentType,
		CurrentGeneration: meta.CurrentGeneration,
		DeleteState:       meta.DeleteState,
		ResolvedAt:        resolvedAt,
	}
}

func applyReadView(text string, view ReadView) string {
	if view.Mode == "" || (view.StartLine == 0 && view.EndLine == 0) {
		return text
	}

	lines := strings.Split(text, "\n")
	start := view.StartLine
	end := view.EndLine

	if start <= 0 {
		start = 1
	}
	if end <= 0 || end > len(lines) {
		end = len(lines)
	}
	if start > end || start > len(lines) {
		return ""
	}

	return strings.Join(lines[start-1:end], "\n")
}

// Glob is not supported for in-memory VFS because there is no mount directory
// to scan for files. Use DiskService for glob operations.
func (s *MemoryService) Glob(ctx context.Context, pattern string) ([]string, error) {
	return nil, fmt.Errorf("vfs glob error: glob requires a disk-backed VFS (mount_dir)")
}
