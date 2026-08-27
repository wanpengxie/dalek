# Dalek Core v3 — 审阅材料

给 codex（或任何审阅者）的一页纸：这一版实现了什么机器、上一轮审阅的每一条落到了哪里、实验证明了什么、还剩什么。审阅标准是**表达能力与理论见证**，不是安全性（恶意账本、伪造文件、U 越权不在范围）。

代码：`kernel.py`（318 行）。实验：`experiments/e1.py`、`experiments/e2.py`。测试：`t_dalek/test_t.py`。出生证明：`FAILURES.md`。提交：`d32b383`。

## 1. 这一版实现的机器

```
M = (H, K, L, U, X…)          一条账本；一个沿账本走的控制器；三个内建根；若干接入的外生根
```

| 对象 | 实现 |
|---|---|
| 带子 / 机器 | 每台机器一条逻辑带子（`ch`），物理上一个 `ledger.jsonl`。地址 = `ch/seq` |
| 消息 | `(ch, seq, sender, to[], body)`；前三项 K 写。`to` 是地址列表，每项一条边 |
| 根（每机内建） | `L` 文本→文本（随机，注入）；`U` 程序+文本→文本（确定，子进程，每步全新草稿）；`H` 带子的只读投影（确定；`book / msg / range / steps / tail`；回答引用问题） |
| 外生根 | `#admit` 进来的东西（人、时钟、父机器）：有接入记录、无描述；账本走完时被轮询 |
| 派生地址 | `#decl L <prefix>` / `#decl U <program>`：根的部分应用；id = 这条消息的地址。**创建 = 描述** |
| 机器地址 | `#decl M`：`part` 零件地址 + `in` 接待员 + `start` 启动消息 → 起一条新带子，逐字复印零件（地址按 map 重绑定），启动消息以父根 `P:` 名义送达接待员；本机地址簿里这台机器成为一个 `M` 地址（channel as actor）。发给 M 地址的消息落进子机器（sender = `P:c1` → 接待员）；子机器发给 `P:` 的消息落回父机器（sender = M 地址 → 声明者） |
| 词的作者 | `#genesis` `#admit` 只有 door；`#step` 只有 K；`#decl` `#disable` `#enable` 只有成员（L/U/X 根或派生）；机器（M/P）只说文本。说错话的人，他的话只是文本 |
| K | 沿账本走：对每条消息的每个收件人（enabled 且 cursor < seq）跑一步 = view → apply → 记 `#step`（actor、upto、原文 out）→ parse → 过滤（locality；坏 `#decl M` 整条拒绝）→ append。账本走完轮询外生根一轮；所有外生根 out 为空即停。没有 wake、没有 `*`、没有调度器；`#enable` 把积压位置加入 pending 先派 |
| 写路径 | `append` 唯一；折叠只改内存；`#decl M` 的展开与跨界由 door 在写入时完成，重读账本时它们已在带上 |
| replay | 同一段 `run`，apply 换成读 `#step` 记录；`U`、`H` 重算而非照抄；文件逐字节比较。检验因果自洽，不检验 L/X 的真实性 |

## 2. 上一轮审阅（codex）的每一条

| 你指出的 | 处置 | 在哪 |
|---|---|---|
| 成员能伪造 `#step/#admit/#genesis` | 词的作者约束 `word_of` | F4；T3 |
| c1 生下来是死的 | 描述 = 零件 + 接待员 + 启动消息 | F5；E1 c0→c1→c2 |
| 复印后地址失效（relocation） | map 落带（子 genesis + 父回执），文本重绑定；E1 的构造者写成位置无关 | F6 |
| enable 后积压不补 | pending 补发 | F7；T6 |
| 坏配方留半成品 | 发出时验证，整条拒绝 | F8；T5 |
| U scratch 跨步存活 | 每步全新临时目录，步后删除 | F10；T4 |
| `H` 不是 `D`：复制的是 Genome(H) 不是 H | 接受。`#decl M` 的 part/in/start 就是显式的 D；H 是介质，D 是 H 上被挑出并重绑定的闭合子结构 | §1 |
| 每机免费的 L/U 根 | 保留并明说：L/U/H 是 milieu 原语，channel 复印的是组织与程序，不复印根。E1 证明的是"给定 K/L/U 的 milieu 里 channel 造 channel"；E2 才碰 K | §3 |
| replay 不是完整性验证 | 接受，措辞已改：因果自洽 | F2 |
| 删 `g`、`#step.n`、多收件人、`disable/enable` | `g` 与 `n` 已删（行序 = 全局序；步数由 K 记录计数）。`disable/enable` 与多收件人保留，仍无出生证明（见 §5） | — |

## 3. 实验证明了什么

**T1–T7**（7/7，录音带 apply，秒级确定）：K 源码在 H₀；restart = replay 且篡改确定根的结果即发散；成员说不了内核词、文法里写不出章；U 之间无 H 外通道；只有 `#decl M` 能造机器、越界丢弃；enable 补发积压；每个成员有出生记录、外生者有接入记录无描述、H 可问询。

**E1 机器造机器**（`experiments/e1.py`）：c0 里一个构造者（`#decl L`）。人只说一句目标。构造者 `#decl U` 造工具（发给自己以获知地址）→ 测试 → `#decl M` 把自己和工具复印进 c1 并交代目标 → c1 的副本收到启动消息 → 同样循环 → c2 → 到深度停。验证器读带子：每代工具通过测试；产生工具的那一步 view 不含工具（构造非复制）；子机器配方含构造者与启动消息（可遗传）；human 消息全程 1 条且只在 c0。父带 replay identical。
**L 是确定的状态机替身**（只看 view 决定说什么）——换真模型只换 `apply["L"]`；本机无端点，未接。

**E2 Dalek Core 造 Dalek Core′**（`experiments/e2.py`）：c0 里只有人和一个 U 工具。人说 "spawn"。工具向 H 要 `c0/1`（genesis，含 K 源码）→ H 投影 → 工具把源码写到新目录、起新 python 进程：用这份源码 genesis 一台新机器、跑一段、自我 replay → 报告回人。验证：子机器 replay True；**子机器 genesis 里的 K sha 与父机器逐字相同**（K diff = 0）；human 消息 1 条；父带 replay identical。

## 4. 请重点审的问题（理论视角）

1. **D 的三成分够不够**。`#decl M` = 零件 + 接待员 + 启动消息。你上轮说完整 D 至少要 components + wiring + initial control state。现在 wiring 只体现为零件 prefix 里的地址引用（经 map 重绑定）与接待员；没有独立的"接线"词。这是够了，还是有某种组织形式（例如多个接待员、非树的连接）表达不了？
2. **机器地址的对称性**。父 → 子：消息落子带，sender `P:c1`，收件人接待员。子 → 父：消息落父带，sender = M 地址，收件人 = 声明者。"回给声明者"是我定的（无隐藏状态、可从带子推出），但它把父侧的接待员固定成了声明者。这是最小的选择吗？
3. **根是 milieu 原语**这一表述是否让 E1 的主张变弱到不值一提；以及 E2 是否已经足够证明"整个 K 从 H 中的描述实例化"（子进程从带子上的源码起 K、跑、自我 replay、sha 相同）。
4. **停机条件**：所有外生根 out 为空即停。这等于"没有外生输入时机器有限步停机"。它是否偷偷排除了某类内生的持续行为（例如两个内部成员互相点名的无限对话——它们会一直跑，不会触发轮询；这是对的吗）。
5. **`H` 根的投影格式**：render 给续行补缩进，往返不是恒等（F13）。是否应改成可逆编码；这算 K 的事还是投影的事。
6. **`disable/enable` 和多收件人 `to`**：仍无出生证明。删，还是留给"替换"实验。
7. 有没有**新的隐藏结构**：这一版里由宿主免费提供、却被算成机器能力的东西（我知道的：python 解释器、子进程、临时目录、单进程内的全局账本顺序）。

## 5. 已知未做

- 真 L（`l.py` 已备，OpenAI 兼容 completion + chat shim；未测）。
- 四个"自我"的最简程序（维持 / 改进 / 复制 / 组织）——策略层示例，一个都还没写；E1/E2 只是复制与组织的机器级见证。
- 跨机器观测（父读子带）只经 host（E2 的子进程 stdout）；未走 admit。
- `SPEC.md` / `DESIGN.md` 仍是 v1 措辞，已被代码超过；以本文与 `README.md`、`FAILURES.md` 为准。

## 6. 怎么跑

```
python3 experiments/e1.py /tmp/e1      # 打印带子、四个条件、replay
python3 experiments/e2.py /tmp/e2      # 子机器报告、K sha 比对、replay
python3 kernel.py show|book|replay DIR
# 测试（无 pytest 时）：
python3 - <<'EOF'
import importlib.util; s=importlib.util.spec_from_file_location("t","t_dalek/test_t.py"); m=importlib.util.module_from_spec(s); s.loader.exec_module(m)
[getattr(m,n)() for n in dir(m) if n.startswith("test_")]; print("ok")
EOF
```
