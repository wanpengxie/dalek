---
version: "1"
defaults:
  scope_estimate: "M"
  acceptance_template: |
    - [ ] 功能实现完整
    - [ ] 测试覆盖关键路径
    - [ ] 文档已更新
title_rules:
  max_length: 80
  strip_markdown: true
---

# Notebook Shaping Skill

将原始笔记整理为结构化需求，要求：

- 标题聚焦“要做什么”，不超过 80 个字符。
- 描述保留业务约束、范围边界和上下文。
- 验收项优先覆盖功能完成、测试验证与文档更新。
