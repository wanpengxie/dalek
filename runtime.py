"""运行时 R：极小状态空间 + 转移表。内容盲。

状态    每个 channel 一本只追加的账本（h/<name>.jsonl，四种行 place / retire / msg / step）+ 每个 actor 一个游标。
        全部由账本折叠重建，包括各收件箱读到哪（收进来的行带 at=收件箱偏移）。
事件    某本账上多了一条写给某地址的消息（不带 run 标记的 msg 行）。一条事件 = 一次运行。
运行    收到初始消息 → 过程 { 请求(地址, 正文) → 回复 }* → 结束。运行中 actor 可以请求本 channel 的任何地址，
        介质同步地让对方跑一次运行（嵌套），对方写给请求者的就是回复（可为空）。运行中产生的每一行都带 run=<事件 seq>。
        运行之间 actor 无私有状态；运行之内它是一个进程。
转移表（按初始消息落在的 actor 的 kind；退役的不排）
  program   Exec.open(text)：stdin 先给初始消息 {seq, from, to, body, channel} 一行 JSON；之后 actor 每写一帧
            ">>> <地址>\\n<正文>\\n<<<" 介质就投递、把回复 "<正文>\\n<<<" 写回 stdin；进程退出 = 运行结束
            （帧内只有单独一行 "<<<" 收帧，正文里的 ">>> " 是正文；正文唯一不能含的是单独一行 "<<<"）
  oracle    组装：介质替它向 0 要整本账（记一行 show 事实）+ 成员表 → Port.request(text, 对话) 多轮：模型输出的每帧
            是一个请求，回复作为下一轮 user 消息喂回；模型不再输出帧 = 运行结束
  door      把初始消息原样 Port.send 到 text 所指的端点，署名本 channel 的端点。门是连接，不回复
请求的地址（帧的第一行）
  <序号> 或 <角色>                                           本 channel 的成员：序号是个体的地址，角色是形态里的名字（place 行的 tag，后放的接替先放的）
  0                                                          账本（介质的读地址，人人可用）：正文 show [a] [b]；账上记事实行 msg from=0 body="show a b"，回复是那些行
                                                             （门的 place 行附上此刻的 local：指向本机 channel 吗）
  channel.create <name> / channel.add.actor <channel> <kind> [in] [bind=…] [tag=…] [iface=…] / channel.retire.actor <channel>/<addr>
                                                             syscall；需 bind=syscall；回复是结果或 "<参数> refused"
  <动词> <参数>                                                绑定了的 world 动词（ACTIONS：起一台机器 / 停一个进程）；需 bind=<动词>
  re                                                         回复：写给初始消息的发送者。嵌套运行里 = 请求者拿到的回复；事件运行里 = 普通消息
  写给门 = 送出去，回复为空；写给不存在的地址 = 丢弃，回复为空；写给请求者的序号/角色 = 再起一次它的运行（可重入）
接待员  只由 place 行的 in 决定，没有默认：G 不写就没有（外来消息落空）。
入口    每个 channel 一个收件箱 in/<name>.jsonl（Port 的接收侧）：一行 → msg 给接待员（事件），署名指回发信端点的门（没有则 door）。
        根收件箱 in/_root.jsonl 在 channel 之前存在：开着 ⇔ 所有账本无 msg 行。开着时接受两个 syscall（by=_root）
        和 msg <channel>（第一条消息，顺手关门）。关门后忽略。
调度    轮转每个 channel，各取最早的事件跑一次运行；静止则停（serve 模式下继续轮询）。

本文件只认识介质词汇：地址、角色、kind、text、消息、账本、追加、投递、帧、回复、运行、步记录、门、端点、syscall、动词、退役。
它不认识任何组织词汇。检验：把 G 里所有名字换掉，本文件的行为逐字节不变。
"""
from __future__ import annotations
import json, os, select, time
from dataclasses import dataclass, field
from pathlib import Path
from omega import Exec, Store, Port


KINDS = ("program", "oracle", "door")
ROOT = "_root"
LEDGER = "0"                                   # 每个 channel 的读地址：写给它 = 读它；不是成员，不放、不退、不遗传
RUN_TIMEOUT = 60.0                             # 一次运行的上限（秒）
MAX_TURNS = 16                                 # oracle 一次运行最多几轮


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
        self.timeout = RUN_TIMEOUT

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
            c.actors[row["addr"]] = Actor(row["addr"], row["kind"], row["text"], tuple(row.get("bind", ())),
                                          row.get("tag"), row.get("iface"))
            if row.get("in"):
                c.receptionist = row["addr"]                # 没有默认：接待员只由显式 in 决定
        elif k == "retire":
            a = c.actors.get(row["addr"])
            if a:
                a.retired = True                          # 接待员退不了（retire 拒绝），所以接待员不变
        elif k == "step" and "run" not in row:
            c.cursor[row["actor"]] = row["upto"]          # 只有事件的运行推游标
        if "at" in row:                                   # 收件箱读到哪，从账本折出来
            box = ROOT if row.get("by") == ROOT or row.get("from") == ROOT else c.name
            self.offsets[box] = max(self.offsets.get(box, 0), row["at"])

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

    # ------------------------------------------------------------ 寻址
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

    # ------------------------------------------------------------ 转移
    def _pending(self, c: Channel, addr: str) -> list[dict]:
        seen = c.cursor.get(addr, 0)
        return [r for r in c.rows if r["k"] == "msg" and r["to"] == addr and "run" not in r and r["seq"] > seen]

    def _dispatch(self, c: Channel, a: Actor, m: dict, head: str, body: str, root: int, caller: Actor | None, replies: list) -> str:
        """一帧：投递到地址，返回回复。"""
        t = head.split()
        if not t:
            return ""
        if t == ["re"]:
            if caller is not None:                                     # 嵌套：这是回复
                self.msg(c.name, a.addr, caller.addr, body, run=root); replies.append(body)
                return ""
            t = [m["from"]]                                            # 事件：写给发送者
        if t == [LEDGER]:
            w = body.split()
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
        if tgt.kind == "door":
            Port.send(self._target(tgt.text), {"from": self.ep(c.name), "body": body})
            self._append(c, {"k": "step", "actor": tgt.addr, "upto": row["seq"], "out": "", "err": "", "run": root})
            return ""
        return "\n".join(self._run(c, tgt, row, root, caller=a))

    def _run(self, c: Channel, a: Actor, m: dict, root: int | None = None, caller: Actor | None = None) -> list[str]:
        """一次运行。返回它写给请求者的回复。root=None 是事件（推游标），否则是嵌套的请求。"""
        nested = root is not None
        root = m["seq"] if root is None else root
        replies: list[str] = []
        if a.kind == "door":
            Port.send(self._target(a.text), {"from": self.ep(c.name), "body": m["body"]})
            out, err = "", ""
        elif a.kind == "program":
            out, err = self._run_program(c, a, m, root, caller, replies)
        else:
            out, err = self._run_oracle(c, a, m, root, caller, replies)
        row = {"k": "step", "actor": a.addr, "upto": m["seq"], "out": out, "err": err}
        if nested: row["run"] = root
        self._append(c, row)
        return replies

    def _mview(self, c: Channel, m: dict) -> str:
        return json.dumps({"seq": m["seq"], "from": m["from"], "to": m["to"], "body": m["body"], "channel": c.name}, ensure_ascii=False)

    def _run_program(self, c: Channel, a: Actor, m: dict, root: int, caller, replies) -> tuple[str, str]:
        p = Exec.open(a.text, cwd=self.P)
        frames, err, cur, buf = [], "", None, b""
        deadline = time.monotonic() + self.timeout

        def write(s: str) -> None:
            try:
                p.stdin.write(s.encode("utf-8")); p.stdin.flush()
            except (BrokenPipeError, OSError):
                pass                                                   # 对方不读回复（说完就走）

        def frame(lines: list[str]) -> None:
            head, body = lines[0][4:].strip(), "\n".join(lines[1:])
            frames.append("\n".join(lines) + "\n<<<")
            reply = self._dispatch(c, a, m, head, body, root, caller, replies)
            write((reply + "\n" if reply else "") + "<<<\n")

        write(self._mview(c, m) + "\n")
        fd = p.stdout.fileno()
        while True:
            remaining = deadline - time.monotonic()
            ready = remaining > 0 and select.select([fd], [], [], remaining)[0]
            if not ready:
                p.kill(); err = f"timeout {self.timeout:g}s"; break
            chunk = os.read(fd, 1 << 16)
            if not chunk:
                break                                                  # EOF：运行结束
            buf += chunk
            while b"\n" in buf:
                line, buf = buf.split(b"\n", 1)
                s = line.decode("utf-8", "replace")
                if cur is None:
                    if s.startswith(">>> "): cur = [s]                 # 帧外：只认帧头
                elif s == "<<<":
                    frame(cur); cur = None                             # 帧内：只有 <<< 收帧，正文里的 ">>> " 是正文
                else:
                    cur.append(s)
        if cur:
            frame(cur)                                                 # 说完就走的最后一帧
        try:
            p.stdin.close()
        except OSError:
            pass
        rc = p.wait()
        p.errfile.seek(0); stderr = p.errfile.read().decode("utf-8", "replace"); p.errfile.close()
        if rc != 0 and not err:
            err = f"exit {rc}\n{stderr}"
        return "\n".join(frames), err

    def _run_oracle(self, c: Channel, a: Actor, m: dict, root: int, caller, replies) -> tuple[str, str]:
        hi = c.seq
        self.msg(c.name, LEDGER, a.addr, f"show 1 {hi}", run=root)                 # 组装：读在账上
        view = {"msg": json.loads(self._mview(c, m)), "ledger": [self._annot(r) for r in c.rows if r["seq"] <= hi],
                "members": self.members(c)}
        messages = [{"role": "user", "content": json.dumps(view, ensure_ascii=False)}]
        outs, err = [], ""
        for _ in range(MAX_TURNS):
            out, err = Port.request(a.text, messages)
            if err:
                break
            outs.append(out)
            fr = parse(out)
            if not fr:
                break                                                  # 不再请求：运行结束
            rs = [{"to": h, "reply": self._dispatch(c, a, m, h, b, root, caller, replies)} for h, b in fr]
            messages += [{"role": "assistant", "content": out},
                         {"role": "user", "content": json.dumps(rs, ensure_ascii=False)}]
        return "\n".join(outs), err

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
                    self._run(c, best[0], best[1]); steps += 1; progressed = True
            if not progressed:
                if not serve:
                    break
                time.sleep(poll)
        return steps


def parse(out: str) -> list[tuple[str, str]]:
    """把一段输出拆成帧：行首 ">>> " 起帧，"<<<" 一行收帧（末尾没收就到末尾）。帧内的 ">>> " 是正文；正文逐字节保留，唯一不能出现的是单独一行 "<<<"。"""
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
