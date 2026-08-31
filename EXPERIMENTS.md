# 实验记录（真模型）

> ABI 版本说明：E1/E2 的原始账本生成于 T30 之前，当时 `placed/retired` 的外部回执使用数字地址（如 `c2/5`）。当前实现改用 channel 内唯一 tag（如 `c2/file`）；原始记录不改写。精确复现旧轨迹请使用各实验列出的提交，当前协议的机制回归见 T30。

面向论文的原始记录。机制层的对照组是测试套件（T24/T27，脚本桩 L，确定性）；本文只记**真模型**实验：作者是外部 LLM，人不介入运行中。所有账本原件在 `runs/`（凭据已抹），全部时间为本机时间，2026-09-01 凌晨。

体例：每个实验给 目的 / 装置 / 输入 / 过程（账本引用 seq）/ 结果 / 归因与偏差 / 效度威胁。"c2 做到某事"一律指账本上可指认的行，不指模型的自述。

---

## E1 · 任务 0：自改进（c2 → c2′ → 遗传）

**命题**：c2（L+U+门）缺一个零件时，能只靠 syscall 与现有成员造出 c2′ = c2 + 新零件，用它完成任务，且零件随 G 遗传、零件的产物（世界中的文件）不遗传。

**装置**
- 机器：`genesis.G2()`，P=/tmp/d0-real，进程内驱动（同步轮转）。c2 = {L, U, 门→c0, 门→me}。
- L：`kind=program, tag=L`，text=actors/l.py；端点 `https://api.deepseek.com/chat/completions`，模型 **deepseek-chat**，`max_tokens 8192`，temperature 未发（API 默认），HTTP 超时 120 s，`TURNS=16`。对话 = system 提示语 + 首轮 user `{msg, ledger, members}` + 每轮 assistant 原文 / user `[{to, reply}]`。
- 输入唯一：经门 me 发 `task\n把 hello 写进 notes.txt 再读回来`。

**E1-a（失败样本，提示语 T18 形状；未归档）**
过程：L 直接 `U test` 一段把任务写死在函数体里的代码（`open('/tmp/notes.txt','w')…`，U 真跑通过，文件在 /tmp 生成）→ `add c2 program tag=hello_task iface=task\n<要求> -> done\n<说明>` → `placed` 一到即报 `done`。
偏差三处：一次性零件而非工具；新零件从未被调用（"装而不用"）；绝对路径。
归因：当时提示语的任务协议是"写 → U 测 → add → placed 后 done"（T18 流程的措辞），模型逐字执行。**作者的行为是 G 里的 text**。

**E1-b（成功样本；`runs/task0-deepseek-c2.jsonl`）**
提示语改为："缺能力就添**通用零件**（例：file），placed 后**用它**做任务，路径相对 cwd"。重跑：

| seq | 事件 |
|---|---|
| 5 | task 进账（门 me → L，第 1 次调用） |
| 6–7 | L 读 `show`、`who`（组装，入账的事实行） |
| 8→10 | `U test` v1 → `result 1`（头行解析错：`op=="write"` 对 `write notes.txt` 不成立） |
| 11→13 | v2 → `result 1`（漏 `===` 分隔，测试没被当测试执行） |
| 14→16 | v3（`body.startswith("write ")`）→ `result 0` |
| 17 | 经门 c0：`add c2 program tag=file iface=write <path>\n<text> \| read <path> -> written / file content\n<14 行源码>` |
| 19 | 第 1 次调用收笔，step 帧 `[0,0,U,U,U,c0]`，无 re |
| 20–21 | R 放入并实例化 c2/5；`placed c2/5` 经门回来 = 第 2 次调用 |
| 24–29 | `write notes.txt\nhello`→`written`；`read notes.txt`→`hello`（调用的是放进机器的零件，非 U） |
| 30 | `done\n…读回 hello` → 门 me；发起者收件箱收到，署名 `file:<P>#c2` |

产物：`P/notes.txt == "hello"`。全程约 70–90 s，两次调用共约 6 轮 HTTP。
遗传（`runs/task0-deepseek-child-c2.jsonl`）：`spawn kid` 后子代 c2/5 = file，text 逐字节相同，`decl(child)==decl(parent)`；子代目录**无** notes.txt；对子代 file 发 `read notes.txt` → err `FileNotFoundError`；`write notes.txt\nmine` → 子代自己的文件出现、父代的不动（此两针最初只在桩版 T24 的子代上验过，后补在真模型子代上重验，行在归档账本尾部）。

**结论**：验收句成立——c2 没有 file → c2 造（写、测、两轮 debug）→ add 进本 channel → 用它做到 → done → 子代继承零件不继承文件。新能力进入机器的路只有一条：门 → c0 → syscall → R 放入实例化 → 登记 → 回执成为下一次调用。
**效度威胁**：提示语例子点名了 file（造什么被引导；怎么造、怎么进来无引导）；n=1；E1-a 与 E1-b 差一段提示语，构成"text 即策略"的对照但非受控重复。

---

## E2 · 任务 1：自组织 + 自维护（种群、损伤、修复）

**命题**：(i) c2 能照行为规格写出 hub/reporter 两个器官并装机连线；(ii) spawn 出的子代凭遗传的器官自组织成网（互长门、ping/pong）；(iii) 器官带 bug 时，**修复走机器自己的路**（变异 + 同角色接替），修好的器官能把被硬杀的机器照它自己的账本唤醒。

**装置**
- 机器：G2，P0=/tmp/t1-real，**全部为进程**（init.py --serve）；子代 d1、d2 由 C spawn。
- L：**deepseek-v4-pro**（thinking 默认开启，未发 reasoning_effort），`max_tokens 32768`，超时 300 s/轮；提示语含"只有帧有效果"，loop 对"有话没帧"纠偏一轮。
- 任务文本 = 两个器官的**行为规格**（协议四个词：hello / peers / ping-pong / tick-spawn；iface、bind=spawn、装法与连线），实现留给模型。
- 膜外时钟：实验者向 c4 收件箱发 `tick`。

**E2-a（deepseek-chat + 旧提示语，失败）**：首轮回 48 字纯文本（"好的，我需要先获取本机目录……"），无帧，调用按"无帧 = 结束"收场。修复 = 提示语加规则 + loop 纠偏一轮（策略层，R/Ω 不动）。

**E2-b（v4-pro，主实验；`runs/task1-d0-*.jsonl`、`task1-d1-*.jsonl`、`task1-d2-c4.jsonl`）**
- 03:33 task 进账。L 先 `U run: os.path.abspath('.')` 取本机目录（规格允许），随后在 U 里体外迭代器官代码，无一 err。
- 03:41:05 装机完成：c3={hub}，c4={reporter(bind=spawn), 门→c0, 门→hub(text=file:P0#c3)}，peer 连线；`done` 达发起者。**耗时约 8 分钟。**
- 03:41:18 tick 后 hub 回路成立（报到 → 放回指门 → 广播 peers）；spawn d1、d2。
- 03:41:24 **三台机器的 c4 各自长出指向另外两台的门**；ping/pong 大部分互通。
- 驱动断言超时（d2 缺一条来自 d1 的 pong）。**归因是实验驱动而非被试**：d2 动作快，ping 在 d1 放好回指门前到达，署名 `door`（d1/c4 seq 18），d1 回的 pong 写给 `door` 被介质丢弃；协议本身由下一次 tick 心跳自愈，驱动在心跳前就断言了。修驱动（断言前一轮 tick），同一竞态也存在于 T27，同法修（d8f4f3d）。
- 续跑（唤醒三台 → 一轮 tick）后 ping/pong 全部成立（03:48:53）。
- **抓到被试一个真 bug**：SIGKILL dalek0 后 d1 连续 tick 不 spawn。读账 + 读它写的源码定位：失联检测 `_need_spawn` 正确判真，但它写了 `call("spawn", d)`——两参数，动词进头、目录进正文；ABI 里动词调用参数必须在头行同一字符串。头行只剩 `spawn`，解析不到成员，被静默丢弃。

**E2-c（修复实验；`runs/task1-d1-c2.jsonl`、`task1-d1-c4.jsonl`）**

> **历史语义注记**：E2 在 M4 最初的“R 向每个有接待员的 channel 注入 `world up/down`”实现上运行，所以下述 c3/c4 up 是原始账本的真实事实，不应改写。review 后当前语义已收紧为“物理只在根 channel 记 `_root up/down`，A 经门向 c1 发 `reconcile`”，由 E3/T25–T31 见证。E2 的自组织、ABI bug、机内修复和 peer 唤醒结论不依赖物理事件在哪本内脏账本留痕。

修复任务发给 **d1 自己的 c2**（附旧源码与 bug 定位；诊断在门外，修复在门内）：
- 03:52:50 三台照 H 唤醒（各 c4 多一条 `up`）。
- ~03:54:02（约 70 s）：d1 的 c2 写出新 reporter（源码中可指认 `call("spawn " + d)`）→ `add c4 program in bind=spawn tag=reporter iface=…` → **c4/6 接任接待员** → 经门 `retire c4/1`。旧件退役、新件同角色接替——任务 0 的机制作用于任务 1 的活器官。
- 03:54:05 SIGKILL dalek0（此刻其 c3 账上 up=2）。
- 03:54:11 d1 tick：检测 hub 无 pong → `spawn /tmp/t1-real`，回执 `{"seq":138, "from":"spawn", "body":"/tmp/t1-real pid=2705088"}` 在 d1/c4 账上 → **dalek0 照自己的账本醒来（c3 第 3 条 up）** → hub 的 pong 恢复。PASS。

**结论**：(i)(ii)(iii) 全部有账本见证。附带三条野外教训（均已落进 text/测试）：模型首轮"说计划不写帧"；早到的 ping 在回门放好前丢失（心跳自愈是协议的一部分）；ABI 拼写是真实变异面——错一处，整台机器的远端维护失灵，而修复不必碰 R。
**效度威胁**：任务 1 是照规格实现（考实现与接线，不考发明）；bug 的**诊断**由实验者完成、经门递入（选择/评价在膜外，符合模型设定，但应写明）；n=1；E2-b 的驱动竞态最初与被试失败混同，靠账本区分（记录在案，作为"账本可归因"的正面证据）。

---

## E3 · M4：生命周期（机制层实验，无 LLM）

M4 的验收不需要作者参与，故为机制层实验（确定性，可无限重复），与 E1/E2 的真模型实验区分记录。桩 = 测试内的 `def run(m)` 小 actor；机器与进程都是真的。

| 验收 | 实验 | 见证 |
|---|---|---|
| 重启 = 同一个体（进程内） | T25 | down()/up(P) 后只有根 channel 的 `_root` 行序列 `up`→`down`→`up`；内脏 x 零物理行；游标由折叠恢复；pending 消息重跑；decl 不变 |
| 重启 = 同一个体（**进程级**） | T28 | 真 SIGTERM → 根 channel `down` + A 的 step + 进程退出；休眠来信醒来即处理；根边界序列恰为 `down, up, down`，内脏 x 零物理行；place、decl、游标不变 |
| 本地维护，零时钟 | T26 | `rm h/c8.jsonl` 后 channel 不存在；根 `up` → A 经门发 `reconcile` → c1 按 decl 发 `rebuild` → A 对 exists 跳过、对 new 重造；不重复登记 |
| 无接待员内脏的重新实例化 | T31 | y 的常驻函数在首个 incarnation 内计数 `1,2`；重启后第一次调用回到 `1`（Σ 归零）；y 账本零 up/down，全机只有根 channel 一条 `_root up` |
| 远端维护 | T27 + E2-c（历史真模型） | SIGKILL 的 dalek0 被邻居 `spawn <P>` 照 H 唤醒，当前 T27 见证根 channel 多一条 `_root up`；E2 原账见上述历史注记 |

**野外附证**（来自 E2 的旧语义过程，非独立设计）：d1/d2 在驱动收尾被 SIGTERM，原账本尾部出现 `world→接待员 down`；dalek0 被 SIGKILL 后无 down，pending 重跑。当前实现的对应进程级证据由 T28 给出，物理行仅落根 channel。
**调试记录**：一度以为 SIGTERM 后进程不退，实为**僵尸**——`Exec.spawn` 的 Popen 无人 `waitpid`，`killpg(pid, 0)` 对僵尸返回成功；R 的停机路径无缺陷（e8ae083）。
**未覆盖**：R 在一次事件中间崩溃（step 行写了一半）——H10 残，语义未定；自停（stop 请求 / C 停自己）。

---

## E4 · 换 world 的自举不动点（机制层 T29；真模型版未做）

**命题**："运行时对个体不可改，谱系可改"——机器能产出一个跑在**修改过的 R′**上的子代，且该子代仍是完整的 Dalek（能繁殖），变异随登记处遗传。

**方法**：世界变异从**打包器官**进入。C′ = spawn.py + 一处幂等变换（pack 前把 `G.world["runtime.py"]` 的 `_append` 行改为附加 `"w": 2` 字段——R′ 写的每一行带可观测标记）；`add c0 program tag=C bind=syscall,spawn`（同角色接替）→ `retire` 旧 C → 普通 `spawn`。R / Ω / syscall / A 零改动。

**结果**（T29，确定性）：父代所有行无标记（个体不可改）；子代 runtime.py 含标记、**每一行**带 `"w": 2`（跑在 R′ 上）、发育正常；`decl(子代).world` 含标记（born 把 world′ 记进登记处 → 可遗传）；子代 `spawn` 孙代成功（**不动点**：R′ 上繁殖仍成立），孙代继承 world′ 与 C′（变换幂等空转）。

**变异选择的理由**：改的是 **R 的记账规则**——`_append` 是介质写下每一行账的唯一通道，R′ 在每行多盖一个常量戳 `"w": 2`。选它因为三条：可观测性最大（子代任抽一行判定"跑在 R′ 上"，父代任抽一行判定"未被波及"，四条断言全靠它）；无害性最大（折叠只读已知键，R′ 与 R 的机器互相生养、账本互读——实验测"换 world 这条路通不通"，不让"新 R 对不对"搅局）；幂等可判（补丁存在则不再打，孙代不叠加）。它**没有**改任何转移规则——调度、call、投递、折叠原样；真要换实质规则（帧格式、调度、句柄式收件箱）走同一条 C′ 的路，但新 R 的合法性验证成为真问题，即下述"开放演化"。

**边界**：变异内容是实验者规定的（打标记），C′ 只是载体；"机器自己发明并验证新 R"（开放演化）不在本实验内。真模型版（模型照 diff 规格写 C′）未做。

---

## 复现

```
python3 runs/drivers/run_real.py  <key>     # E1：起机、发 task、打印 c2 账本（机器在 /tmp，key 不落仓库）
python3 runs/drivers/run_task1.py <key>     # E2-b：全链路（写器官 → spawn → 自组织 → 杀 → 唤醒）
python3 runs/drivers/run_task1b.py          # E2-b 续跑：唤醒三台 + 心跳 + 终局（不再调模型写码）
python3 runs/drivers/run_task1c.py          # E2-c：修复任务 + 终局（d1 的 c2 换 reporter；要 key 在机器 G 里）
python3 t/test_c0.py                        # 机制层：T24=E1；T25/T26/T28/T31=E3；T27=E2；T29=E4；31/31
```

模型配置以 `actors/l.py` 首行与 `ask()` 为准（4e0b54c 起：deepseek-v4-pro / 32768 / OpenAI 兼容报文，`/messages` 路径则讲 Anthropic 报文）。相关提交：cc2d239（T24）、fc29667（E1-b）、d50612d（T25–T27）、d8f4f3d（心跳）、4e0b54c（v4-pro）、a2186d2（E2）、f3dcd81（账本归档）。
