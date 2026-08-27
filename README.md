# Dalek Core

一台可以自我维持、自我改进、自我复制、自我组织的智能单元的最小内核。副标题：如何优雅地把 LLM 塞进冯诺依曼机器。

它不是 agent 框架。它是一条账本和一个沿着账本走的控制器；agent、工具、人、别的机器——全是账本上的地址，用同一套词说话。四个"自我"不是内核功能，是这套词上的普通程序。

- **Dalek**：机器的名字（用户的另一个项目）。**Dalek Core**：本仓库，内核。**c0**：第一条 channel，内核所在。
- 状态：`kernel.py` 318 行；T1–T7 全绿；E1（机器造机器，c0 → c1 → c2，人只说一句）与 E2（Dalek Core 起 Dalek Core′，K 逐字相同）通过，两者 replay 逐字相同。真 L 尚未接入（`l.py` 已备）。

## 一页纸

**对象**：一条 append-only 账本（每台机器一条带子，物理上一个 `ledger.jsonl`）；消息 `(ch, seq, sender, to[], body)`，前三项由 K 盖章；成员 = 带子上的一条描述，id 就是那条消息的地址 `ch/seq`。

**根**（每台机器内建，可直接寻址）：`L` 想（文本→文本，随机）、`U` 算（程序+文本→文本，确定，每步全新草稿）、`H` 记（带子的只读投影，问询本身入带）。外面的东西（人、时钟、别的机器）经 `#admit` 成为外生根。

**词**（body 第一行；说错话的人，他的话只是文本）：

| 词 | 谁能说 | 造世界的动作 |
|---|---|---|
| `#genesis` | door | 一条带子开始；c0 的 genesis 携带 K 的源码 |
| `#admit` | door | 外面的东西成为一个地址 |
| `#decl L\|U` | 成员 | 一段描述成为一个地址——**创建 = 描述** |
| `#decl M` | 成员 | 一组描述（part）+ 接待员（in）+ 启动消息（start）成为**一台新机器**，并绑成本机的一个地址（channel as actor）；复印时地址按 map 重绑定 |
| `#disable` / `#enable` | 成员 | 逻辑删除；enable 补发积压 |
| `#step` | K | K 记下自己这一步（actor、看到哪、原文 out）；replay 用 |
| 普通消息 + `to` | 任何地址 | 一条边 |

**K**：沿账本走，对每条消息的每个收件人跑一步（view → apply → 记 `#step` → parse → append）；账本走完轮询外生根，一轮无人开口即停。没有 agent loop，没有 `*`，没有调度器——公平性就是账本顺序。

**replay**：同一段 `run`，apply 换成读 `#step` 记录；`U`、`H` 重算而非照抄；文件逐字节比较。任何带外效应、任何对确定根结果的篡改都使其发散。它检验因果自洽，不检验 L 的真实性。

## 跑

```
python3 experiments/e1.py /tmp/e1     # 机器造机器：c0 → c1 → c2，人只说一句；打印带子、四个条件、replay
python3 experiments/e2.py /tmp/e2     # Dalek Core 起 Dalek Core′：子机器自我 replay、K sha 相同
python3 kernel.py show|book|replay DIR
python3 -c "..."                       # 测试：见 t_dalek/test_t.py（无 pytest 时用文件头的跑法）
```

E1/E2 里的 L 是确定的状态机替身（`experiments/e1.py: constructor`）。接真模型：设 `DALEK_L_URL`，把 `apply["L"]` 换成 `l.L`。

## 文件

```
kernel.py        K：对象、文法、账本（append 唯一写路径；折叠只改内存；跨界由 door 在写入时做）、派发、根、replay
l.py             L：裸 completion 端点（未测：本机无端点）
experiments/     e1.py e2.py
t_dalek/         T1–T7
FAILURES.md      每条法律的出生证明（F1–F13）
SPEC.md DESIGN.md  设计时的假设清单与设计（部分已被 kernel.py 超过；以代码与 FAILURES 为准）
```

## 纪律

- 单线程、单显式循环；K 内无墙钟、无随机源；唯一非确定点是 apply。
- 不做安全加固：本仓库是理论原型，恶意账本、伪造文件、U 越权不在范围内（③ 的事）。
- 每条进 K 的法律先有一份 FAILURES 记录；没有失败要求的机制不进来。
