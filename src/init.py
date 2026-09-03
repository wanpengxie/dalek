"""loader 的入口。没有 boot：起 R，折叠已有账本（已出生则 up 入账），根门开着，等。

    python init.py <P> [--serve]     起这台机器；--serve 静止后持续轮询收件箱
不读 G，不放任何 actor。第一个动作来自膜外（根门）。
"""
from __future__ import annotations
import sys, fcntl
from pathlib import Path

HERE = Path(__file__).resolve().parent
sys.path.insert(0, str(HERE))
from omega import Port          # noqa: E402
from runtime import Runtime, ROOT   # noqa: E402


def lock(P: Path):
    """一个 P 同时只能有一个 R 进程（单写者 I1）。拿到 = 返回持锁的文件对象（进程活着就一直持有，
    退出/被杀时内核自动释放）；拿不到 = 已有活 R，返回 None。flock 的自动释放正好定义 dormant：
    进程死了锁就空出来，spawn 同一个 P 才拿得到 = 唤醒；活着时再 spawn 拿不到 = 无操作（不双写）。
    只在真守护进程（--serve）抢；进程内 up()（测试、膜外读）不抢，避免误伤。"""
    P.mkdir(parents=True, exist_ok=True)
    f = open(P / "lock", "w")
    try:
        fcntl.flock(f, fcntl.LOCK_EX | fcntl.LOCK_NB)
    except OSError:
        f.close(); return None
    return f


def up(P: Path) -> Runtime:
    """起 R：折叠 H；已出生的机器醒来入账（up）。"""
    return Runtime(P).load().wake()


def root(P: Path, body: str, frm: str = "creator") -> None:
    """膜外经根门发一行（syscall 或 msg）。创造者用。"""
    Port.send(f"file:{P}#{ROOT}", {"from": frm, "body": body})


def say(P: Path, channel: str, body: str, frm: str = "creator") -> None:
    """膜外经 channel 收件箱发一条消息。"""
    Port.send(f"file:{P}#{channel}", {"from": frm, "body": body})


if __name__ == "__main__":
    P = Path(sys.argv[1]).resolve()
    serve = "--serve" in sys.argv
    if serve:
        lk = lock(P)                                    # 抢锁在 load/wake 之前：抢不到就一个字节都不写
        if lk is None:
            sys.stderr.write(f"R already alive on {P}; refusing (single-writer I1)\n")
            sys.exit(3)
    up(P).run(serve=serve)
