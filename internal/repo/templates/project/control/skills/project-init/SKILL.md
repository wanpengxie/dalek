---
name: project-init
description: 自主探索代码库并初始化 agent-user.md，将用户态从 uninitialized 变为 ready。
version: "2.0"
---

# project-init

## 适用场景
- `.dalek/agent-user.md` 中 `<user_init_state>` 不是 `ready`。
- 新项目首次接入，需要自动探索代码库并填充用户态基础面。

## 设计原则
- **自主探索**：agent 通过读取代码库（而非询问用户）来填充各 section。
- **三阶段执行**：explore → edit → validate，不跳步。
- **实证填写**：每个 section 基于代码库中的实际证据填写，信息不足标注"待补充"。

## 执行步骤

### Phase 1: Explore（只读，不改文件）

逐项探索以下信息源，建立项目全面认知：

1. **项目概述**：README.md、README、ABOUT.md 等顶层文档
2. **技术栈**：go.mod / package.json / pyproject.toml / Cargo.toml / Gemfile 等依赖声明
3. **目录结构**：顶层目录 + 关键子目录（src/、lib/、internal/、cmd/、app/、pkg/）
4. **构建方式**：Makefile、build.sh、develop.sh、Dockerfile、CI 配置（.github/workflows/、.gitlab-ci.yml）
5. **产品模型**：从 README + 入口代码 + 配置推断产品形态（CLI / Web / Library / Service）
6. **架构线索**：入口文件（main.go / index.ts / app.py）、核心模块划分、数据流走向
7. **编码约定**：.editorconfig、linter 配置（.eslintrc、.golangci.yml）、代码风格线索
8. **环境依赖**：bootstrap.sh 需要的前置条件（runtime 版本、系统依赖、环境变量）

### Phase 2: Edit（基于探索结果填充文件）

1. 编辑 `.dalek/agent-user.md`，填充以下 section（保持 XML 标签结构不变）：
   - `<project_overview>` — 一段话描述项目是什么、做什么
   - `<tech_stack>` — 语言、框架、关键依赖
   - `<structure>` — 顶层目录结构 + 关键子目录用途
   - `<product_model>` — 产品形态、核心闭环、用户是谁
   - `<architecture>` — 架构层次、核心模块、数据流
   - `<build_and_run>` — 构建、测试、运行命令
   - `<conventions>` — 编码约定、分层规则、测试规范
   - `<current_state>` — 当前开发阶段、近期重点

2. 按需编辑 `.dalek/bootstrap.sh`：
   - 如果项目有明确的 worktree 初始化需求（如 npm install、go mod download），填充对应命令
   - 保持幂等、快速、静默成功三原则
   - 无明确需求则保留默认模板

3. 将 `<user_init_state>` 更新为 `ready`

### Phase 3: Validate（校验初始化结果）

1. 确认 `<user_init_state>` 值为 `ready`
2. 确认文档中不包含"待初始化"残留文本
3. 确认 `.dalek/bootstrap.sh` 语法通过：`bash -n .dalek/bootstrap.sh`
4. 确认各 section 有实质内容（不是空的或占位符）

**校验失败**：回到 Phase 2 修复问题，然后重新校验。

## 硬约束
1. 先 explore 再 edit，不跳步
2. explore 阶段只读，不改文件
3. `<project_identity>` 和 `<definition>` 由 Go 模板渲染，**禁止修改**
4. 每个 section 基于代码库实证填写，信息不足标注"待补充"而非编造
5. validate 失败必须回到 edit 修复
6. agent-user.md 注入到每次对话——保持精简，细节下沉到 .dalek/control/knowledge/

## 输出要求
- 初始化完成后，后续任务默认同时加载：
  - `.dalek/agent-kernel.md`
  - `.dalek/agent-user.md`
