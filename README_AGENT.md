# Satis System Reference for AI Agents

> This document is written for AI agents. Its purpose is to give a complete, parseable reference of the Satis runtime: what it is, how it is structured, what commands exist, what rules must be followed, and how to guide human users through it.
>
> Language: English. Format optimized for token-efficient parsing and rule extraction.

---

## SECTION 0 — WHAT IS SATIS

Satis is a **structured AI workflow runtime**. It is not only a chatbot harness. It is an execution engine where:

- Tasks are written as **SatisIL** instructions inside **Chunks**.
- Chunks are connected into **Plans** (directed acyclic graphs, DAG).
- Plans are submitted to a **Bridge scheduler** and run with a connected LLM provider.
- All file state lives inside a **VFS** (Virtual File System), isolated from the OS.

Satis is closest in concept to a lightweight AgentOS / harness engineering framework. Its design separates:

- **Software** = stable, callable computation unit (registered external tools)
- **Smartware** = reusable reasoning unit (Plans + Chunks encoding "how to think")

The key paradigm: reasoning path is decoupled from execution path.

---

## SECTION 1 — SYSTEM COMPONENTS MAP

```
satis_runtime/
├── vfs.config.json          ← VFS + directory config (REQUIRED to start)
├── invoke.config.json       ← LLM provider config (REQUIRED for Invoke)
├── runtui.sh                ← startup script (macOS/Linux only)
│
├── [VFS directories] (configured in vfs.config.json)
│   ├── mount_dir/           ← VFS mounted view
│   ├── state_dir/           ← VFS state + event log
│   ├── system_port_dir/     ← OS file import root (Load reads here)
│   └── software_registry_dir/ ← registered software index
│
└── documents/
    ├── Documents_ZH/操作手册/ ← full ZH docs
    └── Documents_EN/instructions/ ← full EN docs
```

---

## SECTION 2 — STARTUP RULES

### 2.1 Prerequisites
- OS: macOS (Darwin) or Linux. **Windows is not supported.**
- Runtime: Go must be installed.
- Files: `vfs.config.json` and `invoke.config.json` must exist.

### 2.2 How to start
```bash
./runtui.sh
```
This runs preflight (OS check, Go check, config check, smoke tests), then starts the TUI.

### 2.3 Config bootstrap (if first run)
```bash
cp vfs.config.example.json vfs.config.json
cp invoke.config.example.json invoke.config.json
# then edit both files
```

---

## SECTION 3 — CONFIG FILE SCHEMAS

### 3.1 `vfs.config.json`
```json
{
  "backend": "disk",
  "mount_dir": "<absolute path>",
  "state_dir": "<absolute path>",
  "system_port_dir": "<absolute path>",
  "software_registry_dir": "<absolute path>",
  "gc": {
    "deleted_file_retention": 0,
    "max_events": 2048
  }
}
```

Field semantics:
| Field | Role |
|---|---|
| `mount_dir` | VFS mounted view on disk |
| `state_dir` | VFS state, event log |
| `system_port_dir` | OS file import root (Load reads from here, read-only) |
| `software_registry_dir` | software index root |

**Boundary rule**: All Satis-governed data stays within configured paths. The system will not write outside these directories during normal operation.

### 3.2 `invoke.config.json`
```json
{
  "providers": {
    "<provider-id>": {
      "base_url": "<OpenAI-compatible endpoint>/v1/",
      "api_key": "<key>",
      "model": "<model name>",
      "timeout_seconds": 120,
      "temperature": 0.2,
      "max_tokens": 4096
    }
  },
  "router": {
    "default_provider": "<provider-id>"
  }
}
```

Multiple providers can be defined. `default_provider` must match a key in `providers`.

---

## SECTION 4 — SATISIL LANGUAGE REFERENCE

SatisIL is the instruction language executed inside Chunks.

### 4.1 Value types (only two)
| Type | Syntax | Example |
|---|---|---|
| String literal | `[[[...]]]` | `[[[hello world]]]` |
| Variable reference | `@var` | `@summary` |

Supported escapes inside `[[[...]]]`: `\n`, `\t`, `\\`. No others.

### 4.2 Path rules
- Paths are **never** wrapped in `[[[...]]]`. A path is a bare token.
- Paths with spaces must use double quotes: `"./my folder/file.txt"`
- Variables cannot be used as paths.

### 4.3 Full chunk format (for Plan execution)
```
chunk_id: CHK_EXAMPLE
intent_uid: intent_example

<instructions>
```
Both `chunk_id` and `intent_uid` are required in full chunk mode.

### 4.4 Interactive (TUI/line) mode
- No header needed. Instructions are entered one line at a time.
- Session state persists: `cwd`, variables.
- Prompt: `satis (line)>` (line mode) or `satis (chunk)>` (chunk mode).

### 4.5 Complete instruction set

#### Navigation
```
Pwd
Cd <path>
Ls [path]
```

#### Object resolution
```
Resolve file <path> as @var
Resolve file <glob> as @var_list
Resolve folder <path> as @var
```
`Resolve` only works on VFS-governed objects. It does NOT auto-import OS files.

#### OS file import (cross-boundary)
```
Load pwd
Load ls [path]
Load cd <path>
Load <source_path> [as @var]
Load <glob> [as @var]
```
`Load` reads from `system_port_dir`. After import, file enters current VFS `cwd`.

#### Read
```
Read @file_var as @text
Read @file_var lines <start> <end> as @text
Read <path> as @text
```

#### Write / Create / File ops
```
Write [[[text]]] into <path> [as @out]
Write @var into <path> [as @out]
Write @list into <path_{i}> [as @out]

Create file <path> [with [[[init]]] ] [as @var]
Create folder <path> [as @var]

Copy @var to <path> [as @out]
Move @var to <path> [as @out]
Rename @var to <path> [as @out]
Delete file @var | <path>
Delete folder <path>
Patch @var replace [[[old]]] with [[[new]]] [as @out]
```

#### LLM Invoke
```
Invoke [[[prompt]]] [with @input] [as @output]
Invoke @prompt_var [with @input] [as @output]
Invoke [[[prompt]]] concurrently with @list as @output [mode separate_files|single_file]
```
- First value = user prompt. `with` value = system prompt.
- If no `as`: result prints to console.
- `concurrently` = parallel execution over a list.

#### Soft output / debug
```
Print @var
Print [[[text]]]
Print @list[:n]
```

#### Software call
```
<software_name> <function> [--flag @var | [[[val]]] ...] [as @out]
```

#### Software management
```
Software ls
Software find <prefix>
Software describe <name>
Software functions <name>
Software refresh [as @var]
Software cd <path>
Software pwd
```

#### Session control
```
Commit
Rollback
```

#### TUI slash commands (main prompt only)
```
/help
/exit
/clear
/clearchunk
/mode line | chunk
/begin
/commit
/cancel
/exec <host_path.satis>
/workbench <vfs_dir> [description]
/plan validate <vfs_dir>
/plan run <vfs_dir>
/plan status [run_id]
/plan inspect [run_id]
/plan events [run_id]
/history
```

---

## SECTION 5 — CHUNK AND PLAN STRUCTURE

### 5.1 Concepts
| Term | Definition |
|---|---|
| **Chunk** | Atomic execution unit. Contains SatisIL body + metadata. |
| **Plan** | DAG of Chunks. Defines execution order and data flow. |
| **Intent** | Top-level goal (immutable once set). Has a global unique ID. |
| **Handoff** | Data binding between chunks: `from_step + from_port → downstream input`. |
| **Edge** | Scheduling dependency between chunks (A must complete before B). |

### 5.2 Chunk types
| Type | Symbol | Behavior |
|---|---|---|
| `task` | `T` | Executes SatisIL body |
| `decision` | `D` | Control flow branch. Can return to old nodes. |

### 5.3 Handoff data flow
```
Chunk A:
  Write @result into ./out/file.txt as @out_draft

Handoff declaration (in plan JSON):
  downstream.inputs.handoff_inputs.draft = {
    from_step: "CHK_A",
    from_port: "draft"
  }

Chunk B:
  Resolve file ./out/upstream__draft.txt as @in_draft_file
  Read @in_draft_file as @in_draft_text
```

Rule: Edge controls **when** B runs. Handoff controls **what** B receives.

Variable naming convention (not enforced, but standard):
- `@in_<port>_file` — input file object
- `@in_<port>_text` — input text content
- `@out_<port>` — output object

### 5.4 Plan lifecycle
```
[edit in Workbench]
       ↓
    validate          ← check structure, handoffs, single entry chunk
       ↓
     save             ← write plan.json to VFS (allows invalid draft)
       ↓
     submit           ← validate + register plan_id with Bridge
       ↓
     run              ← submit + StartRun
       ↓
  status / events / inspect   ← observe execution
```

---

## SECTION 6 — WORKBENCH REFERENCE

### 6.1 How to enter
```
/workbench <vfs_dir> [description]
```
If `plan.json` does not exist under `<vfs_dir>`, it is auto-created.

### 6.2 Layout
| Panel | Location | Role |
|---|---|---|
| Status bar | Top | Plan path, dirty state, last run_id, validation summary |
| Chunk Graph | Left | ASCII DAG. `T`=task, `D`=decision. Blue highlight = selected. `!` = validation error. |
| Chunk editor | Center-top | Edit `satis_text` of selected chunk |
| Handoff table | Center-bottom | Edit `handoff_inputs` (port, from_step, from_port, var_name) |
| CLI log | Right-top | Command output |
| Diagnostics | Right-mid | Validation errors for current chunk or global |
| CLI input | Right-bottom | Type commands here |

### 6.3 Key bindings
| Key | Action |
|---|---|
| `Tab / Shift+Tab` | Cycle focus |
| `Option/Alt + 1..4` | Jump to Graph / Editor / Handoff / CLI |
| `Ctrl+S` | save |
| `Ctrl+Shift+R` | render |
| `Ctrl+P` | command palette |
| `Ctrl+N` (graph focused) | add task child node |
| `Shift+D` (graph focused) | add decision child node |
| `Ctrl+T` (graph focused) | cycle node type task↔decision |
| `Ctrl+K` (graph focused) | delete current leaf node |
| `↑↓←→` (graph focused) | navigate graph |
| `Enter` (graph focused) | select node into editor |
| `Esc` | exit Workbench (if no unsaved changes) |

### 6.4 CLI commands (Workbench)
Aliases shown in parentheses.

| Command | Action |
|---|---|
| `validate` (`v`) | Validate without writing |
| `render` (`r`) | Sync handoffs + validate + write + redraw |
| `save` (`s`) | Write to VFS (allows invalid draft) |
| `reload` | Reload from VFS (rejects if dirty) |
| `run` | submit + StartRun |
| `runselected` (`rs`) | Run only current chunk + its upstream dependencies |
| `submit` | Validate + write + register plan with Bridge |
| `start` | StartRun (if already submitted) |
| `status` | Print run status |
| `inspect` | Full run snapshot: all chunk states |
| `events` | Incremental events since last call |
| `chunk [CHK_ID]` | Inspect single chunk in current run |
| `synchandoff` (`sh`) | Parse `@in_*` from body → update handoff table |
| `syncbody` (`sb`) | Clean auto-generated input prelude in body |
| `duplicate` (`dup`) | Copy current chunk with new ID |
| `adddecision` | Add decision child to current node |
| `branch <name> <target>` | Add/redirect decision branch |
| `decisiondefault <branch>` | Set default branch |
| `decisionmode <human\|llm\|llm_then_human>` | Set decision mode |
| `decisionprompt <template>` | Set LLM decision template |
| `decisiontitle <title>` | Set human interaction title |
| `plan-continue <file>` | Attach child plan to current node |
| `plan-change <file>` | Replace Next Plan subgraph |
| `plan-detach` | Detach Next Plan (make child independent) |
| `palette` (`p`) | Open command palette |
| `quit` / `quit!` | Exit (prompt if dirty / force) |

---

## SECTION 7 — SOFTWARE REGISTRY

### 7.1 Directory structure
```
software_registry_dir/
  SKILLS.md                ← auto-generated index
  <category>/
    SKILLS.md              ← auto-generated
    <software_name>/
      SKILL.md             ← REQUIRED: frontmatter with name + description
      forsatis.json        ← REQUIRED: function interface declaration
      <runner script>      ← actual implementation
```

### 7.2 `SKILL.md` format (required)
```md
---
name: <software_name>
description: <one-line description>
---
```
Rules:
- Must start with `---` frontmatter block.
- `name` must be non-empty and match the `name` field in `forsatis.json`.
- `description` must be non-empty.
- If either rule fails, the software is skipped and not registered.

### 7.3 Registration flow
```
1. Create software directory under software_registry_dir
2. Write SKILL.md (frontmatter) + forsatis.json
3. In Satis TUI: Software refresh as @report
4. Print @report → check recognized / skipped count
5. Software ls / Software describe <name> → verify visible
```

### 7.4 Call syntax
```
<software_name> <function> [--flag @var | [[[value]]] ...] [as @out]
```

---

## SECTION 8 — ERROR TAXONOMY

| Error class | When | Example cause |
|---|---|---|
| **Parse error** | Syntax invalid | Path in `[[[...]]]`, unsupported escape, unclosed bracket |
| **Validate error** | Semantic invalid | Missing `chunk_id`, illegal `mode` value, multi-entry DAG |
| **Execute error** | Runtime failure | Undefined variable, file not found, LLM call failed |

Debugging order:
1. Can `validate` pass? → Fix structure.
2. `run` → check `inspect` for failed chunk phase.
3. `chunk CHK_ID` → read error message for that node.

---

## SECTION 9 — COMMON PITFALLS (AGENT MUST KNOW)

| Pitfall | Rule |
|---|---|
| Path in `[[[...]]]` | **Illegal in v1.** Paths are bare tokens. |
| Forgetting `@` prefix | Variables MUST start with `@`. `summary` ≠ `@summary`. |
| No `as @var` after Write | Output object is discarded. Cannot be referenced later. |
| `Resolve` on un-imported OS file | Use `Load` first. `Resolve` only sees VFS-governed objects. |
| `Read @folder` | Folders cannot be Read. Only files. |
| Unsupported escape in `[[[...]]]` | Only `\n`, `\t`, `\\` are valid. |
| Plan with multiple entry chunks | Bridge requires exactly 1 entry chunk. `validate` will reject. |
| Software not visible after adding | Check `SKILL.md` frontmatter. Run `Software refresh`. |
| `events` shows nothing | Either run is terminal, or using wrong context (Workbench vs main TUI `/plan` are separate). |
| Windows usage | Not supported. macOS/Linux only. |

---

## SECTION 10 — HOW TO GUIDE A HUMAN USER (AGENT INSTRUCTIONS)

When a human user asks for help with Satis, follow this decision tree:

### 10.1 User is setting up for the first time

Steps to walk them through:
1. Copy config files: `cp vfs.config.example.json vfs.config.json` and same for invoke.
2. Set absolute paths in `vfs.config.json` (all 4 directories under one parent folder).
3. Fill `invoke.config.json` with their LLM endpoint, key, model name, and set `default_provider`.
4. Run `./runtui.sh`.
5. Confirm OS is macOS or Linux. If Windows: explain it is not supported.

### 10.2 User wants to run a quick test

Give them this exact sequence in TUI:
```
invoke [[[hello, how are you?]]]
```
If LLM responds → provider config is working.

Then a small file workflow:
```
Cd /demo
Write [[[test content]]] into test.txt as @f
Read @f as @t
Invoke [[[summarize in one sentence]]] with @t as @out
Print @out
```

### 10.3 User wants to build a multi-step plan

1. Enter Workbench: `/workbench /plans/<name> <description>`
2. Edit root chunk body in center panel.
3. `Ctrl+N` to add child chunks.
4. Fill handoff table for each downstream chunk.
5. `render` → fix any diagnostics.
6. `run` → watch `events` / `inspect`.

### 10.4 User wants to register a software

1. Create directory: `software_registry_dir/tools/<name>/`
2. Write `SKILL.md` with valid frontmatter.
3. Write `forsatis.json` with function interface.
4. In TUI: `Software refresh as @r` then `Print @r`.
5. Verify with `Software describe <name>`.

### 10.5 User gets an error

| Symptom | What to check |
|---|---|
| `Load` fails | `system_port_dir` exists and file is there |
| `Invoke` fails | `invoke.config.json` base_url / api_key / model |
| `validate` fails in Workbench | Diagnostics panel. Usually handoff mismatch or multi-entry DAG |
| Software not found | `SKILL.md` frontmatter valid? Run `Software refresh` |
| `runtui.sh` fails | OS supported? Go installed? Config files exist? |

### 10.6 User asks what a concept means

| Concept | One-line answer |
|---|---|
| VFS | Isolated virtual filesystem for all Satis-governed files. Not the OS filesystem. |
| Chunk | One atomic step. Contains SatisIL instructions. |
| Plan | A DAG connecting Chunks in execution order. |
| Handoff | Data binding: tells downstream chunk what upstream chunk produced. |
| Smartware | A reusable reasoning structure (Plan + Chunks). Analogous to "reusable thought process". |
| Software | An external callable tool registered in the software registry. |
| Load | The only way to import OS files into VFS. Reads from `system_port_dir`. |
| Bridge | Internal scheduler that submits and runs Plans. |

---

## SECTION 11 — MINIMAL DEMO SEQUENCE (COPY-READY)

```bash
# Terminal: initial setup
cp vfs.config.example.json vfs.config.json
cp invoke.config.example.json invoke.config.json
# edit both files, then:
./runtui.sh
```

```
# In TUI — line mode
Cd /demo
Write [[[Satis is a structured AI workflow runtime.]]] into a.txt as @f
Read @f as @t
Invoke [[[Translate to Chinese in one sentence.]]] with @t as @zh
Write @zh into /demo/zh.txt as @out
Print @zh
```

```
# In TUI — enter Workbench
/workbench /plans/demo a demo plan
```

```
# In Workbench CLI
validate
run
inspect
```

If all succeed: VFS is configured, LLM is connected, CLI execution works, Bridge scheduler works.

---

## SECTION 12 — LICENSE

Copyright (c) 2026 Wilson Huang.

This project is licensed under the **Creative Commons Attribution-NonCommercial-ShareAlike 4.0 International License (CC BY-NC-SA 4.0)**.

To view a copy of this license, visit [http://creativecommons.org/licenses/by-nc-sa/4.0/](http://creativecommons.org/licenses/by-nc-sa/4.0/).

---

## SECTION 13 — FURTHER READING

| Topic | File |
|---|---|
| SatisIL syntax (full) | `documents/Documents_ZH/操作手册/01-SatisIL语法说明-易读版.md` |
| CLI full guide | `documents/Documents_ZH/操作手册/02-Satis CLI详细使用教程.md` |
| Workbench full guide | `documents/Documents_ZH/操作手册/04-Satis Workbench使用说明.md` |
| Software registry | `documents/Documents_ZH/操作手册/03-Satis Software集成与注册表说明.md` |
| Design philosophy (ZH) | `documents/Documents_ZH/satis的故事/` |
| Design philosophy (EN) | `documents/Documents_EN/satis_s_story/` |
