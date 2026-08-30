# c0 的装配器（A）。这段源码是 G 里的 text；R 按 program kind 跑它：stdin 先来初始消息，之后一帧一请求、一帧一回复。
# 它读 G 的结构，把每一项写成 syscall；成员的 text 只搬运，不解释。
# 请求：
#   build <门> <创造者地址>\n<G JSON>      经门造一台机器的 c0：只造 G 的第一个 channel，最后放出生证明门；回 built <门>
#   start\n<G JSON>                        出生：本地长出其余 channel 与连线（发育）；然后把 born（world + c0 的成员）交给登记员，
#                                          长出来的每一件以 placed（带真实地址）登记
#   add <channel> <kind> [in] [bind=..] [tag=..] [iface=..]\n<text>   本地放一个 actor（channel 不存在则先 create）
#   peer <a> <b>                            本地两扇互指的门（角色 = 对面的名字）
#   retire <channel>/<addr>                 退役
#   spawn …                                 转给 C
#   decl                                    转给登记员；登记员的回答 decl\n<G> 从门回来后转给 C
# 每条 syscall 是一次请求：回执就在回复里（没有 note、没有对位）。落地后回请求者 placed/retired、送登记员。
# 登记员在 c0 的第一扇内门（place 行 local）那边（G.peers 的第一条是 [c0, c1]）。
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
frm, body = m["from"], m["body"]
head, _, rest = body.partition("\n"); t = head.split()


def ledger():
    return [json.loads(l) for l in call("0", "show").splitlines() if l]


def registry_door(rows):
    gone = {r["addr"] for r in rows if r["k"] == "retire"}
    return next((r["addr"] for r in rows if r["k"] == "place" and r["kind"] == "door" and r.get("local") and r["addr"] not in gone), None)


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
    return lh, lt, call(lh, lt)                                  # 回执就是回复


def register(lh, lt, ret, reg=None):
    """送登记员。登记员的门在 syscall 之后再找：退的可能正是旧门。"""
    lw = lh.split()
    if ret.endswith(" refused"):
        return
    reg = reg or registry_door(ledger())
    if reg is None:
        return
    if lw[0] == "channel.add.actor":
        cn, _, addr = ret.partition("/")
        call(reg, f"placed {cn} {addr} " + " ".join(lw[2:]) + "\n" + lt)
    elif lw[0] == "channel.retire.actor":
        call(reg, f"retired {ret}")


def place(who, line):
    """一条形态改动：syscall → 回执 → 回请求者 → 送登记员。"""
    lh, lt, ret = syscall(line)
    lw = lh.split()
    if ret.endswith(" refused"):
        if who: call(who, f"refused {lh}")
    elif lw[0] == "channel.add.actor":
        if who: call(who, f"placed {ret}")
    elif lw[0] == "channel.retire.actor":
        if who: call(who, f"retired {ret}")
    register(lh, lt, ret)
    return ret


op = t[0] if t else ""
if op == "build" and len(t) >= 3:
    G = json.loads(rest)
    if not G["channels"] or G["channels"][0].get("receptionist") is None:       # 首 channel 必须有接待员，否则 start 送不进去
        call("re", f"invalid {t[1]} first channel needs receptionist")
    else:
        for line in lines_first(G, t[2]):
            call(t[1], line)                                     # 经门，单向
        call("re", f"built {t[1]}")
elif op == "start" and rest.strip():
    if registry_door(ledger()) is None:                          # 已发育过（有内门）：start 只在出生时有意义
        G = json.loads(rest)
        done = [syscall(line) for line in lines_rest(G)]
        reg = registry_door(ledger())
        if reg:
            call(reg, "born\n" + json.dumps({"world": G["world"], "channels": G["channels"][:1], "peers": []}, ensure_ascii=False))
            for lh, lt, ret in done:
                register(lh, lt, ret, reg)
elif op == "add" and len(t) >= 3:
    syscall(f"channel.create {t[1]}")
    place(frm, "channel.add.actor " + " ".join(t[1:]) + "\n" + rest)
elif op == "peer" and len(t) == 3:
    syscall(f"channel.create {t[1]}"); syscall(f"channel.create {t[2]}")
    place(frm, f"channel.add.actor {t[1]} door tag={t[2]}\n{t[2]}")
    place(frm, f"channel.add.actor {t[2]} door tag={t[1]}\n{t[1]}")
elif op == "retire" and len(t) == 2:
    place(frm, f"channel.retire.actor {t[1]}")
elif op == "spawn":
    call("C", body)
elif op == "decl" and not rest.strip():
    reg = registry_door(ledger())
    if reg: call(reg, "decl")
elif op == "decl" and rest.strip():                              # 登记员的回答：转给 C
    call("C", body)
