# c3 的 hub：种群的介绍人。这段源码是 G 里的 text（c2 写的）；无 bind。记忆 = 账本。
#   hello <端点>     reporter 报到：没有指回它的门就经 c0 门 add 一扇（placed 到来再广播）；有就直接广播
#   placed …        新门放好：向每扇外部门广播 peers <所有报到过的端点…>
#   ping            回 pong
import json


def doors():
    return [a for a in json.loads(call("0", "who")) if a["kind"] == "door" and not a["retired"] and not a.get("local")]


def broadcast():
    eps = []
    for r in (json.loads(l) for l in call("0", "show").splitlines() if l):
        if r["k"] == "msg" and r["to"] == me and r["body"].startswith("hello "):
            e = r["body"].split()[1]
            if e not in eps: eps.append(e)
    for d in doors():
        call(d["tag"], "peers " + " ".join(eps))


def run(m):
    t = m["body"].split()
    if not t:
        return
    if t[0] == "ping":
        return "pong"
    if t[0] == "hello" and len(t) == 2:
        if any(d["text"] == t[1] for d in doors()):
            broadcast()
        else:
            call("c0", f"add {channel} door\n{t[1]}")
    elif t[0] == "placed":
        broadcast()
