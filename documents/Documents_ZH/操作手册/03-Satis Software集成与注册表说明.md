# Satis Software 集成与注册表说明

日期：2026-04-23  
适用版本：当前仓库 `satis/software_registry.go`、`satis/parser.go`、`satis/runtime.go`

---

## 1. 目标与范围

本文档说明三件事：

- `Satis` 如何识别并调用外部 software
- `software_registry_dir` 的目录结构与文档规范
- `Software refresh` 如何更新文件夹级 `SKILLS.md`

---

## 2. 配置与启动行为

在 `vfs.config.json` 顶层配置：

- `software_registry_dir`: software 注册表根目录（宿主机路径）

启动时行为：

- 若目录不存在，会自动创建
- 若目录下缺少 `SKILLS.md`，会自动生成模板
- 若 `SKILLS.md` 已存在，不覆盖

说明：

- software 的安装、删除、目录组织由用户在操作系统侧管理
- `Satis` 不提供 uninstall 语句

---

## 3. 注册表目录约定

示例：

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

约定：

- 每个软件目录必须有 `forsatis.json`
- 每个软件目录必须有 `SKILL.md`
- 每个文件夹建议有 `SKILLS.md`（refresh 会自动维护）

---

## 4. 文档规范（Anthropic Skills 风格）

### 4.1 软件级 `SKILL.md`（强约束）

`SKILL.md` 必须以 frontmatter 开头：

```md
---
name: sorting
description: Sort comma-separated integers and return sorted text.
---
```

强约束：

- 必须是 frontmatter 形式（第一行 `---`）
- 必须含非空 `name`
- 必须含非空 `description`
- `name` 必须与 `forsatis.json` 的 software name 一致

不满足时：

- 该 software 会被跳过，不进入注册表
- `Software refresh` 也不会把它写入文件夹 `SKILLS.md`

### 4.2 文件夹级 `SKILLS.md`（refresh 维护）

文件夹级文档也采用 frontmatter：

```md
---
name: tools
description: Utility tools index.
---

## Entries

- sorting: Sort comma-separated integers and return sorted text.
```

说明：

- `name` / `description` 由 refresh 保留或补默认值
- `## Entries` 会按扫描结果重建

---

## 5. SatisIL 语句

### 5.1 software 调用

```text
<software_name> <function> [--flag <value> ...] [as <@var>]
```

其中 `value` 只支持：

- `@var`
- `[[[string]]]`

### 5.2 software 管理语句

支持：

- `Software pwd`
- `Software cd <path>`
- `Software ls`
- `Software find <prefix>`
- `Software describe <name>`
- `Software functions <name>`
- `Software refresh [as @var]`

不支持：

- `Software uninstall`

---

## 6. `Software refresh` 语义

执行流程：

1. 递归扫描 `software_registry_dir`
2. 校验并识别合规 software（按 `SKILL.md` frontmatter 规则）
3. 生成/更新每一级文件夹 `SKILLS.md`
4. 刷新运行时内存中的 software registry

返回报告文本（若使用 `as @var`）：

- `refreshed folders=<n>`
- `recognized=<n>`
- `skipped=<n>`

解释：

- `recognized`: 本次 refresh 识别到的合规 software 数量
- `skipped`: 不合规 software 数量（例如 `SKILL.md` 不符合规范）
- `refreshed folders`: 文件内容有变化并写回的文件夹数量

---

## 7. 典型运维流程

1. 在操作系统侧新增或调整 software 目录
2. 确保软件级 `SKILL.md` frontmatter 合规
3. 在 Satis/TUI 中执行：

```satis
Software refresh as @report
Print @report
```

4. 用 `Software ls` / `Software describe` 验证是否可见

---

## 8. 常见问题

- **Q: 为什么某个软件目录存在但无法调用？**  
  A: 多数是 `SKILL.md` 不合规（无 frontmatter、缺 `name/description`、name 不匹配）。

- **Q: 文件夹 `SKILLS.md` 会手工内容被覆盖吗？**  
  A: refresh 会重建 `## Entries` 列表；frontmatter 的 `name/description` 会尽量保留已有值。

- **Q: 能否通过 Satis 卸载软件？**  
  A: 不能。软件目录生命周期由操作系统侧管理。
