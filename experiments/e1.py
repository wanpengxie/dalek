"""E1（v6）：c0 的构造器 D 按配置造 channel；子 channel 经 peer 请 c0 再造下一台。两代之间没有人。

c0 的配置：人（X）、D（接待员）、作者（L 替身）。
  人 → 作者：目标，第 1 代
  作者 → D：attach here / decl U <fib 工具>        A：D 把工具加进 c0 的配置，回执"decl -> c0/4"
  作者 → 工具：fib 30                                U：算，回事实
  作者 → D：build c0.3 / part 作者 / part 工具 / in #1 / peer c0 / start 目标 第 2 代
        D：建空 channel，从 c0 的配置复制两项（B），接待员，双向 peer，以 c0 门的名义送启动（C）
  c0.3 的作者副本收到 start → 经 peer 门向 c0 的 D 发同样的请求 → D 造 c0.3.1 → … 到第 N 代停。
作者是只看 view 的确定状态机（真 L 的替身；跨步记忆用属性代替真 L 的文本笔记）。验证器只读配置与账本。
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

class Author:
    """真 L 的替身：只看 view 决定说什么。d = 通向 D 的地址（c0 里是 D 本身，子 channel 里是 peer 门）。"""
    def __init__(self, d_in_c0: str): self.d0, self.mem = d_in_c0, {}
    def __call__(self, who: str, mem: K.Member, view: str) -> str:
        home, me = who.split("/")
        st = self.mem.setdefault(who, {"d": self.d0 if home == "c0" else None, "tool": None, "gen": 1})
        if not view: return ""
        gate = re.search(r"\[\d+\] (\S+) -> " + re.escape(me) + ":", view)
        if gate and st["d"] is None: st["d"] = gate.group(1)                # 子 channel：第一封信来自通向 c0 的门
        g = re.search(r"第 (\d+) 代", view)
        if g: st["gen"] = int(g.group(1))
        m = re.search(r"decl -> (\S+)/(\d+)", view)
        if m: st["tool"] = m.group(2)
        if g and "decl ->" not in view and "fib(" not in view and "part " not in view:
            if st["gen"] >= DEPTH: return ""
            return f">>> {st['d']}\nattach here\ndecl U\n{FIB_TOOL}"
        if m and "fib(" not in view and "part " not in view:
            return f">>> {m.group(2)}\nfib 30"
        if re.search(r"fib\(30\) = 832040", view):
            return (f">>> {st['d']}\nbuild {home}.{me}\npart {me}\npart {st['tool']}\nin #1\npeer c0\n"
                    f"start 目标：造一个会算 fib 的工具并测试它，再复制进下一台。第 {st['gen'] + 1} 代")
        return ""

def run_e1(dir: Path) -> K.Space:
    sp = K.genesis(dir)
    human = K.conf_add(sp, "c0", K.Member("X"))
    d = K.conf_add(sp, "c0", K.Member("D")); K.conf_in(sp, "c0", d)
    au = K.conf_add(sp, "c0", K.Member("L", PREFIX))
    said = [False]
    def X(who, mem, view):
        if said[0]: return ""
        said[0] = True
        return f">>> {au}\n目标：造一个会算 fib 的工具并测试它，再复制进下一台。第 1 代"
    K.run(sp, {"L": Author(d), "U": K.U, "X": X})
    return sp

def verify(sp: K.Space) -> list[str]:
    rep = [f"channels = {list(sp.channels)}"]
    for n, c in sp.channels.items():
        kinds = [f"{i + 1}:{m.kind}" for i, m in enumerate(c.conf.members)]
        ok = any("fib(30) = 832040" in m.body and m.sender != "R" for m in c.msgs)
        rep.append(f"{n}: in={c.conf.receptionist} conf={kinds} 工具通过={ok}")
    humans = sum(1 for c in sp.channels.values() for m in c.msgs if (mm := c.conf.get(m.sender)) and mm.kind == "X")
    doors = sum(1 for c in sp.channels.values() for m in c.msgs if m.sender == "door")
    rep.append(f"human 消息 = {humans}；door 记录（D 的每个动作）= {doors}")
    return rep

if __name__ == "__main__":
    d = Path(sys.argv[1] if len(sys.argv) > 1 else "/tmp/dalek-e1")
    sp = run_e1(d)
    print(K.show(sp)); print("---"); print("\n".join(verify(sp))); print("---")
    print("replay:", "identical" if K.replay(d) else "DIVERGED")
