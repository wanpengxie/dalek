"""R 的入口。boot 不存在：起运行时，根门开着，等。

    python init.py <P> [--serve]     起这台机器；--serve 静止后持续轮询收件箱
不读 G，不放任何 actor。第一个动作来自膜外（根门）。
"""
from __future__ import annotations
import sys
from pathlib import Path

HERE = Path(__file__).resolve().parent
sys.path.insert(0, str(HERE))
from omega import Port          # noqa: E402
from runtime import Runtime, ROOT   # noqa: E402


def boot(P: Path, oracle=None) -> Runtime:
    return Runtime(P, oracle=oracle).load()


def root(P: Path, body: str, frm: str = "creator") -> None:
    """膜外经根门发一行（syscall 或 msg）。创造者用。"""
    Port.send(f"file:{P}#{ROOT}", {"from": frm, "body": body})


def say(P: Path, channel: str, body: str, frm: str = "creator") -> None:
    """膜外经 channel 收件箱发一条消息。"""
    Port.send(f"file:{P}#{channel}", {"from": frm, "body": body})


if __name__ == "__main__":
    boot(Path(sys.argv[1]).resolve()).run(serve="--serve" in sys.argv)
