"""Veil VPN — Python bindings for libveil.

This package wraps libveil (the C-shared library produced from
the Go reference implementation) using ctypes. The ABI matches
``core/pkg/cgo/include/veil.h`` v1.

Typical usage::

    import veil

    cfg = open("client.yaml").read()
    with veil.Veil(cfg) as v:
        v.start(on_event=lambda e: print(e))
        time.sleep(60)
        print(v.metrics())

The Veil context manager handles ``destroy`` on exit; ``with`` is
the recommended call style.
"""

from .core import (
    Event,
    EventType,
    LibCode,
    Metrics,
    Veil,
    VeilError,
    Version,
    library_version,
)

__all__ = [
    "Event",
    "EventType",
    "LibCode",
    "Metrics",
    "Veil",
    "VeilError",
    "Version",
    "library_version",
]

__version__ = "0.0.1"
