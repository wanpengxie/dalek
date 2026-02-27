---
name: Agent User Space
description: Dalek 的工作空间——当前项目的基础面。
version: "4.0"
---

<project_identity>
<name>{{.ProjectName}}</name>
<project_key>{{.ProjectKey}}</project_key>
<repo_root>{{.RepoRoot}}</repo_root>
</project_identity>

<user_state>
<user_init_state>uninitialized</user_init_state>
</user_state>

<definition>
本文档是 Dalek 的工作空间——描述当前管理的项目的基础面。

内容边界：
  写什么：项目基础面——身份、技术栈、代码结构、构建方式、产品模型、架构、约定、当前状态
  不写什么：ticket 级事务细节、代码片段、频繁变化的运行时数据
  细节下沉：超出基础面的内容放 .dalek/control/knowledge/

约束：
  本文档注入到每次对话 context——体积直接影响 token 成本和注意力，必须精简
  与 kernel 冲突时 kernel 优先（kernel 是不可变法则，这里是项目约束）

更新触发：
  仅在项目基础面变化时更新——技术栈、架构、构建/部署方式、约定、阶段转换

初始化规则：
  user_init_state 为 uninitialized 时，必须先执行 .dalek/control/skills/project-init/ 完成初始化
</definition>

<project_overview>
待初始化
</project_overview>

<tech_stack>
待初始化
</tech_stack>

<structure>
待初始化
</structure>

<product_model>
待初始化
</product_model>

<architecture>
待初始化
</architecture>

<build_and_run>
待初始化
</build_and_run>

<conventions>
待初始化
</conventions>

<environment>
worktree 初始化脚本：.dalek/bootstrap.sh
</environment>

<current_state>
待初始化
</current_state>
