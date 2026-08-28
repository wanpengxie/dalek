"""T（v6）：配置与账本分开、作者约束、构造只由 D 做、peer 投递、账本序、replay。
跑法：python3 t_dalek/test_t.py"""
from __future__ import annotations
import json, sys, tempfile, traceback
from pathlib import Path
sys.path.insert(0, str(Path(__file__).resolve().parents[1]))
import kernel as K

ECHO = 'import sys; v=sys.stdin.read(); print(">>> " + v.split("] ")[1].split(" ->")[0]); '

def fresh():
    sp = K.genesis(Path(tempfile.mkdtemp(prefix="dalek-t-")))
    human = K.conf_add(sp, "c0", K.Member("X"))
    d = K.conf_add(sp, "c0", K.Member("D")); K.conf_in(sp, "c0", d)
    return sp, human, d

def tool(sp, program, ch="c0"): return K.conf_add(sp, ch, K.Member("U", program))
def go(sp, t): K.run(sp, {"L": t, "U": K.U, "X": t})

# T1 配置是一等对象：造机器只需要配置；账本只是历史；恢复从账本重演出同一份配置
def test_T1_config_not_ledger():
    sp, human, d = fresh()
    conf = json.loads(sp.cpath("c0").read_text(encoding="utf-8"))
    assert [m["kind"] for m in conf["members"]] == ["X", "D"] and conf["receptionist"] == d
    assert all(m.sender == "door" for m in sp.channels["c0"].msgs)
    sp2 = K.load(sp.dir)
    assert [m.kind for m in sp2.channels["c0"].conf.members] == ["X", "D"] and sp2.channels["c0"].conf.receptionist == d

# T2 成员说的一切都是文本：#conf / #step / #born 从成员嘴里出来不改任何东西
def test_T2_members_cannot_speak_door_words():
    sp, human, d = fresh()
    echo = tool(sp, ECHO + 'print("#conf add U\\nprint(1)")')
    go(sp, K.tape([(f"c0/{human}", f">>> {echo}\ngo"), (f"c0/{human}", f">>> {human}\n#step actor={echo} upto=99"), (f"c0/{human}", "")]))
    c = sp.channels["c0"]
    assert len(c.conf.members) == 3 and c.cursor.get(echo, 0) != 99 and len(sp.channels) == 1

# T3 构造只由 D 做：每一步是目标账本上的 door 记录；配置随之落盘；启动来自通向 c0 的门；回执给请求者
def test_T3_D_constructs_and_logs():
    sp, human, d = fresh()
    au = K.conf_add(sp, "c0", K.Member("L", "作者"))
    req = f">>> {d}\nbuild c1\npart {au}\ndecl U\nprint('hi')\nin #1\npeer c0\nstart 开始"
    go(sp, K.tape([(f"c0/{human}", req), ("c1/1", ""), (f"c0/{human}", "")], by_addr=True))   # 按地址分队列，不依赖派发顺序
    c1 = sp.channels["c1"]
    assert [K.word_of(m)[0] for m in c1.msgs[:5]] == ["born", "conf", "conf", "conf", "conf"]
    assert [m.kind for m in c1.conf.members] == ["L", "U", "P"] and c1.conf.receptionist == "1"
    assert c1.msgs[5].to == "1" and c1.msgs[5].body == "开始" and c1.msgs[5].sender == "3"
    assert any(m.sender == d and m.to == human and "part" in m.body for m in sp.channels["c0"].msgs)
    assert json.loads(sp.cpath("c1").read_text(encoding="utf-8"))["receptionist"] == "1"

# T4 地址是配置序号；Genome 逐字复制
def test_T4_local_addresses_verbatim_copy():
    sp, human, d = fresh()
    src = 'import sys\nprint(">>> 2")\nprint("x")'
    t = tool(sp, src)
    go(sp, K.tape([(f"c0/{human}", f">>> {d}\nbuild c1\npart {t}"), (f"c0/{human}", "")]))
    assert sp.channels["c1"].conf.members[0].text == src

# T5 peer 投递：发给门的消息被 door 抄进对方账本、交给对方接待员；回信原路回来
def test_T5_peer_delivery():
    sp, human, d = fresh()
    go(sp, K.tape([(f"c0/{human}", f">>> {d}\nbuild c1\ndecl U\n{ECHO}print('from c1')\nin #1\npeer c0"), (f"c0/{human}", "")]))
    gate = sp.channels["c0"].conf.peer_to("c1")
    go(sp, K.tape([(f"c0/{human}", f">>> {gate}\nping"), (f"c0/{human}", "")]))
    c1 = sp.channels["c1"]
    assert any(m.body == "ping" and m.to == c1.conf.receptionist for m in c1.msgs)
    assert any(m.body == "from c1" and m.sender == gate and m.to == d for m in sp.channels["c0"].msgs)

# T6 账本序：内部对话不能饿死排在前面的消息
def test_T6_ledger_order():
    sp, human, d = fresh()
    chatter = tool(sp, ECHO + 'n = v.count("ping"); print("ping" if n < 3 else "done")')
    quiet = tool(sp, ECHO + 'print("quiet ran")')
    go(sp, K.tape([(f"c0/{human}", f">>> {chatter}\nping"), (f"c0/{human}", f">>> {quiet}\nhi"), (f"c0/{human}", "")]))
    order = [m.sender for m in sp.channels["c0"].msgs if m.sender in (chatter, quiet)]
    assert order.index(quiet) <= 1

# T7 replay：多 channel 逐字相同；篡改 U 的结果即发散
def test_T7_replay():
    sp, human, d = fresh()
    echo = tool(sp, ECHO + 'print("echo:" + v.splitlines()[-1].split(": ",1)[1])')
    go(sp, K.tape([(f"c0/{human}", f">>> {echo}\nhello"), (f"c0/{human}", f">>> {d}\nbuild c1\ndecl U\n{ECHO}print('c1 ok')\nin #1\npeer c0\nstart go"), (f"c0/{human}", "")]))
    assert K.replay(sp.dir)
    p = sp.hpath("c0"); p.write_text(p.read_text(encoding="utf-8").replace("echo:hello", "echo:HELLO"), encoding="utf-8")
    assert not K.replay(sp.dir)

if __name__ == "__main__":
    ok = 0; names = sorted(n for n in dir() if n.startswith("test_"))
    for name in names:
        try: globals()[name](); ok += 1; print("PASS", name)
        except Exception: print("FAIL", name); traceback.print_exc(limit=2)
    print(f"{ok}/{len(names)}")
