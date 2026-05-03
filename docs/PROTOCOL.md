# Veil Wire Protocol — VWP/1

**Specification version:** 0.1.0 (Draft)
**Status:** Pre-alpha. Subject to breaking change without notice
until a `1.0` revision is released.

This document specifies the wire format and operational semantics
of the Veil Wire Protocol, version 1 (VWP/1). It is the source of
truth for protocol implementers.

This is intentionally written in an RFC-influenced style so that
the protocol can be implemented from this document alone, without
reference to the Veil source code.

---

## 1. Conventions

The keywords MUST, MUST NOT, REQUIRED, SHALL, SHALL NOT, SHOULD,
SHOULD NOT, RECOMMENDED, MAY, and OPTIONAL in this document are
to be interpreted as described in
[BCP 14](https://www.rfc-editor.org/info/bcp14)
([RFC 2119](https://www.rfc-editor.org/info/rfc2119),
[RFC 8174](https://www.rfc-editor.org/info/rfc8174))
when, and only when, they appear in all capitals.

Numeric values are network byte order (big-endian) unless stated
otherwise.

Field-size notation in tables is given in **octets** (8-bit bytes).

---

## 2. Layered model

VWP/1 is organised in five conceptual layers:

```
+------------------------------------------------+
| 5. Application data (consumer of the tunnel)   |
+------------------------------------------------+
| 4. Session layer    (multiplex, flow control)  |
+------------------------------------------------+
| 3. Cryptographic layer (Noise XK + AEAD)       |
+------------------------------------------------+
| 2. Mimicry layer    (timing, padding, decoy)   |
+------------------------------------------------+
| 1. Transport adapter (QUIC | TLS-Reality |     |
|                       WSS | HTTP/3 MASQUE)     |
+------------------------------------------------+
```

A complete VWP/1 implementation MUST implement all layers.
Implementations MAY support a subset of the transport adapters
in layer 1, but MUST support **at least the QUIC adapter** to be
considered conformant for the v1 baseline.

---

## 3. Cryptographic layer

### 3.1 Handshake

VWP/1 uses the **Noise XK** handshake pattern from the
[Noise Protocol Framework](http://www.noiseprotocol.org/noise.html),
with the following selection:

```
Noise_XK_25519_ChaChaPoly_BLAKE2s
```

That is:
- DH function: X25519
- Cipher: ChaCha20-Poly1305 (RFC 8439)
- Hash: BLAKE2s

The server's static public key is known to the client out of band
(distributed in the user configuration). The client's static key
is transmitted to the server during the handshake (third Noise
message). Per Noise XK, the responder is authenticated at message 1
and the initiator is authenticated by message 3.

### 3.2 Prologue

Both parties MUST seed the Noise handshake with a `prologue` value:

```
prologue = "VWP/1" || protocol_id_octet
```

where `protocol_id_octet` is `0x01` for v1 of this protocol.

This binds the handshake to the protocol version and prevents
cross-version confusion attacks.

### 3.3 Session keys

The handshake produces two `CipherState` objects per Noise convention:
one for client → server, one for server → client. Each direction
maintains its own AEAD nonce counter, starting at `0` and incremented
by `1` for each message sent in that direction.

### 3.4 Key rotation

A session MUST rotate keys after the **earlier** of:
- 1 GiB of plaintext encrypted in either direction; or
- 60 minutes of wall-clock session time.

Rotation is performed by issuing a `CONTROL/REKEY` frame
(see §5.3). Both parties derive new ephemeral DH keys, perform
a sub-handshake, and switch to the new `CipherState` instances.

Implementations MUST NOT exceed 2^60 messages in either direction
without rotation, regardless of the time/bytes thresholds.

### 3.5 Anti-replay

Each AEAD message includes its 64-bit counter as the nonce.
The receiver maintains a sliding window of size **1024** messages.

A message is REJECTED if:
- Its counter is more than 1024 below the highest counter seen, OR
- Its counter has already been received within the current window.

A rejected message MUST NOT be processed and MUST be logged at
WARN level. Repeated rejections from the same peer SHOULD trigger
a connection reset.

### 3.6 Forward secrecy

Each rotation generates a fresh ephemeral DH pair. After rotation,
the previous session keys MUST be securely zeroed from memory.

---

## 4. Frame format

All session-layer payloads are exchanged as **frames**.

```
 0                   1                   2                   3
 0 1 2 3 4 5 6 7 8 9 0 1 2 3 4 5 6 7 8 9 0 1 2 3 4 5 6 7 8 9 0 1
+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+
|     Type      |     Flags     |          Reserved             |
+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+
|                          Stream ID                            |
+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+
|        Payload Length         |         Padding Length        |
+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+
|                                                               |
~                          Payload                              ~
|                                                               |
+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+
|                                                               |
~                          Padding                              ~
|                                                               |
+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+
```

Field definitions:

| Field          | Size (octets) | Description                                           |
|----------------|---------------|-------------------------------------------------------|
| Type           | 1             | Frame type, see §5                                    |
| Flags          | 1             | Frame flags, see §4.1                                 |
| Reserved       | 2             | MUST be zero on send, MUST be ignored on receive      |
| Stream ID      | 4             | Stream identifier (see §6). Frames not associated with a stream use Stream ID `0` |
| Payload Length | 2             | Length of the Payload field in octets, 0 to 16383     |
| Padding Length | 2             | Length of the Padding field in octets, 0 to 16383     |
| Payload        | variable      | Frame-type-specific data                              |
| Padding        | variable      | Random or PRNG-derived bytes; MUST be ignored on receive |

A complete frame MUST NOT exceed 32 KiB after AEAD overhead.

### 4.1 Flags

| Bit | Name           | Meaning                                                       |
|-----|----------------|---------------------------------------------------------------|
| 0   | END_STREAM     | This frame closes its stream (only valid for STREAM_DATA)     |
| 1   | COMPRESSED     | Payload is zstd-compressed (per-stream opt-in)                |
| 2–7 | Reserved       | MUST be zero on send, MUST be ignored on receive              |

---

## 5. Frame types

| Code   | Name              | Stream-scoped? | Description                              |
|--------|-------------------|:--------------:|------------------------------------------|
| `0x01` | STREAM_DATA       | yes            | Application payload for a stream         |
| `0x02` | STREAM_OPEN       | yes            | Open a new stream                        |
| `0x03` | STREAM_CLOSE      | yes            | Close a stream                           |
| `0x04` | PING              | no             | Liveness probe                           |
| `0x05` | PONG              | no             | Response to PING                         |
| `0x06` | WINDOW_UPDATE     | yes            | Adjust per-stream flow-control window    |
| `0x07` | CONTROL           | no             | Control-plane operations (see §5.3)      |
| `0xFF` | PADDING_ONLY      | no             | Carries no payload; mimicry filler       |

Frame codes not listed MUST cause the receiver to terminate the
connection with `ERR_PROTOCOL_VIOLATION`.

### 5.1 STREAM_OPEN

Payload format:

| Field           | Size | Description                          |
|-----------------|------|--------------------------------------|
| Stream Type     | 1    | `0x01` = TCP-like reliable, `0x02` = datagram (future) |
| Initial Window  | 4    | Initial receive window in bytes      |
| Metadata Len    | 2    | Length of the metadata blob          |
| Metadata        | var  | Stream-type-specific (e.g. SOCKS5-equivalent target address for `0x01`) |

For `Stream Type = 0x01`, the metadata blob is:

| Field        | Size | Description                          |
|--------------|------|--------------------------------------|
| Address Type | 1    | `0x01`=IPv4, `0x02`=IPv6, `0x03`=domain |
| Address      | var  | 4 octets, 16 octets, or 1 octet length + domain  |
| Port         | 2    | Destination port                     |

### 5.2 PING / PONG

Both frames carry an 8-octet opaque token in the payload. The PONG
MUST echo the token from the corresponding PING.

PING SHOULD be sent at intervals jittered around 30 seconds when
the connection is otherwise idle.

### 5.3 CONTROL

Payload format:

| Field        | Size | Description |
|--------------|------|-------------|
| Op Code      | 1    | Control op (see below) |
| Op Payload   | var  | Op-specific data |

| Op Code | Name              | Description                                  |
|---------|-------------------|----------------------------------------------|
| `0x01`  | CAPABILITIES      | Negotiate supported features (see §7)        |
| `0x02`  | REKEY             | Trigger a key rotation                       |
| `0x03`  | DISCONNECT        | Graceful close with reason                   |
| `0x04`  | SERVER_CONFIG_HINT| Server-side hints (e.g. recommended SNI list seed) |

### 5.4 PADDING_ONLY

Carries no payload (`Payload Length = 0`). The Padding field MAY be
of any length up to the frame size limit. Receivers MUST silently
ignore the contents.

---

## 6. Session layer

### 6.1 Stream identifiers

Stream IDs are 32-bit unsigned integers. ID `0` is reserved for
session-level frames (PING, CONTROL, etc.).

- IDs initiated by the client are **odd**: 1, 3, 5, …
- IDs initiated by the server are **even, non-zero**: 2, 4, 6, …
- Each side maintains a monotonically increasing counter for its
  next-ID-to-issue.
- IDs MUST NOT be reused within a session.

### 6.2 Stream lifecycle

```
                      STREAM_OPEN
          IDLE  --------------------->  OPEN
                      STREAM_DATA*
                      WINDOW_UPDATE*
                      STREAM_DATA*
          OPEN  -----------+
            |              |
  END_STREAM|              |STREAM_CLOSE
            v              v
   HALF_CLOSED         CLOSED
            |
            |STREAM_CLOSE
            v
        CLOSED
```

Either side MAY send STREAM_CLOSE at any time, which transitions
the stream to CLOSED for both sides.

### 6.3 Flow control

Each stream has independent receive and send windows.

- **Initial window:** advertised in `STREAM_OPEN` (typically 256 KiB).
- **WINDOW_UPDATE** payload: 4-octet unsigned integer, the number of
  bytes by which the sender's send window MAY be increased.
- A sender MUST NOT transmit more bytes on a stream than its current
  send window permits.
- Implementations SHOULD send WINDOW_UPDATE frames to refill the
  window before it is fully exhausted to avoid stalling.

A session-level window also exists, advertised in CAPABILITIES.

### 6.4 Multiplexing

Many streams share a single VWP/1 session. Frame interleaving is
at the implementer's discretion, subject to flow-control constraints.
Implementations SHOULD interleave to avoid head-of-line blocking
across logical flows.

When the underlying transport is QUIC, the session SHOULD map each
VWP/1 stream onto a QUIC stream, deferring multiplexing to QUIC.
When the underlying transport is TCP-derived (TLS-Reality, WSS),
the session MUST implement multiplexing internally.

---

## 7. Capability negotiation

The first CONTROL frame in each direction MUST be a CAPABILITIES op.
Each side advertises:

```
CAPABILITIES payload:

  uint8   protocol_version_major
  uint8   protocol_version_minor
  uint16  supported_transports_bitfield
  uint16  supported_ciphers_bitfield
  uint32  session_window_initial
  uint16  feature_flags_bitfield
  uint8   mimicry_profile_count
  [for each profile:]
    uint8  profile_id
```

`supported_transports_bitfield`:
| Bit | Transport         |
|-----|-------------------|
| 0   | QUIC              |
| 1   | TLS-Reality       |
| 2   | WebSocket-over-TLS |
| 3   | HTTP/3 MASQUE     |

`feature_flags_bitfield`:
| Bit | Feature           |
|-----|-------------------|
| 0   | Decoy traffic     |
| 1   | Statistical mimicry |
| 2   | PQC hybrid keyex  |
| 3   | Datagram streams  |

Unknown bits MUST be ignored. Both peers operate using the
intersection of their advertised capabilities.

---

## 8. Mimicry layer

The mimicry layer applies between the cryptographic and transport
layers. It transforms a stream of cipher-text frames into a wire
sequence whose statistical properties match a chosen reference
profile.

A **mimicry profile** is a tuple of distributions:

- **Packet size distribution** `P_size`
- **Inter-arrival time distribution** `P_iat`
- **Burst length distribution** `P_burst`

Profiles are identified by a single octet. The following are reserved:

| Profile ID | Description                          |
|------------|--------------------------------------|
| `0x00`     | None (raw, no mimicry)               |
| `0x01`     | Generic browser browsing             |
| `0x02`     | Video streaming (480p)               |
| `0x03`     | Short-form social media scroll       |
| `0x04`     | Messaging (Telegram-style idle)      |
| `0x05`     | Search-and-browse                    |
| `0x06`–`0x7F` | Reserved for future standard profiles |
| `0x80`–`0xFF` | Implementation-defined / experimental |

The selected profile is exchanged via CAPABILITIES (or via a
SERVER_CONFIG_HINT after capability negotiation).

To meet the chosen profile, the implementation:

1. Samples target packet sizes from `P_size` and pads the next outgoing
   AEAD message to that size using PADDING_ONLY frames or
   per-frame Padding fields.
2. Samples inter-arrival times from `P_iat` and delays sends to match.
3. When a real-traffic burst is shorter than `P_burst` would expect,
   emits PADDING_ONLY frames to extend it.

The mimicry layer adds **latency** and **bandwidth overhead**.
Implementations MUST allow the user to disable mimicry (`profile = 0x00`)
when not needed.

---

## 9. Transport adapters

### 9.1 QUIC adapter

- Underlying: UDP. ALPN: `h3`.
- TLS layer uses uTLS to mimic a recent Chrome or Firefox fingerprint.
- 0-RTT MUST be disabled by default.
- Each VWP/1 stream maps to one QUIC stream.

### 9.2 TLS-Reality adapter

The "Reality" technique avoids needing a domain owned by the operator.

Operation summary:
1. Client sends a TLS ClientHello with `SNI = <real_target_domain>`
   (chosen from the dynamic SNI pool) and an authentication token
   embedded in a TLS extension.
2. Server receives the ClientHello, extracts and verifies the token.
3. **If the token is valid**: the server completes the TLS handshake
   itself (presenting a certificate it pre-generates whose chain leads
   to the legitimate target), then upgrades the connection to VWP/1.
4. **If the token is invalid or absent**: the server transparently
   proxies the entire TLS session to `<real_target_domain>:443`,
   making active probes see the real target site.

The full token format and the certificate-derivation procedure are
specified in §9.2.1 (to be added in a subsequent revision of this
draft; the implementation will source from the existing public
XTLS-Reality specification).

### 9.3 WebSocket-over-TLS adapter

- Underlying: TCP + TLS 1.3.
- The TLS handshake uses uTLS for fingerprint mimicry.
- The HTTP upgrade path uses a randomised but realistic URL
  (e.g. `/api/v1/sync`, `/static/app.js`) configured per-deployment.
- Headers MUST match a real browser's WS upgrade request.
- VWP/1 frames are carried in WebSocket binary frames.
- A single WS connection carries the entire VWP/1 session;
  multiplexing is handled internally per §6.4.

### 9.4 HTTP/3 MASQUE adapter

- Implements [RFC 9298](https://www.rfc-editor.org/rfc/rfc9298)
  CONNECT-UDP for tunnelling.
- The MASQUE proxy endpoint URL is configured per-deployment.
- This is the highest-stealth transport but requires HTTP/3 support
  on the server and a sufficiently modern intermediate stack.

---

## 10. Error codes

Used in `CONTROL/DISCONNECT` and other error contexts.

| Code     | Name                       | Description |
|----------|----------------------------|-------------|
| `0x0000` | NO_ERROR                   | Graceful close |
| `0x0001` | ERR_PROTOCOL_VIOLATION     | Frame format or state-machine violation |
| `0x0002` | ERR_AUTH_FAILED            | Noise handshake failure |
| `0x0003` | ERR_REPLAY_DETECTED        | Replay-window violation |
| `0x0004` | ERR_KEY_ROTATION_REQUIRED  | Threshold reached |
| `0x0005` | ERR_FLOW_CONTROL_VIOLATION | Sender exceeded window |
| `0x0006` | ERR_INTERNAL               | Generic implementation failure |
| `0x0007` | ERR_QUOTA_EXCEEDED         | Per-user quota reached |
| `0x0008` | ERR_REVOKED                | User key has been revoked |
| `0xFFFF` | ERR_UNKNOWN                | Catch-all |

---

## 11. Conformance

A VWP/1 implementation is **conformant** if it:

1. Implements all REQUIRED frame types and the QUIC transport adapter.
2. Correctly performs Noise XK handshake and key rotation per §3.
3. Honours flow control per §6.3.
4. Performs CAPABILITIES negotiation per §7.
5. Passes the published reference test vectors (vectors will be
   published alongside this spec at `docs/test-vectors/`).

Implementations MAY add transport adapters and mimicry profiles
beyond this baseline.

---

## 12. Security considerations

This section is intentionally minimal in this draft. The full
security analysis lives in the [threat model](THREAT_MODEL.md);
implementers MUST read it.

Key items to highlight here:

- Servers MUST validate received Noise messages strictly. Any
  deviation from the expected XK pattern MUST result in connection
  termination, not a partial-state retention.
- Implementations MUST use constant-time comparison for
  authentication tokens and MAC checks.
- Random data used in padding MUST come from a CSPRNG. A weak
  PRNG can leak state to a sophisticated observer.
- Implementations MUST clear sensitive material from memory at
  rotation and session close, where the language permits.

---

## 13. IANA / registry considerations

VWP/1 does not currently register anything with IANA. The protocol
ID octet (§3.2), frame type codes (§5), control op codes (§5.3),
mimicry profile IDs (§8), and error codes (§10) are governed by
this document. New code points are added by a versioned revision
of this specification.

---

## 14. References

- Noise Protocol Framework — http://www.noiseprotocol.org/noise.html
- RFC 8439 — ChaCha20 and Poly1305
- BLAKE2 — https://www.blake2.net/
- RFC 9000 — QUIC
- RFC 9298 — HTTP/3 MASQUE CONNECT-UDP
- BCP 14 — Key words for use in RFCs
- The Veil [threat model](THREAT_MODEL.md)
