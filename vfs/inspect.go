package vfs

// RuntimeSnapshot is the persisted committed-state view used for inspection.
type RuntimeSnapshot struct {
	Seq       uint64            `json:"seq"`
	Rev       uint64            `json:"rev"`
	PathIndex map[string]FileID `json:"path_index"`
	Files     []FileMeta        `json:"files"`
	Events    []Event           `json:"events"`
}

// FindFileByID returns the current metadata record for a file id.
func (s *RuntimeSnapshot) FindFileByID(fileID FileID) *FileMeta {
	if s == nil || fileID == "" {
		return nil
	}
	for i := range s.Files {
		if s.Files[i].FileID == fileID {
			return &s.Files[i]
		}
	}
	return nil
}

// FindFileByPath returns the current metadata record bound to a virtual path.
func (s *RuntimeSnapshot) FindFileByPath(path string) *FileMeta {
	if s == nil || path == "" {
		return nil
	}
	fileID, ok := s.PathIndex[path]
	if !ok {
		return nil
	}
	return s.FindFileByID(fileID)
}

// FilterEventsByFileID returns all events that belong to a file object.
func (s *RuntimeSnapshot) FilterEventsByFileID(fileID FileID) []Event {
	if s == nil || fileID == "" {
		return nil
	}
	var out []Event
	for _, event := range s.Events {
		if event.FileID == fileID {
			out = append(out, event)
		}
	}
	return out
}

// FilterEventsByChunkID returns all events emitted by a chunk execution.
func (s *RuntimeSnapshot) FilterEventsByChunkID(chunkID ChunkID) []Event {
	if s == nil || chunkID == "" {
		return nil
	}
	var out []Event
	for _, event := range s.Events {
		if event.ChunkID == chunkID {
			out = append(out, event)
		}
	}
	return out
}
