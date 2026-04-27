package invoke

import (
	"context"
	"os"
	"testing"

	"satis/llmconfig"
)

func TestDefaultProviderSelectorRejectsMissingDefaultProvider(t *testing.T) {
	selector := &DefaultProviderSelector{}
	_, err := selector.Select(context.Background(), &llmconfig.Config{
		Providers: map[string]llmconfig.ProviderConfig{
			"fast": {BaseURL: "https://example.test/v1", Model: "demo"},
		},
		Router: llmconfig.RouterConfig{DefaultProvider: "missing"},
	}, "")
	if err == nil {
		t.Fatalf("expected error for missing default provider")
	}
}

func TestRouterInvokerUsesPreferredProvider(t *testing.T) {
	t.Setenv("FAST_API_KEY", "test-key")
	cfg := &llmconfig.Config{
		Providers: map[string]llmconfig.ProviderConfig{
			"fast": {BaseURL: "https://example.test/v1", APIKeyEnv: "FAST_API_KEY", Model: "demo"},
		},
		Router: llmconfig.RouterConfig{DefaultProvider: "fast"},
	}
	router, err := NewRouterInvoker(cfg)
	if err != nil {
		t.Fatalf("NewRouterInvoker returned error: %v", err)
	}
	if _, _, err := router.selectInvoker(context.Background(), "fast"); err != nil {
		t.Fatalf("selectInvoker returned error: %v", err)
	}
}

func TestResolveConfigPathDefaultsBesideMainConfig(t *testing.T) {
	got := ResolveConfigPath("/tmp/runtime/vfs.config.json", "")
	if got != "/tmp/runtime/invoke.config.json" {
		t.Fatalf("unexpected resolved config path %q", got)
	}
	if override := ResolveConfigPath("/tmp/runtime/vfs.config.json", "/tmp/custom.json"); override != "/tmp/custom.json" {
		t.Fatalf("override should win, got %q", override)
	}
}

func TestLoadFileUsesMultiProviderFormat(t *testing.T) {
	path := t.TempDir() + "/invoke.config.json"
	data := `{"providers":{"fast":{"base_url":"https://example.test/v1","model":"demo"}},"router":{"default_provider":"fast"}}`
	if err := os.WriteFile(path, []byte(data), 0o644); err != nil {
		t.Fatalf("WriteFile returned error: %v", err)
	}
	cfg, err := LoadFile(path)
	if err != nil {
		t.Fatalf("LoadFile returned error: %v", err)
	}
	if cfg.DefaultProvider() != "fast" {
		t.Fatalf("expected default provider fast, got %q", cfg.DefaultProvider())
	}
}
