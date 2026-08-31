import sys, json, tempfile, shutil
from pathlib import Path
sys.path.insert(0, "."); sys.path.insert(0, "t")
from genesis import G2, pack, construct, start
from init import up, say
KEY = sys.argv[1]
G = G2()
for c in G["channels"]:
    for m in c["members"]:
        if m.get("tag") == "L":
            _, _, rest = m["text"].partition("\n")
            m["text"] = f'ENDPOINT, MODEL, KEY = "https://api.deepseek.com/chat/completions", "deepseek-chat", "{KEY}"\n{rest}'
P = Path("/tmp/d0-real"); shutil.rmtree(P, ignore_errors=True)
pack(G, P); rt = up(P); construct(P, G); rt.run(); start(P, G); rt.run()
me = Path("/tmp/me-real"); shutil.rmtree(me, ignore_errors=True); me.mkdir()
rt.msg("c0", "door", "1", f"add c2 door tag=me\nfile:{me}#me"); rt.run()
n0 = rt.channels["c2"].seq
say(P, "c2", "task\n把 hello 写进 notes.txt 再读回来", frm=f"file:{me}#me"); rt.run()
for r in rt.channels["c2"].rows:
    if r["seq"] <= n0: continue
    if r["k"] == "step":
        print(f'{r["seq"]:>3} step  actor={r["actor"]}{" run="+str(r["run"]) if "run" in r else ""} err={r["err"].strip().splitlines()[-1] if r["err"] else ""}')
        for l in r["out"].splitlines(): print("      | " + l[:200])
    elif r["k"] == "place":
        print(f'{r["seq"]:>3} place addr={r["addr"]} kind={r["kind"]} tag={r.get("tag")} iface={r.get("iface")!r} by={r.get("by")}')
    else:
        b = r["body"].replace("\n", " ⏎ ")
        print(f'{r["seq"]:>3} msg   {r["from"]}→{r["to"]}{" run="+str(r["run"]) if "run" in r else ""}  {b[:300]}')
print("=== me inbox:", (me/"in"/"me.jsonl").read_text() if (me/"in"/"me.jsonl").exists() else "(empty)")
print("=== notes.txt:", repr((P/"notes.txt").read_text()) if (P/"notes.txt").exists() else "(none)")
print("=== c2 members:", [(a.addr, a.kind, a.tag) for a in rt.channels["c2"].actors.values()])
