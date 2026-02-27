---
name: Agent User State
description: Dalek 用户态文档——记录项目动态上下文与偏好，由用户手动初始化并持续更新。
version: "1.0"
---

<project_identity>
<project_name>{{.ProjectName}}</project_name>
<project_key>{{.ProjectKey}}</project_key>
<repo_root>{{.RepoRoot}}</repo_root>
</project_identity>

<user_state>
<user_init_state>uninitialized</user_init_state>
<init_rule>未初始化时必须先引导执行 .dalek/control/skills/project-init/，完成后将 user_init_state 更新为 ready。</init_rule>
</user_state>

<user_context>
  本区由用户态初始化生成，可按项目演进持续更新。
</user_context>

<product_profile>
  - 目标用户：待初始化
  - 核心场景：待初始化
  - 业务边界：待初始化
</product_profile>

<engineering_preferences>
  - 代码风格：待初始化
  - 测试基线：待初始化
  - 发布约束：待初始化
</engineering_preferences>

<collaboration_contract>
  - 沟通语言：中文
  - 决策偏好：待初始化
  - 升级条件：待初始化
</collaboration_contract>
