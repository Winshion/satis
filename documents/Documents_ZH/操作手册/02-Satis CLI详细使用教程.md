# Satis CLI 详细使用教程

日期：2026-04-10  
适用版本：当前仓库 `cmd/satis-tui`、`tui/app`、`tui/workbench`、`tui/runtime` 实现

---

> 文轩注：该项目本来是想做成一个TUI，但是后面做着做着就觉得，还是CLI更适合，最后做成了一个CLI，但还是叫成「TUI」了。

## 1. 什么是 Satis CLI

`satis-cli` 是一个交互式命令行界面（REPL），在单个会话里连续执行 SatisIL，底层复用 `satis.Executor` 与配置的 VFS。适合：

- 边试边调（`Resolve` / `Read` / `Load` / `Invoke` / `Write` 小步验证）
- 用 Tab 补全快速输入命令与虚拟路径
- 打开 **工作台（Workbench）** 编辑 `bridge` 的 Chunk Graph 计划（JSON），并走 **bridge 全链路** 提交与运行
- 临时排查 VFS、invoke、路径解析行为

当前计划运行模型已不再局限于静态 DAG。运行层当前实际落地的是两类运行节点：

- **`task`**：普通执行节点
- **`decision`**：控制流分支节点
- **规划动作（planning）**：通过 Workbench 的 planning 命令串联多份 plan 文档；在父图中会出现 `Next Plan`（以及子图中的 `Return Plan`）导航节点

---

## 2. 启动方式

### 2.1 常用启动命令

```bash
go run ./cmd/satis-tui --config vfs.config.json --invoke-mode openai --invoke-config invoke.config_local.json
```

或使用仓库脚本：

```bash
./runtui.sh
```

### 2.2 启动参数说明

| 参数 | 含义 |
|------|------|
| `--config` | VFS 配置路径（默认 `vfs.config.json`） |
| `--invoke-mode` | `error` \| `echo` \| `prompt-echo` \| `openai` |
| `--invoke-config` | 独立 invoke 配置；若指定，优先于 `--config` 内嵌的 `invoke` |
| `--initial-cwd` | TUI 会话初始虚拟工作目录（默认 `/`） |
| `--chunk-id` | 交互会话使用的 chunk_id（默认 `TUI_REPL`） |

### 2.3 `vfs.config.json` 与 `system_port_dir`（可选）

除 `backend`、`mount_dir`、`state_dir`、`gc` 等 VFS 字段外，顶层可配置 **`system_port_dir`**（字符串路径，支持 `~` 展开）：指向宿主机上 **`Load` 指令只读导入** 的根目录。程序启动时会解析该路径并交给沙箱 **`SystemPort`**；**VFS 状态里不存放宿主机绝对路径**。未配置或目录不可用时，执行 `Load …` 会报错。

仓库示例见 `vfs.config.json` 中的 `system_port_dir` 项。

### 2.4 `software_registry_dir`（software 注册表）

`vfs.config.json` 顶层可配置 `software_registry_dir`（字符串路径，支持 `~` 展开）：

- 指向 software 注册表根目录（宿主机目录）
- 启动时若目录不存在会自动创建
- 启动时若缺少 `SKILLS.md` 会自动生成模板

注意：

- 软件安装/删除由操作系统侧管理目录完成
- TUI/Satis 内不提供 uninstall 指令

---

## 3. 提示符与输入模式

提示符为加粗显示：

- `satis (line)>` — **line 模式**（默认）
- `satis (chunk)>` — **chunk 模式**，正在等待你输入要缓冲的行
- `satis (chunk*)>` — 已开始收集 chunk（例如执行了 `/begin`），缓冲未提交

### 3.1 line 模式

- 每一行**普通输入**（不以 `/` 开头）视为一条 SatisIL，**输入后立即执行**。
- 执行成功后，会话会自动 **Commit**（显式的 `Commit` / `Rollback` 指令除外）。

### 3.2 chunk 模式

- 普通输入先进入**缓冲区**，不会立即执行。
- 用 **`/commit`** 一次性执行缓冲区中的完整 body（多行当作一个 chunk）。
- 用 **`/cancel`** 丢弃缓冲区。
- 也可用 **`/begin`** 在任意模式下显式开始收集，再用 **`/commit`** 结束。

> **注意**：当前实现使用 **`/commit`** 提交缓冲内容，**没有** `/end` 命令。若文档或其它材料里写过 `/end`，请以代码与 `/help` 为准。

---

## 4. 斜杠命令（主 TUI）

输入 **`/help`** 可查看内置帮助。下面按类别整理。

### 4.1 通用

| 命令 | 作用 |
|------|------|
| `/help` | 显示帮助 |
| `/history` | 显示当前 TUI 进程内的输入历史 |
| `/exit` | 退出；若有未提交的 chunk 缓冲，需先 `/commit` 或 `/cancel` |

### 4.2 显示与会话

| 命令 | 作用 |
|------|------|
| `/clear` | 仅清屏，**不改变** VFS / 会话状态 |
| `/clearchunk` | 丢弃未提交的交互状态，并**重置当前 session**（相当于新开一局 REPL 会话） |

### 4.3 输入模式与缓冲

| 命令 | 作用 |
|------|------|
| `/mode line` | 切换为 line 模式 |
| `/mode chunk` | 切换为 chunk 模式 |
| `/begin` | 显式开始收集 chunk（任意模式下可用） |
| `/commit` | 执行已缓冲的 chunk body |
| `/cancel` | 丢弃已缓冲的 chunk body |

### 4.4 文件与 Workbench 目录

| 命令 | 作用 |
|------|------|
| `/exec PATH.satis` | 从**主机文件系统**读取 `.satis` 并执行（路径可为相对当前 shell 工作目录的相对路径） |
| `/workbench VFS_WORKSPACE_DIR` | 打开全屏 **三栏工作台**。若目录下没有 `plan.json`，会自动创建目录并生成默认计划模板（见第 7 节） |

### 4.5 line 模式：`/plan`（校验、运行、观测）

以下命令均在**主 TUI**（非 Workbench 全屏界面）中使用。参数里的路径均为 **VFS 路径**（相对当前虚拟 `cwd` 或 `/...` 绝对路径）。

**路径约定**

- 若参数是**目录**（如 `/plans/demo`），则计划文件视为 **`<目录>/plan.json`**。
- 若参数以 **`.json` 结尾**，则视为**显式的 plan 文件路径**（如 `/plans/demo/plan.json`）。

**计划约束**

- 计划必须通过 `bridge.ValidateChunkGraphPlan`；且 **`entry_chunks` 必须恰好 1 个**（单主入口 DAG）。不满足时 `validate` / `run` 会报错。
  - 说明：这是 `/plan ...`（line 模式）与 **实际执行/调度** 的硬约束；但 **Workbench 编辑/保存** 支持 invalid draft（见第 6.4 / 7.3）。

| 命令 | 作用 |
|------|------|
| `/plan validate <目录或 plan.json>` | 读取并校验计划，不写盘、不运行；输出中会含 `plan_id`、`entry_chunk`、chunk 数量等摘要。 |
| `/plan run <目录或 plan.json>` | 提交并 **StartRun**：与当前 REPL **共用 VFS**，但使用**独立 Session** 与**新的 `chunk_id` 前缀**克隆计划后再跑，避免与 `TUI_REPL` 会话混用。成功时摘要中含 **`run_id`**、**`effective_plan_id`**（可能与磁盘上 `plan_id` 不同）。 |
| `/plan status [run_id]` | 查看某次 plan run 的**轻量状态**（`run_id`、`status`、`updated_at`）。**省略 `run_id`** 时，使用本 TUI 进程中**最近一次** `/plan run` 的 run。 |
| `/plan inspect [run_id]` | 查看某次 run 的**完整快照**：汇总行会包含 `plan_id`、`graph_revision`、`task/decision` 数量、成功/失败/阻塞数量、`primary_failure` 等；随后输出每个 chunk 一行状态。省略 `run_id` 时同上，用最近一次 `/plan run`。 |
| `/plan events [run_id]` | 对某次 run **增量拉取事件**：只打印**自上次调用以来尚未输出过**的 `StreamRunEvents` 记录；重复执行可轮询进度。run 进入终态后可能附带 `run_id=... is terminal`。省略 `run_id` 时同上。 |

**与 Workbench 内 `run` / `status` 等的区别**

- Workbench 里：`run`、`status`、`inspect`、`events` 针对的是**你在工作台里**最近一次提交启动的 run（见第 7.3 节）。
- 从当前版本起，`inspect` 汇总还会显示 **`graph_revision`** 以及 `task / decision` 数量，便于观察规划动作是否已把新的 fragment 接入现有运行图。
- 主 TUI 里：`/plan run` 及 `/plan status|inspect|events` 自成一套上下文，**只记录 line 模式下的 `/plan run`**，与 Workbench 内的 `lastRunID` **互不覆盖**。
- `/plan run` 后，在 `inspect` / `events` / bridge 输出里看到的 **chunk_id 会带 `LINE_RUN_...` 前缀**，与 `plan.json` 里原始 id 不同，属预期行为。

**使用顺序建议**

1. `/plan validate /plans/demo`（可选，改 plan 后先校验）  
2. `/plan run /plans/demo`（记下输出中的 `run_id`，或依赖默认「最近一次」）  
3. 运行中多次：`/plan events`（或 `/plan status` 看是否已结束）  
4. 结束后：`/plan inspect` 看各 chunk 最终状态  

**注意**

- 在 **`/begin` 收集 chunk 未提交**时，不能使用 `/plan ...`（需先 `/commit` 或 `/cancel`）。
- `/plan ...` **不会**触发 line 模式下的自动 `CommitSession`（与单行 SatisIL 不同）。

---

## 5. Tab 键补全

在输入行中按 **Tab** 会触发 readline 自动补全，行为接近常见 Unix shell：

- **TUI 斜杠命令**（如 `/help`、`/workbench` …）
- **SatisIL 指令名**（如 `Cd`、`Read`、`Write`、`Load` …）
- **虚拟路径**：基于当前 VFS `cwd` 下列出的目录与文件；目录补全常带尾部 `/`
- **`Load` 指令**：在 `Load` / `Load cd` / `Load ls` / `Load <源路径>` 等位置，按 **load cwd** 下列出 **system_port 逻辑路径** 下的目录与文件名（目录名带 `/` 后缀）。补全数据来自会话的 `ListLoadDir`，**不经过 VFS**，也不会把宿主机绝对路径写进 VFS。
- **`/exec` 后的主机路径**：按宿主当前工作目录做路径补全

补全插入的是**当前词的后续片段**（suffix），避免出现「前缀被重复拼接」的问题。

说明：**通配符模式**（如 `prompts/*.md`）的 Tab 补全不做 glob 展开，仅对「当前正在输入的路径前缀」做目录/文件名前缀补全；完整语义仍以 SatisIL `Load` 运行时为准。

---

## 6. VFS 与路径（TUI 使用者须知）

### 6.1 同一路径可同时存在「目录」与「文件」

虚拟路径支持 **按类型区分**：同一字符串路径下可以同时有目录对象和文本文件对象。典型影响：

- **`Cd projects`** 会解析为**目录** `projects`
- **`Read projects`**（或 `Resolve file …`）会按**文件**语义解析
- **`ls`** 列表中，**目录名带尾部 `/`**，便于与同名文件区分

### 6.1.1 `Load` 与 VFS 路径不是同一套命名空间

- **`Cd` / `Ls` / `Read`（路径形式）** 等操作的是 **VFS 虚拟路径**（相对当前 VFS `cwd`）。
- **`Load pwd` / `Load ls` / `Load cd` / `Load <源>`** 浏览与读取的是 **`system_port_dir` 下的逻辑树**（独立的 **load cwd**）。导入成功后，内容会写入 **当前 VFS `cwd`** 下、文件名为源 basename 的位置。
- **`Resolve`** 只解析 **已纳管的 VFS 对象**，不会因为 `mount_dir` 里存在未纳管宿主文件就自动导入。

### 6.2 `Delete all` 与目录

`Delete all <目录>` 会按列表递归删除其下内容；若某层仅为「合成目录」（没有单独目录对象），实现上会对「删目录自身」容忍不存在，避免误报 `file not found` 并尽量删净子项。

### 6.3 计划文件放在哪

`/workbench` 的参数是 **VFS 工作目录路径**（相对当前 `cwd` 或绝对路径 `/...`）。当前实现默认将计划文件固定为该目录下的 `plan.json`。

- 若目录不存在：会先自动逐级创建目录
- 若 `plan.json` 不存在：会自动生成一份最小合法的 `bridge` 计划模板
- 若 `plan.json` 已存在：直接加载，不覆盖

### 6.4 一个 workbench 工作目录里「必要的文件」有哪些？

以 `/workbench /plans/demo` 为例，workbench 的工作目录是 `/plans/demo/`。当前实现下：

- **必需（workbench 能打开所需）**：`/plans/demo/plan.json`
  - **格式**：`bridge.ChunkGraphPlan` 的 JSON
  - **内容要求（打开/编辑）**：只需是可反序列化的 JSON；**允许是 invalid draft**（不通过 `bridge.ValidateChunkGraphPlan` 也能打开/编辑/保存，用于 IDE 式草稿工作流）
  - **内容要求（提交/运行）**：`submit` / `run` / `runselected` / `render` 会触发 `bridge.ValidateChunkGraphPlan`；不通过时会被拦截并在 Diagnostics/日志中提示
  - **冷启动行为**：如果 `plan.json` 不存在，会自动创建一份「最小合法模板」（单根 chunk、无 edges、无 handoff）

- **必需（目录本身）**：`/plans/demo/`（目录对象）
  - **冷启动行为**：如果目录不存在，会自动逐级创建

- **非必需（可选，按你的工程习惯组织）**
  - **输出目录**：如 `/plans/demo/out/`（你在 chunk 里 `Write ... into /plans/demo/out/...` 时会自动产生）
  - **输入/素材目录**：如 `/plans/demo/inputs/`、`/plans/demo/assets/`
  - **运行产物/工件**：是否落在 `artifacts` 或其它路径由你的 plan/chunk 决定；workbench 不强制要求这些目录存在

### 6.5 Software 注册表管理（非 Workbench）

在主 TUI 的 SatisIL 输入中可使用：

- `Software ls`
- `Software pwd`
- `Software cd <path>`
- `Software find <prefix>`
- `Software describe <name>`
- `Software functions <name>`
- `Software refresh [as @var]`

`Software refresh` 作用：

- 扫描 `software_registry_dir`
- 仅识别合规 software（`SKILL.md` frontmatter 含 `name`/`description` 且与 `forsatis.json` name 一致）
- 自动回写各级文件夹 `SKILLS.md` 的 `## Entries`
- 输出 `refreshed folders / recognized / skipped` 统计

示例：

```satis
Software refresh as @report
Print @report
```

---

## 7. 工作台（Workbench）

`/workbench <VFS工作目录>` 会进入全屏界面（基于 `tview`），与主 REPL **不同时显示**。退出工作台后回到原 TUI。

### 7.1 布局（上 · 左 · 中 · 右）

- **顶部**：状态栏（当前 `plan` 路径、选中 chunk、dirty 状态、最近一次 run、校验状态）

- **左侧**：当前计划的 **ASCII Chunk Graph**（统一工作流图画布，非树）。节点内显示 `in/out` handoff 变量摘要；当前选中 chunk 使用亮蓝色文字与边框高亮；父节点向下分叉起点用黄色 `+` 标识。节点标题前会显示 **`T / D`** 表示 `task / decision`；`decision` 节点会显示 `branches:...` 摘要。
- **中间**
  - 上：**Chunk 编辑器**（当前 chunk 的 `satis_text`）
  - 下：**Handoff** 表格 + 表单 + JSON 预览（编辑 `inputs.handoff_inputs`）
- **右侧**
  - 上：**CLI 日志**
  - 中：**Diagnostics**（当前 chunk 或全局的校验错误摘要）
  - 下：**命令输入框**（输入命令后回车执行）

### 7.2 键盘与焦点

| 按键 | 作用 |
|------|------|
| **Tab** / **Shift+Tab** | 在图、编辑器、表格、表单、CLI 输入等焦点间循环 |
| **Option+1..4**（macOS）/ Alt+1..4 | 快速聚焦 Chunk Graph / Chunk 编辑器 / Handoff / CLI |
| **↑ / ↓ / ← / →**（Chunk Graph 聚焦时） | 在图上移动当前选中 chunk（**上父、下子、左右同层**） |
| **Enter**（Chunk Graph 聚焦时） | 确认当前图选中节点并加载到中间编辑区 |
| **Ctrl+S** | 等价于执行 `save` |
| **Ctrl+Shift+R** | 等价于执行 `render` |
| **Ctrl+P**（或 **Ctrl+Space**） | 打开命令面板（Palette） |
| **Chunk Graph 焦点下 Ctrl+N / Ctrl+K** | 新增 `task` 子 chunk / 删除当前 chunk |
| **Chunk Graph 焦点下 Shift+D** | 新增 `decision` 子 chunk |
| **Chunk Graph 焦点下 Ctrl+T** | 在当前节点上循环切换 `task -> decision -> task` |
| **Handoff 表焦点下 Ctrl+N / Ctrl+K** | 新增 / 删除 handoff 端口 |
| **supplementary 表焦点下 Ctrl+K** | 删除当前 key/value 行 |
| **Esc** | 若无未保存修改则退出工作台；若有未保存会提示先 `save` 或 `quit!` |

也可用命令：`focus left|chunk|handoff|cli` 直接切焦点。

### 7.2.1 高效率结构编辑

- 在 **Chunk Graph** 上按 **`Ctrl+N`** 会以当前节点为父节点新增一个子 chunk。
- 在 **Chunk Graph** 上按 **`Shift+D`** 会以当前节点为父节点新增一个 `decision` 子 chunk。
- 若父 chunk 的正文里已经有 `@out_<port>__<var_name>`，Workbench 会自动：
  - 给新 chunk 预填一条 `handoff`（`from_step` / `from_port` / `var_name`）
  - 在新 chunk 正文里生成 `Print @in_<port>__<var_name>` 脚手架
- 按 **`Ctrl+K`** 删除当前 chunk 时，仅允许删除**非根且无子节点（leaf）**的 chunk。删除成功后会优先选中剩余图中的**最深叶子节点**，并一并清理：
  - 指向该 chunk 的边
  - 下游 `handoff_inputs` 中引用该 chunk 的 `from_step`
  - 再重算 `edges` / `depends_on` / `entry_chunks`

### 7.2.2 节点类型切换

- 在 **Chunk Graph** 上按 **`Ctrl+T`** 可循环切换当前节点类型：
  - `task -> decision -> task`
- 切换时保留 `chunk_id`，但会按目标类型补默认定义：
  - `task`：保留/补 `satis_text`
  - `decision`：补默认分支与交互定义
- 若把已有正文的 `task` 切换为控制节点，其正文会被清空为结构化控制定义。

### 7.2.3 命令面板（Palette）

- 按 **`Ctrl+P`** 打开命令面板，可模糊搜索：
  - `render` / `validate` / `save` / `run`
  - `run selected chunk + dependencies`
  - `sync handoff from body`
  - `sync body prelude from handoff`
  - `duplicate current chunk`
  - `jump to CHK_xxx`
- 命令面板适合替代记忆大量快捷键。

### 7.3 工作台内 CLI 命令

命令可带或不带前导 `/`（实现会去掉 `/`）。常用命令如下。

| 命令 | 作用 |
|------|------|
| `help` | 列出可用命令 |
| `render` | 应用表单、按 handoff 同步拓扑、校验并规范化，**重绘 Chunk Graph 并写回 plan** |
| `validate` | 校验并规范化（不写盘） |
| `save` | 同步拓扑后把计划写回 **VFS 路径**（允许保存 invalid draft；会标记为已保存） |
| `reload` | 从 VFS 重新加载（**有未保存修改时会拒绝**） |
| `palette` | 打开命令面板 |
| `duplicate` | 复制当前 chunk 为新的 `chunk_id` |
| `adddecision` | 以当前图选中节点为父，直接新增一个 `decision` 子节点 |
| `branch <name> <target_chunk_id>` | 给当前图选中的 `decision` 节点新增或重定向一条分支边；目标可以是旧节点 |
| `decisiondefault <branch>` | 设置当前图选中 `decision` 节点的默认分支 |
| `decisionmode <human\|llm\|llm_then_human>` | 设置当前图选中 `decision` 节点的判定模式 |
| `decisionprompt <template>` | 设置当前图选中 `decision` 节点的 LLM 判定模板 |
| `decisiontitle <title>` | 设置当前图选中 `decision` 节点的人类交互标题 |
| `plan-continue <json_file>` | 在当前选中节点后接入下游 planning json；若文件不存在会自动创建；接入时子 plan 与父 plan 共享命名空间（`intent_id / intent_uid` 对齐） |
| `plan-change <json_file>` | 在当前选中的 `Next Plan` 节点上，用另一个 planning json 替换该 Next Plan 子图；替换后仍共享父 plan 命名空间 |
| `plan-detach` | 对当前选中的 `Next Plan` 节点剥离：子 plan 改为独立命名空间，并从父图移除该 Next Plan 子图（包含 Next Plan 根节点） |
| `plan-draft <prompt>` | 当 run 处于 `planning_pending` 时，请求 LLM 继续生成并附着后续 fragment |
| `plan-finish` | 当 run 处于 `planning_pending` 时，结束本轮 planning 并将 run 标记完成 |
| `attachfragment HOST_JSON_FILE` | 从宿主机 JSON 文件导入一个 `plan fragment`，并把其中的新节点/新边追加到当前图；旧图不会被替换 |
| `synchandoff` | 从当前 chunk 正文里的 `@in_<port>__<var_name>` 推导并刷新 handoff 表 |
| `syncbody` | 清理旧版 `@in_<port>_file` / `@in_<port>_text` 生成 prelude，保留手写正文 |
| `submit` | 同步拓扑 + **校验并规范化** + 写盘，然后调用 **`bridge.SubmitChunkGraph`**，登记 `plan_id` |
| `start` | 在已 submit 且 plan 未变的前提下 **`StartRun`**（不强制重新写盘） |
| `run` | **submit + StartRun** 一步（适合改完直接跑；内部会先做同步/校验/写盘） |
| `runselected` | 仅提交并运行「当前 chunk + 它所依赖的上游 chunk」形成的聚焦子图 |
| `status` | 对 **Workbench 内最近一次** `run` / `start` / `runselected` 的 run：一行状态（`run_id`、`status`、`updated` 时间） |
| `inspect` | 同上 run：完整 inspect（汇总 + 各 chunk 状态行）；汇总会带 `graph_revision` 与 `task/decision` 统计 |
| `events` | 同上 run：增量事件；多次执行只输出未读事件；无新事件时提示 `no new events` |
| `chunk` | 需已有上述 run；默认当前选中 chunk，或 `chunk CHK_ID` 查看单节点 **`InspectRunChunk`**（含错误信息） |
| `quit` | 有未保存则提示；否则退出 |
| `quit!` | 强制退出（丢弃未保存提示） |

常用别名：

- `r` → `render`
- `v` → `validate`
- `s` → `save`
- `dup` → `duplicate`
- `rs` → `runselected`
- `sh` → `synchandoff`
- `sb` → `syncbody`
- `p` → `palette`

### 7.3.1 Decision 的分支与跳转条件怎么编辑

当前版本里，Decision 的跳转条件采用结构化编辑，不建议直接手改 JSON。常用命令：

```text
branch <分支名> <target_chunk_id>
decisiondefault <分支名>
decisionmode <human|llm|llm_then_human>
decisionprompt <template>
decisiontitle <title>
```

示例：

```text
adddecision
branch revise CHK_ROOT
branch approve CHK_DONE
decisiondefault revise
decisionmode llm
decisionprompt choose branch from {{allowed_branches}} using {{context.case_body}}
```

若需要回到旧节点继续执行，可直接把 branch 指向旧节点，例如：

```text
branch retry_extract CHK_EXTRACT
```

这表示当 `decision` 选中 `retry_extract` 时，控制流会回到 `CHK_EXTRACT`，并从该旧节点重新推进后续链路。

### 7.3.2 Planning 串联操作步骤（当前推荐）

用于“父 plan -> 子 plan -> 替换 / 剥离”的典型流程：

1. 在父图中选中挂接点（通常是需要继续细化的 task 节点）。
2. 执行 `plan-continue <child.json>`：
   - 若 `child.json` 不存在会自动创建；
   - 子图接入父图，并显示 `Next Plan` 节点；
   - 子 plan 自动与父 plan 共享命名空间（`intent_id / intent_uid` 一致）。
3. 需要编辑子 plan 时，选中 `Next Plan` 按 Enter 打开子文件；在子图中可通过 `Return Plan` 返回父图。
4. 替换当前 Next Plan 子图时，在父图选中 `Next Plan` 执行 `plan-change <other.json>`：
   - 用新的子 plan 替换当前 Next Plan 子图；
   - 替换后的子 plan 仍与父 plan 共享命名空间。
5. 解除共享并切断当前挂接时，选中 `Next Plan` 执行 `plan-detach`：
   - 子 plan 自动改为独立命名空间；
   - 父图中该 Next Plan 子图会被移除（`Next Plan` 根节点也会删除）；
   - 焦点回到锚点节点（通常是原挂接点）。

补充说明：

- `open next plan` / `plan-change` / `plan-detach` 对子 plan 文档使用宽松读取策略（可解析 JSON 即可），避免临时 handoff 不一致阻塞操作。
- 运行态 planning（`planning_pending`）与图编辑 planning 是两条链路：前者用 `plan-draft` / `plan-finish` 处理 run 续接，后者用 `plan-continue` / `plan-change` / `plan-detach` 处理图结构。

### 7.4 bridge 全链路说明

工作台内的 `submit` / `start` / `run` / `inspect` / `events` / `chunk` 均通过 TUI 运行时的 **`bridge.Server`** 完成，与主会话共用：

- 同一 **VFS Service**
- 同一 **Invoker**
- 同一套 **BatchScheduler**（若 Executor 上已配置）

并在 bridge 侧同步 **InitialCWD**，使 chunk 内相对路径行为与你在 TUI 里习惯的 cwd 一致。

计划文件格式为 **`bridge.ChunkGraphPlan` 的 JSON**（`plan_id`、`chunks`、`edges`、`entry_chunks`、`handoff_inputs` 等），需通过 bridge 的校验规则；不满足时 `validate` / `save` / `submit` 会报错并提示字段问题。

---

## 8. 最小工作流示例

### 8.1 line 模式快速调试

```text
Pwd
Cd /docs
Resolve file ./a.txt as @doc
Read @doc as @text
Invoke [[[请总结]]] with @text as @summary
Print @summary
Write @summary into /output/summary.txt as @out
```

### 8.2 chunk 模式一次执行多行

```text
/mode chunk
Resolve file /docs/a.txt as @doc
Read @doc as @text
Invoke [[[请用一句话总结]]] with @text as @summary
Write @summary into /output/summary.txt as @out
/commit
```

### 8.3 执行主机上的 .satis 脚本

```text
/exec examples/01_read_write.satis
```

### 8.4 仅在主 TUI（line 模式）校验并跑 `plan.json`

不进入 Workbench 时，可在主提示符下：

```text
/plan validate /plans/demo
/plan run /plans/demo
/plan status
/plan events
/plan events
/plan inspect
```

说明：`status` / `inspect` / `events` 无参时依赖**上一次** `/plan run` 的 `run_id`；也可写成 `/plan status run_xxx` 等。

### 8.5 打开 Workbench 并在工作台内跑计划

```text
/workbench /plans/demo
```

进入后在右侧 CLI 可依次：

```text
validate
run
status
inspect
events
events
chunk
```

Workbench 内的 `status` / `inspect` / `events` / `chunk` 只针对**工作台里**最近一次 `run` / `start` / `runselected`，与主 TUI 的 `/plan run` **不是同一条「最近 run」上下文**。

---

## 9. Invoke 行为（TUI 重点）

### 9.1 变量 prompt

- `Invoke @prompt`
- `Invoke @prompt with @sys as @out`
- `Invoke @prompts[:2] with @sys as @out`
- `Invoke @prompt concurrently with @inputs as @summary`

说明：prompt 侧需为**文本或文本列表**；若为对象，需先 `Read` 成文本再 Invoke。

### 9.2 `with` 的角色映射

`Invoke VAR1 with VAR2 as VAR3`：

- `VAR1` → user（prompt 侧）
- `VAR2` → system

### 9.3 Thinking 段显示

若模型输出以 `<redacted_thinking>...</redacted_thinking>` 开头，TUI 会对 thinking 段做折叠预览（最多约 6 行），正文继续流式显示；写入变量/文件的结果会做 think 标签清洗。

---

## 10. 会话状态与提交语义

- 会话保持：**cwd**、**变量环境**（如 `@doc`）、未提交 chunk 缓冲等。
- **line** 模式下，普通指令执行成功后会**自动 Commit**（`Commit` / `Rollback` 自身除外）。
- **`/clearchunk`** 会重置会话，丢失未提交状态。
- **工作台** 内对计划文件的修改通过 `save` 写回 VFS；`run` 会先保存再 submit + start，避免磁盘与内存计划不一致。

---

## 11. 常见问题排查

| 现象 | 处理 |
|------|------|
| `undefined variable @xxx` | 变量未定义或被重置；用 `Print` 或 `/history` 核对 |
| `Invoke @p` 报 prompt 非文本 | `@p` 可能是对象；先 `Read @p as @p_text` |
| `/exit` 提示 pending chunk | 先 `/commit` 或 `/cancel` |
| `cd pr` Tab 后路径怪异 | 已按 suffix 补全修复；若仍异常，看是否混用主机路径与 VFS 路径 |
| 同名 `projects` 与 `projects/` | 用 `ls` 看带 `/` 的为目录；`Cd` 与 `Read` 语义不同 |
| workbench `reload` 失败 | 先 `save` 或 `quit!` 放弃本地修改 |
| `submit` / `run` 校验失败 | 对照 bridge 校验信息补全 handoff（如 resolve/read 与声明的 input 一致） |
| `/plan status` 提示 no plan run | 先执行 `/plan run`；或与 Workbench 内 `run` 混淆时，在对应界面使用对应命令 |
| `/plan run` 后 chunk 名带前缀 | 隔离运行会改写 `chunk_id`，请用 `/plan inspect` 中的 id 配合调试 |

---

## 12. 推荐操作习惯

- 先用 **line 模式** 做小步验证，再用 **chunk 模式** 固化多行脚本。
- 关键中间结果多用 **`Print`**，避免错误传到后续 `Write`。
- 调 prompt 先在 TUI 验证，再写入 `.satis` 或计划 JSON。
- 使用 **openai** 前，可用 **echo** / **prompt-echo** 验证流程结构。
- 编辑 Chunk Graph 时：优先用 **DAG 上的 `Ctrl+N`** 加子 chunk；切换大功能优先用 **命令面板**。
- handoff 与正文不一致时，先试 **`synchandoff`** 或 **`syncbody`**，再 `render` / `validate`。
- 调试单个节点时，优先用 **`runselected`** 缩小执行范围。
- 编辑 Chunk Graph 时：**经常 `validate`**，定稿前 **`save`**，整图试跑用 **`run`**。
- 退出 Workbench 后若要在主 TUI 跑同一目录计划：用 **`/plan validate` / `/plan run`**，观测用 **`/plan status|inspect|events`**。

---

## 13. 一句话总结

**Satis TUI** 把 SatisIL 的「解析 → 读文本 → 模型 → 落盘」拆成可观察、可补全、可会话提交的交互流程；**Workbench** 在此基础上让你编辑 **bridge 计划** 并走 **submit / run / inspect / events** 的完整调度链路；主 TUI 还可通过 **`/plan`** 在不进入 Workbench 时校验、隔离运行并观测同一 VFS 上的 `plan.json`。
