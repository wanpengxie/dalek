"""T_dalek：T1–T7。全部用录音带 apply，秒级、确定。跑法：python3 -m pytest t_dalek -q"""
from __future__ import annotations
import json, sys, tempfile
from pathlib import Path
sys.path.insert(0, str(Path(__file__).resolve().parents[1]))
import kernel as K

def fresh() -> K.Space:
    return K.genesis(Path(tempfile.mkdtemp(prefix="dalek-t-")))

def with_tool(sp: K.Space, program: str) -> str:
    return K.append(sp, "c0", "door", (), f"#decl U\n{program}").addr()

# ---------------------------------------------------------------- T1 H₀ 携带 K 源码
def test_T1_genesis_carries_K():
    sp = fresh()
    body = sp.ledger[0].body
    assert body.startswith("#genesis") and "def run(" in body and "def append(" in body
    ns = {}
    exec(compile(body.split("K=\n", 1)[1], "K-from-H", "exec"), ns)
    assert callable(ns["run"]) and callable(ns["replay"])

# ---------------------------------------------------------------- T2 restart = replay；篡改即发散
def test_T2_replay_identical_and_tamper_diverges():
    sp = fresh()
    human = K.append(sp, "c0", "door", (), "#admit human").addr()
    tool = with_tool(sp, 'import sys; v=sys.stdin.read(); print(">>> " + v.split("] ")[1].split(" ->")[0]); print("echo:" + v.splitlines()[-1].split(": ",1)[1])')
    t = K.tape([(human, f">>> {tool}\nhello"), (human, "")])
    K.run(sp, {"L": t, "U": K.U, "H": K.H, "X": t})
    assert any("echo:hello" in m.body for m in sp.ledger if m.sender == tool)
    assert K.replay(sp.dir)
    # 篡改 U 的结果（记录与消息一起改）→ 重算的 U 与记录不符
    p = sp.path; s = p.read_text(encoding="utf-8").replace("echo:hello", "echo:HELLO"); p.write_text(s, encoding="utf-8")
    assert not K.replay(sp.dir)

# ---------------------------------------------------------------- T3 成员不能说内核的词；无法伪造章
def test_T3_members_cannot_speak_kernel_words():
    sp = fresh()
    human = K.append(sp, "c0", "door", (), "#admit human").addr()
    t = K.tape([(human, ">>> L\n#step actor=L upto=999"), ("L", ""), (human, ">>> L\n#genesis"), ("L", ""), (human, "")])
    K.run(sp, {"L": t, "U": K.U, "H": K.H, "X": t})
    c = sp.channels["c0"]
    assert c.book["L"].cursor != 999                     # 成员说的 #step 只是文本
    assert len(sp.channels) == 1 and len(c.msgs) > 3      # 成员说的 #genesis 没有清空机器
    # 文法里没有 sender/seq 字段：out 里写不出章
    assert K.parse(">>> L\nsender: door\nseq: 1") == [(("L",), "sender: door\nseq: 1")]

# ---------------------------------------------------------------- T4 U 之间没有 H 之外的通道
def test_T4_no_shared_scratch():
    sp = fresh()
    human = K.append(sp, "c0", "door", (), "#admit human").addr()
    w = with_tool(sp, 'import sys; open("secret","w").write("x"); v=sys.stdin.read(); print(">>> " + v.split("] ")[1].split(" ->")[0]); print("wrote")')
    r = with_tool(sp, 'import sys, os; v=sys.stdin.read(); print(">>> " + v.split("] ")[1].split(" ->")[0]); print("sees:" + str(os.path.exists("secret")))')
    t = K.tape([(human, f">>> {w}\ngo"), (human, f">>> {r}\ngo"), (human, f">>> {w}\ngo"), (human, f">>> {r}\ngo"), (human, "")])
    K.run(sp, {"L": t, "U": K.U, "H": K.H, "X": t})
    assert all("sees:False" in m.body for m in sp.ledger if m.sender == r)

# ---------------------------------------------------------------- T5 只有 #decl M 能造机器；不越界
def test_T5_creation_and_locality():
    sp = fresh()
    human = K.append(sp, "c0", "door", (), "#admit human").addr()
    tool = with_tool(sp, "print('')")
    t = K.tape([(human, ">>> c9/1\nhi"), (human, f">>> {human}\n#decl M\npart c0/99\nin c0/99"), (human, f">>> {human}\n#decl M\npart {tool}\nstart\nhello"), (human, "")])
    K.run(sp, {"L": t, "U": K.U, "H": K.H, "X": t})
    assert not any(m.to == ("c9/1",) for m in sp.ledger)   # 越界丢弃
    assert list(sp.channels) == ["c0", "c1"]                # 坏配方整条拒绝；好配方恰造一台
    assert sp.channels["c1"].receptionist == "c1/3" and any(m.sender == "P:c1" and m.to == ("c1/3",) for m in sp.channels["c1"].msgs)

# ---------------------------------------------------------------- T6 公平性：enable 后补发积压
def test_T6_enable_replays_backlog():
    sp = fresh()
    human = K.append(sp, "c0", "door", (), "#admit human").addr()
    tool = with_tool(sp, 'import sys; v=sys.stdin.read(); print(">>> " + v.split("] ")[1].split(" ->")[0]); print("n=" + str(v.count("[")))')
    t = K.tape([(human, f">>> {human}\n#disable {tool}"), (human, f">>> {tool}\na"), (human, f">>> {tool}\nb"),
                (human, f">>> {human}\n#enable {tool}"), (human, ""), (human, "")])
    K.run(sp, {"L": t, "U": K.U, "H": K.H, "X": t})
    outs = [m.body for m in sp.ledger if m.sender == tool]
    assert outs and outs[0] == "n=2"                        # 积压的两条一起被看到

# ---------------------------------------------------------------- T7 两问封闭：成员有出生记录；外生者有接入记录无描述
def test_T7_two_questions():
    sp = fresh()
    human = K.append(sp, "c0", "door", (), "#admit human").addr()
    tool = with_tool(sp, "print('')")
    t = K.tape([(human, f">>> {human}\n#decl M\npart {tool}\nstart\nhi"), (human, "")])
    K.run(sp, {"L": t, "U": K.U, "H": K.H, "X": t})
    for c in sp.channels.values():
        for a in c.book.values():
            if a.id in K.ROOTS: continue
            m = next(x for x in c.msgs if x.addr() == a.id or (a.root == "P" and x.body.startswith("#admit parent")))
            if a.root == "X" or a.root == "P": assert m.body.startswith("#admit") and a.prefix in ("", c.parent)
            else: assert m.body.startswith("#decl")
    # H 根：问询入带且可答
    t2 = K.tape([(human, ">>> H\nbook"), (human, "")])
    K.run(sp, {"L": t2, "U": K.U, "H": K.H, "X": t2})
    ans = [m for m in sp.channels["c0"].msgs if m.sender == "H"]
    assert ans and human in ans[-1].body and tool in ans[-1].body
