"""Ω：宿主契约。Exec / Store / Port。

这个文件里不允许出现任何 Dalek 词汇（channel、c0、构造、登记、复制、重演）。
它只认识：源码、进程、字节、路径、端点。第一个实现：Linux + python3 + 文件系统。
"""
from __future__ import annotations
import json, os, subprocess, sys, signal, tempfile, urllib.request, urllib.error
from pathlib import Path


class Exec:
    """通用程序实例化：起一段固定语言（python3）源码的进程并给出它的管道；起一个独立实例；停它。"""

    @staticmethod
    def open(source: str, cwd: str | os.PathLike) -> subprocess.Popen:
        """起一个进程跑源码：stdin / stdout 是无缓冲字节管道（宿主只搬字节，怎么分帧是上面的事），stderr 落到临时文件。"""
        err = tempfile.TemporaryFile()
        p = subprocess.Popen([sys.executable, "-c", source], stdin=subprocess.PIPE, stdout=subprocess.PIPE,
                             stderr=err, cwd=str(cwd), bufsize=0)
        p.errfile = err
        return p

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
    """通用双向字节通信。异步的一半 send / recv：文件收件箱，端点形如 file:<dir>#<box>。
    同步的一半 request：POST 到 http 端点、取回应答。第一个实现讲 Anthropic messages 报文。"""

    @staticmethod
    def request(text: str, messages: list[dict], timeout: float = 120) -> tuple[str, str]:
        """text 第一行 = <url> <model> <key>，其余 = system；messages = 多轮对话 [{role, content}]。
        返回 (应答正文, err)。失败 → 应答视为空，err 记原因；对面的失败不是宿主的失败。"""
        head, _, system = text.partition("\n")
        parts = head.split()
        if not parts or not parts[0].startswith("http"):
            return "", "no endpoint"
        url, model, key = (parts + ["", ""])[:3]
        body = {"model": model, "max_tokens": 8192, "system": system, "messages": messages}
        req = urllib.request.Request(url, data=json.dumps(body).encode("utf-8"), method="POST",
                                     headers={"content-type": "application/json", "x-api-key": key,
                                              "anthropic-version": "2023-06-01"})
        try:
            with urllib.request.urlopen(req, timeout=timeout) as r:
                data = json.loads(r.read().decode("utf-8"))
        except urllib.error.HTTPError as e:
            return "", f"http {e.code}\n{e.read().decode('utf-8', 'replace')[:500]}"
        except Exception as e:                                   # 连不上、超时、坏 JSON
            return "", f"{type(e).__name__}: {e}"
        text = "".join(c.get("text", "") for c in data.get("content", []) if c.get("type") == "text")
        if data.get("stop_reason") == "max_tokens":              # 截断的回答不算回答
            return "", f"truncated at max_tokens\n{text[-500:]}"
        return text, ""

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
