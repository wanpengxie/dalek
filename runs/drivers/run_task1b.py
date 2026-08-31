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
    log("all three woke from their ledgers (up rows appended)")
    for P in (P0, P1, P2): say(P, "c4", "tick")
    for A, B in ((P1, P2), (P2, P1), (P1, P0)):
        wait_child(A, lambda c, B=B: door_msg(c, "c4", "pong", f"file:{B}#c4"), timeout=120)
        wait_child(B, lambda c, A=A: door_msg(c, "c4", "ping", f"file:{A}#c4"), timeout=120)
    log("ping/pong across the population ok (after one tick heartbeat)")
    n_up = sum(1 for r in rows(Runtime(P0).load(), "c3", "msg") if r["body"] == "up")
    os.killpg(pids[0], signal.SIGKILL); time.sleep(0.5)
    log("dalek0 SIGKILLed; c3 up-count =", n_up)
    say(P1, "c4", "tick"); time.sleep(3)
    say(P1, "c4", "tick")
    wait_child(P1, lambda c: has_msg(c, "c4", lambda r: r["from"] == "spawn"), timeout=120)
    pids.append(int([r for r in rows(Runtime(P1).load(), "c4", "msg") if r["from"] == "spawn"][0]["body"].split("pid=")[1]))
    log("d1 respawned dalek0")
    wait_child(P0, lambda c: sum(1 for r in rows(c, "c3", "msg") if r["body"] == "up") == n_up + 1, timeout=120)
    say(P1, "c4", "tick")
    wait_child(P1, lambda c: door_msg(c, "c4", "pong", f"file:{P0}#c3"), timeout=120)
    log("dalek0 woke from its own ledger; hub pong restored. TASK 1 REAL: PASS")
except Exception:
    traceback.print_exc(limit=6)
    log("TASK 1B: FAIL — tails:")
    for name, p in [("d0", P0), ("d1", P1), ("d2", P2)]:
        for ch in ("c3", "c4"):
            f = p/"h"/f"{ch}.jsonl"
            if f.exists():
                log(f"--- {name}/{ch}")
                for l in f.read_text().splitlines()[-10:]:
                    r = json.loads(l)
                    print("   ", r["seq"], r["k"], r.get("from",""), "→", r.get("to",""), (r.get("body") or r.get("err") or "")[:100].replace("\n"," ⏎ "), flush=True)
finally:
    for pid in pids:
        try: os.killpg(pid, signal.SIGTERM)
        except ProcessLookupError: pass
    log("cleaned up", pids)
