# Satis Workbench 使用说明

> 文轩注：由于现在只是demo版本，所以麻烦使用者把窗口开大一些。 TAT 若后续我有时间更新项目，我会尽量让UI更简单好用的！

本文档描述当前仓库中 **TUI Workbench**（`tui/workbench`）的界面布局、快捷键、CLI 命令与典型工作流。Workbench 用于在终端内编辑 **`bridge.ChunkGraphPlan`**（默认工作目录下的 `plan.json`），并与主 TUI 共用 VFS、Invoker 与 bridge 调度。

运行层当前实际落地的是两类运行节点：

- **`task`**：普通执行节点
- **`decision`**：控制流分支节点
- **规划动作（planning）**：通过 Workbench 的 planning 命令串联多份 plan 文档；在父图中会出现 `Next Plan`（以及子图中的 `Return Plan`）导航节点

因此 Workbench 当前编辑的是一张**可增长的工作流图**，而不只是静态 DAG。`decision` 也可以回到图中的旧节点继续执行；planning 导航节点用于跨 plan 文件跳转与替换。

---

## 1. 如何进入与退出

### 1.1 进入

在主 Satis TUI 中输入（`chunk` 模式下需先 `/commit` 或 `/cancel`，不能在有未提交 chunk 缓冲时进入）：

```text
/workbench <VFS工作目录> [Intent...]
```

- 参数为 **VFS 路径**（如 `/plans/demo`），计划文件固定为：`<目录>/plan.json`。
- 打开**已有** Workbench：`/workbench <目录>`
- 新建**全新** Workbench：`/workbench <新目录> <Intent...>`
- 约束：
  - 已有目录/已有 `plan.json` 时，**不要**再附带 `Intent`
  - 新建目录时，**必须**附带 `Intent`
  - 已有目录但缺少 `plan.json` 时，运行时会报错，不再自动补建

### 1.2 退出

- **Esc**：若无未保存修改则退出 Workbench；若有未保存，会提示先 **`save`** 或 **`quit!`**。
- CLI：**`quit`**（有未保存时拒绝）、**`quit!`**（强制退出）。

---

## 2. 界面布局（上 · 左 · 中 · 右）

| 区域 | 说明 |
|------|------|
| **顶部 Status** | 第一行显示当前 **Intent description**；第二行显示当前 `plan` 的 VFS 路径、当前选中 chunk、dirty/clean、校验摘要（是否有 issues）、最近一次 **run_id**（若有）。 |
| **左侧 Chunk Graph** | 统一 ASCII 工作流图画布（非树视图），同屏显示 `in/out` handoff 变量摘要。当前选中 chunk 使用**亮蓝色文字与边框**高亮；有校验问题的节点会带 **`!`** 标记；父节点向下分叉起点会用**黄色 `+`** 标识。节点标题前会显示 **`T / D`**，分别表示 `task / decision`；`decision` 节点还会显示 `branches:...` 摘要。 |
| **中间 · Chunk** | 当前 chunk 的 `satis_text` 编辑器，修改会写回内存中的计划并标记 dirty。 |
| **中间 · Chunk Meta** | 只显示两项：`chunk port` 与 `chunk desc`。`chunk desc` 会同步到该 chunk head 的 `description:`。 |
| **中间 · Handoff** | 表格方式编辑 `inputs.handoff_inputs`。 |
| **右侧 · CLI Log** | 命令回显与运行日志；错误行会以红色标签显示。 |
| **右侧 · Diagnostics** | 静态校验问题摘要：优先列出**当前 chunk** 相关错误；否则列出全局前若干条。 |
| **右侧 · CLI 输入** | 输入命令后回车执行（可带或不带前导 `/`）。 |

---

## 3. 焦点与快捷键

### 3.1 循环与快速聚焦

| 按键 | 作用 |
|------|------|
| **Tab** / **Shift+Tab** | 在预设焦点链中循环（Chunk Graph → Chunk → Decision → CLI）。 |
| **Ctrl+A** / **Ctrl+D** | 同上，反向/正向循环焦点。 |
| **Option+1**（macOS）/ Alt+1 | 聚焦 **Chunk Graph** |
| **Option+2**（macOS）/ Alt+2 | 聚焦 **Chunk 编辑器** |
| **Option+3**（macOS）/ Alt+3 | 聚焦 **Decision 编辑区** |
| **Option+4**（macOS）/ Alt+4 | 聚焦 **CLI 输入** |

也可用命令：`focus left|chunk|decision|cli`。

### 3.2 常用操作

| 按键 | 作用 |
|------|------|
| **Ctrl+Shift+R** | 执行 **`render`**（同步拓扑、校验、写盘、重绘 DAG）。 |
| **Ctrl+S** | 执行 **`save`**。 |
| **Ctrl+P** / **Ctrl+Space** | 打开**命令面板（Palette）**。 |
| **Chunk Graph 焦点 + Ctrl+N** | 以**当前选中节点为父**新增**task 子 chunk**（见下文「结构编辑」）。 |
| **Chunk Graph 焦点 + Shift+D** | 以**当前选中节点为父**新增**decision 子 chunk**。 |
| **Chunk Graph 焦点 + Ctrl+T** | 在当前选中节点上循环切换类型：`task -> decision -> task`。 |
| **Chunk Graph 焦点 + Ctrl+K** | 删除当前高亮 **chunk**（仅允许删除“非根且无子节点”的叶子节点）；会清理边与下游 handoff 中对它的引用。 |
| **Chunk Graph 焦点 + ↑/↓/←/→** | 在图上按方向键移动当前选中 chunk：**上父、下子、左右同层**。 |
| **Chunk Graph 焦点 + Enter** | 以当前图选中节点作为当前 chunk（加载到中间编辑区）。 |
说明：删除规则为：**根节点不可删**、**非叶子节点不可删**、**最后一个 chunk 不可删**。删除成功后会优先选中剩余图中的**最深叶子节点**（而非默认跳回根节点）。删除失败或删后计划不合法时，实现会尽量从磁盘重载以回滚危险状态。

---

## 4. 命令面板（Palette）

- **打开**：`Ctrl+P`、`Ctrl+Space`，或 CLI 输入 **`palette`**（别名 **`p`**）。
- **用法**：在 `palette>` 输入框里输入关键字过滤列表，**Enter** 后焦点到列表，方向键选择，**Enter** 执行并关闭面板；**Esc** 关闭。
- 面板内包含：`render` / `validate` / `save` / `run`、**聚焦运行**、**同步 handoff/body**、**复制 chunk**、**跳转到各 chunk** 等（与实现中的 `paletteActions` 一致）。

---

## 5. CLI 命令一览

命令可省略前导 `/`。以下为常用命令及**别名**。

### 5.1 计划与文件

| 命令 | 别名 | 作用 |
|------|------|------|
| `render` | `r` | 应用当前 handoff 表单、`SyncEdgesFromHandoffs`、校验规范化、**写回 `plan.json`**、重绘 Chunk Graph。 |
| `validate` | `v` | 同上同步与校验，**不写盘**（仍会规范化内存中的计划）。 |
| `save` | `s` | 同步拓扑 + 写盘（**允许保存 invalid draft**；不做强制校验）。 |
| `reload` | — | 从 VFS 重新加载；**有 dirty 时拒绝**。 |

### 5.2 结构与高阶编辑

| 命令 | 别名 | 作用 |
|------|------|------|
| `duplicate` | `dup` | 复制当前 chunk 为新 `chunk_id`，并同步拓扑。 |
| `adddecision` | — | 以当前图选中节点为父，直接新增一个 `decision` 子节点。 |
| `branch <name> <target_chunk_id>` | — | 给当前图选中的 `decision` 节点新增或重定向一条 branch edge。`target_chunk_id` 可以是旧节点。 |
| `decisiondefault <branch>` | — | 设置当前图选中 `decision` 节点的默认分支。 |
| `decisionmode <human\|llm\|llm_then_human>` | — | 设置当前图选中 `decision` 节点的判定模式。 |
| `decisionprompt <template>` | — | 设置当前图选中 `decision` 节点的 LLM 判定模板。 |
| `decisiontitle <title>` | — | 设置当前图选中 `decision` 节点的人类交互标题。 |
| `plan-description [text]` | — | 不带参数时查询当前 `plan_description`；带参数时修改当前 `plan_description` 并立即保存。 |
| `intent-description` | — | 查询当前 `intent_description`。 |
| `plan-continue <json_file>` | — | 在当前选中节点后接入“下游 planning json”。若文件不存在会自动创建。接入时子 plan 会与当前 plan **共享命名空间**（`intent_id / intent_uid` 对齐）。 |
| `plan-change <json_file>` | — | 在当前选中的 `Next Plan` 节点上，用另一个 planning json **替换该 Next Plan 子图**。替换后仍与当前 plan **共享命名空间**。 |
| `plan-detach` | — | 对当前选中的 `Next Plan` 节点执行剥离：把对应子 plan 改为**独立命名空间**，并从当前图中移除该 Next Plan 子图（包含 Next Plan 根节点）。 |
| `plan-draft <prompt>` | — | 当 run 处于 `planning_pending` 时，请求 LLM 继续生成并附着后续 fragment。 |
| `plan-finish` | — | 当 run 处于 `planning_pending` 时，结束本轮 planning 并将 run 标记完成。 |
| `attachfragment <host_json_file>` | — | 从**宿主机 JSON 文件**导入一个 `plan fragment`，并将其中的新节点/新边**追加**到当前图。旧图不会被替换。 |
| `synchandoff` | `sh` | 从当前 chunk 正文解析 `@in_<port>__<var_name>` 引用，刷新 handoff 表中的 `port` 与 `var_name`（尽量保留已有 `from_step` / `from_port`）。 |
| `syncbody` | `sb` | 清理旧版自动生成输入 prelude（`@in_<port>_file`/`@in_<port>_text`），保留你手写正文。 |
| `palette` | `p` | 打开命令面板。 |

### 5.3 Bridge 提交与运行

| 命令 | 作用 |
|------|------|
| `submit` | 同步拓扑 + **校验并规范化** + 写盘，然后 **SubmitChunkGraph**。 |
| `start` | 在已 submit 且 plan 一致前提下 **StartRun**。 |
| `run` | **submit + StartRun**（整图；内部会先做同步/校验/写盘）。 |
| `runselected` | `rs` | 仅构建并提交「**当前 chunk + 其所有上游依赖（沿边反向）**」子图，再 **StartRun**；用于局部联调。 |

### 5.4 运行期观察（Workbench CLI）

以下命令均作用于 **Workbench 内**最近一次成功启动的 plan run（`run` / `start` / `runselected` 会更新「当前 run」）。若尚未启动过 run，会提示先 `run` 或 `start`。

| 命令 | 作用与典型用法 |
|------|----------------|
| **`status`** | **轻量轮询**：打印当前 run 的 `run_id`、`status`（如 pending / running / completed / failed 等）、`updated` 时间。适合只想看「跑完没有」。 |
| **`inspect`** | **完整快照**：一行 run 级信息 + **汇总**（`plan_id`、`graph_revision`、`total`、`task/decision` 数量、各状态 chunk 数量、若有则 `primary_failure`、artifacts 数量等）+ **逐 chunk 一行** `chunk <id>: <phase>`。适合 run 结束后总览、排查失败/阻塞节点，或确认近期规划是否已把新节点附着进当前图。 |
| **`events`** | **增量事件**：调用 `StreamRunEvents`，只输出**自上次在本 Workbench 会话内执行 `events` 以来尚未打印过**的事件行（格式类似 `event <id> <type> [chunk=...]`）。可**多次输入 `events`** 模拟轮询；若没有新事件会提示 `no new events`；run 已终结时可能提示 terminal。 |
| **`chunk`** | **单节点详情**：`chunk`（默认当前 DAG 选中 chunk）或 **`chunk CHK_ID`**，对当前 run 执行 **InspectRunChunk**，输出该 chunk 的 `status`，若有执行错误会打印 error 摘要。适合在 `inspect` 看到某节点失败后针对性查看。 |

**与主 TUI `/plan` 的关系**

- 在主 Satis TUI（非 Workbench）中：**`/plan run`** 与 **`/plan status` / `/plan inspect` / `/plan events`** 使用**另一套**「最近 plan run」上下文，**不会**与 Workbench 里记录的 `lastRunID` 混用。
- **`/plan run`** 会使用隔离的 `chunk_id` 前缀，因此 bridge 里看到的 chunk 名与 `plan.json` 中原始 id **可能不一致**；应以 `/plan inspect` 或事件中的 id 为准。

### 5.5 其它

| 命令 | 作用 |
|------|------|
| `help` | 打印命令与快捷键提示。 |
| `quit` / `quit!` | 见上文「退出」。 |

---

## 6. 多 chunk 与 Handoff 约定（Workbench 侧）

### 6.1 边的来源

- **`render` / `save` / `submit` / `run` / `runselected`** 前会执行 **`SyncEdgesFromHandoffs`**：根据各 chunk 的 `handoff_inputs` 中 **`from_step` + `from_port`**（均非空且合法）推导 **`edges`**、**`depends_on`**、**`entry_chunks`**。
- 仅填一半 handoff（例如只有 `from_step` 没有 `from_port`）会在同步阶段报错。
- 当前实现要求计划**只能有 1 个主入口 chunk**。若同步后出现多个入度为 0 的节点，后续 `validate` / `render` / `run` 都会失败，需先把图整理回单根 DAG。

说明：

- 对 **`task`** 节点，当前 Workbench 仍主要依赖 handoff 推导普通控制边。
- **`decision`** 节点的控制流语义已经进入底层运行时。
- `decision` 的 branch edge 不会在 handoff 拓扑同步时被清掉。
- `decision` 可以跳回旧节点；命中该分支后，旧节点会重新进入执行链路，适合返工、重审核、重抽取。
- Planning 通过 `Next Plan / Return Plan` 导航节点与 `plan-continue / plan-change / plan-detach` 命令在图中体现；同时也可通过 **fragment 导入** 或直接编辑 `plan.json` 追加新节点/新边。

### 6.2 上游输出与下游输入

- 上游在 Satis 中需产出文本别名：**`@out_<port>__<var_name>`**。
- 下游 handoff 约束：`from_port` 对齐上游 `port`，`var_name` 必须与上下游别名中的 `var_name` 完全一致。
- 下游正文直接消费：**`@in_<port>__<var_name>`**。
- Workbench/bridge handoff 已不使用 `virtual_path`，变量通过运行时值绑定传递。

### 6.3 新增子 chunk（Graph 上 Ctrl+N）

- 以当前选中 chunk 为**父**创建子节点，并尝试：
  - 若父 chunk 正文里能解析出 **`@out_<port>__<var_name>`**，则为子 chunk 预填一条 handoff，并在子 chunk 正文放入 `Print @in_<port>__<var_name>` 脚手架。
- 之后你仍需按业务补全子 chunk 正文（例如 `Invoke`、`Write` 等），再 **`render`** / **`validate`**。

补充：

- **`Ctrl+N`** 新增的是 `task` 子节点
- **`Shift+D`** 或命令 **`adddecision`** 新增的是 `decision` 子节点

### 6.4 节点类型切换（Graph 上 Ctrl+T）

- 在左侧图上选中一个节点后按 **`Ctrl+T`**，会按以下顺序循环切换：
  - `task -> decision -> task`
- 切换时：
  - 节点 `chunk_id` 保留
  - `task` 会保留/补默认 `satis_text`
  - `decision` 会补默认分支与交互定义
- 这是**结构级操作**。如果你把一个已有正文的 `task` 切成 `decision`，其正文会被清空为控制节点定义。

### 6.5 编辑 Decision 跳转条件

当前 Workbench 已支持直接编辑 `decision` 的结构化跳转条件。

常用命令：

```text
branch <分支名> <目标chunk_id>
decisiondefault <分支名>
decisionmode <human|llm|llm_then_human>
decisionprompt <template>
decisiontitle <title>
```

示例：

```text
branch revise CHK_ROOT
branch approve CHK_DONE
decisiondefault revise
decisionmode llm
decisionprompt choose branch from {{allowed_branches}} using {{context.case_body}}
```

说明：

- `branch` 会自动把分支名加入 `allowed_branches`
- 如果同名分支已存在，再次执行会把它重定向到新的目标节点
- `target_chunk_id` 可以是**旧节点**

例如：

```text
branch retry_extract CHK_EXTRACT
```

表示当 `decision` 选中 `retry_extract` 时，控制流会回到 `CHK_EXTRACT`，并从该旧节点重新推进后续链路

---

## 7. 推荐工作流（简版）

### 7.1 仅在 Workbench 内编辑并运行

1. **`/workbench /你的/工作目录`**  
2. 在 Chunk Graph 选中 chunk，在中间编辑 **Chunk**；在 **Handoff** 维护 `port` / `from_step` / `from_port` / `var_name`。  
3. 正文与 handoff 不一致时：先试 **`synchandoff`** 或 **`syncbody`**。  
4. 若需要引入新的规划结果，可执行 **`attachfragment <host_json_file>`** 将 fragment 挂接到当前图。  
5. 若当前节点是 `decision`，可继续用 **`branch` / `decisiondefault` / `decisionmode` / `decisionprompt` / `decisiontitle`** 编辑跳转条件。  
6. **`render`** 或 **`save`** 落盘；需要时用 **`validate`** 仅检查不写盘。  
7. 整图试跑：**`run`**；只调当前链路：**`runselected`**。  
8. **运行中**：可多次输入 **`events`** 看时间线；用 **`status`** 快速看是否结束。  
9. **结束后**：用 **`inspect`** 看汇总与各 chunk 状态；重点关注 `graph_revision` 是否变化，以及新节点是否已经进入当前图。对失败节点用 **`chunk CHK_ID`** 看错误详情。  
10. 结合 **`Diagnostics`** 与 **图节点类型标记**修复 plan 后，再 **`render` / `save`** 重试。

### 7.2 Planning 串联操作步骤（当前推荐）

以下步骤用于“父 plan -> 子 plan -> 替换 / 剥离”的典型流程：

1. 在父图中选中挂接点（通常是需要继续细化的 task 节点）。
2. 执行 **`plan-continue <child.json>`**：
   - 若 `child.json` 不存在会自动创建；
   - 子图被接入父图，并在图上显示 `Next Plan` 节点；
   - 该子 plan 会自动与父 plan **共享命名空间**（`intent_id / intent_uid` 一致）；
   - 新生成的子 plan 默认**不写** `plan_description`。
3. 需要进入子 plan 编辑时，在图上选中 `Next Plan` 按 **Enter** 打开子文件；在子图中可通过 `Return Plan` 返回父图。
   - 若子 plan 缺少 `plan_description`，进入时会弹出 TUI 输入框要求填写。
4. 若要替换当前 Next Plan 子图，回到父图后选中 `Next Plan`，执行 **`plan-change <other.json>`**：
   - 用新的子 plan 替换当前 Next Plan 子图；
   - 替换后的子 plan 仍与父 plan **共享命名空间**。
5. 若要解除共享并切断当前挂接，选中 `Next Plan` 执行 **`plan-detach`**：
   - 子 plan 自动改为**独立命名空间**；
   - 父图中该 Next Plan 子图会被移除（`Next Plan` 根节点也会删除）；
   - 焦点会回到锚点节点（通常是原挂接点）。

补充说明：

- `open next plan` / `plan-change` / `plan-detach` 对子 plan 文档使用宽松读取策略（可解析 JSON 即可），避免临时 handoff 不一致阻塞操作。
- Workbench 在**新增 chunk**、**复制 chunk**、**`plan-continue` 创建子 plan** 时，会扫描同一工作目录下其他合法 plan 文件，自动避开已被占用的 `CHK_*`，保证 workspace 级别 `chunk_id` 唯一。
- 运行态 planning（`planning_pending`）与图编辑 planning 是两条链路：前者用 `plan-draft` / `plan-finish` 处理 run 续接，后者用 `plan-continue` / `plan-change` / `plan-detach` 处理图结构。

### 7.3 退出 Workbench 后在主 TUI 跑同一目录计划

适用于已 **`save`** 落盘、希望在 **line 模式**下直接触发 bridge 运行（不打开全屏工作台）：

1. **`/plan validate /你的/工作目录`**（可选）  
2. **`/plan run /你的/工作目录`**（记下输出中的 `run_id`，或依赖默认「最近一次 `/plan run`」）  
3. **`/plan events`** / **`/plan status`** 轮询；结束后 **`/plan inspect`**  

注意：此处观测命令是 **`/plan ...`**，不是 Workbench 内的 **`events`**；两者「最近 run」上下文独立。

---

## 8. 常见问题

| 现象 | 建议 |
|------|------|
| `render` / `save` 报 handoff 绑定 | 核对 `from_step` / `from_port` / `var_name`；检查上游是否产出 `@out_<from_port>__<var_name>`，下游是否消费 `@in_<port>__<var_name>`。 |
| `chunk_id` 校验失败 | 正文第一行 `chunk_id:` 必须与 DAG 节点 id 一致。 |
| `runselected` 与 `run` 行为不同 | `runselected` 只包含当前 chunk 及其上游依赖；并行分支上的 chunk 不会进入该子图。 |
| `events` 没有新输出 | 可能 run 已结束且无新事件；或应用错上下文（Workbench 内用 `events`，主 TUI 用 `/plan events`）。 |
| `status` / `inspect` 提示无 run | Workbench 内需先 **`run`** 或 **`start`**；主 TUI 需先 **`/plan run`**。 |
| 终端里 **Ctrl+Shift+R** 无响应 | 部分终端与系统快捷键冲突；请直接用 CLI **`render`**。 |
| Palette 打不开 | 确认焦点未被异常占用；试 **`palette`** 或 **`p`**。 |

---

## 9. 相关代码与总览文档

- 实现目录：`tui/workbench/`（`workbench.go`、`model.go`、`model_enhanced.go`、`ui_enhanced.go` 等）。
- 与主 TUI 斜杠命令、整体 REPL 说明见：`documents/Satis TUI详细使用教程.md`（其中第 7 节为 Workbench 摘要，细节以本文为准）。

---

*文档版本：与当前仓库 Workbench 实现同步；若行为变更，请以代码为准并更新本文。*
