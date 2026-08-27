"""T_dalek：T1–T7。全部用录音带 apply，秒级、确定。
跑法（无 pytest）：python3 t_dalek/test_t.py"""
from __future__ import annotations
import json, sys, tempfile, traceback
from pathlib import Path
sys.path.insert(0, str(Path(__file__).resolve().parents[1]))
import kernel as K

ECHO_WHO = 'import sys; v=sys.stdin.read(); print(">>> " + v.split("] ")[1].split(" ->")[0]); '

def fresh() -> K.Space:
    return K.genesis(Path(tempfile.mkdtemp(prefix="dalek-t-")))

def with_tool(sp: K.Space, program: str) -> str:
    return K.append(sp, "c0", "door", "", f"#decl U\n{program}").addr()

def go(sp, t): K.run(sp, {"L": t, "U": K.U, "X": t})

# ---------------------------------------------------------------- T1 H₀ 携带 K 源码
def test_T1_genesis_carries_K():
    sp = fresh()
    body = sp.ledger[0].body
    assert body.startswith("#genesis") and "def run(" in body and "def append(" in body
    ns = {}
    exec(compile(body.split("K=\n", 1)[1], "K-from-H", "exec"), ns)
    assert callable(ns["run"]) and callable(ns["replay"])

# ---------------------------------------------------------------- T2 restart = replay；篡改确定根的结果即发散
def test_T2_replay_identical_and_tamper_diverges():
    sp = fresh()
    human = K.append(sp, "c0", "door", "", "#admit human").addr()
    tool = with_tool(sp, ECHO_WHO + 'print("echo:" + v.splitlines()[-1].split(": ",1)[1])')
    go(sp, K.tape([(human, f">>> {tool}\nhello"), (human, "")]))
    assert any("echo:hello" in m.body for m in sp.ledger if m.sender == tool)
    assert K.replay(sp.dir)
    p = sp.path; p.write_text(p.read_text(encoding="utf-8").replace("echo:hello", "echo:HELLO"), encoding="utf-8")
    assert not K.replay(sp.dir)

# ---------------------------------------------------------------- T3 成员不能说内核的词；文法里写不出章
def test_T3_members_cannot_speak_kernel_words():
    sp = fresh()
    human = K.append(sp, "c0", "door", "", "#admit human").addr()
    go(sp, K.tape([(human, ">>> L\n#step actor=L upto=999"), ("L", ""), (human, ">>> L\n#genesis"), ("L", ""), (human, "")]))
    c = sp.channels["c0"]
    assert c.book["L"].cursor != 999                      # 成员说的 #step 只是文本
    assert len(sp.channels) == 1 and len(c.msgs) > 3       # 成员说的 #genesis 没有清空机器
    assert K.parse(">>> L\nsender: door\nseq: 1") == [("L", "sender: door\nseq: 1")]

# ---------------------------------------------------------------- T4 U 之间没有 H 之外的通道
def test_T4_no_shared_scratch():
    sp = fresh()
    human = K.append(sp, "c0", "door", "", "#admit human").addr()
    w = with_tool(sp, 'open("secret","w").write("x"); ' + ECHO_WHO + 'print("wrote")')
    r = with_tool(sp, 'import os; ' + ECHO_WHO + 'print("sees:" + str(os.path.exists("secret")))')
    go(sp, K.tape([(human, f">>> {w}\ngo"), (human, f">>> {r}\ngo"), (human, f">>> {w}\ngo"), (human, f">>> {r}\ngo"), (human, "")]))
    assert all("sees:False" in m.body for m in sp.ledger if m.sender == r)

# ---------------------------------------------------------------- T5 只有 #decl M 能造机器；坏配方整条拒绝；不越界；身份由描述决定
def test_T5_creation_and_locality():
    sp = fresh()
    human = K.append(sp, "c0", "door", "", "#admit human").addr()
    tool = with_tool(sp, "print('')")
    go(sp, K.tape([(human, ">>> c9/1\nhi"),
                   (human, f">>> {human}\n#decl M\npart c0/99"),                 # 零件不存在
                   (human, f">>> {human}\n#decl M\npart {tool}\nin c0/77"),      # 接待员不在零件里
                   (human, f">>> {human}\n#decl M\npart {tool}\nstart\nhello"),  # 好配方
                   (human, "")]))
    assert not any(m.to == "c9/1" for m in sp.ledger)                            # 越界丢弃
    decl = next(m for m in sp.ledger if m.body.startswith("#decl M") and m.sender == human)
    child = decl.addr().replace("/", ".")
    assert list(sp.channels) == ["c0", child]                                     # 恰造一台，id = 声明地址
    c1 = sp.channels[child]
    assert c1.receptionist == f"{child}/3" and c1.out == human
    assert any(m.sender == f"P:{child}" and m.to == f"{child}/3" for m in c1.msgs) # 启动消息送达接待员

# ---------------------------------------------------------------- T6 公平性 = 账本顺序：内部对话不能饿死排在前面的消息
def test_T6_ledger_order_fairness():
    sp = fresh()
    human = K.append(sp, "c0", "door", "", "#admit human").addr()
    chatter = with_tool(sp, ECHO_WHO + 'n = v.count("ping"); print("ping" if n < 3 else "done")')  # 给自己回 3 次
    quiet = with_tool(sp, ECHO_WHO + 'print("quiet ran")')
    go(sp, K.tape([(human, f">>> {chatter}\nping"), (human, f">>> {quiet}\nhi"), (human, "")]))
    order = [m.sender for m in sp.ledger if m.sender in (chatter, quiet)]
    assert "quiet ran" in "".join(m.body for m in sp.ledger if m.sender == quiet)
    assert order.index(quiet) <= 1                                                  # quiet 在 chatter 的第二轮之前

# ---------------------------------------------------------------- T7 两问封闭；H 可问询且 msg 精确
def test_T7_two_questions_and_H():
    sp = fresh()
    human = K.append(sp, "c0", "door", "", "#admit human").addr()
    tool = with_tool(sp, "print('')")
    go(sp, K.tape([(human, f">>> {human}\n#decl M\npart {tool}\nstart\nhi"), (human, "")]))
    for c in sp.channels.values():
        for a in c.book.values():
            if a.id in K.ROOTS: continue
            m = next(x for x in c.msgs if x.addr() == a.id or (a.root == "P" and x.body.startswith("#admit parent")))
            if a.root in ("X", "P"): assert m.body.startswith("#admit")
            else: assert m.body.startswith("#decl")
    go(sp, K.tape([(human, ">>> H\nbook"), (human, ">>> H\nmsg c0/3"), (human, "")]))
    ans = [m for m in sp.channels["c0"].msgs if m.sender == "H"]
    assert human in ans[0].body and tool in ans[0].body
    exact = json.loads(ans[1].body.splitlines()[1])
    assert exact["body"] == sp.channels["c0"].msgs[2].body                            # decode(H.msg(addr)) = ledger[addr]

if __name__ == "__main__":
    ok = 0; names = sorted(n for n in dir() if n.startswith("test_"))
    for name in names:
        try: globals()[name](); ok += 1; print("PASS", name)
        except Exception: print("FAIL", name); traceback.print_exc(limit=2)
    print(f"{ok}/{len(names)}")
