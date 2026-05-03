# Product Requirements Document — Veil

**Версия:** 3.1.0
**Статус:** Draft for Implementation
**Дата:** 2026-05-04
**Тип продукта:** Self-hosted OSS VPN с anti-censorship ядром
**Целевые рынки:** РФ, Китай, Иран, Беларусь, любые страны с активным DPI/SNI-фильтрацией
**Лицензия:** Apache License 2.0 (commercial use OK, требуется attribution через NOTICE)
**Funding model:** Donations only (OpenCollective + GitHub Sponsors)

---

## 0. Содержание

1. Executive Summary
2. Видение и принципы
3. Цели и анти-цели
4. User Personas
5. Threat Model
6. Competitive Analysis
7. Product Architecture
8. Protocol Specification (Veil Wire Protocol v1)
9. Anti-Censorship Strategy
10. Self-Host UX
11. Edge Backend Layer
12. C-API и SDK
13. User Management и Multi-tenancy
14. Observability
15. Security & Privacy
16. Tech Stack
17. DevOps / CI/CD / Distribution
18. Roadmap
19. Success Metrics
20. Risks & Mitigations
21. Open Questions
22. Glossary

---

## 1. Executive Summary

**Veil** — это open-source VPN-платформа, состоящая из:

- **Ядра** (Go, единый бинарь: server + client + admin) с протоколом Veil Wire Protocol (VWP).
- **GUI-инсталлятора** (Tauri) для развёртывания сервера на VPS/edge без терминала.
- **Кросс-платформенных клиентов** (desktop: Tauri, mobile: React Native).

Ключевая дифференциация — **adaptive multi-transport runtime** с **dynamic SNI intelligence** и **decoy traffic engine**, позволяющий протоколу выглядеть как легитимный браузерный трафик к топ-1000 сайтам региона юзера. Цель — не «невидимость», а **collateral damage**: блокировка Veil должна ломать заметную долю легитимного веба.

Продукт self-hosted: пользователь разворачивает сервер на своём VPS, edge-функции (Deno/Fly), или Docker-инфраструктуре через GUI-визард за <5 минут без знания терминала. Шарит конфиг друзьям через QR/ссылку. Управляет sub-юзерами через embedded web UI.

---

## 2. Видение и принципы

### 2.1 Видение
Сделать неубиваемый туннелированный транспорт доступным в один клик любому пользователю в стране с цензурой, без зависимости от централизованной инфраструктуры разработчика.

### 2.2 Принципы

| Принцип | Объяснение |
|---------|-----------|
| **Self-host first** | Никакой нашей инфраструктуры в критическом пути. Юзер контролирует всё. |
| **Zero-knowledge by default** | Клиент не звонит «домой». Никакой телеметрии без явного opt-in. |
| **Single binary** | Один исполняемый файл = server, client, admin, CLI. Меньше частей — меньше поверхности атаки. |
| **Boring crypto** | Только проверенные примитивы (Noise, X25519, ChaCha20-Poly1305). Никаких самописных схем. |
| **Adaptive over static** | Любой статичный признак (порт, SNI, transport) — будущая дыра. Всё ротируется. |
| **Collateral damage > stealth** | Не «не палиться», а «дорого блокировать». |
| **UX = security feature** | Сложный setup = юзер делает ошибку = его палят. UX critical. |
| **Open spec** | Wire protocol документирован, аудитируем, реализуем третьими сторонами. |

---

## 3. Цели и анти-цели

### 3.1 In Scope (v1.0)

- Adaptive multi-transport: QUIC/443, TCP+TLS/443 (Reality-style), WebSocket-over-TLS, HTTP/3 MASQUE.
- Dynamic SNI pool из локального Tranco-snapshot с per-user weighted shards.
- Decoy traffic engine (параллельные реальные HTTP/2 запросы к target SNI).
- Statistical traffic mimicry на основе recorded browsing profiles.
- Self-host deployment через GUI: VPS (SSH), Docker, Edge (Deno/Fly OAuth).
- Embedded admin Web UI (sub-user management, quota, expiry, traffic stats).
- Cross-platform клиенты: Windows, macOS, Linux (Tauri); iOS, Android (React Native).
- C-API (CGO) для интеграции в third-party UI.
- Auto-update сервера с подписью релизов (cosign/sigstore).
- ACME (Let's Encrypt) embed для автоматической выдачи сертификатов.
- Reality-mode без собственного домена (steal SNI from real target).
- Prometheus metrics endpoint + structured logs (slog).
- Pluggable user-management backend (SQLite default, Postgres optional).

### 3.2 Out of Scope (v1.0)

- Управление сетевыми интерфейсами ОС (TUN/TAP) на уровне ядра — это задача обёртки.
- Биллинг, платёжные шлюзы, marketplace.
- Centralized control plane (любой управляющий сервер на нашей стороне).
- Mesh routing, peer discovery (юзеры сами шарят конфиги).
- Tor-like onion routing (multi-hop добавим только если threat model потребует).
- Постквантовая криптография в v1 (заложить интерфейсы, добавить в v2).
- eBPF datapath (overkill для self-host VPS, добавим в v2 если нужна высокая нагрузка).
- Mobile VPN (TUN) на iOS — требует Apple developer cert, отложено до post-v1.

### 3.3 Explicit Non-Goals навсегда

- Закрытый исходный код любой части.
- Хранение пользовательского трафика на любой нашей инфраструктуре.
- Backdoor compliance с любым регулятором.
- Обещание «100% невидимости» — это маркетинговая ложь, мы продаём «expensive to kill».

---

## 4. User Personas

### 4.1 Persona A — «Tech-aware end user» (основная, 70% базы)

- Возраст 18–35, РФ/CN/IR.
- Использует VPN для соцсетей, новостей, работы.
- Знает что такое VPS концептуально, но не хочет лезть в SSH.
- Готов потратить $4–10/мес на Hetzner/DigitalOcean.
- Ценит: «работает», «один клик», «не ломается».
- Не ценит: технические детали, графики, опции.
- **Use case:** скачал → ввёл IP+пароль VPS → жмёт «Установить» → получает QR → подключается с телефона.

### 4.2 Persona B — «Power user / hobbyist» (20%)

- 25–45, ИТ-сфера.
- Знает Docker, ssh, разбирается в DNS.
- Хочет тонкую настройку: выбор transport, custom SNI list, multi-server failover.
- Хост для себя + 5–20 друзей.
- **Use case:** docker compose up → правит config.yml → подключает свой Grafana к metrics endpoint.

### 4.3 Persona C — «Operator» (10%)

- Хостит сервер для 50–500 пользователей (community, активисты, малая компания).
- Заботится о quota, биллинге (внешнем), uptime.
- Использует Ansible/Terraform.
- **Use case:** terraform apply разворачивает 5 серверов в разных регионах + admin UI с user management.

### 4.4 Persona D — «Researcher / auditor» (1%, важно для credibility)

- Безопасник, академик.
- Читает spec, аудитирует код, фаззит.
- Нужен: формальный wire protocol, threat model, reference test vectors.

---

## 5. Threat Model

### 5.1 Adversaries

| Adversary | Capabilities | Motivation |
|-----------|-------------|-----------|
| **ТСПУ (РФ)** | Passive DPI, ML traffic classification, active SNI probing, ASN-wide IP scanning, periodic protocol blacklisting, throttling/shaping | Блокировка «нежелательных» сервисов, сбор статистики |
| **GFW (CN)** | Все возможности ТСПУ + active TLS handshake replay, residual censorship, BGP-уровень манипуляции, ML на flow-level features | Полный контроль информационного пространства |
| **Iranian SmartFilter** | Похоже на GFW lite, агрессивный SNI-block, periodic full-internet blackouts | Контроль |
| **VPS hosting provider** | Видит весь трафик с/на сервер, может логировать, может банить за «abuse» (high bandwidth) | TOS compliance, юридическое давление |
| **CDN provider** (если используется как edge) | Видит plaintext HTTP-уровень, может банить за TOS | TOS, legal pressure |
| **Local network operator** (отель, кафе, корпорат) | DPI, SNI block, port whitelist | Корпоративная политика |
| **Targeted attacker** (оперативник)| Может получить физ. доступ к клиенту, может skill-up до 0day | Идентификация конкретного юзера |

### 5.2 Что защищаем (Assets)

1. **Содержимое трафика юзера** — encrypted end-to-end Noise.
2. **Identity юзера** — клиент не утечёт реальный IP куда не надо.
3. **Identity сервера** — IP сервера не должен тривиально палиться через SNI/handshake/timing.
4. **Факт использования VPN** — flow не должен очевидно выделяться из легитимного веба.
5. **Список юзеров сервера** — admin DB не утечёт через сервер endpoint.
6. **Сервисная инфра юзера** (admin UI) — не должна быть открыта в инет.

### 5.3 Что НЕ защищаем (явно)

- **Анонимность от Veil-сервера.** Хозяин сервера видит метаданные (когда, сколько). Это by design — self-host model. Хочешь анонимность — Tor.
- **Защита от physical compromise клиента** — out of scope, юзер должен использовать FDE.
- **Защита от malicious VPS-провайдера, который активно MITM-ит** — уменьшаем вред (E2E crypto), но если провайдер злонамерен и кооперируется с цензором, threat model нарушен.
- **Защита от 0day в Go runtime / quic-go / uTLS** — best effort через timely updates, но абсолютной гарантии нет.

### 5.4 Attack Vectors (mapped to mitigations)

| Атака | Mitigation в Veil |
|-------|----------------------|
| **Active SNI probing** | Reality-style steal SNI; сервер отвечает как реальный target если probe не имеет валидного auth tag |
| **TLS-in-TLS detection** | HTTP/3 MASQUE и WS+chunked transport вместо raw TLS-in-TLS; uTLS fingerprint rotation |
| **Statistical flow classification** | Decoy engine (parallel real reqs); replay packet timing distributions; weighted SNI shard per user |
| **Port-based block** | Multi-port + port hopping; transport selector falls back в 443/80 если высокие порты блочат |
| **ASN/IP enumeration** | Поддержка multi-IP сервера; CDN fronting опция; integration с CDN edge (Bunny, G-Core) |
| **Handshake timing fingerprint** | Split handshake + jitter инъекция; матчинг к browser handshake distributions |
| **Replay attacks** | Noise XK pattern с anti-replay window; ephemeral keys |
| **DDoS на сервер** | Rate limit per IP; SYN cookies-equivalent для UDP (token-based); fail2ban-style IP bans |
| **Compromised user key** | Per-user keys, instant revocation через admin UI; key rotation API |
| **Server bin compromise (supply chain)** | Reproducible builds; cosign signature; SLSA Level 3 target |
| **Admin UI exposure** | Default bind на 127.0.0.1; access только через SSH tunnel или Tailscale; warning в installer если bind 0.0.0.0 |

---

## 6. Competitive Analysis

| Продукт | Plus | Minus | Чем Veil лучше |
|---------|------|-------|-------------------|
| **Amnezia VPN** | Multi-proto, GUI installer, SSH-based remote setup, RU-команда, mature | Статичный proto-выбор, нет adaptive runtime, нет decoy, нет SNI intelligence, нет edge backends | Adaptive transport, dynamic SNI, decoy engine, edge support |
| **Outline (Jigsaw)** | Polished UX, easy DO deploy, Google backing | Только Shadowsocks (легко детектится), нет fallback, центр-контроль через Outline Manager | Multi-transport, no central manager, anti-DPI built-in |
| **Algo VPN** | Ansible, multi-cloud, для power users | Только WireGuard/IKEv2, нет anti-DPI, нет GUI | Anti-censorship native, GUI |
| **Streisand** | Comprehensive | Заброшен с 2020 | Maintained, modern crypto |
| **WG-Easy** | Простой WG admin UI | Только WG, нет anti-DPI | Multi-proto, anti-DPI |
| **XRay-core / Sing-box** | SOTA anti-censorship (Reality, VLESS, Hysteria) | Server-only, нет deploy UX, конфиги-jsonland, для technical users | Полный продукт + UX |
| **Mullvad / ProtonVPN** | Polished, audited | Centralized (не self-host), exit nodes известны цензору, IP уже забанены | Self-host = unique IPs |
| **Tor** | Strong anonymity | Slow, bridges часто заблочены, не для повседневного веба | Speed-first, не претендуем на anonymity |
| **OpenVPN/IKEv2/L2TP** | Universal | Тривиально детектятся DPI, blocked в РФ/CN | Anti-DPI native |

**Главный конкурент:** Amnezia VPN. Они занимают нишу «easy self-host для РФ». Мы должны быть очевидно лучше них в anti-censorship и adaptive layer, плюс паритет в UX.

**Косвенный конкурент:** XRay/Sing-box. Они дают protocol-уровень. Мы — продукт сверху. Можем даже использовать их как backend transport в одной из инкарнаций.

---

## 7. Product Architecture

### 7.1 High-level

```
┌────────────────────────────────────────────────────────────┐
│                   END USER MACHINES                         │
│  ┌──────────────┐  ┌──────────────┐  ┌──────────────────┐  │
│  │ Desktop      │  │ Mobile       │  │ CLI / 3rd-party  │  │
│  │ (Tauri)      │  │ (RN)         │  │ (via C-API)      │  │
│  └──────┬───────┘  └──────┬───────┘  └────────┬─────────┘  │
│         │                  │                    │           │
│         └──────────────────┴────────────────────┘           │
│                            │                                 │
│                  veil-core (client mode)                  │
│                            │                                 │
└────────────────────────────┼─────────────────────────────────┘
                             │ Veil Wire Protocol (VWP)
                             │ (adaptive transport)
                             ▼
┌────────────────────────────────────────────────────────────┐
│                   USER-OWNED INFRA                           │
│                                                              │
│   ┌────────────────┐    ┌────────────────┐                  │
│   │  VPS / Docker  │ OR │  Edge function │                  │
│   │  veil-core  │    │  (Deno/Fly)    │                  │
│   │  (server mode) │    │  thin proxy    │                  │
│   └────────┬───────┘    └────────┬───────┘                  │
│            │                      │                          │
│            │ exit traffic         │ tunnel to origin         │
│            ▼                      ▼                          │
│         ┌─────────────────────────────┐                      │
│         │       INTERNET              │                      │
│         └─────────────────────────────┘                      │
└──────────────────────────────────────────────────────────────┘

         ┌──────────────────────────────┐
         │ veil-installer (separate) │
         │ Tauri GUI                    │
         │ - SSH remote install         │
         │ - Docker compose gen         │
         │ - Edge OAuth deploy          │
         │ - Config QR/share-link gen   │
         └──────────────────────────────┘
```

### 7.2 Component breakdown

#### 7.2.1 `veil-core` (single Go binary)

Modes (selected via subcommand):
- `veil serve` — server mode (accepts incoming connections, tunnels traffic).
- `veil connect <config>` — client mode (local SOCKS5/HTTP proxy + outbound VWP).
- `veil admin` — embedded admin Web UI server (TLS, auth, bound 127.0.0.1 by default).
- `veil config <subcmd>` — config generation, validation, QR encode, sub-user management.
- `veil doctor` — diagnostics: probe network, verify cert, check transport availability.
- `veil version` / `veil update` — version check, self-update.

Internal modules:
- `transport/` — pluggable transports (QUIC, TLS-Reality, WS, MASQUE).
- `crypto/` — Noise framework, key management, ACME client.
- `dpi/` — uTLS fingerprints, decoy engine, SNI pool, mimicry profiles.
- `proxy/` — SOCKS5/HTTP local proxy, traffic routing.
- `admin/` — embedded HTTP server, web UI assets, auth.
- `users/` — user CRUD, quota, expiry, revocation (SQLite/Postgres).
- `metrics/` — Prometheus exporter, slog handlers.
- `update/` — self-update with cosign verification.
- `cgo/` — C-API bindings (built only when `CGO_ENABLED=1`).

#### 7.2.2 `veil-installer` (Tauri app)

- Cross-platform GUI (Win/Mac/Linux).
- Separate from core to keep core lean.
- Workflows:
  1. **VPS setup**: SSH host + key/password → installer connects → uploads `veil-core` binary → systemd service → ACME bootstrap → returns admin URL + first-user config.
  2. **Docker setup**: generates `compose.yml` + `.env`, copies to clipboard или сохраняет в файл.
  3. **Edge setup**: OAuth flow с Deno Deploy / Fly.io → deploys edge worker → links к VPS origin (если есть) или hosts полный stack.
- Embedded veil-core binary как resource (для local testing mode).

#### 7.2.3 Mobile clients (React Native)

- iOS: NetworkExtension API (требует Apple Developer Account).
- Android: VpnService API.
- Подключение: scan QR / paste config link → store in secure enclave → auto-reconnect.
- Local SNI cache + decoy generator.

#### 7.2.4 Desktop clients (Tauri)

- Same Tauri shell как installer, но client-mode UI.
- TUN setup через wintun (Win), utun (Mac), tun (Linux). Не часть veil-core, чтобы избежать root в core.

### 7.3 Data flow (client → server, simplified)

```
1. Client startup
   ├─ Read config (server addr/port, transport priorities, user key, SNI pool seed)
   ├─ Probe network (which transports likely usable: UDP/443? TCP/443? CDN reachable?)
   └─ Select primary transport + 2 fallbacks

2. Local proxy listens on 127.0.0.1:1080 (SOCKS5) и :8080 (HTTP)

3. App makes request → SOCKS5 → client core
   ├─ Multiplex over existing VWP session if up, else handshake
   ├─ Encrypt с Noise XK
   ├─ Wrap в transport (QUIC/TLS/WS/MASQUE)
   ├─ Apply mimicry profile (timing, padding)
   └─ Send

4. Decoy engine runs in parallel
   ├─ Periodically GETs к target SNI на /favicon.ico, /robots.txt
   └─ Создаёт правдоподобный browsing pattern

5. Server receives
   ├─ Transport demux
   ├─ Auth check (Noise + replay window)
   ├─ Decrypt
   ├─ Forward to upstream (либо raw inet, либо chained Tor/proxy)
   ├─ Track per-user metrics (bytes, conns)
   └─ Apply per-user quota/throttle

6. Response path: reverse, with same mimicry applied
```

### 7.4 Deployment topologies

#### Topology 1: Bare VPS (90% cases)
```
[Client] ─────VWP───→ [VPS:443] ──→ [Internet]
```
Простейший вариант. ACME для real cert или Reality-style без cert.

#### Topology 2: VPS + CDN fronting
```
[Client] ──TLS──→ [BunnyCDN edge] ──TCP──→ [VPS] ──→ [Internet]
```
SNI юзера = CDN domain. IP сервера невидим. Трафик ограничен CDN abuse limits.

#### Topology 3: Edge function (no VPS)
```
[Client] ──TLS──→ [Deno Deploy worker] ──HTTP──→ [Open exit proxy]
                                                  (or direct fetch())
```
Совсем без VPS. Limited bandwidth, но идеально для starter / fallback. Юзер только OAuth-логинит свой Deno аккаунт.

#### Topology 4: Multi-server failover
```
                ┌→ [VPS-EU] ─→ exit
[Client] ──VWP──┼→ [VPS-SG] ─→ exit
                └→ [Edge-fly] → exit
```
Client probes все, выбирает быстрейший живой. Auto-failover при degradation.

---

## 8. Protocol Specification — Veil Wire Protocol v1 (VWP/1)

### 8.1 Layered model

```
┌─────────────────────────────────────────┐
│  Application data (HTTP, BitTorrent...)  │
├─────────────────────────────────────────┤
│  VWP Session Layer                       │  multiplex, flow control, replay window
├─────────────────────────────────────────┤
│  VWP Crypto Layer                        │  Noise XK, ChaCha20-Poly1305
├─────────────────────────────────────────┤
│  VWP Mimicry Layer                       │  timing jitter, padding, decoy interleave
├─────────────────────────────────────────┤
│  Transport Adapter                       │  QUIC | TLS-Reality | WSS | MASQUE
├─────────────────────────────────────────┤
│  UDP / TCP                               │
└─────────────────────────────────────────┘
```

### 8.2 Crypto Layer

- **Handshake:** Noise XK (known server static key, ephemeral client), `Noise_XK_25519_ChaChaPoly_BLAKE2s`.
- **Server static key:** distributed in user config out-of-band.
- **Session keys:** rotated every N minutes или K bytes (configurable, default 60 min / 1 GiB).
- **AEAD:** ChaCha20-Poly1305 (быстрее AES без AES-NI, хорошо на ARM/mobile).
- **Replay protection:** 64-bit nonce + sliding window receiver-side.
- **Forward secrecy:** ephemeral ECDHE на каждой rotation.
- **PQC interface:** trait `KeyExchange` готов к hybrid X25519+Kyber768; реализация в v2.

### 8.3 Session Layer

- Stream multiplex поверх encrypted channel (QUIC streams нативно; для TCP transports — встроенный yamux-style mux).
- Flow control per-stream + per-session windows.
- Heartbeat ping каждые 30s (jittered) для idle detection.
- Graceful close с FIN on streams.

### 8.4 Mimicry Layer

См. секцию 9 — anti-censorship strategy.

### 8.5 Transport Adapters

#### 8.5.1 QUIC adapter
- Base: `quic-go` (использовать как dependency, не fork).
- TLS fingerprint: uTLS-injected Chrome/Firefox latest.
- ALPN: `h3` (или `h2` на TCP-mode).
- 0-RTT отключен по умолчанию (replay risk), opt-in.

#### 8.5.2 TLS-Reality adapter (Reality-like)
- Реализация по спеке XTLS-Reality v1.5+ adaptation:
  - Клиент шлёт ClientHello с SNI = реальный target (e.g. `www.microsoft.com`).
  - Сервер делает TLS handshake до target от имени клиента (proxy).
  - Если клиент шлёт валидный auth tag в ClientHello extension → сервер switches в Veil mode после handshake.
  - Иначе → forwards remaining TLS session к real target. Active probe видит реальный сайт.
- Without own domain mode.

#### 8.5.3 WebSocket-over-TLS adapter
- HTTPS endpoint `/something-randomish`.
- WS upgrade с realistic browser headers.
- VWP frames в WS binary frames.
- Подходит для CDN fronting (большинство CDN поддерживают WS).

#### 8.5.4 HTTP/3 MASQUE adapter
- RFC 9298 compliant proxy.
- Полностью legitimate HTTP/3 трафик.
- Самый stealth, но требует HTTP/3-capable middleware (nginx-quic / HAProxy 2.9+).

### 8.6 Wire format (VWP frames)

```
Frame:
  ┌─────────┬─────────┬──────────────┬──────────┐
  │ Type(1) │ Flags(1)│ StreamID(4)  │ Length(2)│
  ├─────────┴─────────┴──────────────┴──────────┤
  │ Payload (Length bytes)                       │
  ├─────────────────────────────────────────────┤
  │ Padding (variable)                           │
  └─────────────────────────────────────────────┘

Types:
  0x01  STREAM_DATA
  0x02  STREAM_OPEN
  0x03  STREAM_CLOSE
  0x04  PING
  0x05  PONG
  0x06  WINDOW_UPDATE
  0x07  CONTROL  (key rotation, capability negotiation)
  0xFF  PADDING_ONLY  (для mimicry layer)

Flags:
  bit 0  PADDED
  bit 1  END_STREAM
  bit 2  COMPRESSED  (per-stream zstd opt-in)
```

### 8.7 Anti-replay & integrity

- Каждое сообщение AEAD-protected с 64-bit counter nonce.
- Sliding window 1024 messages для out-of-order acceptance.
- Window violations → connection reset + log.

### 8.8 Capability negotiation

Клиент и сервер обмениваются `CONTROL/CAPABILITIES` в первом фрейме:
- Supported transports, ciphers, mimicry profiles, features.
- Backward compatible: unknown caps игнорируются.

---

## 9. Anti-Censorship Strategy

### 9.1 Threat: Static SNI fingerprinting

**Attack:** Цензор видит миллион подключений к `microsoft.com` через VPS-IP не принадлежащий Microsoft → блокировка.

**Mitigation: Dynamic SNI Pool**
- Клиент держит локальный snapshot Tranco-1M (обновляется через signed update channel раз в день).
- Pool фильтруется по региону клиента (TLD, ASN geo, регуляторный whitelist).
- Per-user weighted shard: hash(user_id) → подмножество 200-500 SNI из pool.
- Selection не uniform: вес = popularity score (Zipf-like distribution, как реальный трафик).
- Ротация: каждое новое подключение — новый случайный SNI из своего shard.

**Rationale:** ML классификатор не может learn «все юзеры идут на X» если каждый юзер ходит на свой набор популярных сайтов в распределении, неотличимом от реального.

### 9.2 Threat: Active SNI probing

**Attack:** GFW шлёт TLS handshake с тем же SNI на VPS-IP → проверяет, действительно ли там этот домен.

**Mitigation: Reality-style SNI stealing**
- Сервер для probe-трафика (без валидного auth tag) полностью проксирует TLS handshake к real target.
- Probe видит реальный сайт, реальный cert, реальный response.
- Только клиент с валидным Noise tag в ClientHello extension получает Veil session.

### 9.3 Threat: TLS-in-TLS detection

**Attack:** ML модель видит inner TLS handshake внутри outer TLS — characteristic packet size (517 bytes ClientHello) → classification.

**Mitigations:**
1. **HTTP/3 MASQUE primary transport** — нет nested TLS, всё чистый HTTP/3.
2. **WS+chunked**: фрагментация inner traffic в random-size WS frames с padding.
3. **uTLS fingerprint mimicry**: outer ClientHello matches Chrome/Firefox bit-for-bit, включая GREASE и extension order.
4. **No raw TLS-in-TLS режим больше не default.**

### 9.4 Threat: Statistical flow classification

**Attack:** ML модель classifies VPN flows по features: packet size distribution, inter-arrival times, flow size, burstiness.

**Mitigations:**

#### 9.4.1 Decoy traffic engine
- Параллельно с tunnel клиент генерит реальные HTTPS GET'ы к target SNI host:
  - `GET /` (если есть), `GET /favicon.ico`, `GET /robots.txt`, `GET /sitemap.xml`.
  - Periodic interval, jittered.
- Сервер также может инициировать decoy outbound к похожим targets (server-side mimicry).
- Создаёт plausible browsing pattern на flow-level.

#### 9.4.2 Statistical mimicry profiles
- Pre-recorded packet timing/size distributions для reference activities:
  - "youtube_video_360p"
  - "tiktok_scroll"
  - "instagram_feed"
  - "google_search_browse"
  - "telegram_chat_idle"
- Profile applied to outgoing traffic: padding до next size bucket, delay до next inter-arrival quantile.
- Trade-off: latency vs stealth — настраивается per-connection.
- Profile selection per-user, per-session, по time-of-day patterns.

#### 9.4.3 Padding strategy
- Не uniform padding до MTU (палится).
- Sample target packet size from selected profile distribution.
- Per-flow random padding seed.

### 9.5 Threat: IP/ASN enumeration

**Attack:** Цензор знает что Hetzner ASN = много VPN серверов → blanket block.

**Mitigations:**
1. **Multi-IP server support** — один сервер слушает на N IP, ротация per-user.
2. **CDN fronting topology** — IP сервера невидим за CDN edge.
3. **Edge function topology** — нет фиксированного IP вообще (Deno/Fly edge IPs share с легитимным трафиком).
4. **Port hopping** — сервер слушает на нескольких портах, клиент ротирует.

### 9.6 Threat: Bandwidth-based detection

**Attack:** Клиент потребляет 10 GB/день к одному IP — нетипично для веб-серфинга → flag.

**Mitigations:**
1. **Multi-server failover** — клиент распределяет трафик.
2. **Edge fanout** — heavy traffic offload на edge backends.
3. **Quota-based throttle** на стороне клиента (warning юзеру при threshold).

### 9.7 Threat: Timing fingerprint of handshake

**Attack:** Real browser TLS handshake имеет characteristic timing pattern (ClientHello → ServerHello через ~1RTT → finished). VPN handshakes часто отличаются (extra round-trip).

**Mitigations:**
1. **Split handshake**: фрагментация ClientHello на TCP-level segments с random delays.
2. **Jitter injection**: artificial delay ±N ms (sampled from browser distribution).
3. **0-RTT** (опционально, с replay защитой): уменьшает round-trips.

### 9.8 Anti-Censorship Score

Внутренняя метрика для каждого config / region:
```
ACS = w1 * SNI_diversity + w2 * mimicry_quality + w3 * transport_diversity
    + w4 * decoy_intensity + w5 * IP_diversity - penalties
```
Отображается юзеру в admin UI как «Resilience Score: 87/100» с breakdown.

---

## 10. Self-Host UX

### 10.1 Install paths

#### Path A: GUI installer + VPS (target persona A)

```
1. User downloads veil-installer.exe (~30 MB)
2. Launches → welcome screen
3. "Choose deploy method":
   [VPS via SSH]  [Docker (advanced)]  [Edge function]
4. User clicks "VPS via SSH"
5. Form:
   - Host (IP or hostname)
   - SSH port (default 22)
   - User (default root)
   - Auth: [Password] [SSH key file] [SSH agent]
   - "I want a custom domain (optional)" toggle
6. "Connect & Verify" button
   → installer SSHs in, runs sanity checks (OS, arch, free port 443)
   → reports OK/issues с actionable hints
7. "Configuration":
   - Region preset (RU/CN/IR/Custom)
   - Transport mix (default: Auto)
   - Initial username
8. "Install" button (1-3 min progress bar with live log):
   - Uploads veil-core binary (matched to remote arch)
   - Creates systemd service
   - ACME bootstrap (if domain) или Reality setup (if no domain)
   - Generates user config + admin password
9. Success screen:
   - Admin URL (with note: "access via SSH tunnel for security")
   - First user QR + share link
   - "Open admin UI" / "Save config to file" / "Email to friend" buttons
```

#### Path B: Docker (persona B)

```
1. Installer → "Docker"
2. Form: domain (optional), user count, transport prefs
3. "Generate" → outputs:
   - docker-compose.yml
   - .env file
   - README.md с командами
4. Buttons: [Copy to clipboard] [Save folder] [Open in editor]
```

#### Path C: Edge (persona A or B, no VPS)

```
1. Installer → "Edge function"
2. "Choose provider": [Deno Deploy] [Fly.io] [Cloudflare Workers (when available)]
3. OAuth flow → user logs into provider
4. Installer creates project, deploys worker, sets env vars
5. Returns: edge URL + user config
   (Note: limited bandwidth, suitable as fallback or starter)
```

### 10.2 Admin Web UI

Embedded в `veil-core admin`. Bind 127.0.0.1:8443 default. Access via SSH local forward (installer показывает команду + кнопку "Open SSH tunnel from this machine").

Pages:
- **Dashboard**: connections live, traffic graphs, ACS score, alerts.
- **Users**: CRUD, regen key, set quota/expiry, traffic per user, revoke.
- **Server**: config editor (with validation), restart, logs viewer (last 1000 lines), version + update button.
- **Transports**: enable/disable transports, set priorities, view per-transport stats.
- **Diagnostics**: run `veil doctor` от UI, показывает результаты.

Auth: username + password (bcrypt), стораж в SQLite. Optional WebAuthn.

### 10.3 Sharing config с друзьями

Из admin UI → "Add user":
- Username, optional expiry, optional quota.
- Generates config bundle = encrypted blob с:
  - Server addr/port options (multi-IP/multi-port)
  - User Noise key
  - SNI pool seed
  - Transport caps
- Output форматы:
  - QR code (для mobile scan)
  - Short URL (`veil://...`) для desktop import
  - Email invite (через user-configured SMTP, опционально)

### 10.4 First-run client UX

```
1. User opens Veil Client
2. "Add server": [Scan QR] [Paste link] [Import file]
3. Validates config → "Server: <name>, Region: <region>"
4. [Connect] button
5. Progress: "Probing transports..." (1-3 sec)
6. Connected → status bar: transport=QUIC, latency=43ms, ACS=87
7. Auto-reconnect on network change
```

### 10.5 Failure modes UX

| Failure | UX response |
|---------|-------------|
| Server unreachable | Try fallback transports → if all fail, clear error: "Server X.X.X.X unreachable. Check VPS." |
| Cert expired | Auto-attempt renewal; if fails, switch to Reality mode automatically; notify в admin |
| All transports blocked | Try CDN fronting if configured; else "Currently unable to connect. Try changing region preset" |
| Quota exceeded | Client clear message "Your traffic quota for this month is exceeded" |
| Admin UI port в инете (security risk) | Bold warning в installer + admin UI, button "Restrict to SSH tunnel" |

---

## 11. Edge Backend Layer

### 11.1 Цель

Дать юзерам опцию работать без VPS, либо использовать edge как high-anonymity fallback, либо для bandwidth offload.

### 11.2 Поддерживаемые backends (v1)

| Backend | Free tier | Bandwidth | Region | Auth |
|---------|----------|-----------|--------|------|
| Deno Deploy | 100k req/day, 100 GB/mo | OK | Global edge | OAuth GitHub |
| Fly.io | $5 credit/mo | OK | Global, есть RU-edge | API token |
| Vercel Edge | 100 GB/mo | flaky в РФ | Global | OAuth |
| Supabase Edge Functions | 500k req/mo | OK | Global | API key |

### 11.3 Edge worker design

- Минимальный proxy в Deno/JS runtime.
- Принимает VWP-over-WSS от клиента, форвардит к origin VPS (если configured) либо делает direct `fetch()` к Internet (limited to HTTP-target use cases).
- Не хранит state.
- Health-check endpoint для client probing.

### 11.4 Edge OAuth flow

Installer открывает browser → user authorizes Veil installer как OAuth app → installer получает scoped token → deploys worker через provider API → token stored локально (zero-trust на провайдера, но юзер контролирует).

### 11.5 Rotation на rate-limit

Клиент детектит 429/quota → автоматом switches на следующий configured backend. Notifies юзера в admin.

---

## 12. C-API & SDK

### 12.1 Цель

Дать third-party разработчикам интегрировать Veil в свои UI/приложения без зависимости от наших клиентов.

### 12.2 ABI

Single shared library: `libveil.{so,dll,dylib}`.

```c
// veil.h

typedef struct VeilInstance VeilInstance;

typedef enum {
    VEIL_EVENT_CONNECTED = 1,
    VEIL_EVENT_DISCONNECTED = 2,
    VEIL_EVENT_ERROR = 3,
    VEIL_EVENT_TRAFFIC = 4,
    VEIL_EVENT_TRANSPORT_SWITCH = 5,
} VeilEventType;

typedef void (*VeilEventCallback)(
    VeilEventType type,
    const char* json_payload,
    void* user_data);

// Lifecycle
VeilInstance* veil_create(const char* config_json);
int veil_start(VeilInstance* inst, VeilEventCallback cb, void* user_data);
int veil_stop(VeilInstance* inst);
void veil_destroy(VeilInstance* inst);

// Operations
char* veil_get_metrics(VeilInstance* inst);  // returns malloc'd JSON
int veil_set_config(VeilInstance* inst, const char* config_json);

// Memory mgmt
void veil_free_string(char* str);

// Version
const char* veil_version();  // static, do not free

// Error codes
typedef enum {
    VEIL_OK = 0,
    VEIL_ERR_INVALID_CONFIG = -1,
    VEIL_ERR_TRANSPORT_FAILED = -2,
    VEIL_ERR_AUTH_FAILED = -3,
    VEIL_ERR_INTERNAL = -99,
} VeilError;
```

### 12.3 Thread safety

- All `veil_*` functions thread-safe (internal mutex).
- Callback invoked from internal goroutine — implementer должен dispatch в свой UI thread.
- One `VeilInstance` = one client session.

### 12.4 Memory model

- Strings возвращаемые из `veil_get_metrics` — malloc'd Go-side, MUST be freed via `veil_free_string`.
- Strings передаваемые в `veil_create` / `veil_set_config` — копируются, caller владеет своим.
- Callback `json_payload` — действителен только в течение callback, caller должен copy если хочет сохранить.

### 12.5 Bindings (provided)

- **Rust**: `veil-rs` crate с safe wrapper.
- **Python**: `veil-py` (cffi).
- **Node.js**: `node-veil` (NAPI).
- **Swift**: `VeilKit` (Swift Package).
- **Kotlin/JVM**: `veil-jni`.

### 12.6 Stability

- C-API stable across minor versions (semver).
- Breaking changes только в major version bump.
- Wire protocol versioned independently.

---

## 13. User Management & Multi-Tenancy

### 13.1 Модель

- **Server operator** (admin): полный доступ к admin UI, управляет всеми users.
- **End user**: имеет config, использует server, не имеет доступа к admin.
- **Sub-admin** (v1.5+): ограниченный admin access (например, видит только своих users).

### 13.2 User entity

```
User {
    id: uuid
    name: string
    noise_static_key: bytes (per-user keypair)
    created_at: timestamp
    expires_at: timestamp | null
    quota_bytes_per_month: int64 | null  (null = unlimited)
    used_bytes_current_month: int64
    last_seen: timestamp
    status: active | revoked | expired | quota_exceeded
    notes: string (admin-only)
    tags: [string]  (для группировки)
}
```

### 13.3 Storage

- Default: SQLite embedded (zero-config, single file).
- Optional: Postgres (для operator-сценариев, multi-replica).
- Schema migrations через embedded migrate library.

### 13.4 Operations

- CRUD via admin UI.
- CLI: `veil user add/list/revoke/regen-key/set-quota`.
- API (HTTP, auth-protected) для интеграции с external systems.
- Bulk import/export (CSV/JSON).

### 13.5 Quota enforcement

- Real-time accounting per-user в memory.
- Periodic flush в DB (default 30s).
- Hard cutoff при exceeding: server отказывает в новых connections, existing graceful disconnect.
- Quota reset: monthly (default), configurable.

---

## 14. Observability

### 14.1 Metrics (Prometheus)

Endpoint `/metrics` (admin-only, auth-protected).

Examples:
```
veil_server_connections_active{transport="quic"}
veil_server_bytes_total{direction="rx",user="alice"}
veil_handshake_duration_seconds{transport="reality"}
veil_decoy_requests_total{target="microsoft.com"}
veil_user_quota_used_ratio{user="alice"}
veil_acs_score
veil_transport_failure_total{transport="quic",reason="udp_blocked"}
```

### 14.2 Structured logging

- `slog` (Go 1.21+ stdlib).
- JSON output by default.
- Levels: DEBUG, INFO, WARN, ERROR.
- Sensitive fields auto-redacted (keys, IPs опционально).
- Configurable sinks: stdout, file, syslog.

### 14.3 Tracing (opt-in, v1.5+)

OpenTelemetry SDK для server-side traces при отладке. Не включено по умолчанию (privacy + overhead).

### 14.4 Alerting hooks

- Webhook на события: server crash, cert expiry < 7 days, quota threshold, suspicious activity (mass auth failures).
- Built-in Telegram bot integration (для админа).

---

## 15. Security & Privacy

### 15.1 Privacy guarantees

- **No phone-home** в ядре v1.0. Update check можно disabled.
- **Zero telemetry в v1.0.** Никакого Sentry, никакой аналитики.
- **Post-v1 (v1.5+):** возможна opt-in анонимная usage-метрика (например, Sentry или GlitchTip self-hosted) — только counter "active installations", без user data, без crash telemetry с PII. Решение принимается отдельным RFC с public discussion перед включением.
- **No DB user IPs** unless admin explicitly enables.
- Logs default level INFO без IP-логирования (debug logs могут содержать).

### 15.2 Security practices

- **Reproducible builds** (Go + cosign signature).
- **SLSA Level 3** target для CI.
- **Dependencies pinned**, vulnerability scan в CI (govulncheck + trivy).
- **Fuzzing**: go-fuzz / native fuzz для config parser, wire protocol parser, handshake state machine. Continuous OSS-Fuzz integration.
- **Memory safety**: minimal CGO, no unsafe in hot paths без code review.
- **Crypto audit**: planned external audit перед v1.0 release (target: Cure53 or NCC Group, см. Open Questions).
- **Bug bounty**: planned post-v1.0.

### 15.3 Hardening defaults

- Admin UI: bind 127.0.0.1.
- Server: drop privileges после bind (non-root systemd service).
- Containers: distroless base, non-root user, read-only rootfs.
- Capabilities: только `CAP_NET_BIND_SERVICE` если bind <1024.
- Strict TLS config (TLS 1.3 only для internal admin).

### 15.4 Update security

- Releases signed via cosign / sigstore.
- Server `veil update` verifies signature before applying.
- Auto-update opt-in (default off для server, on для clients).
- Rollback at startup if new version crashes within 60s.

---

## 16. Tech Stack

### 16.1 Core
- **Language:** Go 1.22+
- **Build:** standard `go build`, CGO_ENABLED=1 для shared lib targets
- **Crypto:** `flynn/noise`, `golang.org/x/crypto`
- **TLS:** `refraction-networking/utls`
- **QUIC:** `quic-go/quic-go` (as dependency, no fork)
- **HTTP/3 MASQUE:** `quic-go/masque-go` или собственная реализация
- **DB:** `mattn/go-sqlite3` (default), `jackc/pgx/v5` (optional)
- **Web admin:** embedded HTTP server + Vite-built SPA assets via `embed.FS`
- **Logging:** stdlib `log/slog`
- **Metrics:** `prometheus/client_golang`
- **CLI:** `urfave/cli/v3`
- **ACME:** `caddyserver/certmagic`

### 16.2 Installer
- **Framework:** Tauri v2 (Rust shell + WebView frontend)
- **Frontend:** SvelteKit (тонкий, быстрый билд) или React (если экосистема важнее)
- **SSH:** `russh` (Rust)
- **Embeds:** veil-core binaries для всех платформ как resources

### 16.3 Mobile clients
- **Framework:** React Native (shared codebase)
- **Native modules:** Swift (iOS NetworkExtension), Kotlin (Android VpnService)
- **Crypto/networking:** через veil-core C-API (libveil.so / .dylib)

### 16.4 Desktop clients
- **Same Tauri stack как installer**, но client-mode UI.
- **TUN drivers:** wintun (Win), utun via syscall (Mac), native (Linux).

---

## 17. DevOps / CI/CD / Distribution

### 17.1 Repository layout

```
veil/
├── core/                    # Go ядро (single binary)
│   ├── cmd/veil/         # main
│   ├── internal/
│   │   ├── transport/
│   │   ├── crypto/
│   │   ├── dpi/
│   │   ├── proxy/
│   │   ├── admin/
│   │   ├── users/
│   │   ├── metrics/
│   │   └── update/
│   ├── pkg/cgo/             # C-API
│   └── go.mod
│
├── installer/               # Tauri app
│   ├── src-tauri/
│   ├── src/                 # Svelte/React UI
│   └── package.json
│
├── clients/
│   ├── desktop/             # Tauri client app
│   └── mobile/              # React Native
│
├── deploy/
│   ├── docker/
│   │   ├── Dockerfile
│   │   ├── compose.yml
│   │   └── compose.with-postgres.yml
│   ├── ansible/
│   ├── terraform/
│   └── edge/
│       ├── deno/
│       ├── fly/
│       └── vercel/
│
├── sdks/
│   ├── veil-rs/
│   ├── veil-py/
│   ├── veil-node/
│   ├── VeilKit/
│   └── veil-jni/
│
├── docs/
│   ├── README.md
│   ├── INSTALL.md
│   ├── THREAT_MODEL.md
│   ├── PROTOCOL.md          # VWP/1 spec
│   ├── SECURITY.md
│   ├── CONTRIBUTING.md
│   └── architecture/
│
├── scripts/
│   ├── install.sh           # one-liner для bare-Linux
│   ├── build-cross.sh
│   └── release.sh
│
└── .github/
    └── workflows/
        ├── ci.yml
        ├── release.yml
        ├── fuzz.yml
        └── audit.yml
```

### 17.2 CI/CD (GitHub Actions)

**ci.yml** (on PR/push):
- `go vet`, `golangci-lint`, `gosec`, `govulncheck`.
- Unit tests + race detector.
- Integration tests против Docker compose stack.
- Build matrix: Linux/Mac/Win × amd64/arm64.
- Mobile build smoke test.

**release.yml** (on tag `v*`):
- Cross-compile binaries для всех targets:
  - `linux/amd64`, `linux/arm64`, `linux/mipsle`, `linux/armv7`
  - `darwin/amd64`, `darwin/arm64`
  - `windows/amd64`, `windows/arm64`
  - `android/arm64`, `ios/arm64`
- Build shared libs (`.so`, `.dll`, `.dylib`) с CGO.
- Build Docker images, push в ghcr.io.
- Sign artifacts с cosign.
- Generate SBOM (syft).
- GitHub Release с changelogs.
- Update install.sh с new version checksums.

**fuzz.yml** (nightly):
- Run fuzz corpora для config parser, wire protocol, handshake.
- Report new crashes как issues.

**audit.yml** (weekly):
- Trivy scan на все Docker images.
- govulncheck + cargo audit (для installer/clients).
- Dependency update PRs (renovate-style).

### 17.3 Distribution

- **GitHub Releases**: primary source of truth.
- **Bash one-liner**: `curl -fsSL veil.sh/install | bash` (host static script на собственном домене).
- **Homebrew tap**: `brew install redstone-md/tap/veil`.
- **Scoop bucket** (Windows).
- **Docker Hub + ghcr.io**: `veil/server:latest`, `:v1.0.0`.
- **Mobile**:
  - Android: F-Droid + signed APK на GitHub Release. Google Play (если policies позволяют).
  - iOS: TestFlight initially, App Store если permitted (high risk of rejection).
- **OpenWRT package**: для роутеров (post-v1.0).

---

## 18. Roadmap

### Phase 0 — Foundation (Month 1)

- Repo setup, CI skeleton, license decisions (MIT или GPLv3 — open question).
- Wire protocol spec v0.1 draft.
- Threat model document.
- Architecture decision records (ADRs).
- Hello-world Go skeleton с QUIC + Noise handshake.

**Deliverable:** working code accepting one connection через QUIC+Noise, no anti-DPI yet.

### Phase 1 — Core MVP (Months 2–3)

- Single transport: QUIC с uTLS Chrome fingerprint.
- SOCKS5 local proxy на client.
- Basic config file (TOML).
- CLI-only (no GUI).
- Manual user keys.
- Unit tests + integration test suite.
- Docker compose deploy.

**Deliverable:** работающий VPN end-to-end через CLI, можно использовать для browsing.

### Phase 2 — Anti-DPI Layer (Months 4–5)

- TLS-Reality transport (steal SNI).
- WebSocket-over-TLS transport.
- uTLS fingerprint rotation.
- Dynamic SNI pool с Tranco integration.
- Decoy traffic engine (basic).
- Multi-transport client с fallback.

**Deliverable:** работает в РФ против ТСПУ на тестовых полигонах.

### Phase 3 — Self-Host UX (Months 6–7)

- Tauri installer GUI.
- SSH remote install workflow.
- Embedded admin Web UI (basic): users CRUD, dashboard.
- ACME embed.
- User management с SQLite.
- Docs: install guide, troubleshooting.

**Deliverable:** non-technical user может развернуть сервер за 5 минут.

### Phase 4 — Clients & SDK (Months 8–9)

- Desktop clients (Tauri) for Win/Mac/Linux.
- C-API stable, libveil shared libs published.
- SDK: Rust, Python, Node bindings.
- Mobile MVP: Android (iOS если есть Apple cert).
- QR/share-link config import flow.

**Deliverable:** end-user скачивает client app, scans QR, подключается.

### Phase 5 — Hardening & Edge (Months 10–11)

- Statistical mimicry profiles (recorded YouTube/TikTok/Telegram).
- HTTP/3 MASQUE transport.
- Edge backend support (Deno Deploy, Fly.io).
- Multi-IP / multi-port server.
- Auto-update с cosign verification.
- Fuzzing infrastructure live.
- Security audit готовый к external review.

**Deliverable:** v1.0 RC.

### Phase 6 — Release & Ecosystem (Month 12)

- External security audit (Cure53 / NCC Group / Trail of Bits).
- Bug fixes from audit.
- Public launch, marketing, docs polish.
- F-Droid release.
- v1.0 GA.

### Post v1.0 (months 13+)

- iOS App Store push.
- OpenWRT package.
- PQC hybrid keyex.
- eBPF datapath на server (для high-throughput operators).
- Mesh / multi-hop optional layer.
- Sub-admin role.
- Built-in usage marketplace (operator продаёт доступ end-users — UI helper, биллинг external).

---

## 19. Success Metrics

### 19.1 Technical

| Metric | Target v1.0 | Target v2.0 |
|--------|-------------|-------------|
| Handshake success rate под active probing (РФ test lab) | ≥ 95% | ≥ 99% |
| Median latency overhead vs WireGuard | ≤ 30 ms | ≤ 15 ms |
| Throughput @ 1 Gbps server | ≥ 500 Mbps | ≥ 800 Mbps |
| CPU per Gbps (server, single core) | ≤ 60% | ≤ 35% |
| Time to first connection (cold start, fresh client) | ≤ 3s | ≤ 1.5s |
| Time от download installer до working server | ≤ 5 min (persona A) | ≤ 3 min |
| Zero CVE in core for 90 days post-audit | ✓ | ✓ |
| Deterministic build reproducibility | 100% | 100% |

### 19.2 Adoption (12 months post-v1.0)

| Metric | Target |
|--------|--------|
| GitHub stars | ≥ 5,000 |
| Active server installations (estimated) | ≥ 10,000 |
| Discord/community members | ≥ 2,000 |
| External SDK adoption | ≥ 3 third-party apps |
| Peer-reviewed security audit publication | ✓ |
| Listed in top 5 results for "anti-censorship VPN" | ✓ |

### 19.3 Resilience

| Metric | Target |
|--------|--------|
| Days от первого блока ТСПУ до working workaround release | ≤ 7 |
| % users surviving major censorship event (auto-failover) | ≥ 80% |

---

## 20. Risks & Mitigations

| Risk | Severity | Likelihood | Mitigation |
|------|----------|-----------|-----------|
| Single-developer burnout / scope explosion | High | High | Strict MVP discipline, scope cuts agreed upfront, community contributors recruitment по plan |
| GFW/ТСПУ adapts faster than releases | High | Medium | Modular transport architecture, fast-iteration release cadence, public test lab |
| Apple App Store rejection (iOS) | Medium | High | Plan B: TestFlight + sideload via AltStore; iOS as nice-to-have не critical path |
| Google Play rejection (Android) | Medium | Medium | F-Droid + direct APK primary distribution channel |
| Legal exposure (DMCA, country-level lawsuits) | Medium | Medium | Project incorporated в jurisdiction with strong free-speech protections; maintainer pseudonymity option |
| Critical CVE post-launch | High | Medium | Bug bounty, security audit pre-1.0, fast patch process, signed auto-update |
| Quic-go breaking changes | Medium | Medium | Pin version, contribute upstream, abstraction layer вокруг quic-go |
| Tranco list изменён / отозван | Low | Low | Multiple list sources (Tranco, Cisco Umbrella, custom curated), self-hosted fallback |
| CDN providers ban our use-cases | Medium | High | Multi-provider support, edge as opt-in not core path |
| Reality protocol declared deprecated by author | Low | Low | Independent implementation, fork if needed |
| Hardware-accelerated crypto (AES-NI) absent on cheap VPS | Low | Medium | ChaCha20 default (быстрее на ARM/no-AES-NI) |
| Project copied / forked maliciously | Medium | Medium | Trademark "Veil VPN", clear branding, signed releases как trust signal |

---

## 21. Decisions & Open Questions

### 21.1 Decided (locked in v3.1)

| # | Question | Decision | Rationale |
|---|----------|----------|-----------|
| 1 | License | **Apache License 2.0** | Allows commercial use, requires attribution via NOTICE file, includes explicit patent grant. Standard для OSS infrastructure projects. Не блокирует commercial integrators (vs GPL), но защищает от patent trolling. |
| 2 | Project name | **Veil** | Locked. Все артефакты (binary, lib, namespaces) переименованы. См. 21.3 risk о коллизиях имени. |
| 3 | Funding model | **Donations only** | OpenCollective (transparent finances) + GitHub Sponsors (низкое трение). Никакой коммерциализации ядра. Возможен grant application к OTF/NLnet позднее, но не зависим от этого. |
| 4 | Telemetry в v1.0 | **None** | Zero phone-home. Любая аналитика добавится только через public RFC + opt-in toggle. Возможный кандидат позднее: self-hosted GlitchTip или Sentry для anonymized install counter. |
| 5 | License attribution mechanism | **NOTICE file + footer credit в admin UI** | Apache 2.0 standard practice. Forks обязаны сохранять NOTICE. Admin UI показывает "Powered by Veil" linking back. |

### 21.2 Open Questions (требуют решения по ходу)

1. **iOS strategy:** push в App Store (risk rejection из-за VPN-в-сложных-странах policy) vs **sideload-only** (AltStore, Sideloadly). **Tentative:** TestFlight как minimum viable, App Store push как stretch goal v1.5+. Решение откладывается до завершения Android клиента.
2. **Audit vendor & timing:** Cure53, NCC Group, Trail of Bits, Radically Open Security — выбор зависит от raised donations к моменту v1.0 RC. Минимальный target: Radically Open Security ($15-30k). Stretch: Cure53 ($50-80k). Если donations недостаточно — community-driven peer review через published spec + bug bounty.
3. **Default Reality SNI target:** региональные defaults (RU → vk.com / yandex.ru / mail.ru; CN → bilibili.com / qq.com; IR → digikala.com)? Или universal (cloudflare.com / microsoft.com)? Вероятно: shipped defaults per region + admin может переопределить.
4. **Mesh layer:** включать ли в roadmap как первоклассную фичу или как third-party plugin поверх C-API? **Lean:** plugin, чтобы не раздувать core scope.
5. **Centralized SNI pool updates** (signed update channel) — кто подписывает? Single maintainer key (риск bus factor) vs threshold signatures (sigstore/cosign keyless?). Решение к Phase 2.
6. **Persona D (researcher) artifacts:** публиковать ли test vectors и formal proof attempts? Большая работа, но огромный credibility boost. Минимум: public protocol spec + reference test vectors. Stretch: TLA+ модель handshake state machine.
7. **Logo / brand identity:** требуется designer pass перед публичным launch.
8. **Domain:** veil.sh? veilvpn.org? veilproject.org? Зависит от availability на момент launch.

### 21.3 Known risks of "Veil" name

- Имя коллизирует с: Veil cryptocurrency project, Veil Browser (privacy extension), Veil Framework (security tooling). SEO будет конкурентным.
- **Mitigation:** диференцировать как **"Veil VPN"** или **"Veil Project"** в публичных коммуникациях; репо хостится на `github.com/redstone-md/veil`; купить домен с префиксом/суффиксом.
- **Если нейминг конфликт станет блокером** (cease & desist, trademark dispute): план B — переименование к v1.0 RC (cost: medium, главная боль — переименование Go modules и SDK packages).

---

## 22. Glossary

- **ACME** — Automatic Certificate Management Environment, RFC 8555. Используется для авто-выдачи Let's Encrypt cert.
- **ACS** — Anti-Censorship Score, internal Veil metric.
- **ASN** — Autonomous System Number, идентификатор провайдера в BGP.
- **CDN** — Content Delivery Network.
- **CGO** — Go's foreign function interface к C.
- **Collateral damage** — стратегия anti-censorship где блокировка нашего трафика обязательно ломает заметную долю легитимного.
- **VWP** — Veil Wire Protocol.
- **Decoy traffic** — реальные легитимные запросы рядом с tunneled, для маскировки.
- **DPI** — Deep Packet Inspection.
- **ECH** — Encrypted Client Hello, TLS extension скрывающий SNI.
- **GFW** — Great Firewall (CN).
- **JA3/JA4** — TLS fingerprinting hashes.
- **MASQUE** — Multiplexed Application Substrate over QUIC Encryption, RFC 9298.
- **MTU** — Maximum Transmission Unit.
- **Noise** — Noise Protocol Framework, фреймворк для построения secure handshake patterns.
- **PQC** — Post-Quantum Cryptography.
- **Reality (XTLS-Reality)** — anti-censorship transport, steals real SNI.
- **SLSA** — Supply-chain Levels for Software Artifacts.
- **SNI** — Server Name Indication, TLS extension с hostname.
- **Tranco** — академический rolling top-1M доменов (tranco-list.eu).
- **ТСПУ** — Технические Средства Противодействия Угрозам, RU цензурное оборудование.
- **uTLS** — utlsproxy / refraction-networking utls, библиотека для произвольных TLS fingerprints.

---

**End of PRD v3.1.0**
