# c1 的登记员：基因组登记处，不是镜子。这段源码是 G 里的 text。没有任何绑定：它读账本和别人一样——问地址 0。
# 它只折自己的账本，不碰文件。形态登记三种（c0 的宣称——基因组的 commit，不是 R 的事实），只认来自本机 channel 的门（成员表里 local 的门，含已退役的——历史的解释不随拓扑漂）：
#   born\n<G>                                         脐带放的：world + 第一个 channel 的成员（地址 = 序号）
#   placed <ch> <addr> <kind> [in] [bind=…]\n<text>    c0 的手放的，带真实地址（出生时长出的其余器官也走这条）
#   retired <ch>/<addr>
# 请求 decl（任何人）→ 两步：>>> 0 show（问账本）→ 下一步拿到 rows，折叠，回 decl\n<G_t>：G₀ ⊕ placed ⊖ retired。
#   channel 按出生 + 首次出现顺序；成员按地址；门就是成员（kind=door，text 原样），不折 peers；接待员显式，没有就没有；
#   退役的不输出；world 原样来自 born。这就是 π(A_t)：去掉不经 c0 放的门（出生证明、生子的临时门）、去掉退役、地址重排。
import sys, json
v = json.load(sys.stdin)
ch, me, msgs, actors = v["channel"], v["me"], v["msgs"], v["actors"]
out = []
fact_doors = {a["addr"] for a in actors if a["kind"] == "door" and a.get("local")}


def fold(rows):
    G, entries = None, {}                       # entries[ch] = [{addr, kind, text, bind, in, retired}]
    for m in rows:
        if m["from"] not in fact_doors:
            continue                                # 不是本机 channel 经门说的，不算登记
        head, _, rest = m["body"].partition("\n"); t = head.split()
        if not t:
            continue
        if t[0] == "born":
            if G is not None:
                continue                                # 只出生一次
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


shown = next((m for m in msgs if m["from"] == "0"), None)                    # 账本来了
if shown:
    rows = shown["rows"]
    prev = max((r["seq"] for r in rows if r["k"] == "msg" and r["from"] == "0" and r["to"] == me), default=0)   # 上一次看到哪
    G, entries = fold([r for r in rows if r["k"] == "msg" and r["to"] == me])
    for r in rows:                                                              # 这一次看之前到的 decl 请求，逐个答
        if r["k"] == "msg" and r["to"] == me and r["seq"] > prev and r["body"].strip() == "decl" and G:
            out.append(f">>> {r['from']}\ndecl\n" + json.dumps(decl(G, entries), ensure_ascii=False))
edge = shown["seq"] if shown else 0
if any(m["from"] != "0" and m["body"].strip() == "decl" and m["seq"] > edge for m in msgs):   # 看之后又来的请求：再看一次
    out.append(">>> 0\nshow")
print("\n".join(out))
