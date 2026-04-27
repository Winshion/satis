# Satis: A Possible Paradigm Shift

<h3 align="center">If one wishes to understand all Buddhas of the past, present, and future, one should contemplate the nature of reality: all is mind-made. — *Avatamsaka Sutra*</h3>

> Preface: This repository is built as my personal side project and is unrelated to any research projects or topics from any school or company. I am currently a graduate student with my own research workload, so updates will be irregular based on availability. Thank you for your support.
>
> You can use various agents to help you understand and run Satis. I specifically wrote a `README_AGENT.md`, and you can directly ask your agent to read this document first!


The name `Satis` comes from the Buddhist idea of “mindfulness” (`Sati`). I interpret it as an engineering method: keep intention clear, and make action observable. Inspired by this line from the *Avatamsaka Sutra*, it led me to a core judgment: in complex tasks, what is truly worth accumulating may be reusable intention structures and reasoning paths. For this reason, Satis tries to push one step beyond the existing harness paradigm: not only orchestrating “model + tool” calls, but also structuring intentions, branches, constraints, and retrospection mechanisms in the process, so workflows can explore dynamically while still converging into auditable execution before runtime. Along this direction, I propose a layered vision of “software + smartware”: **software as stable, replaceable fixed computation and control units; smartware as reusable, evolvable reasoning units**, carrying experiential assets such as “what to do when, why this path, and how to reroute after failure.” There is also a naming pun: the process of turning software into “mindful thought units” can be playfully expressed as “Satisfy”!

From today’s AI Agent development perspective, Satis can be seen as a lightweight harness engineering system, and also as an AgentOS. It decouples reasoning from control (or, from another angle, the reasoning path becomes “meta-control” over immediate control), separating software from thought process.

In short: `smartware (on Satis) -> software (On Operating System) -> hardware`.

## Satis Quick Start (For First-Time Users)

Sorry to say: Satis does not support Windows at the moment. It is currently adapted only for Darwin (macOS) and Linux (please forgive me, TAT). If support becomes feasible, I will add Windows support as soon as possible.

---

## 0) Design Philosophy (Short Version)

`Satis` is a process system that turns complex tasks into editable, runnable, and auditable workflows. It started as a small inspiration during my engineering PhD journey, and became a personal project with a fairly complete conceptual shape (thanks to Vibe Coding! Though yes, the codebase definitely has some spaghetti parts, lol). This system will likely evolve slowly over time. If more people in the community find it useful, I will keep pushing it forward.

- **Local certainty, global growth**: `Chunk` defines “how this step works”, `Plan` defines “where to go next”.
- **Boundaries before capability**: internal VFS workspace and external OS files are bridged explicitly via `Load`, avoiding arbitrary cross-boundary access.
- **Explore first, converge later**: drafts are allowed during exploration; strict validation is enforced at `render/submit/run`.
- **Software vs reasoning-layer separation**: software is callable capability; Plan/Chunk in `Satis` are reusable reasoning structures, which is temporarily referred to as "smartware".

From these angles, `Satis` is closer to a “traceable AI workflow kernel” than a one-shot command executor.

---

The following steps are enough to get you started quickly.

## 1) Configure VFS

### 1.1 Copy config template

Run in repository root:

```bash
cp vfs.config.example.json vfs.config.json
```

### 1.2 Edit `vfs.config.json`

Example (replace with your own absolute paths):

```json
{
  "backend": "disk",
  "mount_dir": "/Users/you/Desktop/satis_backend/mounted",
  "state_dir": "/Users/you/Desktop/satis_backend/satis_vfs",
  "system_port_dir": "/Users/you/Desktop/satis_backend/system_port",
  "software_registry_dir": "/Users/you/Desktop/satis_backend/software_registry",
  "gc": {
    "deleted_file_retention": 0,
    "max_events": 2048
  }
}
```

### 1.3 What each directory does

- `mount_dir`: mounted view directory exposed by VFS.
- `state_dir`: VFS runtime state and event data.
- `system_port_dir`: OS file import entry point (read by `Load`).
- `software_registry_dir`: root directory of the software registry.

### 1.4 Security boundary note (important)

If you place all these directories under one parent path (for example, `/Users/you/Desktop/satis_backend`), Satis-managed data and governed assets will remain inside this boundary, and the system will not arbitrarily write outside it.  
External files must be imported explicitly through `system_port_dir + Load`.

---

## 2) Configure `invoke.config.json` (Connect Your Own API)

### 2.1 Copy template

```bash
cp invoke.config.example.json invoke.config.json
```

### 2.2 Fill with your model service settings

```json
{
  "providers": {
    "my-openai-compatible": {
      "base_url": "https://your-endpoint/v1/",
      "api_key": "sk-xxxx",
      "model": "your-model-name",
      "timeout_seconds": 120,
      "temperature": 0.2,
      "max_tokens": 4096
    }
  },
  "router": {
    "default_provider": "my-openai-compatible"
  }
}
```

Field meanings:

- `base_url`: your model gateway endpoint (OpenAI-compatible format).
- `api_key`: corresponding API credential.
- `model`: model name.
- `router.default_provider`: default provider key.

> Recommendation: do not commit `invoke.config.json` to public repositories.

---

## 3) Enter CLI (`runtui.sh`) + Fancy Examples

### 3.1 Start

```bash
./runtui.sh
```

The script runs preflight checks automatically (OS, Go, config files, smoke tests), then starts the TUI.

### 3.2 Platform support

Current `runtui.sh` supports:

- macOS (`Darwin`)
- Linux

**Windows is not supported yet.**

### 3.3 Run a few fancy examples first (line mode)

After connecting your own LLM provider, please note: Satis has no built-in prompts. All token usage comes from your own `Invoke` commands. You can start with the most basic command:

```text
invoke [[[hello, how are you?]]]
```

If your LLM provider is correctly configured, you should quickly see an LLM response in the console. To avoid ambiguity with model outputs, **strings are designed as `[[[strings]]]` using triple square brackets**.

After entering CLI, you will see `satis (line)>`. Then type (commands are case-insensitive; string content is case-sensitive):

```text
Pwd
Cd /demo
Write [[[你好，Satis。这里是一段待总结文本：Satis把流程分成可追踪步骤。]]] into note.txt as @f
ls
Read @f as @text
Invoke [[[请用一句话总结上面内容，并加一个标题emoji]]] with @text as @summary
Print @summary
Write @summary into /demo/summary.txt as @out
```

Another batch-style example (if your model and syntax setup are aligned):

```text
Write [[[苹果\n香蕉\n西瓜]]] into /demo/fruits.txt as @f2
Read @f2 as @fruits
Invoke [[[把每一行变成“水果名 + 一句8字卖点”]]] with @fruits as @ads
Print @ads
```

---

## 4) Workbench Guide (with minimal example)

### 4.1 Enter Workbench

In main CLI, type:

```text
/workbench /plans/demo a demo
```

If `/plans/demo/plan.json` does not exist, a minimal valid template will be created automatically.

### 4.2 Common operations (enough to start)

- `Tab / Shift+Tab`: switch focus (graph, editor, handoff, CLI).
- `Ctrl+S`: save (`save`).
- `Ctrl+Shift+R`: render and validate (`render`).
- `Ctrl+N`: add a task child node (with graph focused).
- `Shift+D`: add a decision child node.
- `Ctrl+P`: command palette.

### 4.3 Minimal Workbench demo

After entering, type in the right-side CLI:

```text
validate
run
status
events
inspect
```

This flow quickly verifies that plans can be submitted, executed, and observed.

---

## 5) Software Calling Guide: Design and Register a Simple Software Compatible with Satis

Goal: register a `sorting` software and call it in `Satis`.

### 5.1 Create directory under `software_registry_dir`

Example structure:

```text
software_registry/
  tools/
    sorting/
      forsatis.json
      SKILL.md
      sort.py
```

### 5.2 Write `SKILL.md` (frontmatter required)

```md
---
name: sorting
description: Sort comma-separated integers and return sorted text.
---
```

### 5.3 Write `forsatis.json` (declare function interface)

Fill in functions, parameters, and entry script according to your current runner conventions (keep fields aligned with existing runnable examples in this project).

### 5.4 Refresh registry in Satis

```text
Software refresh as @report
Print @report
Software ls
Software describe sorting
Software functions sorting
```

### 5.5 Call software

```text
sorting sort --input [[[3,1,2]]] as @sorted
Print @sorted
```

---

## 6) Operating System File Calling Guide (How to read OS files)

In `Satis`, OS files are not read arbitrarily. They are imported explicitly through `system_port_dir`.

### 6.1 Prepare OS file

Put your host file under configured `system_port_dir`, for example:

```text
<system_port_dir>/inputs/paper.txt
```

### 6.2 Import and use in Satis

```text
Load pwd
Load ls
Load inputs/paper.txt
Read /paper.txt as @paper
Invoke [[[请提取这篇文本的3个关键观点]]] with @paper as @k
Print @k
```

Key points:

- `Load` uses the `system_port_dir` namespace (external import zone).
- After import, files enter current VFS workspace and can continue through `Read/Write/Invoke`.

---

## 10-Minute Minimal Demo

1. Prepare configs:

```bash
cp vfs.config.example.json vfs.config.json
cp invoke.config.example.json invoke.config.json
```

2. Update path and API settings in both files.  
3. Start:

```bash
./runtui.sh
```

4. Run in CLI:

```text
Cd /demo
Write [[[Satis is a structured runtime for AI workflows.]]] into a.txt as @f
Read @f as @t
Invoke [[[Translate to Chinese in one sentence.]]] with @t as @zh
Write @zh into /demo/zh.txt as @out
Print @zh
```

5. Open Workbench:

```text
/workbench /plans/demo just to demonstrate it
```

Then input `just a plan` in the popup Plan description.

6. Press `Tab` multiple times until focus switches to the right-side Workbench CLI, then type:

```text
validate
run
inspect
```

If all steps above succeed, you have completed an end-to-end demo:  
**Configure VFS -> Connect model API -> Run in CLI -> Run plan in Workbench -> Observe results.**

---

## Common Questions (First Run)

- Startup fails with unsupported OS: currently only macOS/Linux are supported, not Windows.
- `Load` fails: check whether `system_port_dir` exists in `vfs.config.json`.
- `Invoke` fails: check `base_url/api_key/model/default_provider` in `invoke.config.json`.
- Workbench run fails: run `validate` in Workbench first, then fix handoff/graph structure.

---

## License

Copyright (c) 2026 Wilson Huang.

This project is licensed under the **Creative Commons Attribution-NonCommercial-ShareAlike 4.0 International License (CC BY-NC-SA 4.0)**.

To view a copy of this license, visit [http://creativecommons.org/licenses/by-nc-sa/4.0/](http://creativecommons.org/licenses/by-nc-sa/4.0/).

---

If you want to dive deeper, recommended docs:

- `documents/Documents_EN/instructions/02-Satis CLI Detailed User Guide.md`
- `documents/Documents_EN/instructions/03-Satis Software Integration and Registry Guide.md`
- `documents/Documents_EN/instructions/04-Satis Workbench User Guide.md`

Also, docs inside `documents/Documents_EN/satis_s_story` is also interesting!


> This README is mostly written by using LLMs, I myself prompted.