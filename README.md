# Dalek

一种可以自我维持、自我改进、自我复制、自我组织的智能单元。副标题：如何优雅地把 LLM 塞进冯诺依曼机器。

理论在 **[DALEK.md](DALEK.md)**（权威）。本目录本身就是 dalek0 的机器包 P：`omega.py` + `runtime.py` + `init.py`（世界）+ `G.json`（描述）。

## 三层

```
Ω        omega.py    宿主契约：Exec / Store / Port。不含任何 Dalek 词。
运行时    runtime.py  极小状态空间 + 四行转移表（program / oracle / door / place）。内容盲。随包携带，不在 G 里。
组织      G.json      channel、成员（kind + text + bind）、门。c0 按它 realize。
```

`init.py` 起运行时、把 G 的第一个 actor 放进它的 channel、不发消息。第一条消息从根门进来（`--kick`）。

## dalek0（M1 第一天）

G 里一个 channel `c0`，注册三样：
- `actors/realize.py` — 装配器（A）。请求：`realize [G]`、`add <channel> <kind> [in] [bind=…]\n<text>`、`peer <a> <b>`。
- `actors/spawn.py` — 起子代（C）。请求：`spawn <name>`：pack（抄世界 + G 原样）→ `Exec.spawn` → 放一扇门 → 经门踢一脚 `realize G.json`。踢完义务结束。
- 根门（对面是创造者）。

`genesis.py` 把这两段源码写成 `G.json`。

## 跑

```
python3 t/test_c0.py                 # T1–T7
python3 genesis.py                   # 重新生成 G.json
python3 init.py <P> [--serve]        # 起一台机器；--serve 静止后持续轮询收件箱
python3 init.py <P> --kick "realize G.json"   # 创造者踢一脚
```

## 状态

- 2026-08-29：Ω、运行时、init、c0（realize + spawn）、genesis；T1–T7 绿（转移表、放 actor、门、init 只放一个、syscall、内容盲、spawn 子代在独立进程自发育）。
- 待：c1（登记、decl）、c2（L + U 的 coding agent）、oracle 端点（LLM / 人）、replay、c3（www）。

## 文件

```
DALEK.md      理论：公理、定义、Ω、运行时、构造器、里程碑、ABI
MODEL.md      面向不了解概念的读者的机制说明（部分已过时，以 DALEK.md 为准）
FAILURES.md   旧内核时代每条规则的出生证明（F1–F24）
omega.py runtime.py init.py genesis.py G.json actors/ t/
```
