"""运行时 R：极小状态空间 + 转移表。内容盲。

状态    每个 channel 一本只追加的账本（h/<name>.jsonl，三种行 place / msg / step）+ 每个 actor 一个游标。
        全部由账本折叠重建；进程里只另记各收件箱读到的字节偏移。
事件    某本账上多了一条写给某地址的消息。
转移表（按被写到的 actor 的 kind）
  program   视图 → Exec.run(text, 视图) → stdout 原样记 step，按 ">>> " 拆成动作
  oracle    视图 → Ω 侧的端点 → 同上
  door      每条消息原样 Port.send 到 text 所指的端点，署名本 channel 的端点
动作（program / oracle 输出的每一段）
  >>> <addr>                                                 消息给本 channel 的 <addr>
  >>> channel.create <name>                                  syscall；需 bind=syscall
  >>> channel.add.actor <channel> <kind> [in] [bind=…] + text  syscall；需 bind=syscall
  >>> <动词> <参数>                                            绑定了的 world 动词（ACTIONS 表：起一台机器 / 停一个进程，用 Ω 实现）；需 bind=<动词>
  返回都是一条 msg：from=<动作名>。其他一律忽略。
入口    每个 channel 一个收件箱 in/<name>.jsonl（Port 的接收侧）：一行 → msg 给接待员，署名指回发信端点的门（没有则 door）。
        根收件箱 in/_root.jsonl 在 channel 之前存在：开着 ⇔ 所有账本无 msg 行。开着时接受两个 syscall（by=_root）
        和 msg <channel>（第一条消息，顺手关门）。关门后忽略。
调度    轮转每个 channel，各取 pending 最早的 actor 走一步；静止则停（serve 模式下继续轮询）。

本文件只认识介质词汇：地址、kind、text、消息、账本、追加、投递、视图、步记录、门、端点、syscall、动词。
它不认识任何组织词汇。检验：把 G 里所有名字换掉，本文件的行为逐字节不变。
"""
from __future__ import annotations
import json, time
from dataclasses import dataclass, field
from pathlib import Path
from typing import Callable
from omega import Exec, Store, Port

KINDS = ("program", "oracle", "door")
ROOT = "_root"


def _spawn(P: Path, arg: str) -> str:
    d = Path(arg) if Path(arg).is_absolute() else P / arg
    pid = Exec.spawn([str(d / "init.py"), str(d), "--serve"], cwd=d, log=d / "init.log")
    return f"{d} pid={pid}"


def _stop(P: Path, arg: str) -> str:
    Exec.stop(int(arg))
    return arg


ACTIONS: dict[str, Callable[[Path, str], str]] = {"spawn": _spawn, "stop": _stop}   # 可绑定的 world 动词：spawn 知道 loader 协议（init.py <P> --serve），这是 world 知道自己的布局
BINDS = ("syscall", *ACTIONS)


@dataclass
class Actor:
    addr: str
    kind: str
    text: str
    bind: tuple[str, ...] = ()


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


Oracle = Callable[[str, str, Actor, list[dict]], str]


class Runtime:
    def __init__(self, P: str | Path, oracle: Oracle | None = None):
        self.P = Path(P)
        self.h = self.P / "h"
        self.oracle = oracle
        self.channels: dict[str, Channel] = {}
        self.offsets: dict[str, int] = {}

    # ------------------------------------------------------------ 端点
    def ep(self, box: str) -> str:
        return f"file:{self.P}#{box}"

    def _target(self, text: str) -> str:
        return text if ":" in text else self.ep(text)          # 门的 text：端点，或本机 channel 名

    def _door(self, c: Channel, sender: str) -> str | None:
        return next((a.addr for a in c.actors.values()
                     if a.kind == "door" and (a.text == sender or self.ep(a.text) == sender)), None)

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
            c.actors[row["addr"]] = Actor(row["addr"], row["kind"], row["text"], tuple(row.get("bind", ())))
            if row.get("in") or c.receptionist is None:
                c.receptionist = row["addr"]
        elif k == "step":
            c.cursor[row["actor"]] = row["upto"]

    @property
    def root_open(self) -> bool:
        return not any(r["k"] == "msg" for c in self.channels.values() for r in c.rows)

    # ------------------------------------------------------------ syscall
    def create(self, name: str) -> str:
        if name in self.channels:
            return "exists"
        self.channels[name] = Channel(name)
        Store.append(self.h / "_order", name)
        return "new"

    def add(self, channel: str, kind: str, text: str, bind=(), receptionist: bool = False, by: str = ROOT) -> str | None:
        if kind not in KINDS or channel not in self.channels:
            return None
        c = self.channels[channel]
        addr = str(len(c.actors) + 1)
        self._append(c, {"k": "place", "addr": addr, "kind": kind, "text": text,
                         "bind": [b for b in bind if b in BINDS], "in": bool(receptionist), "by": by})
        return addr

    def msg(self, channel: str, frm: str, to: str, body: str) -> dict:
        return self._append(self.channels[channel], {"k": "msg", "from": frm, "to": to, "body": body})

    def _syscall(self, head: str, body: str, by: str) -> tuple[str, str] | None:
        t = head.split()
        if t and t[0] == "channel.create" and len(t) == 2:
            return ("channel.create", f"{t[1]} {self.create(t[1])}")
        if t and t[0] == "channel.add.actor" and len(t) >= 3:
            flags = t[3:]
            bind = next((f[5:].split(",") for f in flags if f.startswith("bind=")), [])
            addr = self.add(t[1], t[2], body, bind, receptionist=("in" in flags), by=by)
            return ("channel.add.actor", f"{t[1]}/{addr}") if addr else None
        return None

    # ------------------------------------------------------------ 入口
    def drain(self) -> None:
        for box in [ROOT, *self.channels]:
            payloads, self.offsets[box] = Port.recv(self.ep(box), self.offsets.get(box, 0))
            for p in payloads:
                self._receive(box, p)

    def _receive(self, box: str, p: dict) -> None:
        sender, body = p.get("from", ""), p.get("body", "")
        if box != ROOT:
            self._inbound(self.channels[box], sender, body)
            return
        if not self.root_open:
            return                                                     # 关门后全部忽略
        head, _, rest = body.partition("\n")
        t = head.split()
        if t and t[0] == "msg" and len(t) == 2 and t[1] in self.channels:
            self._inbound(self.channels[t[1]], sender, rest)           # 第一条消息：关门
        else:
            self._syscall(head, rest, by=ROOT)

    def _inbound(self, c: Channel, sender: str, body: str) -> None:
        if c.receptionist is not None:
            self.msg(c.name, self._door(c, sender) or "door", c.receptionist, body)

    # ------------------------------------------------------------ 转移
    def _pending(self, c: Channel, addr: str) -> list[dict]:
        seen = c.cursor.get(addr, 0)
        return [r for r in c.rows if r["k"] == "msg" and r["to"] == addr and r["seq"] > seen]

    def step(self, channel: str, addr: str) -> bool:
        c = self.channels[channel]; a = c.actors[addr]
        msgs = self._pending(c, addr)
        if not msgs:
            return False
        upto = msgs[-1]["seq"]
        if a.kind == "door":
            for m in msgs:
                Port.send(self._target(a.text), {"from": self.ep(channel), "body": m["body"]})
            self._append(c, {"k": "step", "actor": addr, "upto": upto, "out": "", "err": ""})
            return True
        view = {"channel": channel, "me": addr,
                "msgs": [{k: m[k] for k in ("seq", "from", "to", "body")} for m in msgs]}
        if a.kind == "program":
            out, err = Exec.run(a.text, json.dumps(view, ensure_ascii=False), cwd=self.P)
        else:
            out, err = (self.oracle(channel, addr, a, view["msgs"]) if self.oracle else ""), ""
        self._append(c, {"k": "step", "actor": addr, "upto": upto, "out": out, "err": err})
        for head, body in parse(out):
            self._execute(c, a, head, body)
        return True

    def _execute(self, c: Channel, a: Actor, head: str, body: str) -> None:
        t = head.split()
        if not t:
            return
        if t[0].startswith("channel.") and "syscall" in a.bind:
            r = self._syscall(head, body, by=a.addr)
            if r:
                self.msg(c.name, r[0], a.addr, r[1])
        elif t[0] in ACTIONS and t[0] in a.bind and len(t) == 2:
            self.msg(c.name, t[0], a.addr, ACTIONS[t[0]](self.P, t[1]))
        elif len(t) == 1 and t[0] in c.actors:
            self.msg(c.name, a.addr, t[0], body)

    # ------------------------------------------------------------ 驱动
    def run(self, max_steps: int = 10_000, serve: bool = False, poll: float = 0.2) -> int:
        steps = 0
        while steps < max_steps:
            self.drain()
            progressed = False
            for c in list(self.channels.values()):
                best = None
                for addr in c.actors:
                    p = self._pending(c, addr)
                    if p and (best is None or p[0]["seq"] < best[1]):
                        best = (addr, p[0]["seq"])
                if best:
                    self.step(c.name, best[0]); steps += 1; progressed = True
            if not progressed:
                if not serve:
                    break
                time.sleep(poll)
        return steps


def parse(out: str) -> list[tuple[str, str]]:
    res, head, buf = [], None, []
    for line in out.splitlines():
        if line.startswith(">>> "):
            if head is not None:
                res.append((head, "\n".join(buf)))          # 正文逐字节保留
            head, buf = line[4:].strip(), []
        elif head is not None:
            buf.append(line)
    if head is not None:
        res.append((head, "\n".join(buf)))
    return [(h, b) for h, b in res if h]
