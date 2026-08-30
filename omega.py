"""Ω：宿主契约。Exec / Store / Port。

这个文件里不允许出现任何 Dalek 词汇（channel、c0、构造、登记、复制、重演）。
它只认识：源码、进程、字节、路径、端点。第一个实现：Linux + python3 + 文件系统。
"""
from __future__ import annotations
import json, os, subprocess, sys, signal, traceback
from pathlib import Path


class Exec:
    """通用程序实例化：把一段固定语言（python3）的源码实例化成可调用对象；起一个独立实例；停它。"""

    @staticmethod
    def load(source: str, env: dict) -> "callable":
        """在本进程里 exec 源码，env 里的名字对它可见；源码必须定义 run(m)。怎么实例化是绑定的事，上面看不见。"""
        ns = dict(env)
        exec(compile(source, "<actor>", "exec"), ns)
        if not callable(ns.get("run")):
            raise TypeError("source defines no run(m)")
        return ns["run"]

    @staticmethod
    def spawn(argv: list[str], cwd: str | os.PathLike, log: str | os.PathLike | None = None) -> int:
        out = open(log, "ab") if log else subprocess.DEVNULL
        p = subprocess.Popen([sys.executable, *argv], cwd=str(cwd), stdin=subprocess.DEVNULL,
                             stdout=out, stderr=subprocess.STDOUT, start_new_session=True)
        if log:
            out.close()                                  # 子进程已继承句柄，父进程不留
        return p.pid

    @staticmethod
    def stop(pid: int) -> None:
        try:
            os.killpg(pid, signal.SIGTERM)
        except ProcessLookupError:
            pass


class Store:
    """持久字节介质：读、写、原子追加（一行一条，flush + fsync）。"""

    @staticmethod
    def read(path: str | os.PathLike) -> str:
        p = Path(path)
        return p.read_text(encoding="utf-8") if p.exists() else ""

    @staticmethod
    def write(path: str | os.PathLike, text: str) -> None:
        p = Path(path); p.parent.mkdir(parents=True, exist_ok=True)
        p.write_text(text, encoding="utf-8")

    @staticmethod
    def append(path: str | os.PathLike, line: str) -> None:
        p = Path(path); p.parent.mkdir(parents=True, exist_ok=True)
        with open(p, "a", encoding="utf-8") as f:
            f.write(line.rstrip("\n") + "\n"); f.flush(); os.fsync(f.fileno())

    @staticmethod
    def lines(path: str | os.PathLike, offset: int = 0) -> tuple[list[str], int]:
        """从字节偏移 offset 起读完整的行；返回 (行, 新偏移)。"""
        p = Path(path)
        if not p.exists():
            return [], offset
        with open(p, "rb") as f:
            f.seek(offset); data = f.read()
        if not data:
            return [], offset
        cut = data.rfind(b"\n")
        if cut < 0:
            return [], offset
        return data[:cut].decode("utf-8").split("\n"), offset + cut + 1


class Port:
    """通用双向字节通信：send / recv，文件收件箱，端点形如 file:<dir>#<box>。只给门用；L 问模型是 L 的 text 自己的事（M3.4 起 Ω 没有 request）。"""

    @staticmethod
    def send(endpoint: str, payload: dict) -> bool:
        if not endpoint.startswith("file:"):
            return False
        d, _, box = endpoint[5:].partition("#")
        Store.append(Path(d) / "in" / f"{box}.jsonl", json.dumps(payload, ensure_ascii=False))
        return True

    @staticmethod
    def recv(endpoint: str, offset: int = 0) -> list[tuple[dict, int]]:
        """从字节偏移起收完整的行；返回 [(载荷, 该行之后的偏移)]。"""
        if not endpoint.startswith("file:"):
            return []
        d, _, box = endpoint[5:].partition("#")
        lines, _ = Store.lines(Path(d) / "in" / f"{box}.jsonl", offset)
        out, at = [], offset
        for l in lines:
            at += len(l.encode("utf-8")) + 1
            if l.strip():
                out.append((json.loads(l), at))
        return out
