# c1 的登记员：基因组登记处，不是镜子。这段源码是 G 里的 text；R 放入时 exec 一次得到常驻的 run(m)。没有任何绑定：它读账本和别人一样——call("0", "show")。
# 它只折自己的账本，不碰文件。形态登记三种（c0 的宣称——基因组的 commit，不是 R 的事实），只认来自本机 channel 的门（0 交出的 place 行 local 的门，含已退役——历史的解释不随拓扑漂）：
#   born\n<G>                                         脐带放的：world + 第一个 channel 的成员（G 中 tag 已唯一）
#   placed <ch> <tag> <kind> [in] [bind=…] [iface=…]\n<text>    c0 的手放的，tag 是 R 实际分配的 channel 内唯一逻辑地址
#   retired <ch>/<tag>
# 请求 decl（任何人）→ 读账本、折叠、返回 decl\n<G_t>：G₀ ⊕ placed ⊖ retired。
# 收到 c0/A 经门发来的 reconcile → 对账：对 decl 里每个 channel 经门向 c0 发 rebuild <name>\n<channel>。
# 物理 up 只在 c0 边界留痕；c1 看到的永远是 actor 协作，期望 = 登记处，实际 = R 折的。
#   channel 按出生 + 首次出现顺序；成员按登记顺序；门就是成员（kind=door，text 原样），不折 peers；接待员显式，没有就没有；
#   退役的不输出；world 原样来自 born。这就是 π(A_t)：去掉不经 c0 放的门（出生证明、生子的临时门）、去掉退役；数字地址不进入登记。
import json


def fold(rows):
    fact_doors = {r["addr"] for r in rows if r["k"] == "place" and r["kind"] == "door" and r.get("local")}
    G, entries = None, {}                       # entries[ch] = [{kind, text, bind, tag, iface, in, retired}]
    for r in rows:
        if r["k"] != "msg" or r["to"] != me or r["from"] not in fact_doors:
            continue                                # 不是本机 channel 经门说的，不算登记
        head, _, rest = r["body"].partition("\n"); t = head.split()
        if not t:
            continue
        if t[0] == "born":
            if G is not None:
                continue                                # 只出生一次
            G = json.loads(rest)
            for c in G["channels"]:
                entries[c["name"]] = [{"kind": x["kind"], "text": x["text"], "bind": x.get("bind", []),
                                       "tag": x.get("tag"), "iface": x.get("iface"),
                                       "in": i + 1 == c.get("receptionist"), "retired": False}
                                      for i, x in enumerate(c["members"])]
        elif t[0] == "placed" and len(t) >= 4:
            flags = t[4:]; bind, tag, iface = [], t[2], None
            for i, f in enumerate(flags):
                if f.startswith("bind="): bind = f[5:].split(",")
                elif f.startswith("iface="): iface = " ".join([f[6:], *flags[i + 1:]]); break
            e = entries.setdefault(t[1], [])
            if "in" in flags:
                for x in e: x["in"] = False
            e.append({"kind": t[3], "text": rest, "bind": bind, "tag": tag, "iface": iface,
                      "in": "in" in flags, "retired": False})
        elif t[0] == "retired" and len(t) == 2:
            cn, _, tag = t[1].partition("/")
            for x in reversed(entries.get(cn, [])):
                if x["tag"] == tag and not x["retired"]:
                    x["retired"] = True; x["in"] = False
                    break
    return G, entries


def decl(G, entries):
    channels = []
    for name, e in entries.items():
        members, recept = [], None
        for x in e:
            if x["retired"]:
                continue
            mm = {"kind": x["kind"], "text": x["text"]}
            if x["bind"]: mm["bind"] = x["bind"]
            if x["tag"]: mm["tag"] = x["tag"]
            if x["iface"]: mm["iface"] = x["iface"]
            members.append(mm)
            if x["in"]: recept = len(members)
        c = {"name": name, "members": members}
        if recept: c["receptionist"] = recept
        channels.append(c)
    return {"world": G["world"], "channels": channels, "peers": []}


def run(m):
    body = m["body"].strip()
    if body not in ("decl", "reconcile"):
        return
    rows = [json.loads(l) for l in call("0", "show").splitlines() if l]
    G, entries = fold(rows)
    if not G:
        return
    D = decl(G, entries)
    if body == "decl":
        return "decl\n" + json.dumps(D, ensure_ascii=False)
    c0 = next((a["addr"] for a in json.loads(call("0", "who")) if a["kind"] == "door" and a.get("local") and not a["retired"]), None)
    if c0:
        for c in D["channels"]:
            call(c0, f"rebuild {c['name']}\n" + json.dumps(c, ensure_ascii=False))
