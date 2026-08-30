# Dalek：定义与讨论

从头开始。旧内核（v2–v6）已删，只保留 git 历史。本文档按讨论进度追加；第 0、1 章是定下来的，第 2 章起是讨论。
codex 审阅（2026-08-29）指出四个阻塞问题；1（运行时归包 P，不归 Ω）、2（两套构造模型并存 → 只留自发育）、3（c0 ≠ 运行时）已改入正文。4（C 未定义 → C 是 c0 里的一个程序成员，见 2.2.5 与 M1）已改入正文。四条全部处理。

---

## 第 0 章 公理与定义

### 0.0 概念表与上下游

| 符号 | 名字 | 是什么 | 死/活 | 谁产生 |
|---|---|---|---|---|
| **Ω** | 宿主 | Exec / Store / Port 三条契约（第 1 章） | — | 外面给的，不是 Dalek |
| **world** | 世界 | G 的根字段：随 P 运走、且携带零比特关于这台机器的信息的三样——**ω-bind**（omega.py：Ω 契约在本宿主上的实现）、**loader**（init.py + P 的布局与启动协议）、**R** | 软件 | 机器内无人读它：pack 抄，Ω 执行；对个体冻结、对谱系可变 |
| **R** | 运行时 | 三种 kind 的转移表 + 账本 + 根门（第 1½ 章）；world 的一部分 | 软件 | 同上 |
| **G** | 描述（基因），配置形态 | 一个 JSON：`world`（ω-bind + loader + R 的源码）、channels、members(kind, text, bind)、receptionist、peers | 死 | 初代：人写；之后：c1 从 H 折叠出（decl） |
| **P** | 机器，G 的可运行形态 | 同一个 G 展开到目录：`world` 写成文件 + G.json 原样。自包含，Ω 能跑 | 死 | pack = 展开（B：抄，不读） |
| **S** | 运行的机器（Space） | Ω.run(P) 得到的进程：R 驱动着一组 channel | 活 | Ω.run + 创造者的 A 经根门造 + start |
| **H** | 账本（历史） | 每 channel 一本，三种行：place / msg / step | 死，只增 | R 在 S 运行时写 |

S 里的三层：actor（kind + text + bind，行为）→ channel（账本 + 注册表 + 接待员 + 门，边界）→ space（S 本身，个体）。c0 / c1 / c2 是三个有角色的 channel：造（realize + C）、记（fold → decl）、写新描述。

```
                 decl = fold(H)          （c1：抄出当前形态，B 的另一半）
        ┌──────────────────────────────────────┐
        ▼                                      │
        G ──pack──▶ P ──Ω.run──▶ S ──R 边跑边写──▶ H
        │  (B: 抄)    (Ω)        ▲
        │                        │ realize（创造者的 A 经根门只造 c0；子代的 c0 收到 start\n<G> 后自己长其余——发育）
        └────────────────────────┘
                                 ▲
                      start\n<G>：创造者经根门发第一条消息，带着基因组，关门 = 切离（人 → dalek0；C actor → dalek1）
```

| 箭头 | 输入 → 输出 | 谁做 | 读不读 G |
|---|---|---|---|
| pack | G → P（换形态，不换内容） | c0 的 C actor | 不读，抄 |
| Ω.run | P → S（空的 R：零 channel，根门开着） | Ω | 不读 |
| start | 创造者 → S 的第一条消息 `start\n<G>`，关根门 = 切离，把 G 交给子代的 c0 | 人 / 父代 C actor | 不读，抄 |
| realize | G → S 里的器官。两段：创造者的 A 经根门只造 c0（+ 出生证明门）；子代自己的 c0 收到 `start\n<G>` 后长出其余 channel 与连线（发育）；之后 add / peer 生长 | 父代 c0 的 realize actor 或人侧 genesis（c0）；子代的 realize（其余） | **读**：唯一解释 G 的地方 |
| 写 H | S 的每一步 → H 的行 | R | — |
| decl | H → G_t | c1 | 折叠 |
| spawn | S → S′ | C actor = pack + Ω.run + 把 G 交给本机 A 经门造 c0 + `start\n<G>` | — |

不变量：G 在 S 里有三个身影——P 里的 G.json、H 里的 place 行、c1 折叠出的文件——三者相等。`world` 不是 actor，不进 H，所以拆成两条：`fold(H) == G.channels/peers + 改动`，`P.world == G.world`。

一句话：一台机器是一个 JSON；给它一个宿主（Ω）和一个创造者，它把自己长成一个会写日记的 Space；日记能折回 JSON；JSON 能连同世界的拷贝交给下一个进程——于是有了下一台。


### 0.1 公理

**一台机器是一个 Space，不是一个 channel。channel 是器官。**

机器是被构造、被运行、被复制、被比较的单位。channel 不单独构成机器：它不能自己被 `Ω.run`，不能自己复制，它的描述只是机器描述的一部分。

**为什么**（推论，不是约定）：一个 channel 自己有的东西——配置、账本、成员——全是死的。让它动的循环（运行时，见第 1½ 章）不在它里面，在 Space 这一层，被所有器官共享，不属于任何一个器官，c0 也不是。`Ω.run(channel)` 没有入口、没有驱动器，所以没有意义。**机器 = 能被 Ω.run 的最小单位 = 带着运行时的东西 = Space。** "channel 不能自主"的模糊感来源就在这里。

### 0.1½ 三层结构：actor → channel → space

```
actor    行为。kind + text + 绑定的句柄。由运行时驱动，Ω 执行。
channel  边界。账本 + 已注册 actor 的表 + 接待员 + 门。没有代码。
space    个体。channel 们 + 门的拓扑 + 一份运行时实例。能被 Ω.run 的最小单位。
```

- **没有 channel 的源码，只有 actor 的 text。** channel 是纯结构；它的描述是 G 里的一个结构节点。actor 的描述是 kind + text + 句柄：程序的 text 是源码，oracle 的 text 是提示语，门的 text 是地址。
- **actor 的接口、能力、kind 不在 G 里定义**：kind 的语义是运行时的（转移表），句柄是 Ω 的（Exec / Store / Port）。G 只引用它们。
- **c0 不是 actor，是注册了构造 actor 的 channel。** 它之所以是"构造器所在"，只因为它里面注册了会 realize 的 actor 和会 C 的 actor，且这两个 actor 绑定了"放 actor"这一介质动作和 spawn 句柄。换个名字、放到别的 channel，机器照跑。特权 = 谁绑定了"放 actor"，写在 G 里，运行时内容盲地绑。
- 每层不越级：actor 不知道 channel 之外有什么，channel 不知道 space 里还有谁，space 不知道 Ω 上还有谁——三层膜。

### 0.2 三个定义

| 词 | 定义 |
|---|---|
| **描述** | 一个配置文件（JSON）。死的。说一台机器**是什么**——含根字段 `world`（ω-bind + loader + R 的源码）。G 的配置形态。 |
| **机器** | 同一个 G 的可运行形态：展开到目录，`world` 成为文件。死的。自包含 = 除了 Ω 什么都不缺，**包括自己的描述原样在内**。 |
| **运行的机器** | `Ω.run(机器)` 得到的进程，加上它运行后写下的账本。活的。 |

三者的关系：

```
描述 ──pack──▶ 机器 ──Ω.run──▶ 空的 R（根门开）──创造者的 A 经根门造 c0──start\n<G>──▶ c0 自己长出其余 ──▶ 运行的机器（完整 Space）
  ▲               │
  └──── decl ─────┘          （机器里原样带着描述；decl 把它取出来）
```

- **pack** 是 Dalek 的事：输入 G，输出 P——换形态不换内容：把 `world` 写成文件，G.json 放旁边。死的、确定的、不解释描述。
- **run** 是 Ω 的事：输入机器，输出进程。Ω 不读描述。
- **realize** 是运行中的 c0 的事：读描述的结构，逐项发 syscall——经根门造一台新机器（造子代；人造 dalek0 用同一套 syscall），或在本机器内生长（add / peer）。**解释描述的只有这一步。**
- 只有一条构造路径：空的 R + 创造者经根门造 c0 + `start\n<G>` 关门 + c0 发育出其余。不存在"在静态目录里先造好全部器官再运行"的路径，也没有 boot。

---

## 第 1 章 Ω：宿主契约

Ω 是机器运行于其上的底层。它是**固定的、有限的、不含任何 Dalek 词汇的**能力清单。Dalek 是运行在 Ω 上的一个软件；Ω 不知道什么是 channel、c0、管理器、Space、复制。

### 1.1 三条能力

| 能力 | 契约 | 一个实例 |
|---|---|---|
| **运行** | `run(源码, 参数) → 进程`；能等它结束、能杀它 | python3 + 进程 |
| **存储** | `read / write / append(路径, 字节)`；append 原子、落盘 | 文件系统 |
| **网络** | `request(url, 字节) → 字节`；`serve(端口, handler)` | http |

"运行"必须含进程：不只是把源码翻成可执行，而是让它成为一个独立活着的东西，并且能停它。没有这条，子代不能独立于父代存活。

"网络"必须含 `serve`，不只 `request`：机器要能被找到——人要跟它说话，另一台机器要给它递描述。单机实验可以用文件代替；LLM 和跨宿主必须网络。

### 1.2 Ω 明确不提供的

- 谁能构造（Dalek 内部的权限，Dalek 自己管）
- 调度、channel 语义、消息格式
- 重演、验证
- 多台机器之间的隔离（靠目录约定）

### 1.3 判据

Ω 的接口表上出现任何一个 Dalek 词，就是作弊——把要证明的能力偷偷放进了宿主。反过来，只要一个底层系统满足 1.1 的三条，它就是一个合法的 Ω；Dalek 的身份不依赖任何特定宿主。

分层不靠"去掉它跑不跑得起来"（R 也满足），靠两个问题：

| | 随 P 运走？ | 携带这台机器的信息？ |
|---|---|---|
| **Ω**（Linux + python3 + 文件系统） | 否，落地处提供 | 否 |
| **world** = ω-bind（omega.py）+ loader（init.py、P 的布局）+ R（runtime.py） | 是 | 否 |
| G 的其余部分 | 是 | 是 |

**契约是不动点，绑定是基因**：Ω 是抽象契约；ω-bind 是它在某一宿主上的实现，在 world 里随 P 遗传，所以换宿主 = c2 写一个新的 omega.py（Port 改 http、Exec 改容器），pack 一个子代用它跑，自举不动点验收——与换 R 同一条路。loader 知识（入口文件名、`--serve`、目录布局）只能在 world 里：R 的 `spawn` 知道 `init.py <P> --serve` 是 world 知道自己的布局；放到 G 里的 actor 才是组织读世界。

### 1.4 非确定性的来源

**凡不能由 G、H 和确定程序重新推出的 Ω 返回值，都必须作为外生观察入账。** LLM 和人是主要来源，但不是唯一来源：时间、失败、并发顺序、外部文件都可能成为外生结果。（旧句"只有网络非确定"撤回。）
运行时结构上已满足这条：转移表把每个 actor 的输出**原样追加**，不管它从哪来。于是它变成重演策略：每一步要么重算并比对（确定性检验），要么照抄记录。

### 1.5 第一个 Ω

Linux + python3 + 文件系统 + http。

---

## 第 1½ 章 物理：最小运行时

### 1.6 冯诺依曼里没有运行时

他的机器是元胞的排列，泡在一个有固定转移规则的空间里。A、B、C 动，不是因为机器里有循环在跑它们，而是空间的规则在更新每个元胞；"伸臂、置态"这些原始动作也是规则自带的。让东西动的机制是**空间的物理**，不在机器里，不在描述里。

对应过来：消息循环、调度、投递、落账（R）对**机器**而言是物理——机器改不了它，基因不描述它，正如 I_D 不描述转移规则。但它相对 **Ω** 是软件：Ω 不认识 channel，所以它不可能是 Ω 的一部分；空白宿主要 P 把它带过去，这已经证明它不属于宿主。它也不是 c0：它驱动所有器官，包括 c0。

两刀切完（不是 Ω、不是 c0），它住在 **G 的根字段 `world` 里**，P 是它的文件形态。三层：

```
Ω        宿主契约：Exec / Store / Port。不含任何 Dalek 词。不随 P 运走。
world    随 P 运走、不含这台机器信息的三样：ω-bind（契约在本宿主的实现）、loader（P 的布局与启动协议）、R（转移表）。机器内无人读它；Ω 执行它。
组织      G 的其余部分：channel、成员、连线。c0 按它 realize。
```

- **判据**：运行时里只能有介质词汇（actor、账本、消息、投递），不能有组织词汇（c0、c1、构造器、登记、复制）。
- **可遗传且可描述**：R 的源码——连同 ω-bind 和 loader——就是 G 的根字段 `world`。这是冯诺依曼的 CA 做不到的一步（规则表写不进带子），生物也做不到（密码表不在基因组里）；软件可以，因为世界也是文本。守住的一条：**机器内没有任何东西读 `world`**——pack 抄它，Ω 执行它，R 不读 G。改名检验照样通过：同版本世界的所有 G 的 `world` 逐字相同。恢复时它缺不得（world + H，不是只有 H）。
- **G 是 H 的投影**：因为每一次放 actor 都带完整 text 记在账上，H 是 G 的超集，`G_t = fold(H)`，不需要 G₀ 做起点。从 H 恢复得到的是组织，不是机器——`world` 不是 actor，不进 H。c1 的 decl 把 `world` 从 P 原样抄回来拼上。P 里那份 G 原样仍然要：它是 B 的产物，子代在有任何 H 之前靠它 realize；一致性拆成两条：`fold(H) == G.channels/peers + 后续改动`，`P.world == G.world`。
- **代价**：运行时对个体不可改（正在跑的 R 改不了自己）。谱系可以：c2 改 G′.world，pack 一个子代用它跑，自举不动点验收。
- 旧句"Ω = 硬件 + 物理"撤回。Atoll 对应的是 Ω + 运行时两层，不是 Ω 一层。

### 1.7 运行时的定义

**运行时 = 一个极小的状态空间 + 一张对内容重命名不变的转移表。** 它有操作语义（什么东西移到哪里），没有意图语义（这一步是在"构造"还是在"聊天"，它不知道）。像冯诺依曼的 29 态规则表，只是行数少得多。

**状态**：每个 channel 一本只追加的账本 + 每个 actor 一个游标（看到哪了）。
**事件**：某本账上多了一条写给某地址的消息。
**转移表**：

| 事件落在的 actor 的 kind | 转移 |
|---|---|
| **程序** | 取视图 → `Exec.run(text, 视图)` → stdout 原样追加为步记录（谁、看到哪、回了什么）+ 拆成动作：消息、syscall、绑定了的 world 动词（spawn / stop，一张表，用 Ω 实现）；游标前移 |
| **oracle** | 取视图 → Ω 侧的端点 → 回答同程序行；游标前移 |
| **门** | 把这条消息原样 `Port.send` 到 text 所指的端点（本机 channel 名也是端点），署名本 channel 的端点；对面收件箱进账时署名指回来的门，收件人是对面的接待员 |
| **放 actor**（syscall `channel.add.actor`） | 在该 channel 的下一个地址写下 kind + text，在该账本记一行——**这一行带完整的 kind + text**。channel 存在 = 它账本有第一行；经根门放也走这一行 |

三种 kind 一行一条，加一条放 actor；没有第五行。视图 = 写给该 actor、且它上次没看过的那些条。

**kind**：三种，都是"转移表定行为 + 一个 text 参数"，区别只在 text 是什么——程序的 text 是源码，oracle 的 text 是提示语/模型绑定，门的 text 是对面的地址。门最退化：text 不是行为、不是内容，是指针；但它是唯一 text 指向 channel 外面的 kind。整台机器的拓扑 = 全部门的 text 的集合。

**门**：是 actor，不是 channel（没有自己的账本，是两本账之间的管子）。一条连线 = 两扇门互指。膜内成员只能写给本 channel 的地址；要出去只能写给门；外面的东西进来必须先变成账本上的一行——"影响一个 channel 的一切都在它的账本上"由此保证。内外同一种门：门只做一件事——原样 `Port.send` 到 text 所指的端点；对面是本机器另一个 channel 时端点就是本机的收件箱，对面是人、LLM、另一台机器时是外面的端点；运行时里只有一条门的规则。冯诺依曼的"+"在这里有了定义：接上 = 两本账各有一扇门互指。

**内容盲**（运行时唯一的纪律）：运行时只处理形，不处理义。它读的东西有且只有：地址、kind、text 作为该 kind 的参数。检验：把 G 里所有组织层的名字全部替换（c0 改叫 x7，源码做等价改写），运行时行为逐字节不变。

| 运行时认识的（介质词汇） | 运行时不认识的（组织词汇） |
|---|---|
| 地址（序号、门） | c0、c1、c2 |
| kind：程序、oracle、门 | 构造器、登记处、作者 |
| text 作为参数 | text 的含义 |
| 消息、账本、追加、投递、视图、步记录、放 actor | syscall、realize、pack、decl、clone；谁有权放、放了算不算采纳 |

违反内容盲的样子（都是旧版本犯过的）：运行时里有 `if name == "c0"`；运行时认识一种"构造请求"并替它执行；运行时校验"这是不是合法机器"；调度器给某器官优先级；运行时保存一张"配置 ↔ 实例"表。

为什么必须盲：诚实（构造是机器做的，不是宿主替它做的）；通用（不偏向任何一种机器）；能冻结（组织词汇再长，运行时不用动）；能替换、能验证（规范只覆盖介质词汇，几页纸）。

**全局账本还是每 channel 一本**：便利问题，条件是每个 channel 的账本必须能从全局账本完整投影。**路由**：暂不做，每条消息自带合法地址；通用路由是一个功能性 actor。

**运行时的六个性质**（它是支撑而不是桎梏的条件）：通用（限制怎么通信，不限制能算什么）；小（一页纸，能被整个读懂、验证、独立重写）；内容盲；忠实无私状态（每一步效果都在账上，自己不做决定）；按规范定义、实现可替换（同一 G 跑在不同实现上，多实现互相核对）；**对个体冻结、对谱系可变**（正在跑的机器改不了它；但它是 P 里的源码，c2 可以写出运行时′，pack 一个子代用它跑，用不动点测试验证——编译器自举、内核换代的方式）。历史上同一位置的东西：语言的操作语义 + ABI、内核 ABI、遗传密码表 + 核糖体、β 归约、UTM 的读写头、Lisp 的 eval、Smalltalk VM。

### 1.8 待定

新 actor 怎么出现——运行时一侧已由内容盲定形：只有"放 actor"这一 syscall。谁能用它、born / peer 如何由它和门组合出来，是组织层的事，见第 2 章，与 syscall 一起定。

---

## 第 2 章 讨论：构造器

### 2.1 已定

**两个接口，分工固定**：
```
pack(G)      → P      死的。P = G 的目录形态：G.world 写成文件 + G.json 原样。不解释 G。
c0.realize(G)         活的。运行中的 c0 读 G 的结构，逐项发 syscall：经根门造子代，或在本机器内生长。唯一解释 G 的地方。
```
旧写法 `construct(G) → 完整机器` 撤回：它和"空 R + 经根门逐条造"是两套模型，只能留一套。留后者（2.2.5）。

**pack 的输出是源码不是进程**，四个理由：
1. 把包变成进程是 Ω 的事。pack 输出进程就得内含某个 Ω 的起进程语义，绑死在那个宿主上。字节才能跨宿主：进程传不过网络，包传得过。
2. 冯诺依曼的 A 造出静止的机器，C 才启动。输出必须是死的。
3. 死的才能比。不动点判据比的是两个包的字节或两份描述；进程没法比。pack 因此是纯函数：同一份描述，永远同一个包。
4. 两次使用要看得见。包里并排放着运行时源码和原样的 G（B 的产物），谁都能检查描述有没有被吞掉。

**pack 不生成任何东西**：运行时源码是抄的，G 是抄的。它只是把两样东西放进一个 Ω 能跑的目录。抄得越干净，A 越不可能吞掉 B。

**pack 只用 Ω 的存储**。不起进程，不上网。

**realize：造一台机器 = 依次造它的器官**。c0 只有一个核心操作：按一个 channel 描述造一个器官。`realize(G)` = 对 G 里的每个 channel 做这一操作，然后按拓扑放门；第一条消息（start）不是 realize 的事，由 C（或人）经根门发。所以：
- 表面上在解决"channel 怎么造、怎么复制"，实际上解决的就是 Space 的构造与复制——两者不是两个问题。
- channel 从不单独被复制。它只在一台机器被构造时作为器官被造出来；"复制一个 channel" = 用同一份 channel 描述再造一个器官，仍然是构造。
- 递归由此免费得到：Space 描述里某一项若本身是一台机器的描述，c0 对它调用自己。

**描述 = 结构字段 + source 字段，两类字段处理方式不同。**
- 组织（JSON）：有哪些 channel、每个里有哪些成员（kind + 指向源码）、接待员、门指向哪。回答"怎么排"。
- 行为（源码）：每个成员做什么。回答"每个元胞是什么"。
源码可以作为 JSON 里的 text 叶子，描述就是一个 JSON；展开成目录只是它的另一种形式。分界不在"哪个文件"，在**谁解释**：结构字段（channel、成员 kind、接待员、门指向）由 c0 解释；source 字段（text）c0 不解释、只抄，以后由运行时解释。这正是 A 与 B 的分离落在字段上。守住这条，c1 折叠账本时才分得清哪是组织改动。
kind 的取值范围由物理封闭（程序、oracle、门）；开放的只有源码内容。

**新描述从哪来。** 最小机器 {c0, c1} 造不出新 actor，不是格式的限制，是它没有作者：它的描述只能被抄，不能被扩展。L + U 接成一个 channel（L 写、U 跑、L 改，账本驱动器让它们来回）就是 coding agent，记为 **c2**。c2 的产出是描述：新 actor 的源码 + 一小段组织（它是谁的成员、接谁）。c0 装，c1 记，描述从 {c0, c1, c2} 变成 {c0, c1, c2, F}，F 可遗传——E → E_F，变异在机器内部发生。
新东西的**来源**仍在机器外（LLM 是 Ω 的 oracle），但**生产新描述的组织**进了机器里：机器不产生随机性，机器组织随机性。

**划到工程侧的（不是理论模型）**：描述是一个文件还是一个目录；谁有权发形态改动请求、装了算不算采纳；描述里要不要列出外部能力需求（理论只说：造不出来是 Ω 不够，不是描述错）。

**通用性**：`realize` 对任何合法描述都工作，不只对自己的。这是它和病毒 / quine 的区别——描述被用两次（解释、抄），且能造别人。验收时必须造一台 ≠ 自己的机器。

**复制的不动点**（验收）：
```
m′ = Ω.run(pack(m.c1.decl()))；m.c0.realize 经 m′ 的根门造出 m′ 的全部器官；m.C 发 start
m′.c1.decl() == m.c1.decl()
Ω.run(pack(G′))，G′ ≠ m.c1.decl()，得到另一台机器且它的 decl() == G′
```

### 2.2 待讨论

1. ~~描述的形状~~ → 理论层已定（组织 + 行为，见 2.1）。具体字段与 syscall 一起定。
2. ~~包的形状~~ → 定（0.2）：P = `world` 写成文件（omega.py / runtime.py / init.py）+ G.json 原样。成员文本不单独成文件，在 G.json 里。
3. ~~构造器住在哪~~ → 定：**构造器和复制器在描述里，是描述里的第一项。** 按冯诺依曼 E = D + I_D，I_D 描述的正是 A+B+C 自己。对应过来：构造器（c0，realize）和复制器（c1，decl）是描述里的两个普通成员，它们的描述就是它们的源码，与任何工具的程序文本同等地位。pack 下一台机器时，把这两段源码当成员文本从**描述里**抄进包——不特殊对待，不从自己运行中的代码取。这就是"描述被用两次"落在构造器自己身上。
   ~~dalek0 的基因因此只有：一个 channel `c0`，两个成员~~（历史方案，已被 M1 的 {c0, c1} 取代）。
4. ~~一个包是一台 Space 还是一个 channel~~ → 由公理定：**一个包 = 一台机器 = 一个 Space**；描述里的多个 channel 都在这一个包里。由定义推出（未单独决定，可推翻）：一次 `Ω.run(机器)` 是一个进程，channel 是进程里的对象；进程之间只有机器与机器的关系，经网络。
5. ~~启动（C）放哪~~ → 定：**C 住在 c0 里。** C 是普通程序 actor：pack → `Exec.spawn` → 把 G 交给本机的 A 经根门造子代 → 发 start。它必须是描述里的成员，不能是外面的脚本。
   - **根门**是 Space 级的、属于 R、在任何 channel 之前存在，不在 G 里。它接受三个词：`channel.create`、`channel.add.actor`（含 actor.create）、`msg`。前两个造，`msg` 是第一条消息——启动，并顺手关门。**根门开着 ⇔ 账本里没有任何 msg 行**，由账本派生，无隐藏状态。
     一句话：**根门是脐带——出生前唯一能从外面往身体里放器官的口子，第一声之后就废掉。** 它在账本上留疤：`by=_root` 的那几行是脐带放进来的器官，出生证明门是它连着的另一头。
   - **boot 不存在**：Ω.run 起 R，R 开着根门等。零行构造、零次读 G。
   - **发育版（2026-08-30 定）：父代只做三件事——造 c0、复制 G、发 start。** 父代的 realize 读 G 的第一个 channel（c0），把它的成员写成 syscall 发给指向子代根门的门，最后放一扇指回父代的普通门（出生证明，不在 G 里；放在 c0 的成员之后，成员地址才与 G 里的序号一致）。然后 C 经根门发 `msg c0\nstart\n<G>`：第一条消息带着基因组，关门。子代的 c0 收到它，把其余 channel 和全部连线用本地 syscall 长出来（`by=c0/1`）。父代的 A 只搬运 text 不解释它——A 读结构不读内容。账本上因此看得见两只手：c0 的行 `by=_root`（父代），其余 `by=1`（自己）。**唯一不是 c0 造的器官是 c0 自己。** 之前的冯诺依曼原版（父代的 A 造全部，子代的 realize 出生时无事可做）撤回：发育版让子代的 A 在出生时就被使用和检验，父代的义务缩到最小，并与生物一致——亲代给的是带机器的细胞（world + c0），其余按基因组发育。这不是有性繁殖（那要两份 G 重组，见 M3 之后），是母体效应。
   - **切离 = 关门 = start**，一个动作。门开着：形态由膜外的 A 决定，机器是被造的对象；门一关：形态只能经自己的 c0 改，机器成了主体。封闭性由此从"要守护的不变量"变成一次事件——出生。
   - 构造期间机器机械地不动（没有 msg 行就没有 pending）：准静止是自动的。
6. **内部原语还要不要**。旧模型里的 born / add / copy / peer / start 是运行时的 syscall；现在 pack 是纯函数、realize 在运行中做，这些词变成了什么？

---

## 第 3 章 里程碑

### M1 · 最小机器：描述并构造自己

**约定**：一台最小的机器 = Space { c0, c1 }，一条连线 c0–c1。按 0.1½ 的三层：c0、c1 是 channel，里面注册的 actor 才有代码。

- **运行时 ≠ c0。** R 驱动所有 channel，包括 c0。R 还带着一扇 Space 级的**根门**（在 channel 之前存在，不在 G 里）。
- **R 的 syscall 两个词**：`channel.create(name)`、`channel.add.actor(channel, kind, text, bind[, in])`（含 actor.create：actor 只在被加入某个 channel 时诞生，没有游离的 actor）。持有 `bind=syscall` 的 actor 可以在本机器内发；根门开着时膜外可以发。c0 的 `add / build / spawn` 叫**请求**，是 syscall 上的程序。
- **c0 = 注册了两个 actor 的 channel**：**realize actor**（A：读 G 的结构，逐项发 syscall——本地生长，或经门造子代；接待员）、**C actor**（pack → spawn → 把 G 交给 realize 经门造 → start；绑定 spawn）。c0 是唯一持有形态变更能力的 channel，仅因为这两个 actor 注册在它里面。任何形态改动都是给 c0 的请求。
- **入账规则**：一条形态改动**三边同时记账**——被改的 channel 记一行，c0 记一行，c0 再经门给 c1 发一行。
- **c1 = 注册了登记 actor 的 channel**：对自己账本上收到的形态改动折叠一遍得到配置，落成文件；`decl` = 原样抄出 + 从 P 拼上 `world`。不读别的账本。文件是派生物。
- 通用性落在 realize 的"任意合法 G"上；任意机器 = 世界 + 任意 G。

**造另一台机器**（冯诺依曼的三步，一步不少）：
1. `G = c1.decl()`（c1 落地前：P 里的 G.json）。
2. **B** `P′ = pack(G)`：把 G.world 写成文件，G.json 放旁边。C actor 做。
3. `Ω.run(P′)`：子代的 R 起来，根门开着，账本全空，等。
4. **A（父代的手）** 父代的 realize 收到 `build <门> <父代地址>\n<G>`，经门发 syscall 只造 c0：`channel.create c0`；c0 的每个成员 `channel.add.actor`（text 逐字搬运，不解释）；最后放一扇指回父代的门（出生证明）。子代账本：c0 的前 n 行 `by=_root`。
5. **C** 父代的 C actor 经根门发 `msg c0\nstart\n<G>`：第一条消息，署名出生证明那扇门，正文带着 G。**门关，切离。** 父代义务结束。
6. **A（自己的手）** 子代的 realize 收到 `start\n<G>`，用本地 syscall 长出其余 channel、成员和全部连线（`by=1`）。发育完成，静止，等收件箱。

父代从不进入子代的进程；它只往子代的根门收件箱写行。子代的 realize 出生时就干活：它造出的器官和父代造它时用的是同一套 syscall——造出来的 A 出生即被检验。

**同一份 G 的两次使用**：pack 抄（B，不读）；realize 读结构、搬 text（A）。父代账本上有发出的每一条 syscall，子代账本上有落地的每一条：复制在两边都可重演。

非确定性不在 c0、c1 里；只从后来长出的作者器官（LLM、人）进来。

G（dalek0；根门与出生证明都不在 G 里）：
```
{ world: { omega.py, runtime.py, init.py 的源码 },
  channels: [ { name: c0, members: [ {kind: program, text: <realize 源码>, bind: [syscall]},
                                     {kind: program, text: <C 源码>, bind: [syscall, spawn]} ],
                receptionist: 1 } ],
  peers: [] }
```

验收：
```
m 造 m′（上面五步）；m′.c1.decl() == m.c1.decl()                复制闭合
改 G 里 realize 的 text 一处，父代不动，造 m′ → m′ 行为按改动变    子代是从 G 造的，不是抄的（非平凡）
m 造 G′ ≠ 自己的机器，其 decl() == G′                           是构造器，不是复印机
杀掉 m，m′ 仍能造 m″                                          独立
关门后经根门再发 channel.add.actor → 无效                       切离
杀掉 m′ 的进程，只留 world 与账本，重起后 decl 不变              配置可从账本恢复
```

**什么是只抄不解释的**：G.world = R。内容盲，换任何机器它的字节不变，携带零比特关于这台机器的信息——抄它是安装世界，不是复制自己。世界（Ω + R）里没有任何一段知道 G 形状的代码；构造的递归在机器之间闭合，底是人：dalek0 由人经根门用同一套 syscall 造出来。

Ω：Linux + python3 + 文件系统（M1 不需要网络）。

### M2 · c1：登记与 decl（周日 2026-08-30）

- c1 = 注册了登记 actor 的 channel。c0 每次 syscall 后把 place 行（含完整 text）经门转发给 c1（三边记账）；c1 折叠自己的账本得到 `channels/peers`，从 P 拼上 `world`，落成文件；`decl` = 原样抄出。
- C actor 的 pack 改用 `c1.decl()`，不再抄 P 里的 G.json（关 H4）。
- 逻辑删除 `retire <channel>/<addr>`：c1 表上划掉，不再给它送消息；账本不动。

验收：
```
运行中 add 一个 actor 后 spawn 子代 → 子代有这个 actor            遗传运行中的形态改动
decl(S) 与 fold(H) 逐字相等；P.world == decl.world                三个身影
改 realize 的 text 一处 → spawn → 子代的 realize 是新的            非平凡（此时才真正可测）
```

### M3 · c2 与网络：一次演示三个自我（周一–周二 2026-08-31 / 09-01）

c2 = L + U 接成的 channel（coding agent，"code mode"）。两个演示任务，先 **c2 → c2′**，再**自组织**。

**任务 0 · c2 → c2′：改进器改进自己**（Trusting Trust 的启发；Yampolskiy 的 Level 3——改进"改进机制"本身）

- 第一件事不是写别的 actor，是**给 code mode 自己加一个工具**：一个 actor，收到 `show G` / `show H <channel>` 回本机的 decl 或账本片段。这是论文里的 "self-access"，但**结构性**地实现：机器认识自己 = 读自己的描述和历史，不是模型读权重。它也是任务 1 本来就要的（写 hub/reporter 前得知道本机有哪些 channel、门指向哪）。
- 机制上没有新东西：c2 的产出是 G 的增量（新工具的 text + 它是 c2 的成员），经 c0 `add` 装、c1 记、子代继承——**改进器改进自己和改进任何东西是同一条 syscall**，因为改进器不是特殊的，只是 G 里的几段 text。这本身就是要演示的命题。
- Trusting Trust 的教训不是"自编译"，是**不要原地改**：改坏了自己的 U，就再也验不出任何东西。所以走"本版的不动点"那节的流程：c2 写出 c2′ 的描述 → c0 造出 c2′（同机器里另一个 channel，或带 G′ 的子代）→ **老 c2 的 U 测 c2′** → c2′ 做同一任务得 c2″，`decl(c2″) == decl(c2′)`（自举不动点）→ 通过才采纳（`retire` 老 c2 或切接待员）。
- 攻击面因此说清：G 和 H 全是文本，没有二进制，"看不见的传染"只能藏在 Ω 的 python 和 **L 的权重**里——L 是本体系里那台不透明编译器。DDC 的对策可用：两个不同的 L 各生成一次 c2′，比对（记为论文里一句话，不做）。
- 任务与判据从门外来（人给 t* 和回归集 T）：选择压力来自膜外，不假装内生。

验收：
```
t*：回答"本机有几个 channel、各自接待员是谁"     c2 失败（没有读 H 的工具）；c2′ 通过
回归：c2 之前通过的任务集 T                        c2′ 全部通过
自举：c2′ 接到同一任务 → c2″；decl(c2″) == decl(c2′)   改进器仍是改进器
遗传：spawn 子代 → 子代的 c2 带着新工具               L3 的改进被继承
```
洞：syscall 只有 create/add，没有 replace；"改 U 的 text" = add 新 + `retire` 旧，接待员怎么切换随 M2 的 retire 一起定。

**任务 1 · 自组织**

1. **自改进**：c2 写出两个 actor 并通过 U 的测试——**hub**（c3，住在 dalek0）和 **reporter**（c4）；经 c0 `add` 装进本机器；c1 登记。G 从 {c0, c1, c2} 变成 {c0, c1, c2, c3, c4}。
2. **自复制**：C 用 `c1.decl()` spawn dalek1、dalek2——它们生来带着 reporter。
3. **自组织**：协议只有四个词：
   ```
   reporter → hub：       hello <我的端点>
   hub → reporter：       peers <端点…>                （intro）
   reporter → 自己的 c0： add c0 door\n<端点>          （对每个 peer 放一扇门——这一步才是组织）
   peer ↔ peer：          ping / pong
   ```
   hub 的记忆是它的账本。子代出生、start 一到，reporter 就 hello：**组织是遗传来的**。

验收：三台机器的 G 各自多出指向对方的门；ping/pong 在两边账本上都有；杀掉 dalek0 后 dalek1↔dalek2 仍在 ping。
概念上要写准：这是**种群层面**的自组织——网络的拓扑分散在每台机器自己的 G 里，没有网络级的 G 或 c0。它是生态，不是更大的有机体（那是递归 Space，另一个里程碑）。单机多进程用 file 端点；http 只在跨宿主时需要。

### M4 · 生命周期与自维护

**Space 的五个状态**（全部由 P 的内容派生，无标志文件）：

| 状态 | P 里有什么 | 根门 |
|---|---|---|
| unborn | world + G.json，没有账本 | 开 |
| constructing | 有 place 行、没有 msg 行 | 开 |
| alive | 有 msg 行，有进程 | 关 |
| dormant | 有 msg 行，没有进程 | 关 |
| dead | H 没了 | — |

转移：pack → unborn；`Exec.spawn(init, P)` → constructing；第一条 msg → alive；`stop` → dormant；**在同一个 P 上再 `Exec.spawn` → alive**（重启 = 账本非空的出生；出生 = 账本为空的重启，R 不区分）；删 H → dead。自己不能重启自己（死的东西不能动）：dormant → alive 的主语是父代、peer 或人。dormant 是合法状态（休眠、孢子、停掉未删的容器），故障只是"没人来唤醒"。

**起停入账，外面只给信号**：
- 起：R 折叠 H 后，**若根门已关**（已出生），对每个 channel 追加 `msg from=world to=接待员 body=up`。未出生（根门开着）不发——发了会关门；出生的第一条消息仍是父代的 start。**出生 = 父代的 start；醒来 = 世界的 up**，两个词、两个来源，都在账上。
- 停：外部信号（`Exec.stop` 的 SIGTERM，或经收件箱给 C 的 stop 请求）→ R 对每个 channel 追加 `down` → 跑到静止或预算耗尽 → 退出。自停：C actor 绑定 `stop`（与 spawn 同类的 Ω 动作），走同一条路。
- 各器官对 up/down 做什么由 G 定，不由世界定：c1 收到 up → reconcile；reporter 收到 up → hello hub、收到 down → 告别；c0 什么都不做。
- 崩溃可判定：账本最后有 up 没有 down → 上次是硬杀，没写 step 行的 pending 消息会重跑（at-least-once；有外部效果的动作要幂等，spawn 目录已存在则不再起）。第几条 up = 第几次 incarnation。

**自维护 = 期望与实际的对账。** 期望 = c1 的注册表（decl）；实际 = R 折叠 H 的结果。不变量：G 里每个 channel 在 H 里都有一本账且至少一条 place 行。

- **本地损伤不需要时钟**：`stop → rm h/c8.jsonl → spawn → up` → c1 收到 up → 对注册表里每个 channel 向 c0 发 `rebuild <描述>`；c0 逐个 `channel.create`，**返回 new | exists**：exists 跳过，new 才 add 它的 actor 和门。幂等、无探测、一轮消息。重造出的 c8 是新器官：同样的 text、空的账本（照 spec 重造）；整机重启是 R 折叠 H（照 WAL 重放）。两种损伤两种恢复，不混。
- **channel 存在 ⇔ 账本里至少一条 place 行**（不看 `_order`）。
- **远端损伤才需要时钟**：对面机器死了，本地没有文件可查，只能"ping 了没 pong"；ping/pong 链自计时但链断了 reporter 不会再醒——发现链断需要一个膜外的 tick（Ω 的时钟经门送进来，和 LLM 同类的 oracle 端点）。发现后：照 G 长一个新 hub（reporter → c0 `add c3 …`），或 peer 把 dormant 的 dalek0 再 spawn 起来（照 H 恢复，同一个个体醒来）。
- H 坏了不可修：H 是真相。能做的是备份（工程）。

验收：
```
stop，rm h/c8.jsonl，spawn，→ c1 收到 up → c8 回来，text 相同，账本为空       本地维护，零时钟
stop，spawn → decl 不变，游标不变，pending 消息重跑，账本有 down/up            重启 = 同一个体
杀掉 dalek0 进程，tick → dalek1 长出 hub 或重新 spawn dalek0 → ping/pong 恢复    远端维护
```

**要改的机制**（介质级，不认识名字）：R 起来时广播 up（仅已出生）；SIGTERM → 广播 down → 静止 → 退出；收件箱偏移从 H 派生（收进来的 msg 行带收件箱行号，关 H6）；step 之间退出。

### 本版的不动点（定位）

**G.world = R**（可描述，但机器内无人读、只抄）本版不处理其变异，**作为当前理论的不动点**：介质的不动点，世界的版本的不动点，不是机器的版本的——同一个 world 服务所有 G。构造器没有世界侧的不动点：递归的底是人造 dalek0。构造器（realize、C、登记 actor 的源码）在 G 里，改它们的 text 子代就变——机制上已经允许变异，本版只是**不验证**变异后的构造器是否仍是合法构造器。

但这是本版选定的不动点，不是理论上的不动点。理论上没有东西是不动点：**c2 总是可以重新编码它们**——运行时和 c0、c1 都只是源码；再往下，连 Ω 的编译器本身也不是不动点：c2 可以在旧 Exec 上写出一个新的解释器 / 编译器，谱系可以整体迁到它上面（这正是编译器自举一路做到 hex0 的事）。路径都一样：写出新版本，pack 一个子代用新版本跑，用自举不动点验收：

```
旧版本造子代 m*（带新版本）
m* 用同一份 G* 造孙代 m**
m**.decl() == m*.decl()，且 m** 能继续造
```

通过前新版本只是候选。这是 1.7 "对个体冻结、对谱系可变"的落实，而"可变"的边界可以一直推到硬件。先把不变的做通，再让它变。

---

## 第 4 章 ABI 与 syscall（M1 实现约定）

理论层到第 3 章为止。本章是工程约定，但必须与 1.7 的转移表和内容盲一致。

### 4.1 账本行

每个 channel 一个文件 `h/<name>.jsonl`，单写者（本机器的 R），三种行：

| k | 字段 | 谁写 |
|---|---|---|
| `place` | `seq, addr, kind, text, bind, in, by` | R 执行 `channel.add.actor` 时；**带完整 text**；`by` = 发起者地址，或 `_root`（经根门） |
| `msg` | `seq, from, to, body` | 成员输出拆成的消息；门抄来的消息；syscall 的返回 |
| `step` | `seq, actor, upto, out, err` | 某 actor 被叫醒：看到哪、原样回了什么 |

channel 的创建顺序记在 `h/_order`。`from` 可以是 `door`（膜外来、无对应门）或 syscall 名（返回）。

### 4.2 根门与收件箱

- `in/_root.jsonl`：Space 级根门。**开着 ⇔ 所有账本无 msg 行。** 开着时接受 `channel.create <name>`、`channel.add.actor <channel> <kind> [in] [bind=…]\n<text>`（by=_root）、`msg <channel>\n<body>`（追加给该 channel 的接待员，署名匹配的门或 `door`；**这一条关门**）。关门后根门的行全部忽略。
- `in/<channel>.jsonl`：channel 级收件箱（`Port.recv`，Ω 的接收侧），只收 `{from, body}` → msg 给接待员，署名指回 from 的门（本机 channel 名与 `file:<P>#<名>` 视为同一端点）。

### 4.3 程序 actor 的 ABI

stdin：`{"channel", "me", "msgs": [{seq, from, to, body}…]}`。stdout 原样进 `step.out`，按行首 `>>> ` 拆：

```
>>> <addr>                                          消息，发给本 channel 的 <addr>（含门）
>>> channel.create <name>                           syscall；需 bind=syscall
>>> channel.add.actor <channel> <kind> [in] [bind=…]  syscall（含 actor.create）；后续各行是 text；需 bind=syscall
>>> <动词> <参数>                                   绑定了的 world 动词，一张表：spawn <dir>（按 loader 协议 Exec.spawn(init.py <dir> --serve)）、stop <pid>；需 bind=<动词>
```

返回以 msg 追加给调用者：`from=channel.create body="<name> new|exists"`；`from=channel.add.actor body=<channel>/<addr>`；`from=spawn body="<dir> pid=<n>"`。跨膜没有返回（根门单向）。

### 4.4 门与 Port

门 actor 的 text 是端点：`file:<dir>#<box>`（box = `_root` 或 channel 名），或本机器的 channel 名（= `file:<P>#<名>` 的缩写）。写给门的消息一律 `Port.send` 到该端点，署名 `file:<P>#<本 channel>`；本机目标也经自己的收件箱进账。

### 4.5 loader：init.py 与 P 的布局

P 的启动协议（world 的一部分，与 ω-bind、R 同版本遗传）：
```
P/omega.py runtime.py init.py     world 三文件（= G.world 逐字节）
P/G.json                          描述原样
P/h/<channel>.jsonl, h/_order     账本（运行时产生）
P/in/<box>.jsonl                  收件箱：_root 与各 channel（运行时产生）
P/spawn/<name>/                   子代的 P（C 的 pack 产生）
python init.py <P> [--serve]      起 R，折叠已有账本，驱动；--serve 静止后持续轮询收件箱。不读 G
```
不变量：`P.world == G.world`，对每个 f：`bytes(P/f) == G.world[f]`（T0）。

### 4.6 c0 的请求

| 请求（写给 realize） | 做什么 |
|---|---|
| `build <门> <创造者地址>\n<G>` | 经门发 syscall 造 G 的第一个 channel（c0）；最后放出生证明门；回 `built <门>` |
| `start\n<G>` | 出生：本地 syscall 长出 G 的其余 channel 与全部连线（发育）。正文为空则不动 |
| `add <channel> <kind> [in] [bind=…]\n<text>` | 本地：`channel.create`（幂等）+ `channel.add.actor` |
| `peer <a> <b>` | 本地两扇门 |
| `spawn <name>`（写给 C） | pack → spawn → 放两扇门（根门、c0）→ `build` 交给 realize → `msg c0\nstart\n<G>` |

### 4.7 实现状态（2026-08-29，晚）

按 §2.2.5 / M1 / DESIGN.md §5 的版本重写：`runtime.py`（转移表三行 + syscall 两词 + Space 级根门，`root_open` 由账本派生；place 行带 `by`；消息正文逐字节保留）、`init.py`（R 的入口，不读 G）、`actors/realize.py`（A：`build` 经门造整台机器，`add / peer` 本地生长）、`actors/spawn.py`（C：pack → spawn → 两扇门 → 把 G 交给 realize → `msg c0 start`）、`genesis.py`（人侧的 A + B：dalek0 由人经根门用同一套 syscall 造）。T1–T7 绿：T4 验证构造期间机械不动、start 关门、关门后根门无效；T7 验证子代全部 place 行 `by=_root`、出生证明指回父代、第一条消息署名出生证明门、父代对象销毁后子代已切离且活着。

仍开着的洞（DESIGN.md §3）：H3（外来请求只到接待员，`spawn` 只能由 c0 内部发给 C）、H4（无 c1，pack 抄 P 里的 G）、H5/H7（C actor 用文件系统 pack；程序 actor cwd = P）、H6（收件箱偏移在进程内存）、H9（oracle 端点）、H10（崩溃语义）。H1、H2、H15 已由本版消除。

2026-08-30 凌晨收缩 R（规则 16 → 9，不动理论）：world 动词一张表（spawn、stop）而非逐个分支；门只有一条规则（一律 `Port.send`，本机 channel 名是端点缩写）；收件箱读取归 Ω（`Port.recv`），根门与 channel 收件箱走同一条入口；`channel.create` 返回 new|exists。T1–T7 仍绿。

2026-08-30 凌晨改为发育版（2.2.5）：父代的 A 经门只造 c0 + 出生证明；C 发 `start\n<G>`；子代的 realize 收到后本地长出其余（T3、T4、T7 验证 `by=_root` / `by=1` 两只手）。T0–T7 绿。
