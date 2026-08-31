import sys, json, time, os, signal, traceback
from pathlib import Path
sys.path.insert(0, "."); sys.path.insert(0, "t")
from test_c0 import rows, wait_child, has_msg, door_msg
from init import say
from omega import Exec
from runtime import Runtime

def log(*a): print(time.strftime("[%H:%M:%S]"), *a, flush=True)
P0 = Path("/tmp/t1-real"); P1, P2 = P0/"spawn/d1", P0/"spawn/d2"
ups = {P: sum(1 for r in rows(Runtime(P).load(), "c4", "msg") if r["body"] == "up") for P in (P0, P1, P2)}
pids = [Exec.spawn(["init.py", str(P), "--serve"], cwd=P, log=P/"log") for P in (P0, P1, P2)]
try:
    for P in (P0, P1, P2):
        wait_child(P, lambda c, P=P: sum(1 for r in rows(c, "c4", "msg") if r["body"] == "up") == ups[P] + 1, timeout=60)
    log("three machines woke")
    old_src = rows(Runtime(P1).load(), "c4", "place")[0]["text"]
    TASK = f"""task
修复本机 c4 的 reporter（tag=reporter，地址 c4/1）。它有一个 bug：调世界动词时写了 call("spawn", 目录)——两个参数。ABI 里动词调用只有一个参数，动词和它的参数写在同一个头字符串里：call("spawn " + 目录)。除此之外行为保持：收 ping 回 pong；收 start/up/tick 时若账上还没给 tag=hub 的门发过 hello 就发 hello file:<os.path.abspath('.')>#<channel>；收 tick 还要：若我发给 hub 门的最后一条 ping 之后没有收到它的 pong，就 call("spawn " + 从 hub 门 text 的 file: 和 # 之间取出的目录)；然后给 hub 门和每扇非 local 门发 ping。收 peers <端点…> 对每个不是自己、还没有同 text 门的端点经 c0 门 "add c4 door\\n<端点>"；收 placed c4/<n> 就 call(<n>, "ping")。
旧源码如下（供参考，修 spawn 那一处即可）：
{old_src}
装法：经 c0 的门 "add c4 program in bind=spawn tag=reporter iface=tick | peers <endpoint..> | ping -> pong\\n<新源码>"（in 让它接任接待员），等 placed c4/<新地址> 到来后，经 c0 的门 "retire c4/1" 退役旧的，然后 done。"""
    say(P1, "c2", TASK)
    wait_child(P1, lambda c: any(r["k"] == "retire" and r["addr"] == "1" for c4 in ["c4"] for r in rows(c, "c4", "retire")), timeout=600)
    d1 = Runtime(P1).load()
    log("repaired: c4 members:", [(a.addr, a.tag, a.retired, a.bind) for a in d1.channels["c4"].actors.values()], "receptionist:", d1.channels["c4"].receptionist)
    say(P1, "c4", "tick"); time.sleep(2)                       # 新 reporter 第一脚：re-hello
    os.killpg(pids[0], signal.SIGKILL); time.sleep(0.5)
    n_up = sum(1 for r in rows(Runtime(P0).load(), "c3", "msg") if r["body"] == "up")
    log("dalek0 SIGKILLed; c3 up-count =", n_up)
    for i in range(6):
        say(P1, "c4", "tick"); time.sleep(3)
        if has_msg(Runtime(P1).load(), "c4", lambda r: r["from"] == "spawn"): break
    wait_child(P1, lambda c: has_msg(c, "c4", lambda r: r["from"] == "spawn"), timeout=60)
    pids.append(int([r for r in rows(Runtime(P1).load(), "c4", "msg") if r["from"] == "spawn"][0]["body"].split("pid=")[1]))
    log("d1 respawned dalek0 (spawn receipt in its ledger)")
    wait_child(P0, lambda c: sum(1 for r in rows(c, "c3", "msg") if r["body"] == "up") == n_up + 1, timeout=120)
    say(P1, "c4", "tick")
    wait_child(P1, lambda c: door_msg(c, "c4", "pong", f"file:{P0}#c3"), timeout=120)
    log("dalek0 woke from its own ledger; hub pong restored. TASK 1 REAL: PASS")
except Exception:
    traceback.print_exc(limit=6)
    log("TASK 1C: FAIL — tails:")
    for name, p in [("d1c2", P1/"h/c2.jsonl"), ("d1c4", P1/"h/c4.jsonl"), ("d0c3", P0/"h/c3.jsonl")]:
        if p.exists():
            log("---", name)
            for l in p.read_text().splitlines()[-12:]:
                r = json.loads(l)
                print("   ", r["seq"], r["k"], r.get("from",""), "→", r.get("to",""), (r.get("body") or r.get("err") or "")[:110].replace("\n"," ⏎ "), flush=True)
finally:
    for pid in pids:
        try: os.killpg(pid, signal.SIGTERM)
        except ProcessLookupError: pass
    log("cleaned up", pids)
