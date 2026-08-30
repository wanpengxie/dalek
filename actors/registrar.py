# c1 的登记员：基因组登记处，不是镜子。这段源码是 G 里的 text。没有任何绑定：它读账本和别人一样——请求地址 0。
# 它只折自己的账本，不碰文件。形态登记三种（c0 的宣称——基因组的 commit，不是 R 的事实），只认来自本机 channel 的门（place 行 local 的门，含已退役的——历史的解释不随拓扑漂）：
#   born\n<G>                                         脐带放的：world + 第一个 channel 的成员（地址 = 序号）
#   placed <ch> <addr> <kind> [in] [bind=…] [tag=…] [iface=…]\n<text>    c0 的手放的，带真实地址（出生时长出的其余器官也走这条）
#   retired <ch>/<addr>
# 请求 decl（任何人）→ 读账本、折叠、回 decl\n<G_t>：G₀ ⊕ placed ⊖ retired。
#   channel 按出生 + 首次出现顺序；成员按地址；门就是成员（kind=door，text 原样），不折 peers；接待员显式，没有就没有；
#   退役的不输出；world 原样来自 born。这就是 π(A_t)：去掉不经 c0 放的门（出生证明、生子的临时门）、去掉退役、地址重排。
import sys, json


def call(to, body=""):
    sys.stdout.write(f">>> {to}\n{body}\n<<<\n"); sys.stdout.flush()
    r = []
    while True:
        line = sys.stdin.readline()
        if not line or line == "<<<\n": break
        r.append(line.rstrip("\n"))
    return "\n".join(r)


m = json.loads(sys.stdin.readline())
me = m["to"]


def fold(rows):
    fact_doors = {r["addr"] for r in rows if r["k"] == "place" and r["kind"] == "door" and r.get("local")}
    G, entries = None, {}                       # entries[ch] = [{addr, kind, text, bind, tag, iface, in, retired}]
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
                entries[c["name"]] = [{"addr": str(i + 1), "kind": x["kind"], "text": x["text"], "bind": x.get("bind", []),
                                       "tag": x.get("tag"), "iface": x.get("iface"),
                                       "in": i + 1 == c.get("receptionist"), "retired": False}
                                      for i, x in enumerate(c["members"])]
        elif t[0] == "placed" and len(t) >= 4:
            flags = t[4:]; bind, tag, iface = [], None, None
            for i, f in enumerate(flags):
                if f.startswith("bind="): bind = f[5:].split(",")
                elif f.startswith("tag="): tag = f[4:]
                elif f.startswith("iface="): iface = " ".join([f[6:], *flags[i + 1:]]); break
            e = entries.setdefault(t[1], [])
            if "in" in flags:
                for x in e: x["in"] = False
            e.append({"addr": t[2], "kind": t[3], "text": rest, "bind": bind, "tag": tag, "iface": iface,
                      "in": "in" in flags, "retired": False})
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


if m["body"].strip() == "decl":
    rows = [json.loads(l) for l in call("0", "show").splitlines() if l]
    G, entries = fold(rows)
    if G:
        call("re", "decl\n" + json.dumps(decl(G, entries), ensure_ascii=False))
