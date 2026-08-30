# c1 的登记员（B 的另一半）。这段源码是 G 里的 text；bind=ledger：视图里带写给它的全部历史消息。
# 它只折叠自己的账本，不碰文件。进账三种，全部由 c0 经门送来：
#   born\n<G>                                         出生时的基因组（含 world）——第一行
#   placed <ch> <addr> <kind> [in] [bind=…]\n<text>    一次 add 落地后
#   retired <ch>/<addr>                               一次 retire 落地后
# 请求：decl → 回 decl\n<G_t>：G₀ ⊕ placed ⊖ retired。channel 按出生顺序 + 首次出现顺序；成员按地址；
#   text 是本机 channel 名的门折成 peers，指向外面端点的门是 kind=door 的成员；退役的不输出。
import sys, json
v = json.load(sys.stdin)
ch, me, msgs, history = v["channel"], v["me"], v["msgs"], v.get("history", [])
out = []


def fold(rows):
    G, entries = None, {}                       # entries[ch] = [{addr, kind, text, bind, in, retired}]
    for m in rows:
        head, _, rest = m["body"].partition("\n"); t = head.split()
        if not t:
            continue
        if t[0] == "born":
            G = json.loads(rest)
            for c in G["channels"]:
                entries[c["name"]] = [{"addr": str(i + 1), "kind": x["kind"], "text": x["text"], "bind": x.get("bind", []),
                                       "in": i + 1 == c.get("receptionist", 1), "retired": False}
                                      for i, x in enumerate(c["members"])]
            for a, b in G.get("peers", []):
                entries.setdefault(a, []).append({"addr": None, "kind": "door", "text": b, "bind": [], "in": False, "retired": False})
                entries.setdefault(b, []).append({"addr": None, "kind": "door", "text": a, "bind": [], "in": False, "retired": False})
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
                    x["retired"] = True
                    if x["in"]: x["in"] = False
    return G, entries


def decl(G, entries):
    channels, peers, seen = [], [], set()
    for name, e in entries.items():
        members, recept = [], None
        for x in sorted(e, key=lambda x: int(x["addr"]) if x["addr"] else 0):
            if x["retired"]:
                continue
            if x["kind"] == "door" and ":" not in x["text"]:
                key = frozenset((name, x["text"]))
                if key not in seen:
                    seen.add(key); peers.append([name, x["text"]])
                continue
            m = {"kind": x["kind"], "text": x["text"]}
            if x["bind"]: m["bind"] = x["bind"]
            members.append(m)
            if x["in"]: recept = len(members)
        c = {"name": name, "members": members}
        if recept: c["receptionist"] = recept
        channels.append(c)
    return {"world": G["world"], "channels": channels, "peers": peers}


G, entries = fold(history)
for m in msgs:
    if m["body"].strip() == "decl" and G:
        out.append(f">>> {m['from']}\ndecl\n" + json.dumps(decl(G, entries), ensure_ascii=False))
print("\n".join(out))
