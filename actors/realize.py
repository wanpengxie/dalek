# c0 的装配器（A）。这段源码是 G 里的 text；R 放入时 exec 一次得到常驻的 run(m)，命名空间里有 call、me、channel。
# 它读 G 的结构，把每一项写成 syscall；成员的 text 只搬运，不解释。
# 请求：
#   build <门> <创造者地址>\n<G JSON>      经门造一台机器的 c0：只造 G 的第一个 channel，最后放出生证明门；返回 built <门>
#   start\n<G JSON>                        出生：本地长出其余 channel 与连线（发育）；然后把 born（world + c0 的成员）交给登记员，
#                                          长出来的每一件以 placed（带真实地址）登记
#   add <channel> <kind> [in] [bind=..] [tag=..] [iface=..]\n<text>   本地放一个 actor（channel 不存在则先 create）；返回 R 分配的 placed <ch>/<tag>
#   peer <a> <b>                            本地两扇互指的门（角色 = 对面的名字）
#   retire <channel>/<tag>                  退役
#   spawn …                                 转给 C
#   decl                                    转给登记员；登记员的回答 decl\n<G> 从门回来后转给 C
#   rebuild <channel>\n<{name, members, receptionist}>   对账（登记员在 up 时发）：channel.create → exists 跳过；new 才把成员逐个放回去，不再登记（登记处本来就有）
#   start 长完之后给每扇本机门那边送一句 start（出生 = 父代的 start；醒来 = 世界的 up，两个来源都在账上）；up / down / start 到 A 自己：不做事
# 每条 syscall 是一次 call：回执就是返回值。落地后送登记员；返回值回请求者。
# 登记员在 c0 的第一扇内门（who 里 local 的门）那边（G.peers 的第一条是 [c0, c1]）。
import json


def who():
    return json.loads(call("0", "who"))


def registry_door():
    return next((a["addr"] for a in who() if a["kind"] == "door" and a.get("local") and not a["retired"]), None)


def add_lines(c):
    L = [f"channel.create {c['name']}"]
    for i, x in enumerate(c["members"]):
        flags = []
        if i + 1 == c.get("receptionist"): flags.append("in")          # 没有默认：G 不写就没有接待员
        if x.get("bind"): flags.append("bind=" + ",".join(x["bind"]))
        if x.get("tag"): flags.append("tag=" + x["tag"])
        if x.get("iface"): flags.append("iface=" + x["iface"])          # iface 必须是最后一个 flag
        L.append(" ".join(["channel.add.actor", c["name"], x["kind"], *flags]) + "\n" + x["text"])
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
        L.append(f"channel.add.actor {a} door tag={b}\n{b}")
        L.append(f"channel.add.actor {b} door tag={a}\n{a}")
    return L


def syscall(line):
    lh, _, lt = line.partition("\n")
    return lh, lt, call(lh, lt)                                  # 回执就是返回值


def register(lh, lt, ret, reg=None):
    """送登记员。登记员的门在 syscall 之后再找：退的可能正是旧门。"""
    lw = lh.split()
    if ret.endswith(" refused"):
        return
    reg = reg or registry_door()
    if reg is None:
        return
    if lw[0] == "channel.add.actor":
        cn, _, tag = ret.partition("/")
        flags = [f for f in lw[3:] if not f.startswith("tag=")]
        call(reg, f"placed {cn} {tag} {lw[2]} " + " ".join(flags) + "\n" + lt)
    elif lw[0] == "channel.retire.actor":
        call(reg, f"retired {ret}")


def place(line):
    """一条形态改动：syscall → 回执 → 送登记员 → 给请求者的话。"""
    lh, lt, ret = syscall(line)
    lw = lh.split()
    register(lh, lt, ret)
    if ret.endswith(" refused"):
        return f"refused {lh}"
    if lw[0] == "channel.add.actor":
        return f"placed {ret}"
    if lw[0] == "channel.retire.actor":
        return f"retired {ret}"
    return ret


def run(m):
    head, _, rest = m["body"].partition("\n"); t = head.split()
    op = t[0] if t else ""
    if op == "build" and len(t) >= 3:
        G = json.loads(rest)
        if not G["channels"] or G["channels"][0].get("receptionist") is None:   # 首 channel 必须有接待员，否则 start 送不进去
            return f"invalid {t[1]} first channel needs receptionist"
        for line in lines_first(G, t[2]):
            call(t[1], line)                                     # 经门，单向
        return f"built {t[1]}"
    if op == "start" and rest.strip():
        if registry_door() is not None:                          # 已发育过（有内门）：start 只在出生时有意义
            return
        G = json.loads(rest)
        done = [syscall(line) for line in lines_rest(G)]
        reg = registry_door()
        if reg:
            call(reg, "born\n" + json.dumps({"world": G["world"], "channels": G["channels"][:1], "peers": []}, ensure_ascii=False))
            for lh, lt, ret in done:
                register(lh, lt, ret, reg)
        for a in who():                                          # 发育完成：告诉每个器官它出生了
            if a["kind"] == "door" and a.get("local") and not a["retired"]:
                call(a["addr"], "start")
        return
    if op == "rebuild" and len(t) == 2 and rest.strip():
        if not syscall(f"channel.create {t[1]}")[2].endswith(" new"):
            return f"exists {t[1]}"
        for line in add_lines(json.loads(rest))[1:]:
            syscall(line)
        return f"rebuilt {t[1]}"
    if op == "add" and len(t) >= 3:
        syscall(f"channel.create {t[1]}")
        return place("channel.add.actor " + " ".join(t[1:]) + "\n" + rest)
    if op == "peer" and len(t) == 3:
        syscall(f"channel.create {t[1]}"); syscall(f"channel.create {t[2]}")
        return "\n".join([place(f"channel.add.actor {t[1]} door tag={t[2]}\n{t[2]}"),
                          place(f"channel.add.actor {t[2]} door tag={t[1]}\n{t[1]}")])
    if op == "retire" and len(t) == 2:
        return place(f"channel.retire.actor {t[1]}")
    if op == "spawn":
        call("C", m["body"]); return
    if op == "decl" and not rest.strip():
        reg = registry_door()
        if reg: call(reg, "decl")
        return
    if op == "decl" and rest.strip():                            # 登记员的回答：转给 C
        call("C", m["body"]); return
