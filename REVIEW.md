# Dalek Core v4 — 审阅材料

审阅标准：**表达能力与理论见证**，不是安全性。代码：`kernel.py`（314 行）。实验：`experiments/e1.py`、`e2.py`。测试：`t_dalek/test_t.py`。出生证明：`FAILURES.md`。

## 1. v4 相对 v3 的改动（对应 codex 第二轮审阅）

| codex 指出 | 处置 | 在哪 |
|---|---|---|
| E1 的 L 替身直接读 Space | Apply 签名改为 (自己, view)；只有 K 内建的 H 见 Space；替身需要历史时问 H `steps <me>`（E1 每代问一次） | F14 |
| `in` 不验证属于 parts | `in ∈ parts ∪ 根`，`out ∈ 本机地址簿`，否则整条拒绝 | F15；T5 |
| H 投影不可逆 | `H msg` 返回消息 JSON；`decode(H.msg(addr)) = ledger[addr]` | F16；T7 |
| 子机器身份由全局计数分配 | id = 声明地址（`c0/17` → `c0.17`，孙代 `c0.17.17`）；谱系在名字里 | F17；T5 |
| 回复固定给声明者是隐式 out | `out` 进 D，缺省声明者，写进子 genesis | F18 |
| `disable/enable`、多收件人、`g`、`n` | 全部删除（无出生证明） | FAILURES 末节 |
| wiring 藏在 prefix 文本里 | 零件 prefix 不再重绑定，只有 `in`/`start` 重绑定；描述应位置无关 | 已声明 |
| E1/E2 的主张过强 | 收窄：E1 = 递归自我组织；E2 = K 自举 | README |
| 停机语义、M/P 网关、replay 只重建首步前 door 事实、根实现在 H 外、M 不能作 part | 声明为 milieu 结构，不修 | FAILURES 末节 |

## 2. 这一版实现的机器

| 对象 | 实现 |
|---|---|
| 带子 / 机器 | 逻辑上每机一条（`ch`），物理上一个 `ledger.jsonl`。地址 = `ch/seq` |
| 消息 | `(ch, seq, sender, to, body)`；前三项 K 写；`to` 单个地址 |
| 根 | `L`、`U`（注入，只见 view）；`H`（K 内建，见 Space，确定，replay 重算）。外生根经 `#admit`，账本静止时轮询 |
| 派生地址 | `#decl L|U <prefix>`：根的部分应用；id = 消息地址 |
| 机器 | `#decl M`：`part`（逐字复印）+ `in` + `out`（缺省声明者）+ `start`（重绑定）；子 id = 声明地址；子带子前两条是 genesis（含 parent/in/out/map）与 `#admit parent=`；父带子里该地址成为 M 地址 |
| 网关 | 发给 M 地址 → 落子带（sender `P:<子>`，to 接待员）；子发给 `P:` → 落父带（sender = M 地址，to = `out`）。M/P 不被 step |
| 词的作者 | door：genesis/admit/decl；K：step；成员：decl；机器：只说文本 |
| K | 沿账本走；每条消息的收件人跑一步；静止时轮询外生根；全空即停 |
| replay | 同一段 `run`；U/H 重算；L/X 照抄；逐字节比较 |

## 3. 实验

**T1–T7**：K 源码在 H₀；replay 与篡改发散；成员说不了内核词；U 无 H 外通道；只有 `#decl M` 造机器、坏配方（零件不存在 / 接待员不在零件里）整条拒绝、越界丢弃、id = 声明地址、启动消息送达接待员；账本序公平（内部对话不饿死排在前面的消息）；两问封闭 + H `msg` 精确。

**E1（递归自我组织）**：c0 → c0.17 → c0.17.17。构造者只经 view 与 H；每代造工具、测试、问 H 回忆代数与工具地址、`#decl M` 复印自己和工具并交代目标；human 消息 1 条；replay identical。

**E2（K 自举）**：spawner 向 H 要 `msg c0/1`（JSON），把源码写到新目录、新进程里 genesis + 跑 + 自我 replay；子 K sha == 父 K sha；human 消息 1 条；父 replay identical。

## 4. 请审的问题

1. **D = part + in + out + start** 现在是否已经是"边界写进描述"的最小完整形式；`out` 缺省声明者是否仍藏着什么。
2. **只对 `in`/`start` 重绑定、零件逐字复印**：这是否把 relocation 问题正确地推给了"描述位置无关"这条纪律，还是只是把问题藏进了 prefix。
3. **H 作为 K 内建根**（不在 apply 表里，见 Space）：这是否与"根是 milieu 原语"的说法一致——H 显然不是 milieu 给的，它是 K 的一部分。词表要不要把 H 从"根"改称"K 的投影接口"。
4. **id = 声明地址**带来的后果：名字里带谱系（`c0.17.17`）。这是特性还是泄漏（一台机器的名字暴露了它的出身）。
5. 删除 `disable/enable` 后，**替换**（不 in-place）在当前词表里怎么表达：新地址 + 把边指向新地址 + 旧地址永远不再被点名。这是否足够，还是"旧地址还会被轮询/占用"会在某个实验里成为失败。
6. E3（独立单元复制：子机器在另一进程且可寻址）需要什么最小机制：一个把"另一进程的带子"接成外生根的桥。它是 K 的词，还是一个 U 程序。
7. 还有没有新的隐藏结构。

## 5. 已知未做

真 L；四个"自我"的最简策略程序；E3；`SPEC.md`/`DESIGN.md` 未重写。

## 6. 怎么跑

```
python3 t_dalek/test_t.py
python3 experiments/e1.py /tmp/e1
python3 experiments/e2.py /tmp/e2
python3 kernel.py show|book|replay DIR
```
