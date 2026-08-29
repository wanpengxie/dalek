"""运行时：极小状态空间 + 四行转移表。内容盲。

状态：每个 channel 一本只追加的账本（h/<name>.jsonl）+ 每个 actor 一个游标。
事件：某本账上多了一条写给某地址的消息。
转移表（按被写到的 actor 的 kind）：
  program   取视图 → Exec.run(text, 视图) → stdout 原样追加为 step + 拆成新消息追加；游标前移
  oracle    取视图 → 交给 Ω 侧的端点 → 回答同上
  door      把这条消息原样抄到 text 所指的账本，署名对面的门（没有则 door），收件人是对面的接待员
  place     介质动作：在某 channel 的下一个地址写下 kind + text（带完整 text 记一行）

这个文件只认识介质词汇：地址、kind、text、消息、账本、追加、投递、视图、步记录、放 actor、门。
它不认识任何组织词汇。检验：把 G 里所有名字换掉，本文件的行为逐字节不变。
"""
from __future__ import annotations
import json, time
from dataclasses import dataclass, field
from pathlib import Path
from typing import Callable
from omega import Exec, Store, Port

KINDS = ("program", "oracle", "door")
BINDS = ("place", "spawn")


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
    rows: list[dict] = field(default_factory=list)      # 账本（内存镜像，文件是持久层）
    cursor: dict[str, int] = field(default_factory=dict)  # actor 地址 → 看到哪
    inbox_offset: int = 0

    @property
    def seq(self) -> int:
        return self.rows[-1]["seq"] if self.rows else 0

    def door_to(self, target: str) -> str | None:
        return next((a.addr for a in self.actors.values() if a.kind == "door" and a.text == target), None)


Oracle = Callable[[str, str, Actor, list[dict]], str]   # (channel, addr, actor, msgs) -> 回答


class Runtime:
    def __init__(self, P: str | Path, oracle: Oracle | None = None):
        self.P = Path(P)
        self.h = self.P / "h"
        self.oracle = oracle
        self.channels: dict[str, Channel] = {}

    # ------------------------------------------------------------ 账本
    def _path(self, name: str) -> Path:
        return self.h / f"{name}.jsonl"

    def load(self) -> "Runtime":
        order = Store.read(self.h / "_order").split()
        for name in order:
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

    def _channel(self, name: str) -> Channel:
        if name not in self.channels:
            self.channels[name] = Channel(name)
            Store.append(self.h / "_order", name)
        return self.channels[name]

    # ------------------------------------------------------------ 介质动作
    def place(self, channel: str, kind: str, text: str, bind=(), receptionist: bool = False) -> str:
        assert kind in KINDS, kind
        c = self._channel(channel)
        addr = str(len(c.actors) + 1)
        self._append(c, {"k": "place", "addr": addr, "kind": kind, "text": text,
                         "bind": [b for b in bind if b in BINDS], "in": bool(receptionist)})
        return addr

    def msg(self, channel: str, frm: str, to: str, body: str) -> dict:
        return self._append(self.channels[channel], {"k": "msg", "from": frm, "to": to, "body": body})

    # ------------------------------------------------------------ 收件箱（膜外来的消息）
    def drain_inbox(self) -> bool:
        got = False
        for c in list(self.channels.values()):
            if c.receptionist is None:
                continue
            lines, off = Store.lines(self.P / "in" / f"{c.name}.jsonl", c.inbox_offset)
            for line in lines:
                if not line.strip():
                    continue
                p = json.loads(line)
                frm = c.door_to(p.get("from", "")) or "door"
                self.msg(c.name, frm, c.receptionist, p["body"]); got = True
            c.inbox_offset = off
        return got

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
        else:  # oracle：交给 Ω 侧的端点；没有端点就沉默
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
        if t and t[0] == "place" and "place" in a.bind and len(t) >= 3:
            flags = t[3:]
            bind = next((f[5:].split(",") for f in flags if f.startswith("bind=")), [])
            addr = self.place(t[1], t[2], body, bind, receptionist=("in" in flags))
            self.msg(c.name, "place", a.addr, f"{t[1]}/{addr}")
        elif t and t[0] == "spawn" and "spawn" in a.bind and len(t) == 2:
            d = Path(t[1]) if Path(t[1]).is_absolute() else self.P / t[1]
            pid = Exec.spawn([str(d / "init.py"), str(d), "--serve"], cwd=d, log=d / "init.log")
            self.msg(c.name, "spawn", a.addr, f"{d} pid={pid}")
        elif len(t) == 1 and t[0] in c.actors:
            self.msg(c.name, a.addr, t[0], body)
        # 其他：不是合法地址，也不是持有绑定的介质动作 → 只留在 step.out 里

    # ------------------------------------------------------------ 驱动
    def run(self, max_steps: int = 10_000, serve: bool = False, poll: float = 0.2) -> int:
        steps = 0
        while steps < max_steps:
            self.drain_inbox()
            progressed = False
            for c in list(self.channels.values()):
                # 一轮里每个 channel 走一步：最早的未读消息先投递（账本序）
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
                res.append((head, "\n".join(buf).rstrip("\n")))
            head, buf = line[4:].strip(), []
        elif head is not None:
            buf.append(line)
    if head is not None:
        res.append((head, "\n".join(buf).rstrip("\n")))
    return [(h, b) for h, b in res if h]
