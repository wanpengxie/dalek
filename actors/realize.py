# c0 的装配器（A）。这段源码是 G 里的 text；R 按 program kind 跑它：stdin 是视图，stdout 按 ABI 拆。
# 它读 G 的结构，把每一项写成 syscall；成员的 text 只搬运，不解释。
# 请求：
#   build <门> <创造者地址>\n<G JSON>      经门造一台机器的 c0：只造 G 的第一个 channel，最后放出生证明门；回 built <门>
#   start\n<G JSON>                        出生：本地长出其余 channel 与连线（发育）；下一步把 born（world + c0 的成员）交给登记员，
#                                          长出来的每一件以 placed（带真实地址）登记
#   add <channel> <kind> [in] [bind=..]\n<text>   本地放一个 actor（channel 不存在则先 create）
#   peer <a> <b>                            本地两扇互指的门
#   retire <channel>/<addr>                 退役
#   spawn …                                 转给持有 spawn 绑定的成员（C）
#   decl                                    转给登记员；登记员的回答 decl\n<G> 从门回来后转给持有 spawn 绑定的成员（C）
# 三段因果记账：syscall 落地（目标账本）→ 下一步收到回执（c0 账本）→ 送登记员（c1 账本）。回请求者 placed/retired。
# 登记员在 c0 的第一扇内门（local）那边（G.peers 的第一条是 [c0, c1]）。
import sys, json
v = json.load(sys.stdin)
ch, me, msgs, actors = v["channel"], v["me"], v["msgs"], v["actors"]
out, pending = [], []                        # pending: 本步发出的 syscall 行 [{who, line}]，写进 note，下一步对回执


def add_lines(c):
    L = [f"channel.create {c['name']}"]
    for i, m in enumerate(c["members"]):
        flags = []
        if i + 1 == c.get("receptionist"): flags.append("in")          # 没有默认：G 不写就没有接待员
        if m.get("bind"): flags.append("bind=" + ",".join(m["bind"]))
        L.append(" ".join(["channel.add.actor", c["name"], m["kind"], *flags]) + "\n" + m["text"])
    return L


def lines_first(G, creator):
    """父代的手：G 的第一个 channel + 出生证明门。"""
    L = add_lines(G["channels"][0])
    if creator:
        L.append(f"channel.add.actor {G['channels'][0]['name']} door\n{creator}")
    return L


def lines_rest(G):
    """自己的手：其余 channel + 全部连线。"""
    L = []
    for c in G["channels"][1:]:
        L += add_lines(c)
    for a, b in G.get("peers", []):
        L.append(f"channel.add.actor {a} door\n{b}")
        L.append(f"channel.add.actor {b} door\n{a}")
    return L


def registry_door():
    return next((a["addr"] for a in actors if a["kind"] == "door" and not a["retired"] and a.get("local")), None)


def spawner():
    return next((a["addr"] for a in actors if "spawn" in a["bind"] and not a["retired"]), None)


def emit(who, line):
    out.append(f">>> {line}")
    pending.append({"who": who, "line": line})              # who=None：出生时长的，不回话但要登记


returns, note, requests = [], None, []
for m in msgs:
    frm, body = m["from"], m["body"]
    head, _, rest = body.partition("\n"); t = head.split()
    if frm in ("channel.create", "channel.add.actor", "channel.retire.actor"):
        returns.append(body)
    elif frm == me and t and t[0] == "note":
        note = json.loads(rest)
    elif frm == me and t and t[0] == "born":
        d = registry_door(); G = json.loads(rest)
        if d: out.append(f">>> {d}\nborn\n" + json.dumps({"world": G["world"], "channels": G["channels"][:1], "peers": []}, ensure_ascii=False))
    elif t:
        requests.append((frm, t, rest, body))

# 上一步发出的 syscall 的回执：回请求者，告诉登记员
if note:
    reg = registry_door(); replies = {}
    for item, ret in zip(note["pending"], returns):
        lh, _, lt = item["line"].partition("\n"); lw = lh.split()
        if ret.endswith(" refused"):                                              # 回执稠密：拒绝也占一位
            if item["who"]: replies.setdefault(item["who"], []).append(f"refused {lh}")
        elif lw[0] == "channel.add.actor" and "/" in ret:
            cn, _, addr = ret.partition("/")
            if item["who"]: replies.setdefault(item["who"], []).append(f"placed {ret}")
            if reg: out.append(f">>> {reg}\nplaced {cn} {addr} " + " ".join(lw[2:]) + "\n" + lt)
        elif lw[0] == "channel.retire.actor":
            if item["who"]: replies.setdefault(item["who"], []).append(f"retired {ret}")
            if reg: out.append(f">>> {reg}\nretired {ret}")
    for who, lines in replies.items():
        out.append(f">>> {who}\n" + "\n".join(lines))

for frm, t, rest, body in requests:
    op = t[0]
    if op == "build" and len(t) >= 3:
        G = json.loads(rest)
        if not G["channels"] or G["channels"][0].get("receptionist") is None:       # 首 channel 必须有接待员，否则 start 送不进去
            out.append(f">>> {frm}\ninvalid {t[1]} first channel needs receptionist"); continue
        for line in lines_first(G, t[2]):
            out.append(f">>> {t[1]}\n{line}")
        out.append(f">>> {frm}\nbuilt {t[1]}")
    elif op == "start" and rest.strip():
        if any(a["kind"] == "door" and a.get("local") for a in actors):
            continue                                      # 已发育过（有内门）：start 只在出生时有意义
        for line in lines_rest(json.loads(rest)):
            emit(None, line)                              # 出生：每件以 placed 登记；born 只带脐带放的
        out.append(f">>> {me}\nborn\n{rest}")
    elif op == "add" and len(t) >= 3:
        emit(frm, f"channel.create {t[1]}")
        emit(frm, "channel.add.actor " + " ".join(t[1:]) + "\n" + rest)
    elif op == "peer" and len(t) == 3:
        emit(frm, f"channel.create {t[1]}"); emit(frm, f"channel.create {t[2]}")
        emit(frm, f"channel.add.actor {t[1]} door\n{t[2]}")
        emit(frm, f"channel.add.actor {t[2]} door\n{t[1]}")
    elif op == "retire" and len(t) == 2:
        emit(frm, f"channel.retire.actor {t[1]}")
    elif op == "spawn":
        s = spawner()
        if s: out.append(f">>> {s}\n{body}")
    elif op == "decl" and not rest.strip():
        d = registry_door()
        if d: out.append(f">>> {d}\ndecl")
    elif op == "decl" and rest.strip():                    # 登记员的回答：转给 C
        s = spawner()
        if s: out.append(f">>> {s}\n{body}")

if pending:
    out.append(f">>> {me}\nnote\n" + json.dumps({"pending": pending}, ensure_ascii=False))
print("\n".join(out))
