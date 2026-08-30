# Dalek

一种可以自我维持、自我改进、自我复制、自我组织的智能单元。副标题：如何优雅地把 LLM 塞进冯诺依曼机器。

理论在 **[DALEK.md](DALEK.md)**（权威）。本目录本身就是 dalek0 的机器包 P：`omega.py` + `runtime.py` + `init.py`（世界）+ `G.json`（描述）。

## 三层

```
Ω        omega.py    宿主契约：Exec / Store / Port。不含任何 Dalek 词。
运行时    runtime.py  极小状态空间 + 转移表（program / oracle / door）+ 三个 syscall + Space 级根门。内容盲。源码在 G.world 里，机器内无人读。
组织      G.json      channel、成员（kind + text + bind）、门。c0 按它 realize。
```

`init.py` 是 R 的入口：起运行时，根门开着，等。不读 G，不放任何 actor。机器由膜外经根门用 syscall 造出来（父代的 A，或人），第一条消息关门——切离。

## dalek0

G 里三个 channel：`c0`（构造）、`c1`（登记，`actors/registrar.py`：只折自己的账本，`decl` 吐出当前形态）、`c2`（作者：`actors/l.txt` 一个 oracle = LLM 端点 + 提示语，`actors/u.py` 一个执行器；没有 driver，循环由转移表驱动），连线 c0–c1、c0–c2（`genesis.G2()`；`G0()` 是没有作者的最小机器）。c0 注册两个 actor：
- `actors/realize.py` — 装配器（A）。请求：`build <门> <创造者>\n<G>`（经门造一台机器的 c0）、`start\n<G>`（出生：自己长出其余——发育）、`add …`、`peer …`（本地生长）。
- `actors/spawn.py` — 起子代（C）。请求：`spawn <name>`：pack（G.world 写成文件 + G.json 原样）→ `Exec.spawn` → 放两扇门 → 把 G 交给 realize 经门造子代的 c0 → `msg c0\nstart\n<G>`。关门即切离，子代的 c0 自己长其余。

出生证明（指回创造者的门）由创造者放，不在 G 里。`genesis.py` 是人侧的 A + B：dalek0 由人经根门用同一套 syscall 造。

## 跑

```
python3 t/test_c0.py                 # T0–T19
python3 genesis.py                   # 重新生成 G.json
python3 init.py <P> [--serve]        # 起 R；--serve 静止后持续轮询收件箱
```

真模型跑 c2（把 `actors/l.txt` 第一行的 `KEY` 换掉，`python3 genesis.py` 重生成 G.json）：
```
python3 -c "from genesis import *; from pathlib import Path; P=Path('/tmp/d0'); G=G2(); pack(G,P); construct(P,G); start(P,G)"
python3 init.py /tmp/d0 --serve &
python3 -c "from init import say; say('/tmp/d0','c0','add c2 door\nfile:/tmp/me#me','x')"      # 给自己一扇门，L 的 done 才回得来
python3 -c "from init import say; say('/tmp/d0','c2','task\n写一个 actor：收到 hi 回 hello，装进 c3','file:/tmp/me#me')"
tail -f /tmp/d0/h/c2.jsonl; cat /tmp/me/in/me.jsonl
```

## 状态

- 2026-08-29：Ω、R（含根门）、c0（realize + spawn）、genesis；T1–T7 绿（转移表、syscall、门、根门开关、请求、内容盲、父代的 A 经门造子代 + start 切离）。
- 2026-08-30：发育版（父代只造 c0，子代的 c0 长其余）；三层命名 Ω / world{ω-bind, loader, R} / dalek；M2：c1 登记员（`actors/registrar.py`，只折自己的账本）、`decl`、第三个 syscall `channel.retire.actor`、视图带成员表、C 的 pack 用 decl；T0–T11 绿；M2.1：接待员显式、R 拒绝自退役/退役接待员、门是成员、c1 只认 c0 的事实，π(A) ≅ G；T0–T12 绿。
- 2026-08-30 晚：M3.0 c2 骨架——`Port.request`、oracle = 解释器在远处的成员（与门的区分见 DALEK 1.7）、T18 task → L ↔ U → add c3 → decl → done；T19 端点不通机器活着。T0–T19 绿。
- 待：任务 0（c2 → c2′，show G/H）、真模型跑一遍、M4 的 up/down 与对账、c3（www）。

## 文件

```
DALEK.md      理论：公理、定义、Ω、运行时、构造器、里程碑、ABI
DESIGN.md     M1 设计：syscall 闭集、R 的完整定义、洞、创世流程
MODEL.md      面向不了解概念的读者的机制说明（部分已过时，以 DALEK.md 为准）
FAILURES.md   旧内核时代每条规则的出生证明（F1–F24）
omega.py runtime.py init.py genesis.py G.json actors/ t/
```
