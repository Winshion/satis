# SatisIL 语法说明（易读版，v1）

日期：2026-04-10  
适用版本：SatisIL **v1**（对应当前仓库 `satis/parser.go`、`satis/validate.go` 与 `satis/runtime.go`）

---

## 1. 先看一个最小可运行示例

```satis
chunk_id: CHK_DEMO
intent_uid: intent_demo

Resolve file ./examples/inputs/source.md as @doc
Read @doc as @text
Invoke @text with [[[请用一句话总结]]] as @summary
Write @summary into ./examples/output/summary.txt as @out_summary
```

这段做了 4 件事：
- 解析一个文件为对象：`@doc`
- 读取文本内容：`@text`
- 调模型生成结果：`@summary`
- 写出文件并得到新对象：`@out_summary`

---

## 2. 基本结构（固定格式）

每个 chunk 都是：

1) **头部元信息**（至少两行）
- `chunk_id: ...`
- `intent_uid: ...`

2) **指令区**（一行一条）

注意：
- 如果没有指令，则会报错
- 变量名必须以 `@` 开头（如 `@doc`）

### 2.1 交互式 Session / TUI 模式

除完整 chunk 外，SatisIL 也支持 **body-only** 的交互执行模式：

- 用户可以只输入指令区（不写 `chunk_id` / `intent_uid`）
- TUI 可按**一行一条命令**解析并执行
- 同一个 session 内会保留：
  - 当前工作目录 `cwd`
  - 已定义的变量环境（如 `@doc`、`@text`）

这适合做类似 shell 的交互命令；而完整 `.satis` 文件仍适合批量脚本执行。

---

## 3. 两种“值”的写法（只有“值”才用三括号）

SatisIL 里只有两种“值”（Value）：

- **文本字面量**：`[[[...]]]`
- **变量引用**：`@var_name`（支持列表选择器：`@var[i]`、`@var[:n]`、`@var[a:b]`）

例如：
- `Write [[[hello]]] into ./a.txt as @out_a`
- `Write @summary into ./b.txt as @out_b`
- `Print @content_list[:3]`

### 三括号内的转义（当前实现）

在所有**作为“值”使用的**三括号字面量中（例如 `Write` 的写入内容、`Invoke` 的 prompt/输入、`Print` 的字面量等），反斜杠 `\` 会启动转义解析。**仅支持**以下三种；其它写法（如 `\uXXXX`）会在**解析阶段**报错。

| 写法 | 含义 |
|------|------|
| `\n` | 换行 |
| `\t` | 制表符 |
| `\\` | 字面量 `\` |

说明：
- 行尾的“真实换行”仍然可以直接写在 `[[[` 与 `]]]` 之间；`\n` 用于在**同一物理行**里写入换行。

---

## 4. 路径写法（v1 变化最大的一点）

### 4.1 路径不使用三括号

v1 中，**VFS 虚拟路径**不属于“值”，因此**不**使用 `[[[...]]]`。路径直接写成 token（或 shell 风格引用）：

- `./docs/a.txt`
- `/docs/a.txt`
- `../output/result.txt`

如果你使用三括号括住（例如 `Resolve file [[[./a.txt]]]`），v1 会直接报 **Parse 错**。

### 4.2 含空格路径（参照 Linux）

v1 支持两种常见写法：

1) **双引号**（推荐）：

```satis
Cd "/sales reports"
Resolve file "/sales reports/final report.txt" as @doc
Write @text into "/sales reports/out 1.txt" as @out
```

2) **反斜杠转义空格**（裸 token 内）：

```satis
Resolve file monthly\ report.txt as @doc
```

说明：
- 单引号 `'...'` 也可用（内容原样作为路径，不做转义）。
- 路径**不能**写成 `@变量`；路径必须是字面 token / 引号路径。

---

## 5. 当前支持的主要指令（v1）

## 5.1 cd / pwd / ls（文件树切换与查看）

v1 引入工作目录（cwd）：
- 每个 chunk 执行开始时，`cwd` 默认为 `/`
- `Cd` 会改变当前 cwd
- 相对路径（不以 `/` 开头）将相对 cwd 解析

```satis
Pwd
Cd /docs
Pwd
Ls
Ls ../output
```

`Ls` 输出到标准输出（控制台），每行一条，形如：

```text
dir  subdir
file a.txt
```

---

## 5.2 Commit / Rollback（session / 事务段控制）

在交互模式或完整 chunk 中，都可以显式写：

```satis
Commit
Rollback
```

语义：
- `Commit`：提交当前事务段的修改，并开始新的事务段；`cwd` 和变量环境会保留
- `Rollback`：回滚当前事务段的修改，并回到该事务段开始时的 `cwd` 与变量环境

用途：
- 适合 TUI 按行执行时做 checkpoint
- 也可用于完整 chunk 中，把脚本切成多个“已提交片段”

---

## 5.3 Resolve / Read

### Resolve 单文件或文件夹

```satis
Resolve file ./path/file.txt as @f
Resolve folder ./path/folder as @dir
```

### Resolve 多文件（glob）

```satis
Resolve file ./docs/*.txt as @doc_list
```

### Read

**从已 Resolve 的文件对象读**——第一个操作数是 `@` 文件对象变量：

```satis
Read @f as @text
Read @f lines 1 20 as @snippet
```

说明：
- `lines` **仅支持** `Read @对象 … lines …`；没有 `Read PATH lines …` 形态。

**按路径直接读文本（面向文档）**——第一个操作数是路径 token。运行时会对该路径做 `Resolve`；解析结果必须是**普通文件**，不能是目录：

```satis
Read ./path/to/file.txt as @text_var
```

与 `Read @f …` 的关系：
- 每条 `Read` **二选一**：要么来源是 `@变量`，要么来源是 `PATH`。

### 5.3.1 Resolve 与宿主机边界（重要）

- `Resolve` 以及「第一个操作数是路径」的 `Read PATH …` 只会解析 **已在 VFS 中纳管的对象**（有正式 `file_id` 等语义）。
- **不会**因为 `disk` 后端的 `mount_dir` 下碰巧存在同名宿主文件，就在 `Resolve` 时自动把该文件懒导入进 VFS。
- 若要把操作系统侧的文本文件**显式导入**到当前 VFS 工作区，请使用 **`Load` 指令**（见下节）。导入源目录由应用配置项 **`system_port_dir`** 指定，运行时通过沙箱 **`SystemPort`** 只读访问；VFS 本身不记录宿主机绝对路径。

### 5.3.2 Load（从 system_port 导入文本到 VFS）

`Load` 用于在 **system_port 逻辑命名空间**（根为 `/`，路径形如 `/prompts/foo.md`）与 **VFS** 之间搬运**纯文本**内容。与 VFS 的 `Cd`/`Ls` 不同，`Load` 维护**独立的 load 当前目录**（`load cwd`），用 `load pwd` / `load ls` / `load cd` 浏览。

**导航（不改变 VFS，只改变 load cwd）**

```satis
Load pwd
Load ls
Load ls /prompts
Load cd /prompts
```

**导入（写入当前 VFS `cwd`，目标文件名为各源的 basename）**

```satis
Load hello.md
Load prompts/intro.md ./other.txt
Load prompts/*.md
Load a.md b.md as @docs
```

说明：

- **来源路径**：相对 **load cwd** 或 `/` 开头的绝对路径（均在 system_port 内解析）。支持 Linux 风格通配符 `*`、`?`、`[]`；一条语句可有多个源，通配结果会去重、排序后再导入。
- **仅文本**：任一匹配文件被判定为非文本（如含 NUL 等），**整批失败**，且本批对 VFS 的修改会**全部回滚**（与当前 chunk 事务语义一致）。
- **`as @var`**：可选。省略时只执行写入、不绑定变量。多文件且写了 `as @var` 时，运行时会绑定为 **`@var_0000`、`@var_0001`、…**（与 `Resolve file … glob` 多文件约定类似）。
- **目标冲突**：若多个源解析后的 **basename** 相同，或目标路径在 VFS 上已是**目录**，会报错并回滚本批导入。

配置：在 **`vfs.config.json`** 顶层设置 **`system_port_dir`**（可为 `~` 展开的路径），指向宿主机上供导入的只读根目录；未配置或不可用时，`Load` 相关操作会失败。

---

## 5.4 写入与文件操作

### Create file / Create folder

```satis
Create folder ./out/reports as @dir
Create folder ./out/a ./out/b as @dirs
Create file ./out/reports/summary.md as @file
Create file ./out/reports/draft.md with [[[初始内容]]] as @draft
Create file ./out/reports/empty.txt
Create file ./out/a.txt ./out/b.txt with [[[统一初始内容]]] as @files
```

说明：
- `Create folder PATH [as @var]`
- `Create folder PATH [PATH ...] [as @var]`
- `Create file PATH [PATH ...] [with [[[init text]]]] [as @var]`
- `Create file` 默认创建空文本文件
- `Create folder` 如果目标目录已存在，则视为成功（幂等）
- `Create file` 如果目标已存在，则报错，避免误覆盖
- `as @var` 可选；省略时只执行副作用，不绑定返回对象
- 批量创建多个 `file/folder` 时，若写了 `as @var`，该变量绑定为**对象列表**
- `Create file` 的 `with [[[init text]]]` 会把同一份初始文本应用到该语句内的所有目标文件

### Write

```satis
Write [[[固定文本]]] into ./out/a.txt [as @out_a]
Write @text into ./out/b.txt [as @out_b]
```

说明：
- `as @var` 在 v1 中对 `Write` 是**可选**的。
- 省略 `as @var` 时：写入仍会发生，但**不会把输出对象绑定到任何变量**（等价于“丢弃返回值”）。
- 如果 `Write` 的值是**文本列表**，可以使用路径模板批量写出。
- 路径模板支持 `{i}` 或 `{i:04d}` 这类索引占位符。

```satis
Write @result_list into ./out/result_{i:04d}.txt
Write @result_list[-2:] into ./out/result_{i}.txt [as @written]
```

补充说明：
- 文本列表切片支持负数（倒数）。
- 批量写出时，索引从 `0` 开始。
- 如果要写的是文本列表，但目标路径里没有 `{i}` / `{i:0Nd}` 占位符，运行时会报错，避免多个结果误写到同一个文件。

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

说明：
- `Copy` / `Move` / `Rename` / `Patch` 的 `as @var` 也是**可选**的。
- `Delete` 没有 `as`，也不会产生新对象绑定。
- `Delete` 必须显式声明目标类型：`Delete file ...` 或 `Delete folder ...`。
- `Delete` 支持批量删除：一条语句里可混写多个对象变量与路径。
- 路径来源支持 Linux 风格通配：`*`、`?`、`[]`（例如 `Delete file ./logs/*.txt`）。
- 如果你后续还要用这个对象（比如 `Read @out_xxx`），就必须写 `as @out_xxx` 把它保存到变量里。
- `Delete PATH` 删除的是**当前解析到的 VFS 虚拟路径对象**。
- 删除一旦提交，就**不支持语言级回退/恢复**；只有在当前 chunk 执行失败时，事务回滚才会撤销这次删除。

---

## 5.5 Invoke（单次）与并发 Invoke（批量）

### 单次 Invoke

完整写法（prompt + 输入 + 绑定变量）：

```satis
Invoke [[[请总结]]] with @text as @summary
Invoke [[[请翻译成英文]]] with [[[你好世界]]] as @en
Invoke @prompt_text with @system_hint as @out
```

其中 `prompt` 支持任意 **Value**：

- 三括号文本：`[[[...]]]`
- 变量：`@var`
- 变量选择器：`@list[i]`、`@list[:n]`、`@list[a:b]`

可选简写（v1）：

- **只有 prompt**：不写 `with`，输入视为空字符串；不写 `as` 时，运行时会将模型返回**打印到标准输出**（TUI/控制台里便于即看即得；若底层 Invoker 支持流式接口，会尽量**流式打印**）。
- **有 prompt、要绑定变量、无输入**：`Invoke VALUE as @out`（等价于输入为空）。
- **有 prompt 和输入、不绑定**：`Invoke VALUE with VALUE`（结果打印到标准输出）。

消息组装语义（当前实现）：
- **只有一个源**（没有 `with`）：`Invoke` 后面的 `VALUE` 作为 **user prompt**
- **有两个源**（出现 `with`）：`Invoke` 后面的 `VALUE` 作为 **user prompt**，`with` 后面的 `VALUE` 作为 **system prompt**

```satis
Invoke [[[hello]]]
Invoke @prompt_text as @polished
Invoke @prompt_lines[:2] with @sys_text
```

子句顺序约定：`with ...` 若出现，须在 `as ...` 之前（与完整形式一致）。

### 并发批量 Invoke（当前推荐）

```satis
Invoke [[[请用一句话总结]]] concurrently with @content_list as @summary mode separate_files
Invoke @prompt_text concurrently with @content_list as @summary
Invoke @prompt_lines[:2] concurrently with @content_list as @summary
```

说明：
- `@content_list` 常见来源：`Resolve file <glob>` + `Read`
- 并发 Invoke 的 prompt 同样支持 `VALUE`（含变量与选择器）
- `mode` 仅允许：`separate_files` / `single_file`
- 如果省略 `mode`，默认等于 `separate_files`
- 若 prompt 对应变量是对象/对象列表，会报错并提示先 `Read` 为文本

---

## 5.6 Print（调试输出）

用于把内容打到标准输出（控制台），不产生新变量、不写 VFS：

```satis
Print @text_var
Print [[[一行提示\n第二行]]]
Print @content_list[:3]
Print @content_list[-1]
```

打印变量时的行为（运行时）：
- 文本变量：打印字符串全文
- 文本列表：多段文本用换行拼接后打印
- 文件对象 / 对象列表：打印摘要信息（虚拟路径、`file_id`、类型、代数等），默认不自动 Read 全文

列表选择语法（类 Python）：
- `@list`：默认打印全部对象/文本
- `@list[i]`：单个索引，支持负数
- `@list[:n]`
- `@list[a:b]`

规则：
- 切片越界时会**自动截断，不报错**
- 单个索引越界会报错

---

## 5.7 Software 调用与注册表管理

### 5.7.1 software 调用语法

```satis
sorting sort --numbers [[[1,3,2]]] --order [[[asc]]] as @result
sorting sort --numbers @nums --order [[[desc]]] as @result
```

统一形式：

```text
<software_name> <function> [--flag <value> ...] [as <@var>]
```

其中 `<value>` 只允许：

- `@var`
- `[[[string]]]`

### 5.7.2 Software 管理语句

```satis
Software ls
Software find sor as @found
Software describe sorting as @desc
Software functions sorting as @funcs
Software refresh as @report
```

当前支持子命令：

- `ls`
- `pwd`
- `cd`
- `find`
- `describe`
- `functions`
- `refresh`

`Software refresh` 语义：

- 扫描 `software_registry_dir`
- 仅识别合规 software（`SKILL.md` frontmatter 必须有 `name`、`description`，且 `name` 与 `forsatis.json` 一致）
- 将合规 software 的描述回写到所属文件夹 `SKILLS.md` 的 `## Entries`
- 返回统计：`refreshed folders / recognized / skipped`

说明：

- 不合规 software 会被跳过，不进入注册表
- `Software uninstall` 不支持

---

## 6. 常见变量约定（上层物化常用）

在 planner/materializer 生成的 chunk 里，经常看到：

- `@in_<port>_file`：输入端口对应的文件对象
- `@in_<port>_text`：输入端口对应的文本
- `@out_<port>`：输出端口对象

这不是语法强制，而是上层约定。  
按这个命名可读性最好，也便于输入绑定校验。

---

## 7. 你最容易踩的坑（v1）

### 1) 忘了 `@`

错误：

```satis
Write summary_0000 into ./out/0.txt as @out_0
```

正确：

```satis
Write @summary_0000 into ./out/0.txt as @out_0
```

### 1.1) 不写 `as` 就没法引用输出对象

例如下面这段会失败，因为 `Write` 没有把输出对象绑定到 `@out`：

```satis
Write [[[hello]]] into ./a.txt
Read @out as @text
```

正确做法是：

```satis
Write [[[hello]]] into ./a.txt as @out
Read @out as @text
```

### 2) `invoke concurrently` 的 mode 写错

只接受 `separate_files` / `single_file`。

### 3) 头部缺失

缺 `chunk_id` 或 `intent_uid` 会被校验拒绝。

### 4) 对 folder 做不支持的操作

例如 `Read @folder as @text` 会在校验阶段被拒绝。  
若使用 `Read 某路径 as @text`，路径解析到的是**目录**时，会在执行阶段报错。

### 5) 三括号里写了不支持的转义

只允许 `\n`、`\t`、`\\`。

### 6) 路径里有空格但没加引号/转义

错误：

```satis
Write @x into ./bad path.txt as @out
```

正确：

```satis
Write @x into "./bad path.txt" as @out
```

### 7) 以为 `Resolve` 会自动读 `mount_dir` 里未纳管的宿主文件

`Resolve` 只认 **VFS 已管理对象**。宿主侧新放进 `mount_dir` 的文件若尚未通过 `Create` / `Write` / `Load` 等进入 VFS，**`Resolve` 会找不到**。应使用 **`Load …`** 从 **`system_port_dir`** 对应目录导入文本。

### 8) software 目录存在，但 `Software find` 看不到

常见原因是该软件目录的 `SKILL.md` 不符合规范（frontmatter 缺失或 `name/description` 非法）。  
执行 `Software refresh as @report` 后可通过 `@report` 里的 `skipped` 计数判断是否被跳过。

---

## 8. 一份“批处理 5 文件”的示例

```satis
chunk_id: CHK_BATCH
intent_uid: intent_batch

Resolve file ./test_batch/docs/*.txt as @doc_list
Read @doc_list as @content_list
Invoke [[[请用一句话总结]]] concurrently with @content_list as @summary mode separate_files
Write @summary_0000 into ./test_batch/output/summary_0.txt as @out_0
Write @summary_0001 into ./test_batch/output/summary_1.txt as @out_1
Write @summary_0002 into ./test_batch/output/summary_2.txt as @out_2
Write @summary_0003 into ./test_batch/output/summary_3.txt as @out_3
Write @summary_0004 into ./test_batch/output/summary_4.txt as @out_4
```

---

## 9. 如何判断是“解析错”还是“执行错”

- **解析错（Parse）**：语法形态不合法（例如旧式路径三括号、引号未闭合、三括号值内出现不支持的 `\` 转义）
- **校验错（Validate）**：语义不合法（缺头、mode 非法等）
- **执行错（Execute）**：运行时问题（变量未定义、文件不存在、调用失败等）

调试建议：
1. 先看是否能 `submit_chunk_graph` 被接受  
2. 再看 `inspect_run` 的 chunk error code/message  
3. 最后定位到具体指令行

补充：
- 在 session / TUI 模式下，通常是“先 `ParseBody`，再按行或按段解释执行”
- 在完整 chunk 模式下，仍是“先 `Parse` 整个 chunk，再整体执行”

---

## 10. 一句话记忆版

SatisIL 可以理解成：  
**“在 VFS 上用 `Cd/Pwd/Ls` 浏览；用 `Create/Resolve` 得到对象；需要时 `Load` 从 system_port 把宿主文本导入 VFS；`Read` 得到文本；需要时 `Print` 调试；用 `Invoke` 处理；最后 `Write/Copy/Move/Rename/Delete/Patch` 落盘。”**

---

## 11. 不同 chunk 之间怎么连接（科普版）

先说结论：  
**chunk 与 chunk 不是靠 SatisIL 文本直接“跳转”，而是靠计划图（ChunkGraphPlan）连接。**

连接分两层：

1) **调度连接（谁先跑）**：`edges` / `depends_on`  
- 例如 `A -> B`，表示 B 要等 A 完成后再执行。
- 这是控制流依赖，不是变量传参。

2) **数据连接（传什么）**：`inputs.handoff_inputs` + 约定变量  
- 上游 chunk 产出 `@out_<port>`  
- 下游 chunk 通过 `@in_<port>_file` / `@in_<port>_text` 消费

你可以把它想成：
- `edges` 决定**时序**
- `handoff` 决定**数据对象映射**

---

## 12. handoff 机制细讲（重点）

### 12.1 handoff 在计划里长什么样

下游 chunk 的 `inputs.handoff_inputs` 里通常会有：

- `from_step`：来自哪个上游 chunk
- `from_port`：来自上游哪个输出端口
- `virtual_path`：路径提示（用于声明/回退）

示意（非完整 JSON）：

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

这里的 `"draft"` 是下游输入端口名。

### 12.2 下游 SatisIL 里怎么写

**推荐（对象引用 + 读文本，与 handoff 的对象跟踪最一致）：**

```satis
Resolve file ./examples/output/upstream__draft.txt as @in_draft_file
Read @in_draft_file as @in_draft_text
Write @in_draft_text into ./examples/output/final.txt as @out_final
```

**等价替代（仅读文本、路径与 `virtual_path` 一致时）：** 若计划校验要求输入端口路径与 SatisIL 中声明一致，也可以写成：

```satis
Resolve file ./examples/output/upstream__draft.txt as @in_draft_file
Read ./examples/output/upstream__draft.txt as @in_draft_text
Write @in_draft_text into ./examples/output/final.txt as @out_final
```

调试时可在读入文本后增加一行（不影响 handoff 对象逻辑，且计划校验会把 `Print @in_draft_text` 视为使用了该输入文本）：

```satis
Print @in_draft_text
```

上层约定会要求：
- `@in_<port>_file` 对应输入对象
- `@in_<port>_text` 对应输入文本

### 12.3 运行时到底是按“路径”还是按“对象 ID”传

当前机制是：

1. 上层仍然提供路径（方便人读与声明）
2. 运行时入口会优先把 handoff 解析成**对象引用**（本质 `file_id`）
3. 下游执行时，`@in_<port>_file` 优先复用该对象引用
4. 若拿不到上游对象，才回退到 `virtual_path` 再 Resolve

这就是为什么现在即使上游发生 `Rename`，下游通常仍能跟踪到同一个对象。

### 12.4 为什么这套机制更稳

- 路径可能变（重命名、搬目录）
- `file_id` 是对象稳定身份
- 用 `file_id` 传递可以减少“路径已变但下游还拿旧路径”的失败

### 12.5 handoff 与重试的关系

chunk 重试时，会重新走入口绑定逻辑。  
只要上游对象还在，重试依然能按对象引用继续消费，不必依赖旧路径重新找文件。

---

## 13. 一张图看懂 chunk 连接

```text
[Chunk A]
  Write ... as @out_draft
      |
      |  调度层：A -> B (edge)
      |  数据层：handoff_inputs { port=draft, from_step=A, from_port=draft, ... }
      v
[Chunk B]
  Resolve ... as @in_draft_file   (运行时优先绑定为上游对象 file_id)
  Read @in_draft_file as @in_draft_text
  ...
```

一句话：  
**调度靠 edge，数据靠 handoff；声明看路径，执行优先对象 ID。**

