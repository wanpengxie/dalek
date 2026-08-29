# c0 的起子代（C）。这段源码是 G 里的 text；持有 bind=place,spawn。
# 请求：spawn <name>
#   1. pack：把 omega.py / runtime.py / init.py（世界）和 G.json（描述，原样）抄进 spawn/<name>/
#   2. >>> spawn spawn/<name>          介质动作：Exec.spawn(init, dir)
#   3. >>> place <本 channel> door      放一扇门指向子代的第一个 channel
#   4. 下一步收到门的地址后，经门踢一脚："realize G.json"。踢完，义务结束。
import sys, json, os, shutil
v = json.load(sys.stdin)
ch, me, msgs = v["channel"], v["me"], v["msgs"]
out, notes, door, pid = [], [], None, None

for m in msgs:
    frm, body = m["from"], m["body"]
    t = body.split()
    if frm == "place" and t:
        door = body.split("/")[-1]; continue
    if frm == "spawn":
        pid = body; continue
    if frm == me and t and t[0] == "note":
        notes.append((t[1], t[2])); continue
    if t and t[0] == "spawn" and len(t) == 2:
        name = t[1]; d = os.path.join("spawn", name); os.makedirs(d, exist_ok=True)
        for f in ("omega.py", "runtime.py", "init.py", "G.json"):
            shutil.copyfile(f, os.path.join(d, f))
        G = json.load(open("G.json", encoding="utf-8"))
        out.append(f">>> spawn {d}")
        out.append(f">>> place {ch} door\nfile:{os.path.abspath(d)}#{G['channels'][0]['name']}")
        out.append(f">>> {me}\nnote {frm} {os.path.abspath(d)}")

if door and notes:
    who, d = notes[-1]
    out.append(f">>> {door}\nrealize G.json")
    out.append(f">>> {who}\nspawned {d} {pid or ''} door={door}".rstrip())

print("\n".join(out))
