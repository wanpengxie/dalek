# c0 的装配器（A）。这段源码是 G 里的 text；运行时按 program kind 跑它：stdin 是视图，stdout 按 ABI 拆。
# 请求（写给它的消息）：
#   realize [G 路径]                       读 G，把除自己之外的所有成员放进各自 channel，按 peers 放门
#   add <channel> <kind> [in] [bind=..]\n<text>   放一个 actor
#   peer <a> <b>                            放两扇互指的门
# 介质返回（from=place）会在下一步的视图里出现；用写给自己的便签记住请求者，把 placed 转发给他。
import sys, json
v = json.load(sys.stdin)
ch, me, msgs = v["channel"], v["me"], v["msgs"]
out, notes, placed = [], [], []


def place(channel, kind, text, flags=()):
    head = " ".join(["place", channel, kind, *flags]).rstrip()
    out.append(f">>> {head}\n{text}")


for m in msgs:
    frm = m["from"]; body = m["body"]
    head, _, rest = body.partition("\n"); t = head.split()
    if frm == "place":
        placed.append(body); continue
    if frm == me and t and t[0] == "note":
        notes.append(t[1]); continue
    if not t:
        continue
    if t[0] == "realize":
        G = json.load(open(t[1] if len(t) > 1 else "G.json", encoding="utf-8"))
        first = G["channels"][0]
        for c in G["channels"]:
            for i, mem in enumerate(c["members"]):
                if c is first and i == 0 and c["name"] == ch and me == "1":
                    continue                       # 自己已由 init 放好
                flags = []
                if i + 1 == c.get("receptionist", 1): flags.append("in")
                if mem.get("bind"): flags.append("bind=" + ",".join(mem["bind"]))
                place(c["name"], mem["kind"], mem["text"], flags)
        for a, b in G.get("peers", []):
            place(a, "door", b); place(b, "door", a)
        out.append(f">>> {me}\nnote {frm}")
    elif t[0] == "add" and len(t) >= 3:
        place(t[1], t[2], rest, t[3:]); out.append(f">>> {me}\nnote {frm}")
    elif t[0] == "peer" and len(t) == 3:
        place(t[1], "door", t[2]); place(t[2], "door", t[1]); out.append(f">>> {me}\nnote {frm}")

# 把上一轮的介质返回转发给当时的请求者（便签和返回在同一视图里到达）
if placed and notes:
    for who in dict.fromkeys(notes):
        out.append(f">>> {who}\n" + "\n".join("placed " + p for p in placed))

print("\n".join(out))
