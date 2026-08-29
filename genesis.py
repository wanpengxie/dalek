"""genesis：人侧的 A + B。第一台机器由手造——用和机器内 realize 同一套 syscall，经根门。

    G0()                 dalek0 的 G：world（R 的源码）+ c0{realize, C}
    pack(G, P)           B：把 G.world 写成文件，G.json 放旁边 → P
    construct(P, G)      A：经 P 的根门逐条发 syscall（最后放出生证明门）
    start(P, G)          C：经根门发第一条消息 → 关门

    python genesis.py            把 dalek0 的 G.json 写到本目录（本目录就是 dalek0 的 P）
"""
from __future__ import annotations
import json, sys
from pathlib import Path

HERE = Path(__file__).resolve().parent
sys.path.insert(0, str(HERE))
from init import root   # noqa: E402

WORLD = ("omega.py", "runtime.py", "init.py")


def G0() -> dict:
    src = lambda p: (HERE / p).read_text(encoding="utf-8")
    return {
        "world": {f: src(f) for f in WORLD},
        "channels": [
            {"name": "c0",
             "members": [
                 {"kind": "program", "text": src("actors/realize.py"), "bind": ["syscall"]},
                 {"kind": "program", "text": src("actors/spawn.py"), "bind": ["syscall", "spawn"]},
             ],
             "receptionist": 1},
        ],
        "peers": [],
    }


def pack(G: dict, P: Path) -> Path:
    P.mkdir(parents=True, exist_ok=True)
    for f, s in G["world"].items():
        (P / f).write_text(s, encoding="utf-8")
    (P / "G.json").write_text(json.dumps(G, ensure_ascii=False, indent=1), encoding="utf-8")
    return P


def lines_of(G: dict, creator: str | None) -> list[str]:
    """与 actors/realize.py 的 lines_of 相同：G → syscall 行。"""
    L = [f"channel.create {c['name']}" for c in G["channels"]]
    for c in G["channels"]:
        for i, m in enumerate(c["members"]):
            flags = []
            if i + 1 == c.get("receptionist", 1): flags.append("in")
            if m.get("bind"): flags.append("bind=" + ",".join(m["bind"]))
            L.append(" ".join(["channel.add.actor", c["name"], m["kind"], *flags]) + "\n" + m["text"])
    for a, b in G.get("peers", []):
        L += [f"channel.add.actor {a} door\n{b}", f"channel.add.actor {b} door\n{a}"]
    if creator and G["channels"]:
        L.append(f"channel.add.actor {G['channels'][0]['name']} door\n{creator}")
    return L


def construct(P: Path, G: dict, creator: str = "human") -> None:
    for line in lines_of(G, creator):
        root(P, line, frm=creator)


def start(P: Path, G: dict, body: str = "start", creator: str = "human") -> None:
    root(P, f"msg {G['channels'][0]['name']}\n{body}", frm=creator)


if __name__ == "__main__":
    out = Path(sys.argv[1]).resolve() if len(sys.argv) > 1 else HERE
    (out / "G.json").write_text(json.dumps(G0(), ensure_ascii=False, indent=1), encoding="utf-8")
    print(out / "G.json")
