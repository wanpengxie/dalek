"""运行时 R：极小状态空间 + 转移表 + 一扇根门。内容盲。

状态：每个 channel 一本只追加的账本（h/<name>.jsonl）+ 每个 actor 一个游标。
事件：某本账上多了一条写给某地址的消息。
转移表（按被写到的 actor 的 kind）：
  program   取视图 → Exec.run(text, 视图) → stdout 原样追加为 step + 拆成新消息追加；游标前移
  oracle    取视图 → 交给 Ω 侧的端点 → 回答同上
  door      把这条消息原样抄到 text 所指的账本，署名对面的门（没有则 door），收件人是对面的接待员
syscall（三个词；持有 bind=syscall 的 actor 可发；根门开着时膜外可发）：
  channel.create <name>
  channel.add.actor <channel> <kind> [in] [bind=…] + text     （含 actor.create；带完整 text 记一行）
根门（in/_root.jsonl，Space 级，在 channel 之前存在）：
  开着 ⇔ 所有账本里没有任何 msg 行。开着时接受两个 syscall 和 msg；msg 是第一条消息，顺手关门。关门后忽略。

这个文件只认识介质词汇：地址、kind、text、消息、账本、追加、投递、视图、步记录、门、syscall。
它不认识任何组织词汇。检验：把 G 里所有名字换掉，本文件的行为逐字节不变。
"""
from __future__ import annotations
import json, time
from dataclasses import dataclass, field
from pathlib import Path
from typing import Callable
from omega import Exec, Store, Port

KINDS = ("program", "oracle", "door")
BINDS = ("syscall", "spawn")
ROOT = "_root"


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
    inbox_offset: int = 0

    @property
    def seq(self) -> int:
        return self.rows[-1]["seq"] if self.rows else 0

    def door_to(self, target: str) -> str | None:
        return next((a.addr for a in self.actors.values() if a.kind == "door" and a.text == target), None)


Oracle = Callable[[str, str, Actor, list[dict]], str]


class Runtime:
    def __init__(self, P: str | Path, oracle: Oracle | None = None):
        self.P = Path(P)
        self.h = self.P / "h"
        self.oracle = oracle
        self.channels: dict[str, Channel] = {}
        self.root_offset = 0

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
    def create(self, name: str) -> bool:
        if name in self.channels:
            return False
        self.channels[name] = Channel(name)
        Store.append(self.h / "_order", name)
        return True

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
        """执行一条 syscall 行；返回 (op, 结果) 或 None。"""
        t = head.split()
        if t and t[0] == "channel.create" and len(t) == 2:
            self.create(t[1]); return ("channel.create", t[1])
        if t and t[0] == "channel.add.actor" and len(t) >= 3:
            flags = t[3:]
            bind = next((f[5:].split(",") for f in flags if f.startswith("bind=")), [])
            addr = self.add(t[1], t[2], body, bind, receptionist=("in" in flags), by=by)
            return ("channel.add.actor", f"{t[1]}/{addr}") if addr else None
        return None

    # ------------------------------------------------------------ 收件箱
    def drain_root(self) -> None:
        lines, off = Store.lines(self.P / "in" / f"{ROOT}.jsonl", self.root_offset)
        self.root_offset = off
        for line in lines:
            if not line.strip() or not self.root_open:
                continue                                   # 关门后全部忽略
            p = json.loads(line)
            head, _, body = p["body"].partition("\n")
            t = head.split()
            if t and t[0] == "msg" and len(t) == 2 and t[1] in self.channels:
                c = self.channels[t[1]]
                if c.receptionist is not None:
                    self.msg(c.name, c.door_to(p.get("from", "")) or "door", c.receptionist, body)   # 关门
            else:
                self._syscall(head, body, by=ROOT)

    def drain_inbox(self) -> None:
        for c in list(self.channels.values()):
            if c.receptionist is None:
                continue
            lines, off = Store.lines(self.P / "in" / f"{c.name}.jsonl", c.inbox_offset)
            c.inbox_offset = off
            for line in lines:
                if line.strip():
                    p = json.loads(line)
                    self.msg(c.name, c.door_to(p.get("from", "")) or "door", c.receptionist, p["body"])

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
                self._deliver(c, a, m["body"])
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

    def _deliver(self, c: Channel, door: Actor, body: str) -> None:
        target = door.text
        if target in self.channels:
            t = self.channels[target]
            if t.receptionist is not None:
                self.msg(t.name, t.door_to(c.name) or "door", t.receptionist, body)
        else:
            Port.send(target, {"from": f"file:{self.P}#{c.name}", "body": body})

    def _execute(self, c: Channel, a: Actor, head: str, body: str) -> None:
        t = head.split()
        if t and t[0].startswith("channel.") and "syscall" in a.bind:
            r = self._syscall(head, body, by=a.addr)
            if r:
                self.msg(c.name, r[0], a.addr, r[1])
        elif t and t[0] == "spawn" and "spawn" in a.bind and len(t) == 2:
            d = Path(t[1]) if Path(t[1]).is_absolute() else self.P / t[1]
            pid = Exec.spawn([str(d / "init.py"), str(d), "--serve"], cwd=d, log=d / "init.log")
            self.msg(c.name, "spawn", a.addr, f"{d} pid={pid}")
        elif len(t) == 1 and t[0] in c.actors:
            self.msg(c.name, a.addr, t[0], body)

    # ------------------------------------------------------------ 驱动
    def run(self, max_steps: int = 10_000, serve: bool = False, poll: float = 0.2) -> int:
        steps = 0
        while steps < max_steps:
            self.drain_root(); self.drain_inbox()
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
                res.append((head, "\n".join(buf)))          # 正文逐字节保留（含末尾换行）
            head, buf = line[4:].strip(), []
        elif head is not None:
            buf.append(line)
    if head is not None:
        res.append((head, "\n".join(buf)))
    return [(h, b) for h, b in res if h]
