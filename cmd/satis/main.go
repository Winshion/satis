package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"satis/appconfig"
	"satis/invoke"
	"satis/sandbox"
	"satis/satis"
	"satis/vfs"
	"satis/vfsremote"
)

func main() {
	args := os.Args[1:]
	if len(args) > 0 {
		switch args[0] {
		case "run":
			runCommand(args[1:])
			return
		case "inspect":
			inspectCommand(args[1:])
			return
		}
	}

	// Backward-compatible default: treat root flags as run mode.
	runCommand(args)
}

func runCommand(args []string) {
	fs := flag.NewFlagSet("run", flag.ExitOnError)
	configPath := fs.String("config", "vfs.config.json", "path to VFS config JSON (optional \"invoke\" object for OpenAI-compatible API)")
	invokeConfigPath := fs.String("invoke-config", "", "optional path to invoke-only JSON; overrides embedded \"invoke\" in --config")
	filePath := fs.String("file", "", "path to .satis file")
	invokeMode := fs.String("invoke-mode", "error", "invoke mode: error|echo|prompt-echo|openai")
	showEvents := fs.Bool("show-events", false, "print persisted VFS events after execution (disk backend only)")
	showVersions := fs.Bool("show-versions", false, "print persisted versions for output objects after execution (disk backend only)")
	fs.Parse(args)

	if *filePath == "" {
		fmt.Fprintln(os.Stderr, "missing required flag: --file")
		os.Exit(1)
	}

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

	vfsSvc, backendLabel, cleanup, err := buildVFS(context.Background(), cfg, *configPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "failed to initialize VFS backend: %v\n", err)
		os.Exit(1)
	}
	defer func() { _ = cleanup() }()

	chunk, err := satis.ParseFile(*filePath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "failed to parse satis file: %v\n", err)
		os.Exit(1)
	}
	if err := satis.Validate(chunk); err != nil {
		fmt.Fprintf(os.Stderr, "failed to validate satis file: %v\n", err)
		os.Exit(1)
	}

	exec := &satis.Executor{
		VFS:              vfsSvc,
		Invoker:          invoker,
		SoftwareRegistry: softwareRegistry,
		LoadPort:         loadPort,
		InvokeConfigPath: resolvedInvokeConfigPath,
	}

	result, err := exec.Execute(context.Background(), chunk)
	if err != nil {
		fmt.Fprintf(os.Stderr, "execution failed: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("Executed chunk %s\n", result.ChunkID)
	fmt.Printf("Backend: %s\n", backendLabel)
	fmt.Printf("Source: %s\n", *filePath)
	fmt.Printf("Invoke mode: %s\n", *invokeMode)
	printResult(result)

	if cfg.Backend == "disk" {
		if *showEvents {
			if err := printEventsForChunk(cfg.StateDir, vfs.ChunkID(result.ChunkID)); err != nil {
				fmt.Fprintf(os.Stderr, "failed to print events: %v\n", err)
				os.Exit(1)
			}
		}
		if *showVersions {
			if err := printVersions(cfg.StateDir, result); err != nil {
				fmt.Fprintf(os.Stderr, "failed to print versions: %v\n", err)
				os.Exit(1)
			}
		}
	}
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

func inspectCommand(args []string) {
	fs := flag.NewFlagSet("inspect", flag.ExitOnError)
	configPath := fs.String("config", "vfs.config.json", "path to VFS config file")
	pathArg := fs.String("path", "", "virtual path to inspect")
	fileIDArg := fs.String("file-id", "", "file id to inspect")
	showEvents := fs.Bool("show-events", false, "include file events")
	showVersions := fs.Bool("show-versions", false, "include file versions")
	showContent := fs.Bool("show-content", false, "include current content summary")
	jsonOutput := fs.Bool("json", false, "print JSON output")
	fs.Parse(args)

	if (*pathArg == "" && *fileIDArg == "") || (*pathArg != "" && *fileIDArg != "") {
		fmt.Fprintln(os.Stderr, "inspect requires exactly one of --path or --file-id")
		os.Exit(1)
	}

	cfg, err := vfs.LoadConfig(*configPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "failed to load VFS config: %v\n", err)
		os.Exit(1)
	}
	if cfg.Backend != "disk" {
		fmt.Fprintln(os.Stderr, "inspect currently requires disk backend")
		os.Exit(1)
	}

	snapshot, err := sandbox.LoadRuntimeSnapshot(cfg)
	if err != nil {
		fmt.Fprintf(os.Stderr, "failed to load runtime snapshot: %v\n", err)
		os.Exit(1)
	}

	var meta *vfs.FileMeta
	if *pathArg != "" {
		meta = snapshot.FindFileByPath(normalizeVirtualPath(*pathArg))
	} else {
		meta = snapshot.FindFileByID(vfs.FileID(*fileIDArg))
	}
	if meta == nil {
		fmt.Fprintln(os.Stderr, "inspect target not found")
		os.Exit(1)
	}

	payload := inspectPayload{
		Backend:  fmt.Sprintf("disk mount=%s", cfg.MountDir),
		StateDir: cfg.StateDir,
		File:     *meta,
	}
	if *showEvents {
		payload.Events = snapshot.FilterEventsByFileID(meta.FileID)
	}
	if *showVersions {
		versions, err := sandbox.LoadVersionEntries(cfg, meta.FileID)
		if err != nil {
			fmt.Fprintf(os.Stderr, "failed to load versions: %v\n", err)
			os.Exit(1)
		}
		payload.Versions = versions
	}
	if *showContent {
		content, err := sandbox.LoadCurrentContent(cfg, *meta)
		if err != nil {
			fmt.Fprintf(os.Stderr, "failed to load content: %v\n", err)
			os.Exit(1)
		}
		payload.Content = summarizeContent(meta.Kind, content)
	}

	if *jsonOutput {
		data, _ := json.MarshalIndent(payload, "", "  ")
		fmt.Println(string(data))
		return
	}

	printInspectPayload(payload)
}

func buildVFS(ctx context.Context, cfg vfs.Config, configPath string) (vfs.Service, string, func() error, error) {
	svc, cleanup, label, err := vfsremote.LocalOrRemote(ctx, cfg, configPath)
	if err != nil {
		return nil, "", nil, err
	}
	if cleanup == nil {
		cleanup = func() error { return nil }
	}
	return svc, label, cleanup, nil
}

func printResult(result *satis.ExecutionResult) {
	if result == nil {
		return
	}

	objectNames := sortedKeys(result.Objects)
	valueNames := sortedKeys(result.Values)

	if len(objectNames) > 0 {
		fmt.Println("Objects:")
		for _, name := range objectNames {
			ref := result.Objects[name]
			data, _ := json.Marshal(ref)
			fmt.Printf("  %s = %s\n", name, string(data))
		}
	}

	if len(valueNames) > 0 {
		fmt.Println("Values:")
		for _, name := range valueNames {
			data, _ := json.Marshal(result.Values[name])
			fmt.Printf("  %s = %s\n", name, string(data))
		}
	}

	if len(result.Notes) > 0 {
		fmt.Println("Notes:")
		for _, note := range result.Notes {
			fmt.Printf("  %s\n", note)
		}
	}
}

func printEventsForChunk(stateDir string, chunkID vfs.ChunkID) error {
	cfg := vfs.Config{MountDir: "", StateDir: stateDir}
	snapshot, err := sandbox.LoadRuntimeSnapshot(cfg)
	if err != nil {
		return err
	}

	events := snapshot.FilterEventsByChunkID(chunkID)
	fmt.Println("Events:")
	for _, event := range events {
		data, _ := json.Marshal(event)
		fmt.Printf("  %s\n", string(data))
	}
	return nil
}

func printVersions(stateDir string, result *satis.ExecutionResult) error {
	if result == nil {
		return nil
	}

	objectNames := sortedKeys(result.Objects)
	if len(objectNames) == 0 {
		return nil
	}

	fmt.Println("Versions:")
	for _, name := range objectNames {
		ref := result.Objects[name]
		versions, err := sandbox.LoadVersionEntries(vfs.Config{StateDir: stateDir}, ref.FileID)
		if err != nil {
			return err
		}
		fmt.Printf("  %s (%s)\n", name, ref.FileID)
		for _, version := range versions {
			data, _ := json.Marshal(version)
			fmt.Printf("    %s\n", string(data))
		}
	}
	return nil
}

func sortedKeys[T any](m map[string]T) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

type inspectPayload struct {
	Backend  string             `json:"backend"`
	StateDir string             `json:"state_dir"`
	File     vfs.FileMeta       `json:"file"`
	Events   []vfs.Event        `json:"events,omitempty"`
	Versions []vfs.VersionEntry `json:"versions,omitempty"`
	Content  *contentSummary    `json:"content,omitempty"`
}

type contentSummary struct {
	Kind      string `json:"kind"`
	ByteSize  int    `json:"byte_size"`
	Preview   string `json:"preview,omitempty"`
	Truncated bool   `json:"truncated,omitempty"`
}

func printInspectPayload(payload inspectPayload) {
	fmt.Println("Inspect:")
	fmt.Printf("  Backend: %s\n", payload.Backend)
	fmt.Printf("  State dir: %s\n", payload.StateDir)
	fmt.Printf("  File ID: %s\n", payload.File.FileID)
	fmt.Printf("  Virtual path: %s\n", payload.File.VirtualPath)
	fmt.Printf("  Kind: %s\n", payload.File.Kind)
	fmt.Printf("  Generation: %d\n", payload.File.CurrentGeneration)
	fmt.Printf("  State: %s\n", payload.File.DeleteState)
	fmt.Printf("  Size: %d\n", payload.File.Size)
	fmt.Printf("  Creator chunk: %s\n", payload.File.CreatorChunkID)
	fmt.Printf("  Last writer chunk: %s\n", payload.File.LastWriterChunkID)
	if len(payload.Versions) > 0 {
		fmt.Println("  Versions:")
		for _, version := range payload.Versions {
			data, _ := json.Marshal(version)
			fmt.Printf("    %s\n", string(data))
		}
	}
	if len(payload.Events) > 0 {
		fmt.Println("  Events:")
		for _, event := range payload.Events {
			data, _ := json.Marshal(event)
			fmt.Printf("    %s\n", string(data))
		}
	}
	if payload.Content != nil {
		fmt.Printf("  Content kind: %s\n", payload.Content.Kind)
		fmt.Printf("  Content bytes: %d\n", payload.Content.ByteSize)
		if payload.Content.Preview != "" {
			fmt.Printf("  Content preview: %q\n", payload.Content.Preview)
		}
		if payload.Content.Truncated {
			fmt.Println("  Content preview is truncated")
		}
	}
}

func normalizeVirtualPath(path string) string {
	path = strings.TrimSpace(path)
	if path == "" {
		return "/"
	}
	if !strings.HasPrefix(path, "/") {
		path = "/" + path
	}
	cleaned := filepath.Clean(path)
	if cleaned == "." {
		return "/"
	}
	if !strings.HasPrefix(cleaned, "/") {
		cleaned = "/" + cleaned
	}
	return cleaned
}

func summarizeContent(kind vfs.FileKind, data []byte) *contentSummary {
	if data == nil && kind == vfs.FileKindDirectory {
		return &contentSummary{
			Kind:     string(kind),
			ByteSize: 0,
		}
	}

	summary := &contentSummary{
		Kind:     string(kind),
		ByteSize: len(data),
	}

	switch kind {
	case vfs.FileKindBinary:
		return summary
	default:
		text := string(data)
		if len(text) > 240 {
			summary.Preview = text[:240]
			summary.Truncated = true
		} else {
			summary.Preview = text
		}
		return summary
	}
}
