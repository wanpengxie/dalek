# c2 的 U：执行器。这段源码是 G 里的 text。用宿主的 python 跑 L 写的代码——机器里的编译器和机器的物理是同一个解释器。
#   run\n<代码>                     跑代码
#   test\n<代码>\n===\n<测试>        代码写成 m.py，测试在同目录跑（可 import m / subprocess 跑 m.py）
# 回 result <退出码>\n<输出>（输出每行缩进两格，免得里面的 ">>> " 被当成动作）。超时 30 s → 退出码 -1。
import sys, json, os, subprocess, tempfile
v = json.load(sys.stdin)
for m in v["msgs"]:
    op, _, rest = m["body"].partition("\n")
    if op not in ("run", "test"):
        continue
    code, _, tests = rest.partition("\n===\n")
    d = tempfile.mkdtemp(prefix="u-", dir=os.getcwd())
    with open(os.path.join(d, "m.py"), "w", encoding="utf-8") as f:
        f.write(code)
    try:
        r = subprocess.run([sys.executable, "-c", tests if op == "test" else code],
                           capture_output=True, text=True, cwd=d, timeout=30)
        rc, out = r.returncode, r.stdout + r.stderr
    except subprocess.TimeoutExpired:
        rc, out = -1, "timeout 30s"
    print(f">>> {m['from']}\nresult {rc}\n" + "\n".join("  " + l for l in out[-4000:].splitlines()))
