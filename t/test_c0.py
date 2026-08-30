"""M1 · c0 + R；M2 · c1 + decl + retire；M3 · c2 = L + U。跑法：python3 t/test_c0.py

T0 本目录 = dalek0 的 P：G.json 的 world 与磁盘文件逐字节相等；成员 text 与 actors/* 相等
T1 运行：program 收初始消息、写帧、re 回发送者；出生证明门给外来消息署名；回信经门出去
T2 syscall 是请求：回执就是回复；无绑定则丢弃
T3 门：c0 发育出 a 和两扇互指的门（角色 = 对面的名字）；消息抄到对面账本，署名对面的门，收件人是接待员；回信原路回来
T4 根门：R 起来零 channel；膜外经根门只造 c0（by=_root）；构造期间机械不动；start\\n<G> 关门；c0 自己长出其余（by=1）；关门后根门无效
T5 请求 add / peer（本地生长）；placed 回请求者（嵌套运行）；登记员收到 placed
T6 内容盲：R 源码不含组织词汇；G 全部改名后账本同构
T7 生子：C 经 A 向登记员要 decl（两次运行）→ pack + spawn + 两扇门 + A 经门造子代的 c0 + start；父代对象销毁后子代活着且已切离
T8 登记：出生后 decl(dalek0) == G0；c1 账本第一条消息是 born；channels 顺序 == _order
T9 遗传运行中的改动：add + peer 后经接待员 spawn → 子代有它们；decl(子) == decl(父)
T10 替换 + 非平凡：add 新 realize′(in, tag=A) + retire 旧 → 角色 A 解析到新的、旧不再被运行、decl 略过 → 子代的接待员是 realize′
T11 retire 后写给它的消息留账不投递；退役的门不再署名
T12 形态闭包与拒绝：单向门、无接待员 channel；自退役 / 退役接待员 / 伪造 placed 无效；refused 是同步回复
T14 跨 channel 退役（同地址不算自己）；T15 事实来源不随拓扑漂（先加新门再退旧门，历史仍可解释）
T16 actor 崩溃 / 超时：out 空、err 记原因，游标照推，机器活着
T17 start 只在出生时有效；第二个 born 不算
T18 c2 = L(oracle) + U(program)：一次运行里 task → U 败 → U 过 → 经门 add c3；placed 到来是新的运行 → done 经真门回到发起者，seq(done) > seq(placed)
T19 oracle 端点不通：输出视为空、err 记原因，机器活着
T20 账本是地址 0：show 全部/窗口；账上只记事实行；带内 == 膜外；0 不是成员
T21 角色：按 tag 寻址；后放的接替先放的；退役后回到前一个
"""
from __future__ import annotations
import json, re, sys, tempfile, time, traceback
from pathlib import Path

ROOT = Path(__file__).resolve().parents[1]
sys.path.insert(0, str(ROOT))
from omega import Store                              # noqa: E402
from runtime import Runtime, parse                   # noqa: E402
from init import up, root as root_line, say          # noqa: E402
from genesis import G0, G2, pack, construct, start   # noqa: E402
import threading, http.server                        # noqa: E402

CALL = ('import sys, json\n'
        'def call(to, body=""):\n'
        '    sys.stdout.write(">>> %s\\n%s\\n<<<\\n" % (to, body)); sys.stdout.flush()\n'
        '    r = []\n'
        '    while True:\n'
        '        line = sys.stdin.readline()\n'
        '        if not line or line == "<<<\\n": break\n'
        '        r.append(line.rstrip("\\n"))\n'
        '    return "\\n".join(r)\n'
        'm = json.loads(sys.stdin.readline())\n')
ECHO = CALL + 'call("re", "echo:" + m["body"])\n'
ECHO2 = CALL + 'call("re", "echo2:" + m["body"])\n'
PING = CALL + 'if m["body"] == "hi": call("2", "ping")\n'
PLACER = CALL + 'call("channel.create y"); call("channel.add.actor y program in", "print(1)")\n'
ASKER = CALL + ('if m["body"] == "go": call("A", "add y program in tag=e\\n" + %r); call("A", "peer c0 y")\n'
                'else: call("4", "relay:" + m["body"])\n') % ECHO


def G_of(channels, peers=()):
    for c in channels: c.setdefault("receptionist", 1)                         # 接待员必须显式
    return {"world": G0()["world"], "channels": channels, "peers": list(peers)}


def expand(G: dict) -> dict:
    """人写的 G（peers 是糖）→ 规范形：门就是成员（角色 = 对面的名字），peers 空。与 realize 放门的顺序一致。"""
    G = json.loads(json.dumps(G)); by = {c["name"]: c for c in G["channels"]}
    for a, b in G.get("peers", []):
        by[a]["members"].append({"kind": "door", "text": b, "tag": b}); by[b]["members"].append({"kind": "door", "text": a, "tag": a})
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


def frames_of(step: dict) -> list[tuple[str, str]]:
    return parse(step["out"])


def decl_of(rt: Runtime, ch="c1") -> dict:
    """膜外问登记员 decl；回答写给 door（无门可回）→ 只留在 step.out 里，从那里读。"""
    r = rt.channels[ch].receptionist
    rt.msg(ch, "door", r, "decl"); rt.run()
    for st in reversed(rows(rt, ch, "step")):
        fr = dict(frames_of(st))
        if fr.get("re", "").startswith("decl\n"):
            return json.loads(fr["re"].split("\n", 1)[1])
    raise AssertionError("no decl reply")


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
        ms = []
        for a in live:
            m = {"kind": a.kind, "text": a.text}
            if a.bind: m["bind"] = list(a.bind)
            if a.tag: m["tag"] = a.tag
            if a.iface: m["iface"] = a.iface
            ms.append(m)
        ch = {"name": c.name, "members": ms}
        if c.receptionist is not None:
            ch["receptionist"] = [a.addr for a in live].index(c.receptionist) + 1
        out.append(ch)
    return out


def declared(D: dict):
    return D["channels"]


def wait_child(d: Path, ready, timeout=20.0) -> Runtime:
    t0 = time.time()
    while time.time() - t0 < timeout:
        try:
            c = Runtime(d).load()
            if ready(c): return c
        except Exception:
            pass
        time.sleep(0.2)
    raise AssertionError(f"child not ready: {d}")


# ---------------------------------------------------------------- M1
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


def test_T1_run_program():
    G = G_of([{"name": "c0", "members": [{"kind": "program", "text": ECHO}]}])
    rt, P = fresh(G)
    c0 = rt.channels["c0"]
    assert list(c0.actors) == ["1", "2"] and c0.actors["2"].kind == "door" and c0.actors["2"].text == "human"
    say(P, "c0", "hi", frm="human"); rt.run()                                    # 膜外经收件箱：出生证明门署名
    msgs = rows(rt, "c0", "msg")
    assert (msgs[0]["from"], msgs[0]["to"], msgs[0]["body"]) == ("2", "1", "hi") and "run" not in msgs[0]   # 事件
    st = [r for r in rows(rt, "c0", "step") if r["actor"] == "1"][0]
    assert st["upto"] == msgs[0]["seq"] and st["err"] == "" and frames_of(st) == [("re", "echo:hi")]
    assert (msgs[1]["from"], msgs[1]["to"], msgs[1]["body"], msgs[1]["run"]) == ("1", "2", "echo:hi", msgs[0]["seq"])   # 回信经门，带 run
    assert any(r["k"] == "step" and r["actor"] == "2" and r.get("run") == msgs[0]["seq"] for r in c0.rows)   # 门的一步嵌套在运行里
    assert json.loads((P / "in" / "human.jsonl").read_text().splitlines()[-1])["body"] == "echo:hi"          # 出去了
    assert rt._pending(c0, "1") == []


def test_T2_syscall_is_a_request():
    G = G_of([{"name": "c0", "members": [{"kind": "program", "text": PLACER, "bind": ["syscall"]}]}])
    rt, P = fresh(G)
    rt.msg("c0", "door", "1", "go"); rt.run()
    assert "y" in rt.channels and rt.channels["y"].actors["1"].text == "print(1)" and rt.channels["y"].receptionist == "1"
    rets = [m for m in rows(rt, "c0", "msg") if m["from"].startswith("channel.")]
    assert [m["body"] for m in rets] == ["y new", "y/1"] and all(m["to"] == "1" and "run" in m for m in rets)   # 回执在账上，带 run
    st = [r for r in rows(rt, "c0", "step") if r["actor"] == "1"][0]
    assert [h for h, _ in frames_of(st)] == ["channel.create y", "channel.add.actor y program in"]
    G = G_of([{"name": "c0", "members": [{"kind": "program", "text": PLACER}]}])                # 无绑定
    rt, P = fresh(G)
    rt.msg("c0", "door", "1", "go"); rt.run()
    assert "y" not in rt.channels and not any(m["from"].startswith("channel.") for m in rows(rt, "c0", "msg"))


def test_T3_doors():
    SENDER = CALL + 'if m["body"] == "go": call("a", "hello")\n'
    G = G0(); G["channels"][0]["members"].append({"kind": "program", "text": SENDER})     # c0/3
    G["channels"].append({"name": "a", "members": [{"kind": "program", "text": ECHO}], "receptionist": 1}); G["peers"].append(["c0", "a"])
    rt, P = fresh(G); start(P, G); rt.run()
    c0, a = rt.channels["c0"], rt.channels["a"]
    assert c0.actors["6"].text == "a" and c0.actors["6"].tag == "a" and a.actors["2"].text == "c0" and a.actors["2"].tag == "c0"   # 两扇互指的门，角色 = 对面
    assert rows(rt, "a", "place")[0]["by"] == "1" and rows(rt, "c0", "place")[5]["by"] == "1"   # 自己的手长的
    rt.msg("c0", "door", "3", "go"); rt.run()
    m = [x for x in rows(rt, "a", "msg") if x["body"] == "hello"][0]
    assert (m["from"], m["to"]) == ("2", "1")                                   # 抄到对面：署名对面的门，收件人接待员
    back = [x for x in rows(rt, "c0", "msg") if x["body"] == "echo:hello"][0]
    assert (back["from"], back["to"]) == ("6", "1")                             # 回信原路回来：署名这边的门，给接待员


def test_T4_root_door():
    G = G0(); G["channels"].append({"name": "x", "members": [{"kind": "program", "text": ECHO}], "receptionist": 1}); G["peers"].append(["c0", "x"])
    P = pack(G, Path(tempfile.mkdtemp(prefix="dalek-")))
    rt = up(P); assert rt.channels == {}
    construct(P, G); rt.run()
    assert list(rt.channels) == ["c0"] and rt.root_open                         # 膜外只造 c0
    c0 = rt.channels["c0"]
    assert all(r["by"] == "_root" for r in rows(rt, "c0", "place")) and not rows(rt, "c0", "msg")
    assert c0.actors["1"].bind == ("syscall",) and c0.actors["1"].tag == "A" and c0.actors["2"].bind == ("syscall", "spawn")
    assert c0.actors["3"].text == "human" and c0.receptionist == "1"
    start(P, G); rt.run()                                                      # 第一条消息：关门；c0 自己长其余
    assert not rt.root_open and list(rt.channels) == ["c0", "c1", "x"]
    assert all(r["by"] == "1" for r in rows(rt, "x", "place")) and rows(rt, "c0", "place")[3]["by"] == "1"
    x = rt.channels["x"]
    assert x.actors["1"].text == ECHO and x.actors["2"].text == "c0"
    assert c0.actors["4"].text == "c1" and c0.actors["5"].text == "x"
    root_line(P, "channel.add.actor c0 program\nprint(0)"); rt.run()
    assert len(c0.actors) == 5                                                   # 切离：根门无效
    rt.msg("c0", "1", "5", "hello"); rt.run()
    assert any(m["from"] == "5" and m["body"] == "echo:hello" for m in rows(rt, "c0", "msg"))


def test_T5_requests_add_peer():
    G = G0(); G["channels"][0]["members"].append({"kind": "program", "text": ASKER})   # c0/3；出生证明门 c0/4
    rt, P = fresh(G); start(P, G); rt.run()
    rt.msg("c0", "door", "3", "go"); rt.run()
    y = rt.channels["y"]
    assert y.actors["1"].text == ECHO and y.actors["1"].tag == "e" and y.receptionist == "1" and y.actors["2"].text == "c0"
    assert rt.channels["c0"].actors["6"].text == "y" and rows(rt, "y", "place")[0]["by"] == "1"
    ms = rows(rt, "c0", "msg")
    ev = [m for m in ms if m["body"] == "go"][0]
    assert [m["body"] for m in ms if m["from"] == "3" and m["to"] == "1"] == ["add y program in tag=e\n" + ECHO, "peer c0 y"]
    assert [m["body"] for m in ms if m["from"] == "1" and m["to"] == "3"] == ["placed y/1", "placed c0/6", "placed y/2"]   # 回请求者
    assert [m["body"] for m in ms if m["from"] == "3" and m["to"] == "4"][0] == "relay:placed y/1"       # 请求者被嵌套地运行了
    assert all(m.get("run") == ev["seq"] for m in ms if m["seq"] > ev["seq"])                             # 全在一次运行里
    reg = [m["body"] for m in rows(rt, "c1", "msg") if m["body"].startswith("placed y 1 ")]
    assert reg and reg[0].startswith("placed y 1 program in tag=e\n")                                    # 登记员知道了


def test_T6_content_blind():
    src = (ROOT / "runtime.py").read_text(encoding="utf-8").replace("\\n", " ")
    for w in ("c0", "c1", "c2", "realize", "registrar", "decl", "born", "placed", "pack", "clone", "构造器", "登记", "装配"):
        assert not re.search(rf"(?<![A-Za-z0-9_]){re.escape(w)}(?![A-Za-z0-9_])", src), w
    def run(names):
        a, b = names
        G = json.loads(json.dumps(G0(), ensure_ascii=False).replace("c0", a).replace("c1", b))
        G["channels"][0]["members"].append({"kind": "program", "text": ASKER.replace("c0", a)})
        rt, P = fresh(G); start(P, G); rt.run()
        rt.msg(a, "door", "3", "go"); rt.run()
        return [(n, c.rows) for n, c in rt.channels.items()]
    def norm(h, names):
        a, b = names
        s = json.dumps(h, ensure_ascii=False, sort_keys=True).replace(a, "#0").replace(b, "#1")
        return re.sub(r"file:[^\"#]+#", "file:P#", s)
    assert norm(run(("c0", "c1")), ("c0", "c1")) == norm(run(("q7", "zz")), ("q7", "zz"))


def test_T7_spawn_child_built_by_parent_A():
    G = G0(); G["channels"][0]["members"].append(
        {"kind": "program", "text": CALL + 'if m["body"] == "go": call("C", "spawn d1")\nelse: call("4", "reply:" + m["body"])\n'})
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
        assert any(m["to"] == "4" and m["body"].startswith("reply:spawned") for m in rows(rt, "c0", "msg"))   # 请求者拿到回话
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
        assert rows(child, "c1", "msg")[0]["body"].startswith("born\n")
    finally:
        import os, signal
        try: os.killpg(pid, signal.SIGTERM)
        except ProcessLookupError: pass


# ---------------------------------------------------------------- M2
def test_T8_registrar_decl():
    G = G0()
    rt, P = fresh(G); start(P, G); rt.run()
    D = decl_of(rt)
    assert D == expand(G0())                                                     # 出生后的形态 == 基因组
    first = rows(rt, "c1", "msg")[0]
    assert first["body"].startswith("born\n") and first["from"] == "2"           # 经门来的 born
    born = json.loads(first["body"].split("\n", 1)[1])
    assert [c["name"] for c in born["channels"]] == ["c0"] and born["peers"] == []
    assert [c["name"] for c in D["channels"]] == Store.read(P / "h" / "_order").split()
    assert form_of(rt) == declared(D)


def test_T9_inherit_runtime_changes():
    G = G0()
    rt, P = fresh(G); start(P, G); rt.run()
    rt.msg("c0", "door", "1", "add c0 program tag=e\n" + ECHO); rt.run()          # c0/4
    rt.msg("c0", "door", "1", "add y program\n" + ECHO); rt.run()                # y，无接待员
    rt.msg("c0", "door", "1", "peer c0 y"); rt.run()
    D = decl_of(rt)
    assert D["channels"][0]["members"][3]["text"] == ECHO and D["channels"][2]["name"] == "y" and "receptionist" not in D["channels"][2]
    assert form_of(rt) == declared(D)
    rt.msg("c0", "door", "1", "spawn d2"); rt.run()
    d = P / "spawn" / "d2"
    pid = int([m for m in rows(rt, "c0", "msg") if m["from"] == "spawn"][0]["body"].split("pid=")[1])
    try:
        child = wait_child(d, lambda c: "y" in c.channels and rows(c, "y", "place") and len(rows(c, "c1", "msg")) >= 4)
        assert child.channels["c0"].actors["4"].text == ECHO and child.channels["c0"].actors["4"].tag == "e"
        assert child.channels["y"].receptionist is None and child.channels["y"].actors["2"].text == "c0"
        assert child.channels["c0"].actors["3"].text == "c1" and child.channels["c0"].actors["5"].text == "y"   # 地址重排
        time.sleep(0.5)
        assert decl_of(child) == D                                               # 子代的基因组 == 父代的
        assert form_of(child) == declared(D)
    finally:
        import os, signal
        try: os.killpg(pid, signal.SIGTERM)
        except ProcessLookupError: pass


R2 = (ROOT / "actors" / "realize.py").read_text(encoding="utf-8") + "\n# realize'\n"


def test_T10_replace_realize_is_nontrivial():
    G = G0()
    rt, P = fresh(G); start(P, G); rt.run()
    c0 = rt.channels["c0"]
    rt.msg("c0", "door", "1", "add c0 program in bind=syscall tag=A\n" + R2); rt.run()   # 新的接待员 c0/5，同角色
    assert c0.receptionist == "5" and c0.actors["5"].text == R2 and rt._resolve(c0, "A").addr == "5"   # 后放的接替
    rt.msg("c0", "door", "5", "retire c0/1"); rt.run()                             # 再退旧的
    assert c0.actors["1"].retired and rows(rt, "c0", "retire")[0]["addr"] == "1"
    rt.msg("c0", "door", "1", "add z program\n" + ECHO); rt.run()
    assert "z" not in rt.channels                                                  # 旧的不再运行
    D = decl_of(rt)
    assert [m["text"] for m in D["channels"][0]["members"]] == [G["channels"][0]["members"][1]["text"], "c1", R2]
    assert D["channels"][0]["receptionist"] == 3 and form_of(rt) == declared(D)
    rt.msg("c0", "door", "5", "spawn d3"); rt.run()
    d = P / "spawn" / "d3"
    pid = int([m for m in rows(rt, "c0", "msg") if m["from"] == "spawn"][0]["body"].split("pid=")[1])
    try:
        child = wait_child(d, lambda c: "c1" in c.channels and rows(c, "c1", "msg"))
        cc = child.channels["c0"]
        assert cc.receptionist == "3" and cc.actors["3"].text == R2 and cc.actors["3"].bind == ("syscall",) and cc.actors["3"].tag == "A"
    finally:
        import os, signal
        try: os.killpg(pid, signal.SIGTERM)
        except ProcessLookupError: pass


def test_T11_retired_not_run():
    G = G0(); G["channels"][0]["members"].append({"kind": "program", "text": ECHO})   # c0/3
    rt, P = fresh(G); start(P, G); rt.run()
    rt.msg("c0", "door", "1", "retire c0/3"); rt.run()
    assert rt.channels["c0"].actors["3"].retired
    n = len(rows(rt, "c0", "step"))
    rt.msg("c0", "door", "3", "hi"); rt.run()
    assert len(rows(rt, "c0", "step")) == n and rows(rt, "c0", "msg")[-1]["body"] == "hi"       # 留账不投递
    assert any(m["body"].startswith("retired c0/3") for m in rows(rt, "c1", "msg"))             # 登记员知道了
    rt.msg("c0", "door", "1", "retire c0/5"); rt.run()                                          # 退了通往 c1 的门
    say(P, "c0", "x", frm=f"file:{P}#c1"); rt.run()
    assert rows(rt, "c0", "msg")[-1]["from"] == "door"                                          # 退役的门不再署名


def test_T12_form_closure_and_refusals():
    G = G0()
    G["channels"].append({"name": "y", "members": [{"kind": "program", "text": ECHO}, {"kind": "door", "text": "c0"}]})   # 单向门，无接待员
    G["channels"].append({"name": "z", "members": [{"kind": "door", "text": "y"}]})                                     # 只有门
    rt, P = fresh(G); start(P, G); rt.run()
    D = decl_of(rt)
    assert D == expand(G) and form_of(rt) == declared(D)                          # 单向门、无接待员、只有门都能表达
    c0 = rt.channels["c0"]
    assert rt._syscall("channel.retire.actor c0/2", "", by="2", by_channel="c0")[1] == "c0/2 refused" and not c0.actors["2"].retired   # 自退役被拒
    assert rt._syscall("channel.retire.actor c0/1", "", by="2", by_channel="c0")[1] == "c0/1 refused" and not c0.actors["1"].retired   # 退役接待员被拒
    assert rt._syscall("channel.add.actor nope program", "x", by="1", by_channel="c0")[1] == "nope refused"
    rt.msg("c1", "door", "1", "placed q 1 program in\nprint(1)"); rt.run()          # 不是本机门说的
    assert decl_of(rt) == D
    REF = CALL + 'call("re", "got:" + call("channel.add.actor nope program", "x"))\n'
    rt.msg("c0", "door", "1", "add c0 program bind=syscall tag=r\n" + REF); rt.run()
    rt.msg("c0", "door", rt._resolve(c0, "r").addr, "go"); rt.run()
    assert ("re", "got:nope refused") in frames_of(rows(rt, "c0", "step")[-1])    # refused 是同步回复


def test_T14_cross_channel_retire():
    G = G0(); G["channels"].append({"name": "y", "members": [{"kind": "program", "text": ECHO}, {"kind": "program", "text": ECHO}], "receptionist": 1})
    rt, P = fresh(G); start(P, G); rt.run()
    assert rt._syscall("channel.retire.actor y/2", "", by="2", by_channel="c0")[1] == "y/2"      # c0/2 退 y/2：不是自己
    assert rt.channels["y"].actors["2"].retired


def test_T15_fact_source_survives_topology():
    G = G0()
    rt, P = fresh(G); start(P, G); rt.run()
    rt.msg("c0", "door", "1", "peer c0 c1"); rt.run()                              # 先加新门
    rt.msg("c0", "door", "1", "retire c0/4"); rt.run()                             # 再退旧门
    rt.msg("c0", "door", "1", "add c0 program\n" + ECHO); rt.run()                 # 新门送的事实
    D = decl_of(rt)
    assert [m["kind"] for m in D["channels"][0]["members"]] == ["program", "program", "door", "program"]   # 旧门退了、新门在、新成员在
    assert form_of(rt) == declared(D)                                              # 老账本仍可解释


def test_T16_actor_failure_is_not_machine_failure():
    CRASH = CALL + 'raise SystemExit(3)\n'
    SLEEP = CALL + 'import time; time.sleep(5)\n'
    G = G_of([{"name": "c0", "members": [{"kind": "program", "text": CRASH}, {"kind": "program", "text": SLEEP}, {"kind": "program", "text": ECHO}]}])
    rt, P = fresh(G); rt.timeout = 1.0
    rt.msg("c0", "door", "1", "x"); rt.run()
    st = rows(rt, "c0", "step")[-1]
    assert st["actor"] == "1" and st["out"] == "" and st["err"].startswith("exit 3")
    rt.msg("c0", "door", "2", "x"); rt.run()
    st = rows(rt, "c0", "step")[-1]
    assert st["actor"] == "2" and st["out"] == "" and st["err"].startswith("timeout")
    assert rt._pending(rt.channels["c0"], "1") == [] and rt._pending(rt.channels["c0"], "2") == []   # 游标照推
    rt.msg("c0", "door", "3", "x"); rt.run()
    assert ("re", "echo:x") in frames_of(rows(rt, "c0", "step")[-1])                                 # 机器活着


def test_T17_start_and_born_only_once():
    G = G0()
    rt, P = fresh(G); start(P, G); rt.run()
    n = len(rt.channels["c0"].actors)
    G2_ = json.loads(json.dumps(G)); G2_["channels"].append({"name": "w", "members": [{"kind": "program", "text": ECHO}], "receptionist": 1})
    rt.msg("c0", "door", "1", "start\n" + json.dumps(G2_)); rt.run()
    assert "w" not in rt.channels and len(rt.channels["c0"].actors) == n           # 已发育：start 无效
    rt.msg("c1", "2", "1", "born\n" + json.dumps({"world": G["world"], "channels": [{"name": "c0", "members": [], "receptionist": 1}], "peers": []})); rt.run()
    assert decl_of(rt) == expand(G0())                                             # 第二个 born 不算


# ---------------------------------------------------------------- M3：c2 = L + U
HELLO_BAD = CALL + 'if m["body"] == "hi": call("re", "hullo")\n'
HELLO = CALL + 'if m["body"] == "hi": call("re", "hello")\n'
HELLO_T = ('import sys, json, subprocess\n'
           'm = {"seq": 1, "from": "2", "to": "1", "body": "hi", "channel": "c3"}\n'
           'r = subprocess.run([sys.executable, "m.py"], input=json.dumps(m) + "\\n", capture_output=True, text=True)\n'
           'assert r.stdout == ">>> re\\nhello\\n<<<\\n", r.stdout')


class StubL:
    """L 的桩：一个 http 服务，收 Anthropic messages 报文（多轮），按对话状态回固定的帧。记下每次看到的对话。"""
    def __init__(self):
        self.calls = []
        stub = self
        class H(http.server.BaseHTTPRequestHandler):
            def log_message(self, *a): pass
            def do_POST(self):
                body = json.loads(self.rfile.read(int(self.headers["content-length"])))
                stub.calls.append(body)
                text = stub.answer(body["messages"])
                out = json.dumps({"content": [{"type": "text", "text": text}]}).encode()
                self.send_response(200); self.send_header("content-type", "application/json")
                self.send_header("content-length", str(len(out))); self.end_headers(); self.wfile.write(out)
        self.srv = http.server.ThreadingHTTPServer(("127.0.0.1", 0), H)
        threading.Thread(target=self.srv.serve_forever, daemon=True).start()
        self.url = f"http://127.0.0.1:{self.srv.server_address[1]}/v1/messages"

    @staticmethod
    def answer(messages):
        view = json.loads(messages[0]["content"])
        if len(messages) == 1:                                                     # 第一轮：看初始消息
            b = view["msg"]["body"]
            if b.startswith("task\n"):
                return f">>> U\ntest\n{HELLO_BAD}\n===\n{HELLO_T}\n<<<"
            if b.startswith("placed "):
                asker = next(r["from"] for r in view["ledger"] if r["k"] == "msg" and r["body"].startswith("task\n"))
                return f">>> {asker}\ndone\nc3 已装\n<<<"
            return ""
        replies = json.loads(messages[-1]["content"])                              # 之后：看回复
        for r in replies:
            if r["to"] == "U" and not r["reply"].startswith("result 0"):
                return f">>> U\ntest\n{HELLO}\n===\n{HELLO_T}\n<<<"
            if r["to"] == "U" and r["reply"].startswith("result 0"):
                return f">>> c0\nadd c3 program in tag=hello iface=hi -> hello\n{HELLO}\n<<<"
        return ""                                                                  # 没有请求：运行结束

    def close(self): self.srv.shutdown()


def with_L(G: dict, url: str) -> dict:
    """把 c2 里 oracle 的第一行（端点 模型 密钥）换成桩的 url；提示语不动。"""
    for c in G["channels"]:
        for m in c["members"]:
            if m["kind"] == "oracle":
                _, _, rest = m["text"].partition("\n"); m["text"] = f"{url} stub key\n{rest}"
    return G


def test_T18_c2_hosts_oracle_guided_synthesis_loop():
    L = StubL()
    try:
        G = with_L(G2(), L.url)
        rt, P = fresh(G); start(P, G); rt.run()
        me = Path(tempfile.mkdtemp(prefix="me-"))
        rt.msg("c0", "door", "1", f"add c2 door tag=me\nfile:{me}#me"); rt.run()          # 发起者的真门 c2/4
        c2 = rt.channels["c2"]
        assert c2.actors["1"].kind == "oracle" and c2.actors["1"].tag == "L" and c2.receptionist == "1"
        assert c2.actors["2"].tag == "U" and c2.actors["3"].tag == "c0" and c2.actors["4"].text == f"file:{me}#me"
        say(P, "c2", "task\n写一个 actor：收到 hi 回 hello，装进 c3", frm=f"file:{me}#me"); rt.run()
        ms = rows(rt, "c2", "msg")
        task = [m for m in ms if m["body"].startswith("task\n")][0]
        assert task["from"] == "4" and "run" not in task                                            # 事件，署名真门
        runs = [r for r in rows(rt, "c2", "step") if r["actor"] == "1" and "run" not in r]
        assert len(runs) == 2 and all(r["err"] == "" for r in runs)                                 # 两次运行：task、placed
        fr = frames_of(runs[0])
        assert [h for h, _ in fr] == ["U", "U", "c0"] and fr[2][1].startswith("add c3 program in tag=hello iface=hi -> hello\n")
        results = [m["body"] for m in ms if m["from"] == "2" and m["to"] == "1"]
        assert len(results) == 2 and not results[0].startswith("result 0") and results[1].startswith("result 0")   # U：先败后通过，都在运行里
        placed = [m for m in ms if m["from"] == "3" and m["body"].startswith("placed c3/1")][0]
        assert all(m["run"] == task["seq"] for m in ms if task["seq"] < m["seq"] < placed["seq"] and m["from"] in ("1", "2"))
        assert "run" not in placed                                                                  # c0 的回执经门回来：新的事件
        done = [m for m in ms if m["body"].startswith("done\n")][0]
        assert (done["from"], done["to"]) == ("1", "4") and done["seq"] > placed["seq"] and done["run"] == placed["seq"]   # placed 之后才 done，经真门
        got = json.loads((me / "in" / "me.jsonl").read_text().splitlines()[-1])
        assert got["body"].startswith("done\n") and got["from"] == f"file:{P}#c2"                    # 发起者真的收到了
        reads = [m for m in ms if m["from"] == "0"]
        assert len(reads) == 2 and all(m["body"].startswith("show 1 ") for m in reads)               # 每次运行组装读一次账
        c3 = rt.channels["c3"]
        assert c3.actors["1"].text == HELLO and c3.actors["1"].tag == "hello" and c3.actors["1"].iface == "hi -> hello" and rows(rt, "c3", "place")[0]["by"] == "1"
        D = decl_of(rt)
        assert [c["name"] for c in D["channels"]] == ["c0", "c1", "c2", "c3"] and D["channels"][3]["members"][0]["tag"] == "hello"
        assert D["channels"][2]["members"][0]["kind"] == "oracle" and form_of(rt) == declared(D)    # 作者遗传；形态闭包仍成立
        rt.msg("c3", "door", "1", "hi"); rt.run()
        assert ("re", "hello") in frames_of(rows(rt, "c3", "step")[-1])                              # 新器官在工作
        first = json.loads(L.calls[0]["messages"][0]["content"])
        assert set(first) == {"msg", "ledger", "members"} and first["msg"]["body"].startswith("task\n")
        disk = [json.loads(l) for l in (P / "h" / "c2.jsonl").read_text(encoding="utf-8").splitlines()]
        strip = lambda r: {k: v for k, v in r.items() if k != "local"}
        assert [strip(r) for r in first["ledger"]] == disk[:len(first["ledger"])]                     # 带内看到的 = 膜外看到的（门行多一个此刻的 local）
        assert [x["tag"] for x in first["members"]] == ["L", "U", "c0", "me"] and "iface" in first["members"][1]
        assert len(L.calls[0]["messages"]) == 1 and len(L.calls[2]["messages"]) == 5                  # 多轮：第三次请求带两轮回复
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


PEEK = CALL + ('if m["body"].startswith("peek"):\n'
               '    rows = [json.loads(l) for l in call("0", "show" + m["body"][4:]).splitlines() if l]\n'
               '    call("re", "saw %d %s" % (len(rows), " ".join(r["k"] for r in rows)))\n')


def test_T20_ledger_is_address_zero():
    G = G_of([{"name": "c0", "members": [{"kind": "program", "text": PEEK}]}])
    rt, P = fresh(G, creator=None)
    rt.msg("c0", "door", "1", "peek"); rt.run()                                       # 全部
    fact = [m for m in rows(rt, "c0", "msg") if m["from"] == "0"]
    assert len(fact) == 1 and fact[0]["body"] == f"show 1 {fact[0]['seq'] - 1}" and "rows" not in fact[0] and fact[0]["run"] == 2   # 账上只有事实行
    assert ("re", "saw 2 place msg") in frames_of(rows(rt, "c0", "step")[-1])          # 拿到了整本账（放人、peek）
    rt.msg("c0", "door", "1", "peek 2 3"); rt.run()                                   # 窗口
    assert ("re", "saw 2 msg msg") in frames_of(rows(rt, "c0", "step")[-1])
    disk = [json.loads(l) for l in (P / "h" / "c0.jsonl").read_text(encoding="utf-8").splitlines()]
    c0 = rt.channels["c0"]
    assert [r["seq"] for r in disk] == list(range(1, len(disk) + 1)) and disk == c0.rows            # 膜外的账 = R 的账
    assert "0" not in c0.actors and all(r["addr"] != "0" for r in rows(rt, "c0", "place"))         # 0 不是成员


def test_T21_roles():
    CALLER = CALL + 'call("re", "got:" + call("e", "hi"))\n'
    G = G_of([{"name": "c0", "members": [{"kind": "program", "text": ECHO, "tag": "e"}, {"kind": "program", "text": CALLER}], "receptionist": 2}])
    rt, P = fresh(G, creator=None)
    rt.msg("c0", "door", "2", "go"); rt.run()
    assert ("re", "got:echo:hi") in frames_of(rows(rt, "c0", "step")[-1])            # 按角色找到 1
    rt.add("c0", "program", ECHO2, tag="e"); rt.msg("c0", "door", "2", "go"); rt.run()
    assert ("re", "got:echo2:hi") in frames_of(rows(rt, "c0", "step")[-1])           # 后放的接替
    rt.retire("c0", "3", by="door", by_channel=None); rt.msg("c0", "door", "2", "go"); rt.run()
    assert ("re", "got:echo:hi") in frames_of(rows(rt, "c0", "step")[-1])            # 退役后回到前一个
    assert rt._resolve(rt.channels["c0"], "nobody") is None


if __name__ == "__main__":
    ok = 0; names = sorted(n for n in dir() if n.startswith("test_"))
    for name in names:
        try:
            globals()[name](); ok += 1; print("PASS", name)
        except Exception:
            print("FAIL", name); traceback.print_exc(limit=3)
    print(f"{ok}/{len(names)}")
