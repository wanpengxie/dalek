# c4 的 reporter：这台机器在种群里的嘴和耳朵。这段源码是 G 里的 text（c2 写的）；bind=spawn（能唤醒休眠的邻居）。记忆 = 账本。
#   start / up / tick   还没向 hub 报到就报到（hello <我的端点>）；tick 时再给 hub 和每个 peer 发 ping；
#                       上一轮给 hub 的 ping 没等到 pong → hub 那台机器死了 → spawn <它的目录>（照 H 唤醒，同一个体）
#   peers <端点…>       对每个不是自己、还没有门的端点经 c0 门 add 一扇门（组织：边长在用它的器官旁边）
#   placed <ch>/<n>     新门放好：ping 它
#   ping                回 pong
import json, os


def who():
    return json.loads(call("0", "who"))


def doors():
    return [a for a in who() if a["kind"] == "door" and not a["retired"] and not a.get("local")]


def hub():
    return next((a for a in who() if a.get("tag") == "hub" and not a["retired"]), None)


def rows():
    return [json.loads(l) for l in call("0", "show").splitlines() if l]


def mine():
    return f"file:{os.path.abspath('.')}#{channel}"


def run(m):
    t = m["body"].split()
    if not t:
        return
    if t[0] == "ping":
        return "pong"
    if t[0] in ("start", "up", "tick"):
        h = hub()
        if not h:
            return
        R = rows()
        if not any(r["k"] == "msg" and r["from"] == me and r["to"] == h["addr"] and r["body"].startswith("hello ") for r in R):
            call(h["addr"], "hello " + mine()); return
        if t[0] == "tick":
            pings = [r["seq"] for r in R if r["k"] == "msg" and r["from"] == me and r["to"] == h["addr"] and r["body"] == "ping"]
            pongs = [r["seq"] for r in R if r["k"] == "msg" and r["from"] == h["addr"] and r["to"] == me and r["body"] == "pong"]
            if pings and not any(p > pings[-1] for p in pongs):
                call("spawn " + h["text"][5:].partition("#")[0])          # hub 没回：把它那台机器照 H 叫醒
            call(h["addr"], "ping")
            for d in doors():
                if d["addr"] != h["addr"]:
                    call(d["addr"], "ping")
        return
    if t[0] == "peers":
        have = {d["text"] for d in doors()}
        for ep in t[1:]:
            if ep != mine() and ep not in have:
                call("c0", f"add {channel} door\n{ep}")
        return
    if t[0] == "placed" and len(t) == 2 and "/" in t[1]:
        call(t[1].split("/")[1], "ping")
