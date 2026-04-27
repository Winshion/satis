package llmconfig

import "testing"

func TestParseDataLegacyConfig(t *testing.T) {
	cfg, err := ParseData([]byte(`{"base_url":"https://example.test/v1","model":"demo"}`))
	if err != nil {
		t.Fatalf("ParseData returned error: %v", err)
	}
	if cfg.DefaultProvider() != LegacyDefaultProvider {
		t.Fatalf("expected default provider %q, got %q", LegacyDefaultProvider, cfg.DefaultProvider())
	}
	if got := cfg.Providers[LegacyDefaultProvider].Model; got != "demo" {
		t.Fatalf("expected legacy model demo, got %q", got)
	}
}

func TestParseDataMultiProviderConfig(t *testing.T) {
	cfg, err := ParseData([]byte(`{"providers":{"fast":{"base_url":"https://example.test/v1","model":"demo"}},"router":{"default_provider":"fast"}}`))
	if err != nil {
		t.Fatalf("ParseData returned error: %v", err)
	}
	if cfg.DefaultProvider() != "fast" {
		t.Fatalf("expected default provider fast, got %q", cfg.DefaultProvider())
	}
	if _, ok := cfg.Providers["fast"]; !ok {
		t.Fatalf("expected fast provider to exist")
	}
}
