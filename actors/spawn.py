# c0 的起子代（C）。这段源码是 G 里的 text；持有 bind=syscall,spawn。
# 请求：spawn <name>（来自 r）
#   A. 向接待员要 decl（接待员转给登记员，答案 decl\n<G> 再转回来）
#   B. 收到 G：pack——G.world 写成 spawn/<name>/ 下的文件，G.json 原样放旁边（B：抄，不读）
#      >>> spawn spawn/<name>                          子代 R 起来，根门开
#      放两扇门：一扇指向子代根门，一扇指向子代的第一个 channel
#   C. 收到门的地址后：把 G 交给接待员：build <根门> <本机地址>\n<G>      （A 经门只造子代的 c0）
#   D. 收到 built 后：经根门发 msg <first>\nstart\n<G> —— 第一条消息带着 G：关门、切离，子代的 c0 自己长其余。回 r：spawned
# 状态全在写给自己的 note 里（含 G）；一步没等到的 note 原样再写一次（跨 channel 的回答要几轮才到）。
import sys, json, os
v = json.load(sys.stdin)
ch, me, msgs, actors = v["channel"], v["me"], v["msgs"], v["actors"]
out, notes, doors, decls, built = [], [], [], [], None
P = os.path.abspath(".")
recept = next(a["addr"] for a in actors if a["in"] and not a["retired"])

for m in msgs:
    frm, body = m["from"], m["body"]
    head, _, rest = body.partition("\n"); t = head.split()
    if frm == "channel.add.actor":
        doors.append(body.split("/")[-1]); continue
    if frm == "spawn" or not t:
        continue
    if frm == me and t[0] == "note":
        notes.append(json.loads(rest))
    elif t[0] == "decl" and rest.strip():
        decls.append(rest)
    elif t[0] == "built":
        built = t[1]
    elif t[0] == "spawn" and len(t) == 2:
        out.append(f">>> {recept}\ndecl")
        out.append(f">>> {me}\nnote\n" + json.dumps({"s": "A", "r": frm, "name": t[1]}))

for n in notes:
    if (n["s"] == "A" and not decls) or (n["s"] == "B" and len(doors) < 2) or (n["s"] == "C" and not built):
        out.append(f">>> {me}\nnote\n" + json.dumps(n, ensure_ascii=False)); continue     # 没等到：带到下一步
    if n["s"] == "A" and decls:
        G = json.loads(decls[0]); d = os.path.join("spawn", n["name"]); os.makedirs(d, exist_ok=True)
        for f, src in G["world"].items():
            open(os.path.join(d, f), "w", encoding="utf-8").write(src)
        open(os.path.join(d, "G.json"), "w", encoding="utf-8").write(json.dumps(G, ensure_ascii=False, indent=1))
        first = G["channels"][0]["name"]; ad = os.path.abspath(d)
        out.append(f">>> spawn {d}")
        out.append(f">>> channel.add.actor {ch} door\nfile:{ad}#_root")
        out.append(f">>> channel.add.actor {ch} door\nfile:{ad}#{first}")
        out.append(f">>> {me}\nnote\n" + json.dumps({"s": "B", "r": n["r"], "ad": ad, "first": first, "G": G}, ensure_ascii=False))
    elif n["s"] == "B" and len(doors) >= 2:
        out.append(f">>> {recept}\nbuild {doors[0]} file:{P}#{ch}\n" + json.dumps(n["G"], ensure_ascii=False))
        out.append(f">>> {me}\nnote\n" + json.dumps({**n, "s": "C", "root": doors[0], "door": doors[1]}, ensure_ascii=False))
    elif n["s"] == "C" and built:
        out.append(f">>> {n['root']}\nmsg {n['first']}\nstart\n" + json.dumps(n["G"], ensure_ascii=False))
        out.append(f">>> {n['r']}\nspawned {n['ad']} door={n['door']}")

print("\n".join(out))
