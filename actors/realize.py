# c0 的装配器（A）。这段源码是 G 里的 text；R 按 program kind 跑它：stdin 是视图，stdout 按 ABI 拆。
# 它读 G 的结构，把每一项写成 syscall；成员的 text 只搬运，不解释。
# 请求：
#   build <门> <创造者地址>\n<G JSON>      经门造一台机器的 c0：channel.create / channel.add.actor 逐条发给门，
#                                          只造 G 的第一个 channel，最后放一扇指回创造者的门（出生证明）；回 built <门>
#   start\n<G JSON>                        出生：把 G 里第一个 channel 之外的全部长出来（本地 syscall）——发育
#   add <channel> <kind> [in] [bind=..]\n<text>   本地放一个 actor（channel 不存在则先 create）
#   peer <a> <b>                            本地两扇互指的门
# syscall 的返回（from=channel.*）在下一步的视图里出现；用写给自己的便签记住请求者，把 placed 转发给他。
import sys, json
v = json.load(sys.stdin)
ch, me, msgs = v["channel"], v["me"], v["msgs"]
out, notes, placed = [], [], []


def add_lines(c):
    """一个 channel 描述 → 它的 syscall 行（create + 每个成员）。"""
    L = [f"channel.create {c['name']}"]
    for i, m in enumerate(c["members"]):
        flags = []
        if i + 1 == c.get("receptionist", 1): flags.append("in")
        if m.get("bind"): flags.append("bind=" + ",".join(m["bind"]))
        L.append(" ".join(["channel.add.actor", c["name"], m["kind"], *flags]) + "\n" + m["text"])
    return L


def lines_first(G, creator):
    """父代的 A 经根门做的：G 的第一个 channel（c0）+ 出生证明门。"""
    L = add_lines(G["channels"][0])
    if creator:
        L.append(f"channel.add.actor {G['channels'][0]['name']} door\n{creator}")   # 出生证明
    return L


def lines_rest(G):
    """子代的 c0 自己做的：其余 channel + 全部连线。"""
    L = []
    for c in G["channels"][1:]:
        L += add_lines(c)
    for a, b in G.get("peers", []):
        L.append(f"channel.add.actor {a} door\n{b}")
        L.append(f"channel.add.actor {b} door\n{a}")
    return L


for m in msgs:
    frm = m["from"]; body = m["body"]
    head, _, rest = body.partition("\n"); t = head.split()
    if frm in ("channel.create", "channel.add.actor"):
        placed.append(body); continue
    if frm == me and t and t[0] == "note":
        notes.append(t[1]); continue
    if not t:
        continue
    if t[0] == "build" and len(t) >= 3:
        G = json.loads(rest)
        for line in lines_first(G, t[2]):
            out.append(f">>> {t[1]}\n{line}")            # 每条 syscall 是发给门的一条消息
        out.append(f">>> {frm}\nbuilt {t[1]}")
    elif t[0] == "start" and rest.strip():
        for line in lines_rest(json.loads(rest)):
            out.append(f">>> {line}")                    # 本地 syscall：自己长出其余器官
    elif t[0] == "add" and len(t) >= 3:
        out.append(f">>> channel.create {t[1]}")
        out.append(">>> channel.add.actor " + " ".join(t[1:]) + "\n" + rest)
        out.append(f">>> {me}\nnote {frm}")
    elif t[0] == "peer" and len(t) == 3:
        out.append(f">>> channel.create {t[1]}"); out.append(f">>> channel.create {t[2]}")
        out.append(f">>> channel.add.actor {t[1]} door\n{t[2]}")
        out.append(f">>> channel.add.actor {t[2]} door\n{t[1]}")
        out.append(f">>> {me}\nnote {frm}")

if placed and notes:
    for who in dict.fromkeys(notes):
        out.append(f">>> {who}\n" + "\n".join("placed " + p for p in placed if "/" in p))

print("\n".join(out))
