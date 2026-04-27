package vfsremote

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"satis/sandbox"
	"satis/vfs"
)

type ProcessClient struct {
	*Client
	session *sandbox.Session
}

func StartProcess(ctx context.Context, cfg vfs.Config, configPath string) (*ProcessClient, error) {
	args := cfg.WorkerCommand
	if len(args) == 0 {
		args = []string{"satis-worker"}
	}
	cmdArgs := append([]string(nil), args[1:]...)
	if configPath != "" {
		cmdArgs = append(cmdArgs, "--config", configPath)
	}
	driver, err := sandbox.NewDriver()
	if err != nil {
		return nil, err
	}
	readWritePaths := collectSecureReadWritePaths(cfg)
	readOnlyPaths := collectSecureReadOnlyPaths(configPath)
	extraEnv := collectSecureEnv()
	session, err := driver.LaunchWorker(ctx, sandbox.LaunchConfig{
		Command:        args[0],
		Args:           cmdArgs,
		SecureMode:     cfg.Mode == "secure",
		ReadWritePaths: readWritePaths,
		ReadOnlyPaths:  readOnlyPaths,
		ExtraEnv:       extraEnv,
	})
	if err != nil {
		return nil, err
	}

	client := NewClient(struct {
		io.Reader
		io.Writer
		io.Closer
	}{
		Reader: session.Stdout,
		Writer: session.Stdin,
		Closer: session,
	})
	return &ProcessClient{Client: client, session: session}, nil
}

func collectSecureReadWritePaths(cfg vfs.Config) []string {
	seen := map[string]struct{}{}
	var paths []string
	add := func(path string) {
		path = strings.TrimSpace(path)
		if path == "" {
			return
		}
		path = filepath.Clean(path)
		for _, candidate := range withParentVariants(path) {
			if _, ok := seen[candidate]; ok {
				continue
			}
			seen[candidate] = struct{}{}
			paths = append(paths, candidate)
		}
	}
	add(cfg.MountDir)
	add(cfg.StateDir)
	add(os.Getenv("GOCACHE"))
	add(os.TempDir())
	add(os.Getenv("TMPDIR"))
	return paths
}

func collectSecureReadOnlyPaths(configPath string) []string {
	seen := map[string]struct{}{}
	var paths []string
	add := func(path string) {
		path = strings.TrimSpace(path)
		if path == "" {
			return
		}
		path = filepath.Clean(path)
		for _, candidate := range pathVariants(path) {
			if _, ok := seen[candidate]; ok {
				continue
			}
			seen[candidate] = struct{}{}
			paths = append(paths, candidate)
		}
	}
	add(configPath)
	if wd, err := os.Getwd(); err == nil {
		add(wd)
	}
	return paths
}

func collectSecureEnv() []string {
	var out []string
	for _, key := range []string{"GOCACHE", "TMPDIR", "HOME", "PATH"} {
		if value := os.Getenv(key); value != "" {
			out = append(out, key+"="+value)
		}
	}
	return out
}

func withParentVariants(path string) []string {
	seen := map[string]struct{}{}
	var out []string
	addAll := func(value string) {
		for _, candidate := range pathVariants(value) {
			if _, ok := seen[candidate]; ok {
				continue
			}
			seen[candidate] = struct{}{}
			out = append(out, candidate)
		}
	}
	addAll(path)
	parent := filepath.Dir(path)
	if parent != "." && parent != path {
		addAll(parent)
	}
	return out
}

func pathVariants(path string) []string {
	path = filepath.Clean(path)
	var out []string
	seen := map[string]struct{}{}
	add := func(candidate string) {
		if candidate == "" {
			return
		}
		candidate = filepath.Clean(candidate)
		if _, ok := seen[candidate]; ok {
			return
		}
		seen[candidate] = struct{}{}
		out = append(out, candidate)
	}
	add(path)
	if resolved, err := filepath.EvalSymlinks(path); err == nil {
		add(resolved)
	}
	return out
}

func LocalOrRemote(ctx context.Context, cfg vfs.Config, configPath string) (vfs.Service, func() error, string, error) {
	if cfg.Mode != "secure" {
		switch cfg.Backend {
		case "", "memory":
			return vfs.NewMemoryService(), func() error { return nil }, "memory", nil
		case "disk":
			svc, err := sandbox.NewDiskService(cfg)
			if err != nil {
				return nil, nil, "", err
			}
			return svc, func() error { return nil }, fmt.Sprintf("disk mount=%s", cfg.MountDir), nil
		default:
			return nil, nil, "", fmt.Errorf("unsupported backend %q", cfg.Backend)
		}
	}

	client, err := StartProcess(ctx, cfg, configPath)
	if err != nil {
		return nil, nil, "", err
	}
	label := fmt.Sprintf("worker(%s)", cfg.Backend)
	if cfg.Backend == "" {
		label = "worker(memory)"
	}
	return client, client.Close, label, nil
}
