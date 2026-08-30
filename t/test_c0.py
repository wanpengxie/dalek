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
T16 actor 超时/崩溃机器不死；T17 start/born 只一次
T18 c2 = L(oracle) + U(program)：task → L 写 → U 测（失败）→ L 改 → U 通过 → 经 c0 的门 add c3 → decl 有 c3 → 回 done；L 的历史含自己说过的
T19 oracle 端点不通：输出视为空、err 记原因，机器活着
T20 账本是地址 0：show 全部/窗口；账上只记事实行；带内 == 膜外；0 不是成员
"""
from __future__ import annotations
import json, re, sys, tempfile, time, traceback
from pathlib import Path

ROOT = Path(__file__).resolve().parents[1]
sys.path.insert(0, str(ROOT))
from omega import Exec, Store                       # noqa: E402
from runtime import Runtime                         # noqa: E402
from init import up, root as root_line            # noqa: E402
from genesis import G0, G2, pack, construct, start  # noqa: E402
import threading, http.server                         # noqa: E402

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
    """π(A)，按出处：去掉脐带放的、不指向本机的门（出生证明），去掉 c0 里持 spawn 绑定的成员（C）放的门（生子的临时门），
    去掉退役；其余全留（含 realize 放的外部门），地址重排。"""
    c0 = next(iter(rt.channels.values()))
    spawners = {a.addr for a in c0.actors.values() if "spawn" in a.bind}
    by_of = {(c.name, r["addr"]): r["by"] for c in rt.channels.values() for r in c.rows if r["k"] == "place"}
    def heritable(cn, a):
        if a.retired: return False
        if a.kind != "door": return True
        by = by_of[(cn, a.addr)]
        if by == "_root": return a.text in rt.channels
        return not (cn == c0.name and by in spawners)
    out = []
    for c in rt.channels.values():
        live = [a for a in c.actors.values() if heritable(c.name, a)]
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
    c2 = G["channels"][2]["members"]
    assert c2[0]["kind"] == "oracle" and c2[0]["text"] == (ROOT / "actors" / "l.txt").read_text(encoding="utf-8")
    assert c2[1]["text"] == (ROOT / "actors" / "u.py").read_text(encoding="utf-8")
    assert G == G2()                                                             # genesis 重生成后不变


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


def test_T16_actor_failure_is_not_machine_failure():
    assert Exec.run("import time; time.sleep(3)", "", ".", timeout=0.5) == ("", "timeout 0.5s")
    out, err = Exec.run("import sys; print('>>> 1\\nx'); sys.exit(3)", "", ".")
    assert out == "" and err.startswith("exit 3")                                     # 非零退出 → 输出视为空（H10）
    G = G_of([{"name": "a", "members": [{"kind": "program", "text": "import time; time.sleep(3)"}]}])
    rt, P = fresh(G); start(P, G, "hi")
    Exec.run.__defaults__ = (0.5,)
    try:
        rt.run()
    finally:
        Exec.run.__defaults__ = (60,)
    st = rows(rt, "a", "step")
    assert st and st[-1]["out"] == "" and st[-1]["err"].startswith("timeout") and rt.channels["a"].cursor["1"] == st[-1]["upto"]


def test_T17_start_and_born_only_once():
    G = G0()
    rt, P = fresh(G); start(P, G); rt.run()
    n = len(rows(rt, "c1", "place")); D = decl_of(rt)
    rt.msg("c0", "door", "1", "start\n" + json.dumps(G)); rt.run()                   # 出生后再 start：忽略
    assert len(rows(rt, "c1", "place")) == n and len(rt.channels["c0"].actors) == 4
    rt.msg("c1", "door", "1", "born\n" + json.dumps({"world": G["world"], "channels": [], "peers": []})); rt.run()
    assert decl_of(rt) == D                                                          # 第二个 born 不算



# ---------------------------------------------------------------- c2：L 的桩（讲 Anthropic 报文；按视图查表回话）
HELLO_BAD = 'import sys, json\nv = json.load(sys.stdin)\nfor m in v["msgs"]:\n    if m["body"] == "hi": print(">>> %s\\nhullo" % m["from"])'
HELLO = 'import sys, json\nv = json.load(sys.stdin)\nfor m in v["msgs"]:\n    if m["body"] == "hi": print(">>> %s\\nhello" % m["from"])'
HELLO_T = ('import sys, json, subprocess\n'
           'v = {"channel": "c3", "me": "1", "msgs": [{"seq": 1, "from": "2", "to": "1", "body": "hi"}], "actors": []}\n'
           'r = subprocess.run([sys.executable, "m.py"], input=json.dumps(v), capture_output=True, text=True)\n'
           'assert r.stdout == ">>> 2\\nhello\\n", r.stdout')


class StubL:
    """L 的桩：一个 http 服务，收 Anthropic messages 报文，把 user 内容当视图，按状态回固定的话。记下每次看到的视图。"""
    def __init__(self):
        self.views = []
        stub = self
        class H(http.server.BaseHTTPRequestHandler):
            def log_message(self, *a): pass
            def do_POST(self):
                body = json.loads(self.rfile.read(int(self.headers["content-length"])))
                view = json.loads(body["messages"][0]["content"]); stub.views.append((body["system"], view))
                text = stub.answer(view)
                out = json.dumps({"content": [{"type": "text", "text": text}]}).encode()
                self.send_response(200); self.send_header("content-type", "application/json")
                self.send_header("content-length", str(len(out))); self.end_headers(); self.wfile.write(out)
        self.srv = http.server.ThreadingHTTPServer(("127.0.0.1", 0), H)
        threading.Thread(target=self.srv.serve_forever, daemon=True).start()
        self.url = f"http://127.0.0.1:{self.srv.server_address[1]}/v1/messages"

    @staticmethod
    def answer(v):
        """固定策略：没看账就先看；看到账再按最新事件行动。"""
        shown = next((m for m in v["msgs"] if m["from"] == "0"), None)
        if not shown:
            return ">>> 0\nshow"
        rows, me = shown["rows"], v["me"]
        prev = max((r["seq"] for r in rows if r["k"] == "msg" and r["from"] == "0" and r["to"] == me), default=0)
        events = [r for r in rows if r["k"] == "msg" and r["to"] == me and r["from"] != "0" and r["seq"] > prev]
        U = next(a["addr"] for a in v["actors"] if a["kind"] == "program")
        door = next(a["addr"] for a in v["actors"] if a["kind"] == "door" and a["text"] == "c0")
        out = []
        for m in events:
            b = m["body"]
            if b.startswith("task\n"):
                out.append(f">>> {U}\ntest\n{HELLO_BAD}\n===\n{HELLO_T}")
            elif b.startswith("result ") and not b.startswith("result 0"):
                out.append(f">>> {U}\ntest\n{HELLO}\n===\n{HELLO_T}")
            elif b.startswith("result 0"):
                asker = next(r["from"] for r in rows if r["k"] == "msg" and r["body"].startswith("task\n"))
                out.append(f">>> {door}\nadd c3 program in\n{HELLO}\n>>> {asker}\ndone\nc3 已装")
        return "\n".join(out)

    def close(self): self.srv.shutdown()


def with_L(G: dict, url: str) -> dict:
    """把 c2 里 oracle 的第一行（端点 模型 密钥）换成桩的 url；提示语不动。"""
    for c in G["channels"]:
        for m in c["members"]:
            if m["kind"] == "oracle":
                _, _, rest = m["text"].partition("\n"); m["text"] = f"{url} stub key\n{rest}"
    return G


def test_T18_c2_is_an_agent():
    L = StubL()
    try:
        G = with_L(G2(), L.url)
        rt, P = fresh(G); start(P, G); rt.run()
        c2 = rt.channels["c2"]
        assert c2.actors["1"].kind == "oracle" and c2.receptionist == "1" and c2.actors["3"].text == "c0"
        rt.msg("c2", "door", "1", "task\n写一个 actor：收到 hi 回 hello，装进 c3"); rt.run()
        steps = [r for r in rows(rt, "c2", "step") if r["actor"] == "1"]
        assert all(r["err"] == "" for r in steps)
        said = [r["out"] for r in steps if r["out"]]
        assert [o.startswith(">>> 0\nshow") for o in said] == [True, False] * 3 + [True]           # 每个事件两步：先看账，再行动；placed 看完没话说
        assert said[1].startswith(">>> 2\ntest\n") and said[3].startswith(">>> 2\ntest\n")
        assert said[5].startswith(">>> 3\nadd c3 program in\n") and ">>> door\ndone" in said[5]
        results = [m["body"] for m in rows(rt, "c2", "msg") if m["from"] == "2"]
        assert len(results) == 2 and not results[0].startswith("result 0") and results[1].startswith("result 0")   # U：先败后通过
        assert any(m["from"] == "3" and m["body"].startswith("placed c3/1") for m in rows(rt, "c2", "msg"))   # c0 的回执从门回来
        reads = [m for m in rows(rt, "c2", "msg") if m["from"] == "0"]
        assert len(reads) == 4 and all(m["body"].startswith("show 1 ") and "rows" not in m for m in reads)   # 读账在账上：只记事实
        c3 = rt.channels["c3"]
        assert c3.actors["1"].text == HELLO and c3.receptionist == "1" and rows(rt, "c3", "place")[0]["by"] == "1"   # c0 的手装的
        D = decl_of(rt)
        assert [c["name"] for c in D["channels"]] == ["c0", "c1", "c2", "c3"] and D["channels"][3]["members"][0]["text"] == HELLO
        assert D["channels"][2]["members"][0]["kind"] == "oracle" and form_of(rt) == declared(D)   # 作者遗传；形态闭包仍成立
        rt.msg("c3", "door", "1", "hi"); rt.run()
        assert rows(rt, "c3", "step")[-1]["out"] == ">>> door\nhello\n"                              # 新器官在工作
        system, last = L.views[-1]
        assert system.startswith("你是一台机器") and "history" not in last                            # 提示语；视图里没有历史字段
        got = next(m for m in last["msgs"] if m["from"] == "0")["rows"]
        disk = [json.loads(l) for l in (P / "h" / "c2.jsonl").read_text(encoding="utf-8").splitlines()]
        assert got == disk[:len(got)] and got[-1]["seq"] == int(got and next(m for m in last["msgs"] if m["from"] == "0")["body"].split()[2])   # 带内看到的 = 膜外看到的
        assert any(r["k"] == "step" and r["actor"] == "1" and "test\n" in r["out"] for r in got)      # 含自己上一轮说的
        assert any(r["k"] == "place" for r in got) and any(r["k"] == "step" and r["actor"] == "2" for r in got)   # 整本账：放人、U 的步都在
    finally:
        L.close()


def test_T19_oracle_endpoint_down_machine_alive():
    G = with_L(G2(), "http://127.0.0.1:1/v1/messages")
    rt, P = fresh(G); start(P, G); rt.run()
    rt.msg("c2", "door", "1", "task\nx"); rt.run()
    r = [r for r in rows(rt, "c2", "step") if r["actor"] == "1"][-1]
    assert r["out"] == "" and r["err"].startswith("URLError")                                          # 外生失败入账
    assert rt._pending(rt.channels["c2"], "1") == []                                                  # 游标照推，机器静止
    rt.msg("c0", "door", "1", "add y program in\n" + ECHO); rt.run()
    assert "y" in rt.channels                                                                          # 别的器官照常工作

PEEK = ('import sys, json\nv = json.load(sys.stdin)\n'
        'for m in v["msgs"]:\n'
        '    if m["from"] == "0": print(">>> door\\nsaw %d %s" % (len(m["rows"]), " ".join(r["k"] for r in m["rows"])))\n'
        '    elif m["body"].startswith("peek"): print(">>> 0\\nshow " + m["body"][4:])')


def test_T20_ledger_is_address_zero():
    G = G_of([{"name": "c0", "members": [{"kind": "program", "text": PEEK}]}])
    rt, P = fresh(G, creator=None); start(P, G); rt.run()
    c0 = rt.channels["c0"]
    rt.msg("c0", "door", "1", "peek"); rt.run()                                       # 全部
    fact = [m for m in rows(rt, "c0", "msg") if m["from"] == "0"]
    assert len(fact) == 1 and fact[0]["body"] == f"show 1 {fact[0]['seq'] - 1}" and "rows" not in fact[0]   # 账上只有事实行
    n = fact[0]["seq"] - 1
    assert rows(rt, "c0", "step")[-1]["out"].startswith(f">>> door\nsaw {n} place msg step msg")           # 拿到了整本账（放人、start、自己的步、peek）
    rt.msg("c0", "door", "1", "peek 2 3"); rt.run()                                   # 窗口
    assert rows(rt, "c0", "step")[-1]["out"] == ">>> door\nsaw 2 msg step\n"
    disk = [json.loads(l) for l in (P / "h" / "c0.jsonl").read_text(encoding="utf-8").splitlines()]
    assert [r["seq"] for r in disk] == list(range(1, len(disk) + 1)) and disk == c0.rows            # 膜外的账 = R 的账
    assert "0" not in c0.actors and all(r["addr"] != "0" for r in rows(rt, "c0", "place"))         # 0 不是成员

if __name__ == "__main__":
    ok = 0; names = sorted(n for n in dir() if n.startswith("test_"))
    for name in names:
        try:
            globals()[name](); ok += 1; print("PASS", name)
        except Exception:
            print("FAIL", name); traceback.print_exc(limit=3)
    print(f"{ok}/{len(names)}")
