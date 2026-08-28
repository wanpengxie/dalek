"""T（v5）：账本只记录、作者约束、构造只由 D 做、peer 投递、账本序、replay。
跑法：python3 t_dalek/test_t.py"""
from __future__ import annotations
import sys, tempfile, traceback
from pathlib import Path
sys.path.insert(0, str(Path(__file__).resolve().parents[1]))
import kernel as K

ECHO = 'import sys; v=sys.stdin.read(); print(">>> " + v.split("] ")[1].split(" ->")[0]); '

def fresh():
    sp = K.genesis(Path(tempfile.mkdtemp(prefix="dalek-t-")))
    human = str(K.append(sp, "c0", "door", "", "#admit human").seq)
    d = str(K.append(sp, "c0", "door", "", "#decl D").seq)
    K.append(sp, "c0", "door", "", f"#in {d}")
    return sp, human, d

def tool(sp, program, ch="c0"):
    return str(K.append(sp, ch, "door", "", f"#decl U\n{program}").seq)

def go(sp, t): K.run(sp, {"L": t, "U": K.U, "X": t})

# T1 成员说的一切都是文本：成员写 #decl / #step / #genesis 不产生地址、不改游标、不起带子
def test_T1_members_cannot_speak_door_words():
    sp, human, d = fresh()
    echo = tool(sp, ECHO + 'print("#decl U\\nprint(1)")')          # 工具回一段以 #decl 开头的文本
    go(sp, K.tape([(f"c0/{human}", f">>> {echo}\ngo"), (f"c0/{human}", f">>> {human}\n#step actor={echo} upto=99"), (f"c0/{human}", "")]))
    c = sp.channels["c0"]
    assert all(m.sender == "door" for m in c.msgs if K.word_of(m)[0] == "decl")
    assert c.book[echo].cursor != 99 and len(sp.channels) == 1

# T2 构造只由 D 做，且每一步都是 door 写在目标账本上的行；D 的回执给请求者
def test_T2_D_constructs_and_logs():
    sp, human, d = fresh()
    au = str(K.append(sp, "c0", "door", "", "#decl L\n作者").seq)
    req = f">>> {d}\nbuild c1\npart {au}\ndecl U\nprint('hi')\nin #1\npeer c0\nstart 开始"
    go(sp, K.tape([(f"c0/{human}", req), ("c1/2", ""), (f"c0/{human}", "")]))   # c1 的作者副本收到 start 会被点名
    assert "c1" in sp.channels
    c1 = sp.channels["c1"]
    kinds = [(m.sender, K.word_of(m)[0]) for m in c1.msgs[:5]]
    assert kinds == [("door", "genesis"), ("door", "decl"), ("door", "decl"), ("door", "in"), ("door", "peer")]
    assert c1.receptionist == "2" and c1.msgs[5].to == "2" and c1.msgs[5].body == "开始"      # 启动交给接待员
    assert any(m.sender == d and m.to == human and "part" in m.body for m in sp.channels["c0"].msgs)  # 回执
    assert any(K.word_of(m)[0] == "peer" for m in sp.channels["c0"].msgs)                         # 双向接线

# T3 膜内地址不带 channel 名；Genome 逐字复制
def test_T3_local_addresses_verbatim_copy():
    sp, human, d = fresh()
    src = 'import sys\nprint(">>> 2")\nprint("x")'
    t = tool(sp, src)
    go(sp, K.tape([(f"c0/{human}", f">>> {d}\nbuild c1\npart {t}"), (f"c0/{human}", "")]))
    copied = [m for m in sp.channels["c1"].msgs if K.word_of(m)[0] == "decl"][0]
    assert copied.body == f"#decl U\n{src}" and all("/" not in a for a in sp.channels["c1"].book)

# T4 peer 投递：发给门的消息被 door 抄进对方账本、交给对方接待员；对方回信原路回来
def test_T4_peer_delivery():
    sp, human, d = fresh()
    au = str(K.append(sp, "c0", "door", "", "#decl L\n作者").seq)
    go(sp, K.tape([(f"c0/{human}", f">>> {d}\nbuild c1\ndecl U\n{ECHO}print('from c1')\nin #1\npeer c0"), (f"c0/{human}", "")]))
    gate = next(a.addr for a in sp.channels["c0"].book.values() if a.kind == "P" and a.prefix == "c1")
    go(sp, K.tape([(f"c0/{human}", f">>> {gate}\nping"), (f"c0/{human}", "")]))
    c1 = sp.channels["c1"]
    assert any(m.body == "ping" and m.to == c1.receptionist for m in c1.msgs)           # 抄进对方账本，给接待员
    assert any(m.body == "from c1" and m.sender == gate and m.to == sp.channels["c0"].receptionist for m in sp.channels["c0"].msgs)  # 回信从门回来，到 c0 的接待员

# T5 账本序：内部对话不能饿死排在前面的消息
def test_T5_ledger_order():
    sp, human, d = fresh()
    chatter = tool(sp, ECHO + 'n = v.count("ping"); print("ping" if n < 3 else "done")')
    quiet = tool(sp, ECHO + 'print("quiet ran")')
    go(sp, K.tape([(f"c0/{human}", f">>> {chatter}\nping"), (f"c0/{human}", f">>> {quiet}\nhi"), (f"c0/{human}", "")]))
    order = [m.sender for m in sp.channels["c0"].msgs if m.sender in (chatter, quiet)]
    assert order.index(quiet) <= 1

# T6 replay：多 channel 逐字相同；篡改 U 的结果即发散
def test_T6_replay():
    sp, human, d = fresh()
    echo = tool(sp, ECHO + 'print("echo:" + v.splitlines()[-1].split(": ",1)[1])')
    go(sp, K.tape([(f"c0/{human}", f">>> {echo}\nhello"), (f"c0/{human}", f">>> {d}\nbuild c1\ndecl U\n{ECHO}print('c1 ok')\nin #1\npeer c0\nstart go"), (f"c0/{human}", "")]))
    assert K.replay(sp.dir)
    p = sp.path("c0"); p.write_text(p.read_text(encoding="utf-8").replace("echo:hello", "echo:HELLO"), encoding="utf-8")
    assert not K.replay(sp.dir)

if __name__ == "__main__":
    ok = 0; names = sorted(n for n in dir() if n.startswith("test_"))
    for name in names:
        try: globals()[name](); ok += 1; print("PASS", name)
        except Exception: print("FAIL", name); traceback.print_exc(limit=2)
    print(f"{ok}/{len(names)}")
