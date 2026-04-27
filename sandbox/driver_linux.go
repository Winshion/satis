package sandbox

import (
	"context"
	"os"
	"os/exec"
)

type linuxDriver struct{}

func (linuxDriver) Name() string { return "linux" }

func (linuxDriver) LaunchWorker(ctx context.Context, cfg LaunchConfig) (*Session, error) {
	cmd := exec.CommandContext(ctx, cfg.Command, cfg.Args...)
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
	return &Session{Cmd: cmd, Stdin: stdin, Stdout: stdout, Verification: Verify(linuxDriver{}, cfg.SecureMode)}, nil
}
