"""M1 · c0 + 运行时。跑法：python3 t/test_c0.py

T1 转移表：program 行——视图、step 记录、拆消息
T2 放 actor：介质动作带完整 text 记一行；返回 from=place；无绑定则忽略
T3 门：两扇门互指；抄到对面账本，署名对面的门，收件人是接待员；回信原路回来
T4 init 只放第一个 actor；根门踢一脚；realize 照 G 长出其余器官
T5 syscall add / peer；placed 回执转发给请求者
T6 内容盲：运行时源码不含组织词汇；G 全部改名后账本逐字节同构
T7 spawn：C actor pack + Exec.spawn + 踢一脚；子代在独立进程里自发育；父代杀掉后子代仍活着
"""
from __future__ import annotations
import json, os, shutil, sys, tempfile, time, traceback
from pathlib import Path

ROOT = Path(__file__).resolve().parents[1]
sys.path.insert(0, str(ROOT))
from omega import Exec, Store          # noqa: E402
from runtime import Runtime, parse     # noqa: E402
from init import boot, kick            # noqa: E402
from genesis import G0                 # noqa: E402

ECHO = ('import sys, json\nv = json.load(sys.stdin)\n'
        'print("\\n".join(">>> %s\\necho:%s" % (m["from"], m["body"]) for m in v["msgs"]))')
PING = ('import sys, json\nv = json.load(sys.stdin)\n'
        'print("\\n".join(">>> 2\\nping" for m in v["msgs"] if m["body"] == "hi"))')
PLACER = ('import sys, json\nv = json.load(sys.stdin)\n'
          'for m in v["msgs"]:\n'
          '    if m["from"] != "place": print(">>> place y program in\\nprint(1)")')
ASKER = ('import sys, json\nv = json.load(sys.stdin)\n'
         'for m in v["msgs"]:\n'
         '    b = m["body"]\n'
         '    if b == "go": print(">>> 1\\nadd y program in\\n" + %r + "\\n>>> 1\\npeer c0 y")\n'
         '    else: print(">>> 3\\nrelay:" + b)') % ECHO


def fresh(G: dict) -> tuple[Runtime, Path]:
    P = Path(tempfile.mkdtemp(prefix="dalek-"))
    for f in ("omega.py", "runtime.py", "init.py"):
        shutil.copyfile(ROOT / f, P / f)
    (P / "G.json").write_text(json.dumps(G, ensure_ascii=False), encoding="utf-8")
    return boot(P), P


def rows(rt: Runtime, ch: str, k: str) -> list[dict]:
    return [r for r in rt.channels[ch].rows if r["k"] == k]


def test_T1_transition_program():
    rt, P = fresh({"channels": [{"name": "a", "members": [{"kind": "program", "text": PING}]}]})
    rt.place("a", "program", ECHO)
    kick(P, "hi"); rt.run()
    a = rt.channels["a"]
    m = rows(rt, "a", "msg")
    assert m[0]["from"] == "door" and m[0]["to"] == "1" and m[0]["body"] == "hi"
    assert m[1] == {"seq": m[1]["seq"], "k": "msg", "from": "1", "to": "2", "body": "ping"}
    assert m[2]["from"] == "2" and m[2]["to"] == "1" and m[2]["body"] == "echo:ping"
    st = rows(rt, "a", "step")
    assert st[0]["actor"] == "1" and st[0]["upto"] == m[0]["seq"] and st[0]["out"].startswith(">>> 2")
    assert a.cursor["1"] == m[2]["seq"]                       # 1 看过 echo 回来的那条


def test_T2_place_action():
    rt, P = fresh({"channels": [{"name": "a", "members": [{"kind": "program", "text": PLACER, "bind": ["place"]}]}]})
    kick(P, "go"); rt.run()
    y = rows(rt, "y", "place")
    assert y[0]["text"] == "print(1)" and y[0]["kind"] == "program" and y[0]["in"] and y[0]["addr"] == "1"
    assert rt.channels["y"].receptionist == "1"
    ret = [m for m in rows(rt, "a", "msg") if m["from"] == "place"]
    assert ret and ret[0]["to"] == "1" and ret[0]["body"] == "y/1"
    # 无绑定：同样的输出只留在 step.out 里，不放
    rt2, P2 = fresh({"channels": [{"name": "a", "members": [{"kind": "program", "text": PLACER}]}]})
    kick(P2, "go"); rt2.run()
    assert "y" not in rt2.channels and ">>> place" in rows(rt2, "a", "step")[0]["out"]


def test_T3_doors():
    rt, P = fresh({"channels": [{"name": "a", "members": [{"kind": "program", "text": PING}]}]})
    rt.place("a", "door", "b")                    # a/2 → b
    rt.place("b", "program", ECHO)                # b/1 接待员
    rt.place("b", "door", "a")                    # b/2 → a
    kick(P, "hi"); rt.run()                       # a/1 收到 hi → ping 给 2（门）→ 抄到 b
    b = rows(rt, "b", "msg")
    assert b[0] == {"seq": 1 + 2, "k": "msg", "from": "2", "to": "1", "body": "ping"}   # 署名 b 里指回 a 的门
    assert b[1]["from"] == "1" and b[1]["to"] == "2" and b[1]["body"] == "echo:ping"   # echo 回给门
    a = rows(rt, "a", "msg")
    assert a[-1]["from"] == "2" and a[-1]["to"] == "1" and a[-1]["body"] == "echo:ping"  # 原路回到 a 的接待员


def G_realize() -> dict:
    G = G0()
    G["channels"].append({"name": "x", "members": [{"kind": "program", "text": ECHO}], "receptionist": 1})
    G["peers"] = [["c0", "x"]]
    return G


def test_T4_init_kick_realize():
    rt, P = fresh(G_realize())
    assert list(rt.channels) == ["c0"] and list(rt.channels["c0"].actors) == ["1"]     # init 只放第一个
    assert rows(rt, "c0", "msg") == []                                                 # init 不发消息
    kick(P, "realize G.json"); rt.run()
    c0, x = rt.channels["c0"], rt.channels["x"]
    assert [a.kind for a in c0.actors.values()] == ["program", "program", "door", "door"]
    assert c0.actors["3"].text == "creator" and c0.actors["4"].text == "x"
    assert x.actors["1"].text == ECHO and x.receptionist == "1" and x.actors["2"].text == "c0"
    assert c0.actors["2"].bind == ("place", "spawn")
    pl = rows(rt, "x", "place")
    assert pl[0]["text"] == ECHO                                                      # 放 actor 带完整 text
    first = rows(rt, "c0", "msg")[0]
    assert first["from"] == "door" and first["to"] == "1" and first["body"] == "realize G.json"   # 根门那一脚
    # 经 c0 的门给 x 说话，echo 原路回来
    rt.msg("c0", "1", "4", "hello"); rt.run()
    assert any(m["from"] == "4" and m["body"] == "echo:hello" for m in rows(rt, "c0", "msg"))


def test_T5_syscalls_add_peer():
    G = G0(); G["channels"][0]["members"].append({"kind": "program", "text": ASKER})   # c0/4 请求者
    rt, P = fresh(G)
    kick(P, "realize G.json"); rt.run()
    rt.msg("c0", "door", "4", "go"); rt.run()
    y = rt.channels["y"]
    assert y.actors["1"].text == ECHO and y.receptionist == "1" and y.actors["2"].text == "c0"
    assert rt.channels["c0"].actors["5"].text == "y"
    relayed = [m["body"] for m in rows(rt, "c0", "msg") if m["from"] == "4" and m["to"] == "3" and m["body"].startswith("relay:")]
    assert any("placed y/1" in b for b in relayed) and any("placed y/2" in b for b in relayed), relayed


def test_T6_content_blind():
    src = (ROOT / "runtime.py").read_text(encoding="utf-8")
    for w in ("c0", "c1", "c2", "realize", "pack", "decl", "registry", "G.json", "构造", "登记"):
        assert w not in src, w
    # 全部改名后账本同构
    import re
    def run(names):
        s = json.dumps(G_realize(), ensure_ascii=False)
        s = re.sub(r"\bc0\b", names[0], s); s = re.sub(r"\bx\b", names[1], s)   # 结构和 text 里的名字一起换
        G = json.loads(s)
        rt, P = fresh(G); kick(P, "realize G.json"); rt.run()
        return {c.name: [{k: v for k, v in r.items() if k not in ("out", "err")} for r in c.rows] for c in rt.channels.values()}
    a, b = run(("c0", "x")), run(("q7", "zz"))
    def norm(d, n):
        s = json.dumps(d, ensure_ascii=False, sort_keys=True)
        s = re.sub(r"\b%s\b" % re.escape(n[0]), "A", s)
        return re.sub(r"\b%s\b" % re.escape(n[1]), "B", s)
    assert norm(a, ("c0", "x")) == norm(b, ("q7", "zz"))


def test_T7_spawn_child_independent():
    G = G0(); G["channels"][0]["members"].append(
        {"kind": "program", "text": 'import sys, json\nv = json.load(sys.stdin)\nfor m in v["msgs"]:\n'
                                    '    if m["body"] == "go": print(">>> 2\\nspawn d1")\n'
                                    '    else: print(">>> 3\\nreply:" + m["body"])'})
    rt, P = fresh(G)
    kick(P, "realize G.json"); rt.run()
    rt.msg("c0", "door", "4", "go"); rt.run()
    d = P / "spawn" / "d1"
    assert all((d / f).exists() for f in ("omega.py", "runtime.py", "init.py", "G.json"))
    assert (d / "G.json").read_bytes() == (P / "G.json").read_bytes()                  # G 原样
    sp = [m for m in rows(rt, "c0", "msg") if m["from"] == "spawn"]
    pid = int(sp[0]["body"].split("pid=")[1])
    try:
        door = rt.channels["c0"].door_to(f"file:{d}#c0")
        assert door and any(m["to"] == door and m["body"] == "realize G.json" for m in rows(rt, "c0", "msg"))
        assert any("spawned" in m["body"] and m["to"] == "4" and m["from"] == "2" for m in rows(rt, "c0", "msg"))
        deadline = time.time() + 15
        while time.time() < deadline:                                                    # 子代在自己的进程里长
            child = Runtime(d).load()
            if "c0" in child.channels and len(child.channels["c0"].actors) >= 4:
                break
            time.sleep(0.2)
        else:
            raise AssertionError("child did not realize: " + Store.read(d / "init.log")[-800:])
        del rt                                                                           # 父代死了
        assert any(m["from"] == "door" and m["body"] == "realize G.json" for m in rows(child, "c0", "msg"))
        assert child.channels["c0"].actors["2"].bind == ("place", "spawn")
    finally:
        Exec.stop(pid)


if __name__ == "__main__":
    ok = 0; names = sorted(n for n in dir() if n.startswith("test_"))
    for name in names:
        try:
            globals()[name](); ok += 1; print("PASS", name)
        except Exception:
            print("FAIL", name); traceback.print_exc(limit=3)
    print(f"{ok}/{len(names)}")
