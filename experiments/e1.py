"""E1（v5）：c0 的构造器 D 造 channel；子 channel 经 peer 请 c0 再造下一台。两代之间没有人。

c0：人（X）、作者（L 替身）、D（构造器，接待员）。
  人 → 作者：目标，第 1 代
  作者 → D：attach here / decl U <fib 工具>          A：D 把工具贴到 c0 的膜上，回执给作者
  作者 → 工具：fib 30                                  U：算，回事实
  作者 → D：build c1 / part 作者 / part 工具 / in #1 / peer c0 / start 目标 第 2 代
        D：genesis c1，逐字复制两份描述（B），接待员，c1<->c0 接 peer，以 c0 门的名义把 start 交给 c1 的作者副本（C）
  c1 的作者副本收到 start → 经 c1 的 peer 门向 c0 的 D 发同样的请求 → D 造 c2 → … 到第 N 代停。
作者是只看 view 的确定状态机（真 L 的替身）。验证器只读账本。
"""
from __future__ import annotations
import re, sys
from pathlib import Path
sys.path.insert(0, str(Path(__file__).resolve().parents[1]))
import kernel as K

DEPTH = 3

FIB_TOOL = '''import sys, re
v = sys.stdin.read()
m = re.search(r"fib (\\d+)", v)
if m:
    n = int(m.group(1)); a, b = 0, 1
    for _ in range(n): a, b = b, a + b
    who = re.search(r"\\] (\\S+) ->", v).group(1)
    print(">>> " + who)
    print("fib(%d) = %d" % (n, a))'''

PREFIX = "你是作者。目标：请 D 造一个会算 fib 的工具，测试它，然后请 D 把你和工具复制进下一台 channel 并交代同样的目标。"

def author(a: K.Member, view: str) -> str:
    """真 L 的替身：只看 view。D 的地址与 peer 门的地址都从 view 里读。"""
    me = a.addr
    if not view: return ""
    # 谁给我发的 = 我该回给谁：本机的 D（c0）或者通向 c0 的 peer 门（子 channel）
    gate = re.search(r"\[\d+\] (\S+) -> " + re.escape(me) + ":", view).group(1)
    g = re.search(r"第 (\d+) 代", view)
    if g and "decl ->" not in view and "fib(" not in view and "part " not in view:
        gen = int(g.group(1))
        if gen >= DEPTH: return ""
        return f">>> {a.d}\nattach here\ndecl U\n{FIB_TOOL}\n# gen {gen}"   # c0 里 a.d 是 D；子 channel 里是通向 c0 的门
    m = re.search(r"decl -> (\S+)/(\d+)", view)          # D 的回执：工具贴上了，地址在这
    if m and "fib(" not in view and "part " not in view:
        return f">>> {m.group(2)}\nfib 30"
    if re.search(r"fib\(30\) = 832040", view):            # 事实 → 请 D 复制自己和工具进下一台
        gen = a.gen
        return (f">>> {a.d}\nbuild {a.home}.{me}\npart {me}\npart {a.tool}\nin #1\npeer c0\n"
                f"start 目标：造一个会算 fib 的工具并测试它，再复制进下一台。第 {gen + 1} 代")
    return ""

class Author:
    """给替身挂上它从 view 里学到的两个地址（真 L 会自己记在文本里；这里用属性代替笔记）。"""
    def __init__(self): self.mem = {}
    def __call__(self, a: K.Member, view: str) -> str:
        st = self.mem.setdefault(f"{a.home}/{a.addr}", {"d": None, "tool": None, "gen": 1})
        gate = re.search(r"\[\d+\] (\S+) -> " + re.escape(a.addr) + ":", view)
        if gate and st["d"] is None: st["d"] = gate.group(1)       # 第一条来信的发件人就是通向 D 的路（人除外，见下）
        g = re.search(r"第 (\d+) 代", view)
        if g: st["gen"] = int(g.group(1))
        m = re.search(r"decl -> (\S+)/(\d+)", view)
        if m: st["tool"] = m.group(2)
        a.d, a.tool, a.gen = st["d"], st["tool"], st["gen"]
        return author(a, view)

def run_e1(dir: Path) -> K.Space:
    sp = K.genesis(dir)
    human = str(K.append(sp, "c0", "door", "", "#admit human").seq)
    d = str(K.append(sp, "c0", "door", "", "#decl D").seq)
    K.append(sp, "c0", "door", "", f"#in {d}")                               # c0 的接待员是 D：peer 来的请求归它
    au = str(K.append(sp, "c0", "door", "", f"#decl L\n{PREFIX}").seq)
    said = [False]
    def X(a, view):
        if said[0]: return ""
        said[0] = True
        return f">>> {au}\n目标：造一个会算 fib 的工具并测试它，再复制进下一台。第 1 代"
    A = Author()
    def L(a, view):
        # 在 c0 里，作者的第一条来信来自人，不是 D；D 的地址是已知的膜内地址
        st = A.mem.setdefault(f"{a.home}/{a.addr}", {"d": None, "tool": None, "gen": 1})
        if a.home == "c0": st["d"] = d
        return A(a, view)
    K.run(sp, {"L": L, "U": K.U, "X": X})
    return sp

def verify(sp: K.Space) -> list[str]:
    rep = [f"channels = {list(sp.channels)}"]
    for n, c in sp.channels.items():
        kinds = [f"{a.addr}:{a.kind}" for a in c.book.values()]
        ok = any("fib(30) = 832040" in m.body and m.sender != "R" for m in c.msgs)
        starts = [m for m in c.msgs if m.sender not in ("door", "R") and c.book.get(m.sender, K.Member("", "")).kind == "P"]
        rep.append(f"{n}: in={c.receptionist} book={kinds} 工具通过={ok} 经门收到={len(starts)}")
    humans = sum(1 for c in sp.channels.values() for m in c.msgs if m.sender in c.book and c.book[m.sender].kind == "X")
    rep.append(f"human 消息 = {humans}")
    door_lines = sum(1 for c in sp.channels.values() for m in c.msgs if m.sender == "door")
    rep.append(f"door 写的行（D 的动作全部落账）= {door_lines}")
    return rep

if __name__ == "__main__":
    d = Path(sys.argv[1] if len(sys.argv) > 1 else "/tmp/dalek-e1")
    sp = run_e1(d)
    print(K.show(sp)); print("---"); print("\n".join(verify(sp))); print("---")
    print("replay:", "identical" if K.replay(d) else "DIVERGED")
