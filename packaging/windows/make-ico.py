#!/usr/bin/env python3
"""Build a multi-size Windows .ico from the NextSQL PNG icons."""

from __future__ import annotations

import argparse
import sys
from pathlib import Path

from PIL import Image


def main() -> int:
    p = argparse.ArgumentParser()
    p.add_argument("--src", required=True, help="source PNG (any square size)")
    p.add_argument("--out", required=True, help="output .ico path")
    args = p.parse_args()
    src = Path(args.src)
    out = Path(args.out)
    if not src.is_file():
        print(f"missing {src}", file=sys.stderr)
        return 1
    im = Image.open(src).convert("RGBA")
    sizes = [(16, 16), (32, 32), (48, 48), (64, 64), (128, 128), (256, 256)]
    out.parent.mkdir(parents=True, exist_ok=True)
    im.save(out, format="ICO", sizes=sizes)
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
