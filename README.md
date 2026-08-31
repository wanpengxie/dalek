# Dalek

一种可以自我维持、自我改进、自我复制、自我组织的智能单元。副标题：如何优雅地把 LLM 塞进冯诺依曼机器。

理论在 **[DALEK.md](DALEK.md)**（权威）。本目录本身就是 dalek0 的机器包 P：`omega.py` + `runtime.py` + `init.py`（世界）+ `G.json`（描述）。

## 三层

```
Ω        omega.py    宿主契约：Exec / Store / Port。不含任何 Dalek 词。
运行时    runtime.py  极小状态空间 + 转移表（program / door）+ 三个 syscall + Space 级根门。内容盲。源码在 G.world 里，机器内无人读。
组织      G.json      channel、成员（kind + text + bind）、门。c0 按它 realize。
```

`init.py` 是 R 的入口：起运行时，根门开着，等。不读 G，不放任何 actor。机器由膜外经根门用 syscall 造出来（父代的 A，或人），第一条消息关门——切离。

## dalek0

G 里三个 channel：`c0`（构造）、`c1`（登记，`actors/registrar.py`：只折自己的账本，`decl` 吐出当前形态）、`c2`（作者：`actors/l.py` 一个 program（理论里的 oracle）= 自带 agent loop 的源码（端点 + 提示语 + 组装 + POST + 解帧），`actors/u.py` 一个执行器；没有 driver），连线 c0–c1、c0–c2（`genesis.G2()`；`G0()` 是没有作者的最小机器）。c0 注册两个 actor：
- `actors/realize.py` — 装配器（A）。请求：`build <门> <创造者>\n<G>`（经门造一台机器的 c0）、`start\n<G>`（出生：自己长出其余——发育）、`add …`、`peer …`（本地生长）。
- `actors/spawn.py` — 起子代（C）。请求：`spawn <name>`：pack（G.world 写成文件 + G.json 原样）→ `Exec.spawn` → 放两扇门 → 把 G 交给 realize 经门造子代的 c0 → `msg c0\nstart\n<G>`。关门即切离，子代的 c0 自己长其余。

出生证明（指回创造者的门）由创造者放，不在 G 里。`genesis.py` 是人侧的 A + B：dalek0 由人经根门用同一套 syscall 造。

## 跑

```
python3 t/test_c0.py                 # T0–T30（当前 30 项）
python3 genesis.py                   # 重新生成 G.json
python3 init.py <P> [--serve]        # 起 R；--serve 静止后持续轮询收件箱
```

真模型跑 c2（把 `actors/l.py` 第一行的 `ENDPOINT, MODEL, KEY` 换掉——端点路径含 `/messages` 讲 Anthropic 报文，否则 OpenAI 兼容（DeepSeek：`https://api.deepseek.com/chat/completions`, `deepseek-chat`）；`python3 genesis.py` 重生成 G.json）：
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
- 2026-08-30 晚：M3.1 账本是介质的读地址 0（`>>> 0\nshow [a] [b]`，事实入账、投递附 rows），视图 = 投递的消息 + 地址簿；`bind=ledger` 退役；oracle 的转移行带组装。T0–T20 绿。
- 2026-08-30 深夜：M3.2 运行模型——一步 = 一次运行（初始消息 → 请求/回复 → 结束），程序走帧协议、oracle 走多轮 agent loop、`re` 回复、角色 `tag` 寻址、`iface` 接口、H6 关闭；c0/c1 按 call 重写；T18 因果闭环（placed 后 done、真门）。T0–T21 绿。
- 2026-08-31 凌晨：M3.3 actor 是常驻函数——放入时 exec 一次（Python 的 exec，选 Python 的理由），`call` 是真函数，返回值即回复；帧只给 LLM；0 加 `who`；U 在进程内把候选当活函数测。进程、管道、帧协议、助手代码全部删除。T0–T23 绿。
- 2026-08-31 上午：M3.4 agent loop 出 R——kind 是类别（理论），实例化是工程：oracle 和程序同一个 exec，L 的 text = `actors/l.py` = 整个 loop（端点、提示语、组装、POST、解帧、轮数）。R 删 `_oracle`，Ω 删 `Port.request`。T0–T23 绿。
- 2026-09-01 凌晨：M3 任务 0 的路——T24：c2 + file → c2′（L 经门 add 进本 channel，用它写读，done 给发起者）→ spawn 子代继承 file 零件、不继承 notes.txt。T24 是桩版；**真模型（DeepSeek）同日跑通**：作者自己写 file、U 测三版、add、placed 后用它、done；子代继承零件不继承文件（`runs/task0-deepseek-*.jsonl`）。结论：提示语是策略，核心路径是新能力完全靠 syscall + 现有 actor 进入机器（门 → c0 → syscall → 实例化 → 登记 → 回执）。任务 0 关闭。24/24。
- 2026-09-01 凌晨：M3.5 oracle 不是原语——R 的 kind 集 = {program, door}，L = `kind=program, tag=L`；oracle 留作理论类别词。膜定为窄版（DESIGN I6）。24/24。
- 2026-09-01 晨：M3 任务 1 + M4——channel 存在 ⇔ 有 place 行；生命周期物理事件最终收紧为只在 G 的首 channel 记 `_root up/down`，A 把 up 翻译为给 c1 的 `reconcile`，内脏账本不受 R 广播；A 的出生 start 协作广播保留。T25/T28 验证进程内与真 SIGTERM 重启，T26 验证 `up→A→reconcile→rebuild`，T31 验证无接待员 channel 的成员仍重新实例化且 Σ 归零，T27 验证三机自组织与 peer 唤醒。
- 2026-09-01 晨：**真模型任务 1**（deepseek-v4-pro，thinking 默认开）——c2 自己写出 hub/reporter 并装好连线，spawn d1/d2，三台互长门、ping/pong；reporter 有一处 ABI bug（`call("spawn", d)` 两参数，动词参数须在头行），**用机器自己的路修**：给 d1 的 c2 发修复任务 → 写新 reporter → `add c4 … in tag=reporter` 接替接待员 → `retire c4/1` → SIGKILL dalek0 → d1 连续 tick 后 `spawn` 唤醒它（同一本账多一条 up）→ hub pong 恢复。账本在 `runs/task1-*.jsonl`。
- 2026-09-01 午：**T29 换 world 的自举不动点**——换打包器官 C′（pack 前给 runtime.py 打幂等标记），子代跑 R′、decl 遗传 world′、R′ 上能生孙代；父代不受影响。R/Ω/syscall 零改动。29/29。
- 2026-09-01 晚：**逻辑地址收口**——actor 的组织层地址统一为 `channel/tag`；R 用从 H 折出的活跃路由表原子保证 channel 内 tag 唯一，重名分配 `tag1/tag2/…`，数字 addr 只留在 H；c0/c1 只透传最终 tag。T30 覆盖重名、retire 空洞、损伤 rebuild、再次按 tag retire。30/30。
- 2026-09-01 晚：**生命周期边界收口**——删除逐 channel `world up/down`；R 仅在首 channel 留一条 `_root up/down`，由 A 经门触发 c1 对账。新增 T31 的无接待员反例。31/31。
- 待：R 事件中崩溃的中间态（H10 残）、自停（stop 请求 / C 停自己——pid 怎么可见是设计点）、H16、长账本的折叠器官。

## 文件

```
DALEK.md      理论：公理、定义、Ω、运行时、构造器、里程碑、ABI
EXPERIMENTS.md  真模型实验记录（任务 0 / 任务 1，DeepSeek；账本在 runs/）
DESIGN.md     M1 设计：syscall 闭集、R 的完整定义、洞、创世流程
MODEL.md      面向不了解概念的读者的机制说明（部分已过时，以 DALEK.md 为准）
FAILURES.md   旧内核时代每条规则的出生证明（F1–F24）
omega.py runtime.py init.py genesis.py G.json actors/ t/
```
