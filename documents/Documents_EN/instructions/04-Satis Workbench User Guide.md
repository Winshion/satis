# Satis Workbench User Guide

> Wilson's Note: Since since it's just a demo version (and most of the functions are vibe-coded), make sure your terminal window is big enough to hold all the components TAT! Time permitted, I will continue optimizing the operation interface.

This document describes the UI layout, shortcuts, CLI commands, and typical workflows of the current **TUI Workbench** (`tui/workbench`) in this repository. Workbench is used to edit **`bridge.ChunkGraphPlan`** in the terminal (default `plan.json` in the selected workspace directory), and it shares VFS, Invoker, and bridge scheduling with the main TUI.

At the runtime layer, two node types are currently implemented:

- **`task`**: normal execution node
- **`decision`**: control-flow branching node
- **planning actions**: chain multiple plan documents through Workbench planning commands; the parent graph shows `Next Plan` (and child graphs show `Return Plan`) navigation nodes

So Workbench currently edits a **growable workflow graph**, not just a static DAG. A `decision` node can jump back to previous nodes and continue execution. Planning navigation nodes are used for cross-plan navigation and replacement.

---

## 1. Entering and Exiting

### 1.1 Enter

In the main Satis TUI, run (in `chunk` mode, you must `/commit` or `/cancel` first; entering Workbench is blocked when there is an uncommitted chunk buffer):

```text
/workbench <VFS workspace directory> [Intent...]
```

- The argument is a **VFS path** (for example `/plans/demo`), and the plan file is fixed at `<directory>/plan.json`.
- Open an **existing** Workbench: `/workbench <directory>`
- Create a **new** Workbench: `/workbench <new-directory> <Intent...>`
- Rules:
  - If the directory or `plan.json` already exists, do **not** pass `Intent`
  - When creating a new directory, `Intent` is **required**
  - If the directory exists but `plan.json` is missing, the runtime now returns an error instead of scaffolding automatically

### 1.2 Exit

- **Esc**: exits Workbench if no unsaved changes; if dirty, prompts you to **`save`** or **`quit!`** first.
- CLI: **`quit`** (refuses when dirty), **`quit!`** (force exit).

---

## 2. Layout (Top · Left · Center · Right)

| Area | Description |
|------|------|
| **Top Status** | First line shows current **Intent description**. Second line shows current plan VFS path, selected chunk, dirty/clean state, validation summary, and latest **run_id** (if any). |
| **Left Chunk Graph** | Unified ASCII workflow canvas (not a tree). Shows `in/out` handoff variable summary on the same screen. The selected chunk is highlighted with **bright blue text and border**. Nodes with validation issues are marked with **`!`**. Downward branch start points from parent nodes are marked with a **yellow `+`**. Node titles include **`T / D`** for `task / decision`; `decision` nodes also show `branches:...` summary. |
| **Center · Chunk** | `satis_text` editor for the current chunk. Changes update the in-memory plan and mark it dirty. |
| **Center · Chunk Meta** | Only two fields are shown: `chunk port` and `chunk desc`. `chunk desc` is synced to the chunk-head `description:` line. |
| **Center · Handoff** | Table-based editing for `inputs.handoff_inputs`. |
| **Right · CLI Log** | Command echo and runtime logs; error lines are shown with red tags. |
| **Right · Diagnostics** | Static validation issue summary: prioritizes errors related to the **current chunk**; otherwise shows a subset of global issues. |
| **Right · CLI Input** | Press Enter after typing a command to execute it (with or without leading `/`). |

---

## 3. Focus and Shortcuts

### 3.1 Focus cycle and quick focus

| Key | Action |
|------|------|
| **Tab** / **Shift+Tab** | Cycle focus through preset chain (Chunk Graph -> Chunk -> Decision -> CLI). |
| **Ctrl+A** / **Ctrl+D** | Same focus cycle, backward/forward. |
| **Option+1** (macOS) / Alt+1 | Focus **Chunk Graph** |
| **Option+2** (macOS) / Alt+2 | Focus **Chunk editor** |
| **Option+3** (macOS) / Alt+3 | Focus **Decision editor** |
| **Option+4** (macOS) / Alt+4 | Focus **CLI input** |

You can also use command: `focus left|chunk|decision|cli`.

### 3.2 Common operations

| Key | Action |
|------|------|
| **Ctrl+Shift+R** | Execute **`render`** (sync topology, validate, write to disk, redraw DAG). |
| **Ctrl+S** | Execute **`save`**. |
| **Ctrl+P** / **Ctrl+Space** | Open **command palette**. |
| **Chunk Graph + Ctrl+N** | Add a **task child chunk** with the **current selected node as parent** (see "Structural Editing"). |
| **Chunk Graph + Shift+D** | Add a **decision child chunk** with the **current selected node as parent**. |
| **Chunk Graph + Ctrl+T** | Cycle current selected node type: `task -> decision -> task`. |
| **Chunk Graph + Ctrl+K** | Delete current highlighted **chunk** (only allowed for non-root leaf nodes); cleans edges and downstream handoff references. |
| **Chunk Graph + Arrow keys** | Move selection by direction: **up parent, down child, left/right sibling level**. |
| **Chunk Graph + Enter** | Set current graph-selected node as current chunk (load into center editor). |
Deletion rules: **root cannot be deleted**, **non-leaf cannot be deleted**, **last remaining chunk cannot be deleted**. After successful deletion, focus prefers the **deepest leaf node** in the remaining graph (instead of jumping back to root). If deletion fails or leaves the plan invalid, implementation attempts disk reload to roll back dangerous state.

---

## 4. Command Palette

- **Open**: `Ctrl+P`, `Ctrl+Space`, or CLI command **`palette`** (alias **`p`**).
- **Usage**: type keywords in the `palette>` input to filter list; press **Enter** to move focus to list, arrow keys to choose, **Enter** to execute and close; **Esc** closes.
- Includes actions such as `render` / `validate` / `save` / `run`, **focused run**, **handoff/body sync**, **copy chunk**, **jump to chunk**, etc. (consistent with `paletteActions` implementation).

---

## 5. CLI Command Reference

Commands may omit leading `/`. Common commands and **aliases** are listed below.

### 5.1 Plan and files

| Command | Alias | Action |
|------|------|------|
| `render` | `r` | Apply current handoff form, run `SyncEdgesFromHandoffs`, validate+normalize, **write back `plan.json`**, redraw Chunk Graph. |
| `validate` | `v` | Same sync and validation, **without writing** (still normalizes in-memory plan). |
| `save` | `s` | Sync topology + write to disk (**invalid draft can be saved**; no forced validation). |
| `reload` | — | Reload from VFS; **refuses when dirty**. |

### 5.2 Structure and advanced editing

| Command | Alias | Action |
|------|------|------|
| `duplicate` | `dup` | Duplicate current chunk with a new `chunk_id`, then sync topology. |
| `adddecision` | — | Add a `decision` child node with current graph-selected node as parent. |
| `branch <name> <target_chunk_id>` | — | Add or redirect a branch edge on current selected `decision` node. `target_chunk_id` may be an existing old node. |
| `decisiondefault <branch>` | — | Set default branch for current selected `decision` node. |
| `decisionmode <human\|llm\|llm_then_human>` | — | Set decision mode for current selected `decision` node. |
| `decisionprompt <template>` | — | Set LLM decision template for current selected `decision` node. |
| `decisiontitle <title>` | — | Set human interaction title for current selected `decision` node. |
| `plan-description [text]` | — | Without arguments, show current `plan_description`; with text, update current `plan_description` and save immediately. |
| `intent-description` | — | Show current `intent_description`. |
| `plan-continue <json_file>` | — | Attach downstream planning JSON after current selected node. If missing, file is auto-created. Child plan **shares namespace** with current plan (`intent_id / intent_uid` aligned). |
| `plan-change <json_file>` | — | On current selected `Next Plan` node, **replace the Next Plan subgraph** with another planning JSON. New subgraph still **shares namespace** with current plan. |
| `plan-detach` | — | Detach current selected `Next Plan`: convert child plan to an **independent namespace**, and remove this Next Plan subgraph from current graph (including Next Plan root). |
| `plan-draft <prompt>` | — | When run is `planning_pending`, request LLM to continue generating and attaching subsequent fragments. |
| `plan-finish` | — | When run is `planning_pending`, finish current planning round and mark run completed. |
| `attachfragment <host_json_file>` | — | Import a `plan fragment` from a **host JSON file**, and **append** new nodes/edges to current graph. Existing graph is not replaced. |
| `synchandoff` | `sh` | Parse `@in_<port>__<var_name>` references from current chunk body and refresh `port` / `var_name` in handoff table (preserving existing `from_step` / `from_port` when possible). |
| `syncbody` | `sb` | Clean old auto-generated input prelude (`@in_<port>_file` / `@in_<port>_text`) while preserving your handwritten body. |
| `palette` | `p` | Open command palette. |

### 5.3 Bridge submit and run

| Command | Action |
|------|------|
| `submit` | Sync topology + **validate and normalize** + write to disk, then **SubmitChunkGraph**. |
| `start` | **StartRun** when already submitted and plan is unchanged. |
| `run` | **submit + StartRun** (full graph; internally syncs/validates/writes first). |
| `runselected` | `rs` | Build and submit only subgraph of **current chunk + all upstream dependencies (reverse along edges)**, then **StartRun**; useful for focused debugging. |

### 5.4 Runtime observation (Workbench CLI)

All commands below operate on the most recently started plan run **inside Workbench** (`run` / `start` / `runselected` updates this "current run"). If no run has been started, it prompts you to run `run` or `start` first.

| Command | Action and Typical Usage |
|------|----------------|
| **`status`** | **Lightweight polling**: print current run `run_id`, `status` (e.g. pending/running/completed/failed), and `updated` time. Best when you only need to check if execution finished. |
| **`inspect`** | **Full snapshot**: one run-level line + **summary** (`plan_id`, `graph_revision`, `total`, `task/decision` counts, per-status chunk counts, optional `primary_failure`, artifact count, etc.) + **one line per chunk** `chunk <id>: <phase>`. Useful for overall review after completion, diagnosing failures/blocking nodes, or confirming whether recent planning has attached new nodes into current graph. |
| **`events`** | **Incremental events**: calls `StreamRunEvents` and prints only events **not yet printed since last `events` in this Workbench session** (format like `event <id> <type> [chunk=...]`). You can run `events` repeatedly to poll; if no new events, prints `no new events`; terminal-state runs may show terminal hints. |
| **`chunk`** | **Single-node detail**: `chunk` (defaults to current graph-selected chunk) or **`chunk CHK_ID`**, calls **InspectRunChunk** on current run and prints chunk `status` plus error summary if failed. Best for targeted inspection after finding failed chunks via `inspect`. |

**Relationship with `/plan` in main TUI**

- In main Satis TUI (outside Workbench), **`/plan run`** and **`/plan status` / `/plan inspect` / `/plan events`** use another "latest plan run" context, and do **not** mix with Workbench `lastRunID`.
- **`/plan run`** uses an isolated `chunk_id` prefix, so chunk names seen in bridge may differ from original IDs in `plan.json`; rely on IDs from `/plan inspect` or events.

### 5.5 Others

| Command | Action |
|------|------|
| `help` | Print commands and shortcuts help. |
| `quit` / `quit!` | See "Exit" section above. |

---

## 6. Multi-chunk and Handoff Conventions (Workbench side)

### 6.1 Source of edges

- Before **`render` / `save` / `submit` / `run` / `runselected`**, Workbench executes **`SyncEdgesFromHandoffs`**: derive **`edges`**, **`depends_on`**, and **`entry_chunks`** from each chunk's `handoff_inputs` **`from_step` + `from_port`** (both must be non-empty and valid).
- Half-filled handoff (for example, only `from_step` without `from_port`) causes sync errors.
- Current implementation requires **exactly one primary entry chunk**. If sync results in multiple zero-indegree nodes, `validate` / `render` / `run` fail; you must restore a single-root DAG first.

Notes:

- For **`task`** nodes, Workbench still mainly derives normal control edges from handoff.
- **`decision`** control-flow semantics are now supported by the runtime.
- Branch edges on `decision` are not removed during handoff topology sync.
- `decision` can jump back to old nodes; when that branch is chosen, old nodes re-enter execution flow, useful for rework/review/re-extraction.
- Planning is represented in graph by `Next Plan / Return Plan` navigation nodes and `plan-continue / plan-change / plan-detach` commands; you can also append new nodes/edges through **fragment import** or direct `plan.json` edits.

### 6.2 Upstream outputs and downstream inputs

- Upstream chunks should produce text aliases in Satis: **`@out_<port>__<var_name>`**.
- Downstream handoff constraints: `from_port` must match upstream `port`, and `var_name` must exactly match alias `var_name` on both sides.
- Downstream body consumes directly via: **`@in_<port>__<var_name>`**.
- Workbench/bridge handoff no longer uses `virtual_path`; variables are passed by runtime value binding.

### 6.3 Add child chunk (Ctrl+N on graph)

- Creates a child node with current selected chunk as **parent**, and tries:
  - If parent body contains parseable **`@out_<port>__<var_name>`**, prefill one handoff for child and scaffold `Print @in_<port>__<var_name>` in child body.
- You still need to complete business logic in child body (for example `Invoke`, `Write`, etc.), then run **`render`** / **`validate`**.

Additional notes:

- **`Ctrl+N`** adds a `task` child.
- **`Shift+D`** or **`adddecision`** adds a `decision` child.

### 6.4 Node type switch (Ctrl+T on graph)

- Select a node in left graph and press **`Ctrl+T`** to cycle:
  - `task -> decision -> task`
- During switching:
  - `chunk_id` is preserved.
  - `task` preserves/fills default `satis_text`.
  - `decision` fills default branches and interaction definition.
- This is a **structural operation**. If you switch a `task` with existing body into `decision`, its body is cleared into control-node definition.

### 6.5 Editing decision jump conditions

Workbench now supports direct editing of structured `decision` jump conditions.

Common commands:

```text
branch <branch_name> <target_chunk_id>
decisiondefault <branch_name>
decisionmode <human|llm|llm_then_human>
decisionprompt <template>
decisiontitle <title>
```

Example:

```text
branch revise CHK_ROOT
branch approve CHK_DONE
decisiondefault revise
decisionmode llm
decisionprompt choose branch from {{allowed_branches}} using {{context.case_body}}
```

Notes:

- `branch` automatically adds branch name to `allowed_branches`.
- If same branch name exists, re-running `branch` redirects it to the new target node.
- `target_chunk_id` can be an **existing old node**.

For example:

```text
branch retry_extract CHK_EXTRACT
```

Means when `decision` chooses `retry_extract`, control flow jumps back to `CHK_EXTRACT` and resumes downstream from that old node.

---

## 7. Recommended Workflow (Short Version)

### 7.1 Edit and run entirely in Workbench

1. **`/workbench /your/workspace`**  
2. Select chunks in Chunk Graph, edit **Chunk** in center; maintain `port` / `from_step` / `from_port` / `var_name` in **Handoff**.  
3. If body and handoff differ, try **`synchandoff`** or **`syncbody`** first.  
4. To introduce new planning results, run **`attachfragment <host_json_file>`** to attach fragment into current graph.  
5. If current node is `decision`, continue editing jump conditions via **`branch` / `decisiondefault` / `decisionmode` / `decisionprompt` / `decisiontitle`**.  
6. Use **`render`** or **`save`** to persist; use **`validate`** when you only want checks without writing.  
7. Full-graph test run: **`run`**; focused chain debug: **`runselected`**.  
8. **During run**: use **`events`** repeatedly for timeline; use **`status`** for quick completion check.  
9. **After run**: use **`inspect`** for summary and per-chunk status; focus on whether `graph_revision` changed and whether new nodes entered current graph. For failed nodes, use **`chunk CHK_ID`** for detailed errors.  
10. Combine **Diagnostics** and **graph node type markers** to fix plan, then **`render` / `save`** and retry.

### 7.2 Planning chaining steps (current recommendation)

For the typical flow "parent plan -> child plan -> replace / detach":

1. Select attach point in parent graph (usually a task node requiring further decomposition).
2. Execute **`plan-continue <child.json>`**:
   - If `child.json` does not exist, it is auto-created.
   - Child subgraph is attached and a `Next Plan` node appears.
   - Child plan automatically **shares namespace** with parent (`intent_id / intent_uid` aligned).
   - The new child plan is created with an empty `plan_description` by default.
3. To edit child plan, select `Next Plan` and press **Enter** to open child file; in child graph use `Return Plan` to go back.
   - If the child plan has no `plan_description`, Workbench prompts for it when the child plan is opened.
4. To replace current Next Plan subgraph, return to parent graph, select `Next Plan`, and run **`plan-change <other.json>`**:
   - Replace current Next Plan subgraph with new child plan.
   - Replaced child plan still **shares namespace** with parent.
5. To remove sharing and cut current attachment, select `Next Plan` and run **`plan-detach`**:
   - Child plan becomes **independent namespace**.
   - Next Plan subgraph is removed from parent graph (`Next Plan` root is also deleted).
   - Focus returns to anchor node (usually the original attach point).

Additional notes:

- `open next plan` / `plan-change` / `plan-detach` use relaxed loading for child plan docs (as long as JSON is parseable), to avoid temporary handoff inconsistency blocking operations.
- When Workbench **adds a chunk**, **duplicates a chunk**, or **creates a child plan via `plan-continue`**, it scans other valid plan files in the same workspace directory and skips already used `CHK_*` ids, so `chunk_id` stays unique at the workspace level.
- Runtime planning (`planning_pending`) and graph-edit planning are two separate tracks: use `plan-draft` / `plan-finish` for run continuation, and `plan-continue` / `plan-change` / `plan-detach` for graph structure.

### 7.3 Run same workspace plan in main TUI after exiting Workbench

For plans already persisted by **`save`**, when you want to run in **line mode** directly (without full-screen Workbench):

1. **`/plan validate /your/workspace`** (optional)  
2. **`/plan run /your/workspace`** (record `run_id` in output, or rely on default "latest `/plan run`")  
3. Poll via **`/plan events`** / **`/plan status`**; after completion use **`/plan inspect`**  

Note: these observation commands are **`/plan ...`**, not Workbench `events`; the "latest run" contexts are independent.

---

## 8. FAQ

| Symptom | Suggestion |
|------|------|
| `render` / `save` reports handoff binding error | Check `from_step` / `from_port` / `var_name`; verify upstream outputs `@out_<from_port>__<var_name>` and downstream consumes `@in_<port>__<var_name>`. |
| `chunk_id` validation failed | First line in body `chunk_id:` must match DAG node id. |
| `runselected` behavior differs from `run` | `runselected` includes only current chunk and its upstream dependencies; chunks in parallel branches are excluded. |
| `events` has no new output | Run may already be finished with no new events, or wrong context is used (`events` in Workbench, `/plan events` in main TUI). |
| `status` / `inspect` says no run | In Workbench run **`run`** or **`start`** first; in main TUI run **`/plan run`** first. |
| **Ctrl+Shift+R** does not respond in terminal | Some terminal/system shortcut conflicts; use CLI command **`render`** directly. |
| Palette cannot open | Confirm focus is not stuck; try **`palette`** or **`p`**. |

---

## 9. Related Code and Overview Docs

- Implementation directory: `tui/workbench/` (`workbench.go`, `model.go`, `model_enhanced.go`, `ui_enhanced.go`, etc.).
- For slash commands in main TUI and overall REPL overview, see: `documents/Satis TUI详细使用教程.md` (Section 7 there is a Workbench summary; details in this document take precedence).

---

*Document version: synchronized with current repository Workbench implementation. If behavior changes, code is authoritative and this document should be updated.*
