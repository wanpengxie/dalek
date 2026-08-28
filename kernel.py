"""Dalek Core — v5（整合版模型）。

先验（相对于 milieu Ω）：
  H   账本：被动的追加介质，一台 channel 一条（h/<name>.jsonl）。死的。
  R   runtime：读消息、成 view、调成员、落带盖章、投递、恢复。白盒，就是下面这几条；由 Ω 的 U 执行。
  D⊂H 账本里能被解释为机器描述的行（#decl / #peer / #in）。不是每一行都是。

channel = (H, R, bindings, boundary)。活的单元。账本自己不跑。
零件：Author（L、人：产生候选描述）；U（执行/验证描述，确定）；D（c0 里的构造子系统）；F（普通成员/普通 channel）。

D = A + B + C，是 c0 的一个成员（kind "D"）：
  A  建空 channel、构造成员、绑定地址          B  复制 Genome（部件描述），不是整盘账本
  C  控制过程：构造 → 复制 → 装进子代 → 接 peer → 启动
D 对目标 channel 做的每一步都以 door 的章落在目标账本上；D 自己这一步以 #step 落在 c0 账本上。

膜内地址不带 channel 名（"17"）；channel 名属于门（peer）。Genome 逐字复制，无需重绑定。
词（行首）：#genesis #admit #decl #peer #in 只有 door 能说；#step 只有 R 能说；成员说的一切都是文本。
"""
from __future__ import annotations
import json, shutil, subprocess, sys, tempfile, filecmp
from dataclasses import dataclass, field
from pathlib import Path
from typing import Callable

# ----------------------------------------------------------------- 对象

@dataclass(frozen=True)
class Msg:
    seq: int              # 带内序号（R 写）
    sender: str           # 地址 | "door" | "R"（R 写）
    to: str               # 地址 | ""
    body: str

@dataclass
class Member:
    addr: str
    kind: str             # L | U | X | D | P(peer 门)
    prefix: str = ""      # L: prompt；U: 源码；P: 对方 channel 名
    cursor: int = 0
    home: str = ""        # 所属 channel 名（只为零件实现与录音带用；膜内地址不含它）

@dataclass
class Channel:
    name: str
    msgs: list[Msg] = field(default_factory=list)
    book: dict[str, Member] = field(default_factory=dict)   # 插入序
    receptionist: str | None = None                          # #in：门把外来消息交给谁

@dataclass
class Space:
    dir: Path
    channels: dict[str, Channel] = field(default_factory=dict)
    def path(self, name: str) -> Path: return self.dir / "h" / f"{name}.jsonl"

Apply = Callable[[Member, str], str]     # L / U / X：只见 (自己, view)

DOOR_WORDS = {"genesis", "admit", "decl", "peer", "in"}

# ----------------------------------------------------------------- 文法

def parse(out: str) -> list[tuple[str, str]]:
    """'>>> addr' 头 + 正文；第一个头之前的文字丢弃。"""
    res, to, buf = [], None, []
    for line in out.splitlines():
        if line.startswith(">>> "):
            if to is not None: res.append((to, "\n".join(buf).rstrip("\n")))
            t = line[4:].split(); to, buf = (t[0] if t else ""), []
        elif to is not None:
            buf.append(line)
    if to is not None: res.append((to, "\n".join(buf).rstrip("\n")))
    return [(t, b) for t, b in res if t]

def directive(body: str) -> tuple[str, list[str], str]:
    head, _, rest = body.partition("\n")
    if head.startswith("#"):
        p = head[1:].split(); return (p[0] if p else ""), p[1:], rest
    return "", [], body

def word_of(m: Msg) -> tuple[str, list[str], str]:
    """作者约束：door 说膜的词；R 说 step；成员说的一切只是文本。"""
    w, args, rest = directive(m.body)
    if (m.sender == "door" and w in DOOR_WORDS) or (m.sender == "R" and w == "step"):
        return w, args, rest
    return "", [], m.body

def render(view: list[Msg]) -> str:
    return "\n".join(f"[{m.seq}] {m.sender} -> {m.to}: " + m.body.replace("\n", "\n    ") for m in view)

# ----------------------------------------------------------------- H：append 只记录；fold 只改内存

def append(sp: Space, name: str, sender: str, to: str, body: str) -> Msg:
    c = sp.channels.get(name)
    m = Msg(seq=(c.msgs[-1].seq + 1) if (c and c.msgs) else 1, sender=sender, to=to, body=body)
    p = sp.path(name); p.parent.mkdir(parents=True, exist_ok=True)
    with open(p, "a", encoding="utf-8") as f:
        f.write(json.dumps(m.__dict__, ensure_ascii=False) + "\n")
    _fold(sp, name, m)
    return m

def _fold(sp: Space, name: str, m: Msg) -> None:
    w, args, rest = word_of(m)
    if w == "genesis":
        sp.channels[name] = Channel(name=name)
    c = sp.channels[name]
    c.msgs.append(m)
    a = str(m.seq)
    if w == "admit":   c.book[a] = Member(addr=a, kind="X", home=name)
    elif w == "decl":  c.book[a] = Member(addr=a, kind=args[0], prefix=rest, home=name)
    elif w == "peer":  c.book[a] = Member(addr=a, kind="P", prefix=args[0], home=name)
    elif w == "in":    c.receptionist = args[0]
    elif w == "step":
        kv = dict(x.split("=", 1) for x in args)
        c.book[kv["actor"]].cursor = int(kv["upto"])

def load(dir: Path) -> Space:
    sp = Space(dir=dir)
    hdir = dir / "h"
    if hdir.exists():
        names = sorted((p.stem for p in hdir.glob("*.jsonl")), key=lambda n: (len(n), n))
        # 按 genesis 顺序恢复：先读每条带子第一行，按其 order 字段排序
        order = {}
        for n in names:
            first = json.loads(sp.path(n).read_text(encoding="utf-8").splitlines()[0])
            order[n] = int(dict(x.split("=", 1) for x in directive(first["body"])[1] if "=" in x).get("order", 0))
        for n in sorted(names, key=lambda n: order[n]):
            for line in sp.path(n).read_text(encoding="utf-8").splitlines():
                _fold(sp, n, Msg(**json.loads(line)))
    return sp

# ----------------------------------------------------------------- 构造子系统 D（c0 的成员；A + B + C）
#
# 一个 Author 给 D 发一条请求（文本），D 解释并执行：
#   build <name>            A：建空 channel（genesis）
#   part <addr>             B：把本 channel 里 <addr> 的描述逐字复制进去（可多行）
#   decl <L|U> <text…>      A：用新描述构造一个成员（单行文本；多行用 part）
#   in <ref>                C：接待员。<ref> 是 part/decl 的序号 (#1 #2 …) 或已知地址
#   peer <channel>          C：与已有 channel 双向接 peer
#   start <text…>           C：启动：以对方门的名义把 <text> 交给接待员
#   attach <channel> …      对已有 channel 做 part/decl/in/peer（组织）
# 每一步都以 door 的章落在目标账本；D 把回执发给请求者。

def D(sp: Space, me: Member, home: str, view_msgs: list[Msg]) -> str:
    out = []
    for m in view_msgs:
        lines = m.body.splitlines()
        if not lines: continue
        head = lines[0].split()
        if head[0] == "build" and len(head) > 1:
            target = head[1]
            order = len(sp.channels)
            append(sp, target, "door", "", f"#genesis name={target} by={home}/{me.addr} order={order}")
            here = sp.channels[home].book.get(m.sender)
            origin = here.prefix if (here and here.kind == "P") else home
            out.append(f">>> {m.sender}\n" + _construct(sp, origin, target, lines[1:], home))
        elif head[0] == "attach" and len(head) > 1:
            here = sp.channels[home].book.get(m.sender)
            origin = here.prefix if (here and here.kind == "P") else home       # 请求者所在的 channel
            target = origin if head[1] == "here" else head[1]
            if target in sp.channels:
                out.append(f">>> {m.sender}\n" + _construct(sp, origin, target, lines[1:], home))
            else:
                out.append(f">>> {m.sender}\n? no such channel {target}")
        # 不是请求的消息：沉默。构造器不闲聊。
    return "\n".join(out)

KEYS = ("part", "decl", "in", "peer", "start")

def _construct(sp: Space, home: str, target: str, spec: list[str], dhome: str = "c0") -> str:
    """home：part 的来源（请求者所在 channel）；dhome：D 所在 channel（启动消息以它的门的名义发出）。"""
    src, made, report, start = sp.channels[home], [], [], None
    i = 0
    while i < len(spec):
        t = spec[i].split(); i += 1
        if not t: continue
        if t[0] == "part" and len(t) > 1 and t[1] in src.book and src.book[t[1]].kind in ("L", "U"):
            p = src.book[t[1]]
            m = append(sp, target, "door", "", f"#decl {p.kind}\n{p.prefix}")      # B：逐字复制
            made.append(str(m.seq)); report.append(f"part {t[1]} -> {target}/{m.seq}")
        elif t[0] == "decl" and len(t) > 1 and t[1] in ("L", "U"):
            body = [" ".join(t[2:])] if len(t) > 2 else []
            while i < len(spec) and (spec[i].split() or [""])[0] not in KEYS:
                body.append(spec[i]); i += 1
            m = append(sp, target, "door", "", f"#decl {t[1]}\n" + "\n".join(body))   # A：新描述构造
            made.append(str(m.seq)); report.append(f"decl -> {target}/{m.seq}")
        elif t[0] == "in" and len(t) > 1:
            ref = made[int(t[1][1:]) - 1] if t[1].startswith("#") else t[1]
            append(sp, target, "door", "", f"#in {ref}"); report.append(f"in {ref}")
        elif t[0] == "peer" and len(t) > 1 and t[1] in sp.channels:
            a = append(sp, target, "door", "", f"#peer {t[1]}")                    # C：双向接线
            b = append(sp, t[1], "door", "", f"#peer {target}")
            report.append(f"peer {target}/{a.seq} <-> {t[1]}/{b.seq}")
        elif t[0] == "start":
            start = " ".join(t[1:]) + ("\n" + "\n".join(spec[i:]) if i < len(spec) else ""); break
        else:
            report.append(f"skip: {' '.join(t)}")
    c = sp.channels[target]
    if start is not None and c.receptionist:                                        # C：启动
        gate = next((x.addr for x in c.book.values() if x.kind == "P" and x.prefix == dhome), "door")
        append(sp, target, gate, c.receptionist, start); report.append(f"start -> {target}/{c.receptionist}")
    return "\n".join(report) or "nothing"

# ----------------------------------------------------------------- R：一台 channel 的运行

def view_of(c: Channel, a: Member) -> list[Msg]:
    return [m for m in c.msgs if m.seq > a.cursor and m.to == a.addr and m.sender != "R"]

def step(sp: Space, name: str, a: Member, apply: dict[str, Apply]) -> str:
    c = sp.channels[name]
    view = view_of(c, a)
    out = D(sp, a, name, view) if a.kind == "D" else apply[a.kind](a, render(view))    # 唯一非确定点在 apply
    append(sp, name, "R", "", f"#step actor={a.addr} upto={view[-1].seq if view else a.cursor}\n{out}")
    for to, body in parse(out):
        if to in c.book: append(sp, name, a.addr, to, body)                            # 只能寻址本膜
    return out

def deliver(sp: Space, name: str, m: Msg) -> bool:
    """R 的投递：发给 peer 门的消息，door 抄进对方账本，交给对方接待员。返回是否有成员被点名。"""
    c = sp.channels[name]
    a = c.book.get(m.to)
    if not a or a.cursor >= m.seq: return False
    if a.kind == "P":
        other = sp.channels[a.prefix]
        gate = next((x.addr for x in other.book.values() if x.kind == "P" and x.prefix == name), "door")
        if other.receptionist:
            append(sp, other.name, gate, other.receptionist, m.body)
        a.cursor = m.seq                                                                # 门不 step；记已投递
        return False
    return True

def run_channel(sp: Space, name: str, apply: dict[str, Apply], pos: dict[str, int], budget: list[int]) -> bool:
    """沿本 channel 账本走到头。返回本轮是否有人被 step。"""
    c = sp.channels[name]; acted = False
    while pos.get(name, 0) < len(c.msgs) and budget[0] > 0:
        m = c.msgs[pos.get(name, 0)]; pos[name] = pos.get(name, 0) + 1
        if deliver(sp, name, m):
            step(sp, name, c.book[m.to], apply); acted = True; budget[0] -= 1
    return acted

def run(sp: Space, apply: dict[str, Apply], max_steps: int = 2000) -> None:
    """宿主（Ω）：轮流让每台 channel 自己跑到静止；全部静止时轮询外生者一轮；无人开口即停。
    max_steps 是宿主的预算（Ω 的事），不是机器的语义。"""
    pos: dict[str, int] = {}
    budget = [max_steps]
    while budget[0] > 0:
        while any(run_channel(sp, n, apply, pos, budget) for n in list(sp.channels)): pass
        spoke = False
        for n in list(sp.channels):
            for a in list(sp.channels[n].book.values()):
                if a.kind == "X":
                    spoke |= bool(step(sp, n, a, apply).strip()); budget[0] -= 1
        if not spoke: return

# ----------------------------------------------------------------- 零件的实现（Ω）

def U(a: Member, view: str) -> str:
    scratch = Path(tempfile.mkdtemp(prefix="dalek-u-"))
    try:
        r = subprocess.run([sys.executable, "-c", a.prefix], input=view, capture_output=True, text=True, cwd=scratch, timeout=60)
        return r.stdout
    finally:
        shutil.rmtree(scratch, ignore_errors=True)

def tape(outs: list[tuple[str, str]], live: dict[str, Apply] | None = None, by_addr: bool = False) -> Apply:
    """录音带：(期望 "<channel>/<addr>", out) 序列。live 里的种类重算。
    by_addr=True：每个地址一条队列（replay 用，不依赖跨带子的全局顺序）。"""
    live = live or {}
    if by_addr:
        q: dict[str, list[str]] = {}
        for who, out in outs: q.setdefault(who, []).append(out)
        def g(a: Member, view: str) -> str:
            if a.kind in live: return live[a.kind](a, view)
            key = f"{a.home}/{a.addr}"
            if not q.get(key):
                if a.kind == "X": return ""
                raise RuntimeError(f"divergence: no record for {key}")
            return q[key].pop(0)
        return g
    it = iter(outs)
    def f(a: Member, view: str) -> str:
        who, out = next(it, (None, None))
        if who is None:
            if a.kind in live: return live[a.kind](a, view)
            if a.kind == "X": return ""
            raise RuntimeError(f"tape exhausted at {a.addr}")
        if who.split("/")[-1] != a.addr: raise RuntimeError(f"divergence: expected {who}, got {a.addr}")
        return live[a.kind](a, view) if a.kind in live else out
    return f

# ----------------------------------------------------------------- replay：同一段 run；Author/X 照抄记录，U/D 重算

def genesis(dir: Path, name: str = "c0") -> Space:
    sp = Space(dir=dir); shutil.rmtree(dir / "h", ignore_errors=True)
    append(sp, name, "door", "", f"#genesis name={name} by=omega order=0")
    return sp

def replay(src: Path) -> bool:
    sp = load(src)
    recs = []
    for n in sp.channels:                                   # 记录按各带子顺序；派发顺序由 run 决定，故只按地址核对
        for m in sp.channels[n].msgs:
            if m.sender == "R":
                kv = dict(x.split("=", 1) for x in directive(m.body)[1])
                recs.append((f"{n}/{kv['actor']}", directive(m.body)[2]))
    dst = Path(tempfile.mkdtemp(prefix="dalek-replay-"))
    sp2 = Space(dir=dst)
    c0 = next(iter(sp.channels))
    for m in sp.channels[c0].msgs:                          # 首步之前 Ω 放进 c0 的门事实照抄
        if m.sender == "R": break
        if m.sender == "door": append(sp2, c0, "door", m.to, m.body)
    t = tape(recs, live={"U": U}, by_addr=True)
    run(sp2, {"L": t, "X": t, "U": t})
    return all(filecmp.cmp(sp.path(n), sp2.path(n), shallow=False) for n in sp.channels)

# ----------------------------------------------------------------- CLI

def show(sp: Space) -> str:
    lines = []
    for n, c in sp.channels.items():
        for m in c.msgs:
            b = (m.body.splitlines() or [""])[0]
            lines.append(f"[{n}/{m.seq:<3}] {m.sender:6} -> {m.to or '-':4}: {b[:78]}")
    return "\n".join(lines)

def main(argv: list[str]) -> None:
    cmd, d = argv[1], Path(argv[2])
    if cmd == "show": print(show(load(d)))
    elif cmd == "book":
        for n, c in load(d).channels.items():
            print(f"{n} in={c.receptionist}")
            for a in c.book.values(): print(f"  {a.addr:4} {a.kind} cursor={a.cursor} {a.prefix[:40]!r}")
    elif cmd == "replay":
        ok = replay(d); print("replay:", "identical" if ok else "DIVERGED"); sys.exit(0 if ok else 1)

if __name__ == "__main__":
    main(sys.argv)
