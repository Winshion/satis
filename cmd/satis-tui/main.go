package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/chzyer/readline"

	"satis/appconfig"
	"satis/invoke"
	"satis/sandbox"
	"satis/satis"
	"satis/tui/app"
	tuiruntime "satis/tui/runtime"
	"satis/vfs"
	"satis/vfsremote"
)

func main() {
	fs := flag.NewFlagSet("satis-tui", flag.ExitOnError)
	configPath := fs.String("config", "vfs.config.json", "path to VFS config JSON")
	invokeConfigPath := fs.String("invoke-config", "", "optional path to invoke-only JSON; overrides embedded \"invoke\" in --config")
	invokeMode := fs.String("invoke-mode", "error", "invoke mode: error|echo|prompt-echo|openai")
	initialCWD := fs.String("initial-cwd", "/", "initial VFS cwd for the TUI session")
	replChunkID := fs.String("chunk-id", "TUI_REPL", "chunk id used by the interactive session")
	fs.Parse(os.Args[1:])

	cfg, embeddedInvoke, systemPortDir, softwareRegistryDir, err := appconfig.LoadVFSAndInvoke(*configPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "failed to load config: %v\n", err)
		os.Exit(1)
	}
	if err := appconfig.EnsureRuntimeDirs(cfg, systemPortDir, softwareRegistryDir); err != nil {
		fmt.Fprintf(os.Stderr, "failed to ensure runtime directories: %v\n", err)
		os.Exit(1)
	}

	invSettings := embeddedInvoke
	resolvedInvokeConfigPath := invoke.ResolveConfigPath(*configPath, *invokeConfigPath)
	if *invokeConfigPath != "" {
		invSettings, err = invoke.LoadFile(*invokeConfigPath)
		if err != nil {
			fmt.Fprintf(os.Stderr, "failed to load invoke config: %v\n", err)
			os.Exit(1)
		}
	} else if loaded, err := invoke.LoadFile(resolvedInvokeConfigPath); err == nil {
		invSettings = loaded
	}

	invoker, err := invoke.InvokerForMode(*invokeMode, invSettings)
	if err != nil {
		fmt.Fprintf(os.Stderr, "invoke: %v\n", err)
		os.Exit(1)
	}
	softwareRegistry := loadSoftwareRegistry(softwareRegistryDir)
	loadPort, err := sandbox.NewSystemPort(systemPortDir)
	if err != nil {
		fmt.Fprintf(os.Stderr, "failed to initialize system_port: %v\n", err)
		os.Exit(1)
	}

	ctx := context.Background()
	vfsSvc, cleanup, err := buildVFS(ctx, cfg, *configPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "failed to initialize VFS backend: %v\n", err)
		os.Exit(1)
	}
	defer func() { _ = cleanup() }()

	executor := &satis.Executor{
		VFS:              vfsSvc,
		Invoker:          invoker,
		SoftwareRegistry: softwareRegistry,
		LoadPort:         loadPort,
		InvokeConfigPath: resolvedInvokeConfigPath,
		InitialCWD:       *initialCWD,
	}

	hostCWD, err := os.Getwd()
	if err != nil {
		fmt.Fprintf(os.Stderr, "failed to get current directory: %v\n", err)
		os.Exit(1)
	}

	runtime, err := tuiruntime.NewSessionAdapter(ctx, executor, *replChunkID, hostCWD)
	if err != nil {
		fmt.Fprintf(os.Stderr, "failed to start TUI session: %v\n", err)
		os.Exit(1)
	}
	runtime.SetSecureMode(cfg.Mode == "secure")
	runtime.SetStdoutMirror(os.Stdout)
	executor.InvokePreview = tuiruntime.NewTerminalPreview(os.Stdout, 6, 120)
	defer func() {
		_ = runtime.Close(ctx)
	}()

	repl := app.New(runtime, os.Stdout)
	fmt.Println("Satis TUI")
	fmt.Println("Type /help for commands.")

	input, err := readline.NewEx(&readline.Config{
		Prompt:          repl.Prompt(),
		InterruptPrompt: "^C",
		EOFPrompt:       "exit",
		AutoComplete:    tuiruntime.NewReadlineCompleter(runtime),
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "failed to initialize readline: %v\n", err)
		os.Exit(1)
	}
	defer input.Close()

	collector := explicitBatchCollector{}
	for {
		input.SetPrompt(collector.prompt(repl.Prompt()))
		line, err := input.Readline()
		if err != nil {
			if errors.Is(err, readline.ErrInterrupt) {
				if collector.cancel() {
					continue
				}
				continue
			}
			if errors.Is(err, io.EOF) {
				fmt.Println()
				return
			}
			fmt.Fprintf(os.Stderr, "input error: %v\n", err)
			os.Exit(1)
		}
		ready, batch := collector.push(line)
		if !ready {
			continue
		}
		if batch != "" {
			input.SaveHistory(batch)
		}
		exit, err := repl.HandleInput(ctx, batch)
		if err != nil {
			fmt.Fprintf(os.Stdout, "error: %v\n", err)
			continue
		}
		if exit {
			fmt.Println()
			return
		}
	}
}

type explicitBatchCollector struct {
	lines []string
}

func (c *explicitBatchCollector) prompt(defaultPrompt string) string {
	if c.active() {
		return ""
	}
	return defaultPrompt
}

func (c *explicitBatchCollector) push(line string) (bool, string) {
	trimmed := strings.TrimSpace(line)
	if !c.active() {
		if !strings.HasPrefix(trimmed, ">>>") || strings.HasSuffix(trimmed, "<<<") {
			return true, line
		}
		c.lines = []string{line}
		return false, ""
	}

	c.lines = append(c.lines, line)
	if !strings.HasSuffix(trimmed, "<<<") {
		return false, ""
	}

	batch := strings.Join(c.lines, "\n")
	c.lines = nil
	return true, batch
}

func (c *explicitBatchCollector) active() bool {
	return len(c.lines) > 0
}

func (c *explicitBatchCollector) cancel() bool {
	if !c.active() {
		return false
	}
	c.lines = nil
	return true
}

func loadSoftwareRegistry(configuredDir string) *satis.SoftwareRegistry {
	dir := strings.TrimSpace(configuredDir)
	if dir == "" {
		cwd, err := os.Getwd()
		if err != nil {
			return nil
		}
		dir = filepath.Join(cwd, "software_registry")
	}
	reg, err := satis.LoadSoftwareRegistry(dir)
	if err != nil {
		fmt.Fprintf(os.Stderr, "failed to load software registry: %v\n", err)
		os.Exit(1)
	}
	satis.SetDefaultSoftwareRegistry(reg)
	return reg
}

func buildVFS(ctx context.Context, cfg vfs.Config, configPath string) (vfs.Service, func() error, error) {
	svc, cleanup, _, err := vfsremote.LocalOrRemote(ctx, cfg, configPath)
	if err != nil {
		return nil, nil, err
	}
	if cleanup == nil {
		cleanup = func() error { return nil }
	}
	return svc, cleanup, nil
}
