"""E1：递归自我组织——机器造机器，两代之间没有人。

c0 里有一个构造者（#decl L）。人只在开头说一句目标。之后：
  构造者 → #decl U 造一个工具（发给自己以获知地址）→ 测试工具 → 问 H 回忆自己的代数 →
  #decl M 把自己和工具复印进下一台机器并带启动消息 → 子机器里的副本收到启动消息 → 同样循环 → 到第 N 代停。
本文件里的 L 是一个确定的状态机（真 L 的替身）：**只看 view**；需要历史就问 H。换真模型只换 apply["L"]。
验证器只读带子。
注意：所有 channel 共用一个 Space / K 循环 / 账本，所以这证明的是递归自我组织，不是独立单元的复制（那是 E3）。
"""
from __future__ import annotations
import re, sys
from pathlib import Path
sys.path.insert(0, str(Path(__file__).resolve().parents[1]))
import kernel as K

DEPTH = 3   # 造到第 DEPTH 代为止（c0 是第 1 代）

FIB_TOOL = '''import sys, re
v = sys.stdin.read()
m = re.search(r"fib (\\d+)", v)
if m:
    n = int(m.group(1)); a, b = 0, 1
    for _ in range(n): a, b = b, a + b
    who = re.search(r"\\] (\\S+) ->", v).group(1)
    print(">>> " + who)
    print("fib(%d) = %d" % (n, a))'''

CONSTRUCTOR_PREFIX = "你是构造者。目标：造一个会算 fib 的工具，测试它，然后把自己和工具复印进下一台机器并交代同样的目标。"

def constructor(a: K.Addr, view: str) -> str:
    """真 L 的替身：只看 view 的确定状态机。"""
    me = a.id
    if not view:
        return ""
    # 1. 收到目标（来自人或父机器）→ 造工具，发给自己以获知地址；代数写进工具描述
    g = re.search(r"第 (\d+) 代", view)
    if g and "#decl" not in view and "fib(" not in view and "> steps" not in view:
        gen = int(g.group(1))
        if gen >= DEPTH:
            return ""                                     # 到深度即停：这一代不再造
        return f">>> {me}\n#decl U\n{FIB_TOOL}\n# gen {gen}"
    # 2. 看到自己写的 #decl U 落带 → 得知工具地址 → 测试
    m = re.search(rf"\[(\S+)\] {re.escape(me)} -> {re.escape(me)}: #decl U", view)
    if m:
        return f">>> {m.group(1)}\nfib 30"
    # 3. 工具回答正确 → 问 H：我之前的步里写过什么（回忆代数与工具地址）
    if re.search(r"fib\(30\) = 832040", view):
        return f">>> H\nsteps {me}"
    # 4. H 的回答 → 读出代数与工具地址 → 复印自己和工具进下一台机器，带启动消息
    if "> steps" in view:
        gen = int(re.search(r"# gen (\d+)", view).group(1))
        tool = re.search(r">>> (\S+)\s+fib 30", view)              # 从自己那一步的记录里读出工具地址
        tool = tool.group(1) if tool else None
        if not tool:
            return ""
        return (f">>> {me}\n#decl M\npart {me}\npart {tool}\nin {me}\nstart\n"
                f"目标：造一个会算 fib 的工具并测试它，再复印进下一台机器。第 {gen + 1} 代")
    return ""

def run_e1(dir: Path) -> K.Space:
    sp = K.genesis(dir)
    human = K.append(sp, "c0", "door", "", "#admit human").addr()
    ctor = K.append(sp, "c0", "door", "", f"#decl L\n{CONSTRUCTOR_PREFIX}").addr()
    said = [False]
    def X(a, view):                                          # 人：只说一句，之后无话
        if said[0]: return ""
        said[0] = True
        return f">>> {ctor}\n目标：造一个会算 fib 的工具并测试它，再复印进下一台机器。第 1 代"
    K.run(sp, {"L": constructor, "U": K.U, "X": X})
    return sp

def verify(sp: K.Space) -> list[str]:
    report = []
    chans = list(sp.channels.values())
    report.append(f"机器数 = {len(chans)}：{[c.id for c in chans]}")
    for c in chans[1:]:
        roots = [a.root for a in c.book.values() if a.id not in K.ROOTS and a.root != "P"]
        start = [m for m in c.msgs if m.sender == f"P:{c.id}"]
        report.append(f"{c.id}: parent={c.parent} parts={roots} 启动消息={len(start)} 接待员={c.receptionist} out={c.out}")
    for c in chans:
        ok = any("fib(30) = 832040" in m.body and m.sender != "K" for m in c.msgs)
        report.append(f"{c.id}: 工具通过测试 = {ok}")
    for c in chans:
        for m in c.msgs:
            if m.body.startswith("#decl U") and m.sender != "door":
                steps = [s for s in c.msgs if s.sender == "K" and f"actor={m.sender} " in s.body and s.seq < m.seq]
                upto = int(re.search(r"upto=(\d+)", steps[-1].body).group(1)) if steps else -1
                report.append(f"{c.id}: 工具 {m.addr()} 由步 upto={upto} 产生，{'不含自身 ✓' if upto < m.seq else '✗'}")
    humans = [m for c in chans for m in c.msgs if m.sender in c.book and c.book[m.sender].root == "X"]
    report.append(f"human 消息总数 = {len(humans)}（全部在 {sorted({m.ch for m in humans})}）")
    hq = [m for c in chans for m in c.msgs if m.to == "H"]
    report.append(f"构造者问 H 的次数 = {len(hq)}")
    return report

if __name__ == "__main__":
    d = Path(sys.argv[1] if len(sys.argv) > 1 else "/tmp/dalek-e1")
    import shutil; shutil.rmtree(d, ignore_errors=True)
    sp = run_e1(d)
    print(K.show(sp)); print("---")
    print("\n".join(verify(sp))); print("---")
    print("replay:", "identical" if K.replay(d) else "DIVERGED")
