# Satis Software Integration and Registry Guide

Date: 2026-04-23  
Applicable version: current repository implementations in `satis/software_registry.go`, `satis/parser.go`, and `satis/runtime.go`

---

## 1. Goal and Scope

This document explains three things:

- How `Satis` discovers and invokes external software
- The directory structure and documentation conventions of `software_registry_dir`
- How `Software refresh` updates folder-level `SKILLS.md`

---

## 2. Configuration and Startup Behavior

Configure at the top level of `vfs.config.json`:

- `software_registry_dir`: root directory of the software registry (host-machine path)

Behavior at startup:

- If the directory does not exist, it is created automatically.
- If `SKILLS.md` is missing in the directory, a template is generated automatically.
- If `SKILLS.md` already exists, it is not overwritten.

Notes:

- Software installation, deletion, and directory organization are managed by users on the OS side.
- `Satis` does not provide an uninstall statement.

---

## 3. Registry Directory Conventions

Example:

```text
software_registry/
  SKILLS.md
  tools/
    SKILLS.md
    sorting/
      forsatis.json
      SKILL.md
      ...
```

Conventions:

- Each software directory must contain `forsatis.json`.
- Each software directory must contain `SKILL.md`.
- Each folder is recommended to contain `SKILLS.md` (automatically maintained by refresh).

---

## 4. Documentation Conventions (Anthropic Skills Style)

### 4.1 Software-level `SKILL.md` (strict requirements)

`SKILL.md` must start with frontmatter:

```md
---
name: sorting
description: Sort comma-separated integers and return sorted text.
---
```

Strict requirements:

- Must use frontmatter format (first line is `---`).
- Must include a non-empty `name`.
- Must include a non-empty `description`.
- `name` must match the software name in `forsatis.json`.

If requirements are not met:

- The software is skipped and not added to the registry.
- `Software refresh` will not write it into folder `SKILLS.md` either.

### 4.2 Folder-level `SKILLS.md` (maintained by refresh)

Folder-level docs also use frontmatter:

```md
---
name: tools
description: Utility tools index.
---

## Entries

- sorting: Sort comma-separated integers and return sorted text.
```

Notes:

- `name` / `description` are preserved by refresh when possible, or filled with defaults.
- `## Entries` is rebuilt based on scan results.

---

## 5. SatisIL Statements

### 5.1 software invocation

```text
<software_name> <function> [--flag <value> ...] [as <@var>]
```

`value` only supports:

- `@var`
- `[[[string]]]`

### 5.2 software management statements

Supported:

- `Software pwd`
- `Software cd <path>`
- `Software ls`
- `Software find <prefix>`
- `Software describe <name>`
- `Software functions <name>`
- `Software refresh [as @var]`

Not supported:

- `Software uninstall`

---

## 6. `Software refresh` Semantics

Execution flow:

1. Recursively scan `software_registry_dir`.
2. Validate and recognize compliant software (based on `SKILL.md` frontmatter rules).
3. Generate/update `SKILLS.md` in every folder level.
4. Refresh the in-memory software registry at runtime.

Returned report text (when using `as @var`):

- `refreshed folders=<n>`
- `recognized=<n>`
- `skipped=<n>`

Explanation:

- `recognized`: number of compliant software entries recognized in this refresh.
- `skipped`: number of non-compliant software entries (for example, invalid `SKILL.md` format).
- `refreshed folders`: number of folders whose file content changed and was written back.

---

## 7. Typical Operations Workflow

1. Add or adjust software directories on the OS side.
2. Ensure software-level `SKILL.md` frontmatter is compliant.
3. Execute in Satis/TUI:

```satis
Software refresh as @report
Print @report
```

4. Verify visibility with `Software ls` / `Software describe`.

---

## 8. FAQ

- **Q: Why does a software directory exist but cannot be invoked?**  
  A: Most commonly because `SKILL.md` is non-compliant (no frontmatter, missing `name/description`, or name mismatch).

- **Q: Will manual content in folder `SKILLS.md` be overwritten?**  
  A: refresh rebuilds the `## Entries` list; `name/description` in frontmatter are preserved when possible.

- **Q: Can software be uninstalled through Satis?**  
  A: No. The software directory lifecycle is managed on the OS side.
