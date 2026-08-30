"""M1 · c0 + R；M2 · c1 + decl + retire。跑法：python3 t/test_c0.py

T0 本目录 = dalek0 的 P：G.json 的 world 与磁盘文件逐字节相等；c0 成员 text 与 actors/*.py 相等
T1 转移表：program 行——视图、step 记录、拆消息；出生证明门给外来消息署名
T2 syscall（本地）：channel.create / channel.add.actor 带完整 text 记一行；返回；无绑定则忽略
T3 门：c0 发育出 a、b 和两扇互指的门；消息抄到对面账本，署名对面的门，收件人是接待员；回信原路回来
T4 根门：R 起来零 channel；膜外经根门只造 c0（by=_root）；构造期间机械不动；start\n<G> 关门；c0 自己长出其余（by=1）；关门后根门无效
T5 请求 add / peer（本地生长）；placed 回执转发给请求者
T6 内容盲：R 源码不含组织词汇；G 全部改名后账本同构
T7 生子：C 经接待员向登记员要 decl 后 pack + spawn；父代的 A 经门只造子代的 c0；C 发 start\n<G> 关门；子代的 c0 自己长出 c1、x；父代对象销毁后子代活着且已切离
T8 登记：出生后 decl(dalek0) == G0；c1 账本第一行是 born；channels 顺序 == _order
T9 遗传运行中的改动：add + peer 后经接待员 spawn（H3）→ 子代有它们；decl(子) == decl(父)
T10 替换 + 非平凡：add 新 realize′(in) + retire 旧 → 旧不再被 step、decl 略过 → 子代的接待员是 realize′
T11 retire 后写给它的消息留账不投递；退役的门不再署名
T12 形态闭包与拒绝；T13 回执稠密（拒绝也占一位）；T14 跨 channel 退役；T15 事实来源不随拓扑漂
"""
from __future__ import annotations
import json, re, sys, tempfile, time, traceback
from pathlib import Path

ROOT = Path(__file__).resolve().parents[1]
sys.path.insert(0, str(ROOT))
from omega import Exec, Store                       # noqa: E402
from runtime import Runtime                         # noqa: E402
from init import up, root as root_line            # noqa: E402
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
    for c in channels: c.setdefault("receptionist", 1)                         # 接待员必须显式
    return {"world": G0()["world"], "channels": channels, "peers": list(peers)}


def expand(G: dict) -> dict:
    """人写的 G（peers 是糖）→ 规范形：门就是成员，peers 空。与 realize 放门的顺序一致。"""
    G = json.loads(json.dumps(G)); by = {c["name"]: c for c in G["channels"]}
    for a, b in G.get("peers", []):
        by[a]["members"].append({"kind": "door", "text": b}); by[b]["members"].append({"kind": "door", "text": a})
    for c in G["channels"]:
        c.pop("receptionist", None) if c.get("receptionist") is None else None
    G["peers"] = []
    return G


def fresh(G: dict, creator="human"):
    P = pack(G, Path(tempfile.mkdtemp(prefix="dalek-")))
    rt = up(P)
    assert rt.channels == {}                                   # R 起来：零 channel，零 actor
    construct(P, G, creator); rt.run()
    return rt, P


def rows(rt: Runtime, ch: str, k: str):
    return [r for r in rt.channels[ch].rows if r["k"] == k]


def decl_of(rt: Runtime, ch="c1") -> dict:
    """膜外问登记员 decl；回答写给 door（无门可回）→ 只留在 step.out 里，从那里读。"""
    r = rt.channels[ch].receptionist
    rt.msg(ch, "door", r, "decl"); rt.run()
    out = rows(rt, ch, "step")[-1]["out"]
    assert out.startswith(">>> door\ndecl\n"), out[:80]
    return json.loads(out.split("\n", 2)[2])


def form_of(rt: Runtime):
    """π(A)：R 的实际形态去掉不经 c0 放的门（出生证明、生子的临时门）与退役，地址重排。"""
    out = []
    for c in rt.channels.values():
        live = [a for a in c.actors.values() if not a.retired and not (a.kind == "door" and (":" in a.text or a.text == "human"))]
        if not live:
            continue
        out.append((c.name, [(a.kind, a.text, tuple(a.bind)) for a in live],
                    next((i + 1 for i, a in enumerate(live) if a.addr == c.receptionist), None)))
    return out


def declared(D: dict):
    return [(c["name"], [(m["kind"], m["text"], tuple(m.get("bind", ()))) for m in c["members"]], c.get("receptionist")) for c in D["channels"]]


def wait_child(d: Path, ready, timeout=20) -> Runtime:
    deadline = time.time() + timeout
    while time.time() < deadline:
        child = Runtime(d).load()
        if ready(child):
            return child
        time.sleep(0.2)
    raise AssertionError("child not ready: " + Store.read(d / "init.log")[-800:])


def test_T0_P_equals_own_G():
    G = json.loads((ROOT / "G.json").read_text(encoding="utf-8"))
    for f, src in G["world"].items():
        assert (ROOT / f).read_bytes() == src.encode("utf-8"), f                 # P.world == G.world
    c0 = G["channels"][0]["members"]
    assert c0[0]["text"] == (ROOT / "actors" / "realize.py").read_text(encoding="utf-8")
    assert c0[1]["text"] == (ROOT / "actors" / "spawn.py").read_text(encoding="utf-8")
    assert G["channels"][1]["members"][0]["text"] == (ROOT / "actors" / "registrar.py").read_text(encoding="utf-8")
    assert G == G0()                                                             # genesis 重生成后不变


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
    assert [x["body"] for x in ret] == ["y new", "y/1"]
    G2 = G_of([{"name": "a", "members": [{"kind": "program", "text": PLACER}]}])   # 无绑定
    rt2, P2 = fresh(G2); start(P2, G2, "go"); rt2.run()
    assert "y" not in rt2.channels and ">>> channel.create" in rows(rt2, "a", "step")[0]["out"]


def test_T3_doors():
    G = G0()
    G["channels"] += [{"name": "a", "members": [{"kind": "program", "text": PING}], "receptionist": 1},
                      {"name": "b", "members": [{"kind": "program", "text": ECHO}], "receptionist": 1}]
    G["peers"] = [["a", "b"]]
    rt, P = fresh(G); start(P, G); rt.run()
    # c0 长出来的：a: 1 PING, 2 门→b；b: 1 ECHO, 2 门→a
    assert all(r["by"] == "1" for r in rows(rt, "a", "place") + rows(rt, "b", "place"))
    rt.msg("a", "door", "1", "hi"); rt.run()
    b = rows(rt, "b", "msg")
    assert (b[0]["from"], b[0]["to"], b[0]["body"]) == ("2", "1", "ping")       # 署名 b 里指回 a 的门
    assert (b[1]["from"], b[1]["to"], b[1]["body"]) == ("1", "2", "echo:ping")
    a = rows(rt, "a", "msg")
    assert (a[-1]["from"], a[-1]["to"], a[-1]["body"]) == ("2", "1", "echo:ping")


def G_realize():
    G = G0()
    G["channels"].append({"name": "x", "members": [{"kind": "program", "text": ECHO}], "receptionist": 1})
    G["peers"].append(["c0", "x"])
    return G


def test_T4_root_door():
    G = G_realize()
    rt, P = fresh(G)
    c0 = rt.channels["c0"]
    assert list(rt.channels) == ["c0"]                                           # 父代只造 c0
    assert [a.kind for a in c0.actors.values()] == ["program", "program", "door"]
    assert c0.actors["1"].bind == ("syscall",) and c0.actors["2"].bind == ("syscall", "spawn")
    assert c0.actors["3"].text == "human" and c0.receptionist == "1"
    assert all(r["by"] == "_root" for r in rows(rt, "c0", "place"))
    assert rt.root_open and not rows(rt, "c0", "msg") and not rows(rt, "c0", "step")     # 门开着，机器不动
    start(P, G); rt.run()
    first = rows(rt, "c0", "msg")[0]
    assert (first["from"], first["to"]) == ("3", "1") and first["body"].startswith("start\n") and not rt.root_open   # 关门
    x = rt.channels["x"]                                                         # c0 自己长出来的：c1、x、门
    assert x.actors["1"].text == ECHO and x.actors["2"].text == "c0"
    assert c0.actors["4"].text == "c1" and c0.actors["5"].text == "x" and list(rt.channels) == ["c0", "c1", "x"]
    assert all(r["by"] == "1" for r in rows(rt, "x", "place") + rows(rt, "c1", "place")) and rows(rt, "c0", "place")[-1]["by"] == "1"
    root_line(P, "channel.add.actor c0 program\nprint(0)"); rt.run()
    assert len(c0.actors) == 5                                                   # 切离：根门无效
    rt.msg("c0", "1", "5", "hello"); rt.run()
    assert any(m["from"] == "5" and m["body"] == "echo:hello" for m in rows(rt, "c0", "msg"))


def test_T5_requests_add_peer():
    G = G0(); G["channels"][0]["members"].append({"kind": "program", "text": ASKER})   # c0/3；出生证明门 c0/4
    rt, P = fresh(G); start(P, G); rt.run()
    rt.msg("c0", "door", "3", "go"); rt.run()
    y = rt.channels["y"]
    assert y.actors["1"].text == ECHO and y.receptionist == "1" and y.actors["2"].text == "c0"
    assert rt.channels["c0"].actors["6"].text == "y" and rows(rt, "y", "place")[0]["by"] == "1"
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
        return [[{k: v for k, v in r.items() if k not in ("out", "err")} for r in c.rows] for c in rt.channels.values()]   # 按创建顺序，不按名字
    def norm(d, n):
        s = json.dumps(d, ensure_ascii=False, sort_keys=True).replace("\\n", " ")     # 嵌套 JSON 的 \n 转义会吃掉词边界
        s = re.sub(r"\b%s\b" % re.escape(n[0]), "A", s)
        return re.sub(r"\b%s\b" % re.escape(n[1]), "B", s)
    assert norm(run(("c0", "x")), ("c0", "x")) == norm(run(("q7", "zz")), ("q7", "zz"))


def test_T7_spawn_child_built_by_parent_A():
    G = G0(); G["channels"][0]["members"].append(
        {"kind": "program", "text": 'import sys, json\nv = json.load(sys.stdin)\nfor m in v["msgs"]:\n'
                                    '    if m["body"] == "go": print(">>> 2\\nspawn d1")\n'
                                    '    else: print(">>> 4\\nreply:" + m["body"])'})
    G["channels"].append({"name": "x", "members": [{"kind": "program", "text": ECHO}], "receptionist": 1}); G["peers"].append(["c0", "x"])
    rt, P = fresh(G); start(P, G); rt.run()
    rt.msg("c0", "door", "3", "go"); rt.run()
    d = P / "spawn" / "d1"
    assert json.loads((d / "G.json").read_text(encoding="utf-8")) == decl_of(rt)                     # B：抄的是 decl
    assert all((d / f).exists() for f in ("omega.py", "runtime.py", "init.py"))                       # B：world
    pid = int([m for m in rows(rt, "c0", "msg") if m["from"] == "spawn"][0]["body"].split("pid=")[1])
    try:
        c0 = rt.channels["c0"]
        rdoor, cdoor = rt._door(c0, f"file:{d}#_root"), rt._door(c0, f"file:{d}#c0")
        sent = [m["body"] for m in rows(rt, "c0", "msg") if m["to"] == rdoor]                        # 父代 A 发出的 syscall
        assert sent[0] == "channel.create c0" and sum(b.startswith("channel.add.actor") for b in sent) == 6   # 3 成员 + 门→c1 + 门→x + 出生证明
        assert sent[-1].startswith("msg c0\nstart\n{") and sent[-2].startswith("channel.add.actor c0 door\nfile:")   # 出生证明，然后 start 带 G
        assert not any("channel.create x" in b for b in sent)                                         # x 不是父代造的
        assert any(m["to"] == "3" and m["body"].startswith("spawned") for m in rows(rt, "c0", "msg"))
        child = wait_child(d, lambda c: "x" in c.channels and rows(c, "x", "place") and len(c.channels["c0"].actors) >= 6
                           and rows(c, "c1", "msg"))
        del rt                                                                                        # 父代死了
        cc = child.channels["c0"]
        assert [a.kind for a in cc.actors.values()] == ["program"] * 3 + ["door"] * 3                # realize, C, 请求者, 门→c1, 门→x, 出生证明
        assert all(r["by"] == "_root" for r in rows(child, "c0", "place"))                            # c0（含它的门）全由膜外（父代）造
        assert cc.actors["1"].text == G["channels"][0]["members"][0]["text"] and cc.actors["2"].bind == ("syscall", "spawn")
        assert cc.actors["6"].text == f"file:{P}#c0"                                                  # 出生证明指回父代，放在最后
        m0 = rows(child, "c0", "msg")[0]
        assert (m0["from"], m0["to"]) == ("6", "1") and m0["body"].startswith("start\n") and not child.root_open   # 关门 = 切离
        assert rows(child, "x", "place")[0]["by"] == "1" and child.channels["x"].actors["2"].text == "c0"   # 子代的 c0 长出 x
        assert cc.actors["4"].text == "c1" and cc.actors["5"].text == "x"
        assert rows(child, "c1", "place")[-1]["by"] == "1" and rows(child, "x", "place")[-1]["by"] == "1"   # 其余是自己长的
        assert rows(child, "c1", "msg")[0]["body"].startswith("born\n")                              # 登记员收到了基因组
    finally:
        Exec.stop(pid)


def test_T8_registrar_decl():
    G = G0()
    rt, P = fresh(G); start(P, G); rt.run()
    born = rows(rt, "c1", "msg")[0]
    assert born["body"].startswith("born\n")
    assert json.loads(born["body"].split("\n", 1)[1]) == {"world": G["world"], "channels": G["channels"][:1], "peers": []}   # 脐带放的
    assert sum(m["body"].startswith("placed ") for m in rows(rt, "c1", "msg")) == 3                      # c1 的登记员 + 两扇门
    D = decl_of(rt)
    assert D == expand(G)                                                         # π(A) ≅ G：规范结构相等
    assert [c["name"] for c in D["channels"]] == Store.read(P / "h" / "_order").split()


def test_T9_inherit_runtime_changes():
    G = G0()
    rt, P = fresh(G); start(P, G); rt.run()
    rt.msg("c0", "door", "1", "add c0 program\n" + ECHO); rt.run()                 # 经接待员本地生长
    rt.msg("c0", "door", "1", "peer c0 y"); rt.run()
    D = decl_of(rt)
    c0m = D["channels"][0]["members"]
    assert [m.get("text") for m in c0m[2:]] == ["c1", ECHO, "y"] and c0m[4]["kind"] == "door"        # 门就是成员，按地址
    assert D["channels"][2] == {"name": "y", "members": [{"kind": "door", "text": "c0"}]}          # 只有门、没有接待员：如实
    assert [c["name"] for c in D["channels"]] == ["c0", "c1", "y"]
    rt.msg("c0", "door", "1", "spawn d2"); rt.run()                                # 外来 spawn 经接待员到 C（H3）
    d = P / "spawn" / "d2"
    pid = int([m for m in rows(rt, "c0", "msg") if m["from"] == "spawn"][0]["body"].split("pid=")[1])
    try:
        assert json.loads((d / "G.json").read_text(encoding="utf-8")) == D                      # pack 用的是 decl，不是 P 里的 G.json
        child = wait_child(d, lambda c: "y" in c.channels and rows(c, "c1", "msg") and len(rows(c, "c0", "msg")) > 1)
        assert child.channels["c0"].actors["4"].text == ECHO and child.channels["y"].actors["1"].text == "c0"   # 遗传了运行中的改动
        assert child.channels["y"].receptionist is None and child.channels["c0"].actors["5"].text == "y"
        time.sleep(1.0)
        assert form_of(Runtime(d).load()) == declared(D)                                                 # π(A_子) ≅ decl(父)
    finally:
        Exec.stop(pid)


def test_T10_replace_realize_is_nontrivial():
    G = G0()
    rt, P = fresh(G); start(P, G); rt.run()
    R2 = G["channels"][0]["members"][0]["text"].replace("# c0 的装配器（A）", "# c0 的装配器（A′）")
    rt.msg("c0", "door", "1", "add c0 program in bind=syscall\n" + R2); rt.run()  # 先放新的（接待员）
    c0 = rt.channels["c0"]
    assert c0.receptionist == "5" and c0.actors["5"].text == R2
    rt.msg("c0", "door", "5", "retire c0/1"); rt.run()                             # 再退旧的
    assert c0.actors["1"].retired and rows(rt, "c0", "retire")[0]["addr"] == "1"
    D = decl_of(rt)
    assert [m["text"][:20] for m in D["channels"][0]["members"]] == [G["channels"][0]["members"][1]["text"][:20], "c1", R2[:20]]
    assert D["channels"][0]["receptionist"] == 3                                   # 地址前移：不遗传
    rt.msg("c0", "door", "5", "spawn d3"); rt.run()
    d = P / "spawn" / "d3"
    pid = int([m for m in rows(rt, "c0", "msg") if m["from"] == "spawn"][0]["body"].split("pid=")[1])
    try:
        child = wait_child(d, lambda c: "c1" in c.channels and rows(c, "c1", "msg"))
        cc = child.channels["c0"]
        assert cc.receptionist == "3" and cc.actors["3"].text == R2 and cc.actors["3"].bind == ("syscall",)   # 子代的 A 是新的
    finally:
        Exec.stop(pid)


def test_T11_retired_not_stepped():
    G = G0(); G["channels"][0]["members"].append({"kind": "program", "text": ECHO})   # c0/3
    rt, P = fresh(G); start(P, G); rt.run()
    rt.msg("c0", "door", "1", "retire c0/3"); rt.run()
    n = len(rows(rt, "c0", "step"))
    rt.msg("c0", "door", "3", "hi"); rt.run()
    assert len(rows(rt, "c0", "step")) == n and not any(s["actor"] == "3" for s in rows(rt, "c0", "step"))   # 留账不投递
    assert any(m["body"].startswith("retired c0/3") for m in rows(rt, "c1", "msg"))                       # 登记员知道了


def test_T12_form_closure_and_refusals():
    G = G0()
    rt, P = fresh(G); start(P, G); rt.run()
    c0 = rt.channels["c0"]
    assert rt._syscall("channel.retire.actor c0/2", "", by="2", by_channel="c0")[1] == "c0/2 refused" and not c0.actors["2"].retired   # 自退役被拒
    assert rt._syscall("channel.retire.actor c0/1", "", by="2", by_channel="c0")[1] == "c0/1 refused" and not c0.actors["1"].retired   # 退役接待员被拒
    rt.msg("c1", "door", "1", "placed z 1 program in\nprint(1)"); rt.run()
    assert "z" not in [c["name"] for c in decl_of(rt)["channels"]]                                    # 不是 c0 说的，不是事实
    rt.msg("c0", "door", "1", "add y program in\n" + ECHO); rt.run()
    rt.msg("c0", "door", "1", "add c0 door\ny"); rt.run()                                              # 单向门
    rt.msg("c0", "door", "1", "peer c0 z"); rt.run()                                                    # 只有门的 channel：没有接待员
    D = decl_of(rt)
    ch = {c["name"]: c for c in D["channels"]}
    assert ch["c0"]["members"][3:] == [{"kind": "door", "text": "y"}, {"kind": "door", "text": "z"}]
    assert ch["y"] == {"name": "y", "members": [{"kind": "program", "text": ECHO}], "receptionist": 1}     # 没有回指的门：如实
    assert ch["z"] == {"name": "z", "members": [{"kind": "door", "text": "c0"}]} and rt.channels["z"].receptionist is None
    assert form_of(rt) == declared(D)                                                                    # π(A) ≅ G


def test_T13_dense_returns():
    """同一步里一条被拒绝的 retire + 一条成功的 add：回执不错位，c1 不会收到假的 retired。"""
    G = G0()
    rt, P = fresh(G); start(P, G); rt.run()
    rt.msg("c0", "door", "1", "retire c0/1")                                        # 退役接待员：拒绝
    rt.msg("c0", "door", "1", "add y program in\n" + ECHO); rt.run()                 # 同一步处理
    ret = [m["body"] for m in rows(rt, "c0", "msg") if m["from"].startswith("channel.")]
    assert "c0/1 refused" in ret and "y/1" in ret                                    # 每条 syscall 恰好一条回执
    facts = [m["body"].split("\n")[0] for m in rows(rt, "c1", "msg") if m["from"] != "door"]
    assert not any(f.startswith("retired") for f in facts) and "placed y 1 program in" in facts
    D = decl_of(rt)
    assert [c["name"] for c in D["channels"]] == ["c0", "c1", "y"] and form_of(rt) == declared(D)


def test_T14_cross_channel_retire():
    G = G0()
    rt, P = fresh(G); start(P, G); rt.run()
    rt.msg("c0", "door", "1", "add y program in\n" + ECHO); rt.run()
    rt.msg("c0", "door", "1", "add y program\n" + ECHO); rt.run()                    # y/2，不是接待员
    rt.msg("c0", "door", "1", "retire y/2"); rt.run()                                 # c0/1 退 y/2：不是自退役
    assert rt.channels["y"].actors["2"].retired
    assert rt._syscall("channel.retire.actor c0/2", "", by="2", by_channel="c0")[1].endswith("refused")   # 同 channel 同地址才是自己
    assert rt._syscall("channel.retire.actor y/1", "", by="1", by_channel="c0")[1].endswith("refused")    # y 的接待员：拒绝
    assert form_of(rt) == declared(decl_of(rt))


def test_T15_fact_source_survives_topology():
    G = G0()
    rt, P = fresh(G); start(P, G); rt.run()
    rt.msg("c0", "door", "1", "add y program in\n" + ECHO); rt.run()
    rt.msg("c0", "door", "1", "add c1 door\nc0"); rt.run()                           # 先加新门 c1→c0（c1/3）
    rt.msg("c0", "door", "1", "retire c1/2"); rt.run()                                # 再退旧门
    assert rt.channels["c1"].actors["2"].retired
    D = decl_of(rt)                                                                  # 历史仍可解释，权威迁到新门
    assert [c["name"] for c in D["channels"]] == ["c0", "c1", "y"]
    assert D["channels"][1]["members"][1] == {"kind": "door", "text": "c0"} and len(D["channels"][1]["members"]) == 2
    rt.msg("c0", "door", "1", "add y program\n" + ECHO); rt.run()                    # 新事实经新门到达
    assert len(decl_of(rt)["channels"][2]["members"]) == 2 and form_of(rt) == declared(decl_of(rt))


if __name__ == "__main__":
    ok = 0; names = sorted(n for n in dir() if n.startswith("test_"))
    for name in names:
        try:
            globals()[name](); ok += 1; print("PASS", name)
        except Exception:
            print("FAIL", name); traceback.print_exc(limit=3)
    print(f"{ok}/{len(names)}")
