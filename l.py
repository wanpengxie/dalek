"""L：裸 completion 端点。文本进、文本出，无工具、无角色结构。

环境变量：
  DALEK_L_URL    端点，如 http://localhost:8080/v1/completions（OpenAI 兼容）
  DALEK_L_MODEL  模型名（可选）
  DALEK_L_CHAT=1 端点是 chat 接口时打开：整段 prompt 作为一条 user 消息发出——工程近似，须注明
  DALEK_L_KEY    Bearer（可选）
未测试：本机无端点。
"""
from __future__ import annotations
import json, os, urllib.request
import kernel as K

def complete(text: str) -> str:
    url = os.environ["DALEK_L_URL"]
    chat = os.environ.get("DALEK_L_CHAT") == "1"
    body = {"model": os.environ.get("DALEK_L_MODEL", ""), "max_tokens": 2048, "temperature": 0.7}
    if chat: body["messages"] = [{"role": "user", "content": text}]
    else:    body["prompt"] = text
    req = urllib.request.Request(url, data=json.dumps(body).encode(), headers={"Content-Type": "application/json"})
    if k := os.environ.get("DALEK_L_KEY"): req.add_header("Authorization", f"Bearer {k}")
    with urllib.request.urlopen(req, timeout=120) as r:
        d = json.load(r)
    ch = d["choices"][0]
    return ch["message"]["content"] if chat else ch["text"]

GRAMMAR = """输出格式（严格）：每条消息以一行 `>>> <地址>` 开头，其后为正文；正文第一行若为 `#decl U` 则其余行是 python 源码，若为 `#decl M` 则其余行是 part/in/start。
第一个 `>>> ` 之前的文字会被丢弃。你的地址在 view 里以 `-> <地址>` 出现。"""

def L(sp: K.Space, a: K.Addr, view: str) -> str:
    return complete(f"{a.prefix}\n\n{GRAMMAR}\n\n[view]\n{view}\n\n[out]\n")
