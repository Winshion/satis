# Satis CLI Detailed User Guide

Date: 2026-04-10  
Applicable version: current implementations in `cmd/satis-tui`, `tui/app`, `tui/workbench`, and `tui/runtime`

---

> Wilson's Note: this project was originally intended to be a TUI, but during development CLI proved more suitable. In the end it became a CLI, while still being referred to as "TUI".

## 1. What is Satis CLI

`satis-cli` is an interactive command-line interface (REPL) that continuously executes SatisIL in one session, reusing `satis.Executor` and the configured VFS. It is suitable for:

- incremental debugging (`Resolve` / `Read` / `Load` / `Invoke` / `Write` step-by-step validation)
- fast command and virtual-path input with Tab completion
- opening **Workbench** to edit bridge Chunk Graph plans (JSON), and executing the full **bridge pipeline** of submit/run
- ad-hoc investigation of VFS, invoke, and path resolution behavior

The current planning runtime is no longer limited to static DAGs. The runtime currently supports:

- **`task`**: normal execution node
- **`decision`**: control-flow branching node
- **planning actions**: chain multiple plan docs via Workbench planning commands; `Next Plan` appears in parent graph (and `Return Plan` in child graph)

---

## 2. Startup

### 2.1 Common startup command

```bash
go run ./cmd/satis-tui --config vfs.config.json --invoke-mode openai --invoke-config invoke.config_local.json
```

Or use repository script:

```bash
./runtui.sh
```

### 2.2 Startup parameters

| Parameter | Meaning |
|------|------|
| `--config` | VFS config path (default `vfs.config.json`) |
| `--invoke-mode` | `error` \| `echo` \| `prompt-echo` \| `openai` |
| `--invoke-config` | Separate invoke config; if provided, overrides embedded `invoke` in `--config` |
| `--initial-cwd` | Initial virtual working directory for TUI session (default `/`) |
| `--chunk-id` | `chunk_id` used by interactive session (default `TUI_REPL`) |

### 2.3 `vfs.config.json` and `system_port_dir` (optional)

Besides VFS fields like `backend`, `mount_dir`, `state_dir`, and `gc`, top-level **`system_port_dir`** can be configured (string path, supports `~` expansion). It points to the host root directory for read-only imports via **`Load`**. At startup this path is resolved and passed to sandbox **`SystemPort`**; **absolute host paths are not persisted in VFS state**. If missing or unavailable, `Load ...` fails at runtime.

See repository sample in `vfs.config.json` under `system_port_dir`.

### 2.4 `software_registry_dir` (software registry)

Top-level `vfs.config.json` can also configure `software_registry_dir` (string path, supports `~` expansion):

- points to software registry root (host directory)
- auto-created at startup if missing
- generates `SKILLS.md` template at startup if missing

Notes:

- software install/uninstall is managed by directory operations on OS side
- no uninstall command is provided inside TUI/Satis

---

## 3. Prompt and input modes

Prompt appears in bold:

- `satis (line)>` — **line mode** (default)
- `satis (chunk)>` — **chunk mode**, waiting for buffered lines
- `satis (chunk*)>` — chunk collection started (for example after `/begin`), buffer not committed

### 3.1 line mode

- each normal input line (not starting with `/`) is treated as one SatisIL statement and **executes immediately**
- after success, session auto-**Commit** (except explicit `Commit` / `Rollback`)

### 3.2 chunk mode

- normal input goes into **buffer** and does not execute immediately
- use **`/commit`** to execute buffered body as one multi-line chunk
- use **`/cancel`** to discard buffer
- you can also use **`/begin`** in any mode to start collection, then `/commit` to finish

> **Note**: current implementation uses **`/commit`** to submit buffered content, and does **not** provide `/end`. If other docs mention `/end`, follow code and `/help`.

---

## 4. Slash commands (main TUI)

Use **`/help`** to view built-in help. Commands are grouped below.

### 4.1 General

| Command | Action |
|------|------|
| `/help` | Show help |
| `/history` | Show input history within current TUI process |
| `/exit` | Exit; if chunk buffer pending, run `/commit` or `/cancel` first |

### 4.2 Display and session

| Command | Action |
|------|------|
| `/clear` | Clear screen only; does **not** change VFS/session state |
| `/clearchunk` | Discard uncommitted interactive state and **reset current session** (equivalent to a fresh REPL session) |

### 4.3 Input mode and buffer

| Command | Action |
|------|------|
| `/mode line` | Switch to line mode |
| `/mode chunk` | Switch to chunk mode |
| `/begin` | Explicitly start chunk collection (available in any mode) |
| `/commit` | Execute buffered chunk body |
| `/cancel` | Discard buffered chunk body |

### 4.4 Files and Workbench workspace

| Command | Action |
|------|------|
| `/exec PATH.satis` | Read `.satis` from **host filesystem** and execute it (path may be relative to current shell working dir) |
| `/workbench VFS_WORKSPACE_DIR` | Open full-screen **three-panel Workbench**. If `plan.json` is missing, directory and default plan template are auto-created (see Section 7). |

### 4.5 line mode: `/plan` (validate, run, observe)

These commands run in **main TUI** (not Workbench full screen). All path arguments are **VFS paths** (relative to current virtual `cwd` or absolute `/...`).

**Path conventions**

- if argument is a **directory** (e.g. `/plans/demo`), plan file is treated as **`<dir>/plan.json`**
- if argument ends with **`.json`**, it is treated as an **explicit plan file path** (e.g. `/plans/demo/plan.json`)

**Plan constraints**

- plan must pass `bridge.ValidateChunkGraphPlan`
- **`entry_chunks` must be exactly 1** (single-root DAG)
- otherwise `validate` / `run` fails
  - note: this is a hard constraint for `/plan ...` and actual execution/scheduling; Workbench editing/saving allows invalid drafts (see Sections 6.4 / 7.3)

| Command | Action |
|------|------|
| `/plan validate <dir or plan.json>` | Read and validate plan without writing/running; output includes summary like `plan_id`, `entry_chunk`, chunk count. |
| `/plan run <dir or plan.json>` | Submit and **StartRun**: shares VFS with current REPL but clones plan with an **isolated session** and **new `chunk_id` prefix** to avoid mixing with `TUI_REPL`. Success output includes **`run_id`** and **`effective_plan_id`** (may differ from on-disk `plan_id`). |
| `/plan status [run_id]` | Lightweight status for a run (`run_id`, `status`, `updated_at`). If omitted, uses **latest** `/plan run` in current TUI process. |
| `/plan inspect [run_id]` | Full run snapshot: summary line includes `plan_id`, `graph_revision`, `task/decision` counts, success/failure/block counts, `primary_failure`, etc.; then one line per chunk. If omitted, same latest-run fallback. |
| `/plan events [run_id]` | Incrementally pull events for a run: only `StreamRunEvents` records **not printed since last call**. Repeat for polling. Terminal state may include `run_id=... is terminal`. If omitted, same latest-run fallback. |

**Difference from Workbench `run` / `status`**

- in Workbench: `run`, `status`, `inspect`, `events` operate on latest run started **inside Workbench** (see Section 7.3)
- from current version, `inspect` summary also shows **`graph_revision`** and `task/decision` counts to observe whether planning fragments are attached
- in main TUI: `/plan run` and `/plan status|inspect|events` use a separate context that records only line-mode `/plan run`, and does **not** overwrite Workbench `lastRunID`
- after `/plan run`, chunk IDs in `inspect` / `events` / bridge output have `LINE_RUN_...` prefix and differ from original IDs in `plan.json`; expected behavior

**Suggested command order**

1. `/plan validate /plans/demo` (optional, validate after edits)  
2. `/plan run /plans/demo` (note `run_id` or rely on latest-run default)  
3. During run: `/plan events` repeatedly (or `/plan status` to check completion)  
4. After completion: `/plan inspect` for final per-chunk statuses  

**Notes**

- `/plan ...` cannot be used while chunk collection from `/begin` is pending (run `/commit` or `/cancel` first)
- `/plan ...` does **not** trigger line-mode auto `CommitSession` (unlike single-line SatisIL)

---

## 5. Tab completion

Press **Tab** on input line to trigger readline completion, similar to common Unix shells:

- **TUI slash commands** (e.g. `/help`, `/workbench`, ...)
- **SatisIL command names** (e.g. `Cd`, `Read`, `Write`, `Load`, ...)
- **virtual paths**: based on directories/files listed under current VFS `cwd`; directories usually include trailing `/`
- **`Load` command**: in positions such as `Load`, `Load cd`, `Load ls`, `Load <source>`, Tab lists directories/files under current **load cwd** in system_port logical path (dirs with trailing `/`). Data is from session `ListLoadDir`, **not through VFS**, and host absolute paths are never written into VFS
- **host path after `/exec`**: completion relative to host current working directory

Completion inserts only the suffix of current token to avoid duplicated prefixes.

Note: in wildcard mode (e.g. `prompts/*.md`), Tab does not expand glob patterns; it only prefix-completes current path token. Full semantics are determined by SatisIL `Load` runtime behavior.

---

## 6. VFS and paths (important for TUI users)

### 6.1 Same path can contain both "directory" and "file"

Virtual paths are **type-aware**: same string path can host both a directory object and a text file object. Typical effects:

- **`Cd projects`** resolves to directory `projects`
- **`Read projects`** (or `Resolve file ...`) resolves with file semantics
- in **`ls`** output, directory names have trailing `/` for disambiguation

### 6.1.1 `Load` and VFS paths are different namespaces

- operations like **`Cd` / `Ls` / `Read` (path form)** use **VFS virtual paths** (relative to VFS `cwd`)
- **`Load pwd` / `Load ls` / `Load cd` / `Load <src>`** browse/read the **logical tree under `system_port_dir`** (separate **load cwd**); after import, content is written into current VFS `cwd` using source basename as filename
- **`Resolve`** only resolves **VFS-managed objects**; unmanaged host files under `mount_dir` are not auto-imported

### 6.2 `Delete all` and directories

`Delete all <directory>` recursively deletes listed contents. If a level is only a "synthetic directory" (without a standalone directory object), implementation tolerates "directory itself missing" to avoid false `file not found` and still remove child items.

### 6.3 Where plan files are stored

`/workbench` takes a **VFS workspace path** (relative to current `cwd` or absolute `/...`). Current implementation fixes plan file at `plan.json` under this directory.

- if directory missing: auto-created recursively
- if `plan.json` missing: auto-generate minimal valid bridge plan template
- if `plan.json` exists: load directly without overwrite

### 6.4 Required files in a Workbench workspace directory

Example: with `/workbench /plans/demo`, workspace is `/plans/demo/`. Under current implementation:

- **Required (to open Workbench)**: `/plans/demo/plan.json`
  - **format**: JSON of `bridge.ChunkGraphPlan`
  - **content requirement (open/edit)**: only needs to be deserializable JSON; **invalid drafts are allowed** (can open/edit/save without passing `bridge.ValidateChunkGraphPlan`, enabling IDE-like draft workflow)
  - **content requirement (submit/run)**: `submit` / `run` / `runselected` / `render` trigger `bridge.ValidateChunkGraphPlan`; failing plans are blocked and reported via Diagnostics/logs
  - **cold start behavior**: if missing, auto-create minimal valid template (single root chunk, no edges, no handoff)

- **Required (directory itself)**: `/plans/demo/` directory object
  - **cold start behavior**: auto-created recursively if missing

- **Optional (organized by your project habits)**
  - output dirs, e.g. `/plans/demo/out/` (auto-generated when chunk runs `Write ... into /plans/demo/out/...`)
  - input/assets dirs, e.g. `/plans/demo/inputs/`, `/plans/demo/assets/`
  - runtime artifacts: location depends on your plan/chunk design; Workbench does not enforce specific folders

### 6.5 Software registry management (outside Workbench)

Use these in main TUI SatisIL input:

- `Software ls`
- `Software pwd`
- `Software cd <path>`
- `Software find <prefix>`
- `Software describe <name>`
- `Software functions <name>`
- `Software refresh [as @var]`

`Software refresh`:

- scans `software_registry_dir`
- recognizes only compliant software (`SKILL.md` frontmatter with `name`/`description`, and name matches `forsatis.json`)
- auto-rewrites `## Entries` in each folder `SKILLS.md`
- outputs statistics: `refreshed folders / recognized / skipped`

Example:

```satis
Software refresh as @report
Print @report
```

---

## 7. Workbench

`/workbench <VFS workspace>` enters full-screen mode (based on `tview`) and does **not** display with main REPL at the same time. Exiting Workbench returns to original TUI.

### 7.1 Layout (Top · Left · Center · Right)

- **Top**: status bar (current plan path, selected chunk, dirty state, latest run, validation status)
- **Left**: current plan **ASCII Chunk Graph** (unified workflow canvas, not tree). Node shows `in/out` handoff summaries; selected chunk uses bright blue text and border; downward split starts are marked with yellow `+`. Node title prefix **`T / D`** means `task / decision`; `decision` node also shows `branches:...`.
- **Center**
  - top: **Chunk editor** (current chunk `satis_text`)
  - bottom: **Handoff** table + form + JSON preview (edit `inputs.handoff_inputs`)
- **Right**
  - top: **CLI log**
  - middle: **Diagnostics** (validation summary for current chunk or global)
  - bottom: **command input** (execute on Enter)

### 7.2 Keyboard and focus

| Key | Action |
|------|------|
| **Tab** / **Shift+Tab** | Cycle focus across graph, editor, table, form, CLI input |
| **Option+1..4** (macOS) / Alt+1..4 | Quick focus Chunk Graph / Chunk editor / Handoff / CLI |
| **↑ / ↓ / ← / →** (with Chunk Graph focus) | Move current selected chunk on graph (**up parent, down child, left/right same level**) |
| **Enter** (with Chunk Graph focus) | Confirm graph-selected node and load into center editor |
| **Ctrl+S** | Equivalent to `save` |
| **Ctrl+Shift+R** | Equivalent to `render` |
| **Ctrl+P** (or **Ctrl+Space**) | Open command palette |
| **Ctrl+N / Ctrl+K** in Chunk Graph focus | Add `task` child / delete current chunk |
| **Shift+D** in Chunk Graph focus | Add `decision` child |
| **Ctrl+T** in Chunk Graph focus | Cycle node type `task -> decision -> task` |
| **Ctrl+N / Ctrl+K** in Handoff table focus | Add / delete handoff port |
| **Ctrl+K** in supplementary table focus | Delete current key/value row |
| **Esc** | Exit if clean; if dirty, asks `save` or `quit!` first |

You can also switch focus by command: `focus left|chunk|handoff|cli`.

### 7.2.1 High-efficiency structural editing

- Press **`Ctrl+N`** on Chunk Graph to add a child chunk under current node.
- Press **`Shift+D`** on Chunk Graph to add a `decision` child under current node.
- If parent chunk body already contains `@out_<port>__<var_name>`, Workbench auto:
  - pre-fills one `handoff` (`from_step` / `from_port` / `var_name`) for new chunk
  - scaffolds `Print @in_<port>__<var_name>` in new chunk body
- On **`Ctrl+K`** delete, only **non-root leaf** chunks are deletable. After success, focus jumps to the **deepest leaf** in remaining graph and also cleans:
  - edges pointing to deleted chunk
  - `from_step` references to deleted chunk in downstream `handoff_inputs`
  - then recalculates `edges` / `depends_on` / `entry_chunks`

### 7.2.2 Node type switching

- Press **`Ctrl+T`** on Chunk Graph to cycle current node type:
  - `task -> decision -> task`
- Keeps `chunk_id` while filling target-type defaults:
  - `task`: preserve/fill `satis_text`
  - `decision`: fill default branch and interaction definition
- If switching a populated `task` to control node, body is cleared into structured control definition.

### 7.2.3 Command palette

- Open with **`Ctrl+P`**, supports fuzzy search for:
  - `render` / `validate` / `save` / `run`
  - `run selected chunk + dependencies`
  - `sync handoff from body`
  - `sync body prelude from handoff`
  - `duplicate current chunk`
  - `jump to CHK_xxx`
- Palette is useful instead of memorizing many shortcuts.

### 7.3 CLI commands inside Workbench

Commands can include or omit leading `/` (implementation strips it). Common commands:

| Command | Action |
|------|------|
| `help` | list available commands |
| `render` | apply form, sync topology from handoff, validate+normalize, **redraw Chunk Graph and write plan** |
| `validate` | validate+normalize (no disk write) |
| `save` | sync topology then write plan back to **VFS path** (allows invalid draft; marks as saved) |
| `reload` | reload from VFS (**refuses when dirty**) |
| `palette` | open command palette |
| `duplicate` | duplicate current chunk into new `chunk_id` |
| `adddecision` | add `decision` child under current selected graph node |
| `branch <name> <target_chunk_id>` | add/redirect branch edge on selected `decision`; target may be old node |
| `decisiondefault <branch>` | set default branch for selected `decision` |
| `decisionmode <human\|llm\|llm_then_human>` | set decision mode for selected `decision` |
| `decisionprompt <template>` | set LLM decision template for selected `decision` |
| `decisiontitle <title>` | set human interaction title for selected `decision` |
| `plan-continue <json_file>` | attach downstream planning JSON after selected node; auto-create file if missing; child shares parent namespace (`intent_id / intent_uid`) |
| `plan-change <json_file>` | replace selected `Next Plan` subgraph with another planning JSON; still shares parent namespace |
| `plan-detach` | detach selected `Next Plan`: child becomes independent namespace; remove Next Plan subgraph from parent (including Next Plan root) |
| `plan-draft <prompt>` | when run is `planning_pending`, request LLM to continue and attach fragments |
| `plan-finish` | when run is `planning_pending`, finish current planning round and mark run completed |
| `attachfragment HOST_JSON_FILE` | import plan fragment from host JSON and append new nodes/edges to current graph without replacing old graph |
| `synchandoff` | derive and refresh handoff table from `@in_<port>__<var_name>` in current chunk body |
| `syncbody` | clean old `@in_<port>_file` / `@in_<port>_text` prelude generation while keeping handwritten body |
| `submit` | sync topology + **validate and normalize** + write disk, then call **`bridge.SubmitChunkGraph`**, register `plan_id` |
| `start` | **StartRun** when submitted and plan unchanged (no forced rewrite) |
| `run` | one-step **submit + StartRun** (sync/validate/write first) |
| `runselected` | submit and run only focused subgraph: current chunk + all upstream dependencies |
| `status` | one-line status for latest Workbench `run` / `start` / `runselected` (`run_id`, `status`, `updated`) |
| `inspect` | full inspect for same run (summary + per-chunk statuses), including `graph_revision` and `task/decision` stats |
| `events` | incremental events for same run; repeated calls output only unread events; shows `no new events` if none |
| `chunk` | requires existing run; inspect selected chunk by default, or `chunk CHK_ID` for single-node **`InspectRunChunk`** (with errors) |
| `quit` | warns if unsaved; exits otherwise |
| `quit!` | force exit (ignore unsaved prompt) |

Common aliases:

- `r` -> `render`
- `v` -> `validate`
- `s` -> `save`
- `dup` -> `duplicate`
- `rs` -> `runselected`
- `sh` -> `synchandoff`
- `sb` -> `syncbody`
- `p` -> `palette`

### 7.3.1 How to edit Decision branches and jump conditions

In current version, Decision jump logic is edited structurally; direct JSON editing is not recommended. Common commands:

```text
branch <branch_name> <target_chunk_id>
decisiondefault <branch_name>
decisionmode <human|llm|llm_then_human>
decisionprompt <template>
decisiontitle <title>
```

Example:

```text
adddecision
branch revise CHK_ROOT
branch approve CHK_DONE
decisiondefault revise
decisionmode llm
decisionprompt choose branch from {{allowed_branches}} using {{context.case_body}}
```

To jump back to an old node, point branch to that node:

```text
branch retry_extract CHK_EXTRACT
```

This means when `decision` selects `retry_extract`, control flow goes back to `CHK_EXTRACT` and continues downstream from there.

### 7.3.2 Planning chaining steps (current recommendation)

Typical "parent plan -> child plan -> replace / detach" flow:

1. Select attach point in parent graph (usually a task needing further decomposition).
2. Run `plan-continue <child.json>`:
   - auto-create `child.json` if missing
   - attach child subgraph and show `Next Plan`
   - child plan automatically shares namespace with parent (`intent_id / intent_uid`)
3. To edit child plan, select `Next Plan` and press Enter to open child file; use `Return Plan` in child graph to return.
4. To replace current `Next Plan` subgraph, select `Next Plan` in parent and run `plan-change <other.json>`:
   - replace with new child plan
   - replaced child still shares parent namespace
5. To detach and stop sharing, select `Next Plan` and run `plan-detach`:
   - child becomes independent namespace
   - parent removes this `Next Plan` subgraph (including root)
   - focus returns to anchor node (usually original attach point)

Additional notes:

- `open next plan` / `plan-change` / `plan-detach` use relaxed child-plan reads (parseable JSON is enough) to avoid temporary handoff mismatch blocking operations.
- runtime planning (`planning_pending`) and graph-edit planning are separate tracks: use `plan-draft` / `plan-finish` for run continuation, and `plan-continue` / `plan-change` / `plan-detach` for graph editing.

### 7.4 bridge end-to-end pipeline

Inside Workbench, `submit` / `start` / `run` / `inspect` / `events` / `chunk` are handled through TUI runtime **`bridge.Server`** and share with main session:

- same **VFS Service**
- same **Invoker**
- same **BatchScheduler** (if configured on Executor)

It also syncs **InitialCWD** on bridge side, keeping relative path behavior inside chunks consistent with your TUI `cwd`.

Plan file format is JSON of **`bridge.ChunkGraphPlan`** (`plan_id`, `chunks`, `edges`, `entry_chunks`, `handoff_inputs`, etc.) and must satisfy bridge validation rules. Otherwise `validate` / `save` / `submit` reports field-level errors.

---

## 8. Minimal workflow examples

### 8.1 Fast debugging in line mode

```text
Pwd
Cd /docs
Resolve file ./a.txt as @doc
Read @doc as @text
Invoke [[[Please summarize]]] with @text as @summary
Print @summary
Write @summary into /output/summary.txt as @out
```

### 8.2 Execute multiple lines in chunk mode

```text
/mode chunk
Resolve file /docs/a.txt as @doc
Read @doc as @text
Invoke [[[Please summarize in one sentence]]] with @text as @summary
Write @summary into /output/summary.txt as @out
/commit
```

### 8.3 Execute `.satis` script from host

```text
/exec examples/01_read_write.satis
```

### 8.4 Validate and run `plan.json` only in main TUI (line mode)

Without entering Workbench:

```text
/plan validate /plans/demo
/plan run /plans/demo
/plan status
/plan events
/plan events
/plan inspect
```

Notes: parameter-less `status` / `inspect` / `events` rely on `run_id` of latest `/plan run`; you can also pass explicit ID like `/plan status run_xxx`.

### 8.5 Open Workbench and run plan inside it

```text
/workbench /plans/demo
```

Then in right-side CLI:

```text
validate
run
status
inspect
events
events
chunk
```

Workbench `status` / `inspect` / `events` / `chunk` only target latest `run` / `start` / `runselected` **inside Workbench**, and do not share "latest run" context with main TUI `/plan run`.

---

## 9. Invoke behavior (TUI focus)

### 9.1 Variable prompt

- `Invoke @prompt`
- `Invoke @prompt with @sys as @out`
- `Invoke @prompts[:2] with @sys as @out`
- `Invoke @prompt concurrently with @inputs as @summary`

Note: prompt side must be **text or text list**; if object type, `Read` it into text first.

### 9.2 Role mapping for `with`

`Invoke VAR1 with VAR2 as VAR3`:

- `VAR1` -> user (prompt side)
- `VAR2` -> system

### 9.3 Thinking segment display

If model output starts with `<redacted_thinking>...</redacted_thinking>`, TUI shows collapsed preview for thinking segment (about up to 6 lines), while main response continues streaming. Values written into variable/file will be cleaned of thinking tags.

---

## 10. Session state and commit semantics

- session retains: **cwd**, **variable environment** (e.g. `@doc`), and uncommitted chunk buffer
- in **line** mode, successful normal statements auto-**Commit** (`Commit` / `Rollback` themselves excluded)
- **`/clearchunk`** resets session and drops uncommitted state
- in **Workbench**, plan edits are written back to VFS by `save`; `run` saves first then submit+start, avoiding disk/memory plan mismatch

---

## 11. Common troubleshooting

| Symptom | Handling |
|------|------|
| `undefined variable @xxx` | variable not defined or reset; verify with `Print` or `/history` |
| `Invoke @p` says prompt not text | `@p` may be object; run `Read @p as @p_text` first |
| `/exit` says pending chunk | run `/commit` or `/cancel` first |
| strange path after Tab on `cd pr` | suffix completion fix is applied; if still odd, check host path vs VFS path mixing |
| same-name `projects` and `projects/` | use `ls`; entry with `/` is directory; `Cd` and `Read` semantics differ |
| Workbench `reload` failed | run `save` first, or `quit!` to discard local changes |
| `submit` / `run` validation failed | complete handoff according to bridge validation (e.g. resolve/read usage matches declared inputs) |
| `/plan status` says no plan run | run `/plan run` first; or when mixed with Workbench run, use commands in the corresponding interface |
| chunk names have prefix after `/plan run` | isolated runs rewrite `chunk_id`; use IDs from `/plan inspect` for debugging |

---

## 12. Recommended habits

- start with **line mode** for small-step validation, then use **chunk mode** to consolidate multi-line scripts
- use **`Print`** heavily for key intermediates to avoid propagating errors into downstream `Write`
- validate prompts in TUI before writing them into `.satis` or plan JSON
- before using **openai**, verify process structure with **echo** / **prompt-echo**
- while editing Chunk Graph: prefer adding child chunks via **`Ctrl+N` on DAG**; use **command palette** for large action switches
- if handoff and body mismatch, try **`synchandoff`** or **`syncbody`**, then `render` / `validate`
- for single-node debugging, prefer **`runselected`** to narrow execution scope
- while editing Chunk Graph: run **`validate`** frequently, **`save`** before finalization, and use **`run`** for full-graph tests
- after leaving Workbench, to run same workspace plan in main TUI: use **`/plan validate` / `/plan run`**, and observe with **`/plan status|inspect|events`**

---

## 13. One-sentence summary

**Satis TUI** turns SatisIL flow ("parse -> read text -> model -> write") into an observable, completable, session-commit interactive process; **Workbench** further lets you edit **bridge plans** and execute full scheduling pipeline via **submit / run / inspect / events**; and main TUI can also validate, isolate-run, and observe the same VFS `plan.json` through **`/plan`** without entering Workbench.
