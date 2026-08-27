# Dalek Core — 代码设计 v1（2026-08-27）

目标：最小 Dalek Core，Python，单进程、单线程、单显式循环。K 本体 ≤ 800 行。
公开交付物：**一盘可重放的带子**——gen1 → gen2 的完整 H，任何人 `dalek replay` 得到逐字相同的结果，其中没有一条人类消息，gen2 通过 gen1 通不过的测试。

本设计取代 v0。与 v0 的实质差异：door 不再有效应词（创建 = 描述，由折叠规则实现）；外生成员有接入记录、无构造描述；"I ∈ H"升为不变量；Emit 文法改为指令行而非 JSON。

## 0. 三条不变量（内部纪律，不进公开文本）

| | 内容 | 由谁守 |
|---|---|---|
| **因果闭包** | 系统的每个效应、每次跨界（进入/创建/接入）都是 K 盖章的 Message，在 H 上。没有带外。 | `append` 是唯一写路径；文法无 sender 字段 |
| **描述闭包** | 每个非外生成员的描述（kind + program）在 H 上；K 自己的源码在 H₀ 上 | 折叠规则；genesis |
| **生成闭包** | 从 H 上的描述能得到成员与新带子（Decl → 成员，recipe → 新带子）；milieu 里有构造者零件（L） | 折叠规则 + Apply 表 |

两条作者法律：**公平性**（enabled 者不被永远跳过）；**L 之外的确定性**（除 Apply 外全是确定函数；判决由 U/K 做）。

外生成员（human）= 有接入记录、无 program 的成员。封闭到此为止。

## 1. 一条原则定结构

**H 是唯一状态；内存里的一切都是 fold(H) 的缓存。**
- 一个 Dalek Core = 一个目录：`h/c0.jsonl, h/c1.jsonl, …`，每行一条 Message。
- actor 表、channel 表、游标、全局步数——全部从 H 折叠出来，不单独持久化。
- K 自己的每一步写进 H（sender = `K`），所以 replay 不依赖 H 之外的任何东西。

## 2. 数据结构

```python
@dataclass(frozen=True)
class Msg:
    ch: str      # "c0", "c1", …          K 写
    seq: int     # 该带子内从 1 递增        K 写
    sender: str  # actor id | "door" | "K" K 写
    to: str      # actor id | "*" | ""     actor 给（K 记录用 ""）
    body: str    # 任意文本                actor 给

@dataclass
class Actor:
    id: str        # "<ch>/<seq>"，即宣告它的那条 Message 的地址。天然唯一、天然 K 盖章
    ch: str
    kind: str      # "agent" | "tool" | "human"
    program: str   # agent: L 的前缀文本；tool: python 源码；human: ""（无构造描述）
    cursor: int    # 已见到的最大 seq —— 由 K 记录折叠得出

@dataclass
class Channel:
    id: str
    msgs: list[Msg]
    actors: dict[str, Actor]   # 折叠顺序即唤醒顺序

@dataclass
class Space:
    dir: Path
    channels: dict[str, Channel]   # 创建顺序
    nsteps: int                    # 全局步数，由 K 记录折叠得出
```

## 3. 折叠规则（K 的"读法"——创建就在这里）

按带子创建顺序、带子内按 seq，逐条折叠。body 第一行若是指令行则有结构意义，否则是普通消息。

| 触发 | 谁写 | 折叠效果 |
|---|---|---|
| 带子第一条：`#genesis` + 可选 `K=<源码>` / `parent=<msg id>` | door | 新建 Channel。c0 携带 K 源码（T1）；子带子携带 parent 指针（谱系） |
| `#admit human` | door | 新成员，id = 本条地址，kind human，program ""。**外生成员的接入记录** |
| `#decl agent` / `#decl tool` + 其后全部行 = program | 任意成员，to = `*` | 新成员，id = 本条地址。**创建 = 描述** |
| `#recipe` + 每行一个已存在的成员 id | 任意成员，to = `*` | 新带子 c{n}：door 写 `#genesis parent=<本条地址>`，再把每个 id 的 Decl **逐字复印**为 door 写的 `#decl` 消息（B：复印非理解）。回执：door 在本带子写 `#created c{n}` to 发起者 |
| `#step actor=<id> upto=<seq> n=<全局步数>` + 其后全部行 = out | K | actor.cursor = upto；nsteps = n。replay 用 |
| 其他 | 任意 | 普通消息，按 to 可见 |

door 只写三种东西：genesis、admit、复印。**它不是成员，不接受消息，只在跨界处出现**：带子诞生、外来者接入、描述过界复印。

id 规则：成员 id 就是宣告它的消息地址 `ch/seq`。不需要分配器，不可能伪造（seq 是 K 的）。

## 4. K 主循环

```python
def run(space, apply, max_steps=None):
    while max_steps is None or space.nsteps < max_steps:
        a = wake(space)
        if a is None:
            idle(space); continue                 # 无人 enabled：等 stdin 或退出
        view = view_of(space, a)
        out  = apply[a.kind](a, render(view))     # 唯一的非确定点
        record(space, a, upto=last_seq(view, a), out=out)   # sender=K，先记后发
        for to, body in parse(out):
            if to == "*" or to in space.channels[a.ch].actors:
                append(space, Msg(a.ch, next_seq, a.id, to, body))   # 折叠规则在 append 内即时生效
            # 否则丢弃：locality
```

- `wake`：全局 round-robin 游标（从 nsteps 推出：`nsteps % len(all_actors)` 起找第一个 enabled）。`enabled(a)` ⇔ 存在 `seq > a.cursor` 且 `to ∈ {a.id, "*"}` 且 `sender ≠ K` 的 Msg。human 永远 enabled，但 apply 非阻塞，无输入返回 ""（空 out 也记一步）。
- `view_of`：`[m for m in ch.msgs if m.seq > a.cursor and m.to in (a.id, "*") and m.sender != "K"]`。含自己发的消息。v1 不截断（窗口问题留给失败记录）。
- `render`：每条一行 `[{ch}/{seq}] {sender} → {to}: {body}`；多行 body 缩进。
- `record` 先于 `append`：一步的"意图"先落带，再落效果，重放时才能逐字对齐。
- `append` 是唯一写文件的函数，写完立即对该条做折叠（所以 `#decl` 一落带成员就存在，`#recipe` 一落带子带子就诞生）。

## 5. Emit 文法

```
>>> <to>
<body 行…>
>>> <to>
<body 行…>
```

- `>>> ` 开头的行是头；第一个头之前的文本丢弃（L 的自言自语不入带）。
- to ∈ {成员 id, `*`}。没有 `door`——门不收信。
- body 的结构由第一行指令决定（§3）。program 是裸文本，不需要转义——这是不用 JSON 的原因。

例：一个 agent 造一个 tool 并把自己和它一起复印进新带子：
```
>>> *
#decl tool
import sys; v = sys.stdin.read()
if "fib 30" in v: print(">>> *\n832040")
>>> *
#recipe
c0/3
c0/7
```
（c0/3 是它自己的 Decl 地址，c0/7 是刚才那条 `#decl tool` 的地址——它得等下一步从 view 里看到才知道。）

## 6. Apply 的三种实现（K 之外，经 `apply: dict[kind, Callable]` 注入）

- **agent**：`l.complete(a.program + "\n\n" + rendered_view)`。program 就是这个 agent 的 harness（系统提示 + 文法说明），在 H 上，是数据。`l.py` 只做 `POST {prompt, max_tokens, temperature}` 到 `DALEK_L_URL`；chat 接口用一个 shim，注明是工程近似。
- **tool**：`subprocess.run([sys.executable, "-c", a.program], input=view, cwd=scratch/<a.id>, capture_output=True, timeout=T_HOST)` → stdout。每成员一个草稿目录（草稿不共享）。
- **human**：`select([stdin],[],[],0)`，有则读一行，否则 `""`。
- **录音带（tape）**：`lambda a, v: next(outs)`——测试与 replay 用同一个 `run`。

E2 的 spawner tool 用 `subprocess.Popen` 起新进程——允许，milieu = Linux。

## 7. replay

```python
def replay(src_dir) -> Report:
    steps = fold_steps(src_dir)            # 所有 #step 记录，按 n 排序
    dst = fresh_space(tmpdir, genesis_from(src_dir))   # 同一份 K 源码、同一条 genesis
    it = iter(steps)
    def tape(a, v):
        s = next(it)
        if s.actor != a.id: raise Divergence(n=s.n, expected=s.actor, got=a.id)
        return s.out
    run(dst, {k: tape for k in KINDS}, max_steps=len(steps))
    return diff_dirs(src_dir, dst.dir)     # 逐字节比较每条带子
```

发散的两种形式都被抓：wake 选了不同的人（法律变了 / 有带外状态），或落带内容不同（效应不由记录决定）。这是 T2，也是公开带子的验证命令。

## 8. 模块与预算

```
dalek-core/kernel.py    Msg/Actor/Channel/Space、fold 规则、append、wake/view/render、run、replay   ≤ 450
dalek-core/grammar.py   parse(out) / 指令行识别                                                    ≤ 60
dalek-core/l.py         completion 客户端 + chat shim                                               ≤ 60
dalek-core/u.py         子进程 runner                                                               ≤ 50
dalek-core/human.py     stdin 非阻塞                                                                ≤ 25
dalek-core/cli.py       init / run / replay / fold / t                                              ≤ 100
t_dalek-core/           T1–T7（pytest，录音带 apply，秒级、确定）                                     ≤ 400
experiments/pi/    E0：最强形态 pi（loop + file + bash + git），不 import Dalek Core                  ≤ 200
experiments/e1.py  E1 驱动 + 验证器（四个条件）                                                  ≤ 150
experiments/e2.py  E2 spawner tool 的 program + 父 Dalek Core 对子目录跑 t                            ≤ 100
FAILURES.md        每条法律的出生证明（记录纪律；不是建造门槛）
```

CLI：
- `dalek init DIR`：写 `h/c0.jsonl` 第一条 `#genesis K=<kernel.py+grammar.py 源码>`。
- `dalek run DIR [--human] [--l URL] [--steps N]`：`--human` 先 append `#admit human`。
- `dalek replay DIR`：§7，退出码 0/1，打印首个发散点。
- `dalek fold DIR`：打印折叠出的成员表与带子表（调试）。
- `dalek t DIR`：对一个已存在的目录跑 T1–T7（E2 里父 Dalek Core 用它验子 Dalek Core）。

## 9. T_coral（与 §0 对应）

| 测试 | 做法 | 守的是 |
|---|---|---|
| T1 | c0 第一条含 K 源码，`exec` 它得到可用的 `run` | 描述闭包（K 自身） |
| T2 | 跑一段录音带 → replay → 逐字节相同；篡改任一行后 replay 报发散 | 因果闭包 + 确定性 |
| T3 | out 里试图伪造 sender/seq：文法无此字段，不可表达；直接改文件 → fold 报 seq 不连续 | 因果闭包 |
| T4 | 两个 tool 各写自己的 scratch，互相读不到；同带子内只经 Msg 可见 | 因果闭包 |
| T5 | actor 发 `>>> c1/2`（别的带子的成员）→ 丢弃；无任何 API 能不经 `#recipe` 得到新带子 | 因果闭包（locality） |
| T6 | 3 个成员，其中一个只偶尔 enabled，验证 N 轮内被 wake | 公平性 |
| T7 | 每个非 human 成员都能在 H 上找到自己的 `#decl`（及 `#recipe` 复印来源）；human 有 `#admit` 无 program | 描述闭包 + 外生边界 |

## 10. 实验

**E0（对照，pi 形态）**：L + loop + 文件 + bash + git，最强 prompt。同一任务："在无人介入下产出通过测试的下一代"。裂开处记 FAILURES.md。

**E1（channel 造 channel）**：c0 里一个构造者 agent（program = 角色 + 文法说明 + 目标测试的描述）+ 真实 L。目标：它 `#decl` 一个 tool，再 `#recipe` 把自己和该 tool 复印进 c1；c1 里的 tool 通过 gen1 不通过的测试。验证器查四条：
1. V 通过，且 T 含 gen1 不通过的测试；
2. 产生该 `#decl` 的 `#step` 的 upto 早于该 Decl 的 seq（view 不含它——构造非复制）；
3. c1 的配方含构造者（可遗传）；
4. c1 → c2 重复一次。
**gen1 → gen2 之间 H 上没有 human 消息**——验证器直接查带子。

**E2（Dalek Core 造 Dalek Core'）**：c0 里一个 spawner tool：读 c0 第一条取 K 源码，写到 `DIR2/`，`Popen([python, DIR2/kernel.py, "init+run", …])`；父 Dalek Core 再以 tool 跑 `dalek t DIR2`，结果作为 Msg 落回 c0。通过即一次自我制造，K diff = 0。**这盘带子就是公开交付物。**

## 11. 建造顺序（每步有验收）

| 步 | 交付 | 验收 |
|---|---|---|
| 1 | `kernel.py` `grammar.py` + 录音带 apply | T1 T2 T3 T5 T6 T7 绿 |
| 2 | `u.py` `human.py` `cli.py`；一个 echo tool | T4 绿；`dalek run --human` 能和 echo 对话；`dalek replay` 通过 |
| 3 | `l.py` + 构造者 agent 的 program | 手动跑通一次 `#decl` + `#recipe` |
| 4 | `experiments/e1.py` | 四条件通过；带子里无 human 消息 |
| 5 | `experiments/e2.py` + `dalek t` | 子 Dalek Core 通过 T1–T7；带子公开 |
| 6 | `experiments/pi/` + FAILURES.md | 对照记录 |

第 1 步先于一切：**K 在没有 L 的情况下就必须能被完整测试**。

## 12. 对照 atoll（只取内核形状）

atoll `runtime + platform + protocol` 约 39k 行 Go；Dalek Core K ≤ 800。差的全是 ③ 的自由项。
- 信封：atoll `id, ts, channel_id, sender{kind,id}, kind, type, payload, parent_id, correlation_id, visibility, audience, expires_at`，seq 为存储派生列。Dalek Core 只留 K 盖的三个章（ch, seq, sender）+ to + body；**无 ts、无 uuid**——墙钟与随机源是外生输入，进 K 即破 replay。
- 写入链：atoll `harness/` 8 步（shape → caller_auth → sender_consistent → kind_audience → type_registered → receiver_gate → response_pairing → normalize）在 Dalek Core 坍缩为 `parse` + 盖章 + locality。atoll 里叫 harness 的东西就是 K 的 Emit，与文章里"harness 在 H 里"是撞名。
- 门：atoll `systemkernel` 把门做成特殊 actor 并有词表；Dalek Core 的 door 不是成员、不收信、无词，只在跨界处写三种记录。
- 时间轴：atoll 有 `schedule`；Dalek Core 没有。若某次失败要求 K 有时间，记 FAILURES.md 作候选。

## 13. 未决（由代码回答，答案回写本文件）

1. view 是否需要截断；截断规则是否必须进 K。
2. `#recipe` 引用 id 的方式够不够——构造者要等一步才知道刚宣告的 Decl 地址。
3. round-robin + "human 永远 enabled" 是否足够公平且不空转。
4. E1 里 L 能否稳定遵守文法；不能则 harness（program）如何写，仍须在 H 上。
