package vfs

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Config describes how the VFS should be wired to a concrete backend.
type Config struct {
	Backend         string   `json:"backend"`
	MountDir        string   `json:"mount_dir"`
	StateDir        string   `json:"state_dir,omitempty"`
	Mode            string   `json:"mode,omitempty"`
	WorkerCommand   []string `json:"worker_command,omitempty"`
	GC              GCConfig `json:"gc,omitempty"`
}

// GCConfig controls how the disk-backed VFS reclaims old state.
//
// DeletedFileRetention keeps the newest N deleted file objects as tombstones.
// A value of 0 purges deleted objects on the next commit. A negative value
// disables deleted-object reclamation.
//
// MaxEvents caps the persisted runtime event log. A value of 0 uses the
// recommended default, while a negative value disables event trimming.
type GCConfig struct {
	DeletedFileRetention int `json:"deleted_file_retention,omitempty"`
	MaxEvents            int `json:"max_events,omitempty"`
}

// LoadConfig reads a VFS config file and resolves relative paths against the
// directory that contains the config file.
func LoadConfig(path string) (Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Config{}, err
	}
	return ParseConfigData(data, filepath.Dir(path))
}

// ParseConfigData unmarshals JSON bytes into Config and resolves relative paths
// against baseDir (typically the directory containing the config file).
func ParseConfigData(data []byte, baseDir string) (Config, error) {
	var cfg Config
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&cfg); err != nil {
		return Config{}, err
	}

	var err error
	if cfg.MountDir, err = expandTildeInPath(cfg.MountDir); err != nil {
		return Config{}, err
	}
	if cfg.StateDir, err = expandTildeInPath(cfg.StateDir); err != nil {
		return Config{}, err
	}

	if cfg.MountDir != "" && !filepath.IsAbs(cfg.MountDir) {
		cfg.MountDir = filepath.Join(baseDir, cfg.MountDir)
	}
	if cfg.StateDir != "" && !filepath.IsAbs(cfg.StateDir) {
		cfg.StateDir = filepath.Join(baseDir, cfg.StateDir)
	}
	if cfg.StateDir == "" && cfg.MountDir != "" {
		cfg.StateDir = filepath.Join(cfg.MountDir, ".sati_vfs")
	}

	return cfg, nil
}

// expandTildeInPath turns "~" or "~/..." into the current user's home directory.
// Other paths are returned unchanged (including absolute paths and "./...").
func expandTildeInPath(p string) (string, error) {
	p = strings.TrimSpace(p)
	if p == "" {
		return "", nil
	}
	if p == "~" || strings.HasPrefix(p, "~/") {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", fmt.Errorf("vfs config: expand ~ in path: %w", err)
		}
		if p == "~" {
			return home, nil
		}
		return filepath.Join(home, strings.TrimPrefix(p, "~/")), nil
	}
	return p, nil
}
