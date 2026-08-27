"""Dalek Core kernel — 最简版（v2 模型）。

一条账本；K 沿着账本走；一切皆地址；派生地址是账本数据。

词表：#genesis  #admit  #decl L|U  #recipe  #disable  #enable  #step  + 普通消息(to 列表)
根：  L（文本→文本，随机，注入）  U（程序+文本→文本，确定，注入）
"""
from __future__ import annotations
import json, os, subprocess, sys, tempfile, filecmp
from dataclasses import dataclass, field
from pathlib import Path
from typing import Callable

# ----------------------------------------------------------------- 对象

@dataclass(frozen=True)
class Msg:
    g: int            # 全局追加序号（K 写）
    ch: str           # 带子（K 写）
    seq: int          # 带内序号（K 写）
    sender: str       # 地址 | "door" | "K"（K 写）
    to: tuple[str, ...]
    body: str

    def addr(self) -> str:
        return f"{self.ch}/{self.seq}"

@dataclass
class Addr:
    id: str
    ch: str
    root: str         # "L" | "U" | "X"(外生)
    prefix: str = ""  # L: prompt 前缀；U: python 源码；X: 空
    cursor: int = 0
    enabled: bool = True

@dataclass
class Channel:
    id: str
    parent: str | None = None
    msgs: list[Msg] = field(default_factory=list)
    book: dict[str, Addr] = field(default_factory=dict)   # 插入序 = 派发序

    def __post_init__(self):
        for r in ("L", "U"):                               # 内建根地址
            self.book[r] = Addr(id=r, ch=self.id, root=r)

@dataclass
class Space:
    dir: Path
    channels: dict[str, Channel] = field(default_factory=dict)
    ledger: list[Msg] = field(default_factory=list)       # 全局顺序
    nsteps: int = 0

    @property
    def path(self) -> Path:
        return self.dir / "ledger.jsonl"

Apply = Callable[[Addr, str], str]

# ----------------------------------------------------------------- 文法

def parse(out: str) -> list[tuple[tuple[str, ...], str]]:
    """'>>> a b' 头 + body 行；第一个头之前的文本丢弃。"""
    res, to, buf = [], None, []
    for line in out.splitlines():
        if line.startswith(">>> "):
            if to is not None:
                res.append((to, "\n".join(buf).rstrip("\n")))
            to, buf = tuple(line[4:].split()), []
        elif to is not None:
            buf.append(line)
    if to is not None:
        res.append((to, "\n".join(buf).rstrip("\n")))
    return res

def directive(body: str) -> tuple[str, list[str], str]:
    """返回 (词, 参数, 其余行)。非指令则词为 ''。"""
    head, _, rest = body.partition("\n")
    if head.startswith("#"):
        parts = head[1:].split()
        return parts[0], parts[1:], rest
    return "", [], body

def render(view: list[Msg]) -> str:
    lines = []
    for m in view:
        b = m.body.replace("\n", "\n    ")
        lines.append(f"[{m.addr()}] {m.sender} -> {' '.join(m.to)}: {b}")
    return "\n".join(lines)

# ----------------------------------------------------------------- 账本：append 是唯一写路径，落带即折叠

def append(sp: Space, ch: str, sender: str, to: tuple[str, ...], body: str) -> Msg:
    c = sp.channels.get(ch)
    seq = (c.msgs[-1].seq + 1) if (c and c.msgs) else 1
    m = Msg(g=len(sp.ledger) + 1, ch=ch, seq=seq, sender=sender, to=to, body=body)
    with open(sp.path, "a", encoding="utf-8") as f:
        f.write(json.dumps(m.__dict__, ensure_ascii=False) + "\n")
    _fold(sp, m)
    word, _, rest = directive(body)
    if word == "recipe":                                    # 遗传：door 在写入时过界复印（重读时已在带上）
        new = f"c{len(sp.channels)}"
        append(sp, new, "door", (), f"#genesis parent={m.addr()}")
        for aid in rest.split():
            src = sp.channels[ch].book[aid]
            append(sp, new, "door", (), f"#decl {src.root}\n{src.prefix}")
        append(sp, ch, "door", (sender,), f"#created {new}")
    return m

def _fold(sp: Space, m: Msg) -> None:
    sp.ledger.append(m)
    word, args, rest = directive(m.body)
    if word == "genesis":                                   # 存在
        parent = next((a[7:] for a in args if a.startswith("parent=")), None)
        sp.channels[m.ch] = Channel(id=m.ch, parent=parent)
    c = sp.channels[m.ch]
    c.msgs.append(m)
    if word == "admit":                                     # 进入
        c.book[m.addr()] = Addr(id=m.addr(), ch=m.ch, root="X")
    elif word == "decl":                                    # 命名：创建 = 描述
        c.book[m.addr()] = Addr(id=m.addr(), ch=m.ch, root=args[0], prefix=rest)
    elif word in ("disable", "enable"):                     # 生死
        c.book[args[0]].enabled = (word == "enable")
    elif word == "step":                                    # 记忆
        kv = dict(a.split("=", 1) for a in args)
        c.book[kv["actor"]].cursor = int(kv["upto"])
        sp.nsteps = int(kv["n"])

def load(dir: Path) -> Space:
    sp = Space(dir=dir)
    if sp.path.exists():
        for line in sp.path.read_text(encoding="utf-8").splitlines():
            d = json.loads(line); d["to"] = tuple(d["to"])
            _fold(sp, Msg(**d))
    return sp

def genesis(dir: Path) -> Space:
    dir.mkdir(parents=True, exist_ok=True)
    sp = Space(dir=dir)
    src = Path(__file__).read_text(encoding="utf-8")
    append(sp, "c0", "door", (), "#genesis\nK=\n" + src)
    return sp

# ----------------------------------------------------------------- K：沿着账本走

def view_of(c: Channel, a: Addr) -> list[Msg]:
    return [m for m in c.msgs if m.seq > a.cursor and a.id in m.to and m.sender != "K"]

def step(sp: Space, a: Addr, apply: dict[str, Apply]) -> None:
    c = sp.channels[a.ch]
    view = view_of(c, a)
    out = apply[a.root](a, render(view))                     # 唯一非确定点
    upto = view[-1].seq if view else a.cursor
    append(sp, a.ch, "K", (), f"#step actor={a.id} upto={upto} n={sp.nsteps + 1}\n{out}")
    for to, body in parse(out):
        to = tuple(t for t in to if t in c.book)             # locality：越界者丢弃
        if to:
            append(sp, a.ch, a.id, to, body)

def run(sp: Space, apply: dict[str, Apply], max_steps: int | None = None) -> None:
    """沿着账本走：对每条消息的收件人各跑一步。账本走完时轮询外生根（外面的东西会不请自来）；
    一轮轮询无人开口即停。"""
    p = 0
    while True:
        while p < len(sp.ledger):
            m = sp.ledger[p]; p += 1
            c = sp.channels[m.ch]
            for x in m.to:
                a = c.book.get(x)
                if a and a.enabled and a.cursor < m.seq:
                    if max_steps is not None and sp.nsteps >= max_steps:
                        return
                    step(sp, a, apply)
        spoke = False
        for c in list(sp.channels.values()):
            for a in list(c.book.values()):
                if a.root == "X" and a.enabled:
                    if max_steps is not None and sp.nsteps >= max_steps:
                        return
                    n0 = len(sp.ledger)
                    step(sp, a, apply)
                    spoke |= len(sp.ledger) > n0 + 1          # 除 #step 记录外还落了消息
        if not spoke:
            return

# ----------------------------------------------------------------- 根的实现（K 之外）

def U(a: Addr, view: str) -> str:
    scratch = Path(tempfile.gettempdir()) / "dalek-core-scratch" / a.ch / a.id.replace("/", "_")
    scratch.mkdir(parents=True, exist_ok=True)
    r = subprocess.run([sys.executable, "-c", a.prefix], input=view, capture_output=True,
                       text=True, cwd=scratch, timeout=30)
    return r.stdout

def tape(outs: list[tuple[str, str]], live: dict[str, Apply] | None = None) -> Apply:
    """录音带：按顺序给出 (期望地址, out)。live 里的根（确定的 U）不照抄记录而是重算——
    这样篡改记录本身也会被 replay 抓到。"""
    it = iter(outs)
    def f(a: Addr, view: str) -> str:
        who, out = next(it)
        if who != a.id:
            raise RuntimeError(f"divergence: expected step of {who}, got {a.id}")
        return live[a.root](a, view) if live and a.root in live else out
    return f

# ----------------------------------------------------------------- replay = 同一段 run，apply 换成读 #step 记录

def replay(src: Path) -> bool:
    sp = load(src)
    recs = []
    for m in sp.ledger:
        w, args, rest = directive(m.body)
        if w == "step":
            recs.append((dict(a.split("=", 1) for a in args)["actor"], rest))
    dst = Path(tempfile.mkdtemp(prefix="dalek-core-replay-"))
    sp2 = Space(dir=dst)
    append(sp2, "c0", "door", (), sp.ledger[0].body)         # 同一条 genesis
    t = tape(recs, live={"U": U})
    for m in sp.ledger[1:]:                                    # 重放 genesis 之后、首步之前的外生输入
        if directive(m.body)[0] == "step":
            break
        if m.sender == "door" and m.ch == "c0":
            append(sp2, m.ch, m.sender, m.to, m.body)
    run(sp2, {"L": t, "U": t, "X": t})
    return filecmp.cmp(sp.path, sp2.path, shallow=False)

# ----------------------------------------------------------------- 演示

FIB_TOOL = """import sys
v = sys.stdin.read()
import re
m = re.search(r"fib (\\d+)", v)
if m:
    n = int(m.group(1)); a, b = 0, 1
    for _ in range(n): a, b = b, a + b
    print(">>> " + v.split("] ")[1].split(" ->")[0])
    print("fib(%d) = %d" % (n, a))"""

def demo(dir: Path) -> None:
    sp = genesis(dir)
    human = append(sp, "c0", "door", (), "#admit human").addr()      # c0/2
    script = [   # (谁的步, 它说的话)。派发顺序由账本决定，脚本只是按那个顺序把话备好。
        (human, ">>> L\n请造一个会算 fib 的工具，然后用它算 fib 30"),  # 人对根 L 说话
        ("L",   ">>> " + human + "\n#decl U\n" + FIB_TOOL),           # L 写下一段描述 → 地址 c0/6 诞生
        (human, ">>> c0/6\nfib 30"),                                   # 人寻址新工具（工具那一步是真 U，不在带上）
        (human, ">>> L\n把这个工具复印进一台新机器"),
        ("L",   ">>> " + human + "\n#recipe\nc0/6"),                   # 遗传：过界复印 → c1 诞生
        (human, ""), ("L", ""),                                        # 收到回执，无话
        (human, ""),                                                   # 轮询：人无话 → 停机
    ]
    t = tape(script)
    run(sp, {"L": t, "X": t, "U": U})

def main(argv: list[str]) -> None:
    cmd, d = argv[1], Path(argv[2])
    if cmd == "demo":
        demo(d)
        for m in load(d).ledger:
            b = m.body if len(m.body) < 90 else m.body[:87] + "..."
            print(f"[{m.addr():6}] {m.sender:6} -> {' '.join(m.to) or '-':8}: {b.splitlines()[0] if b else ''}")
    elif cmd == "replay":
        ok = replay(d)
        print("replay:", "identical" if ok else "DIVERGED")
        sys.exit(0 if ok else 1)
    elif cmd == "book":
        sp = load(d)
        for c in sp.channels.values():
            print(f"{c.id} (parent={c.parent}):")
            for a in c.book.values():
                print(f"  {a.id:8} root={a.root} enabled={a.enabled} cursor={a.cursor} prefix={a.prefix[:30]!r}")

if __name__ == "__main__":
    main(sys.argv)
