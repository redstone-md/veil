# Installing Veil

This guide walks through bringing up a Veil server end-to-end on a
single VPS, registering a client, and connecting through a local
SOCKS5 proxy. The flow targets the **CLI** path; the GUI installer
arrives in a later phase.

The instructions assume a Linux server (Debian/Ubuntu/Alpine all
work) and any modern desktop OS for the client.

> **Status:** Veil is pre-alpha. The wire protocol, configuration
> formats, and CLI surface will change without notice until the v1.0
> release. Do not deploy Veil for anyone whose access matters until
> the v1.0 audit is complete.

---

## 1. Install the binary

### Option A — Build from source

```bash
git clone https://github.com/redstone-md/veil.git
cd veil/core
go build -o /usr/local/bin/veil ./cmd/veil
veil version
```

### Option B — Docker (server only)

The pre-built image runs the server side only. Clients must still
install a `veil` binary locally.

```bash
cd veil/deploy/docker
cp server.example.yaml server.yaml          # edit if needed
cp authorized_keys.example authorized_keys  # legacy file path; new
                                            # deployments use the
                                            # SQLite store instead
docker compose up -d
docker compose logs -f veil
```

(See `deploy/docker/README.md` for the full container recipe.)

---

## 2. First-run server setup

### 2a. Pick a transport mix

Veil's server can listen on several wire-level transports
simultaneously. A reasonable starter mix is QUIC for fast clients
and Reality for clients on networks where active TLS probing is a
concern. Add WSS as a fall-back if you want to handle networks
that strip UDP.

A minimal `server.yaml` for a Reality-fronted server pointing at
`www.microsoft.com`:

```yaml
transports:
  - type: reality
    listen: "0.0.0.0:443"
    target_sni: "www.microsoft.com"
    target_addr: "www.microsoft.com:443"

static_key_path: "/var/lib/veil/server.key"
user_db_path:    "/var/lib/veil/users.db"
```

> **Choosing `target_sni`.** Pick a host that is reachable from
> *every* network your clients will run from. `www.cloudflare.com`
> is a common example but is blocked or DNS-poisoned in RU and CN;
> a probe in those locales lands on a dead splice and Reality looks
> broken. `www.microsoft.com`, `apple.com`, or
> `update.microsoft.com` are safer global defaults — they serve real
> Microsoft / Apple infrastructure that no censor blocks without
> visible collateral damage. The TLS-layer cover is identical
> either way.

The first time you run `veil serve`, a fresh static keypair is
generated at `static_key_path`. The user database is created on
demand the first time `veil user add` or `veil admin user-create`
runs against `user_db_path`.

### 2b. Create your admin login

```bash
veil admin user-create \
  --db /var/lib/veil/users.db \
  --username root
# (interactive password prompt)
```

### 2c. Start the server

```bash
veil serve --config /etc/veil/server.yaml
```

You should see:

```
INFO server static key ready public_key_b64=…
INFO user store opened path=/var/lib/veil/users.db active_users=0
INFO listening transport=reality addr=0.0.0.0:443
```

Copy the `public_key_b64` line — clients need it to authenticate
the server.

### 2d. Start the admin endpoint (optional)

The admin HTTP server is a separate process so you can put it on
its own systemd unit, behind a different network policy.

```bash
veil admin serve \
  --db /var/lib/veil/users.db \
  --addr 127.0.0.1:8443
```

The admin endpoint binds to `127.0.0.1` by default. Reach it from
your laptop with an SSH local-forward:

```bash
ssh -L 8443:127.0.0.1:8443 youruser@your-vps
# then point a browser at https://localhost:8443/  (or http while
# the embedded TLS story is still pending)
```

The browser will challenge for HTTP Basic credentials — use the
admin login you created in step 2b.

---

## 3. Register a client

### 3a. On the client machine, get its public key

The client generates a keypair the first time it tries to connect.
You can also generate one offline by starting the client once with
a placeholder server entry — the first log line prints the public
key:

```
INFO client static key ready public_key_b64=BASE64STRINGHERE …
```

Save that base64 string.

### 3b. Add the client to the server's user store

From the server (or from the admin Web UI):

```bash
veil user add \
  --db /var/lib/veil/users.db \
  --name alice \
  --pubkey 'BASE64STRINGFROMTHECLIENT'
```

Optional knobs:

```bash
veil user set-quota   --db … --bytes 5368709120 <id>   # 5 GB / month
veil user set-expiry  --db … --at 2026-12-31T23:59:59Z <id>
veil user revoke      --db … <id>
veil user restore     --db … <id>
veil user list        --db …
```

### 3c. Generate a client-side YAML config

Either hand-write `client.yaml` or have the server emit one for you:

```bash
veil user show-config \
  --db /var/lib/veil/users.db \
  --server-pubkey "PUBLIC_KEY_FROM_STEP_2C" \
  --server-addr   "your-vps.example.com:443" \
  --transport     reality \
  --sni           "www.microsoft.com" \
  <user-id>
```

Paste the output into `client.yaml` on the client machine.

### 3d. Connect

```bash
veil connect --config client.yaml
```

You should see:

```
INFO transport connected transport=reality remote=…
INFO session established
INFO socks5 listening addr=127.0.0.1:1080
```

Test it:

```bash
curl --proxy socks5h://127.0.0.1:1080 https://example.com
```

Configure your browser to use `socks5h://127.0.0.1:1080` as a SOCKS
proxy and you are done.

---

## 4. Operating the server

### Quotas

Per-user monthly byte quotas reset 30 days after each user's
`quota_period_start`. Reset is lazy: it happens the next time
`veil user list` (or any other store-touching command) runs against
a user whose period has expired. (A scheduled flush is on the
Phase 4 roadmap.)

### Backups

Back up the entire `/var/lib/veil/` directory. It contains:

- `server.key` — the long-term Noise XK static keypair. **Lose
  this and every client must update its `server_static_key_b64`.**
- `users.db` — the SQLite user store and admin logins.
- `users.db-wal`, `users.db-shm` — SQLite WAL companions; back up
  alongside the main file.

### Updating

```bash
git pull
go build -o /usr/local/bin/veil ./cmd/veil
systemctl restart veil veil-admin
```

### Troubleshooting

- **`veil: user: --db path is required`** — pass `--db PATH` to the
  CLI subcommand. Subcommand-level flags do not inherit from parent
  commands in this version of `urfave/cli/v3`.
- **`reality transport: TargetSNI is required`** — the server config
  is missing `target_sni`. Reality can not splice probe traffic
  without a real origin to point them at.
- **`actively refused` from a freshly-started client** — the server
  bound the QUIC port but you are dialling its TCP port (or vice
  versa). UDP is the QUIC transport; TCP is WSS / Reality.
- **`unauthorized` in server logs** — the client's public key is
  not in the user store. Add it with `veil user add`.

---

## 5. What is not in this release

- The Tauri GUI installer that does steps 1–3 in a single window.
- Automatic ACME (Let's Encrypt) certificate provisioning for WSS.
- iOS / Android client apps.
- A mechanism for the server to push user revocations to the
  database without a process restart.

These land in subsequent phases. See the [PRD roadmap](../PRD.md#18-roadmap)
for the order.
