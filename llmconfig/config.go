package llmconfig

import (
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strings"
)

const LegacyDefaultProvider = "default"

type ProviderConfig struct {
	BaseURL        string   `json:"base_url"`
	APIKey         string   `json:"api_key,omitempty"`
	APIKeyEnv      string   `json:"api_key_env,omitempty"`
	Model          string   `json:"model"`
	TimeoutSeconds int      `json:"timeout_seconds,omitempty"`
	Temperature    *float64 `json:"temperature,omitempty"`
	MaxTokens      *int     `json:"max_tokens,omitempty"`
}

type RouterConfig struct {
	DefaultProvider string `json:"default_provider"`
}

type Config struct {
	Providers map[string]ProviderConfig `json:"providers"`
	Router    RouterConfig              `json:"router"`
}

func LoadFile(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return ParseData(data)
}

func ParseData(data []byte) (*Config, error) {
	data = []byte(strings.TrimSpace(string(data)))
	if len(data) == 0 {
		return &Config{Providers: map[string]ProviderConfig{}}, nil
	}
	var probe map[string]json.RawMessage
	if err := json.Unmarshal(data, &probe); err != nil {
		return nil, err
	}
	if _, ok := probe["providers"]; ok {
		var cfg Config
		if err := json.Unmarshal(data, &cfg); err != nil {
			return nil, err
		}
		if cfg.Providers == nil {
			cfg.Providers = map[string]ProviderConfig{}
		}
		return &cfg, nil
	}
	var legacy ProviderConfig
	if err := json.Unmarshal(data, &legacy); err != nil {
		return nil, err
	}
	cfg := &Config{
		Providers: map[string]ProviderConfig{},
		Router: RouterConfig{
			DefaultProvider: LegacyDefaultProvider,
		},
	}
	if strings.TrimSpace(legacy.BaseURL) != "" || strings.TrimSpace(legacy.Model) != "" || strings.TrimSpace(legacy.APIKey) != "" || strings.TrimSpace(legacy.APIKeyEnv) != "" {
		cfg.Providers[LegacyDefaultProvider] = legacy
	}
	return cfg, nil
}

func (c *Config) Clone() *Config {
	if c == nil {
		return &Config{Providers: map[string]ProviderConfig{}}
	}
	out := &Config{
		Providers: make(map[string]ProviderConfig, len(c.Providers)),
		Router:    c.Router,
	}
	for name, provider := range c.Providers {
		out.Providers[name] = provider
	}
	return out
}

func (c *Config) SaveFile(path string) error {
	if c == nil {
		return fmt.Errorf("llm config is nil")
	}
	payload, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return err
	}
	payload = append(payload, '\n')
	return os.WriteFile(path, payload, 0o644)
}

func (c *Config) ProviderNames() []string {
	if c == nil {
		return nil
	}
	names := make([]string, 0, len(c.Providers))
	for name := range c.Providers {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

func (c *Config) DefaultProvider() string {
	if c == nil {
		return ""
	}
	return strings.TrimSpace(c.Router.DefaultProvider)
}

func (c *Config) SetDefaultProvider(name string) {
	if c == nil {
		return
	}
	c.Router.DefaultProvider = strings.TrimSpace(name)
}
