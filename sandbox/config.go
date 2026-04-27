package sandbox

import "context"

type LaunchConfig struct {
	Command        string
	Args           []string
	SecureMode     bool
	ReadWritePaths []string
	ReadOnlyPaths  []string
	ExtraEnv       []string
}

type Driver interface {
	Name() string
	LaunchWorker(ctx context.Context, cfg LaunchConfig) (*Session, error)
}
