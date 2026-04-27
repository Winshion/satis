package satis

import (
	"context"
	"reflect"
	"testing"

	"satis/vfs"
)

func BenchmarkSessionSnapshotCopy(b *testing.B) {
	env, conversations := buildSessionSnapshotFixtures()
	b.Run("optimized", func(b *testing.B) {
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			_ = copyRuntimeEnvMap(env)
			_ = copyConversationMap(conversations)
		}
	})
	b.Run("reference", func(b *testing.B) {
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			_ = copyRuntimeEnvDeepReference(env)
			_ = copyConversationDeepReference(conversations)
		}
	})
}

func TestSessionSnapshotCopyMatchesReference(t *testing.T) {
	env, conversations := buildSessionSnapshotFixtures()

	gotEnv := copyRuntimeEnvMap(env)
	wantEnv := copyRuntimeEnvDeepReference(env)
	if !reflect.DeepEqual(gotEnv, wantEnv) {
		t.Fatalf("env snapshot mismatch: got %#v want %#v", gotEnv, wantEnv)
	}

	gotConversations := copyConversationMap(conversations)
	wantConversations := copyConversationDeepReference(conversations)
	if !reflect.DeepEqual(gotConversations, wantConversations) {
		t.Fatalf("conversation snapshot mismatch: got %#v want %#v", gotConversations, wantConversations)
	}
}

func TestSessionRollbackRestoresBaseState(t *testing.T) {
	ctx := context.Background()
	state := &sessionState{
		executor:          &Executor{VFS: &stubSessionVFS{}},
		chunkID:           "chunk",
		txn:               vfs.Txn{ID: "txn_1", ChunkID: "chunk"},
		env:               map[string]runtimeValue{"items": {kind: runtimeValueTextList, texts: []string{"one", "two"}}},
		conversations:     map[string][]ConversationMessage{"chat": {{Role: "user", Content: "hello"}}},
		cwd:               "/a",
		loadCWD:           "/load",
		softwareCWD:       "/software",
		activeTxn:         true,
		baseEnv:           map[string]runtimeValue{"items": {kind: runtimeValueTextList, texts: []string{"one", "two"}}},
		baseConversations: map[string][]ConversationMessage{"chat": {{Role: "user", Content: "hello"}}},
		baseCWD:           "/base",
		baseLoadCWD:       "/base-load",
		baseSoftwareCWD:   "/base-software",
	}

	state.env["items"] = runtimeValue{kind: runtimeValueTextList, texts: []string{"changed"}}
	state.conversations["chat"] = append(state.conversations["chat"], ConversationMessage{Role: "assistant", Content: "reply"})
	state.cwd = "/changed"
	state.loadCWD = "/changed-load"
	state.softwareCWD = "/changed-software"

	if err := state.rollbackSegment(ctx); err != nil {
		t.Fatalf("rollback segment: %v", err)
	}

	if got, want := state.env["items"].texts, []string{"one", "two"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("env restored mismatch: got %v want %v", got, want)
	}
	if got, want := state.conversations["chat"], []ConversationMessage{{Role: "user", Content: "hello"}}; !reflect.DeepEqual(got, want) {
		t.Fatalf("conversation restored mismatch: got %v want %v", got, want)
	}
	if state.cwd != "/base" || state.loadCWD != "/base-load" || state.softwareCWD != "/base-software" {
		t.Fatalf("cwd state mismatch after rollback: %q %q %q", state.cwd, state.loadCWD, state.softwareCWD)
	}
}

func buildSessionSnapshotFixtures() (map[string]runtimeValue, map[string][]ConversationMessage) {
	env := map[string]runtimeValue{
		"text":   {kind: runtimeValueText, text: "hello"},
		"items":  {kind: runtimeValueTextList, texts: []string{"a", "b", "c"}},
		"refs":   {kind: runtimeValueObjectList, objects: []vfs.FileRef{{FileID: "1", VirtualPath: "/a"}, {FileID: "2", VirtualPath: "/b"}}},
		"dialog": {kind: runtimeValueConversation, conversation: []ConversationMessage{{Role: "user", Content: "q"}, {Role: "assistant", Content: "a"}}},
	}
	conversations := map[string][]ConversationMessage{
		"chat": {
			{Role: "user", Content: "hello"},
			{Role: "assistant", Content: "world"},
		},
	}
	return env, conversations
}

func copyRuntimeEnvDeepReference(src map[string]runtimeValue) map[string]runtimeValue {
	dst := make(map[string]runtimeValue, len(src))
	for key, value := range src {
		cloned := value
		if value.texts != nil {
			cloned.texts = append([]string(nil), value.texts...)
		}
		if value.objects != nil {
			cloned.objects = append([]vfs.FileRef(nil), value.objects...)
		}
		if value.conversation != nil {
			cloned.conversation = append([]ConversationMessage(nil), value.conversation...)
		}
		dst[key] = cloned
	}
	return dst
}

func copyConversationDeepReference(src map[string][]ConversationMessage) map[string][]ConversationMessage {
	dst := make(map[string][]ConversationMessage, len(src))
	for key, messages := range src {
		dst[key] = append([]ConversationMessage(nil), messages...)
	}
	return dst
}

type stubSessionVFS struct {
	next int
}

func (s *stubSessionVFS) BeginChunkTxn(_ context.Context, chunkID vfs.ChunkID) (vfs.Txn, error) {
	s.next++
	return vfs.Txn{ID: vfs.TxnID("txn_next"), ChunkID: chunkID}, nil
}
func (s *stubSessionVFS) CommitChunkTxn(context.Context, vfs.Txn) error   { return nil }
func (s *stubSessionVFS) RollbackChunkTxn(context.Context, vfs.Txn) error { return nil }
func (s *stubSessionVFS) Create(context.Context, vfs.Txn, vfs.CreateInput) (vfs.FileRef, error) {
	return vfs.FileRef{}, nil
}
func (s *stubSessionVFS) Resolve(context.Context, vfs.ResolveInput) (vfs.FileRef, error) {
	return vfs.FileRef{}, nil
}
func (s *stubSessionVFS) Read(context.Context, vfs.Txn, vfs.ReadInput) (vfs.ReadResult, error) {
	return vfs.ReadResult{}, nil
}
func (s *stubSessionVFS) ListDir(context.Context, vfs.Txn, string) ([]vfs.DirEntry, error) {
	return nil, nil
}
func (s *stubSessionVFS) Write(context.Context, vfs.Txn, vfs.WriteInput) (vfs.FileRef, error) {
	return vfs.FileRef{}, nil
}
func (s *stubSessionVFS) Delete(context.Context, vfs.Txn, vfs.DeleteInput) (vfs.FileRef, error) {
	return vfs.FileRef{}, nil
}
func (s *stubSessionVFS) Rename(context.Context, vfs.Txn, vfs.RenameInput) (vfs.FileRef, error) {
	return vfs.FileRef{}, nil
}
func (s *stubSessionVFS) Glob(context.Context, string) ([]string, error) { return nil, nil }
