package satis

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/mattn/go-runewidth"
	"golang.org/x/term"
	"satis/llmconfig"
	"satis/vfs"
)

// Invoker abstracts the model call used by Invoke statements.
type Invoker interface {
	Invoke(ctx context.Context, prompt string, input string) (string, error)
}

// ConversationMessage captures a single chat turn for stateful invoke flows.
type ConversationMessage struct {
	Role    string
	Content string
}

// ConversationInvoker is optional. When an Invoker implements it, Invoke
// statements with "in @conversation" send the full message history instead of a
// single prompt/input pair.
type ConversationInvoker interface {
	InvokeMessages(ctx context.Context, messages []ConversationMessage) (string, error)
}

// StreamingInvoker is optional. When an Invoker implements it, Invoke without
// "as @var" may stream tokens to Executor.Stdout for better console UX.
type StreamingInvoker interface {
	InvokeStream(ctx context.Context, prompt, input string, w io.Writer) (string, error)
}

type RoutedInvoker interface {
	InvokeWithProvider(ctx context.Context, provider string, prompt string, input string) (string, error)
}

type RoutedConversationInvoker interface {
	InvokeMessagesWithProvider(ctx context.Context, provider string, messages []ConversationMessage) (string, error)
}

type RoutedStreamingInvoker interface {
	InvokeStreamWithProvider(ctx context.Context, provider string, prompt string, input string, w io.Writer) (string, error)
}

type InvokeConfigReloader interface {
	ReloadInvokeConfig(cfg *llmconfig.Config) error
}

// InvokePreviewer is optional. When configured, Invoke statements that bind to
// a variable may still stream a short terminal preview while collecting the
// full response for later binding.
type InvokePreviewer interface {
	io.Writer
	Begin() error
	End() error
}

// InvokerFunc adapts a function to the Invoker interface.
type InvokerFunc func(ctx context.Context, prompt string, input string) (string, error)

func (f InvokerFunc) Invoke(ctx context.Context, prompt string, input string) (string, error) {
	return f(ctx, prompt, input)
}

// Executor executes parsed and validated chunks against a VFS service.
type Executor struct {
	VFS                 vfs.Service
	Invoker             Invoker
	SoftwareRegistry    *SoftwareRegistry
	LoadPort            LoadPort
	InvokePreview       InvokePreviewer
	BatchScheduler      BatchScheduler
	InvokeConfigPath    string
	InputBindings       map[string]RuntimeBinding
	InputObjectBindings map[string]vfs.FileRef
	InputValueBindings  map[string]string
	InitialCWD          string
	Stdout              io.Writer
}

// RuntimeBinding is the serializable form of one runtime variable.
type RuntimeBinding struct {
	Kind         string                `json:"kind"`
	Text         string                `json:"text,omitempty"`
	Texts        []string              `json:"texts,omitempty"`
	Object       *vfs.FileRef          `json:"object,omitempty"`
	Objects      []vfs.FileRef         `json:"objects,omitempty"`
	Conversation []ConversationMessage `json:"conversation,omitempty"`
}

// ExecutionResult captures the final environment after a successful chunk run.
type ExecutionResult struct {
	ChunkID string
	Meta    map[string]string
	Objects map[string]vfs.FileRef
	Values  map[string]string
	Vars    map[string]RuntimeBinding
	Notes   []string
}

type runtimeValueKind string

const (
	runtimeValueText         runtimeValueKind = "text"
	runtimeValueTextList     runtimeValueKind = "text_list"
	runtimeValueObject       runtimeValueKind = "object"
	runtimeValueObjectList   runtimeValueKind = "object_list"
	runtimeValueConversation runtimeValueKind = "conversation"
)

type runtimeValue struct {
	kind         runtimeValueKind
	text         string
	texts        []string
	object       vfs.FileRef
	objects      []vfs.FileRef
	conversation []ConversationMessage
}

var indexedPathPlaceholderPattern = regexp.MustCompile(`\{i(?::0?(\d+)d)?\}`)

// Execute parses, validates, and runs a chunk inside a chunk-scoped VFS txn.
func (e *Executor) Execute(ctx context.Context, chunk *Chunk) (*ExecutionResult, error) {
	if e == nil || e.VFS == nil {
		return nil, fmt.Errorf("satis execute error: missing VFS service")
	}
	if chunk == nil {
		return nil, fmt.Errorf("satis execute error: chunk is nil")
	}
	if err := Validate(chunk); err != nil {
		return nil, err
	}

	chunkID := chunk.Meta["chunk_id"]
	state, err := newSessionState(ctx, e, chunkID)
	if err != nil {
		return nil, err
	}
	defer func() {
		_ = state.abort(ctx)
	}()
	if err := state.executeInstructions(ctx, chunk.Instructions); err != nil {
		return nil, err
	}
	if err := state.finish(ctx); err != nil {
		return nil, err
	}

	result := &ExecutionResult{
		ChunkID: chunkID,
		Meta:    cloneMeta(chunk.Meta),
		Objects: make(map[string]vfs.FileRef),
		Values:  make(map[string]string),
		Vars:    make(map[string]RuntimeBinding),
		Notes:   append([]string(nil), state.notes...),
	}

	for name, value := range state.env {
		result.Vars[name] = runtimeValueToBinding(value)
		switch value.kind {
		case runtimeValueObject:
			result.Objects[name] = value.object
		case runtimeValueText:
			result.Values[name] = value.text
		case runtimeValueTextList:
			result.Values[name] = strings.Join(value.texts, "\n")
		}
	}

	return result, nil
}

// ParseValidateExecute is a convenience entry point for source-driven execution.
func (e *Executor) ParseValidateExecute(ctx context.Context, src string) (*ExecutionResult, error) {
	chunk, err := Parse(src)
	if err != nil {
		return nil, err
	}
	return e.Execute(ctx, chunk)
}

func (e *Executor) executeInstruction(ctx context.Context, txn vfs.Txn, env map[string]runtimeValue, conversations map[string][]ConversationMessage, cwd *string, loadCWD *string, softwareCWD *string, inst Instruction) (string, error) {
	switch stmt := inst.(type) {
	case CdStmt:
		nextPath := resolvePath(currentCWD(cwd), stmt.Path)
		if _, err := e.VFS.ListDir(ctx, txn, nextPath); err != nil {
			return "", lineExecError(stmt.Line, err)
		}
		*cwd = nextPath
		return "", nil

	case PwdStmt:
		if _, err := fmt.Fprintln(e.stdoutWriter(), currentCWD(cwd)); err != nil {
			return "", lineExecError(stmt.Line, err)
		}
		return "", nil

	case LsStmt:
		targetPath := currentCWD(cwd)
		if strings.TrimSpace(stmt.Path) != "" {
			targetPath = resolvePath(targetPath, stmt.Path)
		}
		entries, err := e.VFS.ListDir(ctx, txn, targetPath)
		if err != nil {
			return "", lineExecError(stmt.Line, err)
		}
		if err := writeLsListing(e.stdoutWriter(), entries); err != nil {
			return "", lineExecError(stmt.Line, err)
		}
		return "", nil

	case LoadPwdStmt:
		if _, err := fmt.Fprintln(e.stdoutWriter(), currentLoadCWD(loadCWD)); err != nil {
			return "", lineExecError(stmt.Line, err)
		}
		return "", nil

	case LoadCdStmt:
		if e.LoadPort == nil {
			return "", lineExecError(stmt.Line, fmt.Errorf("load is not configured"))
		}
		nextPath := resolvePath(currentLoadCWD(loadCWD), stmt.Path)
		entry, err := e.LoadPort.Stat(ctx, nextPath)
		if err != nil {
			return "", lineExecError(stmt.Line, err)
		}
		if !entry.IsDir {
			return "", lineExecError(stmt.Line, fmt.Errorf("load path %q is not a directory", nextPath))
		}
		*loadCWD = nextPath
		return "", nil

	case LoadLsStmt:
		if e.LoadPort == nil {
			return "", lineExecError(stmt.Line, fmt.Errorf("load is not configured"))
		}
		entries, err := e.resolveLoadListing(ctx, currentLoadCWD(loadCWD), stmt.Path)
		if err != nil {
			return "", lineExecError(stmt.Line, err)
		}
		if err := writeLsListing(e.stdoutWriter(), loadEntriesToDirEntries(entries)); err != nil {
			return "", lineExecError(stmt.Line, err)
		}
		return "", nil

	case SoftwareManageStmt:
		text, err := e.executeSoftwareManage(stmt, softwareCWD)
		if err != nil {
			return "", lineExecError(stmt.Line, err)
		}
		if stmt.Action == "cd" {
			return "", nil
		}
		if stmt.OutputVar != "" {
			env[stmt.OutputVar] = runtimeValue{kind: runtimeValueText, text: text}
		} else {
			if _, err := fmt.Fprintln(e.stdoutWriter(), text); err != nil {
				return "", lineExecError(stmt.Line, err)
			}
		}
		return "", nil

	case LoadStmt:
		if e.LoadPort == nil {
			return "", lineExecError(stmt.Line, fmt.Errorf("load is not configured"))
		}
		sources, err := e.expandLoadSources(ctx, currentLoadCWD(loadCWD), stmt.Sources)
		if err != nil {
			return "", lineExecError(stmt.Line, err)
		}
		targetNames := make(map[string]string, len(sources))
		type loadedSource struct {
			entry      LoadEntry
			targetName string
			text       string
		}
		loaded := make([]loadedSource, 0, len(sources))
		for _, source := range sources {
			if source.IsDir {
				return "", lineExecError(stmt.Line, fmt.Errorf("load source %q is a directory", source.VirtualPath))
			}
			targetName := path.Base(source.VirtualPath)
			if existing, ok := targetNames[targetName]; ok && existing != source.VirtualPath {
				return "", lineExecError(stmt.Line, fmt.Errorf("duplicate target basename %q from %q and %q", targetName, existing, source.VirtualPath))
			}
			text, err := e.LoadPort.ReadText(ctx, source.VirtualPath)
			if err != nil {
				return "", lineExecError(stmt.Line, err)
			}
			targetPath := resolvePath(currentCWD(cwd), targetName)
			if _, err := e.resolveDirectoryPathInTxn(ctx, txn, targetPath); err == nil {
				return "", lineExecError(stmt.Line, fmt.Errorf("load target %q is an existing directory", targetPath))
			} else if !errors.Is(err, vfs.ErrFileNotFound) {
				return "", lineExecError(stmt.Line, err)
			}
			targetNames[targetName] = source.VirtualPath
			loaded = append(loaded, loadedSource{
				entry:      source,
				targetName: targetName,
				text:       text,
			})
		}
		refs := make([]vfs.FileRef, 0, len(loaded))
		for _, source := range loaded {
			ref, _, err := e.writeSingleText(ctx, txn, currentCWD(cwd), source.targetName, source.text, stmt.Line)
			if err != nil {
				return "", err
			}
			refs = append(refs, ref)
		}
		if stmt.OutputVar != "" && len(refs) > 0 {
			if len(refs) == 1 {
				env[stmt.OutputVar] = runtimeValue{kind: runtimeValueObject, object: refs[0]}
			} else {
				env[stmt.OutputVar] = runtimeValue{kind: runtimeValueObjectList, objects: refs}
			}
		}
		return "", nil

	case ResolveStmt:
		if e != nil && e.InputObjectBindings != nil {
			if ref, ok := e.InputObjectBindings[stmt.OutputVar]; ok {
				if stmt.TargetKind == ResolveTargetFolder && ref.Kind != vfs.FileKindDirectory {
					return "", fmt.Errorf("satis execute error on line %d: expected folder, got %s", stmt.Line, ref.Kind)
				}
				if stmt.TargetKind == ResolveTargetFile && ref.Kind == vfs.FileKindDirectory {
					return "", fmt.Errorf("satis execute error on line %d: expected file, got folder", stmt.Line)
				}
				env[stmt.OutputVar] = runtimeValue{kind: runtimeValueObject, object: ref}
				return "", nil
			}
		}
		if stmt.TargetKind == ResolveTargetFile {
			paths, err := e.expandPathPatterns(ctx, currentCWD(cwd), []string{stmt.Path})
			if err != nil {
				return "", lineExecError(stmt.Line, err)
			}
			refs := make([]vfs.FileRef, 0, len(paths))
			for _, path := range paths {
				ref, err := e.resolveFilePathInTxn(ctx, txn, path)
				if err != nil {
					return "", err
				}
				refs = append(refs, ref)
			}
			if len(refs) == 1 {
				env[stmt.OutputVar] = runtimeValue{kind: runtimeValueObject, object: refs[0]}
			} else {
				env[stmt.OutputVar] = runtimeValue{kind: runtimeValueObjectList, objects: refs}
			}
			return "", nil
		}
		ref, err := e.resolveDirectoryPathInTxn(ctx, txn, resolvePath(currentCWD(cwd), stmt.Path))
		if err != nil {
			return "", err
		}
		env[stmt.OutputVar] = runtimeValue{kind: runtimeValueObject, object: ref}
		return "", nil

	case ReadStmt:
		refs, err := e.resolveReadTargets(ctx, txn, env, currentCWD(cwd), stmt)
		if err != nil {
			return "", lineExecError(stmt.Line, err)
		}

		texts := make([]string, 0, len(refs))
		for _, ref := range refs {
			input := vfs.ReadInput{
				Target: vfs.ResolveInput{FileID: ref.FileID},
			}
			if stmt.StartLine > 0 || stmt.EndLine > 0 {
				input.View = vfs.ReadView{
					Mode:      "lines",
					StartLine: stmt.StartLine,
					EndLine:   stmt.EndLine,
				}
			}
			result, err := e.VFS.Read(ctx, txn, input)
			if err != nil {
				return "", err
			}
			text := result.Text
			if result.Blob != nil {
				text = string(result.Blob)
			}
			texts = append(texts, text)
		}

		if len(texts) == 1 {
			env[stmt.OutputVar] = runtimeValue{kind: runtimeValueText, text: texts[0]}
		} else {
			env[stmt.OutputVar] = runtimeValue{kind: runtimeValueTextList, texts: texts}
		}
		return "", nil

	case CreateStmt:
		refs := make([]vfs.FileRef, 0, len(stmt.Paths))
		for _, path := range stmt.Paths {
			targetPath := resolvePath(currentCWD(cwd), path)
			ref, err := e.createPath(ctx, txn, targetPath, stmt)
			if err != nil {
				return "", lineExecError(stmt.Line, err)
			}
			refs = append(refs, ref)
		}
		if stmt.OutputVar != "" && len(refs) > 0 {
			if len(refs) == 1 {
				env[stmt.OutputVar] = runtimeValue{kind: runtimeValueObject, object: refs[0]}
			} else {
				env[stmt.OutputVar] = runtimeValue{kind: runtimeValueObjectList, objects: refs}
			}
		}
		return "", nil

	case PrintStmt:
		text, err := e.resolvePrintableValue(env, stmt.Value)
		if err != nil {
			return "", lineExecError(stmt.Line, err)
		}
		if _, err := fmt.Fprintln(e.stdoutWriter(), text); err != nil {
			return "", lineExecError(stmt.Line, err)
		}
		return "", nil

	case ConcatStmt:
		text, err := resolveConcatValues(env, stmt.Values)
		if err != nil {
			return "", lineExecError(stmt.Line, err)
		}
		env[stmt.OutputVar] = runtimeValue{kind: runtimeValueText, text: text}
		return "", nil

	case WriteStmt:
		selected, err := resolveValueSelection(env, stmt.Value, true)
		if err != nil {
			return "", lineExecError(stmt.Line, err)
		}
		switch selected.kind {
		case runtimeValueText:
			var (
				ref  vfs.FileRef
				note string
			)
			if stmt.ObjectVar != "" {
				ref, note, err = e.writeSingleTextToObject(ctx, txn, env, stmt.ObjectVar, selected.text, stmt.Line)
			} else {
				ref, note, err = e.writeSingleText(ctx, txn, currentCWD(cwd), stmt.Path, selected.text, stmt.Line)
			}
			if err != nil {
				return "", err
			}
			if stmt.OutputVar != "" {
				env[stmt.OutputVar] = runtimeValue{kind: runtimeValueObject, object: ref}
			}
			return note, nil
		case runtimeValueTextList:
			if stmt.ObjectVar != "" {
				return "", lineExecError(stmt.Line, fmt.Errorf("write text list requires a path target, not an object variable"))
			}
			refs, note, err := e.writeTextList(ctx, txn, currentCWD(cwd), stmt.Path, selected.texts, stmt.Line)
			if err != nil {
				return "", err
			}
			if stmt.OutputVar != "" && len(refs) > 0 {
				env[stmt.OutputVar] = runtimeValue{kind: runtimeValueObject, object: refs[0]}
				if len(refs) > 1 {
					env[stmt.OutputVar+"_list"] = runtimeValue{kind: runtimeValueObjectList, objects: refs}
				}
			}
			return note, nil
		default:
			return "", lineExecError(stmt.Line, fmt.Errorf("write value must be text or text list"))
		}

	case CopyStmt:
		source, err := resolveObjectVar(env, stmt.ObjectVar)
		if err != nil {
			return "", lineExecError(stmt.Line, err)
		}
		targetPath := resolvePath(currentCWD(cwd), stmt.Path)

		readResult, err := e.VFS.Read(ctx, txn, vfs.ReadInput{
			Target: vfs.ResolveInput{FileID: source.FileID},
		})
		if err != nil {
			return "", err
		}
		env[stmt.ObjectVar] = runtimeValue{kind: runtimeValueObject, object: readResult.FileRef}

		ref, resolveErr := e.resolvePathOfKindInTxn(ctx, txn, targetPath, source.Kind)
		note := ""
		switch {
		case resolveErr == nil:
			before := ref
			writeInput := vfs.WriteInput{
				Target:        vfs.ResolveInput{FileID: ref.FileID},
				WriterChunkID: txn.ChunkID,
				Mode:          vfs.WriteModeReplaceFull,
			}
			if readResult.Blob != nil {
				writeInput.Blob = readResult.Blob
			} else {
				writeInput.Text = readResult.Text
			}
			ref, err = e.VFS.Write(ctx, txn, writeInput)
			if err != nil {
				return "", err
			}
			if ref.CurrentGeneration == before.CurrentGeneration {
				note = fmt.Sprintf("line %d: copy to %s unchanged", stmt.Line, targetPath)
			}
		case resolveErr == vfs.ErrFileNotFound:
			createInput := vfs.CreateInput{
				VirtualPath:    targetPath,
				CreatorChunkID: txn.ChunkID,
			}
			if readResult.Blob != nil {
				createInput.Kind = vfs.FileKindBinary
				createInput.InitialBlob = readResult.Blob
			} else {
				createInput.Kind = vfs.FileKindText
				createInput.InitialText = readResult.Text
			}
			ref, err = e.VFS.Create(ctx, txn, createInput)
			if err != nil {
				return "", err
			}
		default:
			return "", resolveErr
		}

		if stmt.OutputVar != "" {
			env[stmt.OutputVar] = runtimeValue{kind: runtimeValueObject, object: ref}
		}
		return note, nil

	case MoveStmt:
		source, err := resolveObjectVar(env, stmt.ObjectVar)
		if err != nil {
			return "", lineExecError(stmt.Line, err)
		}
		targetPath := resolvePath(currentCWD(cwd), stmt.Path)
		if targetPath == source.VirtualPath {
			if stmt.OutputVar != "" {
				env[stmt.OutputVar] = runtimeValue{kind: runtimeValueObject, object: source}
			}
			return fmt.Sprintf("line %d: move to %s unchanged", stmt.Line, targetPath), nil
		}

		readResult, err := e.VFS.Read(ctx, txn, vfs.ReadInput{
			Target: vfs.ResolveInput{FileID: source.FileID},
		})
		if err != nil {
			return "", err
		}
		env[stmt.ObjectVar] = runtimeValue{kind: runtimeValueObject, object: readResult.FileRef}

		ref, resolveErr := e.resolvePathOfKindInTxn(ctx, txn, targetPath, source.Kind)
		note := ""
		switch {
		case resolveErr == nil:
			before := ref
			writeInput := vfs.WriteInput{
				Target:        vfs.ResolveInput{FileID: ref.FileID},
				WriterChunkID: txn.ChunkID,
				Mode:          vfs.WriteModeReplaceFull,
			}
			if readResult.Blob != nil {
				writeInput.Blob = readResult.Blob
			} else {
				writeInput.Text = readResult.Text
			}
			ref, err = e.VFS.Write(ctx, txn, writeInput)
			if err != nil {
				return "", err
			}
			if ref.CurrentGeneration == before.CurrentGeneration {
				note = fmt.Sprintf("line %d: move target %s unchanged", stmt.Line, targetPath)
			}
		case resolveErr == vfs.ErrFileNotFound:
			createInput := vfs.CreateInput{
				VirtualPath:    targetPath,
				CreatorChunkID: txn.ChunkID,
			}
			if readResult.Blob != nil {
				createInput.Kind = vfs.FileKindBinary
				createInput.InitialBlob = readResult.Blob
			} else {
				createInput.Kind = vfs.FileKindText
				createInput.InitialText = readResult.Text
			}
			ref, err = e.VFS.Create(ctx, txn, createInput)
			if err != nil {
				return "", err
			}
		default:
			return "", resolveErr
		}

		_, err = e.VFS.Delete(ctx, txn, vfs.DeleteInput{
			Target:  vfs.ResolveInput{FileID: source.FileID},
			ChunkID: txn.ChunkID,
		})
		if err != nil {
			return "", err
		}

		if stmt.OutputVar != "" {
			env[stmt.OutputVar] = runtimeValue{kind: runtimeValueObject, object: ref}
		}
		return note, nil

	case PatchStmt:
		ref, err := resolveObjectVar(env, stmt.ObjectVar)
		if err != nil {
			return "", lineExecError(stmt.Line, err)
		}
		before := ref
		ref, err = e.VFS.Write(ctx, txn, vfs.WriteInput{
			Target:        vfs.ResolveInput{FileID: ref.FileID},
			WriterChunkID: txn.ChunkID,
			Mode:          vfs.WriteModePatchText,
			PatchText: &vfs.PatchTextInput{
				OldText: stmt.OldText,
				NewText: stmt.NewText,
			},
		})
		if err != nil {
			return "", err
		}
		if stmt.OutputVar != "" {
			env[stmt.OutputVar] = runtimeValue{kind: runtimeValueObject, object: ref}
		}
		if ref.CurrentGeneration == before.CurrentGeneration {
			return fmt.Sprintf("line %d: patch on %s unchanged", stmt.Line, ref.VirtualPath), nil
		}
		return "", nil

	case DeleteStmt:
		if stmt.DeleteAll {
			for _, source := range stmt.Sources {
				if source.ObjectVar != "" {
					ref, err := resolveObjectVar(env, source.ObjectVar)
					if err != nil {
						return "", lineExecError(stmt.Line, err)
					}
					updated, err := e.deleteTree(ctx, txn, ref, true)
					if err != nil {
						return "", lineExecError(stmt.Line, err)
					}
					env[source.ObjectVar] = runtimeValue{kind: runtimeValueObject, object: updated}
					continue
				}
				if source.Path == "" {
					return "", lineExecError(stmt.Line, fmt.Errorf("delete all requires a source"))
				}
				paths, err := e.expandPathPatterns(ctx, currentCWD(cwd), []string{source.Path})
				if err != nil {
					return "", lineExecError(stmt.Line, err)
				}
				deleteSelf := source.Path != "." && source.Path != "./"
				for _, p := range paths {
					if err := e.deleteAllAtPath(ctx, txn, p, deleteSelf); err != nil {
						return "", lineExecError(stmt.Line, err)
					}
				}
			}
			return "", nil
		}
		for _, source := range stmt.Sources {
			if source.ObjectVar != "" {
				ref, err := resolveObjectVar(env, source.ObjectVar)
				if err != nil {
					return "", lineExecError(stmt.Line, err)
				}
				if err := ensureDeleteKindMatches(stmt.TargetKind, ref); err != nil {
					return "", lineExecError(stmt.Line, err)
				}
				if stmt.TargetKind == DeleteTargetFolder {
					ref, err = e.deleteTree(ctx, txn, ref, true)
					if err != nil {
						return "", lineExecError(stmt.Line, err)
					}
				} else {
					ref, err = e.VFS.Delete(ctx, txn, vfs.DeleteInput{
						Target:  vfs.ResolveInput{FileID: ref.FileID},
						ChunkID: txn.ChunkID,
					})
					if err != nil {
						return "", err
					}
				}
				env[source.ObjectVar] = runtimeValue{kind: runtimeValueObject, object: ref}
				continue
			}
			if source.Path == "" {
				return "", lineExecError(stmt.Line, fmt.Errorf("delete requires a source"))
			}
			paths, err := e.expandPathPatterns(ctx, currentCWD(cwd), []string{source.Path})
			if err != nil {
				return "", lineExecError(stmt.Line, err)
			}
			for _, p := range paths {
				ref, err := e.resolveDeleteTargetPathInTxn(ctx, txn, p, stmt.TargetKind)
				if err != nil {
					return "", lineExecError(stmt.Line, err)
				}
				if stmt.TargetKind == DeleteTargetFolder {
					if _, err := e.deleteTree(ctx, txn, ref, true); err != nil {
						return "", lineExecError(stmt.Line, err)
					}
				} else {
					if _, err := e.VFS.Delete(ctx, txn, vfs.DeleteInput{
						Target:  vfs.ResolveInput{FileID: ref.FileID},
						ChunkID: txn.ChunkID,
					}); err != nil {
						return "", err
					}
				}
			}
		}
		return "", nil

	case RenameStmt:
		ref, err := resolveObjectVar(env, stmt.ObjectVar)
		if err != nil {
			return "", lineExecError(stmt.Line, err)
		}
		ref, err = e.VFS.Rename(ctx, txn, vfs.RenameInput{
			Target:         vfs.ResolveInput{FileID: ref.FileID},
			NewVirtualPath: resolvePath(currentCWD(cwd), stmt.NewPath),
			ChunkID:        txn.ChunkID,
		})
		if err != nil {
			return "", err
		}
		if stmt.OutputVar != "" {
			env[stmt.OutputVar] = runtimeValue{kind: runtimeValueObject, object: ref}
		}
		return "", nil

	case InvokeStmt:
		if e.Invoker == nil {
			return "", fmt.Errorf("satis execute error on line %d: missing invoker", stmt.Line)
		}
		prompt, err := resolveInvokePrompt(env, stmt)
		if err != nil {
			return "", lineExecError(stmt.Line, err)
		}
		var text string
		if stmt.HasInput {
			text, err = resolveTextValue(env, stmt.Input)
			if err != nil {
				return "", lineExecError(stmt.Line, err)
			}
		}
		if stmt.ConversationVar != "" {
			output, err := e.invokeConversation(ctx, stmt, prompt, text, conversations)
			if err != nil {
				return "", err
			}
			env[stmt.ConversationVar] = runtimeValue{
				kind:         runtimeValueConversation,
				conversation: append([]ConversationMessage(nil), conversations[stmt.ConversationVar]...),
			}
			if stmt.OutputVar == "" {
				if _, err := fmt.Fprintln(e.stdoutWriter(), output); err != nil {
					return "", lineExecError(stmt.Line, err)
				}
			} else {
				env[stmt.OutputVar] = runtimeValue{kind: runtimeValueText, text: output}
			}
			return "", nil
		}
		var output string
		if stmt.OutputVar == "" {
			w := e.stdoutWriter()
			if rsi, ok := e.Invoker.(RoutedStreamingInvoker); ok && w != nil && stmt.Provider != "" {
				output, err = rsi.InvokeStreamWithProvider(ctx, stmt.Provider, prompt, text, w)
				if err != nil {
					return "", err
				}
				if _, err := fmt.Fprintln(w); err != nil {
					return "", lineExecError(stmt.Line, err)
				}
			} else if si, ok := e.Invoker.(StreamingInvoker); ok && w != nil {
				output, err = si.InvokeStream(ctx, prompt, text, w)
				if err != nil {
					return "", err
				}
				if _, err := fmt.Fprintln(w); err != nil {
					return "", lineExecError(stmt.Line, err)
				}
			} else {
				output, err = e.invokeText(ctx, stmt.Provider, prompt, text)
				if err != nil {
					return "", err
				}
				if _, err := fmt.Fprintln(w, output); err != nil {
					return "", lineExecError(stmt.Line, err)
				}
			}
		} else {
			if rsi, ok := e.Invoker.(RoutedStreamingInvoker); ok && e.InvokePreview != nil && stmt.Provider != "" {
				if err := e.InvokePreview.Begin(); err != nil {
					return "", lineExecError(stmt.Line, err)
				}
				output, err = rsi.InvokeStreamWithProvider(ctx, stmt.Provider, prompt, text, e.InvokePreview)
				endErr := e.InvokePreview.End()
				if err != nil {
					return "", err
				}
				if endErr != nil {
					return "", lineExecError(stmt.Line, endErr)
				}
			} else if si, ok := e.Invoker.(StreamingInvoker); ok && e.InvokePreview != nil {
				if err := e.InvokePreview.Begin(); err != nil {
					return "", lineExecError(stmt.Line, err)
				}
				output, err = si.InvokeStream(ctx, prompt, text, e.InvokePreview)
				endErr := e.InvokePreview.End()
				if err != nil {
					return "", err
				}
				if endErr != nil {
					return "", lineExecError(stmt.Line, endErr)
				}
			} else {
				output, err = e.invokeText(ctx, stmt.Provider, prompt, text)
				if err != nil {
					return "", err
				}
			}
			env[stmt.OutputVar] = runtimeValue{kind: runtimeValueText, text: output}
		}
		return "", nil

	case BatchInvokeStmt:
		if e.Invoker == nil {
			return "", fmt.Errorf("satis execute error on line %d: missing invoker", stmt.Line)
		}
		prompt, err := resolvePromptText(env, stmt.Prompt)
		if err != nil {
			return "", lineExecError(stmt.Line, err)
		}
		inputTexts, err := resolveTextList(env, stmt.InputList)
		if err != nil {
			return "", lineExecError(stmt.Line, err)
		}
		results, err := e.InvokeBatchWithProvider(ctx, stmt.Provider, prompt, inputTexts)
		if err != nil {
			return "", lineExecError(stmt.Line, err)
		}
		if stmt.OutputMode == "separate_files" {
			env[stmt.OutputVar] = runtimeValue{kind: runtimeValueTextList, texts: append([]string(nil), results...)}
			for i, result := range results {
				varName := fmt.Sprintf("%s_%04d", stmt.OutputVar, i)
				env[varName] = runtimeValue{kind: runtimeValueText, text: result}
			}
		} else {
			joined := strings.Join(results, "\n---\n")
			env[stmt.OutputVar] = runtimeValue{kind: runtimeValueText, text: joined}
		}
		return "", nil

	case InvokeProviderStmt:
		text, err := e.executeInvokeProviderManage(stmt)
		if err != nil {
			return "", err
		}
		if stmt.OutputVar != "" {
			env[stmt.OutputVar] = runtimeValue{kind: runtimeValueText, text: text}
			return "", nil
		}
		if text != "" {
			if _, err := fmt.Fprintln(e.stdoutWriter(), text); err != nil {
				return "", lineExecError(stmt.Line, err)
			}
		}
		return "", nil

	case SoftwareCallStmt:
		output, err := e.executeSoftwareCall(ctx, env, stmt)
		if err != nil {
			return "", lineExecError(stmt.Line, err)
		}
		if stmt.OutputVar != "" {
			env[stmt.OutputVar] = runtimeValue{kind: runtimeValueText, text: output}
		}
		return "", nil

	default:
		return "", fmt.Errorf("satis execute error: unsupported instruction %T", inst)
	}
}

func (e *Executor) executeSoftwareManage(stmt SoftwareManageStmt, softwareCWD *string) (string, error) {
	reg := e.softwareRegistry()
	if reg == nil {
		return "", fmt.Errorf("software registry is not configured")
	}
	current := "/"
	if softwareCWD != nil && *softwareCWD != "" {
		current = *softwareCWD
	}
	switch stmt.Action {
	case "pwd":
		return normalizeRegistryPath(current), nil
	case "cd":
		target, err := reg.ResolveFolderPath(current, stmt.Arg)
		if err != nil {
			return "", err
		}
		if softwareCWD != nil {
			*softwareCWD = target
		}
		return "", nil
	case "ls":
		folder, ok := reg.Folder(current)
		if !ok {
			return "", fmt.Errorf("software folder %q not found", current)
		}
		lines := make([]string, 0, len(folder.Entries)+1)
		lines = append(lines, "folder "+folder.Path)
		for _, entry := range folder.Entries {
			line := entry.Kind + " " + entry.Name
			if entry.Description != "" {
				line += " - " + entry.Description
			}
			lines = append(lines, line)
		}
		return strings.Join(lines, "\n"), nil
	case "find":
		matches := reg.FindByPrefix(stmt.Arg)
		if len(matches) == 0 {
			return "", fmt.Errorf("software with prefix %q not found", stmt.Arg)
		}
		lines := make([]string, 0, len(matches))
		for _, spec := range matches {
			lines = append(lines, spec.Name+" -> "+spec.RegistryPath)
		}
		return strings.Join(lines, "\n"), nil
	case "describe":
		spec, ok := reg.Lookup(stmt.Arg)
		if !ok {
			return "", fmt.Errorf("unknown software %q", stmt.Arg)
		}
		if spec.Description != "" {
			return spec.Description, nil
		}
		return spec.Name, nil
	case "functions":
		spec, ok := reg.Lookup(stmt.Arg)
		if !ok {
			return "", fmt.Errorf("unknown software %q", stmt.Arg)
		}
		names := make([]string, 0, len(spec.Functions))
		for name := range spec.Functions {
			names = append(names, name)
		}
		sort.Strings(names)
		lines := make([]string, 0, len(names))
		for _, name := range names {
			fn := spec.Functions[name]
			line := name
			if len(fn.Args) > 0 {
				line += " " + strings.Join(fn.Args, " ")
			}
			lines = append(lines, line)
		}
		return strings.Join(lines, "\n"), nil
	case "readskill":
		spec, ok := reg.Lookup(stmt.Arg)
		if !ok {
			return "", fmt.Errorf("unknown software %q", stmt.Arg)
		}
		data, err := os.ReadFile(spec.SkillPath)
		if err != nil {
			return "", fmt.Errorf("read SKILL.md for software %q: %w", stmt.Arg, err)
		}
		return string(data), nil
	case "refresh":
		reloaded, report, err := reg.Refresh()
		if err != nil {
			return "", err
		}
		if e != nil {
			e.SoftwareRegistry = reloaded
		}
		SetDefaultSoftwareRegistry(reloaded)
		return fmt.Sprintf("refreshed folders=%d recognized=%d skipped=%d", report.RefreshedFolders, report.RecognizedSoftware, report.SkippedSoftware), nil
	default:
		return "", fmt.Errorf("unsupported software subcommand %q", stmt.Action)
	}
}

func (e *Executor) softwareRegistry() *SoftwareRegistry {
	if e != nil && e.SoftwareRegistry != nil {
		return e.SoftwareRegistry
	}
	return DefaultSoftwareRegistry()
}

func (e *Executor) executeSoftwareCall(ctx context.Context, env map[string]runtimeValue, stmt SoftwareCallStmt) (string, error) {
	reg := e.softwareRegistry()
	if reg == nil {
		return "", fmt.Errorf("unknown software %q", stmt.SoftwareName)
	}
	spec, ok := reg.Lookup(stmt.SoftwareName)
	if !ok {
		return "", fmt.Errorf("unknown software %q", stmt.SoftwareName)
	}
	if _, ok := spec.SupportsFunction(stmt.FunctionName); !ok {
		return "", fmt.Errorf("software %q does not support function %q", stmt.SoftwareName, stmt.FunctionName)
	}

	var cmd *exec.Cmd
	switch spec.Runner.Kind {
	case "python_script":
		cmdName := strings.TrimSpace(spec.Runner.Command)
		if cmdName == "" {
			cmdName = "python3"
		}
		scriptPath := strings.TrimSpace(spec.Runner.Script)
		if scriptPath == "" {
			return "", fmt.Errorf("软件错误")
		}
		if !filepath.IsAbs(scriptPath) {
			scriptPath = filepath.Join(spec.Dir, scriptPath)
		}
		args := []string{scriptPath, stmt.FunctionName}
		for _, flag := range stmt.Flags {
			text, err := resolveSoftwareFlagValue(env, flag.Value)
			if err != nil {
				return "", err
			}
			args = append(args, flag.Name, text)
		}
		cmd = exec.CommandContext(ctx, cmdName, args...)
		if workdir := strings.TrimSpace(spec.Locator.Path); workdir != "" {
			cmd.Dir = workdir
		}
	case "binary":
		cmdName := strings.TrimSpace(spec.Runner.Command)
		if cmdName == "" {
			cmdName = strings.TrimSpace(spec.Locator.Path)
		}
		if cmdName == "" {
			return "", fmt.Errorf("软件错误")
		}
		args := []string{stmt.FunctionName}
		for _, flag := range stmt.Flags {
			text, err := resolveSoftwareFlagValue(env, flag.Value)
			if err != nil {
				return "", err
			}
			args = append(args, flag.Name, text)
		}
		cmd = exec.CommandContext(ctx, cmdName, args...)
	default:
		return "", fmt.Errorf("软件错误")
	}

	output, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("软件错误")
	}
	return strings.TrimSpace(string(output)), nil
}

func resolveSoftwareFlagValue(env map[string]runtimeValue, value Value) (string, error) {
	switch value.Kind {
	case ValueKindString:
		return value.Text, nil
	case ValueKindVariable:
		selected, err := resolveValueSelection(env, value, false)
		if err != nil {
			return "", err
		}
		if selected.kind != runtimeValueText {
			return "", fmt.Errorf("variable %s must be text", value.Text)
		}
		return selected.text, nil
	default:
		return "", fmt.Errorf("software flag value must be text")
	}
}

// deleteDirContents removes every immediate listing under dirPath, recursing into subdirectories.
// When deleteSelf is true, it then deletes the directory object at dirPath if one exists in the
// namespace; if dirPath is only a synthetic prefix (no pathIndex entry), that final delete is skipped.
func (e *Executor) deleteDirContents(ctx context.Context, txn vfs.Txn, dirPath string, deleteSelf bool) (vfs.FileRef, error) {
	entries, err := e.VFS.ListDir(ctx, txn, dirPath)
	if err != nil {
		return vfs.FileRef{}, err
	}
	for _, entry := range entries {
		if entry.Kind == vfs.FileKindDirectory {
			if _, err := e.deleteDirContents(ctx, txn, entry.VirtualPath, true); err != nil {
				return vfs.FileRef{}, err
			}
			continue
		}
		ref, err := e.resolvePathOfKindInTxn(ctx, txn, entry.VirtualPath, entry.Kind)
		if err != nil {
			return vfs.FileRef{}, err
		}
		if _, err := e.VFS.Delete(ctx, txn, vfs.DeleteInput{
			Target:  vfs.ResolveInput{FileID: ref.FileID},
			ChunkID: txn.ChunkID,
		}); err != nil {
			return vfs.FileRef{}, err
		}
	}
	if !deleteSelf {
		return vfs.FileRef{}, nil
	}
	ref, err := e.resolveDirectoryPathInTxn(ctx, txn, dirPath)
	if err != nil {
		if errors.Is(err, vfs.ErrFileNotFound) {
			return vfs.FileRef{}, nil
		}
		return vfs.FileRef{}, err
	}
	if ref.Kind != vfs.FileKindDirectory {
		return vfs.FileRef{}, fmt.Errorf("expected directory at %q", dirPath)
	}
	return e.VFS.Delete(ctx, txn, vfs.DeleteInput{
		Target:  vfs.ResolveInput{FileID: ref.FileID},
		ChunkID: txn.ChunkID,
	})
}

func (e *Executor) deleteTree(ctx context.Context, txn vfs.Txn, ref vfs.FileRef, deleteSelf bool) (vfs.FileRef, error) {
	if ref.Kind != vfs.FileKindDirectory {
		return e.VFS.Delete(ctx, txn, vfs.DeleteInput{
			Target:  vfs.ResolveInput{FileID: ref.FileID},
			ChunkID: txn.ChunkID,
		})
	}
	return e.deleteDirContents(ctx, txn, ref.VirtualPath, deleteSelf)
}

func (e *Executor) deleteAllAtPath(ctx context.Context, txn vfs.Txn, path string, deleteSelf bool) error {
	fileRefs, err := e.resolveAllFilePathsInTxn(ctx, txn, path)
	if err != nil {
		return err
	}
	dirRef, dirErr := e.resolveDirectoryPathInTxn(ctx, txn, path)
	if dirErr != nil && !errors.Is(dirErr, vfs.ErrFileNotFound) {
		return dirErr
	}
	deletedSomething := false
	for _, ref := range fileRefs {
		if _, err := e.VFS.Delete(ctx, txn, vfs.DeleteInput{
			Target:  vfs.ResolveInput{FileID: ref.FileID},
			ChunkID: txn.ChunkID,
		}); err != nil {
			return err
		}
		deletedSomething = true
	}
	if dirErr == nil {
		if _, err := e.deleteDirContents(ctx, txn, dirRef.VirtualPath, deleteSelf); err != nil {
			return err
		}
		deletedSomething = true
	} else if !deletedSomething {
		entries, listErr := e.VFS.ListDir(ctx, txn, path)
		if listErr == nil && len(entries) > 0 {
			if _, err := e.deleteDirContents(ctx, txn, path, deleteSelf); err != nil {
				return err
			}
			deletedSomething = true
		} else if listErr != nil && !errors.Is(listErr, vfs.ErrFileNotFound) {
			return listErr
		}
	}
	if !deletedSomething {
		return vfs.ErrFileNotFound
	}
	return nil
}

func (e *Executor) invokeConversation(ctx context.Context, stmt InvokeStmt, prompt string, input string, conversations map[string][]ConversationMessage) (string, error) {
	if conversations == nil {
		return "", lineExecError(stmt.Line, fmt.Errorf("conversation state is unavailable"))
	}
	history := append([]ConversationMessage(nil), conversations[stmt.ConversationVar]...)
	history = append(history, buildConversationTurn(prompt, input)...)
	var output string
	var err error
	if stmt.Provider != "" {
		rci, ok := e.Invoker.(RoutedConversationInvoker)
		if !ok {
			return "", lineExecError(stmt.Line, fmt.Errorf("invoker does not support routed invoke conversations"))
		}
		output, err = rci.InvokeMessagesWithProvider(ctx, stmt.Provider, history)
	} else {
		ci, ok := e.Invoker.(ConversationInvoker)
		if !ok {
			return "", lineExecError(stmt.Line, fmt.Errorf("invoker does not support invoke conversations"))
		}
		output, err = ci.InvokeMessages(ctx, history)
	}
	if err != nil {
		return "", err
	}
	history = append(history, ConversationMessage{
		Role:    "assistant",
		Content: output,
	})
	conversations[stmt.ConversationVar] = history
	return output, nil
}

func (e *Executor) invokeText(ctx context.Context, provider string, prompt string, input string) (string, error) {
	if provider == "" {
		return e.Invoker.Invoke(ctx, prompt, input)
	}
	ri, ok := e.Invoker.(RoutedInvoker)
	if !ok {
		return "", fmt.Errorf("invoker does not support invoke provider routing")
	}
	return ri.InvokeWithProvider(ctx, provider, prompt, input)
}

func (e *Executor) InvokeBatchWithProvider(ctx context.Context, provider string, prompt string, inputs []string) ([]string, error) {
	if e == nil || e.Invoker == nil {
		return nil, fmt.Errorf("satis execute error: missing invoker")
	}
	if len(inputs) == 0 {
		return []string{}, nil
	}

	tasks := make([]BatchTask, 0, len(inputs))
	for i, input := range inputs {
		idx := i
		text := input
		tasks = append(tasks, BatchTask{
			ID:        fmt.Sprintf("invoke_simultaneous_%04d", idx),
			Kind:      BatchTaskKindSimultaneousInvoke,
			Priority:  50,
			CostLevel: estimateLLMCostLevel(len(text), len(prompt)),
			Meta: map[string]string{
				"input_bytes": fmt.Sprintf("%d", len(text)),
			},
			ExecFn: func(runCtx context.Context) (any, error) {
				output, err := e.invokeText(runCtx, provider, prompt, text)
				if err != nil {
					return nil, fmt.Errorf("batch item %d: %w", idx, err)
				}
				return output, nil
			},
		})
	}

	results := e.schedulerOrDefault().Schedule(ctx, tasks)
	outputs := make([]string, 0, len(results))
	for _, result := range results {
		if result.Err != nil {
			return nil, result.Err
		}
		text, ok := result.Value.(string)
		if !ok {
			return nil, fmt.Errorf("batch task %q returned non-string result %T", result.Task.ID, result.Value)
		}
		outputs = append(outputs, text)
	}
	return outputs, nil
}

func (e *Executor) executeInvokeProviderManage(stmt InvokeProviderStmt) (string, error) {
	if e == nil || strings.TrimSpace(e.InvokeConfigPath) == "" {
		return "", fmt.Errorf("invoke provider management requires invoke config path")
	}
	cfg, err := llmconfig.LoadFile(e.InvokeConfigPath)
	if err != nil {
		if os.IsNotExist(err) {
			cfg = &llmconfig.Config{Providers: map[string]llmconfig.ProviderConfig{}}
		} else {
			return "", err
		}
	}
	switch stmt.Action {
	case "ls":
		names := cfg.ProviderNames()
		if len(names) == 0 {
			return "no invoke providers configured", nil
		}
		lines := make([]string, 0, len(names))
		defaultName := cfg.DefaultProvider()
		for _, name := range names {
			provider := cfg.Providers[name]
			line := name
			if name == defaultName {
				line += " (default)"
			}
			if provider.Model != "" {
				line += " model=" + provider.Model
			}
			if provider.BaseURL != "" {
				line += " base_url=" + provider.BaseURL
			}
			lines = append(lines, line)
		}
		return strings.Join(lines, "\n"), nil
	case "current":
		name := cfg.DefaultProvider()
		if name == "" {
			return "", fmt.Errorf("default invoke provider is not configured")
		}
		if _, ok := cfg.Providers[name]; !ok {
			return "", fmt.Errorf("default invoke provider %q is not configured", name)
		}
		return name, nil
	case "show":
		provider, ok := cfg.Providers[stmt.Name]
		if !ok {
			return "", fmt.Errorf("unknown invoke provider %q", stmt.Name)
		}
		payload := struct {
			Name     string                   `json:"name"`
			Provider llmconfig.ProviderConfig `json:"provider"`
		}{Name: stmt.Name, Provider: provider}
		data, err := json.MarshalIndent(payload, "", "  ")
		if err != nil {
			return "", err
		}
		return string(data), nil
	case "set-default":
		if _, ok := cfg.Providers[stmt.Name]; !ok {
			return "", fmt.Errorf("unknown invoke provider %q", stmt.Name)
		}
		cfg.SetDefaultProvider(stmt.Name)
	case "remove":
		if _, ok := cfg.Providers[stmt.Name]; !ok {
			return "", fmt.Errorf("unknown invoke provider %q", stmt.Name)
		}
		delete(cfg.Providers, stmt.Name)
		if cfg.DefaultProvider() == stmt.Name {
			cfg.SetDefaultProvider("")
		}
	case "upsert":
		provider, err := buildProviderConfig(stmt.Flags)
		if err != nil {
			return "", err
		}
		if cfg.Providers == nil {
			cfg.Providers = map[string]llmconfig.ProviderConfig{}
		}
		cfg.Providers[stmt.Name] = provider
		if cfg.DefaultProvider() == "" {
			cfg.SetDefaultProvider(stmt.Name)
		}
	default:
		return "", fmt.Errorf("unsupported invoke provider subcommand %q", stmt.Action)
	}
	if stmt.Action == "ls" || stmt.Action == "current" || stmt.Action == "show" {
		return "", nil
	}
	if err := cfg.SaveFile(e.InvokeConfigPath); err != nil {
		return "", err
	}
	if reloader, ok := e.Invoker.(InvokeConfigReloader); ok {
		if err := reloader.ReloadInvokeConfig(cfg); err != nil {
			return "", err
		}
	}
	switch stmt.Action {
	case "set-default":
		return "default invoke provider set to " + stmt.Name, nil
	case "remove":
		return "removed invoke provider " + stmt.Name, nil
	case "upsert":
		return "upserted invoke provider " + stmt.Name, nil
	}
	return "", nil
}

func buildProviderConfig(flags []SoftwareFlag) (llmconfig.ProviderConfig, error) {
	var provider llmconfig.ProviderConfig
	seenBaseURL := false
	seenModel := false
	for _, flag := range flags {
		value := flag.Value.Text
		switch flag.Name {
		case "--base-url":
			provider.BaseURL = value
			seenBaseURL = true
		case "--api-key":
			provider.APIKey = value
		case "--api-key-env":
			provider.APIKeyEnv = value
		case "--model":
			provider.Model = value
			seenModel = true
		case "--timeout-seconds":
			n, err := strconv.Atoi(value)
			if err != nil {
				return llmconfig.ProviderConfig{}, fmt.Errorf("invalid --timeout-seconds value %q", value)
			}
			provider.TimeoutSeconds = n
		case "--temperature":
			f, err := strconv.ParseFloat(value, 64)
			if err != nil {
				return llmconfig.ProviderConfig{}, fmt.Errorf("invalid --temperature value %q", value)
			}
			provider.Temperature = &f
		case "--max-tokens":
			n, err := strconv.Atoi(value)
			if err != nil {
				return llmconfig.ProviderConfig{}, fmt.Errorf("invalid --max-tokens value %q", value)
			}
			provider.MaxTokens = &n
		default:
			return llmconfig.ProviderConfig{}, fmt.Errorf("unsupported provider flag %q", flag.Name)
		}
	}
	if !seenBaseURL || strings.TrimSpace(provider.BaseURL) == "" {
		return llmconfig.ProviderConfig{}, fmt.Errorf("invoke provider upsert requires --base-url")
	}
	if !seenModel || strings.TrimSpace(provider.Model) == "" {
		return llmconfig.ProviderConfig{}, fmt.Errorf("invoke provider upsert requires --model")
	}
	return provider, nil
}

func runtimeValueToBinding(value runtimeValue) RuntimeBinding {
	out := RuntimeBinding{Kind: string(value.kind)}
	switch value.kind {
	case runtimeValueText:
		out.Text = value.text
	case runtimeValueTextList:
		out.Texts = append([]string(nil), value.texts...)
	case runtimeValueObject:
		ref := value.object
		out.Object = &ref
	case runtimeValueObjectList:
		out.Objects = append([]vfs.FileRef(nil), value.objects...)
	case runtimeValueConversation:
		out.Conversation = append([]ConversationMessage(nil), value.conversation...)
	}
	return out
}

func bindingToRuntimeValue(binding RuntimeBinding) (runtimeValue, error) {
	switch strings.ToLower(strings.TrimSpace(binding.Kind)) {
	case string(runtimeValueText):
		return runtimeValue{kind: runtimeValueText, text: binding.Text}, nil
	case string(runtimeValueTextList):
		return runtimeValue{kind: runtimeValueTextList, texts: append([]string(nil), binding.Texts...)}, nil
	case string(runtimeValueObject):
		if binding.Object == nil {
			return runtimeValue{}, fmt.Errorf("object binding requires object ref")
		}
		return runtimeValue{kind: runtimeValueObject, object: *binding.Object}, nil
	case string(runtimeValueObjectList):
		return runtimeValue{kind: runtimeValueObjectList, objects: append([]vfs.FileRef(nil), binding.Objects...)}, nil
	case string(runtimeValueConversation):
		return runtimeValue{kind: runtimeValueConversation, conversation: append([]ConversationMessage(nil), binding.Conversation...)}, nil
	default:
		return runtimeValue{}, fmt.Errorf("unsupported runtime binding kind %q", binding.Kind)
	}
}

func buildConversationTurn(prompt string, input string) []ConversationMessage {
	prompt = strings.TrimSpace(prompt)
	input = strings.TrimSpace(input)
	if input == "" {
		return []ConversationMessage{{Role: "user", Content: prompt}}
	}
	return []ConversationMessage{
		{Role: "system", Content: input},
		{Role: "user", Content: prompt},
	}
}

func resolveInvokePrompt(env map[string]runtimeValue, stmt InvokeStmt) (string, error) {
	promptValues := stmt.PromptParts
	if len(promptValues) == 0 {
		promptValues = []Value{stmt.Prompt}
	}
	var b strings.Builder
	for _, value := range promptValues {
		prompt, err := resolvePromptText(env, value)
		if err != nil {
			return "", err
		}
		b.WriteString(prompt)
	}
	return b.String(), nil
}

func resolveTextValue(env map[string]runtimeValue, value Value) (string, error) {
	selected, err := resolveValueSelection(env, value, false)
	if err != nil {
		return "", err
	}
	if selected.kind != runtimeValueText {
		return "", fmt.Errorf("variable %s is not a text value", value.Text)
	}
	return selected.text, nil
}

func resolvePromptText(env map[string]runtimeValue, value Value) (string, error) {
	selected, err := resolveValueSelection(env, value, true)
	if err != nil {
		return "", err
	}
	switch selected.kind {
	case runtimeValueText:
		return selected.text, nil
	case runtimeValueTextList:
		return strings.Join(selected.texts, "\n"), nil
	case runtimeValueObject:
		return "", fmt.Errorf("prompt variable %s must resolve to text; %s is an object reference, please Read it into text first", value.Text, value.Text)
	case runtimeValueObjectList:
		return "", fmt.Errorf("prompt variable %s must resolve to text; %s is an object list, please Read it into text first", value.Text, value.Text)
	default:
		return "", fmt.Errorf("prompt variable %s must resolve to text", value.Text)
	}
}

func resolveConcatValues(env map[string]runtimeValue, values []Value) (string, error) {
	var b strings.Builder
	for _, value := range values {
		text, err := resolveTextValue(env, value)
		if err != nil {
			return "", err
		}
		b.WriteString(text)
	}
	return b.String(), nil
}

func (e *Executor) resolvePrintableValue(env map[string]runtimeValue, value Value) (string, error) {
	selected, err := resolveValueSelection(env, value, true)
	if err != nil {
		return "", err
	}
	switch selected.kind {
	case runtimeValueText:
		return selected.text, nil
	case runtimeValueTextList:
		return strings.Join(selected.texts, "\n"), nil
	case runtimeValueObject:
		return formatFileRef(selected.object), nil
	case runtimeValueObjectList:
		lines := make([]string, 0, len(selected.objects))
		for _, obj := range selected.objects {
			lines = append(lines, formatFileRef(obj))
		}
		return strings.Join(lines, "\n"), nil
	case runtimeValueConversation:
		lines := make([]string, 0, len(selected.conversation))
		for _, msg := range selected.conversation {
			lines = append(lines, msg.Role+": "+msg.Content)
		}
		return strings.Join(lines, "\n"), nil
	default:
		if value.Kind == ValueKindString {
			return value.Text, nil
		}
		return "", fmt.Errorf("unsupported value kind %s", value.Kind)
	}
}

func resolveObjectVar(env map[string]runtimeValue, name string) (vfs.FileRef, error) {
	rv, ok := env[name]
	if !ok {
		return vfs.FileRef{}, fmt.Errorf("undefined variable %s", name)
	}
	if rv.kind != runtimeValueObject {
		return vfs.FileRef{}, fmt.Errorf("variable %s is not an object reference", name)
	}
	return rv.object, nil
}

func lineExecError(line int, err error) error {
	return fmt.Errorf("satis execute error on line %d: %w", line, err)
}

func (e *Executor) stdoutWriter() io.Writer {
	if e != nil && e.Stdout != nil {
		return e.Stdout
	}
	return os.Stdout
}

func cloneMeta(src map[string]string) map[string]string {
	dst := make(map[string]string, len(src))
	for k, v := range src {
		dst[k] = v
	}
	return dst
}

func currentCWD(cwd *string) string {
	if cwd == nil {
		return "/"
	}
	return normalizeAbsolutePath(*cwd)
}

func currentLoadCWD(cwd *string) string {
	if cwd == nil {
		return "/"
	}
	return normalizeAbsolutePath(*cwd)
}

func (e *Executor) initialCWD() string {
	if e == nil {
		return "/"
	}
	return normalizeAbsolutePath(e.InitialCWD)
}

func normalizeAbsolutePath(path string) string {
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

func resolvePath(cwd string, path string) string {
	path = strings.TrimSpace(path)
	if path == "" {
		return normalizeAbsolutePath(cwd)
	}
	if strings.HasPrefix(path, "/") {
		return normalizeAbsolutePath(path)
	}
	if cwd = normalizeAbsolutePath(cwd); cwd == "/" {
		return normalizeAbsolutePath("/" + path)
	}
	return normalizeAbsolutePath(cwd + "/" + path)
}

func isGlobPattern(path string) bool {
	return strings.ContainsAny(path, "*?[")
}

func loadEntriesToDirEntries(entries []LoadEntry) []vfs.DirEntry {
	out := make([]vfs.DirEntry, 0, len(entries))
	for _, entry := range entries {
		kind := vfs.FileKindText
		if entry.IsDir {
			kind = vfs.FileKindDirectory
		}
		out = append(out, vfs.DirEntry{
			Name:        entry.Name,
			VirtualPath: entry.VirtualPath,
			Kind:        kind,
		})
	}
	return out
}

func (e *Executor) resolveLoadListing(ctx context.Context, cwd string, rawPath string) ([]LoadEntry, error) {
	if strings.TrimSpace(rawPath) == "" {
		return e.LoadPort.ListDir(ctx, cwd)
	}
	targetPath := resolvePath(cwd, rawPath)
	if isGlobPattern(targetPath) {
		entries, err := e.LoadPort.Glob(ctx, targetPath)
		if err != nil {
			return nil, err
		}
		if len(entries) == 0 {
			return nil, fmt.Errorf("no files matched %q", rawPath)
		}
		trimmedCWD := strings.TrimPrefix(cwd, "/")
		listing := make([]LoadEntry, 0, len(entries))
		for _, entry := range entries {
			display := strings.TrimPrefix(entry.VirtualPath, cwd)
			if cwd != "/" && strings.HasPrefix(display, "/") {
				display = display[1:]
			}
			if cwd == "/" && trimmedCWD == "" {
				display = strings.TrimPrefix(entry.VirtualPath, "/")
			}
			if display == "" {
				display = entry.Name
			}
			listing = append(listing, LoadEntry{
				Name:        display,
				VirtualPath: entry.VirtualPath,
				IsDir:       entry.IsDir,
			})
		}
		return listing, nil
	}
	entry, err := e.LoadPort.Stat(ctx, targetPath)
	if err != nil {
		return nil, err
	}
	if entry.IsDir {
		return e.LoadPort.ListDir(ctx, targetPath)
	}
	entry.Name = path.Base(entry.VirtualPath)
	return []LoadEntry{entry}, nil
}

func (e *Executor) expandLoadSources(ctx context.Context, cwd string, sources []string) ([]LoadEntry, error) {
	seen := make(map[string]bool)
	out := make([]LoadEntry, 0, len(sources))
	for _, source := range sources {
		targetPath := resolvePath(cwd, source)
		if isGlobPattern(targetPath) {
			entries, err := e.LoadPort.Glob(ctx, targetPath)
			if err != nil {
				return nil, err
			}
			if len(entries) == 0 {
				return nil, fmt.Errorf("no files matched %q", source)
			}
			for _, entry := range entries {
				if seen[entry.VirtualPath] {
					continue
				}
				seen[entry.VirtualPath] = true
				out = append(out, entry)
			}
			continue
		}
		entry, err := e.LoadPort.Stat(ctx, targetPath)
		if err != nil {
			return nil, err
		}
		if seen[entry.VirtualPath] {
			continue
		}
		seen[entry.VirtualPath] = true
		out = append(out, entry)
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("load requires at least one source")
	}
	return out, nil
}

func (e *Executor) expandPathPatterns(ctx context.Context, cwd string, paths []string) ([]string, error) {
	out := make([]string, 0, len(paths))
	for _, line := range paths {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		resolved := resolvePath(cwd, line)
		if strings.ContainsAny(resolved, "*?[") {
			matches, err := e.VFS.Glob(ctx, resolved)
			if err != nil {
				return nil, fmt.Errorf("glob error for %q: %w", line, err)
			}
			out = append(out, matches...)
		} else {
			out = append(out, resolved)
		}
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("no paths matched the requested patterns")
	}
	return out, nil
}

func ensureDeleteKindMatches(targetKind DeleteTargetKind, ref vfs.FileRef) error {
	switch targetKind {
	case DeleteTargetFile:
		if ref.Kind == vfs.FileKindDirectory {
			return fmt.Errorf("delete file does not accept folder object %q", ref.VirtualPath)
		}
	case DeleteTargetFolder:
		if ref.Kind != vfs.FileKindDirectory {
			return fmt.Errorf("delete folder does not accept file object %q", ref.VirtualPath)
		}
	default:
		return fmt.Errorf("unsupported delete target kind %q", targetKind)
	}
	return nil
}

func resolveTextList(env map[string]runtimeValue, varName string) ([]string, error) {
	rv, ok := env[varName]
	if !ok {
		return nil, fmt.Errorf("undefined variable %s", varName)
	}
	if rv.kind == runtimeValueTextList {
		return append([]string(nil), rv.texts...), nil
	}
	if rv.kind != runtimeValueText {
		return nil, fmt.Errorf("variable %s is not a text value", varName)
	}
	lines := strings.Split(rv.text, "\n")
	var out []string
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed != "" {
			out = append(out, trimmed)
		}
	}
	if len(out) == 0 {
		out = append(out, rv.text)
	}
	return out, nil
}

func resolveValueSelection(env map[string]runtimeValue, value Value, preferList bool) (runtimeValue, error) {
	switch value.Kind {
	case ValueKindString:
		return runtimeValue{kind: runtimeValueText, text: value.Text}, nil
	case ValueKindVariable:
		rv, err := lookupRuntimeValue(env, value.Text, preferList)
		if err != nil {
			return runtimeValue{}, err
		}
		if value.HasSelector {
			return applyListSelector(rv, value.Text, value.Selector)
		}
		return rv, nil
	default:
		return runtimeValue{}, fmt.Errorf("unsupported value kind %s", value.Kind)
	}
}

func lookupRuntimeValue(env map[string]runtimeValue, name string, preferList bool) (runtimeValue, error) {
	if preferList {
		if rv, ok := env[name]; ok && isListRuntimeValue(rv) {
			return rv, nil
		}
		if rv, ok := env[name+"_list"]; ok && isListRuntimeValue(rv) {
			return rv, nil
		}
	}
	if rv, ok := env[name]; ok {
		return rv, nil
	}
	if preferList {
		if rv, ok := env[name+"_list"]; ok && isListRuntimeValue(rv) {
			return rv, nil
		}
	}
	return runtimeValue{}, fmt.Errorf("undefined variable %s", name)
}

func isListRuntimeValue(rv runtimeValue) bool {
	return rv.kind == runtimeValueTextList || rv.kind == runtimeValueObjectList
}

func applyListSelector(rv runtimeValue, varName string, selector ListSelector) (runtimeValue, error) {
	switch rv.kind {
	case runtimeValueTextList:
		if selector.HasIndex {
			index, ok := normalizeIndex(selector.Index, len(rv.texts))
			if !ok {
				return runtimeValue{}, fmt.Errorf("index %d out of range for %s", selector.Index, varName)
			}
			return runtimeValue{kind: runtimeValueText, text: rv.texts[index]}, nil
		}
		start, end := normalizeSliceBounds(selector, len(rv.texts))
		return runtimeValue{kind: runtimeValueTextList, texts: append([]string(nil), rv.texts[start:end]...)}, nil
	case runtimeValueObjectList:
		if selector.HasIndex {
			index, ok := normalizeIndex(selector.Index, len(rv.objects))
			if !ok {
				return runtimeValue{}, fmt.Errorf("index %d out of range for %s", selector.Index, varName)
			}
			return runtimeValue{kind: runtimeValueObject, object: rv.objects[index]}, nil
		}
		start, end := normalizeSliceBounds(selector, len(rv.objects))
		return runtimeValue{kind: runtimeValueObjectList, objects: append([]vfs.FileRef(nil), rv.objects[start:end]...)}, nil
	default:
		return runtimeValue{}, fmt.Errorf("variable %s is not a list value", varName)
	}
}

func normalizeIndex(index int, length int) (int, bool) {
	if index < 0 {
		index += length
	}
	if index < 0 || index >= length {
		return 0, false
	}
	return index, true
}

func normalizeSliceBounds(selector ListSelector, length int) (int, int) {
	start := 0
	end := length
	if selector.HasStart {
		start = selector.Start
		if start < 0 {
			start += length
		}
	}
	if selector.HasEnd {
		end = selector.End
		if end < 0 {
			end += length
		}
	}
	if start < 0 {
		start = 0
	}
	if start > length {
		start = length
	}
	if end < 0 {
		end = 0
	}
	if end > length {
		end = length
	}
	if end < start {
		end = start
	}
	return start, end
}

func (e *Executor) writeSingleText(ctx context.Context, txn vfs.Txn, cwd string, path string, text string, line int) (vfs.FileRef, string, error) {
	targetPath := resolvePath(cwd, path)
	ref, resolveErr := e.resolveWritableTextPathInTxn(ctx, txn, targetPath)
	note := ""
	var err error
	switch {
	case resolveErr == nil:
		before := ref
		ref, err = e.VFS.Write(ctx, txn, vfs.WriteInput{
			Target:        vfs.ResolveInput{FileID: ref.FileID},
			WriterChunkID: txn.ChunkID,
			Mode:          vfs.WriteModeReplaceFull,
			Text:          text,
		})
		if err != nil {
			return vfs.FileRef{}, "", err
		}
		if ref.CurrentGeneration == before.CurrentGeneration {
			note = fmt.Sprintf("line %d: write to %s unchanged", line, targetPath)
		}
	case resolveErr == vfs.ErrFileNotFound:
		ref, err = e.VFS.Create(ctx, txn, vfs.CreateInput{
			VirtualPath:    targetPath,
			Kind:           vfs.FileKindText,
			CreatorChunkID: txn.ChunkID,
			InitialText:    text,
		})
		if err != nil {
			return vfs.FileRef{}, "", err
		}
	default:
		return vfs.FileRef{}, "", resolveErr
	}
	return ref, note, nil
}

func (e *Executor) writeSingleTextToObject(ctx context.Context, txn vfs.Txn, env map[string]runtimeValue, objectVar string, text string, line int) (vfs.FileRef, string, error) {
	ref, err := resolveObjectVar(env, objectVar)
	if err != nil {
		return vfs.FileRef{}, "", lineExecError(line, err)
	}
	switch ref.Kind {
	case vfs.FileKindText, vfs.FileKindGenerated, vfs.FileKindEphemeral:
	default:
		return vfs.FileRef{}, "", lineExecError(line, fmt.Errorf("write target %s must reference a text file", objectVar))
	}
	before := ref
	ref, err = e.VFS.Write(ctx, txn, vfs.WriteInput{
		Target:        vfs.ResolveInput{FileID: ref.FileID},
		WriterChunkID: txn.ChunkID,
		Mode:          vfs.WriteModeReplaceFull,
		Text:          text,
	})
	if err != nil {
		return vfs.FileRef{}, "", err
	}
	env[objectVar] = runtimeValue{kind: runtimeValueObject, object: ref}
	note := ""
	if ref.CurrentGeneration == before.CurrentGeneration {
		note = fmt.Sprintf("line %d: write to %s unchanged", line, ref.VirtualPath)
	}
	return ref, note, nil
}

func (e *Executor) createPath(ctx context.Context, txn vfs.Txn, targetPath string, stmt CreateStmt) (vfs.FileRef, error) {
	input := vfs.CreateInput{
		VirtualPath:    targetPath,
		CreatorChunkID: txn.ChunkID,
	}
	switch stmt.TargetKind {
	case CreateTargetFolder:
		input.Kind = vfs.FileKindDirectory
	case CreateTargetFile:
		input.Kind = vfs.FileKindText
		if stmt.HasContent {
			input.InitialText = stmt.Content
		}
	default:
		return vfs.FileRef{}, fmt.Errorf("unsupported create target kind %s", stmt.TargetKind)
	}
	ref, err := e.VFS.Create(ctx, txn, input)
	if err == nil {
		return ref, nil
	}
	if err != vfs.ErrPathAlreadyExists {
		return vfs.FileRef{}, err
	}
	if stmt.TargetKind != CreateTargetFolder {
		return vfs.FileRef{}, err
	}
	if _, listErr := e.VFS.ListDir(ctx, txn, targetPath); listErr != nil {
		return vfs.FileRef{}, err
	}
	if existing, resolveErr := e.resolveDirectoryPathInTxn(ctx, txn, targetPath); resolveErr == nil {
		return existing, nil
	}
	return vfs.FileRef{VirtualPath: targetPath, Kind: vfs.FileKindDirectory}, nil
}

func (e *Executor) writeTextList(ctx context.Context, txn vfs.Txn, cwd string, path string, texts []string, line int) ([]vfs.FileRef, string, error) {
	pathTemplate, ok, err := compileIndexedPathTemplate(path)
	if err != nil {
		return nil, "", lineExecError(line, err)
	}
	if !ok {
		return nil, "", lineExecError(line, fmt.Errorf("write text list requires path template containing {i} or {i:0Nd}"))
	}
	refs := make([]vfs.FileRef, 0, len(texts))
	notes := make([]string, 0)
	for i, text := range texts {
		targetPath, err := pathTemplate.render(i)
		if err != nil {
			return nil, "", lineExecError(line, err)
		}
		ref, note, err := e.writeSingleText(ctx, txn, cwd, targetPath, text, line)
		if err != nil {
			return nil, "", err
		}
		refs = append(refs, ref)
		if note != "" {
			notes = append(notes, note)
		}
	}
	return refs, strings.Join(notes, "\n"), nil
}

type indexedPathTemplatePart struct {
	literal     string
	width       int
	placeholder bool
}

type indexedPathTemplate struct {
	parts          []indexedPathTemplatePart
	hasPlaceholder bool
	literalBytes   int
}

func compileIndexedPathTemplate(path string) (indexedPathTemplate, bool, error) {
	matchIdxs := indexedPathPlaceholderPattern.FindAllStringSubmatchIndex(path, -1)
	if len(matchIdxs) == 0 {
		return indexedPathTemplate{
			parts: []indexedPathTemplatePart{{literal: path}},
		}, false, nil
	}
	parts := make([]indexedPathTemplatePart, 0, len(matchIdxs)*2+1)
	literalBytes := 0
	last := 0
	for _, idxs := range matchIdxs {
		start, end := idxs[0], idxs[1]
		if start > last {
			literal := path[last:start]
			parts = append(parts, indexedPathTemplatePart{literal: literal})
			literalBytes += len(literal)
		}
		width := 0
		if len(idxs) >= 4 && idxs[2] >= 0 && idxs[3] >= 0 {
			parsedWidth, err := strconv.Atoi(path[idxs[2]:idxs[3]])
			if err != nil {
				return indexedPathTemplate{}, false, fmt.Errorf("invalid path template %q", path)
			}
			width = parsedWidth
		}
		parts = append(parts, indexedPathTemplatePart{width: width, placeholder: true})
		last = end
	}
	if last < len(path) {
		literal := path[last:]
		parts = append(parts, indexedPathTemplatePart{literal: literal})
		literalBytes += len(literal)
	}
	return indexedPathTemplate{
		parts:          parts,
		hasPlaceholder: true,
		literalBytes:   literalBytes,
	}, true, nil
}

func (t indexedPathTemplate) render(index int) (string, error) {
	var buf strings.Builder
	buf.Grow(t.literalBytes + len(t.parts)*4)
	for _, part := range t.parts {
		if !part.placeholder {
			buf.WriteString(part.literal)
			continue
		}
		if part.width == 0 {
			buf.WriteString(strconv.Itoa(index))
			continue
		}
		buf.WriteString(fmt.Sprintf("%0*d", part.width, index))
	}
	return buf.String(), nil
}

func renderIndexedPathTemplate(path string, index int) (string, bool, error) {
	tmpl, ok, err := compileIndexedPathTemplate(path)
	if err != nil || !ok {
		return path, ok, err
	}
	rendered, err := tmpl.render(index)
	if err != nil {
		return "", false, err
	}
	return rendered, true, nil
}

func resolveReadableObjects(env map[string]runtimeValue, varName string) ([]vfs.FileRef, error) {
	if rv, ok := env[varName]; ok && rv.kind == runtimeValueObjectList {
		return append([]vfs.FileRef(nil), rv.objects...), nil
	}
	if rv, ok := env[varName+"_list"]; ok {
		if rv.kind != runtimeValueObjectList {
			return nil, fmt.Errorf("variable %s_list is not an object list", varName)
		}
		return append([]vfs.FileRef(nil), rv.objects...), nil
	}
	rv, ok := env[varName]
	if !ok {
		return nil, fmt.Errorf("undefined variable %s", varName)
	}
	if rv.kind != runtimeValueObject {
		return nil, fmt.Errorf("variable %s is not an object reference", varName)
	}
	return []vfs.FileRef{rv.object}, nil
}

func (e *Executor) resolveReadTargets(ctx context.Context, txn vfs.Txn, env map[string]runtimeValue, cwd string, stmt ReadStmt) ([]vfs.FileRef, error) {
	if stmt.ObjectVar != "" {
		return resolveReadableObjects(env, stmt.ObjectVar)
	}
	if stmt.Path == "" {
		return nil, fmt.Errorf("read requires a source")
	}

	ref, err := e.resolveFilePathInTxn(ctx, txn, resolvePath(cwd, stmt.Path))
	if err != nil {
		return nil, err
	}
	return []vfs.FileRef{ref}, nil
}

func (e *Executor) resolvePathInTxn(ctx context.Context, txn vfs.Txn, path string) (vfs.FileRef, error) {
	result, err := e.VFS.Read(ctx, txn, vfs.ReadInput{
		Target: vfs.ResolveInput{VirtualPath: path},
	})
	if err == nil {
		return result.FileRef, nil
	}
	if !errors.Is(err, vfs.ErrFileNotFound) {
		return vfs.FileRef{}, err
	}
	return e.VFS.Resolve(ctx, vfs.ResolveInput{VirtualPath: path})
}

func (e *Executor) resolvePathOfKindInTxn(ctx context.Context, txn vfs.Txn, path string, kind vfs.FileKind) (vfs.FileRef, error) {
	result, err := e.VFS.Read(ctx, txn, vfs.ReadInput{
		Target: vfs.ResolveInput{VirtualPath: path, ExpectedKind: kind},
	})
	if err == nil {
		return result.FileRef, nil
	}
	if !errors.Is(err, vfs.ErrFileNotFound) {
		return vfs.FileRef{}, err
	}
	return e.VFS.Resolve(ctx, vfs.ResolveInput{VirtualPath: path, ExpectedKind: kind})
}

func (e *Executor) resolvePathAmongKindsInTxn(ctx context.Context, txn vfs.Txn, path string, kinds ...vfs.FileKind) (vfs.FileRef, error) {
	var (
		resolved vfs.FileRef
		found    bool
	)
	for _, kind := range kinds {
		ref, err := e.resolvePathOfKindInTxn(ctx, txn, path, kind)
		if errors.Is(err, vfs.ErrFileNotFound) {
			continue
		}
		if err != nil {
			return vfs.FileRef{}, err
		}
		if found {
			return vfs.FileRef{}, vfs.ErrAmbiguousPath
		}
		resolved = ref
		found = true
	}
	if !found {
		return vfs.FileRef{}, vfs.ErrFileNotFound
	}
	return resolved, nil
}

func (e *Executor) resolveDirectoryPathInTxn(ctx context.Context, txn vfs.Txn, path string) (vfs.FileRef, error) {
	return e.resolvePathOfKindInTxn(ctx, txn, path, vfs.FileKindDirectory)
}

func (e *Executor) resolveFilePathInTxn(ctx context.Context, txn vfs.Txn, path string) (vfs.FileRef, error) {
	return e.resolvePathAmongKindsInTxn(ctx, txn, path, vfs.FileKindText, vfs.FileKindGenerated, vfs.FileKindEphemeral, vfs.FileKindBinary)
}

func (e *Executor) resolveWritableTextPathInTxn(ctx context.Context, txn vfs.Txn, path string) (vfs.FileRef, error) {
	return e.resolvePathAmongKindsInTxn(ctx, txn, path, vfs.FileKindText, vfs.FileKindGenerated, vfs.FileKindEphemeral)
}

func (e *Executor) resolveAllFilePathsInTxn(ctx context.Context, txn vfs.Txn, path string) ([]vfs.FileRef, error) {
	kinds := []vfs.FileKind{
		vfs.FileKindText,
		vfs.FileKindGenerated,
		vfs.FileKindEphemeral,
		vfs.FileKindBinary,
	}
	refs := make([]vfs.FileRef, 0, len(kinds))
	for _, kind := range kinds {
		ref, err := e.resolvePathOfKindInTxn(ctx, txn, path, kind)
		if errors.Is(err, vfs.ErrFileNotFound) {
			continue
		}
		if err != nil {
			return nil, err
		}
		refs = append(refs, ref)
	}
	return refs, nil
}

func (e *Executor) resolveDeleteTargetPathInTxn(ctx context.Context, txn vfs.Txn, path string, targetKind DeleteTargetKind) (vfs.FileRef, error) {
	switch targetKind {
	case DeleteTargetFolder:
		return e.resolveDirectoryPathInTxn(ctx, txn, path)
	case DeleteTargetFile:
		return e.resolveFilePathInTxn(ctx, txn, path)
	default:
		return vfs.FileRef{}, fmt.Errorf("unsupported delete target kind %q", targetKind)
	}
}

func formatFileRef(ref vfs.FileRef) string {
	return fmt.Sprintf("file(path=%s, id=%s, kind=%s, generation=%d)", ref.VirtualPath, ref.FileID, ref.Kind, ref.CurrentGeneration)
}

const (
	lsAnsiBoldBlue = "\x1b[1;34m"
	lsAnsiReset    = "\x1b[0m"
)

func writeLsListing(w io.Writer, entries []vfs.DirEntry) error {
	if len(entries) == 0 {
		return nil
	}
	sorted := append([]vfs.DirEntry(nil), entries...)
	sort.Slice(sorted, func(i, j int) bool {
		left := lsDisplayName(sorted[i])
		right := lsDisplayName(sorted[j])
		if left != right {
			return left < right
		}
		return sorted[i].Kind < sorted[j].Kind
	})

	useColor := lsShouldUseColor(w)

	tw := lsTerminalWidth(w)
	gap := 2
	maxW := 0
	for _, e := range sorted {
		display := lsDisplayName(e)
		if w := runewidth.StringWidth(display); w > maxW {
			maxW = w
		}
	}
	colW := maxW + gap
	if colW < 1 {
		colW = 1
	}
	ncols := tw / colW
	if ncols < 1 {
		ncols = 1
	}

	n := len(sorted)
	nrows := (n + ncols - 1) / ncols
	for r := 0; r < nrows; r++ {
		var b strings.Builder
		for c := 0; c < ncols; c++ {
			i := r*ncols + c
			if i >= n {
				break
			}
			e := sorted[i]
			prefix, suffix := "", ""
			if useColor && e.Kind == vfs.FileKindDirectory {
				prefix, suffix = lsAnsiBoldBlue, lsAnsiReset
			}
			cell := lsPadCell(lsDisplayName(e), prefix, suffix, colW)
			b.WriteString(cell)
		}
		line := strings.TrimRight(b.String(), " ")
		if _, err := fmt.Fprintln(w, line); err != nil {
			return err
		}
	}
	return nil
}

func lsDisplayName(entry vfs.DirEntry) string {
	if entry.Kind == vfs.FileKindDirectory {
		return entry.Name + "/"
	}
	return entry.Name
}

func lsPadCell(name, ansiPrefix, ansiSuffix string, colW int) string {
	vw := runewidth.StringWidth(name)
	pad := colW - vw
	if pad < 0 {
		pad = 0
	}
	return ansiPrefix + name + ansiSuffix + strings.Repeat(" ", pad)
}

func lsShouldUseColor(out io.Writer) bool {
	if os.Getenv("NO_COLOR") != "" {
		return false
	}
	if force := strings.TrimSpace(os.Getenv("FORCE_COLOR")); force != "" && force != "0" {
		return true
	}
	if f, ok := out.(*os.File); ok {
		fd := int(f.Fd())
		if term.IsTerminal(fd) {
			return true
		}
	}
	termName := strings.TrimSpace(strings.ToLower(os.Getenv("TERM")))
	if termName == "" || termName == "dumb" {
		return false
	}
	return true
}

func lsTerminalWidth(out io.Writer) int {
	if v := os.Getenv("COLUMNS"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			return n
		}
	}
	if f, ok := out.(*os.File); ok {
		if w, _, err := term.GetSize(int(f.Fd())); err == nil && w > 0 {
			return w
		}
	}
	return 80
}
