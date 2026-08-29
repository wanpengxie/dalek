"""genesis：写出 dalek0 的 G.json。

dalek0 = Space { c0 }。c0 注册三样：realize actor（装配器，bind=place）、spawn actor（起子代，bind=place,spawn）、根门（对面是创造者）。
c1、c2 明天加进 G。这台机器本身就是一个 P：本目录含 omega.py / runtime.py / init.py / G.json。

    python genesis.py [目标目录]      默认写到本目录
"""
from __future__ import annotations
import json, sys
from pathlib import Path

HERE = Path(__file__).resolve().parent


def G0() -> dict:
    src = lambda n: (HERE / "actors" / f"{n}.py").read_text(encoding="utf-8")
    return {
        "channels": [
            {"name": "c0",
             "members": [
                 {"kind": "program", "text": src("realize"), "bind": ["place"]},
                 {"kind": "program", "text": src("spawn"), "bind": ["place", "spawn"]},
                 {"kind": "door", "text": "creator"},
             ],
             "receptionist": 1},
        ],
        "peers": [],
    }


if __name__ == "__main__":
    out = Path(sys.argv[1]).resolve() if len(sys.argv) > 1 else HERE
    (out / "G.json").write_text(json.dumps(G0(), ensure_ascii=False, indent=1), encoding="utf-8")
    print(out / "G.json")
