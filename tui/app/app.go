package app

import (
	"context"
	"fmt"
	"io"
	"strings"

	tuiruntime "satis/tui/runtime"
)

type Runtime interface {
	ExecLine(ctx context.Context, line string) (tuiruntime.ExecResult, error)
	ExecBody(ctx context.Context, body string) (tuiruntime.ExecResult, error)
	ExecFile(ctx context.Context, path string) (tuiruntime.ExecResult, error)
	ValidatePlan(ctx context.Context, path string) (tuiruntime.ExecResult, error)
	RunPlan(ctx context.Context, path string) (tuiruntime.ExecResult, error)
	PlanStatus(ctx context.Context, runID string) (tuiruntime.ExecResult, error)
	PlanInspect(ctx context.Context, runID string) (tuiruntime.ExecResult, error)
	PlanEvents(ctx context.Context, runID string) (tuiruntime.ExecResult, error)
	PlanContinue(ctx context.Context, runID string, fragmentPath string) (tuiruntime.ExecResult, error)
	PlanContinueLLM(ctx context.Context, runID string, prompt string) (tuiruntime.ExecResult, error)
	PlanFinish(ctx context.Context, runID string) (tuiruntime.ExecResult, error)
	OpenWorkbench(ctx context.Context, path string, intent string) error
	CommitSession(ctx context.Context) error
	ResetSession(ctx context.Context) error
	Close(ctx context.Context) error
}

const clearScreenANSI = "\x1b[2J\x1b[H"
const promptBoldStart = "\x1b[1m"
const promptBoldEnd = "\x1b[0m"

type App struct {
	runtime Runtime
	out     io.Writer
	state   State
}

func New(runtime Runtime, out io.Writer) *App {
	if out == nil {
		out = io.Discard
	}
	return &App{
		runtime: runtime,
		out:     out,
		state: State{
			Mode: ModeLine,
		},
	}
}

func (a *App) Prompt() string {
	switch {
	case a.state.Collecting:
		return promptBoldStart + "satis (chunk*)> " + promptBoldEnd
	case a.state.Mode == ModeChunk:
		return promptBoldStart + "satis (chunk)> " + promptBoldEnd
	default:
		return promptBoldStart + "satis (line)> " + promptBoldEnd
	}
}

func (a *App) Close(ctx context.Context) error {
	if a == nil || a.runtime == nil {
		return nil
	}
	return a.runtime.Close(ctx)
}

func (a *App) HandleInput(ctx context.Context, input string) (bool, error) {
	trimmed := strings.TrimSpace(input)
	if trimmed == "" && !a.state.Collecting {
		return false, nil
	}

	if hasBatchStartPrefix(trimmed) {
		a.appendHistory(input)
		if a.state.Mode == ModeChunk {
			return false, fmt.Errorf(">>> is only available in line mode; chunk mode already buffers input")
		}
		if a.state.Collecting {
			return false, fmt.Errorf("already collecting a chunk")
		}
		if !hasBatchEndSuffix(trimmed) {
			return false, fmt.Errorf("batch input must end with <<<")
		}
		lines, err := parseBatchInput(input)
		if err != nil {
			return false, err
		}
		return false, a.execBatchBody(ctx, lines)
	}

	if hasBatchEndSuffix(trimmed) {
		a.appendHistory(input)
		return false, fmt.Errorf("<<< is only valid as the end of a >>> ... <<< batch")
	}

	if strings.HasPrefix(trimmed, "/") {
		cmd, err := parseCommand(trimmed)
		if err != nil {
			return false, err
		}
		a.appendHistory(trimmed)
		return a.handleCommand(ctx, cmd)
	}

	if a.state.Mode == ModeChunk || a.state.Collecting {
		if !a.state.Collecting {
			a.state.Collecting = true
			a.state.Pending = nil
		}
		a.state.Pending = append(a.state.Pending, input)
		a.appendHistory(input)
		fmt.Fprintf(a.out, "buffered line %d\n", len(a.state.Pending))
		return false, nil
	}

	a.appendHistory(input)
	result, err := a.runtime.ExecLine(ctx, input)
	if err != nil {
		return false, err
	}
	a.renderResult(result)
	if shouldAutoCommitLine(trimmed) {
		if err := a.runtime.CommitSession(ctx); err != nil {
			return false, fmt.Errorf("auto-commit failed: %w", err)
		}
	}
	return false, nil
}

func (a *App) handleCommand(ctx context.Context, cmd command) (bool, error) {
	switch cmd.name {
	case "help":
		fmt.Fprintln(a.out, helpText)
		return false, nil
	case "clear":
		fmt.Fprint(a.out, clearScreenANSI)
		return false, nil
	case "clearchunk":
		a.state.Collecting = false
		a.state.Pending = nil
		if err := a.runtime.ResetSession(ctx); err != nil {
			return false, err
		}
		fmt.Fprintln(a.out, "chunk context cleared")
		return false, nil
	case "mode":
		if a.state.Collecting {
			return false, fmt.Errorf("cannot switch mode while collecting a chunk")
		}
		if len(cmd.args) != 1 {
			return false, fmt.Errorf("usage: /mode line|chunk")
		}
		switch strings.ToLower(cmd.args[0]) {
		case string(ModeLine):
			a.state.Mode = ModeLine
		case string(ModeChunk):
			a.state.Mode = ModeChunk
		default:
			return false, fmt.Errorf("unknown mode %q", cmd.args[0])
		}
		fmt.Fprintf(a.out, "mode: %s\n", a.state.Mode)
		return false, nil
	case "begin":
		if a.state.Collecting {
			return false, fmt.Errorf("already collecting a chunk")
		}
		a.state.Collecting = true
		a.state.Pending = nil
		fmt.Fprintln(a.out, "chunk input started")
		return false, nil
	case "commit":
		return false, a.commitCollectedBody(ctx)
	case "cancel":
		if !a.state.Collecting {
			return false, fmt.Errorf("no chunk is being collected")
		}
		a.state.Collecting = false
		a.state.Pending = nil
		fmt.Fprintln(a.out, "chunk input cancelled")
		return false, nil
	case "history":
		if len(a.state.History) == 0 {
			fmt.Fprintln(a.out, "(history is empty)")
			return false, nil
		}
		for i, entry := range a.state.History {
			fmt.Fprintf(a.out, "%d: %s\n", i+1, entry)
		}
		return false, nil
	case "exec":
		if a.state.Collecting {
			return false, fmt.Errorf("cannot /exec while collecting a chunk")
		}
		if len(cmd.args) != 1 {
			return false, fmt.Errorf("usage: /exec PATH.satis")
		}
		result, err := a.runtime.ExecFile(ctx, cmd.args[0])
		if err != nil {
			return false, err
		}
		a.renderResult(result)
		return false, nil
	case "workbench":
		if a.state.Collecting {
			return false, fmt.Errorf("cannot /workbench while collecting a chunk")
		}
		if len(cmd.args) < 1 {
			return false, fmt.Errorf("usage: /workbench VFS_WORKSPACE_DIR [INTENT...]")
		}
		intent := ""
		if len(cmd.args) > 1 {
			intent = strings.Join(cmd.args[1:], " ")
		}
		if err := a.runtime.OpenWorkbench(ctx, cmd.args[0], intent); err != nil {
			return false, err
		}
		return false, nil
	case "plan":
		if a.state.Collecting {
			return false, fmt.Errorf("cannot /plan while collecting a chunk")
		}
		var (
			result tuiruntime.ExecResult
			err    error
		)
		if len(cmd.args) == 0 {
			return false, fmt.Errorf("usage: /plan validate|run|status|inspect|events ...")
		}
		switch strings.ToLower(cmd.args[0]) {
		case "validate":
			if len(cmd.args) != 2 {
				return false, fmt.Errorf("usage: /plan validate VFS_PLAN_DIR_OR_JSON")
			}
			result, err = a.runtime.ValidatePlan(ctx, cmd.args[1])
		case "run":
			if len(cmd.args) != 2 {
				return false, fmt.Errorf("usage: /plan run VFS_PLAN_DIR_OR_JSON")
			}
			result, err = a.runtime.RunPlan(ctx, cmd.args[1])
		case "status":
			if len(cmd.args) > 2 {
				return false, fmt.Errorf("usage: /plan status [run_id]")
			}
			runID := ""
			if len(cmd.args) == 2 {
				runID = cmd.args[1]
			}
			result, err = a.runtime.PlanStatus(ctx, runID)
		case "inspect":
			if len(cmd.args) > 2 {
				return false, fmt.Errorf("usage: /plan inspect [run_id]")
			}
			runID := ""
			if len(cmd.args) == 2 {
				runID = cmd.args[1]
			}
			result, err = a.runtime.PlanInspect(ctx, runID)
		case "events":
			if len(cmd.args) > 2 {
				return false, fmt.Errorf("usage: /plan events [run_id]")
			}
			runID := ""
			if len(cmd.args) == 2 {
				runID = cmd.args[1]
			}
			result, err = a.runtime.PlanEvents(ctx, runID)
		default:
			return false, fmt.Errorf("unknown /plan subcommand %q", cmd.args[0])
		}
		if err != nil {
			return false, err
		}
		a.renderResult(result)
		return false, nil
	case "plan-continue":
		if a.state.Collecting {
			return false, fmt.Errorf("cannot /plan-continue while collecting a chunk")
		}
		if len(cmd.args) != 2 {
			return false, fmt.Errorf("usage: /plan-continue RUN_ID FRAGMENT_JSON")
		}
		result, err := a.runtime.PlanContinue(ctx, cmd.args[0], cmd.args[1])
		if err != nil {
			return false, err
		}
		a.renderResult(result)
		return false, nil
	case "plan-draft":
		if a.state.Collecting {
			return false, fmt.Errorf("cannot /plan-draft while collecting a chunk")
		}
		if len(cmd.args) < 1 {
			return false, fmt.Errorf("usage: /plan-draft RUN_ID [PROMPT]")
		}
		prompt := ""
		if len(cmd.args) > 1 {
			prompt = strings.Join(cmd.args[1:], " ")
		}
		result, err := a.runtime.PlanContinueLLM(ctx, cmd.args[0], prompt)
		if err != nil {
			return false, err
		}
		a.renderResult(result)
		return false, nil
	case "plan-finish":
		if a.state.Collecting {
			return false, fmt.Errorf("cannot /plan-finish while collecting a chunk")
		}
		if len(cmd.args) != 1 {
			return false, fmt.Errorf("usage: /plan-finish RUN_ID")
		}
		result, err := a.runtime.PlanFinish(ctx, cmd.args[0])
		if err != nil {
			return false, err
		}
		a.renderResult(result)
		return false, nil
	case "exit":
		if a.state.Collecting && len(a.state.Pending) > 0 {
			return false, fmt.Errorf("chunk input pending; use /commit or /cancel first")
		}
		fmt.Fprintln(a.out, "bye")
		return true, nil
	default:
		return false, fmt.Errorf("unknown command /%s", cmd.name)
	}
}

func (a *App) appendHistory(entry string) {
	a.state.History = append(a.state.History, entry)
}

func (a *App) renderResult(result tuiruntime.ExecResult) {
	if result.Output != "" {
		fmt.Fprintln(a.out, result.Output)
	}
	for _, line := range result.Summary {
		fmt.Fprintln(a.out, line)
	}
}

func (a *App) commitCollectedBody(ctx context.Context) error {
	if !a.state.Collecting {
		return fmt.Errorf("no chunk is being collected")
	}
	body := strings.Join(a.state.Pending, "\n")
	if strings.TrimSpace(body) == "" {
		return fmt.Errorf("collected chunk is empty")
	}
	result, err := a.runtime.ExecBody(ctx, body)
	if err != nil {
		return err
	}
	a.state.Collecting = false
	a.state.Pending = nil
	a.renderResult(result)
	return nil
}

func (a *App) execBatchBody(ctx context.Context, lines []string) error {
	body := strings.Join(lines, "\n")
	if strings.TrimSpace(body) == "" {
		return fmt.Errorf("collected chunk is empty")
	}
	result, err := a.runtime.ExecBody(ctx, body)
	if err != nil {
		return err
	}
	a.renderResult(result)
	return nil
}

func parseBatchInput(input string) ([]string, error) {
	input = strings.ReplaceAll(input, "\r\n", "\n")
	input = strings.ReplaceAll(input, "\r", "\n")
	trimmed := strings.TrimSpace(input)
	if !hasBatchStartPrefix(trimmed) || !hasBatchEndSuffix(trimmed) {
		return nil, fmt.Errorf("batch input must start with >>> and end with <<<")
	}
	body := strings.TrimPrefix(trimmed, ">>>")
	body = strings.TrimSuffix(body, "<<<")
	var lines []string
	for _, line := range strings.Split(body, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		if strings.Contains(line, ";") {
			return nil, fmt.Errorf("batch commands must be separated by newlines; semicolons are not allowed")
		}
		lines = append(lines, line)
	}
	return lines, nil
}

func hasBatchStartPrefix(input string) bool {
	return strings.HasPrefix(strings.TrimSpace(input), ">>>")
}

func hasBatchEndSuffix(input string) bool {
	return strings.HasSuffix(strings.TrimSpace(input), "<<<")
}

func shouldAutoCommitLine(input string) bool {
	fields := strings.Fields(strings.TrimSpace(input))
	if len(fields) == 0 {
		return false
	}
	switch strings.ToLower(fields[0]) {
	case "commit", "rollback":
		return false
	default:
		return true
	}
}
