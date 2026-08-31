import sys, json, time, os, signal, shutil, traceback
from pathlib import Path
sys.path.insert(0, "."); sys.path.insert(0, "t")
from test_c0 import rows, wait_child, has_msg, door_msg, c4_doors
from genesis import G2, pack, construct, start
from init import say
from omega import Exec
from runtime import Runtime

KEY = sys.argv[1]
def log(*a): print(time.strftime("[%H:%M:%S]"), *a, flush=True)

G = G2()
for c in G["channels"]:
    for m in c["members"]:
        if m.get("tag") == "L":
            _, _, rest = m["text"].partition("\n")
            m["text"] = f'ENDPOINT, MODEL, KEY = "https://api.deepseek.com/chat/completions", "deepseek-v4-pro", "{KEY}"\n{rest}'

P0 = Path("/tmp/t1-real"); shutil.rmtree(P0, ignore_errors=True)
me = Path("/tmp/me-t1"); shutil.rmtree(me, ignore_errors=True); me.mkdir()
pack(G, P0)
pids = [Exec.spawn(["init.py", str(P0), "--serve"], cwd=P0, log=P0 / "log")]
construct(P0, G); start(P0, G)
try:
    wait_child(P0, lambda c: "c2" in c.channels and len(rows(c, "c2", "place")) >= 3)
    say(P0, "c0", f"add c2 door tag=me\nfile:{me}#me")
    wait_child(P0, lambda c: len(rows(c, "c2", "place")) >= 4)
    log("dalek0 up (process)")
    TASK = f"""task
造两个器官装进本机并连线（新成员都是 def run(m) 的 python，命名空间有 call/me/channel）：
1) c3 的 hub：接待员，tag=hub，iface=hello <endpoint> | ping -> pong。行为：收 "hello <端点>"——读 0 who，若还没有 text 等于该端点的门，经 c0 的门发 "add c3 door\\n<端点>"，否则广播；收 "placed" 开头的消息就广播；广播 = 读 0 show 收集所有 "hello " 消息里的端点（按出现顺序去重），对成员表里每扇 local 不为真的门 call(它的 addr, "peers <端点们空格连接>")；收 "ping" 返回 "pong"。
2) c4 的 reporter：接待员，bind=spawn，tag=reporter，iface=tick | peers <endpoint..> | ping -> pong。行为：收 "start"/"tick"——找成员表里 tag=hub 的门，若账上（0 show）还没有我发给它的 "hello " 消息，就 call(它, "hello file:" + os.path.abspath(".") + "#" + channel) 然后返回；若是 "tick"：先看账上我发给 hub 门的最后一条 "ping" 之后有没有它回我的 "pong"，若有 ping 且没有 pong，就 call("spawn <hub 门 text 里 file: 和 # 之间的目录>")；然后 call(hub 门, "ping")，再对每扇其他非 local 门 call(它, "ping")；收 "peers <端点…>"——对每个不等于自己端点、且还没有同 text 门的端点，经 c0 的门 "add c4 door\\n<端点>"；收 "placed c4/<n>" 就 call(<n>, "ping")；收 "ping" 返回 "pong"。
3) 装法：经 c0 的门 "add c3 program in tag=hub iface=…\\n<源码>"、"add c4 program in bind=spawn tag=reporter iface=…\\n<源码>"、"peer c0 c3"、"peer c0 c4"，最后 "add c4 door tag=hub\\nfile:" + 本机目录 + "#c3"（本机目录用 U 跑 import os; print(os.path.abspath('.')) 得到，或写进源码里算）。
都装好后 done。可以先用 U test 验证源码。"""
    say(P0, "c2", TASK, frm=f"file:{me}#me")
    log("task sent, waiting for c3+c4+hub door (up to 10 min)...")
    wait_child(P0, lambda c: "c4" in c.channels and any(a.tag == "hub" and a.kind == "door" for a in c.channels.get("c4", type("x",(),{"actors":{}})()).actors.values()), timeout=600)
    d0 = Runtime(P0).load()
    log("c3 members:", [(a.addr, a.kind, a.tag, a.bind) for a in d0.channels["c3"].actors.values()])
    log("c4 members:", [(a.addr, a.kind, a.tag, a.bind) for a in d0.channels["c4"].actors.values()])
    log("done msg:", (me/"in"/"me.jsonl").exists() and (me/"in"/"me.jsonl").read_text()[-200:])
    say(P0, "c4", "tick")
    wait_child(P0, lambda c: has_msg(c, "c3", lambda r: r["body"].startswith("peers ")), timeout=120)
    log("hub broadcast ok")
    say(P0, "c0", "spawn d1"); say(P0, "c0", "spawn d2")
    P1, P2 = P0/"spawn"/"d1", P0/"spawn"/"d2"
    wait_child(P0, lambda c: sum(1 for r in rows(c, "c0", "msg") if r["from"] == "spawn") == 2, timeout=120)
    pids += [int(r["body"].split("pid=")[1]) for r in rows(Runtime(P0).load(), "c0", "msg") if r["from"] == "spawn"]
    log("children spawned", pids[1:])
    eps = {f"file:{p}#c4" for p in (P0, P1, P2)}
    for P in (P0, P1, P2):
        wait_child(P, lambda c, P=P: "c4" in c.channels and c4_doors(c) >= eps - {f"file:{P}#c4"}, timeout=300)
    log("all three machines grew doors to each other")
    for A, B in ((P1, P2), (P2, P1), (P1, P0)):
        wait_child(A, lambda c, B=B: door_msg(c, "c4", "pong", f"file:{B}#c4"), timeout=300)
    log("ping/pong across the population ok")
    os.killpg(pids[0], signal.SIGKILL); time.sleep(0.5)
    n_up = sum(1 for r in rows(Runtime(P0).load(), "c0", "msg") if r["from"] == "_root" and r["body"] == "up")
    log("dalek0 SIGKILLed; root up-count =", n_up)
    say(P1, "c4", "tick"); time.sleep(3)
    say(P1, "c4", "tick")
    wait_child(P1, lambda c: has_msg(c, "c4", lambda r: r["from"] == "spawn"), timeout=120)
    pids.append(int([r for r in rows(Runtime(P1).load(), "c4", "msg") if r["from"] == "spawn"][0]["body"].split("pid=")[1]))
    log("d1 respawned dalek0")
    wait_child(P0, lambda c: sum(1 for r in rows(c, "c0", "msg") if r["from"] == "_root" and r["body"] == "up") == n_up + 1, timeout=120)
    wait_child(P1, lambda c: door_msg(c, "c4", "pong", f"file:{P0}#c3"), timeout=300)
    log("dalek0 woke from its own ledger; hub pong restored. TASK 1 REAL: PASS")
except Exception:
    traceback.print_exc(limit=6)
    log("TASK 1 REAL: FAIL — dumping state")
    for name, p in [("d0", P0), ("d1", P0/"spawn"/"d1"), ("d2", P0/"spawn"/"d2")]:
        for ch in ("c2", "c3", "c4"):
            f = p/"h"/f"{ch}.jsonl"
            if f.exists():
                log(f"--- {name}/{ch} tail")
                for l in f.read_text().splitlines()[-8:]:
                    r = json.loads(l)
                    print("   ", r["seq"], r["k"], r.get("from",""), "→", r.get("to",""), (r.get("body") or r.get("err") or "")[:110].replace("\n"," ⏎ "), flush=True)
finally:
    for pid in pids:
        try: os.killpg(pid, signal.SIGTERM)
        except ProcessLookupError: pass
    log("cleaned up", pids)
