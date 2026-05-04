"""ctypes-backed Veil bindings.

The shared library is searched for in this order:

1. The ``VEIL_LIBRARY`` environment variable, if set, must be the
   absolute path to ``libveil.{so,dylib,dll}``.
2. The directory containing the Python script that imported
   ``veil``.
3. The current working directory.
4. The platform's standard search path (``LD_LIBRARY_PATH`` etc).
"""

from __future__ import annotations

import ctypes
import enum
import json
import os
import sys
import threading
from dataclasses import dataclass, field
from pathlib import Path
from typing import Any, Callable, Optional


class LibCode(enum.IntEnum):
    OK = 0
    INVALID_CONFIG = -1
    TRANSPORT_FAILED = -2
    AUTH_FAILED = -3
    NOT_RUNNING = -4
    ALREADY_RUNNING = -5
    BAD_HANDLE = -6
    INTERNAL = -99


class EventType(enum.IntEnum):
    CONNECTED = 1
    DISCONNECTED = 2
    ERROR = 3
    TRAFFIC = 4
    TRANSPORT_SWITCH = 5


@dataclass
class Event:
    type: int = 0
    message: str = ""
    transport: str = ""
    remote: str = ""
    bytes_tx: int = 0
    bytes_rx: int = 0

    def typed(self) -> Optional[EventType]:
        try:
            return EventType(self.type)
        except ValueError:
            return None


@dataclass
class Metrics:
    running: bool
    bytes_tx: int = 0
    bytes_rx: int = 0


@dataclass
class Version:
    version: str = ""
    commit: str = ""
    date: str = ""


class VeilError(Exception):
    """Raised on libveil failures."""

    def __init__(self, code: int, message: str = "") -> None:
        self.code = code
        try:
            self.lib_code: Optional[LibCode] = LibCode(code)
            label = self.lib_code.name
        except ValueError:
            self.lib_code = None
            label = f"code={code}"
        super().__init__(message or label)


# --- ctypes prototypes -----------------------------------------------------


# Callback signature: void (int kind, const char* json, void* user)
_EVENT_CB = ctypes.CFUNCTYPE(
    None, ctypes.c_int, ctypes.c_char_p, ctypes.c_void_p
)


def _candidate_lib_paths() -> list[Path]:
    out: list[Path] = []
    if env := os.environ.get("VEIL_LIBRARY"):
        out.append(Path(env))
    out.append(Path.cwd())
    if sys.argv and sys.argv[0]:
        out.append(Path(sys.argv[0]).resolve().parent)
    return out


def _platform_filenames() -> list[str]:
    if sys.platform.startswith("win"):
        return ["veil.dll", "libveil.dll"]
    if sys.platform == "darwin":
        return ["libveil.dylib"]
    return ["libveil.so"]


def _load() -> ctypes.CDLL:
    last_exc: Exception | None = None
    for base in _candidate_lib_paths():
        if base.is_file():
            try:
                return ctypes.CDLL(str(base))
            except OSError as exc:
                last_exc = exc
                continue
        for name in _platform_filenames():
            candidate = base / name
            if candidate.is_file():
                try:
                    return ctypes.CDLL(str(candidate))
                except OSError as exc:
                    last_exc = exc
    # Fall back to the platform default search path.
    for name in _platform_filenames():
        try:
            return ctypes.CDLL(name)
        except OSError as exc:
            last_exc = exc
    raise VeilError(
        LibCode.INTERNAL.value,
        f"could not locate libveil: {last_exc}",
    )


_lib = _load()

_lib.veil_create.restype = ctypes.c_uint64
_lib.veil_create.argtypes = [ctypes.c_char_p]

_lib.veil_start.restype = ctypes.c_int
_lib.veil_start.argtypes = [ctypes.c_uint64, _EVENT_CB, ctypes.c_void_p]

_lib.veil_stop.restype = ctypes.c_int
_lib.veil_stop.argtypes = [ctypes.c_uint64]

_lib.veil_destroy.restype = None
_lib.veil_destroy.argtypes = [ctypes.c_uint64]

_lib.veil_get_metrics.restype = ctypes.c_void_p
_lib.veil_get_metrics.argtypes = [ctypes.c_uint64]

_lib.veil_version_string.restype = ctypes.c_void_p
_lib.veil_version_string.argtypes = []

_lib.veil_free_string.restype = None
_lib.veil_free_string.argtypes = [ctypes.c_void_p]


def _take_string(ptr: int) -> str:
    if not ptr:
        raise VeilError(LibCode.INTERNAL.value, "null string from libveil")
    try:
        out = ctypes.c_char_p(ptr).value or b""
        return out.decode("utf-8", errors="replace")
    finally:
        _lib.veil_free_string(ptr)


def library_version() -> Version:
    """Read the loaded libveil version metadata."""
    raw = _lib.veil_version_string()
    payload = _take_string(raw)
    data: dict[str, Any] = json.loads(payload)
    return Version(
        version=data.get("version", ""),
        commit=data.get("commit", ""),
        date=data.get("date", ""),
    )


# --- public API ------------------------------------------------------------


class Veil:
    """A live Veil client instance.

    ``cfg_text`` is a JSON or YAML configuration string (auto-detected
    by leading character).
    """

    def __init__(self, cfg_text: str) -> None:
        if not isinstance(cfg_text, str):
            raise TypeError("cfg_text must be str")
        handle = _lib.veil_create(cfg_text.encode("utf-8"))
        if handle == 0:
            raise VeilError(LibCode.INVALID_CONFIG.value, "invalid configuration")
        self._handle = handle
        self._on_event: Optional[Callable[[Event], None]] = None
        self._cb_holder = None  # keeps ctypes callback alive
        self._lock = threading.Lock()

    def start(self, on_event: Optional[Callable[[Event], None]] = None) -> None:
        """Bring the client up.

        ``on_event`` (optional) is invoked from a Veil-internal thread
        for every runtime event. Don't block in the callback; copy
        the payload and dispatch into the application's own runtime.
        """
        with self._lock:
            self._on_event = on_event

            def trampoline(kind: int, json_ptr: int, user: int) -> None:
                handler = self._on_event
                if handler is None:
                    return
                payload = ""
                if json_ptr:
                    payload = ctypes.c_char_p(json_ptr).value.decode(
                        "utf-8", errors="replace"
                    )
                event = _parse_event(kind, payload)
                try:
                    handler(event)
                except Exception:  # noqa: BLE001
                    # Never let a user callback unwind through CFFI.
                    pass

            cb = _EVENT_CB(trampoline)
            self._cb_holder = cb  # keep ref alive

            rc = _lib.veil_start(self._handle, cb, None)
            if rc != 0:
                self._on_event = None
                self._cb_holder = None
                raise VeilError(rc, "veil_start failed")

    def stop(self) -> None:
        rc = _lib.veil_stop(self._handle)
        if rc != 0:
            raise VeilError(rc, "veil_stop failed")

    def metrics(self) -> Metrics:
        raw = _lib.veil_get_metrics(self._handle)
        payload = _take_string(raw)
        data: dict[str, Any] = json.loads(payload)
        return Metrics(
            running=bool(data.get("running", False)),
            bytes_tx=int(data.get("bytes_tx", 0)),
            bytes_rx=int(data.get("bytes_rx", 0)),
        )

    def close(self) -> None:
        if self._handle:
            _lib.veil_destroy(self._handle)
            self._handle = 0
            self._cb_holder = None
            self._on_event = None

    # Context manager + finalizer ------------------------------------

    def __enter__(self) -> "Veil":
        return self

    def __exit__(self, exc_type, exc, tb) -> None:
        self.close()

    def __del__(self) -> None:
        try:
            self.close()
        except Exception:  # noqa: BLE001
            pass


def _parse_event(kind: int, payload: str) -> Event:
    data: dict[str, Any] = {}
    if payload:
        try:
            data = json.loads(payload) or {}
        except json.JSONDecodeError:
            data = {}
    return Event(
        type=int(data.get("type", kind) or kind),
        message=str(data.get("message", "")),
        transport=str(data.get("transport", "")),
        remote=str(data.get("remote", "")),
        bytes_tx=int(data.get("bytes_tx", 0)),
        bytes_rx=int(data.get("bytes_rx", 0)),
    )
