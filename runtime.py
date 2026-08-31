"""运行时 R：极小状态空间 + 转移表。内容盲。

状态    每个 channel 一本只追加的账本（h/<name>.jsonl，四种行 place / retire / msg / step）+ 每个 actor 一个游标。
        全部由账本折叠重建，包括各收件箱读到哪（收进来的行带 at=收件箱偏移）和每个活成员的函数对象（重新实例化）。
actor   是常驻函数：折叠到 place 行时实例化一次（fn），挂在地址上，活到退役。两种 kind = 两种得到函数的方式：
  program   Exec.load(text, {call, me, channel})：源码在这个命名空间里跑一次，定义 run(m)；以后每条消息调同一个 run
  （L 也是 program：text 是它自己的 agent loop——组装、问端点、解帧、call 全在 text 里；"oracle" 是理论里的类别词，R 不认识）
  door      介质实现的函数体：Port.send 到 text 所指的端点，署名本 channel 的端点；返回空。门是连接
事件    某本账上多了一条写给某地址的消息（不带 run 标记的 msg 行）= 介质替发送者调用它：fn(m)，m = {seq, from, to, body, channel}；
        返回值就是回复，送回发送者（门则出去）。
call    每个 actor 专属的闭包 call(地址, 正文) → 返回值：解析名字、记一行请求、调对方的 fn（嵌套 = 调用栈）、记一行返回。
        这是 actor 作用于机器的唯一方式；它在 run 里面对世界做的事（文件、网络）介质不知道、不记。
        运行中产生的每一行都带 run=<事件 seq>；只有事件推游标。前面放的 actor 用得了后面放的：名字在调用那一刻解析。
call 的地址
  <tag>（数字序号仍供 R 内部回复/兼容）                     本 channel 的成员：tag 是 channel 内唯一逻辑地址；重名时 R 分配 tag1、tag2
  0                                                          介质的读地址，人人可用：show [a] [b] → 账本行（门的 place 行附此刻的 local）；who → 此刻的成员表
                                                             账上记事实行 msg from=0 body="show a b" / "who"，内容可重算不入账
  channel.create <name> / channel.add.actor <channel> <kind> [in] [bind=…] [tag=…] [iface=…] / channel.retire.actor <channel>/<tag>
                                                             syscall；需 bind=syscall；返回结果或 "<参数> refused"
  <动词> [参数]                                                绑定了的 world 动词（ACTIONS：`spawn <目录>` 起一台机器 / `stop` 停本机）；需 bind=<动词>
  写给门 = 送出去，返回空；写给不存在的地址 = 丢弃，返回空；写给请求者 = 再调用它一次（可重入）
账本    每次 call 两行 msg（请求、返回）；每次调用一行 step（out = 它发出的每个 call，加一帧 re = 它的返回值；err = 异常）。
        实例化失败的成员每次被调用都 err；调用抛异常也 err——器官的失败不是机器的失败。
接待员  只由 place 行的 in 决定，没有默认：G 不写就没有（外来消息落空）。
起停    物理只在机器的生命周期边界（G 的首 channel）留痕：已出生的机器醒来/停下时，各记一行 msg _root→接待员 up/down。
        内部 channel 不被 R 直接注入物理消息；边界 actor 如需通知内脏，经门用普通协作消息翻译。
        醒来是外到内（先有物理才有身体）：R 写 up，接待员把它翻译成协作。停下是内到外：唯一合法路径是持 bind=stop 的成员调无参 `stop`
        （对象天然是本 Space；停一个成员是 retire，不是 stop）→ R 置意向 → 在两个事件之间写 down → 跑完账上已有的事 → 退出。
        外面直接杀进程不是合法停止而是故障：没有 down，下次醒来看到最后的 start/up 没有配对的 down，据此判定上次异常终止。未写 step 的消息重启后重跑。
        channel 存在 ⇔ 账本至少一条 place 行；空账本（损伤）= 不存在，create 返回 new。
入口    每个 channel 一个收件箱 in/<name>.jsonl（Port 的接收侧）：一行 → msg 给接待员（事件），署名指回发信端点的门（没有则 door）。
        根收件箱 in/_root.jsonl 在 channel 之前存在：开着 ⇔ 所有账本无 msg 行。开着时接受两个 syscall（by=_root）
        和 msg <channel>（第一条消息，顺手关门）。关门后忽略。
调度    轮转每个 channel，各取最早的事件调用一次；静止则停（serve 模式下继续轮询）。

本文件只认识介质词汇：地址、角色、kind、text、消息、账本、追加、投递、调用、返回、步记录、门、端点、syscall、动词、退役。
它不认识任何组织词汇。检验：把 G 里所有名字换掉，本文件的行为逐字节不变。
"""
from __future__ import annotations
import json, os, re, time, traceback
from dataclasses import dataclass, field
from pathlib import Path
from omega import Exec, Store, Port


KINDS = ("program", "door")
ROOT = "_root"
LEDGER = "0"                                   # 每个 channel 的读地址：写给它 = 读它；不是成员，不放、不退、不遗传


def _spawn(rt: "Runtime", d: str) -> str:
    """起一台机器：<d> 相对本机目录或绝对路径。用目标目录自己的 world（init.py）跑它——同一个 P 再起一次 = 唤醒（同一个体醒来）。"""
    target = (rt.P / d).resolve()
    pid = Exec.spawn(["init.py", str(target), "--serve"], cwd=target, log=target / "log")
    return f"{d} pid={pid}"


def _stop(rt: "Runtime") -> str:
    """停下本机：合法关闭协议的最后一步。**无参数**——对象天然是当前 Space（不是别的进程：那是膜外的物理强杀；也不是某个成员：停成员是 retire）。
    只置意向，不在这里写行：根边界的 down 由主循环在两个事件之间写，不插进调用者的运行里（那会撞序号）。"""
    rt._stopping = True
    return "stopping"


ACTIONS = {"spawn": (_spawn, 1), "stop": (_stop, 0)}          # 动词 → (实现, 参数个数)
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
        self._stopping = False                       # 有人调过 stop：不再收新信，跑完就退出
        self._downed = False                         # 根边界的 down 已写（一次）

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
        """纯折叠：不写任何行。channel 存在 ⇔ 它的账本至少一条 place 行（不看 _order）。"""
        for name in dict.fromkeys(Store.read(self.h / "_order").split()):
            lines = Store.read(self._path(name)).splitlines()
            if not any(json.loads(l)["k"] == "place" for l in lines):
                continue                                                # 空账本 = 不存在（损伤或从未放过）
            c = self.channels[name] = Channel(name)
            for line in lines:
                self._fold(c, json.loads(line))
        return self

    def wake(self) -> "Runtime":
        """醒来入账：已出生的机器只在首 channel 追加 msg _root→接待员 "up"。
        这一个边界事件标记本次 Runtime incarnation；所有 actor 重新实例化（Σ 归零）是它与 place/retire 历史的派生事实。
        未出生不发：出生的第一条消息仍是父代的 start。"""
        if not self.root_open:
            self._lifecycle("up")
        return self

    def _lifecycle(self, body: str) -> None:
        """介质在机器生命周期边界打唯一记号。边界是 _order 的首 channel，不靠组织名字识别。
        首 channel 损伤时不把物理事件偷偷转移给下一个内脏：那是根器官损伤，须另行定义恢复路径。"""
        order = Store.read(self.h / "_order").split()
        c = self.channels.get(order[0]) if order else None
        if c is not None and c.receptionist is not None:
            self.msg(c.name, ROOT, c.receptionist, body)

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

    # ------------------------------------------------------------ 实例化：两种得到函数的方式
    def _caller(self, c: Channel, a: Actor):
        def call(to, body=""):
            return self._dispatch(c, a, str(to), "" if body is None else str(body))
        return call

    def _instantiate(self, c: Channel, a: Actor) -> None:
        try:
            if a.kind == "program":
                a.fn = Exec.load(a.text, {"call": self._caller(c, a), "me": a.addr, "channel": c.name})
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

    # ------------------------------------------------------------ syscall
    def create(self, name: str) -> str:
        if name in self.channels:
            return "exists"
        self.channels[name] = Channel(name)
        if name not in Store.read(self.h / "_order").split():
            Store.append(self.h / "_order", name)
        return "new"

    def _tag(self, c: Channel, requested: str | None) -> str | None:
        """分配 channel 内唯一的逻辑地址。命名策略是介质 ABI：t → t1 → t2；数字 addr 只留在 H 内部。"""
        base = requested or "t"
        if not re.fullmatch(r"[A-Za-z_][A-Za-z0-9_-]*", base):
            return None
        used = {a.tag for a in c.actors.values() if not a.retired}
        if base not in used:
            return base
        n = 1
        while f"{base}{n}" in used:
            n += 1
        return f"{base}{n}"

    def add(self, channel: str, kind: str, text: str, bind=(), receptionist: bool = False, by: str = ROOT,
            tag: str | None = None, iface: str | None = None, at: int | None = None) -> str | None:
        if kind not in KINDS or channel not in self.channels:
            return None
        c = self.channels[channel]
        tag = self._tag(c, tag)
        if tag is None:
            return None
        addr = str(len(c.actors) + 1)
        row = {"k": "place", "addr": addr, "kind": kind, "text": text,
               "bind": [b for b in bind if b in BINDS], "in": bool(receptionist), "by": by, "tag": tag}
        if iface: row["iface"] = iface
        if at is not None: row["at"] = at
        self._append(c, row)
        return tag

    def retire(self, channel: str, tag: str, by: str, by_channel: str | None) -> bool:
        c = self.channels.get(channel)
        a = next((a for a in c.actors.values() if a.tag == tag and not a.retired), None) if c else None
        if a is None:
            return False
        if (channel == by_channel and a.addr == by) or a.addr == c.receptionist:   # 器官不能记下自己的死亡；接待员先换再退
            return False
        self._append(c, {"k": "retire", "addr": a.addr})
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
            effective = self.add(t[1], t[2], body, bind, receptionist=recept, by=by, tag=tag, iface=iface, at=at)
            return ("channel.add.actor", f"{t[1]}/{effective}" if effective else f"{t[1]} refused")
        if t and t[0] == "channel.retire.actor" and len(t) == 2 and "/" in t[1]:
            ch, _, tag = t[1].partition("/")
            return ("channel.retire.actor", t[1] if self.retire(ch, tag, by, by_channel) else f"{t[1]} refused")
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
            if box in self.channels:
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
            return c.actors[head]                                      # 数字序号：H 内部回复与兼容
        return next((a for a in c.actors.values() if a.tag == head and not a.retired), None)   # tag：channel 内唯一逻辑地址

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
        if t[0] in ACTIONS and t[0] in a.bind and len(t) == ACTIONS[t[0]][1] + 1:
            res = ACTIONS[t[0]][0](self, *t[1:])
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
        """驱动到静止。serve：静止后轮询收件箱。
        关机只有一条路：持 bind=stop 的成员调 `stop` → 置意向 → 这里在两个事件之间写根边界的 down → 跑完账上已有的事 → 退出。
        置了意向就不再收新信（留在收件箱等下次醒来），所以关机一定终止。外面直接杀进程不走这条路：没有 down，下次醒来据此判定上次是异常终止。"""
        steps = 0
        while steps < max_steps:
            if self._stopping:
                if not self._downed:                                 # 安全点：不在别人的运行里插行
                    self._downed = True
                    if not self.root_open:
                        self._lifecycle("down")
            else:
                self.drain()                                         # 关机中不再收新信
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
                if not serve or self._stopping:
                    break
                time.sleep(poll)
        return steps


def parse(out: str) -> list[tuple[str, str]]:
    """读 step.out：R 记每次 call 用的帧格式——行首 ">>> 地址" 起帧，单独一行 "<<<" 收帧（末尾没收就到末尾）。帧内的 ">>> " 是正文。"""
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
