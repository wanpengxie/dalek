# c0 的起子代（C）。这段源码是 G 里的 text；持有 bind=syscall,spawn。R 放入时 exec 一次得到常驻的 run(m)。
# 请求：spawn <name>（来自 r）
#   调用 1：向 A 要 decl（A 转给登记员；答案 decl\n<G> 经门回来，A 再转给我——那是下一次调用）
#   调用 2（decl\n<G>）：读账本找到还没办完的 spawn 请求；pack——G.world 写成 spawn/<name>/ 下的文件，G.json 原样放旁边（抄，不读）；
#      spawn spawn/<name>（子代 R 起来，根门开）；放两扇门（子代根门、子代第一个 channel）；
#      请求 A：build <根门> <本机地址>\n<G>（A 经门只造子代的 c0），拿到 built；
#      经根门发 msg <first>\nstart\n<G>——第一条消息带着 G：关门、切离，子代的 c0 自己长其余；告诉 r：spawned
import json, os


def run(m):
    head, _, rest = m["body"].partition("\n"); t = head.split()
    if t and t[0] == "spawn" and len(t) == 2:
        call("A", "decl"); return
    if not (t and t[0] == "decl" and rest.strip()):
        return
    rows = [json.loads(l) for l in call("0", "show").splitlines() if l]
    reqs = [r for r in rows if r["k"] == "msg" and r["to"] == me and r["body"].startswith("spawn ") and len(r["body"].split()) == 2]
    done = [r for r in rows if r["k"] == "msg" and r["from"] == me and r["body"].startswith("spawned ")]
    if len(reqs) <= len(done):
        return
    req = reqs[len(done)]; name, r = req["body"].split()[1], req["from"]
    G = json.loads(rest); d = os.path.join("spawn", name); os.makedirs(d, exist_ok=True)
    for f, src in G["world"].items():
        open(os.path.join(d, f), "w", encoding="utf-8").write(src)
    open(os.path.join(d, "G.json"), "w", encoding="utf-8").write(json.dumps(G, ensure_ascii=False, indent=1))
    first = G["channels"][0]["name"]; ad = os.path.abspath(d); P = os.path.abspath(".")
    call(f"spawn {d}")
    root = call(f"channel.add.actor {channel} door", f"file:{ad}#_root").split("/")[-1]
    door = call(f"channel.add.actor {channel} door tag={name}", f"file:{ad}#{first}").split("/")[-1]
    call("A", f"build {root} file:{P}#{channel}\n" + json.dumps(G, ensure_ascii=False))
    call(root, f"msg {first}\nstart\n" + json.dumps(G, ensure_ascii=False))
    call(r, f"spawned {ad} door={door}")
