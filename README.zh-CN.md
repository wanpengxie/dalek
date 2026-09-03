# Dalek

**一台构造性 Agent 机器**

[English](README.md) · [英文论文（PDF）](Dalek.pdf) · [中文译稿（PDF）](Dalek.zh-CN.pdf)

Dalek 是一个小型、可运行的研究原型。它不把 Agent 仅仅看成“一个语言模型加上一圈工具”，而是把它构造成一台拥有自身边界、蓝图、历史和构造过程的机器。

很多 Agent 系统已经能够生成代码或加载新工具，但外围 harness 仍在替 Agent 决定：这段代码是否已经成为它的一部分，重启后是否仍然存在，复制出来的后代是否会继承它。Dalek 把这些决定收进同一套明确的机器结构里。一项能力可以作为文本被写出、检查、安装成成员、记入历史、在重启后恢复，并通过同一条构造路径传给后代。

Dalek **不是**冯诺依曼 1948 年自复制自动机的一种新实现。它是一台新的 Agent 机器：冯诺依曼的自复制构造只是其中的遗传内核，Dalek 在此基础上加入了长期 Agent 所需的边界、身份、历史和生长机制。

## “构造性”是什么意思

这里的“构造性”不是“有帮助”，而是说：机器由有限种零件和明确的构造、变化规则定义。Dalek 明确给出四件事：

1. 机器与宿主之间的边界；
2. 生成合法机器形态的构造语言；
3. 可以改变机器构成的合法转移；
4. 构造规则如何进入遗传。

于是，生命周期中的每个“自”都有一个确定的主语：被维护、发生改变、产生后代并与其他机器组织起来的，始终是同一台可以辨认的机器。

## 一张图看懂机器

```text
宿主合同 Ω：执行 · 存储 · 通信
└── Space：一个 Dalek 个体
    ├── R     内容盲的运行时与构成法律
    ├── G     静态描述：机器应当复制什么
    ├── H     只追加账本：这个个体经历过什么
    ├── c0    构造器、复印器与控制器
    ├── c1    登记处，由历史生成下一份 G
    └── c2    能力制造器 D = L + U
              L 写出候选能力
              U 编译并检查候选
```

介质只有三种基本元素：

- **actor**：可以被调用的行为零件；
- **message**：所有相互作用的统一形式；
- **channel**：容纳 actor 与账本的局部组织边界。

一个 **Space** 才是可切离、可启动的完整机器：若干 channel 加上一份运行时。程序、语言模型、工具、人和外部系统，都可以通过同一种 actor/message 接口与机器相遇。

一个个体的身份是 `(G, H)`：两台机器可以继承完全相同的描述 `G`，但各自从一本新的历史 `H` 开始，因此是两个不同的个体。

## 这个原型展示了什么

- **自维护**：器官损坏或丢失后，可以沿机器自己的构造路径替换或重建。
- **自进化**：`L` 写候选，`U` 检查；被接受的能力文本会成为正式成员，并进入遗传。
- **自复制**：子代从 `G` 被构造出来，开启自己的 `H`，并能继续繁殖。
- **自组织**：多台独立机器依靠遗传的器官发现彼此、建立拓扑，并唤醒失效的同伴。

这些是机制主张，不是开放式自主策略的主张。实验所证明的是：这些路径确实存在，而且构成变化的每一步都能在账本中找到。

## 快速开始

参考实现依赖 Linux、Python 3 和 Python 标准库。

运行确定性的机制测试：

```bash
cd src
python3 t/test_c0.py
```

当前共 33 项测试，覆盖构造、门、登记、遗传、重启、修复、繁殖、自组织，以及跨世代更换运行时。

在临时目录中启动一台新机器；这个例子不会调用远端模型：

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

持续运行一台已有的机器：

```bash
python3 init.py /path/to/a/machine --serve
```

真模型实验的说明见[论文](Dalek.pdf)第 4 节，原始账本保存在 [`src/runs/`](src/runs/) 中。复现实验需要配置 [`src/actors/l.py`](src/actors/l.py) 使用的端点、模型与凭据，重新生成 `G.json`，再构造一台新机器。不要把凭据提交进仓库或写进要遗传的基因组。

## 仓库结构

| 路径 | 作用 |
|---|---|
| [`src/runtime.py`](src/runtime.py) | 运行时 `R`：折叠、调度、调用、门与构成转移 |
| [`src/omega.py`](src/omega.py) | 宿主合同 `Ω` 的参考绑定：执行、存储与端口 |
| [`src/genesis.py`](src/genesis.py) | 第一台机器创世时位于人一侧的构造器与复印器 |
| [`src/G.json`](src/G.json) | 原型机器的标准描述 |
| [`src/actors/`](src/actors/) | 机器各个器官的源码文本 |
| [`src/t/test_c0.py`](src/t/test_c0.py) | 确定性的机制测试 |
| [`src/runs/`](src/runs/) | 真模型实验留下的原始账本与驱动脚本 |
| [`Dalek.pdf`](Dalek.pdf) | 权威英文论文 |
| [`Dalek.zh-CN.pdf`](Dalek.zh-CN.pdf) | 供阅读与讨论的中文译稿 |

## 范围与安全说明

这是一个**研究原型**，不是生产运行时，也不是安全沙箱。

论文中的“封闭”是说：相对于一个明确的宿主合同，成员、消息、构成变化和遗传都只有定义好的组织路径。它不表示 Python 代码已经被物理隔离。参考运行时会用 Python `exec` 执行 actor 文本；actor 可以访问当前进程有权使用的文件、网络和其他宿主资源。基于文件的端口同样只是演示介质，不是带身份认证的通信设施。

请勿运行不可信的 actor 代码，也不要把该原型直接暴露为公共服务。生产实现需要加入真正的进程隔离、端口认证、资源控制、秘密管理，以及经过加固的 `Ω` 绑定。

## 论文

完整的论证、模型、机器定义、账本走读、讨论与相关工作见：

> Wanpeng Xie. **Dalek: A Constructive Agent Machine — Self-Maintenance, Self-Evolution, Self-Reproduction, and Self-Organization by Construction.**

英文手稿是权威版本；中文版本是与之同步、供阅读和讨论使用的译稿。

[下载英文论文](Dalek.pdf) · [下载中文译稿](Dalek.zh-CN.pdf)
