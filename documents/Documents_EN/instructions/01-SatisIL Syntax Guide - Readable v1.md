# SatisIL Syntax Guide (Readable Version, v1)

Date: 2026-04-10  
Applicable version: SatisIL **v1** (corresponding to current repository `satis/parser.go`, `satis/validate.go`, and `satis/runtime.go`)

---

## 1. Start with a minimal runnable example

```satis
chunk_id: CHK_DEMO
intent_uid: intent_demo

Resolve file ./examples/inputs/source.md as @doc
Read @doc as @text
Invoke @text with [[[Please summarize in one sentence]]] as @summary
Write @summary into ./examples/output/summary.txt as @out_summary
```

This does 4 things:
- resolve one file into an object: `@doc`
- read text content: `@text`
- invoke model to generate output: `@summary`
- write output file and get a new object: `@out_summary`

---

## 2. Basic structure (fixed format)

Each chunk contains:

1) **header metadata** (at least two lines)
- `chunk_id: ...`
- `intent_uid: ...`

2) **instruction block** (one instruction per line)

Notes:
- if no instruction exists, validation fails
- variable names must start with `@` (for example `@doc`)

### 2.1 Interactive Session / TUI mode

Besides full chunks, SatisIL also supports **body-only** interactive execution:

- users can input only instruction body (without `chunk_id` / `intent_uid`)
- TUI can parse and execute as **one command per line**
- within the same session, it keeps:
  - current working directory `cwd`
  - defined variable environment (such as `@doc`, `@text`)

This is suitable for shell-like interactive commands, while complete `.satis` files remain ideal for batch script execution.

---

## 3. Two value syntaxes (only values use triple brackets)

In SatisIL, there are only two value types:

- **text literal**: `[[[...]]]`
- **variable reference**: `@var_name` (supports list selectors: `@var[i]`, `@var[:n]`, `@var[a:b]`)

Examples:
- `Write [[[hello]]] into ./a.txt as @out_a`
- `Write @summary into ./b.txt as @out_b`
- `Print @content_list[:3]`

### Escapes inside triple brackets (current implementation)

In all triple-bracket literals used as **values** (for example `Write` content, `Invoke` prompt/input, `Print` literal), backslash `\` triggers escape parsing. **Only** the following three forms are supported. Any other form (such as `\uXXXX`) throws a **parse-time** error.

| Syntax | Meaning |
|------|------|
| `\n` | newline |
| `\t` | tab |
| `\\` | literal `\` |

Notes:
- actual line breaks can still be written directly between `[[[` and `]]]`; `\n` is for adding newlines within one physical line.

---

## 4. Path syntax (biggest v1 change)

### 4.1 Paths do not use triple brackets

In v1, **VFS virtual paths** are not values, so they should **not** use `[[[...]]]`. Write paths directly as tokens (or shell-style quoted strings):

- `./docs/a.txt`
- `/docs/a.txt`
- `../output/result.txt`

If wrapped in triple brackets (for example `Resolve file [[[./a.txt]]]`), v1 throws a **parse error** directly.

### 4.2 Paths with spaces (Linux-style)

v1 supports two common forms:

1) **double quotes** (recommended):

```satis
Cd "/sales reports"
Resolve file "/sales reports/final report.txt" as @doc
Write @text into "/sales reports/out 1.txt" as @out
```

2) **escape space with backslash** (in bare token):

```satis
Resolve file monthly\ report.txt as @doc
```

Notes:
- single quotes `'...'` are also supported (content is treated as raw path without escaping).
- path cannot be `@variable`; path must be a literal token or quoted path.

---

## 5. Main supported commands (v1)

## 5.1 cd / pwd / ls (navigate and inspect file tree)

v1 introduces working directory (`cwd`):
- at start of each chunk execution, `cwd` defaults to `/`
- `Cd` changes current `cwd`
- relative paths (not starting with `/`) resolve against `cwd`

```satis
Pwd
Cd /docs
Pwd
Ls
Ls ../output
```

`Ls` writes to stdout (console), one entry per line:

```text
dir  subdir
file a.txt
```

---

## 5.2 Commit / Rollback (session / transaction segment control)

In interactive mode or full chunk, you can explicitly write:

```satis
Commit
Rollback
```

Semantics:
- `Commit`: commit modifications in current transaction segment and start a new one; `cwd` and variable environment are preserved
- `Rollback`: roll back modifications in current segment and restore `cwd` and variable environment to segment start

Use cases:
- checkpoint control for line-by-line TUI execution
- split full chunk script into multiple committed segments

---

## 5.3 Resolve / Read

### Resolve single file or folder

```satis
Resolve file ./path/file.txt as @f
Resolve folder ./path/folder as @dir
```

### Resolve multiple files (glob)

```satis
Resolve file ./docs/*.txt as @doc_list
```

### Read

**Read from resolved file object** — first operand is an `@` file-object variable:

```satis
Read @f as @text
Read @f lines 1 20 as @snippet
```

Notes:
- `lines` is **only supported** in `Read @object ... lines ...`; no `Read PATH lines ...` form.

**Directly read text by path (document-oriented)** — first operand is a path token. Runtime resolves this path; it must resolve to a **regular file**, not a directory:

```satis
Read ./path/to/file.txt as @text_var
```

Relation with `Read @f ...`:
- each `Read` is **either-or**: source is either `@variable` or `PATH`.

### 5.3.1 Resolve and host boundary (important)

- `Resolve` and path-form `Read PATH ...` only resolve **VFS-managed objects** (with formal `file_id` semantics).
- They do **not** lazily import host files just because same-name files exist in backend `mount_dir`.
- To **explicitly import** host text files into current VFS workspace, use **`Load`** (next section). Import source root is configured by **`system_port_dir`**, accessed read-only via sandbox **`SystemPort`** at runtime; VFS itself does not store host absolute paths.

### 5.3.2 Load (import text from system_port into VFS)

`Load` moves **plain text** between **system_port logical namespace** (root `/`, paths like `/prompts/foo.md`) and **VFS**. Unlike VFS `Cd`/`Ls`, `Load` maintains a separate **load current directory** (`load cwd`), browsed via `load pwd` / `load ls` / `load cd`.

**Navigation (changes only load cwd, not VFS)**

```satis
Load pwd
Load ls
Load ls /prompts
Load cd /prompts
```

**Import (writes to current VFS `cwd`, target filename is source basename)**

```satis
Load hello.md
Load prompts/intro.md ./other.txt
Load prompts/*.md
Load a.md b.md as @docs
```

Notes:

- **source path**: relative to **load cwd** or absolute path starting with `/` (both resolved inside system_port). Supports Linux-style wildcards `*`, `?`, `[]`; one statement can contain multiple sources, and wildcard results are deduplicated/sorted before import.
- **text only**: if any matched file is non-text (for example contains NUL), the **entire batch fails**, and all VFS changes in this batch are **rolled back** (consistent with chunk transaction semantics).
- **`as @var`**: optional. If omitted, only writes files without variable binding. For multi-file imports with `as @var`, runtime binds as **`@var_0000`, `@var_0001`, ...** (similar to multi-file `Resolve file ... glob` convention).
- **target conflict**: if multiple sources have the same resolved **basename**, or target path is already a **directory** in VFS, command fails and batch is rolled back.

Configuration: set top-level **`system_port_dir`** in **`vfs.config.json`** (supports `~` expansion) to a read-only host root for imports. If missing/unavailable, `Load` operations fail.

---

## 5.4 Writing and file operations

### Create file / Create folder

```satis
Create folder ./out/reports as @dir
Create folder ./out/a ./out/b as @dirs
Create file ./out/reports/summary.md as @file
Create file ./out/reports/draft.md with [[[initial content]]] as @draft
Create file ./out/reports/empty.txt
Create file ./out/a.txt ./out/b.txt with [[[same initial content]]] as @files
```

Notes:
- `Create folder PATH [as @var]`
- `Create folder PATH [PATH ...] [as @var]`
- `Create file PATH [PATH ...] [with [[[init text]]]] [as @var]`
- `Create file` defaults to empty text file
- `Create folder` is idempotent when target already exists
- `Create file` errors if target exists to avoid accidental overwrite
- `as @var` is optional; omitted means side effect only, no return binding
- when creating multiple files/folders with `as @var`, the variable is bound to an **object list**
- `Create file` with `with [[[init text]]]` applies same initial text to all target files in that statement

### Write

```satis
Write [[[fixed text]]] into ./out/a.txt [as @out_a]
Write @text into ./out/b.txt [as @out_b]
```

Notes:
- in v1, `as @var` for `Write` is **optional**
- when omitted, write still happens, but **output object is not bound** to any variable (equivalent to discarding return value)
- if `Write` value is a **text list**, you can use path template for batch writes
- path template supports placeholders like `{i}` or `{i:04d}`

```satis
Write @result_list into ./out/result_{i:04d}.txt
Write @result_list[-2:] into ./out/result_{i}.txt [as @written]
```

Additional notes:
- text list slicing supports negative indices
- batch write index starts from `0`
- if writing text list without `{i}` / `{i:0Nd}` placeholder in target path, runtime errors to avoid accidental overwrite into one file

### Copy / Move / Rename / Delete / Patch

```satis
Copy @out_a to ./out/a_copy.txt [as @copy_a]
Move @copy_a to ./out/archive/a_copy.txt [as @moved_a]
Rename @moved_a to ./out/archive/a_final.txt [as @renamed_a]
Patch @renamed_a replace [[[old]]] with [[[new]]] [as @patched_a]
Delete file @patched_a
Delete file ./out/archive/old.txt
Delete file @a @b ./out/archive/old1.txt ./out/archive/old2.txt
Delete folder ./out/archive/tmp*
```

Notes:
- `as @var` for `Copy` / `Move` / `Rename` / `Patch` is also **optional**
- `Delete` has no `as` and produces no new object binding
- `Delete` must explicitly declare target type: `Delete file ...` or `Delete folder ...`
- `Delete` supports batch deletion: mix multiple object variables and paths in one statement
- path source supports Linux-style wildcards: `*`, `?`, `[]` (for example `Delete file ./logs/*.txt`)
- if object is needed later (for example `Read @out_xxx`), you must keep it via `as @out_xxx`
- `Delete PATH` deletes the currently resolved VFS virtual-path object
- once deletion is committed, there is **no language-level undo/restore**; only transaction rollback on chunk failure can revert it

---

## 5.5 Invoke (single) and concurrent Invoke (batch)

### Single Invoke

Full form (prompt + input + variable binding):

```satis
Invoke [[[Please summarize]]] with @text as @summary
Invoke [[[Please translate to English]]] with [[[Hello world]]] as @en
Invoke @prompt_text with @system_hint as @out
```

`prompt` supports any **Value**:

- triple-bracket text: `[[[...]]]`
- variable: `@var`
- variable selector: `@list[i]`, `@list[:n]`, `@list[a:b]`

Optional shorthand (v1):

- **prompt only**: omit `with`, input is empty string; if `as` is omitted, runtime prints model return to stdout (convenient in TUI/console; if underlying Invoker supports streaming, it streams output as much as possible)
- **prompt + bind output + no input**: `Invoke VALUE as @out` (equivalent to empty input)
- **prompt + input + no binding**: `Invoke VALUE with VALUE` (result printed to stdout)

Message assembly semantics (current implementation):
- **one source only** (no `with`): `VALUE` after `Invoke` is **user prompt**
- **two sources** (with `with`): `VALUE` after `Invoke` is **user prompt**, `VALUE` after `with` is **system prompt**

```satis
Invoke [[[hello]]]
Invoke @prompt_text as @polished
Invoke @prompt_lines[:2] with @sys_text
```

Clause order: if `with ...` exists, it must come before `as ...` (same as full form).

### Concurrent batch Invoke (recommended)

```satis
Invoke [[[Please summarize in one sentence]]] concurrently with @content_list as @summary mode separate_files
Invoke @prompt_text concurrently with @content_list as @summary
Invoke @prompt_lines[:2] concurrently with @content_list as @summary
```

Notes:
- common source for `@content_list`: `Resolve file <glob>` + `Read`
- prompt in concurrent invoke also supports `VALUE` (including variables/selectors)
- `mode` only allows `separate_files` / `single_file`
- if omitted, default mode is `separate_files`
- if prompt variable resolves to object/object-list, runtime errors and asks to `Read` into text first

---

## 5.6 Print (debug output)

Used to print content to stdout (console), without creating new variables or writing VFS:

```satis
Print @text_var
Print [[[one line\nsecond line]]]
Print @content_list[:3]
Print @content_list[-1]
```

When printing variables (runtime behavior):
- text variable: print full string
- text list: concatenate segments with newlines and print
- file object / object list: print summary (virtual path, `file_id`, type, generation, etc.); does not auto-`Read` full content

List selector syntax (Python-like):
- `@list`: print all objects/text by default
- `@list[i]`: single index, negative supported
- `@list[:n]`
- `@list[a:b]`

Rules:
- out-of-range slices are **truncated automatically, no error**
- out-of-range single index raises error

---

## 5.7 Software invocation and registry management

### 5.7.1 software invocation syntax

```satis
sorting sort --numbers [[[1,3,2]]] --order [[[asc]]] as @result
sorting sort --numbers @nums --order [[[desc]]] as @result
```

Unified form:

```text
<software_name> <function> [--flag <value> ...] [as <@var>]
```

`<value>` can only be:

- `@var`
- `[[[string]]]`

### 5.7.2 Software management statements

```satis
Software ls
Software find sor as @found
Software describe sorting as @desc
Software functions sorting as @funcs
Software refresh as @report
```

Currently supported subcommands:

- `ls`
- `pwd`
- `cd`
- `find`
- `describe`
- `functions`
- `refresh`

`Software refresh` semantics:

- scan `software_registry_dir`
- recognize only compliant software (`SKILL.md` frontmatter must include `name`, `description`, and `name` must match `forsatis.json`)
- write compliant software descriptions back to `## Entries` in parent folder `SKILLS.md`
- return statistics: `refreshed folders / recognized / skipped`

Notes:

- non-compliant software is skipped and not added to registry
- `Software uninstall` is not supported

---

## 6. Common variable naming conventions (frequently used by upper layers)

In chunks generated by planner/materializer, you often see:

- `@in_<port>_file`: file object for input port
- `@in_<port>_text`: text for input port
- `@out_<port>`: output port object

This is not syntax-mandated, but an upper-layer convention.  
Using this naming has best readability and helps input-binding validation.

---

## 7. Most common pitfalls in v1

### 1) Forgetting `@`

Wrong:

```satis
Write summary_0000 into ./out/0.txt as @out_0
```

Correct:

```satis
Write @summary_0000 into ./out/0.txt as @out_0
```

### 1.1) Without `as`, output object cannot be referenced

The following fails because `Write` does not bind output object to `@out`:

```satis
Write [[[hello]]] into ./a.txt
Read @out as @text
```

Correct:

```satis
Write [[[hello]]] into ./a.txt as @out
Read @out as @text
```

### 2) Wrong `mode` in `invoke concurrently`

Only `separate_files` / `single_file` are accepted.

### 3) Missing header

Missing `chunk_id` or `intent_uid` fails validation.

### 4) Unsupported operation on folder

For example, `Read @folder as @text` is rejected during validation.  
For `Read <path> as @text`, if the path resolves to a **directory**, runtime fails.

### 5) Unsupported escapes inside triple brackets

Only `\n`, `\t`, `\\` are allowed.

### 6) Path contains spaces but not quoted/escaped

Wrong:

```satis
Write @x into ./bad path.txt as @out
```

Correct:

```satis
Write @x into "./bad path.txt" as @out
```

### 7) Assuming `Resolve` auto-reads unmanaged host files in `mount_dir`

`Resolve` only recognizes **VFS-managed objects**. Host files newly placed in `mount_dir` that are not yet imported into VFS via `Create` / `Write` / `Load` will **not** be found by `Resolve`. Use **`Load ...`** to import text from configured **`system_port_dir`**.

### 8) Software directory exists but `Software find` cannot see it

Common reason: directory `SKILL.md` is non-compliant (missing frontmatter or invalid `name/description`).  
After `Software refresh as @report`, inspect `skipped` in `@report` to confirm whether it was skipped.

---

## 8. Example: batch process 5 files

```satis
chunk_id: CHK_BATCH
intent_uid: intent_batch

Resolve file ./test_batch/docs/*.txt as @doc_list
Read @doc_list as @content_list
Invoke [[[Please summarize in one sentence]]] concurrently with @content_list as @summary mode separate_files
Write @summary_0000 into ./test_batch/output/summary_0.txt as @out_0
Write @summary_0001 into ./test_batch/output/summary_1.txt as @out_1
Write @summary_0002 into ./test_batch/output/summary_2.txt as @out_2
Write @summary_0003 into ./test_batch/output/summary_3.txt as @out_3
Write @summary_0004 into ./test_batch/output/summary_4.txt as @out_4
```

---

## 9. How to tell parse error vs execution error

- **parse error (Parse)**: illegal syntax shape (for example old path triple-bracket style, unclosed quotes, unsupported `\` escapes in triple-bracket values)
- **validation error (Validate)**: illegal semantics (missing header, invalid mode, etc.)
- **execution error (Execute)**: runtime problems (undefined variable, file missing, invoke failure, etc.)

Debug suggestions:
1. first check whether `submit_chunk_graph` is accepted  
2. then check chunk error code/message in `inspect_run`  
3. finally locate exact instruction line  

Additional notes:
- in session/TUI mode, flow is usually "ParseBody first, then interpret line-by-line or block-by-block"
- in full-chunk mode, flow is still "Parse full chunk first, then execute as a whole"

---

## 10. One-line memory aid

SatisIL can be understood as:  
**"browse VFS with `Cd/Pwd/Ls`; get objects with `Create/Resolve`; import host text into VFS from system_port with `Load` when needed; get text with `Read`; debug with `Print` when needed; process with `Invoke`; finally persist with `Write/Copy/Move/Rename/Delete/Patch`."**

---

## 11. How chunks are connected (conceptual)

Bottom line first:  
**chunks are not connected by direct "jump" in SatisIL text, but by the plan graph (`ChunkGraphPlan`).**

Connection has two layers:

1) **scheduling connection (who runs first)**: `edges` / `depends_on`  
- for example `A -> B` means B starts after A completes
- this is control-flow dependency, not variable argument passing

2) **data connection (what is passed)**: `inputs.handoff_inputs` + naming conventions  
- upstream chunk outputs `@out_<port>`
- downstream chunk consumes via `@in_<port>_file` / `@in_<port>_text`

You can think of it as:
- `edges` define **order**
- `handoff` defines **data-object mapping**

---

## 12. Handoff mechanism in detail (important)

### 12.1 Handoff shape in plan

In downstream chunk `inputs.handoff_inputs`, you usually have:

- `from_step`: which upstream chunk it comes from
- `from_port`: which upstream output port
- `virtual_path`: path hint (for declaration/fallback)

Example (incomplete JSON):

```json
{
  "inputs": {
    "handoff_inputs": {
      "draft": {
        "from_step": "CHK_UP",
        "from_port": "draft",
        "virtual_path": "./examples/output/upstream__draft.txt"
      }
    }
  }
}
```

Here `"draft"` is the downstream input port name.

### 12.2 How to write downstream SatisIL

**Recommended (object reference + read text, most consistent with handoff object tracking):**

```satis
Resolve file ./examples/output/upstream__draft.txt as @in_draft_file
Read @in_draft_file as @in_draft_text
Write @in_draft_text into ./examples/output/final.txt as @out_final
```

**Equivalent alternative (path read only, when path equals `virtual_path`):** if plan validation requires input port path to match declaration, you can also write:

```satis
Resolve file ./examples/output/upstream__draft.txt as @in_draft_file
Read ./examples/output/upstream__draft.txt as @in_draft_text
Write @in_draft_text into ./examples/output/final.txt as @out_final
```

During debugging, you can add one line after read (does not affect handoff object logic, and plan validation treats `Print @in_draft_text` as consuming that input text):

```satis
Print @in_draft_text
```

Upper-layer convention usually requires:
- `@in_<port>_file` for input object
- `@in_<port>_text` for input text

### 12.3 Is runtime passing by path or object ID?

Current mechanism:

1. upper layer still provides paths (readable and declarative)
2. runtime entry first resolves handoff into **object references** (essentially `file_id`)
3. during downstream execution, `@in_<port>_file` reuses that object reference with priority
4. only if upstream object cannot be found, fallback to `virtual_path` and resolve again

This is why downstream can usually keep tracking the same object even if upstream file was renamed.

### 12.4 Why this is more robust

- paths can change (rename, directory move)
- `file_id` is stable object identity
- passing by `file_id` reduces failures where downstream still uses stale paths

### 12.5 Handoff and retry

On chunk retry, runtime re-runs entry binding logic.  
As long as upstream object still exists, retry can continue consuming via object reference without depending on old path lookup.

---

## 13. One diagram for chunk connection

```text
[Chunk A]
  Write ... as @out_draft
      |
      |  Scheduling layer: A -> B (edge)
      |  Data layer: handoff_inputs { port=draft, from_step=A, from_port=draft, ... }
      v
[Chunk B]
  Resolve ... as @in_draft_file   (runtime prefers binding to upstream object file_id)
  Read @in_draft_file as @in_draft_text
  ...
```

In one sentence:  
**scheduling uses edges, data uses handoff; declaration is path-based, execution prioritizes object ID.**
