package invoke

import (
	"path/filepath"

	"satis/llmconfig"
)

// Settings is kept as an alias for backward compatibility with the original
// single-provider invoke configuration shape.
type Settings = llmconfig.ProviderConfig

type Config = llmconfig.Config

func LoadFile(path string) (*Config, error) {
	return llmconfig.LoadFile(path)
}

func ResolveConfigPath(configPath string, override string) string {
	if override != "" {
		return override
	}
	if configPath == "" {
		return "invoke.config.json"
	}
	return filepath.Join(filepath.Dir(configPath), "invoke.config.json")
}
