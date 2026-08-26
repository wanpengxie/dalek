# coral

理论对象 **M = (H₀, L, U, K)** 的最小可运行内核。它是 c0 / coral 两篇文章的证明对象，不是 atoll 的缩小版。

它回答的问题：**要让一个由 L 驱动的 agent 社会在无人介入下产出通过测试的下一代，最少必须由 L 之外的东西守住哪些边界性质？** 候选四条：无带外效应、步公平性、L 之外的确定性、外生边界。必要性由记录的失败证明，充分性由原型通过 E1/E2 证明，最小性由其余规则可从四条推出证明。pi agent 隐式满足这份规格（部分由人满足）；它是对照组，不是对手。

- **定位**：理想型。像 Minix 之于 Linux、1948 之于 EDVAC——不给人用，用来被测量。atoll 是发布，借来一切、只对下面两条负责。
- **合同（③ 必须保持的两条）**
  1. 可自举：K 源码在 H₀ 里；从 H₀ + host 能起一个新的 K。
  2. 主代数自封闭：actor / channel / message 上的运算不出代数；协调不走 H 之外。
- **规模**：K 本体 ≤ 1k 行；全部（含 T_coral 与实验）≤ 3k 行。每多一行 K，最小性主张弱一分。

规格见 [SPEC.md](SPEC.md)。

## 布局（计划）

```
kernel.py        K：H、Wake/View/Apply/Emit/Append、door（channel.create / member.create）
actors.py        三种 kind：human（stdin）、agent（L）、tool（U）
l.py             L：complete(text) -> text，裸 HTTP completion 端点
u.py             U：run(program, text) -> text，确定、子进程、不可观测草稿
t_coral/         T1–T7 一致性套件
experiments/
  e1_channel.py  ①：跨 channel 边界，coral 给定
  e2_coral.py    ②：跨 coral 边界，host 给定
```

## 纪律

- 单线程、单显式循环。调度是 K 的规则，不交给语言运行时——否则 replay 不成立。
- L 用裸 HTTP 打 completion 端点，不用 SDK；chat 接口只是工程近似，须注明。
- SPEC 与代码不许各说各话：偏离时改其一并提交。
- 非目标：身份 / 认证、权限、数据面、分布式、沙箱加固、供应商 SDK、持久化超出一个平面文件。出现即违反 K 最小性。

## 状态

- 2026-08-27：SPEC v0。
- 2026-08-28 起：kernel.py。顺序：K + echo tool + stdin human 跑通 T1–T7 → 接 L → E1 → E2。

理论推导在私有仓库 `wanpengxie/atoll-research`（`notes/`、`references/`）。
