package satis

import (
	"context"
	"fmt"
	"path"
	"sort"
	"strings"

	"satis/vfs"
)

type sessionState struct {
	executor          *Executor
	chunkID           string
	txn               vfs.Txn
	env               map[string]runtimeValue
	conversations     map[string][]ConversationMessage
	cwd               string
	loadCWD           string
	softwareCWD       string
	notes             []string
	baseEnv           map[string]runtimeValue
	baseConversations map[string][]ConversationMessage
	baseCWD           string
	baseLoadCWD       string
	baseSoftwareCWD   string
	activeTxn         bool
}

// Session is a REPL-friendly execution context for SatisIL body instructions.
// It preserves env/cwd across line-by-line execution on top of VFS.
type Session struct {
	state *sessionState
}

// SessionVariable describes one visible @binding in the current REPL session.
type SessionVariable struct {
	Name string
	Kind string
}

func newSessionState(ctx context.Context, executor *Executor, chunkID string) (*sessionState, error) {
	if executor == nil || executor.VFS == nil {
		return nil, fmt.Errorf("satis session error: missing VFS service")
	}
	if chunkID == "" {
		return nil, fmt.Errorf("satis session error: missing chunk id")
	}
	txn, err := executor.VFS.BeginChunkTxn(ctx, vfs.ChunkID(chunkID))
	if err != nil {
		return nil, err
	}
	state := &sessionState{
		executor:      executor,
		chunkID:       chunkID,
		txn:           txn,
		env:           make(map[string]runtimeValue),
		conversations: make(map[string][]ConversationMessage),
		cwd:           executor.initialCWD(),
		loadCWD:       "/",
		softwareCWD:   "/",
		activeTxn:     true,
	}
	for name, binding := range executor.InputBindings {
		value, err := bindingToRuntimeValue(binding)
		if err != nil {
			return nil, err
		}
		state.env[name] = value
		if value.kind == runtimeValueConversation {
			state.conversations[name] = append([]ConversationMessage(nil), value.conversation...)
		}
	}
	for name, value := range executor.InputValueBindings {
		if _, exists := state.env[name]; !exists {
			state.env[name] = runtimeValue{kind: runtimeValueText, text: value}
		}
	}
	state.markSegmentBase()
	return state, nil
}

// NewSession creates a session that can execute body-only SatisIL incrementally.
func (e *Executor) NewSession(ctx context.Context, chunkID string) (*Session, error) {
	state, err := newSessionState(ctx, e, chunkID)
	if err != nil {
		return nil, err
	}
	return &Session{state: state}, nil
}

func (s *Session) ExecLine(ctx context.Context, line string) error {
	return s.ExecBody(ctx, line)
}

func (s *Session) ExecBody(ctx context.Context, src string) error {
	if s == nil || s.state == nil {
		return fmt.Errorf("satis session error: session is closed")
	}
	instructions, err := ParseBody(src)
	if err != nil {
		return err
	}
	return s.state.executeInstructions(ctx, instructions)
}

func (s *Session) Commit(ctx context.Context) error {
	if s == nil || s.state == nil {
		return fmt.Errorf("satis session error: session is closed")
	}
	return s.state.commitSegment(ctx)
}

func (s *Session) Rollback(ctx context.Context) error {
	if s == nil || s.state == nil {
		return fmt.Errorf("satis session error: session is closed")
	}
	return s.state.rollbackSegment(ctx)
}

func (s *Session) Close(ctx context.Context) error {
	if s == nil || s.state == nil {
		return nil
	}
	err := s.state.abort(ctx)
	s.state = nil
	return err
}

func (s *Session) CurrentCWD() string {
	if s == nil || s.state == nil {
		return "/"
	}
	return s.state.cwd
}

func (s *Session) CurrentLoadCWD() string {
	if s == nil || s.state == nil {
		return "/"
	}
	return s.state.loadCWD
}

func (s *Session) CurrentSoftwareCWD() string {
	if s == nil || s.state == nil {
		return "/"
	}
	return s.state.softwareCWD
}

func (s *Session) ListVariables() []SessionVariable {
	if s == nil || s.state == nil {
		return nil
	}
	seen := make(map[string]SessionVariable, len(s.state.env)+len(s.state.conversations))
	for name, value := range s.state.env {
		seen[name] = SessionVariable{Name: name, Kind: string(value.kind)}
	}
	for name := range s.state.conversations {
		if _, ok := seen[name]; ok {
			continue
		}
		seen[name] = SessionVariable{Name: name, Kind: "conversation"}
	}
	out := make([]SessionVariable, 0, len(seen))
	for _, variable := range seen {
		out = append(out, variable)
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].Name < out[j].Name
	})
	return out
}

func (s *Session) ResolvePath(path string) string {
	if s == nil || s.state == nil {
		return normalizeAbsolutePath(path)
	}
	return resolvePath(s.state.cwd, path)
}

func (s *Session) ListDir(ctx context.Context, virtualPath string) ([]vfs.DirEntry, error) {
	if s == nil || s.state == nil {
		return nil, fmt.Errorf("satis session error: session is closed")
	}
	return s.state.executor.VFS.ListDir(ctx, s.state.txn, virtualPath)
}

func (s *Session) ListLoadDir(ctx context.Context, virtualPath string) ([]LoadEntry, error) {
	if s == nil || s.state == nil {
		return nil, fmt.Errorf("satis session error: session is closed")
	}
	if s.state.executor == nil || s.state.executor.LoadPort == nil {
		return nil, fmt.Errorf("satis session error: load is not configured")
	}
	targetPath := resolvePath(s.state.loadCWD, virtualPath)
	return s.state.executor.LoadPort.ListDir(ctx, targetPath)
}

func (s *Session) ReadVirtualText(ctx context.Context, virtualPath string) (string, error) {
	if s == nil || s.state == nil {
		return "", fmt.Errorf("satis session error: session is closed")
	}
	targetPath := s.ResolvePath(virtualPath)
	for _, kind := range []vfs.FileKind{vfs.FileKindText, vfs.FileKindGenerated, vfs.FileKindEphemeral} {
		result, err := s.state.executor.VFS.Read(ctx, s.state.txn, vfs.ReadInput{
			Target: vfs.ResolveInput{VirtualPath: targetPath, ExpectedKind: kind},
		})
		if err == nil {
			return result.Text, nil
		}
		if err != vfs.ErrFileNotFound {
			return "", err
		}
	}
	return "", vfs.ErrFileNotFound
}

func (s *Session) WriteVirtualText(ctx context.Context, virtualPath string, text string) (vfs.FileRef, error) {
	if s == nil || s.state == nil {
		return vfs.FileRef{}, fmt.Errorf("satis session error: session is closed")
	}
	targetPath := s.ResolvePath(virtualPath)
	for _, kind := range []vfs.FileKind{vfs.FileKindText, vfs.FileKindGenerated, vfs.FileKindEphemeral} {
		ref, err := s.state.executor.VFS.Resolve(ctx, vfs.ResolveInput{VirtualPath: targetPath, ExpectedKind: kind})
		if err == nil {
			return s.state.executor.VFS.Write(ctx, s.state.txn, vfs.WriteInput{
				Target:        vfs.ResolveInput{FileID: ref.FileID},
				WriterChunkID: s.state.txn.ChunkID,
				Mode:          vfs.WriteModeReplaceFull,
				Text:          text,
			})
		}
		if err != vfs.ErrFileNotFound {
			return vfs.FileRef{}, err
		}
	}
	return s.state.executor.VFS.Create(ctx, s.state.txn, vfs.CreateInput{
		VirtualPath:    targetPath,
		Kind:           vfs.FileKindText,
		CreatorChunkID: s.state.txn.ChunkID,
		InitialText:    text,
	})
}

func (s *Session) EnsureVirtualDirectory(ctx context.Context, virtualPath string) error {
	if s == nil || s.state == nil {
		return fmt.Errorf("satis session error: session is closed")
	}
	targetPath := s.ResolvePath(virtualPath)
	if targetPath == "/" {
		return nil
	}
	segments := strings.Split(strings.TrimPrefix(targetPath, "/"), "/")
	current := "/"
	for _, segment := range segments {
		if segment == "" {
			continue
		}
		current = path.Join(current, segment)
		if _, err := s.state.executor.VFS.Resolve(ctx, vfs.ResolveInput{
			VirtualPath:  current,
			ExpectedKind: vfs.FileKindDirectory,
		}); err == nil {
			continue
		} else if err != vfs.ErrFileNotFound {
			return err
		}
		if _, err := s.state.executor.VFS.Create(ctx, s.state.txn, vfs.CreateInput{
			VirtualPath:    current,
			Kind:           vfs.FileKindDirectory,
			CreatorChunkID: s.state.txn.ChunkID,
		}); err != nil {
			return err
		}
	}
	return nil
}

func (s *sessionState) executeInstructions(ctx context.Context, instructions []Instruction) error {
	for _, inst := range instructions {
		switch inst.(type) {
		case CommitStmt:
			if err := s.commitSegment(ctx); err != nil {
				return err
			}
			continue
		case RollbackStmt:
			if err := s.rollbackSegment(ctx); err != nil {
				return err
			}
			continue
		}
		note, err := s.executor.executeInstruction(ctx, s.txn, s.env, s.conversations, &s.cwd, &s.loadCWD, &s.softwareCWD, inst)
		if err != nil {
			return err
		}
		if note != "" {
			s.notes = append(s.notes, note)
		}
	}
	return nil
}

func (s *sessionState) finish(ctx context.Context) error {
	if !s.activeTxn {
		return nil
	}
	if err := s.executor.VFS.CommitChunkTxn(ctx, s.txn); err != nil {
		return err
	}
	s.activeTxn = false
	return nil
}

func (s *sessionState) abort(ctx context.Context) error {
	if !s.activeTxn {
		return nil
	}
	if err := s.executor.VFS.RollbackChunkTxn(ctx, s.txn); err != nil {
		return err
	}
	s.activeTxn = false
	return nil
}

func (s *sessionState) commitSegment(ctx context.Context) error {
	if err := s.finish(ctx); err != nil {
		return err
	}
	return s.beginNextTxn(ctx)
}

func (s *sessionState) rollbackSegment(ctx context.Context) error {
	if err := s.abort(ctx); err != nil {
		return err
	}
	s.env = copyRuntimeEnvMap(s.baseEnv)
	s.conversations = copyConversationMap(s.baseConversations)
	s.cwd = s.baseCWD
	s.loadCWD = s.baseLoadCWD
	s.softwareCWD = s.baseSoftwareCWD
	return s.beginNextTxn(ctx)
}

func (s *sessionState) beginNextTxn(ctx context.Context) error {
	txn, err := s.executor.VFS.BeginChunkTxn(ctx, vfs.ChunkID(s.chunkID))
	if err != nil {
		return err
	}
	s.txn = txn
	s.activeTxn = true
	s.markSegmentBase()
	return nil
}

func (s *sessionState) markSegmentBase() {
	s.baseEnv = copyRuntimeEnvMap(s.env)
	s.baseConversations = copyConversationMap(s.conversations)
	s.baseCWD = s.cwd
	s.baseLoadCWD = s.loadCWD
	s.baseSoftwareCWD = s.softwareCWD
}

func copyRuntimeEnvMap(src map[string]runtimeValue) map[string]runtimeValue {
	dst := make(map[string]runtimeValue, len(src))
	for key, value := range src {
		dst[key] = value
	}
	return dst
}

func copyConversationMap(src map[string][]ConversationMessage) map[string][]ConversationMessage {
	dst := make(map[string][]ConversationMessage, len(src))
	for key, messages := range src {
		dst[key] = messages
	}
	return dst
}
