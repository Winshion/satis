package main

import (
	"context"
	"flag"
	"fmt"
	"os"

	"satis/appconfig"
	"satis/sandbox"
	"satis/vfs"
	"satis/vfsremote"
)

func main() {
	fs := flag.NewFlagSet("satis-worker", flag.ExitOnError)
	configPath := fs.String("config", "vfs.config.json", "path to VFS config JSON")
	fs.Parse(os.Args[1:])

	cfg, _, _, _, err := appconfig.LoadVFSAndInvoke(*configPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "failed to load config: %v\n", err)
		os.Exit(1)
	}
	if err := appconfig.EnsureRuntimeDirs(cfg, "", ""); err != nil {
		fmt.Fprintf(os.Stderr, "failed to ensure runtime directories: %v\n", err)
		os.Exit(1)
	}

	service, err := buildLocalVFS(cfg)
	if err != nil {
		fmt.Fprintf(os.Stderr, "failed to initialize VFS backend: %v\n", err)
		os.Exit(1)
	}

	server := vfsremote.NewServer(service)
	if err := server.Serve(context.Background(), os.Stdin, os.Stdout); err != nil {
		fmt.Fprintf(os.Stderr, "worker rpc loop failed: %v\n", err)
		os.Exit(1)
	}
}

func buildLocalVFS(cfg vfs.Config) (vfs.Service, error) {
	switch cfg.Backend {
	case "", "memory":
		return vfs.NewMemoryService(), nil
	case "disk":
		return sandbox.NewDiskService(cfg)
	default:
		return nil, fmt.Errorf("unsupported backend %q", cfg.Backend)
	}
}
