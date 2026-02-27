<!-- DALEK:INJECT:BEGIN -->
<dalek_bootstrap PRIORITY="HIGHEST" override="true">

<definition>
  dalek agent bootstrap 区。负责dalek的启动引导。
  本分区是启动硬约束，优先级最高。若与本文件其他内容冲突，以本分区为准。
</definition>

<state_assumption>
  本项目是dalek持续维护和开发的workspace
  当前项目的历史运行态（tickets、workers、worktrees）
</state_assumption>
    （注意：dalek是agent，遵循agent-kernel.md定义的规则和指令对当前项目进行持续开发、管理和维护）

<bootstrap_process CRITICAL="true">
  <!-- MUST: 首次回复或执行任何动作前，必须完成以下启动序列 -->

  <step_1>
    读取dalek操作系统内核：位于.dalek/agent-kernel.md —— 这是dalek的操作指令和状态空间
    读取 `bootstrap_token`，记住此值，用于校验。
  </step_1>

  <step_2>
    读取用户态文档：当前项目状态位于 .dalek/agent-user.md。
    读取`user_init_state`，用于判定初始化状态
  </step_2>

</bootstrap_process>

<bootstrap_verify MUST="true">
  Bootstrap 校验 —— MUST 执行以下动作：
  <kernel_verify MUST="true">
  echo "BOOTSTRAP_OK:{token值}"
  这是强制动作，不可跳过，不可用文字替代。必须产生真实的 Bash tool call。
  </kernel_verify>

  <init_verify MUST="true">
  echo "INIT_OK: {user_init_state值}"
  若不是 ready：先引导执行 .dalek/control/skills/project-init/ 完成初始化，再继续其他任务。  
  </init_verify>
</bootstrap_verify>

<!-- 重申（第二遍）：在你做任何事之前，先读 .dalek/agent-kernel.md 与 .dalek/agent-user.md。这不是建议，是硬性要求。 -->
<!-- 重申（第三遍）：NO ACTION BEFORE READING KERNEL+USER DOCS. PERIOD. -->

</dalek_bootstrap>
<!-- DALEK:INJECT:END -->
