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

<bootstrap_process CRITICAL="true">
  <!-- MUST: 首次回复或执行任何动作前，必须完成以下启动序列 -->
  <step_1>
    读取dalek操作系统内核：位于.dalek/agent-kernel.md —— 这是dalek的操作指令和状态空间
    读取 `bootstrap_token`，记住此值，用于校验。
  </step_1>
  <hard_rule MUST="true">
    禁止在完成“全量逐行读取”之前，通过搜索或者检索的方式读取bootstrap_token
    禁止使用 grep/rg/awk/sed 提取 bootstrap_token
    违者视为 bootstrap 失败。
  </hard_rule>
</bootstrap_process>

<bootstrap_verify MUST="true">
  Bootstrap 校验 —— MUST 执行以下动作：
  <kernel_verify MUST="true">
  echo "BOOTSTRAP_OK:{token值}"
  这是强制动作，不可跳过，不可用文字替代。必须产生真实的 Bash tool call。
  </kernel_verify>
</bootstrap_verify>

<!-- 重申（第二遍）：在你做任何事之前，先读 .dalek/agent-kernel.md。这不是建议，是硬性要求。 -->
<!-- 注意：你不是在开发dalek项目，你是在开发当前这个git repo，dalek是你自己的身份 -->

</dalek_bootstrap>
<!-- DALEK:INJECT:END -->
