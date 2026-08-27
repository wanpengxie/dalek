"""E1：机器造机器，两代之间没有人。

c0 里有一个构造者（#decl L）。人只在开头说一句目标。之后：
  构造者 → #decl U 造一个工具（发给自己以获知地址）→ 测试工具 → #decl M 把自己和工具复印进 c1，
  并带一条启动消息 → c1 的构造者副本收到启动消息 → 同样的循环 → c2 → …到第 N 代停。
本文件里的 L 是一个确定的状态机（真 L 的替身）：它只看 view，决定说什么。换成真模型只需换 apply["L"]。
验证器只读带子：四个条件 + "两代之间没有 human 消息"。
"""
from __future__ import annotations
import re, sys
from pathlib import Path
sys.path.insert(0, str(Path(__file__).resolve().parents[1]))
import kernel as K

DEPTH = 3   # 造到 c{DEPTH-1} 为止

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

def constructor(sp: K.Space, a: K.Addr, view: str) -> str:
    """真 L 的替身：一个只看 view 的确定状态机。"""
    me = a.id
    if not view:
        return ""
    last = view.splitlines()[-1] if not view.startswith("[") else view
    # 1. 收到目标（来自人或父机器）→ 造工具，发给自己以获知地址
    if "目标" in view and "#decl" not in view and "fib(" not in view:
        gen = int(re.search(r"第 (\d+) 代", view).group(1)) if "第 " in view else 1
        if gen >= DEPTH:
            return ""                                     # 到深度即停：这一代不再造
        return f">>> {me}\n#decl U\n{FIB_TOOL}\n# gen {gen}"
    # 2. 看到自己写的 #decl U 落带 → 得知工具地址 → 测试
    m = re.search(rf"\[(\S+)\] {re.escape(me)} -> {re.escape(me)}: #decl U", view)
    if m:
        return f">>> {m.group(1)}\nfib 30"
    # 3. 工具回答正确 → 复印自己和工具进下一台机器，带启动消息
    m = re.search(r"\[(\S+)\] (\S+) -> \S+: fib\(30\) = 832040", view)
    if m:
        tool = m.group(2)
        gen = _my_gen(sp, a)
        return (f">>> {me}\n#decl M\npart {me}\npart {tool}\nin {me}\nstart\n"
                f"目标：造一个会算 fib 的工具并测试它，再复印进下一台机器。第 {gen + 1} 代")
    return ""

def _my_gen(sp: K.Space, a: K.Addr) -> int:
    """从带子上自己写的 #decl U 里读出代数（# gen n）。"""
    c = sp.channels[a.ch]
    for m in c.msgs:
        if m.sender == a.id and m.body.startswith("#decl U"):
            g = re.search(r"# gen (\d+)", m.body)
            return int(g.group(1)) if g else 1
    return 1

def run_e1(dir: Path) -> K.Space:
    sp = K.genesis(dir)
    human = K.append(sp, "c0", "door", (), "#admit human").addr()
    ctor = K.append(sp, "c0", "door", (), f"#decl L\n{CONSTRUCTOR_PREFIX}").addr()
    said = [False]
    def X(sp, a, view):                                      # 人：只说一句，之后无话
        if said[0]: return ""
        said[0] = True
        return f">>> {ctor}\n目标：造一个会算 fib 的工具并测试它，再复印进下一台机器。第 1 代"
    K.run(sp, {"L": constructor, "U": K.U, "H": K.H, "X": X})
    return sp

def verify(sp: K.Space) -> list[str]:
    report = []
    chans = list(sp.channels.values())
    report.append(f"机器数 = {len(chans)}：{[c.id for c in chans]}")
    # 条件 4：可遗传——每台子机器的配方含构造者（root L）与工具（root U），且有启动消息
    for c in chans[1:]:
        roots = [a.root for a in c.book.values() if a.id not in K.ROOTS and a.root != "P"]
        start = [m for m in c.msgs if m.sender == f"P:{c.id}"]
        report.append(f"{c.id}: parent={c.parent} parts={roots} 启动消息={len(start)} 接待员={c.receptionist}")
    # 条件 1：每台机器里工具通过测试（fib(30) = 832040 出现在该机器带上）
    for c in chans:
        ok = any("fib(30) = 832040" in m.body and m.sender != "K" for m in c.msgs)
        report.append(f"{c.id}: 工具通过测试 = {ok}")
    # 条件 2：构造非复制——产生工具 #decl 的那一步，其 view 上界 < 该 decl 的 seq
    for c in chans:
        for m in c.msgs:
            if m.body.startswith("#decl U") and m.sender != "door":
                steps = [s for s in c.msgs if s.sender == "K" and f"actor={m.sender} " in s.body and s.seq < m.seq]
                upto = int(re.search(r"upto=(\d+)", steps[-1].body).group(1)) if steps else -1
                report.append(f"{c.id}: 工具 {m.addr()} 由步 upto={upto} 产生，{'不含自身 ✓' if upto < m.seq else '✗'}")
    # 两代之间没有人：c0 之外无 X 成员，且 c0 里人只说了一句
    humans = [m for c in chans for m in c.msgs if m.sender in c.book and c.book[m.sender].root == "X"]
    report.append(f"human 消息总数 = {len(humans)}（全部在 {sorted({m.ch for m in humans})}）")
    return report

if __name__ == "__main__":
    d = Path(sys.argv[1] if len(sys.argv) > 1 else "/tmp/dalek-e1")
    import shutil; shutil.rmtree(d, ignore_errors=True)
    sp = run_e1(d)
    print(K.show(sp)); print("---")
    print("\n".join(verify(sp))); print("---")
    print("replay:", "identical" if K.replay(d) else "DIVERGED")
