"""M1 · c0 + R。跑法：python3 t/test_c0.py

T1 转移表：program 行——视图、step 记录、拆消息；出生证明门给外来消息署名
T2 syscall（本地）：channel.create / channel.add.actor 带完整 text 记一行；返回；无绑定则忽略
T3 门：两扇门互指；抄到对面账本，署名对面的门，收件人是接待员；回信原路回来
T4 根门：R 起来零 channel；膜外经根门造全部（by=_root）；构造期间机械不动；start 关门；关门后根门无效
T5 请求 add / peer（本地生长）；placed 回执转发给请求者
T6 内容盲：R 源码不含组织词汇；G 全部改名后账本同构
T7 生子：C pack + spawn；父代的 A 经门造子代全部；C 发 start 关门；父代对象销毁后子代活着且已切离
"""
from __future__ import annotations
import json, re, sys, tempfile, time, traceback
from pathlib import Path

ROOT = Path(__file__).resolve().parents[1]
sys.path.insert(0, str(ROOT))
from omega import Exec, Store                       # noqa: E402
from runtime import Runtime                         # noqa: E402
from init import boot, root as root_line            # noqa: E402
from genesis import G0, pack, construct, start      # noqa: E402

ECHO = ('import sys, json\nv = json.load(sys.stdin)\n'
        'print("\\n".join(">>> %s\\necho:%s" % (m["from"], m["body"]) for m in v["msgs"]))')
PING = ('import sys, json\nv = json.load(sys.stdin)\n'
        'print("\\n".join(">>> 2\\nping" for m in v["msgs"] if m["body"] == "hi"))')
PLACER = ('import sys, json\nv = json.load(sys.stdin)\n'
          'for m in v["msgs"]:\n'
          '    if not m["from"].startswith("channel."): print(">>> channel.create y\\n>>> channel.add.actor y program in\\nprint(1)")')
ASKER = ('import sys, json\nv = json.load(sys.stdin)\n'
         'for m in v["msgs"]:\n'
         '    b = m["body"]\n'
         '    if b == "go": print(">>> 1\\nadd y program in\\n" + %r + "\\n>>> 1\\npeer c0 y")\n'
         '    else: print(">>> 4\\nrelay:" + b)') % ECHO


def G_of(channels, peers=()):
    return {"world": G0()["world"], "channels": channels, "peers": list(peers)}


def fresh(G: dict, creator="human"):
    P = pack(G, Path(tempfile.mkdtemp(prefix="dalek-")))
    rt = boot(P)
    assert rt.channels == {}                                   # R 起来：零 channel，零 actor
    construct(P, G, creator); rt.run()
    return rt, P


def rows(rt: Runtime, ch: str, k: str):
    return [r for r in rt.channels[ch].rows if r["k"] == k]


def test_T1_transition_program():
    G = G_of([{"name": "a", "members": [{"kind": "program", "text": PING}, {"kind": "program", "text": ECHO}]}])
    rt, P = fresh(G)
    assert rows(rt, "a", "step") == [] and rows(rt, "a", "msg") == []     # 构造期间机械不动
    start(P, G, "hi"); rt.run()
    m = rows(rt, "a", "msg")
    assert m[0]["from"] == "3" and m[0]["to"] == "1" and m[0]["body"] == "hi"    # 署名出生证明门（a/3 → human）
    assert (m[1]["from"], m[1]["to"], m[1]["body"]) == ("1", "2", "ping")
    assert (m[2]["from"], m[2]["to"], m[2]["body"]) == ("2", "1", "echo:ping")
    st = rows(rt, "a", "step")
    assert st[0]["actor"] == "1" and st[0]["upto"] == m[0]["seq"] and st[0]["out"].startswith(">>> 2")
    assert rt.channels["a"].cursor["1"] == m[2]["seq"]


def test_T2_local_syscall():
    G = G_of([{"name": "a", "members": [{"kind": "program", "text": PLACER, "bind": ["syscall"]}]}])
    rt, P = fresh(G); start(P, G, "go"); rt.run()
    y = rows(rt, "y", "place")
    assert y[0]["text"] == "print(1)" and y[0]["in"] and y[0]["addr"] == "1" and y[0]["by"] == "1"
    ret = [x for x in rows(rt, "a", "msg") if x["from"].startswith("channel.")]
    assert [x["body"] for x in ret] == ["y", "y/1"]
    G2 = G_of([{"name": "a", "members": [{"kind": "program", "text": PLACER}]}])   # 无绑定
    rt2, P2 = fresh(G2); start(P2, G2, "go"); rt2.run()
    assert "y" not in rt2.channels and ">>> channel.create" in rows(rt2, "a", "step")[0]["out"]


def test_T3_doors():
    G = G_of([{"name": "a", "members": [{"kind": "program", "text": PING}]},
              {"name": "b", "members": [{"kind": "program", "text": ECHO}]}], peers=[["a", "b"]])
    rt, P = fresh(G); start(P, G, "hi"); rt.run()
    # a: 1 PING, 2 门→b, 3 出生证明；b: 1 ECHO, 2 门→a
    b = rows(rt, "b", "msg")
    assert (b[0]["from"], b[0]["to"], b[0]["body"]) == ("2", "1", "ping")       # 署名 b 里指回 a 的门
    assert (b[1]["from"], b[1]["to"], b[1]["body"]) == ("1", "2", "echo:ping")
    a = rows(rt, "a", "msg")
    assert (a[-1]["from"], a[-1]["to"], a[-1]["body"]) == ("2", "1", "echo:ping")


def G_realize():
    G = G0()
    G["channels"].append({"name": "x", "members": [{"kind": "program", "text": ECHO}], "receptionist": 1})
    G["peers"] = [["c0", "x"]]
    return G


def test_T4_root_door():
    G = G_realize()
    rt, P = fresh(G)
    c0, x = rt.channels["c0"], rt.channels["x"]
    assert [a.kind for a in c0.actors.values()] == ["program", "program", "door", "door"]
    assert c0.actors["1"].bind == ("syscall",) and c0.actors["2"].bind == ("syscall", "spawn")
    assert c0.actors["3"].text == "x" and c0.actors["4"].text == "human" and c0.receptionist == "1"
    assert x.actors["1"].text == ECHO and x.actors["2"].text == "c0"
    assert all(r["by"] == "_root" for r in rows(rt, "c0", "place") + rows(rt, "x", "place"))
    assert rt.root_open and not rows(rt, "c0", "msg") and not rows(rt, "c0", "step")     # 门开着，机器不动
    start(P, G); rt.run()
    first = rows(rt, "c0", "msg")[0]
    assert (first["from"], first["to"], first["body"]) == ("4", "1", "start") and not rt.root_open   # 关门
    root_line(P, "channel.add.actor c0 program\nprint(0)"); rt.run()
    assert len(c0.actors) == 4                                                   # 切离：根门无效
    rt.msg("c0", "1", "3", "hello"); rt.run()
    assert any(m["from"] == "3" and m["body"] == "echo:hello" for m in rows(rt, "c0", "msg"))


def test_T5_requests_add_peer():
    G = G0(); G["channels"][0]["members"].append({"kind": "program", "text": ASKER})   # c0/3；出生证明门 c0/4
    rt, P = fresh(G); start(P, G); rt.run()
    rt.msg("c0", "door", "3", "go"); rt.run()
    y = rt.channels["y"]
    assert y.actors["1"].text == ECHO and y.receptionist == "1" and y.actors["2"].text == "c0"
    assert rt.channels["c0"].actors["5"].text == "y" and rows(rt, "y", "place")[0]["by"] == "1"
    relayed = [m["body"] for m in rows(rt, "c0", "msg") if m["from"] == "3" and m["to"] == "4" and m["body"].startswith("relay:")]
    assert any("placed y/1" in b for b in relayed) and any("placed y/2" in b for b in relayed), relayed


def test_T6_content_blind():
    src = (ROOT / "runtime.py").read_text(encoding="utf-8")
    for w in ("c0", "c1", "c2", "realize", "pack", "decl", "registry", "G.json", "构造", "登记", "creator", "human"):
        assert w not in src, w
    def run(names):
        s = json.dumps(G_realize(), ensure_ascii=False)
        s = re.sub(r"\bc0\b", names[0], s); s = re.sub(r"\bx\b", names[1], s)
        G = json.loads(s); rt, P = fresh(G); start(P, G); rt.run()
        return {c.name: [{k: v for k, v in r.items() if k not in ("out", "err")} for r in c.rows] for c in rt.channels.values()}
    def norm(d, n):
        s = json.dumps(d, ensure_ascii=False, sort_keys=True)
        s = re.sub(r"\b%s\b" % re.escape(n[0]), "A", s)
        return re.sub(r"\b%s\b" % re.escape(n[1]), "B", s)
    assert norm(run(("c0", "x")), ("c0", "x")) == norm(run(("q7", "zz")), ("q7", "zz"))


def test_T7_spawn_child_built_by_parent_A():
    G = G0(); G["channels"][0]["members"].append(
        {"kind": "program", "text": 'import sys, json\nv = json.load(sys.stdin)\nfor m in v["msgs"]:\n'
                                    '    if m["body"] == "go": print(">>> 2\\nspawn d1")\n'
                                    '    else: print(">>> 4\\nreply:" + m["body"])'})
    rt, P = fresh(G); start(P, G); rt.run()
    rt.msg("c0", "door", "3", "go"); rt.run()
    d = P / "spawn" / "d1"
    assert (d / "G.json").read_text(encoding="utf-8") == (P / "G.json").read_text(encoding="utf-8")   # B：G 原样
    assert all((d / f).exists() for f in ("omega.py", "runtime.py", "init.py"))                       # B：world
    pid = int([m for m in rows(rt, "c0", "msg") if m["from"] == "spawn"][0]["body"].split("pid=")[1])
    try:
        c0 = rt.channels["c0"]
        rdoor, cdoor = c0.door_to(f"file:{d}#_root"), c0.door_to(f"file:{d}#c0")
        sent = [m["body"] for m in rows(rt, "c0", "msg") if m["to"] == rdoor]                        # 父代 A 发出的 syscall
        assert sent[0] == "channel.create c0" and sum(b.startswith("channel.add.actor") for b in sent) == 4
        assert sent[-1] == "msg c0\nstart" and sent[-2].startswith("channel.add.actor c0 door\nfile:")   # 出生证明，然后 start
        assert any(m["to"] == "3" and m["body"].startswith("spawned") for m in rows(rt, "c0", "msg"))
        deadline = time.time() + 15
        while time.time() < deadline:
            child = Runtime(d).load()
            if "c0" in child.channels and any(r["k"] == "msg" for r in child.channels["c0"].rows):
                break
            time.sleep(0.2)
        else:
            raise AssertionError("child not started: " + Store.read(d / "init.log")[-800:])
        del rt                                                                                        # 父代死了
        cc = child.channels["c0"]
        assert [a.kind for a in cc.actors.values()] == ["program", "program", "program", "door"]   # realize, C, 请求者, 出生证明
        assert all(r["by"] == "_root" for r in rows(child, "c0", "place"))                            # 全部由膜外（父代）造
        assert cc.actors["1"].text == G["channels"][0]["members"][0]["text"] and cc.actors["2"].bind == ("syscall", "spawn")
        assert cc.actors["4"].text == f"file:{P}#c0"                                                  # 出生证明指回父代
        m0 = rows(child, "c0", "msg")[0]
        assert (m0["from"], m0["to"], m0["body"]) == ("4", "1", "start") and not child.root_open       # 关门 = 切离
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
