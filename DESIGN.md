# coral — 代码设计 v0（2026-08-27）

对应 SPEC v1。目标：K 本体 ≤ 1k 行，一个进程跑一个 coral（一个 space：c0, c1, …），单线程、单显式循环。

## 1. 一条原则决定全部结构

**H 是唯一状态；内存里的一切都是 fold(H) 的缓存。**
- 每 channel 一个 append-only 文件 `h/<ch>.jsonl`，每行一条 Message。
- actor 表、channel 表、每个 actor 的游标（"上次步到哪"）都不单独存储，启动时从 H 折叠出来。
- K 自己的每一步也写进 H（作为 sender=`K` 的 Message），所以 replay 不需要任何 H 之外的东西。

H 里只有一种东西——Message——但 sender 分四类，靠 sender 区分而不靠 schema：

| sender | 含义 | body |
|---|---|---|
| actor id | 成员发言 | 任意文本 |
| `door` | 创建事件 / 门的回执 | JSON：Decl、channel 回执、K 源码 |
| `K` | 一步的记录（replay 用） | JSON：`{actor, upto, out}` |
| human id | 外生成员发言 | 文本；其 Decl 不在 H |

## 2. 模块

```
coral/
  kernel.py     K：Msg、H、fold、Wake/View/Emit/Append、door、replay      ~450 行
  grammar.py    Emit 文法：parse(out) -> [(to, body)]；render(view) -> text   ~60
  l.py          L：complete(text) -> text，裸 HTTP                            ~50
  u.py          U：run(program, text) -> text，子进程 + 每 actor 草稿目录       ~60
  human.py      stdin 非阻塞读                                                ~30
  cli.py        init / run / replay / t                                      ~80
t_coral/        T1–T7（pytest）                                               ~400
experiments/
  pi/           E0：最强形态的 pi agent（loop + file + bash + git）             ~200
  e1_channel.py E1                                                           ~150
  e2_coral.py   E2                                                           ~100
```

`kernel.py` 只 import 标准库和 `grammar`。L、U、human 通过一个 `apply: Callable[[Actor, str], str]` 表注入——K 不知道 L 是 HTTP 还是录音带，这正是 replay 的实现方式。

## 3. 数据结构

```python
@dataclass(frozen=True)
class Msg:
    ch: str      # channel id，K 写
    seq: int     # 该 channel 内单调递增，K 写
    sender: str  # actor id | "door" | "K"，K 写
    to: str      # actor id | "*" | "door" | ""（K 记录用空）
    body: str

@dataclass
class Actor:
    id: str      # "<ch>/<n>"，如 "c0/3"
    ch: str
    kind: str    # "agent" | "tool" | "human"
    program: str # agent: L 的前缀文本（harness 在 H 里）；tool: python 源码；human: ""
    cursor: int  # 已见到的最大 seq —— 从 K 记录折叠得到，不单独存

@dataclass
class Channel:
    id: str
    msgs: list[Msg]        # fold 的缓存；权威在文件
    actors: dict[str, Actor]
```

Decl（door 发出的 Message 的 body）：`{"decl": {"id": "c1/2", "kind": "tool", "program": "..."}}`。
human 在 `run --human` 时挂上，**不写 Decl**；它的发言与 K 记录照常入 H。这就是 T7 里"外生成员对第二问无答案"的实现。

## 4. K 主循环

```python
def run(space: Space, apply: dict[str, Callable[[Actor, str], str]]) -> None:
    while True:
        a = wake(space)                        # P3
        if a is None: idle(); continue
        view = view_of(space, a)               # 机械投影
        out  = apply[a.kind](a, render(view))  # L / U / stdin —— 唯一的非确定点
        record(space, a, upto=view[-1].seq if view else a.cursor, out=out)   # sender=K
        for to, body in parse(out):            # P4：确定文法
            if to == "door":
                door(space, a, body)           # 唯一效应词的入口，结果也 append
            elif to == "*" or to in space[a.ch].actors:
                append(space, Msg(a.ch, next_seq(a.ch), a.id, to, body))     # P1
            # 其它：越界 → 丢弃（locality）
```

- `wake`：对所有 channel 的所有 actor 做一个全局 round-robin 游标；返回下一个 `enabled` 的。`enabled(a) ⇔ 存在 seq > a.cursor 且 to ∈ {a.id, "*"} 的 Msg`。human 永远 enabled（外生者何时说话不可知），但其 apply 是非阻塞读，无输入即返回 ""。
- `view_of`：`[m for m in ch.msgs if m.seq > a.cursor and m.to in (a.id, "*") and m.sender != "K"]`。不含 K 记录，不含发给别人的私信。**是否含自己发的消息、窗口 n 怎么截断**——SPEC §10.1，先不截断。
- `record`：把这一步写成 `Msg(ch, seq, "K", "", json{actor, upto, out})`。cursor 的更新就是这条记录：fold 时对每个 actor 取最后一条 K 记录的 upto。
- `append` 是**唯一**写文件的函数；`door` 和主循环都只经它。

## 5. door

```python
def door(space, a, body):
    req = json.loads(body)
    if req["word"] == "member.create":
        actor = new_actor(a.ch, req["decl"])            # id = f"{ch}/{n}"
        append(space, Msg(a.ch, seq, "door", "*", json{"decl": actor.decl}))
    elif req["word"] == "channel.create":
        ch = new_channel(space)                          # id = f"c{n}"，新文件
        for decl in req["recipe"]:
            append(space, Msg(ch, seq, "door", "*", json{"decl": ...}))
        append(space, Msg(a.ch, seq, "door", a.id, json{"created": ch}))
    else:
        append(space, Msg(a.ch, seq, "door", a.id, json{"error": "unknown word"}))
```

只有这两个分支。`channel.create` 的回执发给请求者——这是 A∘B 的"构造"回到"复制"一侧的那条线。
genesis：`cli init` 建 `h/c0.jsonl`，第一条是 `door → *`，body = `{"K": <kernel.py + grammar.py 源码>}`（T1）。

## 6. Apply 的三种实现（都在 K 之外）

- **agent**：`l.complete(a.program + "\n\n" + rendered_view)`。program 就是这个 agent 的 harness（系统提示、输出文法说明）——它在 H 里，是数据。L 本身只是 `POST /completion`。
- **tool**：`u.run(a.program, rendered_view)`：`subprocess.run([python, "-c", program], input=view, cwd=scratch/<actor id>, capture_output=True, timeout=…)`，返回 stdout。每 actor 一个草稿目录（P1 的"草稿不共享"）。程序自己按 §7 文法打印输出。
- **human**：`select([stdin], [], [], 0)`，有则读一行，否则 `""`。

E2 的 tool 需要 `subprocess` 起新进程——允许，milieu = Linux。

## 7. Emit 文法（候选，SPEC §10.2）

```
>>> <to>
<body …>
>>> <to>
<body …>
```

- 以 `>>> ` 开头的行是头，其后到下一头之前是 body。第一个头之前的文本丢弃（L 的自言自语不入 H）。
- `to` 是 actor id、`*` 或 `door`。发给 door 的 body 必须是 JSON。
- 之所以不用 JSON 整体输出：让 L 写代码（body 是 python 源码）时不用转义。

## 8. replay

```python
def replay(hdir) -> bool:
    recorded = fold_records(hdir)          # 按 (ch, seq) 顺序的 K 记录
    space2 = Space(tmpdir); genesis(space2, same K source)
    outs = iter(recorded)
    apply_rec = {k: (lambda a, v: next(outs).out) for k in ("agent","tool","human")}
    run(space2, apply_rec, steps=len(recorded))
    return files_equal(hdir, space2.dir)
```

同一个 `run`，只换 `apply`。任何带外效应、任何 L 之外的随机性都会让两组文件不逐字相等——这就是 T2 作为漏检器的实现。要求 `wake` 完全确定（round-robin 游标本身也从 K 记录折叠出来，或简单地：记录里带 actor id，replay 时按记录顺序 wake）。

## 9. T_coral 的挂法

pytest，每个测试起一个临时 space，用**录音带 apply**（预写好的 out 序列）而不是真的 L，这样 T 是确定的、秒级的。

| 测试 | 做法 |
|---|---|
| T1 | genesis 后 c0 第一条 Message 的 body 含 K 源码，且 `exec` 它能得到 `run` |
| T2 | 跑一段录音带 → replay → 文件逐字比较 |
| T3 | 构造一个 out 试图伪造 `sender` / `seq`（文法里根本没有这些字段 → 无法表达）；直接改文件后 fold 应报 seq 不连续 |
| T4 | 两个 tool 各写自己的 scratch，互相读不到（cwd 隔离）；同一 channel 只经 Msg 可见 |
| T5 | actor 发 `>>> c1/0`（别的 channel）→ 被丢弃；非 door 无法造 channel（没有 API） |
| T6 | 3 个 actor，其中一个只在很少的 view 下 enabled，验证 N 轮内被 wake |
| T7 | 对每个非 human actor：H 里有 Decl，且能找到造它的 door 记录；human 找不到 Decl |

## 10. 实验的挂法

- **E0**：`experiments/pi/` 是独立程序，不 import coral。要求它在无人介入下产出 pi'，通过 `tests/`。裂开的地方记 `FAILURES.md`。
- **E1**：c0 里一个 agent（program = "你是构造者…文法…"），一段真实 L。目标：经 door 造 c1，在 c1 里 `member.create` 一个 tool，该 tool 通过 gen1 不过的测试（例：在 view 里出现 `fib 30` 时回答正确数）。验证脚本检查三个条件：V 通过；生成该 Decl 的 K 记录的 `upto` 早于 Decl 的 seq；c1 内重复一次。
- **E2**：c0 里一个 tool，program 读 c0 第一条 Message 取 K 源码，写到临时目录，`subprocess.Popen` 起 `python -c <K> init+run`，然后对新目录跑 T1–T7，把结果作为 Msg 发回。

## 11. 行数预算与顺序

| 步 | 交付 | 预算 |
|---|---|---|
| 1 | `kernel.py` + `grammar.py` + 录音带 apply，T1–T7 绿 | ≤ 500 + 400 |
| 2 | `l.py` `u.py` `human.py` `cli.py`，手动跑一个 echo tool + 人 | ≤ 250 |
| 3 | E0（pi），跑、记失败 | ≤ 200 |
| 4 | E1 | ≤ 150 |
| 5 | E2 | ≤ 100 |

第 1 步先于一切：K 在没有 L 的情况下就必须能被完整测试。这本身就是 P4 的一个体现。

## 12. 对照 atoll（只看内核形状，工程细节不进来）

atoll 的 `runtime + platform + protocol` 非测试 Go 代码约 39k 行；coral 的 K 预算 1k。40× 的差是 ③ 的自由项，不是 K。对照三处：

**信封**（`protocol/message/envelope.go`）：`id, ts, ts_received, channel_id, sender{kind,id}, kind, type, payload, parent_id, correlation_id, visibility, audience, expires_at`；`seq` 是存储派生列，不在线上。coral 的 `Msg(ch, seq, sender, to, body)` 与之的关系：

| atoll 字段 | coral | 理由 |
|---|---|---|
| channel_id, sender, seq | ch, sender, seq（K 写） | 同：三个由 K 盖的章 |
| audience（列表）+ visibility | to（单个 / `*`） | audience 是 to 的集合推广；原型取最小 |
| kind / type / payload | body | request/response/word 的区分是 H 级约定，body 内可表达，不是 K |
| parent_id / correlation_id | 无 | body 引用 seq 即可，可从 H 推出 |
| ts / ts_received / expires_at | **无** | 墙钟是外生输入；进 K 会破坏 replay 的逐字相等。coral 里时间若需要，是一个外生成员（clock） |
| id（uuid） | (ch, seq) | 由 K 递增即唯一，无需随机源 |

**写入链**（`runtime/harness/`，8 步：envelope_shape → caller_auth → sender_consistent → kind_audience → type_registered → receiver_gate → response_pairing → normalize）——这就是 atoll 的 Emit/Validate/Append。在 coral 里坍缩为：

| atoll 步 | coral |
|---|---|
| envelope_shape | `grammar.parse` |
| caller_auth + sender_consistent | 不存在：文法里没有 sender 字段，伪造不可表达；K 直接盖章 |
| kind_audience + receiver_gate | `to ∈ members ∪ {*, door}`，否则丢弃（locality） |
| type_registered | door 的两个词 |
| response_pairing / normalize | 无 |

atoll 里叫 "harness" 的东西就是 K 的 Emit——和文章里"harness 在 H 里"是两个词撞名，注意分开。

**门与内核**：atoll 的 `systemkernel` 拥有恰好一个 SystemActor 单元，门是一个特殊 actor；coral 的 door 不是 actor，是 K 的一个函数。`receiver_gate` 依赖 Presence（接收者是否在线）——coral 无 presence：有 Decl 即在，活性归公平性管。

**时间轴**：atoll 有 `runtime/schedule`（timer 触发消息）。coral 没有。E1/E2 若被迫需要超时，那是 U 的 host 级 timeout（子进程），不是 K 的时间。若某次失败真的要求 K 有时间，记入 FAILURES.md 作为候选第五条。

由此定两条设计决定：
1. **K 内无墙钟、无随机源**。唯一的非确定点是 Apply。
2. **门不是成员**。door 词的处理在主循环里，与 Append 同一层。
