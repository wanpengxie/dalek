"""Dalek Core kernel — v3.

一条账本；K 沿着账本走；一切皆地址；派生地址是账本数据；带子自己也是地址。

根（每台机器内建，可直接寻址）：
  L  文本→文本，随机（注入）        U  程序+文本→文本，确定（注入）        H  带子的只读投影，确定
词（body 第一行）：
  #genesis  #admit        只有 door 能说
  #step                   只有 K 能说
  #decl L|U|M  #disable  #enable   成员能说；机器（M/P 地址）只能说文本
普通消息：to 是地址列表，每项一条边。
机器：#decl M 从本机的描述造一台新机器，并把它绑成本机的一个地址（channel as actor）。
  描述 = 零件（part）+ 接待员（in）+ 启动消息（start）。复印时地址按 map 重绑定。
"""
from __future__ import annotations
import json, re, shutil, subprocess, sys, tempfile, filecmp
from dataclasses import dataclass, field
from pathlib import Path
from typing import Callable

ROOTS = ("L", "U", "H")
DOOR_WORDS, K_WORDS, MEMBER_WORDS = {"genesis", "admit"}, {"step"}, {"decl", "disable", "enable"}

# ----------------------------------------------------------------- 对象

@dataclass(frozen=True)
class Msg:
    ch: str               # 带子            K 写
    seq: int              # 带内序号        K 写
    sender: str           # 地址|door|K     K 写
    to: tuple[str, ...]
    body: str
    def addr(self) -> str: return f"{self.ch}/{self.seq}"

@dataclass
class Addr:
    id: str
    ch: str
    root: str             # L | U | H | X(外生) | M(子机器) | P(父机器)
    prefix: str = ""      # L: prompt 前缀；U: 源码；M: 子机器 id；P: 父机器里对应的 M 地址
    cursor: int = 0
    enabled: bool = True
    def is_member(self) -> bool: return self.root in ("L", "U", "X")
    def is_copyable(self) -> bool: return self.root in ("L", "U")

@dataclass
class Channel:
    id: str
    parent: str | None = None
    receptionist: str | None = None
    msgs: list[Msg] = field(default_factory=list)
    book: dict[str, Addr] = field(default_factory=dict)      # 插入序 = 轮询序
    def __post_init__(self):
        for r in ROOTS:
            self.book[r] = Addr(id=r, ch=self.id, root=r)

@dataclass
class Space:
    dir: Path
    channels: dict[str, Channel] = field(default_factory=dict)
    ledger: list[Msg] = field(default_factory=list)
    pending: list[int] = field(default_factory=list)        # enable 后需补发的账本位置
    @property
    def path(self) -> Path: return self.dir / "ledger.jsonl"
    def nsteps(self) -> int: return sum(1 for m in self.ledger if m.sender == "K")

Apply = Callable[["Space", Addr, str], str]

# ----------------------------------------------------------------- 文法

def parse(out: str) -> list[tuple[tuple[str, ...], str]]:
    """'>>> a b' 头 + body 行；第一个头之前的文本丢弃。"""
    res, to, buf = [], None, []
    for line in out.splitlines():
        if line.startswith(">>> "):
            if to is not None: res.append((to, "\n".join(buf).rstrip("\n")))
            to, buf = tuple(line[4:].split()), []
        elif to is not None:
            buf.append(line)
    if to is not None: res.append((to, "\n".join(buf).rstrip("\n")))
    return res

def directive(body: str) -> tuple[str, list[str], str]:
    head, _, rest = body.partition("\n")
    if head.startswith("#"):
        parts = head[1:].split()
        return (parts[0] if parts else ""), parts[1:], rest
    return "", [], body

def word_of(sp: Space, m: Msg) -> tuple[str, list[str], str]:
    """带作者约束的词：说错话的人，他的话只是文本。door 说 genesis/admit/decl；K 说 step；成员说 decl/disable/enable；机器只说文本。"""
    w, args, rest = directive(m.body)
    if m.sender == "door":  ok = w in DOOR_WORDS or w == "decl"
    elif m.sender == "K":   ok = w in K_WORDS
    else:
        a = sp.channels[m.ch].book.get(m.sender) if m.ch in sp.channels else None
        ok = w in MEMBER_WORDS and (a is None or a.is_member())
    return (w, args, rest) if ok else ("", [], m.body)

def render(view: list[Msg]) -> str:
    return "\n".join(f"[{m.addr()}] {m.sender} -> {' '.join(m.to)}: " + m.body.replace("\n", "\n    ") for m in view)

def relocate(text: str, amap: dict[str, str]) -> str:
    for src in sorted(amap, key=len, reverse=True):
        text = re.sub(rf"(?<![\w/]){re.escape(src)}(?!\d)", amap[src], text)
    return text

def parse_machine(rest: str) -> tuple[list[str], str | None, str]:
    """#decl M 的 body：part <addr>… / in <addr> / start + 其后全部行。"""
    parts, recept, start, lines = [], None, [], iter(rest.splitlines())
    for line in lines:
        t = line.split()
        if t and t[0] == "part": parts += t[1:]
        elif t and t[0] == "in": recept = t[1]
        elif t and t[0] == "start": start = list(lines); break
    return parts, recept, "\n".join(start)

# ----------------------------------------------------------------- 账本：append 是唯一写路径；折叠只改内存；跨界由 door 在写入时完成

def append(sp: Space, ch: str, sender: str, to: tuple[str, ...], body: str) -> Msg:
    c = sp.channels.get(ch)
    m = Msg(ch=ch, seq=(c.msgs[-1].seq + 1) if (c and c.msgs) else 1, sender=sender, to=to, body=body)
    with open(sp.path, "a", encoding="utf-8") as f:
        f.write(json.dumps(m.__dict__, ensure_ascii=False) + "\n")
    _fold(sp, m)
    w, args, rest = word_of(sp, m)
    if w == "decl" and args and args[0] == "M":               # 生成：造一台机器
        _construct(sp, m, rest)
    for x in to:                                              # 跨界：机器地址是门
        a = sp.channels[ch].book.get(x)
        if a and a.root == "M":
            child = sp.channels[a.prefix]
            append(sp, child.id, f"P:{child.id}", (child.receptionist,), body)
        elif a and a.root == "P":
            parent_ch, seq = a.prefix.split("/")
            declarer = sp.channels[parent_ch].msgs[int(seq) - 1].sender
            append(sp, parent_ch, a.prefix, (declarer,), body)
    return m

def _construct(sp: Space, m: Msg, rest: str) -> None:
    parts, recept, start = parse_machine(rest)
    src = sp.channels[m.ch]
    child = f"c{len(sp.channels)}"
    amap = {p: f"{child}/{i + 3}" for i, p in enumerate(parts)}   # 1=genesis 2=admit parent 3..=parts
    recept_new = amap.get(recept, recept) if recept else amap[parts[0]]
    append(sp, child, "door", (), f"#genesis parent={m.addr()} in={recept_new} map=" + ",".join(f"{k}:{v}" for k, v in amap.items()))
    append(sp, child, "door", (), f"#admit parent={m.addr()}")
    for p in parts:
        a = src.book[p]
        append(sp, child, "door", (), f"#decl {a.root}\n{relocate(a.prefix, amap)}")
    if start:
        append(sp, child, f"P:{child}", (recept_new,), relocate(start, amap))
    append(sp, m.ch, "door", (m.sender,), f"#created {child} map=" + ",".join(f"{k}:{v}" for k, v in amap.items()))

def _fold(sp: Space, m: Msg) -> None:
    sp.ledger.append(m)
    w, args, rest = word_of(sp, m)
    kv = dict(a.split("=", 1) for a in args if "=" in a)
    if w == "genesis":
        sp.channels[m.ch] = Channel(id=m.ch, parent=kv.get("parent"), receptionist=kv.get("in"))
    c = sp.channels[m.ch]
    c.msgs.append(m)
    if w == "admit":
        if "parent" in kv: c.book[f"P:{c.id}"] = Addr(id=f"P:{c.id}", ch=c.id, root="P", prefix=kv["parent"])
        else:              c.book[m.addr()] = Addr(id=m.addr(), ch=c.id, root="X")
    elif w == "decl" and args and args[0] == "M":
        child = f"c{len(sp.channels)}"
        c.book[m.addr()] = Addr(id=m.addr(), ch=c.id, root="M", prefix=child)   # 机器是一个地址
    elif w == "decl" and args:
        c.book[m.addr()] = Addr(id=m.addr(), ch=c.id, root=args[0], prefix=rest)
    elif w in ("disable", "enable") and args and args[0] in c.book:
        a = c.book[args[0]]; a.enabled = (w == "enable")
        if a.enabled:                                         # 补发被跳过的积压
            sp.pending += [i for i, x in enumerate(sp.ledger) if x.ch == c.id and a.id in x.to and x.seq > a.cursor]
    elif w == "step":
        c.book[kv["actor"]].cursor = int(kv["upto"])

def load(dir: Path) -> Space:
    sp = Space(dir=dir)
    if sp.path.exists():
        for line in sp.path.read_text(encoding="utf-8").splitlines():
            d = json.loads(line); d["to"] = tuple(d["to"]); _fold(sp, Msg(**d))
    return sp

def genesis(dir: Path, ksrc: str | None = None) -> Space:
    dir.mkdir(parents=True, exist_ok=True)
    sp = Space(dir=dir)
    append(sp, "c0", "door", (), "#genesis\nK=\n" + (ksrc or Path(__file__).read_text(encoding="utf-8")))
    return sp

# ----------------------------------------------------------------- K：沿着账本走

def view_of(c: Channel, a: Addr) -> list[Msg]:
    return [m for m in c.msgs if m.seq > a.cursor and a.id in m.to and m.sender != "K"]

def valid_target(sp: Space, c: Channel, to: tuple[str, ...], body: str) -> tuple[str, ...]:
    to = tuple(t for t in to if t in c.book)                               # locality
    w, args, rest = directive(body)
    if w == "decl" and args and args[0] == "M":                            # 坏配方整条拒绝，不留半成品
        parts, _, _ = parse_machine(rest)
        if not parts or any(p not in c.book or not c.book[p].is_copyable() for p in parts): return ()
    return to

def step(sp: Space, a: Addr, apply: dict[str, Apply]) -> str:
    c = sp.channels[a.ch]
    view = view_of(c, a)
    out = apply[a.root](sp, a, render(view))                               # 唯一非确定点
    append(sp, a.ch, "K", (), f"#step actor={a.id} upto={view[-1].seq if view else a.cursor}\n{out}")
    for to, body in parse(out):
        to = valid_target(sp, c, to, body)
        if to: append(sp, a.ch, a.id, to, body)
    return out

def run(sp: Space, apply: dict[str, Apply], max_steps: int | None = None) -> None:
    """对账本上每条消息的每个收件人各跑一步；账本走完时轮询外生根；无人开口即停。"""
    p = 0
    def budget(): return max_steps is not None and sp.nsteps() >= max_steps
    while True:
        while sp.pending or p < len(sp.ledger):
            if sp.pending: i = sp.pending.pop(0)
            else: i = p; p += 1
            m = sp.ledger[i]; c = sp.channels[m.ch]
            for x in m.to:
                a = c.book.get(x)
                if a and a.enabled and a.root in ROOTS + ("X",) and a.cursor < m.seq:
                    if budget(): return
                    step(sp, a, apply)
        spoke = False
        for c in list(sp.channels.values()):
            for a in list(c.book.values()):
                if a.root == "X" and a.enabled:
                    if budget(): return
                    spoke |= bool(step(sp, a, apply).strip())          # 开口了（哪怕话被丢弃）就再轮一次
        if not spoke: return

# ----------------------------------------------------------------- 根的实现

def U(sp: Space, a: Addr, view: str) -> str:
    scratch = Path(tempfile.mkdtemp(prefix="dalek-u-"))                    # 每步全新，之后不可读
    try:
        r = subprocess.run([sys.executable, "-c", a.prefix], input=view, capture_output=True, text=True, cwd=scratch, timeout=60)
        return r.stdout
    finally:
        shutil.rmtree(scratch, ignore_errors=True)

def H(sp: Space, a: Addr, view: str) -> str:
    """带子的只读投影。词：book | msg <addr> | range <addr> <addr> | steps <addr> | tail <n>"""
    c = sp.channels[a.ch]
    out = []
    for m in view_of(c, a):
        q = m.body.split()
        if not q: continue
        if q[0] == "book":
            ans = "\n".join(f"{x.id} root={x.root} enabled={x.enabled} cursor={x.cursor}" for x in c.book.values())
        elif q[0] == "msg" and len(q) > 1:
            ans = render([x for x in c.msgs if x.addr() == q[1]])
        elif q[0] == "range" and len(q) > 2:
            lo, hi = int(q[1].split("/")[1]), int(q[2].split("/")[1])
            ans = render([x for x in c.msgs if lo <= x.seq <= hi and x.sender != "K"])
        elif q[0] == "steps" and len(q) > 1:
            ans = render([x for x in c.msgs if x.sender == "K" and f"actor={q[1]} " in x.body])
        elif q[0] == "tail" and len(q) > 1:
            ans = render([x for x in c.msgs if x.sender != "K"][-int(q[1]):])
        else:
            ans = "? book | msg <addr> | range <addr> <addr> | steps <addr> | tail <n>"
        out.append(f">>> {m.sender}\n> {m.body.splitlines()[0]}\n{ans}")        # 回答引用问题
    return "\n".join(out)

LIVE = {"U": U, "H": H}                                                    # 确定的根：replay 时重算

def tape(outs: list[tuple[str, str]], live: dict[str, Apply] = LIVE) -> Apply:
    """录音带：(期望地址, out) 序列。live 里的根不照抄记录而是重算。"""
    it = iter(outs)
    def f(sp: Space, a: Addr, view: str) -> str:
        who, out = next(it, (None, None))
        if who is None:
            if a.root in live: return live[a.root](sp, a, view)
            if a.root == "X": return ""                          # 外面没话了：外生根沉默
            raise RuntimeError(f"tape exhausted at step of {a.id}")
        if who != a.id: raise RuntimeError(f"divergence: expected step of {who}, got {a.id}")
        return live[a.root](sp, a, view) if a.root in live else out
    return f

# ----------------------------------------------------------------- replay：同一段 run，apply 换成读 #step 记录

def replay(src: Path) -> bool:
    sp = load(src)
    recs = [(dict(x.split("=", 1) for x in directive(m.body)[1])["actor"], directive(m.body)[2])
            for m in sp.ledger if m.sender == "K"]
    sp2 = Space(dir=Path(tempfile.mkdtemp(prefix="dalek-replay-")))
    for m in sp.ledger:                                                    # 首步之前的 door 输入是外生事实，照抄
        if m.sender == "K": break
        if m.sender == "door": append(sp2, m.ch, m.sender, m.to, m.body)
    t = tape(recs)
    run(sp2, {r: t for r in ROOTS + ("X",)}, max_steps=len(recs))
    return filecmp.cmp(sp.path, sp2.path, shallow=False)

# ----------------------------------------------------------------- CLI

def show(sp: Space) -> str:
    lines = []
    for m in sp.ledger:
        b = m.body.splitlines()[0] if m.body else ""
        lines.append(f"[{m.addr():6}] {m.sender:8} -> {' '.join(m.to) or '-':8}: {b[:80]}")
    return "\n".join(lines)

def main(argv: list[str]) -> None:
    cmd, d = argv[1], Path(argv[2])
    if cmd == "show": print(show(load(d)))
    elif cmd == "book":
        for c in load(d).channels.values():
            print(f"{c.id} parent={c.parent} in={c.receptionist}")
            for a in c.book.values(): print(f"  {a.id:10} root={a.root} enabled={a.enabled} cursor={a.cursor} prefix={a.prefix[:28]!r}")
    elif cmd == "replay":
        ok = replay(d); print("replay:", "identical" if ok else "DIVERGED"); sys.exit(0 if ok else 1)

if __name__ == "__main__":
    main(sys.argv)
