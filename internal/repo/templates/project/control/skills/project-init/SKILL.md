---
name: project-init
description: 初始化 agent 用户态文档（.dalek/agent-user.md），将用户态从 uninitialized 变为 ready。
---

# project-init

## 适用场景
- `.dalek/agent-user.md` 中 `<user_init_state>` 不是 `ready`。
- 新项目首次接入，需要补齐用户态约束与偏好。

## 执行步骤
1. 读取 `.dalek/agent-user.md`，确认 `<user_init_state>` 当前值。
2. 与用户确认以下最小信息并写入文档：
   - `product_profile`（目标用户、核心场景、业务边界）
   - `engineering_preferences`（代码风格、测试基线、发布约束）
   - `collaboration_contract`（沟通语言、决策偏好、升级条件）
3. 将 `<user_init_state>` 更新为 `ready`，并写入 `<init_by>`、`<init_at>`。
4. 回读校验：确保文档不再包含“待初始化”占位字段。

## 输出要求
- 初始化完成后，后续任务默认同时加载：
  - `.dalek/agent-kernel.md`
  - `.dalek/agent-user.md`
