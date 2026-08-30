"""运行时 R：极小状态空间 + 转移表。内容盲。

状态    每个 channel 一本只追加的账本（h/<name>.jsonl，四种行 place / retire / msg / step）+ 每个 actor 一个游标。
        全部由账本折叠重建，包括各收件箱读到哪（收进来的行带 at=收件箱偏移）和每个活成员的函数对象（重新实例化）。
actor   是常驻函数：折叠到 place 行时实例化一次（fn），挂在地址上，活到退役。三种 kind = 三种得到函数的方式：
  program   Exec.load(text, {call, me, channel})：源码在这个命名空间里跑一次，定义 run(m)；以后每条消息调同一个 run
  oracle    介质实现的函数体：组装（call("0","show") 整本账 + call("0","who") 成员表 + 初始消息）→ Port.request(端点, 提示语, 对话) 多轮；
            模型输出的每帧 ">>> 地址 / 正文 / <<<" 是一次 call，回复喂回下一轮；帧 ">>> re" 是返回值；不再输出帧 = 结束
  door      介质实现的函数体：Port.send 到 text 所指的端点，署名本 channel 的端点；返回空。门是连接
事件    某本账上多了一条写给某地址的消息（不带 run 标记的 msg 行）= 介质替发送者调用它：fn(m)，m = {seq, from, to, body, channel}；
        返回值就是回复，送回发送者（门则出去）。
call    每个 actor 专属的闭包 call(地址, 正文) → 返回值：解析名字、记一行请求、调对方的 fn（嵌套 = 调用栈）、记一行返回。
        运行中产生的每一行都带 run=<事件 seq>；只有事件推游标。前面放的 actor 用得了后面放的：名字在调用那一刻解析。
call 的地址
  <序号> 或 <角色>                                           本 channel 的成员：序号是个体的地址，角色是形态里的名字（place 行的 tag，后放的接替先放的）
  0                                                          介质的读地址，人人可用：show [a] [b] → 账本行（门的 place 行附此刻的 local）；who → 此刻的成员表
                                                             账上记事实行 msg from=0 body="show a b" / "who"，内容可重算不入账
  channel.create <name> / channel.add.actor <channel> <kind> [in] [bind=…] [tag=…] [iface=…] / channel.retire.actor <channel>/<addr>
                                                             syscall；需 bind=syscall；返回结果或 "<参数> refused"
  <动词> <参数>                                                绑定了的 world 动词（ACTIONS：起一台机器 / 停一个进程）；需 bind=<动词>
  写给门 = 送出去，返回空；写给不存在的地址 = 丢弃，返回空；写给请求者 = 再调用它一次（可重入）
账本    每次 call 两行 msg（请求、返回）；每次调用一行 step（out = 它发出的每个 call，加一帧 re = 它的返回值；err = 异常）。
        实例化失败的成员每次被调用都 err；调用抛异常也 err——器官的失败不是机器的失败。
接待员  只由 place 行的 in 决定，没有默认：G 不写就没有（外来消息落空）。
入口    每个 channel 一个收件箱 in/<name>.jsonl（Port 的接收侧）：一行 → msg 给接待员（事件），署名指回发信端点的门（没有则 door）。
        根收件箱 in/_root.jsonl 在 channel 之前存在：开着 ⇔ 所有账本无 msg 行。开着时接受两个 syscall（by=_root）
        和 msg <channel>（第一条消息，顺手关门）。关门后忽略。
调度    轮转每个 channel，各取最早的事件调用一次；静止则停（serve 模式下继续轮询）。

本文件只认识介质词汇：地址、角色、kind、text、消息、账本、追加、投递、调用、返回、步记录、门、端点、syscall、动词、退役。
它不认识任何组织词汇。检验：把 G 里所有名字换掉，本文件的行为逐字节不变。
"""
from __future__ import annotations
import json, os, time, traceback
from dataclasses import dataclass, field
from pathlib import Path
from omega import Exec, Store, Port


KINDS = ("program", "oracle", "door")
ROOT = "_root"
LEDGER = "0"                                   # 每个 channel 的读地址：写给它 = 读它；不是成员，不放、不退、不遗传
MAX_TURNS = 16                                 # oracle 一次调用最多几轮


def _spawn(P: Path, d: str) -> str:
    target = (P / d).resolve()
    pid = Exec.spawn(["init.py", str(target), "--serve"], cwd=P, log=target / "log")
    return f"{d} pid={pid}"


def _stop(P: Path, pid: str) -> str:
    Exec.stop(int(pid))
    return f"{pid} stopped"


ACTIONS = {"spawn": _spawn, "stop": _stop}
BINDS = ("syscall", *ACTIONS)


@dataclass
class Actor:
    addr: str
    kind: str
    text: str
    bind: tuple[str, ...] = ()
    tag: str | None = None
    iface: str | None = None
    retired: bool = False
    fn: object = None                          # 常驻的函数对象（实例化失败则 None，err 记原因）
    err: str = ""


@dataclass
class Channel:
    name: str
    actors: dict[str, Actor] = field(default_factory=dict)
    receptionist: str | None = None
    rows: list[dict] = field(default_factory=list)
    cursor: dict[str, int] = field(default_factory=dict)

    @property
    def seq(self) -> int:
        return self.rows[-1]["seq"] if self.rows else 0


class Runtime:
    def __init__(self, P: str | Path):
        self.P = Path(P)
        self.h = self.P / "h"
        self.channels: dict[str, Channel] = {}
        self.offsets: dict[str, int] = {}
        self._events: list[int] = []           # 正在处理的事件（调用栈的根）
        self._calls: list[list[str]] = []      # 每层调用发出的 call（写进 step.out）

    # ------------------------------------------------------------ 端点
    def ep(self, box: str) -> str:
        return f"file:{self.P}#{box}"

    def _target(self, text: str) -> str:
        return text if ":" in text else self.ep(text)          # 门的 text：端点，或本机 channel 名

    def _door(self, c: Channel, sender: str) -> str | None:
        return next((a.addr for a in c.actors.values()
                     if a.kind == "door" and not a.retired and (a.text == sender or self.ep(a.text) == sender)), None)

    # ------------------------------------------------------------ 账本
    def _path(self, name: str) -> Path:
        return self.h / f"{name}.jsonl"

    def load(self) -> "Runtime":
        for name in Store.read(self.h / "_order").split():
            c = self.channels[name] = Channel(name)
            for line in Store.read(self._path(name)).splitlines():
                self._fold(c, json.loads(line))
        return self

    def _append(self, c: Channel, row: dict) -> dict:
        row = {"seq": c.seq + 1, **row}
        Store.append(self._path(c.name), json.dumps(row, ensure_ascii=False))
        self._fold(c, row)
        return row

    def _fold(self, c: Channel, row: dict) -> None:
        c.rows.append(row)
        k = row["k"]
        if k == "place":
            a = c.actors[row["addr"]] = Actor(row["addr"], row["kind"], row["text"], tuple(row.get("bind", ())),
                                              row.get("tag"), row.get("iface"))
            if row.get("in"):
                c.receptionist = row["addr"]                # 没有默认：接待员只由显式 in 决定
            self._instantiate(c, a)                         # 放入即实例化：常驻
        elif k == "retire":
            a = c.actors.get(row["addr"])
            if a:
                a.retired = True; a.fn = None               # 接待员退不了（retire 拒绝），所以接待员不变
        elif k == "step" and "run" not in row:
            c.cursor[row["actor"]] = row["upto"]          # 只有事件推游标
        if "at" in row:                                   # 收件箱读到哪，从账本折出来
            box = ROOT if row.get("by") == ROOT else c.name
            self.offsets[box] = max(self.offsets.get(box, 0), row["at"])

    @property
    def root_open(self) -> bool:
        return not any(r["k"] == "msg" for c in self.channels.values() for r in c.rows)

    # ------------------------------------------------------------ 实例化：三种得到函数的方式
    def _caller(self, c: Channel, a: Actor):
        def call(to, body=""):
            return self._dispatch(c, a, str(to), "" if body is None else str(body))
        return call

    def _instantiate(self, c: Channel, a: Actor) -> None:
        try:
            if a.kind == "program":
                a.fn = Exec.load(a.text, {"call": self._caller(c, a), "me": a.addr, "channel": c.name})
            elif a.kind == "oracle":
                a.fn = self._oracle(c, a)
            else:
                a.fn = self._doorfn(c, a)
            a.err = ""
        except Exception:
            a.fn, a.err = None, traceback.format_exc(limit=3)

    def _doorfn(self, c: Channel, a: Actor):
        def fn(m):
            Port.send(self._target(a.text), {"from": self.ep(c.name), "body": m["body"]})
            return ""
        return fn

    def _oracle(self, c: Channel, a: Actor):
        call = self._caller(c, a)
        endpoint, _, system = a.text.partition("\n")            # text 解析一次：第一行端点，其余提示语

        def fn(m):
            view = {"msg": m, "ledger": [json.loads(l) for l in call("0", "show").splitlines() if l],
                    "members": json.loads(call("0", "who"))}
            messages = [{"role": "user", "content": json.dumps(view, ensure_ascii=False)}]
            ret = []
            for _ in range(MAX_TURNS):
                out, err = Port.request(endpoint, system, messages)
                if err:
                    raise RuntimeError(err)
                fr = parse(out)
                ret += [b for h, b in fr if h == "re"]
                reqs = [(h, b) for h, b in fr if h != "re"]
                if not reqs:
                    break                                          # 不再请求：结束
                rs = [{"to": h, "reply": call(h, b)} for h, b in reqs]
                messages += [{"role": "assistant", "content": out},
                             {"role": "user", "content": json.dumps(rs, ensure_ascii=False)}]
            return "\n".join(ret)
        return fn

    # ------------------------------------------------------------ syscall
    def create(self, name: str) -> str:
        if name in self.channels:
            return "exists"
        self.channels[name] = Channel(name)
        Store.append(self.h / "_order", name)
        return "new"

    def add(self, channel: str, kind: str, text: str, bind=(), receptionist: bool = False, by: str = ROOT,
            tag: str | None = None, iface: str | None = None, at: int | None = None) -> str | None:
        if kind not in KINDS or channel not in self.channels:
            return None
        c = self.channels[channel]
        addr = str(len(c.actors) + 1)
        row = {"k": "place", "addr": addr, "kind": kind, "text": text,
               "bind": [b for b in bind if b in BINDS], "in": bool(receptionist), "by": by}
        if tag: row["tag"] = tag
        if iface: row["iface"] = iface
        if at is not None: row["at"] = at
        self._append(c, row)
        return addr

    def retire(self, channel: str, addr: str, by: str, by_channel: str | None) -> bool:
        c = self.channels.get(channel)
        if not c or addr not in c.actors or c.actors[addr].retired:
            return False
        if (channel == by_channel and addr == by) or addr == c.receptionist:   # 器官不能记下自己的死亡；接待员先换再退
            return False
        self._append(c, {"k": "retire", "addr": addr})
        return True

    def msg(self, channel: str, frm: str, to: str, body: str, run: int | None = None) -> dict:
        row = {"k": "msg", "from": frm, "to": to, "body": body}
        if run is not None: row["run"] = run
        return self._append(self.channels[channel], row)

    def _syscall(self, head: str, body: str, by: str, by_channel: str | None = None, at: int | None = None) -> tuple[str, str] | None:
        """执行一条 syscall 行。合法的头恰好一条回执（成功或 refused）；不是 syscall 的头返回 None。"""
        t = head.split()
        if t and t[0] == "channel.create" and len(t) == 2:
            return ("channel.create", f"{t[1]} {self.create(t[1])}")
        if t and t[0] == "channel.add.actor" and len(t) >= 3:
            bind, tag, iface, recept = [], None, None, False
            flags = t[3:]
            for i, f in enumerate(flags):
                if f == "in": recept = True
                elif f.startswith("bind="): bind = f[5:].split(",")
                elif f.startswith("tag="): tag = f[4:]
                elif f.startswith("iface="):                                   # iface 吞掉头行剩下的全部
                    iface = " ".join([f[6:], *flags[i + 1:]]); break
            addr = self.add(t[1], t[2], body, bind, receptionist=recept, by=by, tag=tag, iface=iface, at=at)
            return ("channel.add.actor", f"{t[1]}/{addr}" if addr else f"{t[1]} refused")
        if t and t[0] == "channel.retire.actor" and len(t) == 2 and "/" in t[1]:
            ch, _, addr = t[1].partition("/")
            return ("channel.retire.actor", t[1] if self.retire(ch, addr, by, by_channel) else f"{t[1]} refused")
        return None

    # ------------------------------------------------------------ 入口
    def drain(self) -> None:
        for box in [ROOT, *self.channels]:
            for p, at in Port.recv(self.ep(box), self.offsets.get(box, 0)):
                self.offsets[box] = at
                self._receive(box, p, at)

    def _receive(self, box: str, p: dict, at: int) -> None:
        sender, body = p.get("from", ""), p.get("body", "")
        if box != ROOT:
            self._inbound(self.channels[box], sender, body, at)
            return
        if not self.root_open:
            return                                                     # 关门后全部忽略
        head, _, rest = body.partition("\n")
        t = head.split()
        if t and t[0] == "msg" and len(t) == 2 and t[1] in self.channels:
            self._inbound(self.channels[t[1]], sender, rest, at, via_root=True)   # 第一条消息：关门
        else:
            self._syscall(head, rest, by=ROOT, at=at)

    def _inbound(self, c: Channel, sender: str, body: str, at: int | None = None, via_root: bool = False) -> None:
        if c.receptionist is None:
            return
        row = {"k": "msg", "from": self._door(c, sender) or "door", "to": c.receptionist, "body": body}
        if at is not None: row["at"] = at
        if via_root: row["by"] = ROOT                                  # 经根门来的第一条消息：偏移记在根收件箱上
        self._append(c, row)

    # ------------------------------------------------------------ 读介质
    def _resolve(self, c: Channel, head: str) -> Actor | None:
        if head in c.actors:
            return c.actors[head]                                      # 序号：个体的地址
        return next((a for a in reversed(list(c.actors.values())) if a.tag == head and not a.retired), None)   # 角色：后放的接替

    def _annot(self, r: dict) -> dict:
        """交出去的账本行：门的 place 行附上此刻的 local（指向本机 channel 吗）。channel 只增不减，local 只会由假变真。"""
        if r["k"] == "place" and r["kind"] == "door":
            return {**r, "local": r["text"] in self.channels}
        return r

    def members(self, c: Channel) -> list[dict]:
        return [{"addr": x.addr, "kind": x.kind, "bind": list(x.bind), "in": x.addr == c.receptionist, "retired": x.retired,
                 **({"tag": x.tag} if x.tag else {}), **({"iface": x.iface} if x.iface else {}),
                 **({"text": x.text, "local": x.text in self.channels} if x.kind == "door" else {})}
                for x in c.actors.values()]

    # ------------------------------------------------------------ 调用
    def _pending(self, c: Channel, addr: str) -> list[dict]:
        seen = c.cursor.get(addr, 0)
        return [r for r in c.rows if r["k"] == "msg" and r["to"] == addr and "run" not in r and r["seq"] > seen]

    def _dispatch(self, c: Channel, a: Actor, head: str, body: str, record: bool = True) -> str:
        """一次 call：投递到地址，返回对方的返回值。"""
        root = self._events[-1]
        if record and self._calls:
            self._calls[-1].append(f">>> {head}\n{body}\n<<<")
        t = head.split()
        if not t:
            return ""
        if t == [LEDGER]:
            w = body.split()
            if w and w[0] == "who":
                self.msg(c.name, LEDGER, a.addr, "who", run=root)
                return json.dumps(self.members(c), ensure_ascii=False)
            lo = int(w[1]) if len(w) > 1 and w[1].isdigit() else 1
            hi = int(w[2]) if len(w) > 2 and w[2].isdigit() else c.seq
            self.msg(c.name, LEDGER, a.addr, f"show {lo} {hi}", run=root)
            return "\n".join(json.dumps(self._annot(r), ensure_ascii=False) for r in c.rows if lo <= r["seq"] <= hi)
        if t[0].startswith("channel.") and "syscall" in a.bind:
            r = self._syscall(head, body, by=a.addr, by_channel=c.name)
            if not r:
                return ""
            self.msg(c.name, r[0], a.addr, r[1], run=root)
            return r[1]
        if t[0] in ACTIONS and t[0] in a.bind and len(t) == 2:
            res = ACTIONS[t[0]](self.P, t[1])
            self.msg(c.name, t[0], a.addr, res, run=root)
            return res
        tgt = self._resolve(c, t[0]) if len(t) == 1 else None
        if tgt is None:
            return ""                                                  # 不存在的地址：丢弃
        row = self.msg(c.name, a.addr, tgt.addr, body, run=root)
        if tgt.retired:
            return ""                                                  # 留在账上，不投递
        return self._invoke(c, tgt, row, caller=a)

    def _mview(self, c: Channel, m: dict) -> dict:
        return {"seq": m["seq"], "from": m["from"], "to": m["to"], "body": m["body"], "channel": c.name}

    def _invoke(self, c: Channel, a: Actor, m: dict, caller: Actor | None = None) -> str:
        """调用一次 actor：fn(m)。caller=None 是事件（推游标，返回值送回发送者），否则是嵌套的 call（返回值给请求者）。"""
        nested = caller is not None
        if not nested:
            self._events.append(m["seq"]); os.chdir(self.P)          # 程序的 cwd = P（工程约定）
        root = self._events[-1]
        self._calls.append([])
        try:
            if a.fn is None:
                reply, err = "", a.err or "not instantiated"
            else:
                try:
                    r = a.fn(self._mview(c, m))
                    reply, err = ("" if r is None else str(r)), ""
                except (Exception, SystemExit):                        # actor 的失败不是机器的失败
                    reply, err = "", traceback.format_exc(limit=3)
        finally:
            calls = self._calls.pop()
        out = "\n".join(calls + ([f">>> re\n{reply}\n<<<"] if reply else []))
        row = {"k": "step", "actor": a.addr, "upto": m["seq"], "out": out, "err": err}
        if nested: row["run"] = root
        self._append(c, row)
        if reply:
            if nested:
                self.msg(c.name, a.addr, caller.addr, reply, run=root)   # 返回值：请求者拿到
            else:
                self._dispatch(c, a, m["from"], reply, record=False)     # 事件：返回值送回发送者（门则出去）
        if not nested:
            self._events.pop()
        return reply

    # ------------------------------------------------------------ 驱动
    def run(self, max_steps: int = 10_000, serve: bool = False, poll: float = 0.2) -> int:
        steps = 0
        while steps < max_steps:
            self.drain()
            progressed = False
            for c in list(self.channels.values()):
                best = None
                for addr, x in c.actors.items():
                    if x.retired:
                        continue
                    p = self._pending(c, addr)
                    if p and (best is None or p[0]["seq"] < best[1]["seq"]):
                        best = (x, p[0])
                if best:
                    self._invoke(c, best[0], best[1]); steps += 1; progressed = True
            if not progressed:
                if not serve:
                    break
                time.sleep(poll)
        return steps


def parse(out: str) -> list[tuple[str, str]]:
    """把 oracle 的一段输出拆成帧：行首 ">>> " 起帧，"<<<" 一行收帧（末尾没收就到末尾）。帧内的 ">>> " 是正文；正文逐字节保留。"""
    res, head, buf = [], None, []
    for line in out.splitlines():
        if head is None:
            if line.startswith(">>> "):
                head, buf = line[4:].strip(), []
        elif line == "<<<":
            res.append((head, "\n".join(buf))); head = None
        else:
            buf.append(line)
    if head is not None:
        res.append((head, "\n".join(buf)))
    return [(h, b) for h, b in res if h]
