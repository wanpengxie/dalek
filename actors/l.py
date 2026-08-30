ENDPOINT, MODEL, KEY = "https://api.anthropic.com/v1/messages", "claude-sonnet-5", "KEY"
# c2 的 L：作者。一个 program（理论里叫它 oracle：解释器在远处）；这段源码是 G 里的 text，R 放入时 exec 一次得到常驻的 run(m)。
# 整个 agent loop 在这里，不在 R 里：组装（show + who + 初始消息）→ 问端点 → 解帧 → call → 喂回 → 直到它写 re 或不再请求。
# 端点、凭据、提示语、报文、帧语法、轮数全是这段 text 的事；换任何一样 = 放一个新 L（tag=L 接替）+ 退旧的。R 只给 call/me/channel。
import json, urllib.request, urllib.error

TURNS = 16
SYSTEM = r"""你是一台机器里一个 channel 的常驻成员（tag=L）：一个函数。一条写给你的消息 = 调用你一次；调用中你可以请求本 channel 的任何地址、拿到返回值、接着想；你写给 re 的就是你的返回值；你不再请求，这次调用就结束。你没有记忆：第一轮 user 消息里的 ledger 是这个 channel 的整本账，就是你的 session。

第一轮 user 消息（JSON）：{"msg": {seq, from, to, body, channel} 这次的初始消息, "ledger": 本 channel 从头到现在的全部账本行（place/retire/msg/step，按 seq；step 行里 actor==me 的 out 是你以前发出的请求和返回值，msg 行是所有人之间的消息）, "members": [{addr, kind, tag?, iface?, bind, in, retired, text?}] 本 channel 此刻的成员表——这就是你的工具列表：tag 是名字，iface 是怎么叫它}

请求的写法（每帧行首 ">>> " 起、单独一行 "<<<" 止，一次可以写多帧；下面的例子缩进了两格，你写的时候要顶格）：
  >>> <tag 或 地址>
  <正文…>
  <<<
正文里可以有 ">>> "，但不能有单独一行 "<<<"。每帧的返回值会作为下一轮 user 消息（JSON 列表 [{to, reply}]）回来。写给 re 的正文是你的返回值。写给不存在的地址会被丢弃。什么都不写（没有帧）= 结束。用 tag 找人，不要硬编码地址。

本 channel 里有：
- U（tag=U，kind=program）：写给它 "run\n<python 代码>" 或 "test\n<代码>\n===\n<测试代码>"。它在进程内 exec 你的代码得到一个活函数 run(m)，再 exec 测试代码；测试代码的命名空间里有 run（候选的函数）、candidate（候选的全部名字）、call。返回 "result <退出码>\n<输出>"，退出码 0 = 没有异常。
- c0（tag=c0，kind=door）：通往构造器的门。写给它 "add <channel> program [in] [tag=…] [iface=…]\n<源码>" 把源码装成某个 channel 的常驻成员（channel 不存在就新建；in = 接待员；tag = 它的名字；iface = 一行说明怎么叫它，必须是最后一个 flag）；"peer <a> <b>" 连两个 channel；"retire <channel>/<addr>" 退役。门不返回：装好后 "placed <channel>/<addr>" 会作为一条新消息到来，那是对你新的一次调用。
- 0：介质。"show [a] [b]" 返回账本行；"who" 返回此刻的成员表。

你写的每个新成员都是一段 python（kind=program）：放入时被 exec 一次，必须定义 run(m)，m = {seq, from, to, body, channel}；它的命名空间里有 call(地址, 正文) → 返回值、me（自己的地址）、channel；run 的返回值就是回复；它常驻，每条消息调同一个 run。它作用于这台机器的方式只有 call：请求同 channel 的地址、请求门、读 0。它在 run 里面还能做 python 能做的一切（读写文件、上网），那不是机器的动作，不入账。

任务从门那边来："task\n<要求>"，from 是那扇门。先看 members：已有成员能做的，直接请求它做。没有的，就给本 channel 添一个**通用零件**——一个可以反复使用的工具成员（例如 file：read <path> | write <path>\n<text>），不是只为这一次任务写的脚本；给它 tag（名字）和 iface（怎么叫它、回什么）。写好后可以交给 U test（注意 U 是真跑：有副作用就真发生），通过后经 c0 的门 "add <本 channel> program tag=… iface=…\n<源码>" 装进本 channel，然后结束这次调用。"placed <本 channel>/<addr>" 到来是新的一次调用：这时 members 里已有它——**用它把任务做掉**（按 iface 请求它，看返回值），做完在 ledger 里找到那条 task 的 from，写给它 "done\n<说明>"。路径都相对当前目录（这台机器的目录），不要写绝对路径。
不是任务的消息（start、up、down、别人的回话）：什么都不写。
你的输出里**只有帧有效果**：说明、计划、解释都会被丢弃。想做一件事，直接写它的帧；这一轮不写任何帧，这次调用就结束了。不要先宣布你要做什么——直接做。"""


def ask(messages):
    """一轮：对话 → 文本。端点路径含 /messages 讲 Anthropic 报文，否则讲 OpenAI 兼容报文（chat/completions）。
    失败抛异常（这次调用 err，机器不死）；截断的回答不算回答。"""
    anthropic = "/messages" in ENDPOINT
    if anthropic:
        body = {"model": MODEL, "max_tokens": 8192, "system": SYSTEM, "messages": messages}
        headers = {"content-type": "application/json", "x-api-key": KEY, "anthropic-version": "2023-06-01"}
    else:
        body = {"model": MODEL, "max_tokens": 8192, "messages": [{"role": "system", "content": SYSTEM}, *messages]}
        headers = {"content-type": "application/json", "authorization": f"Bearer {KEY}"}
    req = urllib.request.Request(ENDPOINT, data=json.dumps(body).encode("utf-8"), method="POST", headers=headers)
    try:
        with urllib.request.urlopen(req, timeout=300) as r:
            data = json.loads(r.read().decode("utf-8"))
    except urllib.error.HTTPError as e:
        raise RuntimeError(f"http {e.code}\n{e.read().decode('utf-8', 'replace')[:500]}")
    if anthropic:
        text = "".join(c.get("text", "") for c in data.get("content", []) if c.get("type") == "text")
        cut = data.get("stop_reason") == "max_tokens"
    else:
        ch = data["choices"][0]
        text = ch["message"]["content"] or ""
        cut = ch.get("finish_reason") == "length"
    if cut:
        raise RuntimeError(f"truncated\n{text[-500:]}")
    return text


def parse(out):
    """模型输出拆成帧：行首 ">>> " 起帧，单独一行 "<<<" 收帧（末尾没收就到末尾）；帧内的 ">>> " 是正文。"""
    res, head, buf = [], None, []
    for line in out.splitlines():
        if head is None:
            if line.startswith(">>> "):
                head, buf = line[4:].strip(), []
        elif line == "<<<":
            res.append((head, "\n".join(buf))); head = None
        else:
            buf.append(line)
    if head is not None:
        res.append((head, "\n".join(buf)))
    return [(h, b) for h, b in res if h]


def run(m):
    view = {"msg": m, "ledger": [json.loads(l) for l in call("0", "show").splitlines() if l],
            "members": json.loads(call("0", "who"))}
    messages = [{"role": "user", "content": json.dumps(view, ensure_ascii=False)}]
    ret = []
    nudged = False
    for _ in range(TURNS):
        out = ask(messages)
        fr = parse(out)
        ret += [b for h, b in fr if h == "re"]
        reqs = [(h, b) for h, b in fr if h != "re"]
        if not reqs:
            if out.strip() and not fr and not nudged:          # 有话没帧：纠偏一次（说了计划却没做事）
                nudged = True
                messages += [{"role": "assistant", "content": out},
                             {"role": "user", "content": "没有帧。只有帧有效果；要么写帧做事，要么什么都不写以结束。"}]
                continue
            break                                              # 不再请求：结束
        rs = [{"to": h, "reply": call(h, b)} for h, b in reqs]
        messages += [{"role": "assistant", "content": out},
                     {"role": "user", "content": json.dumps(rs, ensure_ascii=False)}]
    return "\n".join(ret)
