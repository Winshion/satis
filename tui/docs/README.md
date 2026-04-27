# Satis TUI

基于终端的 REPL，运行在 `satis.Executor` 之上，与配置的 VFS、Invoker 共用一套运行时。

## 斜杠命令

- `/help` — 完整帮助
- `/history`
- `/exit` — 有未提交 chunk 时需先 `/commit` 或 `/cancel`
- `/clear` — 仅清屏
- `/clearchunk` — 丢弃未提交状态并重置 session
- `/mode line|chunk`
- `/begin` / `/commit` / `/cancel`
- `>>>` / `<<<` — 仅在 CLI 的 `line` 模式下可用，等价于一次临时 `/begin ... /commit`；批量中的多条命令必须用换行分隔，不支持分号
- `/exec PATH.satis` — 执行**主机**上的 `.satis` 文件
- `/workbench VFS_WORKSPACE_DIR` — 全屏工作台。若目录下没有 `plan.json`，会自动创建目录与默认 Chunk Graph 模板，并走 bridge 提交与运行

## 输入行为

- **line**：普通行立即按 SatisIL 执行，成功后自动 Commit（`Commit`/`Rollback` 除外）
- **line**：支持用 `>>>` 开始、`<<<` 结束一次多行批处理；`>>>`/`<<<` 与命令之间可有或无换行，但命令与命令之间必须用换行分隔；这是 CLI 便捷语法，不属于 SatisIL
- **chunk**：普通行进入缓冲，直到 **`/commit`** 一次性执行
- **Tab**：补全 TUI 命令、SatisIL 指令名、虚拟路径、`Load` 下的 **system_port** 逻辑路径（与 VFS `cwd` 独立）、`/exec` 的主机路径

## 相关文档

仓库内更完整的说明见：`documents/Satis TUI详细使用教程.md`。
