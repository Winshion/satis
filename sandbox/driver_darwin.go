package sandbox

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

type darwinDriver struct{}

func (darwinDriver) Name() string { return "darwin" }

func (darwinDriver) LaunchWorker(ctx context.Context, cfg LaunchConfig) (*Session, error) {
	command := cfg.Command
	args := append([]string(nil), cfg.Args...)
	verification := Verify(darwinDriver{}, cfg.SecureMode)

	if cfg.SecureMode {
		sandboxExec, err := exec.LookPath("sandbox-exec")
		if err != nil {
			return nil, fmt.Errorf("sandbox-exec not available: %w", err)
		}
		profile := buildDarwinProfile(cfg.ReadWritePaths, cfg.ReadOnlyPaths)
		args = append([]string{"-p", profile, cfg.Command}, args...)
		command = sandboxExec
		verification.SandboxExecutable = sandboxExec
		verification.ProfileApplied = true
		verification.ReadWritePaths = append([]string(nil), cfg.ReadWritePaths...)
		verification.ReadOnlyPaths = append([]string(nil), cfg.ReadOnlyPaths...)
	}

	cmd := exec.CommandContext(ctx, command, args...)
	cmd.Stderr = os.Stderr
	if len(cfg.ExtraEnv) > 0 {
		cmd.Env = append(os.Environ(), cfg.ExtraEnv...)
	}
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	return &Session{Cmd: cmd, Stdin: stdin, Stdout: stdout, Verification: verification}, nil
}

func buildDarwinProfile(readWritePaths []string, readOnlyPaths []string) string {
	var b strings.Builder
	b.WriteString("(version 1)\n")
	b.WriteString("(deny default)\n")
	b.WriteString("(import \"system.sb\")\n")
	b.WriteString("(allow process-fork)\n")
	b.WriteString("(allow process-exec)\n")
	b.WriteString("(allow file-read*)\n")
	if len(readOnlyPaths) > 0 {
		b.WriteString("(allow file-read*\n")
		for _, path := range readOnlyPaths {
			if path == "" {
				continue
			}
			b.WriteString("    (subpath ")
			b.WriteString(quoteSandboxPath(path))
			b.WriteString(")\n")
		}
		b.WriteString(")\n")
	}
	if len(readWritePaths) > 0 {
		b.WriteString("(allow file-write*\n")
		for _, path := range readWritePaths {
			if path == "" {
				continue
			}
			b.WriteString("    (subpath ")
			b.WriteString(quoteSandboxPath(path))
			b.WriteString(")\n")
		}
		b.WriteString(")\n")
	}
	return b.String()
}

func quoteSandboxPath(path string) string {
	return fmt.Sprintf("%q", filepath.Clean(path))
}
