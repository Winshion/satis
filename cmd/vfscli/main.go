package main

import (
	"bufio"
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"strconv"
	"strings"

	"satis/vfs"
	"satis/vfsremote"
)

func main() {
	configPath := flag.String("config", "vfs.config.json", "path to VFS config file")
	flag.Parse()

	ctx := context.Background()
	svc, backendLabel, cleanup, err := buildService(ctx, *configPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "failed to initialize VFS service: %v\n", err)
		os.Exit(1)
	}
	defer func() { _ = cleanup() }()

	fmt.Printf("SATI VFS CLI (%s)\n", backendLabel)
	fmt.Println("Type 'help' for commands. Type 'exit' to quit.")

	var currentTxn *vfs.Txn
	scanner := bufio.NewScanner(os.Stdin)

	for {
		fmt.Print("vfs> ")
		if !scanner.Scan() {
			fmt.Println()
			return
		}

		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}

		args, err := splitArgs(line)
		if err != nil {
			fmt.Printf("error: %v\n", err)
			continue
		}
		if len(args) == 0 {
			continue
		}

		switch args[0] {
		case "help":
			printHelp()
		case "exit", "quit":
			return
		case "begin":
			if len(args) != 2 {
				fmt.Println("usage: begin <chunk_id>")
				continue
			}
			if currentTxn != nil {
				fmt.Println("error: transaction already active")
				continue
			}
			txn, err := svc.BeginChunkTxn(ctx, vfs.ChunkID(args[1]))
			if err != nil {
				fmt.Printf("error: %v\n", err)
				continue
			}
			currentTxn = &txn
			fmt.Printf("ok txn=%s chunk=%s\n", txn.ID, txn.ChunkID)
		case "commit":
			if currentTxn == nil {
				fmt.Println("error: no active transaction")
				continue
			}
			if err := svc.CommitChunkTxn(ctx, *currentTxn); err != nil {
				fmt.Printf("error: %v\n", err)
				continue
			}
			fmt.Printf("ok committed txn=%s\n", currentTxn.ID)
			currentTxn = nil
		case "rollback":
			if currentTxn == nil {
				fmt.Println("error: no active transaction")
				continue
			}
			if err := svc.RollbackChunkTxn(ctx, *currentTxn); err != nil {
				fmt.Printf("error: %v\n", err)
				continue
			}
			fmt.Printf("ok rolled_back txn=%s\n", currentTxn.ID)
			currentTxn = nil
		case "create":
			if currentTxn == nil {
				fmt.Println("error: no active transaction")
				continue
			}
			if len(args) < 3 {
				fmt.Println("usage: create <path> <text|binary|directory|generated|ephemeral> [content]")
				continue
			}
			input := vfs.CreateInput{
				VirtualPath:    args[1],
				Kind:           vfs.FileKind(args[2]),
				CreatorChunkID: currentTxn.ChunkID,
			}
			if len(args) >= 4 {
				if input.Kind == vfs.FileKindBinary {
					input.InitialBlob = []byte(args[3])
				} else {
					input.InitialText = args[3]
				}
			}
			ref, err := svc.Create(ctx, *currentTxn, input)
			printFileResult(ref, err)
		case "resolve":
			if len(args) != 2 {
				fmt.Println("usage: resolve <path|file_id>")
				continue
			}
			ref, err := svc.Resolve(ctx, parseResolveArg(args[1]))
			printFileResult(ref, err)
		case "read":
			if currentTxn == nil {
				fmt.Println("error: no active transaction")
				continue
			}
			if len(args) < 2 {
				fmt.Println("usage: read <path|file_id> [start_line] [end_line]")
				continue
			}
			input := vfs.ReadInput{
				Target: parseResolveArg(args[1]),
			}
			if len(args) == 4 {
				start, err1 := strconv.Atoi(args[2])
				end, err2 := strconv.Atoi(args[3])
				if err1 != nil || err2 != nil {
					fmt.Println("error: start_line and end_line must be integers")
					continue
				}
				input.View = vfs.ReadView{
					Mode:      "lines",
					StartLine: start,
					EndLine:   end,
				}
			}
			result, err := svc.Read(ctx, *currentTxn, input)
			if err != nil {
				fmt.Printf("error: %v\n", err)
				continue
			}
			fmt.Printf("ok file_id=%s path=%s generation=%d\n", result.FileRef.FileID, result.FileRef.VirtualPath, result.Generation)
			if result.Text != "" {
				fmt.Printf("text=%q\n", result.Text)
			} else if result.Blob != nil {
				fmt.Printf("blob=%q\n", string(result.Blob))
			}
		case "write-replace":
			if currentTxn == nil {
				fmt.Println("error: no active transaction")
				continue
			}
			if len(args) != 3 {
				fmt.Println("usage: write-replace <path|file_id> <content>")
				continue
			}
			input := vfs.WriteInput{
				Target:        parseResolveArg(args[1]),
				WriterChunkID: currentTxn.ChunkID,
				Mode:          vfs.WriteModeReplaceFull,
				Text:          args[2],
				Blob:          []byte(args[2]),
			}
			ref, err := svc.Write(ctx, *currentTxn, input)
			printFileResult(ref, err)
		case "append":
			if currentTxn == nil {
				fmt.Println("error: no active transaction")
				continue
			}
			if len(args) != 3 {
				fmt.Println("usage: append <path|file_id> <content>")
				continue
			}
			ref, err := svc.Write(ctx, *currentTxn, vfs.WriteInput{
				Target:        parseResolveArg(args[1]),
				WriterChunkID: currentTxn.ChunkID,
				Mode:          vfs.WriteModeAppend,
				Text:          args[2],
			})
			printFileResult(ref, err)
		case "patch":
			if currentTxn == nil {
				fmt.Println("error: no active transaction")
				continue
			}
			if len(args) != 4 {
				fmt.Println("usage: patch <path|file_id> <old_text> <new_text>")
				continue
			}
			ref, err := svc.Write(ctx, *currentTxn, vfs.WriteInput{
				Target:        parseResolveArg(args[1]),
				WriterChunkID: currentTxn.ChunkID,
				Mode:          vfs.WriteModePatchText,
				PatchText: &vfs.PatchTextInput{
					OldText: args[2],
					NewText: args[3],
				},
			})
			printFileResult(ref, err)
		case "rename":
			if currentTxn == nil {
				fmt.Println("error: no active transaction")
				continue
			}
			if len(args) != 3 {
				fmt.Println("usage: rename <path|file_id> <new_path>")
				continue
			}
			ref, err := svc.Rename(ctx, *currentTxn, vfs.RenameInput{
				Target:         parseResolveArg(args[1]),
				NewVirtualPath: args[2],
				ChunkID:        currentTxn.ChunkID,
			})
			printFileResult(ref, err)
		case "delete":
			if currentTxn == nil {
				fmt.Println("error: no active transaction")
				continue
			}
			if len(args) < 2 || len(args) > 3 {
				fmt.Println("usage: delete <path|file_id> [reason]")
				continue
			}
			reason := ""
			if len(args) == 3 {
				reason = args[2]
			}
			ref, err := svc.Delete(ctx, *currentTxn, vfs.DeleteInput{
				Target:  parseResolveArg(args[1]),
				ChunkID: currentTxn.ChunkID,
				Reason:  reason,
			})
			printFileResult(ref, err)
		default:
			fmt.Printf("error: unknown command %q\n", args[0])
		}
	}
}

func buildService(ctx context.Context, configPath string) (vfs.Service, string, func() error, error) {
	cfg, err := vfs.LoadConfig(configPath)
	if err != nil {
		return nil, "", nil, err
	}
	svc, cleanup, label, err := vfsremote.LocalOrRemote(ctx, cfg, configPath)
	if err != nil {
		return nil, "", nil, err
	}
	if cleanup == nil {
		cleanup = func() error { return nil }
	}
	return svc, label, cleanup, nil
}

func printHelp() {
	fmt.Println("Commands:")
	fmt.Println("  begin <chunk_id>")
	fmt.Println("  commit")
	fmt.Println("  rollback")
	fmt.Println("  create <path> <kind> [content]")
	fmt.Println("  resolve <path|file_id>")
	fmt.Println("  read <path|file_id> [start_line] [end_line]")
	fmt.Println("  write-replace <path|file_id> <content>")
	fmt.Println("  append <path|file_id> <content>")
	fmt.Println("  patch <path|file_id> <old_text> <new_text>")
	fmt.Println("  rename <path|file_id> <new_path>")
	fmt.Println("  delete <path|file_id> [reason]")
	fmt.Println("  help")
	fmt.Println("  exit")
	fmt.Println("")
	fmt.Println("Notes:")
	fmt.Println("  - This CLI uses the in-memory VFS implementation.")
	fmt.Println("  - All state is lost when the process exits.")
	fmt.Println("  - Commands that mutate state require an active chunk transaction.")
	fmt.Println("  - Quote arguments containing spaces.")
}

func printFileResult(ref vfs.FileRef, err error) {
	if err != nil {
		fmt.Printf("error: %v\n", err)
		return
	}
	fmt.Printf("ok file_id=%s path=%s generation=%d state=%s\n",
		ref.FileID,
		ref.VirtualPath,
		ref.CurrentGeneration,
		ref.DeleteState,
	)
}

func parseResolveArg(arg string) vfs.ResolveInput {
	if strings.HasPrefix(arg, "file_") {
		return vfs.ResolveInput{FileID: vfs.FileID(arg)}
	}
	return vfs.ResolveInput{VirtualPath: arg}
}

func splitArgs(line string) ([]string, error) {
	var args []string
	var current strings.Builder
	inQuote := false
	escaped := false

	for _, r := range line {
		switch {
		case escaped:
			current.WriteRune(r)
			escaped = false
		case r == '\\':
			escaped = true
		case r == '"':
			inQuote = !inQuote
		case !inQuote && (r == ' ' || r == '\t'):
			if current.Len() > 0 {
				args = append(args, current.String())
				current.Reset()
			}
		default:
			current.WriteRune(r)
		}
	}

	if escaped || inQuote {
		return nil, errors.New("unterminated quoted argument")
	}
	if current.Len() > 0 {
		args = append(args, current.String())
	}

	return args, nil
}
