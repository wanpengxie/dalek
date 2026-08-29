# Dalek

一种可以自我维持、自我改进、自我复制、自我组织的智能单元。副标题：如何优雅地把 LLM 塞进冯诺依曼机器。

理论在 **[DALEK.md](DALEK.md)**（权威）。本目录本身就是 dalek0 的机器包 P：`omega.py` + `runtime.py` + `init.py`（世界）+ `G.json`（描述）。

## 三层

```
Ω        omega.py    宿主契约：Exec / Store / Port。不含任何 Dalek 词。
运行时    runtime.py  极小状态空间 + 四行转移表（program / oracle / door / place）。内容盲。随包携带，不在 G 里。
组织      G.json      channel、成员（kind + text + bind）、门。c0 按它 realize。
```

`init.py` 是 R 的入口：起运行时，根门开着，等。不读 G，不放任何 actor。机器由膜外经根门用 syscall 造出来（父代的 A，或人），第一条消息关门——切离。

## dalek0（M1 第一天）

G 里一个 channel `c0`，注册三样：
- `actors/realize.py` — 装配器（A）。请求：`build <门> <创造者>\n<G>`（经门造一台机器）、`add …`、`peer …`（本地生长）。
- `actors/spawn.py` — 起子代（C）。请求：`spawn <name>`：pack（G.world 写成文件 + G.json 原样）→ `Exec.spawn` → 放两扇门 → 把 G 交给 realize 经门造 → `msg c0 start`。关门即切离。

出生证明（指回创造者的门）由创造者放，不在 G 里。`genesis.py` 是人侧的 A + B：dalek0 由人经根门用同一套 syscall 造。

## 跑

```
python3 t/test_c0.py                 # T1–T7
python3 genesis.py                   # 重新生成 G.json
python3 init.py <P> [--serve]        # 起 R；--serve 静止后持续轮询收件箱
```

## 状态

- 2026-08-29：Ω、R（含根门）、c0（realize + spawn）、genesis；T1–T7 绿（转移表、syscall、门、根门开关、请求、内容盲、父代的 A 经门造子代 + start 切离）。
- 待：c1（登记、decl）、c2（L + U 的 coding agent）、oracle 端点（LLM / 人）、replay、c3（www）。

## 文件

```
DALEK.md      理论：公理、定义、Ω、运行时、构造器、里程碑、ABI
DESIGN.md     M1 设计：syscall 闭集、R 的完整定义、洞、创世流程
MODEL.md      面向不了解概念的读者的机制说明（部分已过时，以 DALEK.md 为准）
FAILURES.md   旧内核时代每条规则的出生证明（F1–F24）
omega.py runtime.py init.py genesis.py G.json actors/ t/
```
