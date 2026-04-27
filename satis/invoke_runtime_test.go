package satis

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"satis/llmconfig"
	"satis/vfs"
)

type routedTestInvoker struct {
	lastProvider string
	lastPrompt   string
	lastInput    string
	cfg          *llmconfig.Config
}

func (r *routedTestInvoker) Invoke(_ context.Context, prompt string, input string) (string, error) {
	r.lastProvider = ""
	r.lastPrompt = prompt
	r.lastInput = input
	return "default:" + prompt + ":" + input, nil
}

func (r *routedTestInvoker) InvokeWithProvider(_ context.Context, provider string, prompt string, input string) (string, error) {
	r.lastProvider = provider
	r.lastPrompt = prompt
	r.lastInput = input
	return provider + ":" + prompt + ":" + input, nil
}

func (r *routedTestInvoker) InvokeMessages(_ context.Context, messages []ConversationMessage) (string, error) {
	r.lastProvider = ""
	return messages[len(messages)-1].Content, nil
}

func (r *routedTestInvoker) InvokeMessagesWithProvider(_ context.Context, provider string, messages []ConversationMessage) (string, error) {
	r.lastProvider = provider
	return provider + ":" + messages[len(messages)-1].Content, nil
}

func (r *routedTestInvoker) ReloadInvokeConfig(cfg *llmconfig.Config) error {
	r.cfg = cfg.Clone()
	return nil
}

func TestExecuteInvokeUsesExplicitProvider(t *testing.T) {
	exec := &Executor{Invoker: &routedTestInvoker{}}
	env := map[string]runtimeValue{
		"@input": {kind: runtimeValueText, text: "world"},
	}
	_, err := exec.executeInstruction(context.Background(), stubTxn(), env, nil, nil, nil, nil, InvokeStmt{
		Line:      1,
		Prompt:    Value{Kind: ValueKindString, Text: "hello"},
		HasInput:  true,
		Input:     Value{Kind: ValueKindVariable, Text: "@input"},
		Provider:  "fast",
		OutputVar: "@out",
	})
	if err != nil {
		t.Fatalf("executeInstruction returned error: %v", err)
	}
	if got := env["@out"].text; got != "fast:hello:world" {
		t.Fatalf("unexpected invoke output %q", got)
	}
}

func TestInvokeBatchWithProviderUsesRouter(t *testing.T) {
	inv := &routedTestInvoker{}
	exec := &Executor{Invoker: inv}
	got, err := exec.InvokeBatchWithProvider(context.Background(), "fast", "prompt", []string{"a", "b"})
	if err != nil {
		t.Fatalf("InvokeBatchWithProvider returned error: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("expected 2 results, got %d", len(got))
	}
	if inv.lastProvider != "fast" {
		t.Fatalf("expected last provider fast, got %q", inv.lastProvider)
	}
}

func TestExecuteInvokeProviderUpsertWritesConfigAndReloads(t *testing.T) {
	path := filepath.Join(t.TempDir(), "invoke.config.json")
	inv := &routedTestInvoker{}
	exec := &Executor{Invoker: inv, InvokeConfigPath: path}
	_, err := exec.executeInstruction(context.Background(), stubTxn(), map[string]runtimeValue{}, nil, nil, nil, nil, InvokeProviderStmt{
		Line:   1,
		Action: "upsert",
		Name:   "fast",
		Flags: []SoftwareFlag{
			{Name: "--base-url", Value: Value{Kind: ValueKindString, Text: "https://example.test/v1"}},
			{Name: "--model", Value: Value{Kind: ValueKindString, Text: "demo"}},
		},
	})
	if err != nil {
		t.Fatalf("executeInstruction returned error: %v", err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile returned error: %v", err)
	}
	if !strings.Contains(string(data), "\"fast\"") {
		t.Fatalf("expected provider config to contain fast, got %s", string(data))
	}
	if inv.cfg == nil || inv.cfg.DefaultProvider() != "fast" {
		t.Fatalf("expected reloaded config default provider fast, got %#v", inv.cfg)
	}
}

func TestExecuteInvokeProviderSetDefaultMissingProviderFails(t *testing.T) {
	path := filepath.Join(t.TempDir(), "invoke.config.json")
	cfg := &llmconfig.Config{
		Providers: map[string]llmconfig.ProviderConfig{
			"fast": {BaseURL: "https://example.test/v1", Model: "demo"},
		},
	}
	if err := cfg.SaveFile(path); err != nil {
		t.Fatalf("SaveFile returned error: %v", err)
	}
	exec := &Executor{Invoker: &routedTestInvoker{}, InvokeConfigPath: path}
	_, err := exec.executeInstruction(context.Background(), stubTxn(), map[string]runtimeValue{}, nil, nil, nil, nil, InvokeProviderStmt{
		Line:   1,
		Action: "set-default",
		Name:   "missing",
	})
	if err == nil || !strings.Contains(err.Error(), "unknown invoke provider") {
		t.Fatalf("expected missing provider error, got %v", err)
	}
}

func stubTxn() vfs.Txn {
	return vfs.Txn{ID: "txn_1", ChunkID: "chunk"}
}
