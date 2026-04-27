# 枢念 Satis：一次可能的范式转变思考

<h3 align="center">若人欲了知，三世一切佛，应观法界性，一切唯心造。——《华严经》</h3>

> 前注：该仓库出于我的业余兴趣搭建，与任何学校、任何企业的研究项目、课题无关。由于本人还是研究生，致力于AI for Biology的课题，因此后续该仓库会看研究工作紧张与否，不定期更新。谢谢支持！
> 
> 你可以用各种Agent来帮助你理解和运行Satis，我专门写了一个`README_AGENT.md`，你可以直接让Agent先看看这个文档！
>
> 关于Satis的使用说明细节，或者了解该系统的理念、故事，你可以阅读`documents`文件夹里的文档。

Satis 的名字来源于佛教术语“正念”（Sati），我把它理解为一种“让意图保持清明、让行动可被观照”的工程方法，这个系统中文名我取作**“枢念”**，我的思路是：在复杂任务中，真正值得沉淀的可能是可复用的意图结构与思路路径。

也因此，Satis 试图在现有 harness 范式上再往前走一步：不仅做“模型 + 工具”的调用编排，还把过程中的意图、分支、约束与回看机制结构化，让流程既能动态探索，也能在运行前收敛为可审计执行。沿着这个方向，我提出了一种所谓“软件 + 智件”的分层构想：**软件（software）作为稳定、可替换的固定计算和控制单元；智件（smartware）作为可复用、可演进的思路单元**，承载“何时做什么、为何这样做、失败后如何改道”的经验资产。Satis命名还有一个巧思，就是将软件“念化”的过程：可以有趣地翻译成“Satisfy”！

Satis的运行逻辑，有两种解释：

- 软件运行在操作系统层，操作系统是控制硬件的软件
- 智件运行在satis系统层，satis为软件工程师提供统一的接口规范，可以让软件进行各种排列组合，实现和程序世界的交互。

总的来说，就是smartware -> software -> hardware的层级逻辑。

从当今的AI Agent发展来看，Satis可以看作是一个简单的 Harness 工程，亦可看作是一个AgentOS。它能将思维与控制解耦合（或换个角度，思维路径是当下控制的“元控制”），**让软件和思路分离**。这尤其适合做长程研究，因为长程研究思维链很长，且现有Harness无法追溯和审计思维链。我相信这个项目有作为研究者的得力工具。

## Satis 快速上手（面向首次使用者）

抱歉地告诉大家，Satis不支持Windows系统，目前只针对Darwin (macOS)和Linux系统适配（请原谅TAT）。后续若有支持，我一定会尽快补充的！

---

## 0) 设计哲学（简版）

`Satis` 是一个把复杂任务变成可编辑、可运行、可审计的过程系统。是我在工程博士生涯中的一次小灵感，并做成了一个业余的、构思还算完善的系统（多亏了Vibe Coding! 不过一坨屎山代码是板上钉钉的 哭!）。该系统未来可能会慢慢更新，若有更多社区的朋友支持，未来我会继续努力的！

- **局部确定，全局生长**：`Chunk` 负责“这一步怎么做”，`Plan` 负责“下一步走哪里”。
- **边界先于能力**：VFS 内部工作区与外部系统文件通过 `Load` 显式桥接，避免越界读写。
- **先探索，后收敛**：允许草稿迭代，但在 `render/submit/run` 时强约束校验。
- **软件与思路分层**：`Satis` 的 Plan/Chunk 是可复用思路结构，而**软件是可调用的能力，在该系统中可以简单地被一个统一的接口调用**。软件调用 + 控制流这样的可复用思路结构，暂时称之为“智件” (Smartware)。

从这些角度看来，`Satis` 系统可以被看作是一种“可追踪的 AI 工作流内核”。

---

下面是使用步骤，你可以快速通过这些步骤上手玩玩这个小系统。

## 1) 配置 VFS

### 1.1 复制配置文件

在仓库根目录执行：

```bash
cp vfs.config.example.json vfs.config.json
```

### 1.2 修改 `vfs.config.json`

示例（请替换成你自己的绝对路径）：

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

### 1.3 这几个目录分别做什么

- `mount_dir`：VFS 对外“挂载视图”目录。
- `state_dir`：VFS 运行状态与事件数据。
- `system_port_dir`：操作系统文件导入入口（`Load` 从这里读）。
- `software_registry_dir`：软件注册表根目录。

### 1.4 安全边界说明（你特别关心的点）

当你把以上目录都放在某个主目录（例如 `/Users/you/Desktop/satis_backend`）下时，`Satis` 的系统数据与纳管内容会落在这个主目录内，不会在系统内部随意越过该边界去写其他路径。  
外部文件读取也必须经由 `system_port_dir + Load` 的显式导入路径完成。

---

## 2) 配置 `invoke.config.json`（接入你自己的 API）

### 2.1 复制模板

```bash
cp invoke.config.example.json invoke.config.json
```

### 2.2 按你的模型服务填写

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

字段含义：

- `base_url`：你的模型网关地址（OpenAI 兼容格式）。
- `api_key`：对应密钥。
- `model`：模型名。
- `router.default_provider`：默认走哪个 provider。

> 建议：`invoke.config.json` 不要提交到公开仓库。

---

## 3) 进入 CLI（`runtui.sh`）+ Fancy 示例

### 3.1 启动

```bash
./runtui.sh
```

脚本会自动做 preflight（检查 OS、Go、配置文件、基础测试）后启动 TUI。

### 3.2 平台说明

目前 `runtui.sh` 仅支持：

- macOS (`Darwin`)
- Linux

**暂不支持 Windows。**

### 3.3 先跑几个 Fancy 示例（line 模式）

一旦接入你自己的LLM服务商，请记住：系统没有任何内置Prompt，所有的Token consumption都是你自己输入的Invoke指令中的！你可以从以下最最简单的指令开始：

```text
invoke [[[hello, how are you?]]]
```

若你的LLM服务商可用，则你可以快速看到一个LLM response在控制台。为了避免和LLM输出重复，**字符串被设计成```[[[strings]]]```的结构，用三重中括号包裹**。

进入后你会看到 `satis (line)>` 提示符，**依次**输入（指令大小写不敏感、字符串大小写敏感）：

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

再来一个并发/批处理风格示例（如果你的模型与语法已对齐）：

```text
Write [[[苹果\n香蕉\n西瓜]]] into /demo/fruits.txt as @f2
Read @f2 as @fruits
Invoke [[[把每一行变成“水果名 + 一句8字卖点”]]] with @fruits as @ads
Print @ads
```

---

## 4) Workbench 使用说明（含最小例子）

### 4.1 进入 Workbench

在主 CLI 输入：

```text
/workbench /plans/demo a demo
```

如果 `/plans/demo/plan.json` 不存在，会自动创建最小合法模板。

### 4.2 常用操作（够你起步）

- `Tab / Shift+Tab`：切焦点（图、编辑器、handoff、CLI）。
- `Ctrl+S`：保存（`save`）。
- `Ctrl+Shift+R`：渲染校验（`render`）。
- `Ctrl+N`：新增 task 子节点（在图焦点下）。
- `Shift+D`：新增 decision 子节点。
- `Ctrl+P`：命令面板。

### 4.3 最小 Workbench demo

进入后在右侧 CLI 输入：

```text
validate
run
status
events
inspect
```

这套流程可快速验证：计划可提交、可运行、可观测。

---

## 5) 软件调用说明：设计并注册一个可被satis规范调用的简单软件

目标：注册一个 `sorting` 软件，让 `Satis` 能调用它。

### 5.1 在 `software_registry_dir` 下建立目录

目录结构示例：

```text
software_registry/
  tools/
    sorting/
      forsatis.json
      SKILL.md
      sort.py
```

### 5.2 写 `SKILL.md`（必须 frontmatter）

```md
---
name: sorting
description: Sort comma-separated integers and return sorted text.
---
```

### 5.3 写 `forsatis.json`（声明函数接口）

按你当前 runner 规范填写函数、参数、入口脚本（字段请与项目现有可运行样例保持一致）。

### 5.4 在 Satis 中刷新注册表

```text
Software refresh as @report
Print @report
Software ls
Software describe sorting
Software functions sorting
```

### 5.5 调用软件

```text
sorting sort --input [[[3,1,2]]] as @sorted
Print @sorted
```

---

## 6) 操作系统文件调用说明（如何读 OS 文件）

在 `Satis` 中，操作系统文件不是直接乱读，而是通过 `system_port_dir` 显式导入。

### 6.1 准备 OS 文件

先把宿主机文件放到你配置的 `system_port_dir` 目录下，例如：

```text
<system_port_dir>/inputs/paper.txt
```

### 6.2 在 Satis 中导入并使用

```text
Load pwd
Load ls
Load inputs/paper.txt
Read /paper.txt as @paper
Invoke [[[请提取这篇文本的3个关键观点]]] with @paper as @k
Print @k
```

要点：

- `Load` 走的是 `system_port_dir` 命名空间（外部导入区）。
- 导入成功后，文件会进入当前 VFS 工作区，再通过 `Read/Write/Invoke` 继续处理。

---

## 10 分钟最小 Demo

1. 准备配置：

```bash
cp vfs.config.example.json vfs.config.json
cp invoke.config.example.json invoke.config.json
```

2. 修改两个配置中的路径和 API 信息。  
3. 启动：

```bash
./runtui.sh
```

4. 在 CLI 跑：

```text
Cd /demo
Write [[[Satis is a structured runtime for AI workflows.]]] into a.txt as @f
Read @f as @t
Invoke [[[Translate to Chinese in one sentence.]]] with @t as @zh
Write @zh into /demo/zh.txt as @out
Print @zh
```

5. 打开 Workbench：

```text
/workbench /plans/demo just to demonstrate it
```

随后在弹出的Plan description中输入“just a plan”

6. 连续按下多次tab键，直到光标切换到最右侧的 Workbench CLI ，然后依次输入：

```text
validate
run
inspect
```

如果上面都成功，你已经完成了一个端到端 demo：  
**配置 VFS -> 接入模型 API -> CLI 执行 -> Workbench 运行计划 -> 观测结果。**

---

## 常见问题（首次使用）

- 启动失败提示 OS 不支持：当前脚本仅支持 macOS/Linux，Windows 暂不支持。
- `Load` 失败：检查 `vfs.config.json` 的 `system_port_dir` 是否存在。
- `Invoke` 不通：检查 `invoke.config.json` 的 `base_url/api_key/model/default_provider`。
- Workbench 跑不起来：先在 Workbench 内 `validate` 看诊断，再修 handoff/结构。

---

## License

Copyright (c) 2026 Wilson Huang.

This project is licensed under the **Creative Commons Attribution-NonCommercial-ShareAlike 4.0 International License (CC BY-NC-SA 4.0)**.

To view a copy of this license, visit [http://creativecommons.org/licenses/by-nc-sa/4.0/](http://creativecommons.org/licenses/by-nc-sa/4.0/).

---

如果你准备继续深入，建议阅读：

- `documents/Documents_ZH/操作手册/02-Satis CLI详细使用教程.md`
- `documents/Documents_ZH/操作手册/03-Satis Software集成与注册表说明.md`
- `documents/Documents_ZH/操作手册/04-Satis Workbench使用说明.md`


当然，`documents/Documents_EN/satis_s_story`里的文档也挺有意思的！

> 本README文档大部分由 LLM 辅助撰写，我提供写作思路与灵感