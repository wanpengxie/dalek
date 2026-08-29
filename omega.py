"""Ω：宿主契约。Exec / Store / Port。

这个文件里不允许出现任何 Dalek 词汇（channel、c0、构造、登记、复制、重演）。
它只认识：源码、进程、字节、路径、端点。第一个实现：Linux + python3 + 文件系统。
"""
from __future__ import annotations
import json, os, subprocess, sys, signal
from pathlib import Path


class Exec:
    """通用程序实例化：跑一段固定语言（python3）的源码；起一个独立实例；停它。"""

    @staticmethod
    def run(source: str, stdin: str, cwd: str | os.PathLike, timeout: float = 60) -> tuple[str, str]:
        r = subprocess.run([sys.executable, "-c", source], input=stdin, capture_output=True,
                           text=True, cwd=str(cwd), timeout=timeout)
        return r.stdout, r.stderr

    @staticmethod
    def spawn(argv: list[str], cwd: str | os.PathLike, log: str | os.PathLike | None = None) -> int:
        out = open(log, "ab") if log else subprocess.DEVNULL
        p = subprocess.Popen([sys.executable, *argv], cwd=str(cwd), stdin=subprocess.DEVNULL,
                             stdout=out, stderr=subprocess.STDOUT, start_new_session=True)
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
    """通用双向字节通信。M1 实现：文件收件箱。端点形如 file:<dir>#<box>。"""

    @staticmethod
    def send(endpoint: str, payload: dict) -> bool:
        if not endpoint.startswith("file:"):
            return False
        d, _, box = endpoint[5:].partition("#")
        Store.append(Path(d) / "in" / f"{box}.jsonl", json.dumps(payload, ensure_ascii=False))
        return True
