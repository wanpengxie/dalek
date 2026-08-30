# c1 的登记员：基因组登记处，不是镜子。这段源码是 G 里的 text；bind=ledger：视图里带写给它的全部历史消息。
# 它只折自己的账本，不碰文件。形态事实三种，只认来自本机 channel 的门（成员表里 local 的门，含已退役的——历史的解释不随拓扑漂）：
#   born\n<G>                                         脐带放的：world + 第一个 channel 的成员（地址 = 序号）
#   placed <ch> <addr> <kind> [in] [bind=…]\n<text>    c0 的手放的，带真实地址（出生时长出的其余器官也走这条）
#   retired <ch>/<addr>
# 请求 decl（任何人）→ 回 decl\n<G_t>：G₀ ⊕ placed ⊖ retired。
#   channel 按出生 + 首次出现顺序；成员按地址；门就是成员（kind=door，text 原样），不折 peers；接待员显式，没有就没有；
#   退役的不输出；world 原样来自 born。这就是 π(A_t)：去掉不经 c0 放的门（出生证明、生子的临时门）、去掉退役、地址重排。
import sys, json
v = json.load(sys.stdin)
ch, me, msgs, history, actors = v["channel"], v["me"], v["msgs"], v.get("history", []), v["actors"]
out = []
fact_doors = {a["addr"] for a in actors if a["kind"] == "door" and a.get("local")}


def fold(rows):
    G, entries = None, {}                       # entries[ch] = [{addr, kind, text, bind, in, retired}]
    for m in rows:
        if m["from"] not in fact_doors:
            continue                                # 不是本机 channel 经门说的，不是形态事实
        head, _, rest = m["body"].partition("\n"); t = head.split()
        if not t:
            continue
        if t[0] == "born":
            G = json.loads(rest)
            for c in G["channels"]:
                entries[c["name"]] = [{"addr": str(i + 1), "kind": x["kind"], "text": x["text"], "bind": x.get("bind", []),
                                       "in": i + 1 == c.get("receptionist"), "retired": False}
                                      for i, x in enumerate(c["members"])]
        elif t[0] == "placed" and len(t) >= 4:
            flags = t[4:]
            bind = next((f[5:].split(",") for f in flags if f.startswith("bind=")), [])
            e = entries.setdefault(t[1], [])
            if "in" in flags:
                for x in e: x["in"] = False
            e.append({"addr": t[2], "kind": t[3], "text": rest, "bind": bind, "in": "in" in flags, "retired": False})
        elif t[0] == "retired" and len(t) == 2:
            cn, _, addr = t[1].partition("/")
            for x in entries.get(cn, []):
                if x["addr"] == addr:
                    x["retired"] = True; x["in"] = False
    return G, entries


def decl(G, entries):
    channels = []
    for name, e in entries.items():
        members, recept = [], None
        for x in sorted(e, key=lambda x: int(x["addr"])):
            if x["retired"]:
                continue
            m = {"kind": x["kind"], "text": x["text"]}
            if x["bind"]: m["bind"] = x["bind"]
            members.append(m)
            if x["in"]: recept = len(members)
        c = {"name": name, "members": members}
        if recept: c["receptionist"] = recept
        channels.append(c)
    return {"world": G["world"], "channels": channels, "peers": []}


G, entries = fold(history)
for m in msgs:
    if m["body"].strip() == "decl" and G:
        out.append(f">>> {m['from']}\ndecl\n" + json.dumps(decl(G, entries), ensure_ascii=False))
print("\n".join(out))
