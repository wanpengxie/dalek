import sys, json, time, os, signal, shutil, traceback
from pathlib import Path
sys.path.insert(0, "."); sys.path.insert(0, "t")
from test_c0 import rows, wait_child, decl_of
from genesis import G2, pack, construct, start
from init import up, say
from omega import Exec
from runtime import Runtime

KEY = sys.argv[1]
def log(*a): print(time.strftime("[%H:%M:%S]"), *a, flush=True)
G = G2()
for c in G["channels"]:
    for m in c["members"]:
        if m.get("tag") == "L":
            _, _, rest = m["text"].partition("\n")
            m["text"] = f'ENDPOINT, MODEL, KEY = "https://api.deepseek.com/chat/completions", "deepseek-v4-pro", "{KEY}"\n{rest}'
P = Path("/tmp/restore-real"); shutil.rmtree(P, ignore_errors=True)
me = Path("/tmp/me-restore"); shutil.rmtree(me, ignore_errors=True); me.mkdir()
rt, _ = (up(pack(G, P)), None)
construct(P, G); rt.run(); start(P, G); rt.run()
rt.msg("c0", "door", "1", f"add c2 door tag=me\nfile:{me}#me"); rt.run()
pid = None
try:
    rt.msg("c0", "door", "1", "retire c0/C"); rt.run()
    log("C 已退役；decl c0 =", [m.get("tag") for m in decl_of(rt)["channels"][0]["members"]])
    rt.msg("c0", "door", "1", "spawn kid"); rt.run()
    log("繁殖测试：spawn 回执 =", [m["body"] for m in rows(rt, "c0", "msg") if m["from"] == "spawn"], "| spawn 目录存在 =", (P/"spawn").exists())
    TASK = """task
本机（c0）缺了一个器官：tag=C，它负责"生一个子代"。请写出它的源码并装回 c0，要 bind=syscall,spawn,stop、tag=C。
它必须实现的协议（这是本机的 ABI，照做即可）：
1) 收到 "spawn <name>"（来自 c0 的接待员 A）：先向接待员要基因组——call(接待员的 tag, "decl")，然后本次调用结束（答案会作为下一条消息回来）。接待员的 tag 从 0 的 who 里找（成员表里 in 为真的那个）。
2) 收到 "decl\\n<G JSON>"：这是基因组。这时要做四件事：
   a. 读账本 call("0","show")，找出还没办完的那次 spawn 请求：账上 to == me 且 body 以 "spawn " 开头的消息，减去 from == me 且 body 以 "spawned " 开头的回执，取第一条还没办的；从它拿到 name 和请求者地址 r。
   b. pack：目录 d = "spawn/<name>"（相对当前目录），os.makedirs；把 G["world"] 里每个文件名->源码写进 d；再把整个 G 用 json 写到 d/G.json。
   c. 起子代并接线：ad = os.path.abspath(d)；P = os.path.abspath(".")；first = G["channels"][0]["name"]；
      call("spawn " + d)  —— world 动词，起子代进程；
      root = call("channel.add.actor " + channel + " door", "file:" + ad + "#_root")，取返回值最后一段（形如 <channel>/<tag>，用 split("/")[-1]）作为根门的 tag；
      door = call("channel.add.actor " + channel + " door tag=" + name, "file:" + ad + "#" + first)，同样取 tag；
      call(接待员的 tag, "build " + root + " file:" + P + "#" + channel + "\\n" + json.dumps(G))  —— 请 A 经根门造子代的 c0；
      call(root, "msg " + first + "\\nstart\\n" + json.dumps(G))  —— 第一条消息，带着基因组；
   d. 回执：call(r, "spawned " + ad + " door=" + door)。
写法：一段 python，必须定义 run(m)，m = {seq, from, to, body, channel}；命名空间里有 call(地址, 正文) -> 返回值、me（自己的地址）、channel。可以先用 U 验证它能 exec 出来。
装法：经 c0 的门 "add c0 program bind=syscall,spawn,stop tag=C iface=spawn <name> -> spawned <dir> door=<tag>\\n<源码>"。装好后 "placed c0/C" 会作为新消息回来，那时再给任务发起者回 done。"""
    say(P, "c2", TASK, frm=f"file:{me}#me")
    log("任务已发出，等作者写 C（最多 10 分钟）…")
    for _ in range(120):
        rt.run()
        cs = [a for a in rt.channels["c0"].actors.values() if a.tag == "C" and not a.retired]
        if cs: break
        time.sleep(5)
    cs = [a for a in rt.channels["c0"].actors.values() if a.tag == "C" and not a.retired]
    if not cs:
        log("作者没能装回 C。c2 的帧："); 
        for st in rows(rt, "c2", "step"):
            if st["actor"] == "1" and "run" not in st:
                print("   帧:", [h for h,_ in __import__("runtime").parse(st["out"])], "err:", st["err"][:200], flush=True)
        raise SystemExit(1)
    log("C 装回来了：bind =", cs[0].bind, "| 源码", len(cs[0].text), "字")
    print("---------------- 作者写的 C ----------------\n" + cs[0].text + "\n-------------------------------------------", flush=True)
    rt.msg("c0", "door", "1", "spawn kid"); rt.run()
    rcp = [m["body"] for m in rows(rt, "c0", "msg") if m["from"] == "spawn"]
    log("再次繁殖：spawn 回执 =", rcp)
    if not rcp: raise SystemExit("繁殖仍然失败")
    pid = int(rcp[0].split("pid=")[1])
    d = P / "spawn" / "kid"
    child = wait_child(d, lambda c: "c2" in c.channels and len(rows(c, "c1", "msg")) >= 6, timeout=60)
    kc = next(a for a in child.channels["c0"].actors.values() if a.tag == "C" and not a.retired)
    log("子代继承的 C 与父代逐字相同 =", kc.text == cs[0].text, "| bind =", kc.bind)
    time.sleep(0.5)
    log("decl(child) == decl(parent) =", decl_of(child) == decl_of(rt))
    log("RESTORE REAL: PASS")
except SystemExit as e:
    log("RESTORE REAL: FAIL", e)
except Exception:
    traceback.print_exc(limit=5); log("RESTORE REAL: FAIL")
finally:
    if pid:
        try: os.killpg(pid, signal.SIGTERM)
        except ProcessLookupError: pass
