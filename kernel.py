"""Dalek Core — v6。

两个对象，分开：
  配置 Config  这台 channel 是什么：成员（kind, 描述）、接待员、peer。造机器只需要它。繁殖复制它。
  账本 H       这台 channel 发生过什么：消息、每一步、配置改动的记录。恢复重演它。

channel = (Config, H, R)。R（runtime）是白盒：读消息、成 view、调成员、落带盖章、投递。由 Ω 的 U 执行。
零件：Author（L、人：产生候选描述）；U（执行/验证描述，确定）；D（c0 里的构造子系统）。

D = A + B + C，是 c0 的一个成员：
  A  按配置建空 channel、构造成员、绑定地址     B  从请求者的配置复制一项（Genome = 配置）
  C  构造 → 复制 → 装进子代 → 接 peer → 启动
D 改配置的每一步都在目标账本上留一条 door 记录；D 自己那一步以 #step 落在 c0 账本上。

膜内地址 = 成员在配置里的序号（"1"…）；channel 名属于 peer。
词：#born #conf 只有 door 能说；#step 只有 R 能说；成员说的一切都是文本。
"""
from __future__ import annotations
import json, shutil, subprocess, sys, tempfile, filecmp
from dataclasses import dataclass, field, asdict
from pathlib import Path
from typing import Callable

# ----------------------------------------------------------------- 配置（描述）

@dataclass
class Member:
    kind: str             # L | U | X | D | P
    text: str = ""        # L: prompt；U: 源码；P: 对方 channel 名；X/D: 空

@dataclass
class Config:
    members: list[Member] = field(default_factory=list)   # 地址 = 序号（1 起）
    receptionist: str | None = None                        # 外来消息交给谁
    def add(self, m: Member) -> str:
        self.members.append(m); return str(len(self.members))
    def get(self, addr: str) -> Member | None:
        i = int(addr) - 1 if addr.isdigit() else -1
        return self.members[i] if 0 <= i < len(self.members) else None
    def peer_to(self, name: str) -> str | None:
        return next((str(i + 1) for i, m in enumerate(self.members) if m.kind == "P" and m.text == name), None)

# ----------------------------------------------------------------- 账本（历史）

@dataclass(frozen=True)
class Msg:
    seq: int
    sender: str           # 地址 | "door" | "R"
    to: str
    body: str

@dataclass
class Channel:
    name: str
    conf: Config
    msgs: list[Msg] = field(default_factory=list)
    cursor: dict[str, int] = field(default_factory=dict)  # 地址 → 看到哪（从 #step 重建）

@dataclass
class Space:
    dir: Path
    channels: dict[str, Channel] = field(default_factory=dict)
    def hpath(self, n: str) -> Path: return self.dir / "h" / f"{n}.jsonl"
    def cpath(self, n: str) -> Path: return self.dir / "conf" / f"{n}.json"

Apply = Callable[[str, Member, str], str]     # (channel/地址, 成员, view) -> 文本；只见 view

# ----------------------------------------------------------------- 文法

def parse(out: str) -> list[tuple[str, str]]:
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
    """作者约束：door 说 born/conf；R 说 step；成员说的一切只是文本。"""
    w, args, rest = directive(m.body)
    if (m.sender == "door" and w in ("born", "conf")) or (m.sender == "R" and w == "step"):
        return w, args, rest
    return "", [], m.body

def render(view: list[Msg]) -> str:
    return "\n".join(f"[{m.seq}] {m.sender} -> {m.to}: " + m.body.replace("\n", "\n    ") for m in view)

# ----------------------------------------------------------------- 写：账本只记录；配置改动经 door 记录并落盘

def append(sp: Space, name: str, sender: str, to: str, body: str) -> Msg:
    c = sp.channels[name]
    m = Msg(seq=(c.msgs[-1].seq + 1) if c.msgs else 1, sender=sender, to=to, body=body)
    p = sp.hpath(name); p.parent.mkdir(parents=True, exist_ok=True)
    with open(p, "a", encoding="utf-8") as f:
        f.write(json.dumps(m.__dict__, ensure_ascii=False) + "\n")
    _fold(sp, name, m)
    return m

def _fold(sp: Space, name: str, m: Msg) -> None:
    c = sp.channels[name]
    c.msgs.append(m)
    w, args, rest = word_of(m)
    if w == "conf":                                           # 配置改动的记录 → 重建配置（恢复用）
        if args[0] == "add":  c.conf.add(Member(kind=args[1], text=rest))
        elif args[0] == "in": c.conf.receptionist = args[1]
    elif w == "step":
        kv = dict(x.split("=", 1) for x in args)
        c.cursor[kv["actor"]] = int(kv["upto"])

def save_conf(sp: Space, name: str) -> None:
    p = sp.cpath(name); p.parent.mkdir(parents=True, exist_ok=True)
    p.write_text(json.dumps(asdict(sp.channels[name].conf), ensure_ascii=False, indent=1), encoding="utf-8")

def born(sp: Space, name: str, by: str) -> Channel:
    """A：按空配置建一台新 channel——一份配置、一条空账本。造机器不需要账本。"""
    c = Channel(name=name, conf=Config()); sp.channels[name] = c
    append(sp, name, "door", "", f"#born name={name} by={by} order={len(sp.channels) - 1}")
    save_conf(sp, name)
    return c

def conf_add(sp: Space, name: str, m: Member) -> str:
    """改配置：加一个成员。动作落账（#conf add），配置落盘。"""
    addr = str(len(sp.channels[name].conf.members) + 1)
    append(sp, name, "door", "", f"#conf add {m.kind}\n{m.text}")     # _fold 会把它加进配置
    save_conf(sp, name)
    return addr

def conf_in(sp: Space, name: str, addr: str) -> None:
    append(sp, name, "door", "", f"#conf in {addr}"); save_conf(sp, name)

def load(dir: Path) -> Space:
    """恢复：从账本重演出配置与游标。"""
    sp = Space(dir=dir)
    hdir = dir / "h"
    if not hdir.exists(): return sp
    firsts = {p.stem: json.loads(p.read_text(encoding="utf-8").splitlines()[0]) for p in hdir.glob("*.jsonl")}
    order = {n: int(dict(x.split("=", 1) for x in directive(f["body"])[1]).get("order", 0)) for n, f in firsts.items()}
    for n in sorted(firsts, key=lambda n: order[n]):
        sp.channels[n] = Channel(name=n, conf=Config())
        for line in sp.hpath(n).read_text(encoding="utf-8").splitlines():
            _fold(sp, n, Msg(**json.loads(line)))
    return sp

# ----------------------------------------------------------------- D：构造子系统（c0 的成员）。输入是配置，不是账本。
#
#   build <name>       A：建空 channel
#   part <addr>        B：复制请求者配置里的第 <addr> 项
#   decl <L|U> …       A：用新描述构造一个成员（其后各行是正文，直到下一个关键字）
#   in <#k|addr>       接待员
#   peer <channel>     C：双向接 peer
#   start <text…>      C：以 c0 门的名义把启动消息交给接待员
#   attach here|<channel> …   对已有 channel 做上述改动

KEYS = ("part", "decl", "in", "peer", "start")

def D(sp: Space, home: str, me: str, view: list[Msg]) -> str:
    out = []
    for m in view:
        lines = m.body.splitlines()
        if not lines: continue
        head = lines[0].split()
        src = sp.channels[home].conf.get(m.sender)
        origin = src.text if (src and src.kind == "P") else home              # 请求者所在的 channel
        if head[0] == "build" and len(head) > 1:
            born(sp, head[1], f"{home}/{me}")
            out.append(f">>> {m.sender}\n" + _construct(sp, origin, head[1], lines[1:], home))
        elif head[0] == "attach" and len(head) > 1:
            target = origin if head[1] == "here" else head[1]
            if target in sp.channels:
                out.append(f">>> {m.sender}\n" + _construct(sp, origin, target, lines[1:], home))
        # 非请求：沉默
    return "\n".join(out)

def _construct(sp: Space, origin: str, target: str, spec: list[str], dhome: str) -> str:
    src, made, report, start, i = sp.channels[origin].conf, [], [], None, 0
    while i < len(spec):
        t = spec[i].split(); i += 1
        if not t: continue
        if t[0] == "part" and len(t) > 1 and (pm := src.get(t[1])) and pm.kind in ("L", "U"):
            a = conf_add(sp, target, Member(kind=pm.kind, text=pm.text))               # B：复制配置项
            made.append(a); report.append(f"part {t[1]} -> {target}/{a}")
        elif t[0] == "decl" and len(t) > 1 and t[1] in ("L", "U"):
            body = [" ".join(t[2:])] if len(t) > 2 else []
            while i < len(spec) and (spec[i].split() or [""])[0] not in KEYS:
                body.append(spec[i]); i += 1
            a = conf_add(sp, target, Member(kind=t[1], text="\n".join(body)))         # A：新描述构造
            made.append(a); report.append(f"decl -> {target}/{a}")
        elif t[0] == "in" and len(t) > 1:
            ref = made[int(t[1][1:]) - 1] if t[1].startswith("#") else t[1]
            conf_in(sp, target, ref); report.append(f"in {ref}")
        elif t[0] == "peer" and len(t) > 1 and t[1] in sp.channels:
            a = conf_add(sp, target, Member(kind="P", text=t[1]))                      # C：双向接线
            b = conf_add(sp, t[1], Member(kind="P", text=target))
            report.append(f"peer {target}/{a} <-> {t[1]}/{b}")
        elif t[0] == "start":
            start = " ".join(t[1:]) + ("\n" + "\n".join(spec[i:]) if i < len(spec) else ""); break
        else:
            report.append(f"skip: {' '.join(t)}")
    c = sp.channels[target]
    if start is not None and c.conf.receptionist:                                      # C：启动
        gate = c.conf.peer_to(dhome) or "door"
        append(sp, target, gate, c.conf.receptionist, start); report.append(f"start -> {target}/{c.conf.receptionist}")
    return "\n".join(report) or "nothing"

# ----------------------------------------------------------------- R：一台 channel 的运行

def view_of(c: Channel, addr: str) -> list[Msg]:
    cur = c.cursor.get(addr, 0)
    return [m for m in c.msgs if m.seq > cur and m.to == addr and m.sender != "R"]

def step(sp: Space, name: str, addr: str, apply: dict[str, Apply]) -> str:
    c = sp.channels[name]; mem = c.conf.get(addr)
    view = view_of(c, addr)
    out = D(sp, name, addr, view) if mem.kind == "D" else apply[mem.kind](f"{name}/{addr}", mem, render(view))
    append(sp, name, "R", "", f"#step actor={addr} upto={view[-1].seq if view else c.cursor.get(addr, 0)}\n{out}")
    for to, body in parse(out):
        if c.conf.get(to): append(sp, name, addr, to, body)                                 # 只能寻址本膜
    return out

def deliver(sp: Space, name: str, m: Msg) -> bool:
    """发给 peer 门的消息：door 抄进对方账本，交给对方接待员。返回是否有成员要 step。"""
    c = sp.channels[name]; mem = c.conf.get(m.to)
    if not mem or c.cursor.get(m.to, 0) >= m.seq: return False
    if mem.kind == "P":
        other = sp.channels[mem.text]
        if other.conf.receptionist:
            append(sp, other.name, other.conf.peer_to(name) or "door", other.conf.receptionist, m.body)
        c.cursor[m.to] = m.seq
        return False
    return True

def run_channel(sp: Space, name: str, apply, pos: dict, budget: list) -> bool:
    c = sp.channels[name]; acted = False
    while pos.get(name, 0) < len(c.msgs) and budget[0] > 0:
        m = c.msgs[pos.get(name, 0)]; pos[name] = pos.get(name, 0) + 1
        if deliver(sp, name, m):
            step(sp, name, m.to, apply); acted = True; budget[0] -= 1
    return acted

def run(sp: Space, apply: dict[str, Apply], max_steps: int = 2000) -> None:
    """宿主 Ω：轮流让每台 channel 跑到静止；全静止时轮询外生者一轮；无人开口即停。预算是宿主的事。"""
    pos, budget = {}, [max_steps]
    while budget[0] > 0:
        while any(run_channel(sp, n, apply, pos, budget) for n in list(sp.channels)): pass
        spoke = False
        for n in list(sp.channels):
            for i, mem in enumerate(sp.channels[n].conf.members):
                if mem.kind == "X":
                    spoke |= bool(step(sp, n, str(i + 1), apply).strip()); budget[0] -= 1
        if not spoke: return

# ----------------------------------------------------------------- 零件实现（Ω）

def U(who: str, mem: Member, view: str) -> str:
    scratch = Path(tempfile.mkdtemp(prefix="dalek-u-"))
    try:
        r = subprocess.run([sys.executable, "-c", mem.text], input=view, capture_output=True, text=True, cwd=scratch, timeout=60)
        return r.stdout
    finally:
        shutil.rmtree(scratch, ignore_errors=True)

def tape(outs: list[tuple[str, str]], live: dict[str, Apply] | None = None, by_addr: bool = False) -> Apply:
    """录音带：(期望 "<channel>/<addr>", out)。live 里的种类重算。by_addr：每地址一条队列（replay 用）。"""
    live = live or {}
    if by_addr:
        q: dict[str, list[str]] = {}
        for who, out in outs: q.setdefault(who, []).append(out)
        def g(who: str, mem: Member, view: str) -> str:
            if mem.kind in live: return live[mem.kind](who, mem, view)
            if not q.get(who):
                if mem.kind == "X": return ""
                raise RuntimeError(f"divergence: no record for {who}")
            return q[who].pop(0)
        return g
    it = iter(outs)
    def f(who: str, mem: Member, view: str) -> str:
        exp, out = next(it, (None, None))
        if exp is None:
            if mem.kind in live: return live[mem.kind](who, mem, view)
            if mem.kind == "X": return ""
            raise RuntimeError(f"tape exhausted at {who}")
        if exp != who: raise RuntimeError(f"divergence: expected {exp}, got {who}")
        return live[mem.kind](who, mem, view) if mem.kind in live else out
    return f

# ----------------------------------------------------------------- genesis / replay

def genesis(dir: Path, name: str = "c0") -> Space:
    shutil.rmtree(dir / "h", ignore_errors=True); shutil.rmtree(dir / "conf", ignore_errors=True)
    sp = Space(dir=dir); born(sp, name, "omega")
    return sp

def replay(src: Path) -> bool:
    """同一段 run：Author/人照抄记录，U 与 D 重算；逐字节比较每条账本。"""
    sp = load(src)
    recs = []
    for n, c in sp.channels.items():
        for m in c.msgs:
            if m.sender == "R":
                kv = dict(x.split("=", 1) for x in directive(m.body)[1])
                recs.append((f"{n}/{kv['actor']}", directive(m.body)[2]))
    sp2 = Space(dir=Path(tempfile.mkdtemp(prefix="dalek-replay-")))
    c0 = next(iter(sp.channels)); born(sp2, c0, "omega")
    for m in sp.channels[c0].msgs[1:]:                    # 首步之前 Ω 对 c0 配置的改动照抄
        if m.sender == "R": break
        if m.sender == "door": append(sp2, c0, "door", m.to, m.body); save_conf(sp2, c0)
    t = tape(recs, live={"U": U}, by_addr=True)
    run(sp2, {"L": t, "X": t, "U": t})
    return all(filecmp.cmp(sp.hpath(n), sp2.hpath(n), shallow=False) for n in sp.channels)

# ----------------------------------------------------------------- CLI

def show(sp: Space) -> str:
    return "\n".join(f"[{n}/{m.seq:<3}] {m.sender:6} -> {m.to or '-':4}: {(m.body.splitlines() or [''])[0][:78]}"
                     for n, c in sp.channels.items() for m in c.msgs)

def main(argv: list[str]) -> None:
    cmd, d = argv[1], Path(argv[2])
    sp = load(d)
    if cmd == "show": print(show(sp))
    elif cmd == "conf":
        for n, c in sp.channels.items():
            print(f"{n} in={c.conf.receptionist}")
            for i, m in enumerate(c.conf.members): print(f"  {i + 1:3} {m.kind} {m.text[:40]!r}")
    elif cmd == "replay":
        ok = replay(d); print("replay:", "identical" if ok else "DIVERGED"); sys.exit(0 if ok else 1)

if __name__ == "__main__":
    main(sys.argv)
