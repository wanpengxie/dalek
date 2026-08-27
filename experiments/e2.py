"""E2：Dalek Core 造 Dalek Core′，父验子。

c0 里只有人和一个 U 工具（spawner）。人说一句 "spawn"。之后：
  spawner → 问 H 根要 c0/1（genesis，里面是 K 的源码）
  H → 把那条消息投影给 spawner
  spawner → 把源码写到一个新目录，起一个新的 python 进程：用这份源码 genesis 一台新机器、跑一段、自我 replay
          → 把子进程的报告（replay 是否 identical、账本长度、K 源码的 sha）发回给人
验证器（读带子）：子机器 replay identical；子机器 genesis 里的 K sha == 父机器 genesis 里的 K sha（K diff = 0）。
两代之间没有人：人只说了 "spawn"。
"""
from __future__ import annotations
import hashlib, re, sys
from pathlib import Path
sys.path.insert(0, str(Path(__file__).resolve().parents[1]))
import kernel as K

CHILD_DRIVER = r'''
import sys, hashlib
from pathlib import Path
sys.path.insert(0, sys.argv[1])
import kernel as K
d = Path(sys.argv[1]) / "h"
sp = K.genesis(d)
human = K.append(sp, "c0", "door", (), "#admit human").addr()
t = K.tape([(human, ">>> H\nbook"), (human, "")])
K.run(sp, {"L": t, "U": K.U, "H": K.H, "X": t})
ok = K.replay(d)
ksrc = sp.ledger[0].body.split("K=\n", 1)[1]
print("REPLAY", ok, "LEDGER", len(sp.ledger), "KSHA", hashlib.sha256(ksrc.encode()).hexdigest()[:16])
'''

SPAWNER = r'''
import sys, re, subprocess, tempfile, hashlib
from pathlib import Path
v = sys.stdin.read()
lines = v.splitlines()
k = next((i for i, l in enumerate(lines) if l.strip() == "K="), None)
if k is None:
    who = re.search(r"\] (\S+) ->", v).group(1)
    print(">>> H"); print("msg c0/1 reply-to " + who)       # 第一步：向带子要 K 的源码，带上回信人
else:
    who = re.search(r"reply-to (\S+)", v).group(1)
    indent = len(lines[k]) - len(lines[k].lstrip())
    src = "\n".join(l[indent:] for l in lines[k + 1:]).rstrip("\n") + "\n"
    tmp = Path(tempfile.mkdtemp(prefix="dalek-e2-child-"))
    (tmp / "kernel.py").write_text(src, encoding="utf-8")
    (tmp / "driver.py").write_text(''' + repr(CHILD_DRIVER) + r''', encoding="utf-8")
    r = subprocess.run([sys.executable, str(tmp / "driver.py"), str(tmp)], capture_output=True, text=True, timeout=50)
    print(">>> " + who)
    print("child:", (r.stdout.strip() or r.stderr.strip()[-300:]))
    print("sent-KSHA", hashlib.sha256(src.encode()).hexdigest()[:16])
'''

def run_e2(dir: Path) -> K.Space:
    sp = K.genesis(dir)
    human = K.append(sp, "c0", "door", (), "#admit human").addr()
    spawner = K.append(sp, "c0", "door", (), f"#decl U\n{SPAWNER}").addr()
    t = K.tape([(human, f">>> {spawner}\nspawn"), (human, "")])
    K.run(sp, {"L": t, "U": K.U, "H": K.H, "X": t})
    return sp

def verify(sp: K.Space) -> list[str]:
    rep = []
    parent_sha = hashlib.sha256(sp.ledger[0].body.split("K=\n", 1)[1].encode()).hexdigest()[:16]
    report = next((m.body for m in sp.ledger if m.body.startswith("child:")), "")
    rep.append(f"父 K sha = {parent_sha}")
    rep.append(f"子进程报告 = {report.splitlines()[0] if report else '（无）'}")
    m = re.search(r"REPLAY (\w+) LEDGER (\d+) KSHA (\w+)", report)
    if m:
        rep.append(f"子机器 replay = {m.group(1)}；子账本 {m.group(2)} 条；子 K sha = {m.group(3)}")
        rep.append(f"K diff = 0：{'✓' if m.group(3) == parent_sha else '✗'}")
    humans = [x for x in sp.ledger if x.sender in sp.channels['c0'].book and sp.channels['c0'].book[x.sender].root == 'X']
    rep.append(f"human 消息 = {[x.body for x in humans]}")
    return rep

if __name__ == "__main__":
    d = Path(sys.argv[1] if len(sys.argv) > 1 else "/tmp/dalek-e2")
    import shutil; shutil.rmtree(d, ignore_errors=True)
    sp = run_e2(d)
    print(K.show(sp)); print("---")
    print("\n".join(verify(sp))); print("---")
    print("parent replay:", "identical" if K.replay(d) else "DIVERGED")
