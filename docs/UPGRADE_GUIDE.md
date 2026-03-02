# Dalek 项目内优雅升级指南

本文档对应 `dalek upgrade` 能力，目标是让项目升级具备以下特性：
- 可预演：支持 `--dry-run` 查看将发生的变更。
- 可恢复：升级前先备份关键文件，失败时可人工回退。
- 可校验：升级后做版本、配置、控制面完整性校验。

## 1. 适用范围

`dalek upgrade` 用于**已初始化**的 dalek 项目（存在 `.dalek/config.json`）。

升级覆盖的内容：
- DB migration（`schema_migrations` 驱动）
- 项目配置 schema 默认值补齐
- control plane 文件与 skills 模板更新
- manager bootstrap 元信息补齐
- 入口文件注入块修复（`AGENTS.md` / `CLAUDE.md`）
- 项目 dalek 版本写入 `.dalek/.dalek_project.json`

## 2. 快速执行（推荐顺序）

1. 查看当前 binary 版本

```bash
dalek version
```

2. 先做预演，不落盘

```bash
dalek upgrade --dry-run -o json
```

重点看这些字段：
- `already_latest`
- `changes`
- `backups`（dry-run 下为预览路径）
- `warnings`（例如 daemon 正在运行、仍有 running worker）

3. 执行真实升级

```bash
dalek upgrade
```

4. 如果 daemon 运行中，重启生效

```bash
dalek daemon restart
```

## 3. 常用参数

- `--dry-run`：只预览，不写入
- `--force`：即使项目记录版本与当前 binary 一致，也强制执行升级
- `--project` / `-p`：指定项目名（不传则从当前目录推断）
- `--home`：指定 dalek home
- `-o json`：机器可读输出，便于 CI/脚本使用

示例：

```bash
dalek upgrade --project demo --dry-run -o json
dalek upgrade --project demo --force
```

## 4. 升级后检查清单

建议至少执行以下检查：

```bash
dalek upgrade --dry-run -o json
dalek ticket ls -o json
dalek merge ls -o json
```

判断标准：
- `upgrade --dry-run` 返回 `already_latest=true`（表示本轮升级后已对齐）
- ticket/merge 查询可正常返回（代表核心链路未被破坏）

## 5. 失败恢复（人工回滚）

`dalek upgrade` 真实执行前会创建备份，命名形如：

```text
<原路径>.bak.<UTC时间戳>
```

如果升级失败且需要回滚：

1. 从升级输出中找到备份文件路径
2. 将备份覆盖回原文件（DB/config/control 文件）
3. 重启 daemon
4. 重新执行 `dalek upgrade --dry-run -o json` 验证状态

## 6. 当前已知限制

- 当前回滚是“基于备份文件的人工恢复”，不是单事务自动回滚。
- 升级对 running worker 仅告警不阻塞；建议在低峰时段执行。
- control/skills 会按模板覆盖更新，`control/knowledge` 保留用户内容。

## 7. 你这两天没空细测时的最小安全策略

建议只做下面这组操作：

```bash
dalek upgrade --dry-run -o json
dalek upgrade
dalek daemon restart
dalek upgrade --dry-run -o json
```

若最后一步显示 `already_latest=true`，可先认为升级闭环完成；详细链路回归等你有空再补。
