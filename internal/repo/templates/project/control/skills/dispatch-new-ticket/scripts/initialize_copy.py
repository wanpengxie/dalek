#!/usr/bin/env python3
"""
复制 dispatch initialize 阶段模板到目标目录。

约束：
1. 只复制，不做模板渲染与变量替换。
2. 若目标文件已存在，默认不覆盖（可通过 --overwrite 打开）。
"""

from __future__ import annotations

import argparse
import json
from pathlib import Path
from typing import Dict


def build_mapping() -> Dict[str, str]:
    return {
        "worker-agents.md.template": "AGENTS.md",
        "plan.md.template": "PLAN.md",
        "state.json.template": "state.json",
    }


def ensure_json_readable(path: Path) -> None:
    with path.open("r", encoding="utf-8") as f:
        json.load(f)


def copy_file(src: Path, dst: Path, overwrite: bool) -> bool:
    if dst.exists() and not overwrite:
        return False
    dst.parent.mkdir(parents=True, exist_ok=True)
    dst.write_bytes(src.read_bytes())
    return True


def parse_args() -> argparse.Namespace:
    parser = argparse.ArgumentParser(description="Copy dispatch initialize templates")
    parser.add_argument("--source-dir", required=True, help="assets 模板目录")
    parser.add_argument("--target-dir", required=True, help="目标 .dalek 目录")
    parser.add_argument("--overwrite", action="store_true", help="覆盖已存在目标文件")
    return parser.parse_args()


def main() -> int:
    args = parse_args()
    source_dir = Path(args.source_dir).expanduser().resolve()
    target_dir = Path(args.target_dir).expanduser().resolve()
    overwrite = bool(args.overwrite)

    if not source_dir.is_dir():
        raise SystemExit(f"source-dir 不存在或不是目录: {source_dir}")

    mapping = build_mapping()
    copied = []
    skipped = []
    for src_name, dst_name in mapping.items():
        src = source_dir / src_name
        if not src.is_file():
            raise SystemExit(f"模板文件不存在: {src}")
        dst = target_dir / dst_name
        wrote = copy_file(src, dst, overwrite=overwrite)
        if wrote:
            copied.append(str(dst))
        else:
            skipped.append(str(dst))

    # initialize 的最小健壮性检查：state.json 至少可解析
    ensure_json_readable(target_dir / "state.json")

    output = {
        "schema": "dalek.dispatch.initialize.copy.v1",
        "source_dir": str(source_dir),
        "target_dir": str(target_dir),
        "copied": copied,
        "skipped": skipped,
        "overwrite": overwrite,
    }
    print(json.dumps(output, ensure_ascii=False))
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
