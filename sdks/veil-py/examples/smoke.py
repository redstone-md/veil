#!/usr/bin/env python3
"""Minimal end-to-end smoke test for the Veil Python bindings.

Build libveil first:

    cd ../../core
    CGO_ENABLED=1 go build -buildmode=c-shared \
        -o ../sdks/veil-py/libveil.so ./pkg/cgo

Then:

    python examples/smoke.py path/to/client.yaml
"""

from __future__ import annotations

import sys
import time

import veil


def main() -> int:
    if len(sys.argv) < 2:
        print(f"usage: {sys.argv[0]} <path/to/client.yaml-or-json>")
        return 2

    print("library version:", veil.library_version())

    cfg_text = open(sys.argv[1], "r", encoding="utf-8").read()

    with veil.Veil(cfg_text) as v:
        v.start(on_event=lambda e: print(
            f"[event] kind={e.typed()!r} transport={e.transport} "
            f"remote={e.remote} tx={e.bytes_tx} rx={e.bytes_rx} msg={e.message}"
        ))
        for _ in range(10):
            time.sleep(1)
            print("metrics:", v.metrics())
        v.stop()
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
