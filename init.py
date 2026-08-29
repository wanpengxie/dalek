"""init：起运行时；把 G 的第一个 actor 放进它的 channel；不发消息。

    python init.py <P> [--serve]          起这台机器（P 里有 G.json、omega.py、runtime.py、init.py）
    python init.py <P> --kick "<text>"    创造者（人）经 Port 踢一脚：第一条消息从根门进来

init 属于种子，是 userland：它认识 G 的形状（channels / members），不认识任何名字。
它只放一个 actor——多放一个，构造器就跑进世界里了。
"""
from __future__ import annotations
import json, sys
from pathlib import Path

HERE = Path(__file__).resolve().parent
sys.path.insert(0, str(HERE))
from omega import Port          # noqa: E402
from runtime import Runtime     # noqa: E402


def boot(P: Path, oracle=None) -> Runtime:
    rt = Runtime(P, oracle=oracle).load()
    if not rt.channels:
        G = json.loads((P / "G.json").read_text(encoding="utf-8"))
        ch = G["channels"][0]; m = ch["members"][0]
        rt.place(ch["name"], m["kind"], m["text"], m.get("bind", ()), receptionist=True)
    return rt


def kick(P: Path, text: str, frm: str = "creator") -> None:
    G = json.loads((P / "G.json").read_text(encoding="utf-8"))
    Port.send(f"file:{P}#{G['channels'][0]['name']}", {"from": frm, "body": text})


if __name__ == "__main__":
    P = Path(sys.argv[1]).resolve()
    if "--kick" in sys.argv:
        kick(P, sys.argv[sys.argv.index("--kick") + 1])
    else:
        boot(P).run(serve="--serve" in sys.argv)
