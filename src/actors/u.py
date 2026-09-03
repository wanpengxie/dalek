# c2 的 U：执行器。这段源码是 G 里的 text；R 放入时 exec 一次得到常驻的 run(m)。
# 用和 R 同一个 exec 跑 L 写的代码——机器里的编译器和机器的物理是同一个解释器。候选在进程内被实例化成活函数，拿到真的 call：
#   run\n<代码>                     exec 代码（stdout 收下来）
#   test\n<代码>\n===\n<测试>        exec 代码得到它的 run，再 exec 测试；测试的命名空间里有 run（候选的函数）、candidate（候选的全部名字）、call
# 返回 result <退出码>\n<输出>（输出每行缩进两格）。退出码 0 = 没有异常。
import io, contextlib, traceback


def run(m):
    op, _, rest = m["body"].partition("\n")
    if op not in ("run", "test"):
        return
    code, _, tests = rest.partition("\n===\n")
    buf, rc = io.StringIO(), 0
    try:
        with contextlib.redirect_stdout(buf):
            ns = {"call": call, "me": me, "channel": channel}
            exec(compile(code, "<candidate>", "exec"), ns)
            if op == "test":
                exec(compile(tests, "<tests>", "exec"), {"call": call, "run": ns.get("run"), "candidate": ns})
    except (Exception, SystemExit):
        rc = 1; buf.write(traceback.format_exc(limit=3))
    out = buf.getvalue()
    return f"result {rc}\n" + "\n".join("  " + l for l in out[-4000:].splitlines())
