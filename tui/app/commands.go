package app

import (
	"fmt"
	"strings"
)

type command struct {
	name string
	args []string
}

func parseCommand(input string) (command, error) {
	input = strings.TrimSpace(input)
	if input == "" || !strings.HasPrefix(input, "/") {
		return command{}, fmt.Errorf("not a command")
	}
	fields := strings.Fields(input)
	if len(fields) == 0 {
		return command{}, fmt.Errorf("empty command")
	}
	return command{
		name: strings.ToLower(strings.TrimPrefix(fields[0], "/")),
		args: fields[1:],
	}, nil
}

const helpText = `Satis TUI Commands
=================

[General]
  /help
      Show this help message.
  /history
      Show input history in current TUI process.
  /exit
      Exit TUI. If chunk buffer is pending, use /commit or /cancel first.

[Display / Session]
  /clear
      Clear terminal output only (does not change VFS/session state).
  /clearchunk
      Drop uncommitted interactive state and reset current session.

[Input Mode]
  /mode line|chunk
      Switch plain input handling mode.
      - line : execute each plain line immediately
      - chunk: buffer plain lines until /commit
      - line mode also supports >>> ... <<< as a CLI-only batch shortcut
      - in >>> ... <<< batches, commands must be newline-separated; semicolons are rejected
  /begin
      Start explicit chunk buffering (works in any mode).
  /commit
      Execute buffered chunk body.
  /cancel
      Discard buffered chunk body.

[File Execution]
  /exec PATH.satis
      Execute a .satis file from host filesystem.
  /plan validate VFS_PLAN_DIR_OR_JSON
      Validate a workbench plan from a VFS directory or explicit plan.json path.
      Plans must have exactly one entry chunk.
  /plan run VFS_PLAN_DIR_OR_JSON
      Submit and start a workbench DAG from line mode using an isolated session
      and isolated chunk_id prefix. This does not reuse the current REPL chunk.
  /plan status [RUN_ID]
      Show the latest plan-run status summary. If RUN_ID is omitted, use the
      most recent /plan run in this TUI session.
  /plan inspect [RUN_ID]
      Show run summary and per-chunk status lines for a plan run.
  /plan events [RUN_ID]
      Poll new events for a plan run. Repeated calls only print unread events.
  /plan-continue RUN_ID FRAGMENT_JSON
      Continue a planning_pending run with a manual fragment file.
  /plan-draft RUN_ID [PROMPT]
      Ask the LLM to draft and attach a continuation fragment.
  /plan-finish RUN_ID
      Finish a planning_pending run and mark it completed.
  /workbench VFS_WORKSPACE_DIR [INTENT...]
      Open an existing workbench when DIR/plan.json already exists.
      Create a new workbench only when DIR does not exist, and INTENT is required.

Quick examples:
  /mode line
  Pwd
  Cd /docs
  >>>
  Resolve file /docs/a.txt as @a
  Read @a as @text
  <<<
  >>> Resolve file /docs/a.txt as @a
  Read @a as @text<<<
  /exec examples/01_read_write.satis
  /plan validate /plans/demo
  /plan run /plans/demo
  /plan status
  /plan inspect
  /plan events
  /plan-continue run_001 fragment.json
  /plan-draft run_001
  /plan-finish run_001
  /workbench /plans/demo
  /workbench /plans/new_demo 整理输入文件并建立新计划
  /mode chunk
  Resolve file /docs/a.txt as @a
  Read @a as @text
  Load prompts/*.md as @prompts
  /commit`
