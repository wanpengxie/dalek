# Dalek

**A constructive agent machine**

[中文](README.zh-CN.md) · [Paper (PDF)](Dalek.pdf)

Dalek is a small, runnable research prototype that treats an AI agent as a machine with its own boundary, blueprint, history, and construction process—not merely as a language model surrounded by tools.

Most agent systems can generate code or load a new tool. The surrounding harness still decides whether that code is part of the agent, whether it survives a restart, and whether a descendant inherits it. Dalek brings those decisions into one explicit machine structure. A capability can be written as text, checked, installed as a member, recorded in history, restored after restart, and passed to an offspring through the same construction path.

Dalek is **not** a new implementation of von Neumann's 1948 automaton. It is a new agent machine that uses von Neumann's self-reproducing construction as one hereditary core, then adds the boundary, identity, history, and growth required by a long-lived agent.

## What “constructive” means

The word does not mean “helpful.” It means that the machine is defined by a finite set of parts and by explicit rules for constructing and changing them. Dalek makes four things explicit:

1. the boundary between the machine and its host;
2. the language from which legal machine forms are built;
3. the transitions that may change the machine's constitution;
4. how the rules of construction enter heredity.

This gives each lifecycle claim a definite subject. The same machine can be maintained, changed, reproduced, and connected to other machines, while its identity and history remain inspectable.

## The machine in one picture

```text
Host contract Ω: execute · store · communicate
└── Space: one Dalek individual
    ├── R     content-blind runtime and constitutive laws
    ├── G     static description: what the machine should reproduce
    ├── H     append-only ledgers: what this individual has undergone
    ├── c0    constructor, copier, and controller
    ├── c1    registry that derives the next G
    └── c2    capability producer D = L + U
              L writes candidate capabilities
              U compiles and checks them
```

The medium has three primitives:

- **actor** — a callable behavioral part;
- **message** — the uniform form of interaction;
- **channel** — a local organizational boundary containing actors and a ledger.

A **Space** is the detachable, startable machine: its channels plus one runtime. Programs, language models, tools, humans, and external systems can all meet the machine through the same actor/message interface.

The identity of an individual is `(G, H)`: two machines may inherit the same description `G`, but each begins and retains its own history `H`.

## What the prototype demonstrates

- **Self-maintenance** — a damaged or missing organ can be replaced through the machine's own construction path.
- **Self-evolution** — `L` authors a candidate, `U` checks it, and accepted capability text becomes an installed and heritable member.
- **Self-reproduction** — a child is constructed from `G`, starts a new `H`, and can reproduce again.
- **Self-organization** — independent machines use inherited organs to discover peers, form a topology, and wake a failed peer.

These are mechanism claims, not claims of open-ended autonomous strategy. The experiments show that the paths exist and that their constitutive steps are recorded in ledgers.

## Quick start

Dalek's reference implementation uses Linux, Python 3, and the Python standard library.

Run the deterministic mechanism suite:

```bash
cd src
python3 t/test_c0.py
```

The current suite contains 33 tests covering construction, doors, registration, heredity, restart, repair, reproduction, self-organization, and a lineage-level runtime change.

Boot a fresh machine in a temporary directory without calling a remote model:

```bash
python3 - <<'PY'
from pathlib import Path
from tempfile import mkdtemp
from genesis import G2, pack, construct, start
from init import up

P = Path(mkdtemp(prefix="dalek-"))
G = G2()
pack(G, P)
construct(P, G)
start(P, G)
machine = up(P)
machine.run()
print(f"Dalek created at {P}")
print("channels:", sorted(machine.channels))
PY
```

For a persistent runtime:

```bash
python3 init.py /path/to/a/machine --serve
```

The LLM-backed experiments are described in Section 4 of the [paper](Dalek.pdf), with their original ledgers in [`src/runs/`](src/runs/). Reproducing those runs requires configuring the endpoint, model, and credential used by [`src/actors/l.py`](src/actors/l.py), regenerating `G.json`, and constructing a fresh machine. Do not commit credentials into the repository or a genome.

## Repository map

| Path | Purpose |
|---|---|
| [`src/runtime.py`](src/runtime.py) | Runtime `R`: folding, scheduling, calls, doors, and constitutive transitions |
| [`src/omega.py`](src/omega.py) | Reference binding of host contract `Ω`: execution, storage, and ports |
| [`src/genesis.py`](src/genesis.py) | Human-side constructor and copier for the first machine |
| [`src/G.json`](src/G.json) | Canonical description of the prototype machine |
| [`src/actors/`](src/actors/) | Source text of the machine's organs |
| [`src/t/test_c0.py`](src/t/test_c0.py) | Deterministic mechanism tests |
| [`src/runs/`](src/runs/) | Original ledgers and drivers from the LLM-backed experiments |
| [`Dalek.pdf`](Dalek.pdf) | English paper |

## Scope and safety

This repository is a **research prototype**, not a production runtime or a security sandbox.

“Closed” in the paper means **organizationally closed relative to an explicit host contract**: membership, messages, constitutive changes, and heredity have defined paths. It does not mean that Python code is physically isolated. The reference runtime executes actor text with Python `exec`; an actor can access files, the network, and other host resources available to the process. The file-backed port is also a demonstrator, not an authenticated transport.

Do not run untrusted actor code or expose this prototype as a public service. A production realization needs real process isolation, authenticated ports, resource controls, secret management, and a hardened binding of `Ω`.

## Paper

The complete argument, model, machine definition, ledger walkthroughs, discussion, and related work are in:

> Wanpeng Xie. **Dalek: A Constructive Agent Machine — Self-Maintenance, Self-Evolution, Self-Reproduction, and Self-Organization by Construction.**

[Download the English paper](Dalek.pdf).
