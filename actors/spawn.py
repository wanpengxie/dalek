# c0 的起子代（C）。这段源码是 G 里的 text；持有 bind=syscall,spawn。
# 请求：spawn <name>（来自 r）
#   A. pack：把 G.world 写成 spawn/<name>/ 下的文件，G.json 原样放旁边（B：抄，不读）
#      >>> spawn spawn/<name>                          Ω：Exec.spawn(init, dir)；子代 R 起来，根门开着
#      放两扇门：一扇指向子代根门，一扇指向子代的第一个 channel
#   B. 收到门的地址后：把 G 交给本机的 realize：build <根门> <本机地址>\n<G>      （A 经门只造子代的 c0）
#   C. 收到 built 后：经根门发 msg <first> start\n<G> —— 第一条消息带着 G：关门、切离，子代的 c0 自己长其余。回 r：spawned
import sys, json, os
v = json.load(sys.stdin)
ch, me, msgs = v["channel"], v["me"], v["msgs"]
out, notes, doors, built = [], [], [], None
P = os.path.abspath(".")

for m in msgs:
    frm, body = m["from"], m["body"]
    t = body.split()
    if frm == "channel.add.actor":
        doors.append(body.split("/")[-1]); continue
    if frm == "spawn":
        continue
    if frm == me and t and t[0] == "note":
        notes.append(t[1:]); continue
    if t and t[0] == "built":
        built = t[1]; continue
    if t and t[0] == "spawn" and len(t) == 2:
        name = t[1]; d = os.path.join("spawn", name); os.makedirs(d, exist_ok=True)
        G = json.load(open("G.json", encoding="utf-8"))
        for f, src in G["world"].items():
            open(os.path.join(d, f), "w", encoding="utf-8").write(src)
        open(os.path.join(d, "G.json"), "w", encoding="utf-8").write(json.dumps(G, ensure_ascii=False, indent=1))
        first = G["channels"][0]["name"]; ad = os.path.abspath(d)
        out.append(f">>> spawn {d}")
        out.append(f">>> channel.add.actor {ch} door\nfile:{ad}#_root")
        out.append(f">>> channel.add.actor {ch} door\nfile:{ad}#{first}")
        out.append(f">>> {me}\nnote A {frm} {ad} {first}")

for n in notes:
    if n[0] == "A" and len(doors) >= 2:
        _, r, ad, first = n
        G = json.load(open("G.json", encoding="utf-8"))
        realize = str(G["channels"][0].get("receptionist", 1))
        out.append(f">>> {realize}\nbuild {doors[0]} file:{P}#{ch}\n" + json.dumps(G, ensure_ascii=False))
        out.append(f">>> {me}\nnote B {r} {ad} {first} {doors[0]} {doors[1]}")
    elif n[0] == "B" and built:
        _, r, ad, first, root, c0door = n
        G = json.load(open("G.json", encoding="utf-8"))
        out.append(f">>> {root}\nmsg {first}\nstart\n" + json.dumps(G, ensure_ascii=False))
        out.append(f">>> {r}\nspawned {ad} door={c0door}")

print("\n".join(out))
