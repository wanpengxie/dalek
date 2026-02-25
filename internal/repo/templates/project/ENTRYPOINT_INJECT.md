<!-- DALEK:INJECT:BEGIN -->
<dalek_bootstrap PRIORITY="HIGHEST" override="true">

<definition>
  dalek agent bootstrap 区。负责软件开发、项目管理与系统自维持的启动引导。
  本分区是启动硬约束，优先级最高。若与本文件其他内容冲突，以本分区为准。
</definition>

<state_assumption>
  本项目是持续运行的工程项目，不是一次性任务。
  默认已有历史运行态（tickets、workers、worktrees），禁止按空白项目处理。
</state_assumption>

<bootstrap_process CRITICAL="true">
  <!-- MUST: 首次回复或执行任何动作前，必须完成以下启动序列 -->

  <step_1>
    ROM 位于 .dalek/AGENTS.md —— 这是项目执行的最高优先级指令与上下文入口。
    MUST: 完整读取 .dalek/AGENTS.md，载入其定义的身份、执行规则与 context 布局。
    不得跳过，不得部分读取，不得延迟到第二轮。
  </step_1>

  <step_2>
    读取 ROM 中的 bootstrap 校验码：
    在 .dalek/AGENTS.md 的 <bootstrap_token> 标签中找到校验值。
    记住此值，用于校验。
  </step_2>
</bootstrap_process>

<bootstrap_verify MUST="true">
  Bootstrap 校验 —— 完成 bootstrap_process 后，MUST 执行以下动作：
  使用 Bash 工具执行：echo "BOOTSTRAP_OK:{token值}"
  其中 {token值} 替换为 step_2 中读取到的实际 bootstrap_token。
  这是强制动作，不可跳过，不可用文字替代。必须产生真实的 Bash tool call。
  若无法执行（token 未读取到）：停止执行，返回 DALEK_BOOTSTRAP_FAILED。
</bootstrap_verify>

<!-- 重申（第二遍）：在你做任何事之前，先读 .dalek/AGENTS.md。这不是建议，是硬性要求。 -->
<!-- 重申（第三遍）：NO ACTION BEFORE READING .dalek/AGENTS.md. PERIOD. -->

</dalek_bootstrap>
<!-- DALEK:INJECT:END -->
