// Package appconfig loads combined application config (VFS + optional invoke block).
package appconfig

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"satis/llmconfig"
	"satis/vfs"
)

const defaultSoftwareRegistrySkillsTemplate = `---
name: software_registry
description: Root index of registered software folders and software entries.
---

## Entries

<!--
Folder-level index only.
Add entries like:
- tools: Data processing tools
- sorting: Sort comma-separated integers and return sorted text.
-->
`

// LoadVFSAndInvoke reads a JSON file that may contain an "invoke" object alongside
// vfs fields. The "invoke" key is stripped before parsing VFS config so it does not
// interfere with vfs.Config. Relative paths in the VFS section are resolved against
// the directory containing the config file.
func LoadVFSAndInvoke(configPath string) (vfs.Config, *llmconfig.Config, string, string, error) {
	data, err := os.ReadFile(configPath)
	if err != nil {
		return vfs.Config{}, nil, "", "", err
	}

	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		return vfs.Config{}, nil, "", "", err
	}

	var settings *llmconfig.Config
	if invRaw, ok := raw["invoke"]; ok {
		s, err := llmconfig.ParseData(invRaw)
		if err != nil {
			return vfs.Config{}, nil, "", "", err
		}
		settings = s
		delete(raw, "invoke")
	}
	systemPortDir := ""
	if portRaw, ok := raw["system_port_dir"]; ok {
		if err := json.Unmarshal(portRaw, &systemPortDir); err != nil {
			return vfs.Config{}, nil, "", "", err
		}
		delete(raw, "system_port_dir")
	}
	softwareRegistryDir := ""
	if registryRaw, ok := raw["software_registry_dir"]; ok {
		if err := json.Unmarshal(registryRaw, &softwareRegistryDir); err != nil {
			return vfs.Config{}, nil, "", "", err
		}
		delete(raw, "software_registry_dir")
	}

	core, err := json.Marshal(raw)
	if err != nil {
		return vfs.Config{}, nil, "", "", err
	}

	baseDir := filepath.Dir(configPath)
	cfg, err := vfs.ParseConfigData(core, baseDir)
	if err != nil {
		return vfs.Config{}, nil, "", "", err
	}
	systemPortDir, err = resolveHostPath("system_port_dir", systemPortDir, baseDir)
	if err != nil {
		return vfs.Config{}, nil, "", "", err
	}
	softwareRegistryDir, err = resolveHostPath("software_registry_dir", softwareRegistryDir, baseDir)
	if err != nil {
		return vfs.Config{}, nil, "", "", err
	}
	return cfg, settings, systemPortDir, softwareRegistryDir, nil
}

func resolveHostPath(fieldName string, raw string, baseDir string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", nil
	}
	if raw == "~" || strings.HasPrefix(raw, "~/") {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", fmt.Errorf("appconfig: expand ~ in %s: %w", fieldName, err)
		}
		if raw == "~" {
			return home, nil
		}
		return filepath.Join(home, strings.TrimPrefix(raw, "~/")), nil
	}
	if filepath.IsAbs(raw) {
		return filepath.Clean(raw), nil
	}
	return filepath.Join(baseDir, raw), nil
}

// EnsureRuntimeDirs creates configured host directories required at startup.
// Missing directories are created; existing directories are preserved.
func EnsureRuntimeDirs(cfg vfs.Config, systemPortDir string, softwareRegistryDir string) error {
	candidates := []string{
		strings.TrimSpace(systemPortDir),
		strings.TrimSpace(softwareRegistryDir),
	}
	if strings.EqualFold(strings.TrimSpace(cfg.Backend), "disk") {
		candidates = append(candidates, strings.TrimSpace(cfg.MountDir), strings.TrimSpace(cfg.StateDir))
	}
	seen := make(map[string]struct{}, len(candidates))
	dirs := make([]string, 0, len(candidates))
	for _, dir := range candidates {
		if dir == "" {
			continue
		}
		if _, ok := seen[dir]; ok {
			continue
		}
		seen[dir] = struct{}{}
		dirs = append(dirs, dir)
	}
	sort.Strings(dirs)
	for _, dir := range dirs {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return fmt.Errorf("appconfig: ensure runtime dir %q: %w", dir, err)
		}
	}
	if err := ensureSoftwareRegistryTemplate(strings.TrimSpace(softwareRegistryDir)); err != nil {
		return err
	}
	return nil
}

func ensureSoftwareRegistryTemplate(softwareRegistryDir string) error {
	if softwareRegistryDir == "" {
		return nil
	}
	skillsPath := filepath.Join(softwareRegistryDir, "SKILLS.md")
	if _, err := os.Stat(skillsPath); err == nil {
		return nil
	} else if !os.IsNotExist(err) {
		return fmt.Errorf("appconfig: stat %q: %w", skillsPath, err)
	}
	if err := os.WriteFile(skillsPath, []byte(defaultSoftwareRegistrySkillsTemplate), 0o644); err != nil {
		return fmt.Errorf("appconfig: create software registry template %q: %w", skillsPath, err)
	}
	return nil
}
