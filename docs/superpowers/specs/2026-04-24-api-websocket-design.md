# gNb Panel API (WebSocket) — Design Spec

**Date:** 2026-04-24
**Status:** Approved by user, ready for implementation plan

## 1. Goal

Give the gNb bot runner ("node" / "instance") a live control plane that a browser panel (bun + TypeScript + Tailwind + Vite) can connect to. One panel can connect to many nodes concurrently, each via its own WebSocket. The API must expose every operation an IRC owner has through DCC (and slightly more: per-bot `change_nick`, `raw`), plus live lifecycle events (bot connected, nick captured, etc.). Designed to be served behind cloudflared (plain HTTP upstream, cloudflared terminates TLS as `wss://`), with optional native TLS when operator runs standalone.

## 2. Terminology

- **Node / instance** — single `gNb` process. Exposes one API endpoint.
- **Bot** — single IRC connection inside a node (one entry in `cfg.Bots`). Multiple bots per node.
- **Panel** — web client (separate project, out of scope for this spec — API must accommodate it).

## 3. High-level architecture

```
gNb node (process)
├── BotManager ── Bot[0..N] (IRC connections via go-ircevo)
├── NickManager (ISON round-robin)
└── internal/api (NEW)
     ├── HTTP server (net/http) bound to api.bind_addr
     ├── WebSocket upgrade (github.com/coder/websocket v1.8.14)
     ├── Session auth (constant-time token compare)
     ├── Dispatch: method name → handler
     ├── EventHub (ring buffer + subscriber fan-out)
     └── AttachManager (per-bot IRC firehose subscribers)
```

API runs as an additional goroutine alongside BotManager. It does **not** modify DCC or BNC — those remain independent control channels.

Panel opens one WS per node. Multi-node aggregation is entirely panel-side.

## 4. Configuration

New `api:` section in `configs/config.yaml` (all optional, backwards-compatible):

```yaml
api:
  enabled: false                   # master switch; default off
  node_name: "catcher-dc1"         # human-readable, shown in UI
  bind_addr: "127.0.0.1:7766"      # default 127.0.0.1:7766
  auth_token: ""                   # REQUIRED when enabled; >= 32 chars
  tls_cert_file: ""                # optional; empty = plain HTTP (behind cloudflared)
  tls_key_file: ""                 # optional
  event_buffer: 1000               # ring buffer size for lifecycle replay
  max_connections: 4               # max concurrent authenticated sessions
```

- **Auto-scaffolding:** `CheckAndCreateConfigFiles` adds a commented-out `api:` block with a freshly-generated 64-hex token on first run. Operator flips `enabled: true` manually.
- **Persistent node_id:** `data/node_id` holds a UUID v4, auto-generated on first API start. Used in every event payload for multi-node disambiguation in panel.
- **Startup wiring:** in `cmd/main.go`, after `go botManager.StartBots()`, if `cfg.API.Enabled`, start API server as a goroutine. Shutdown joined with SIGTERM handler (graceful 5 s drain).

## 5. Node / bot identification

- **Node:**
  - `node_name` — from `api.node_name` (not unique; human-friendly).
  - `node_id` — UUID v4 persisted in `data/node_id` (unique, stable across restarts).
  - Both sent in `auth.login` response and every `event` payload (`node_id` only in events to keep volume down; name is stable per session).

- **Bot (`bot_id`):** `sha1("{server}:{port}:{vhost}:{index}")[:12]` where `index` is the position in `cfg.Bots`. Deterministic from YAML alone, stable across restarts, unique even when multiple bots share `{server, port, vhost}` (distinguished by index). No user-supplied `bot_id` field.

## 6. Wire protocol

JSON over WebSocket text frames. Single envelope with `type` discriminator — TypeScript consumes a discriminated union. Max message size 64 KB.

### 6.1 Envelope types

```typescript
type Msg = RequestMsg | ResponseMsg | ErrorMsg | EventMsg;

interface RequestMsg {
  type: "request";
  id: string;              // panel-chosen UUID/ULID
  method: string;          // e.g. "bot.raw"
  params?: Record<string, unknown>;
}

interface ResponseMsg {
  type: "response";
  id: string;              // echoes request.id
  ok: true;
  result: unknown;
}

interface ErrorMsg {
  type: "error";
  id?: string;             // present for request-triggered errors
  code: ErrorCode;
  message: string;
}

interface EventMsg {
  type: "event";
  event: string;           // e.g. "bot.nick_changed"
  node_id: string;
  ts: string;              // RFC3339
  seq: number;             // monotonic per-node counter
  data: Record<string, unknown>;
}

type ErrorCode =
  | "unauthorized"
  | "unknown_method"
  | "invalid_params"
  | "not_found"
  | "forbidden"
  | "rate_limited"
  | "cooldown"
  | "internal";
```

### 6.2 Close codes

| Code | Meaning |
|------|---------|
| 1000 | Normal close initiated by panel. |
| 1001 | Node going away (shutdown). |
| 4001 | Authentication failed. |
| 4002 | Too many connections for this node. |
| 4003 | Auth handshake not completed within 5 s. |
| 4004 | Message too large / protocol violation. |

### 6.3 Handshake

Panel MUST send `auth.login` as its first message within 5 s of WS open:

```json
{"type":"request","id":"1","method":"auth.login","params":{"token":"<hex>"}}
```

On success:

```json
{"type":"response","id":"1","ok":true,"result":{
  "node_id":"<uuid>",
  "node_name":"catcher-dc1",
  "api_version":"1.0",
  "session_id":"<uuid>"
}}
```

On failure → `{"type":"error","id":"1","code":"unauthorized","message":"invalid token"}` then close 4001.

### 6.4 Keepalive

- Server sends WS ping every 30 s. Read deadline 45 s. On violation → close 1001.
- `coder/websocket`'s `Conn.Ping(ctx)` and read-deadline on context handle this natively.

## 7. Method catalog

All methods require prior `auth.login` success (otherwise `forbidden`).

### 7.1 Auth

| Method | Params | Result |
|--------|--------|--------|
| `auth.login` | `{token}` | `{node_id, node_name, api_version, session_id}` |

### 7.2 Read-only

| Method | Params | Result |
|--------|--------|--------|
| `node.info` | — | `{node_id, node_name, version, uptime_seconds, pid, started_at, num_bots, num_connected_bots}` |
| `bot.list` | — | `{bots: BotSummary[]}` |
| `nicks.list` | — | `{nicks: string[]}` |
| `owners.list` | — | `{owners: string[]}` |

`BotSummary = {bot_id, server, port, ssl, vhost, current_nick, connected, is_single_letter_nick, joined_channels: string[]}`

### 7.3 Single-bot control

| Method | Params | Result | Notes |
|--------|--------|--------|-------|
| `bot.say` | `{bot_id, target, message}` | `{ok:true}` | PRIVMSG via `bot.SendMessage` |
| `bot.join` | `{bot_id, channel}` | `{ok:true}` | — |
| `bot.part` | `{bot_id, channel}` | `{ok:true}` | — |
| `bot.quit` | `{bot_id, reason?}` | `{ok:true}` | Sends IRC QUIT via `bot.Quit(reason)`. Library handles clean disconnect; `bot.disconnected` event follows. Manager cleanup of dead connections happens per existing `cleanupDisconnectedBots` logic. |
| `bot.reconnect` | `{bot_id}` | `{ok:true}` | Calls `Bot.Reconnect()`. |
| `bot.change_nick` | `{bot_id, new_nick}` | `{ok:true}` | `bot.ChangeNick(new_nick)` (fire-and-forget; result arrives via `bot.nick_changed` event). |
| `bot.raw` | `{bot_id, line}` | `{ok:true}` | `bot.SendRaw(line)`. No sanitization beyond length/CR/LF stripping. |
| `bot.attach` | `{bot_id}` | `{attach_id}` | Subscribes session to IRC firehose for this bot. |
| `bot.detach` | `{bot_id}` | `{ok:true}` | Unsubscribes. Also happens automatically on session close. |

### 7.4 Node-wide mass control

Respects existing `mass_command_cooldown` where applicable (join/part/reconnect).

| Method | Params | Result | Notes |
|--------|--------|--------|-------|
| `node.mass_join` | `{channel}` | `{ok:true, affected}` | cooldown-gated |
| `node.mass_part` | `{channel}` | `{ok:true, affected}` | cooldown-gated |
| `node.mass_reconnect` | — | `{ok:true, affected}` | cooldown-gated |
| `node.mass_raw` | `{line}` | `{ok:true, affected}` | — |
| `node.mass_say` | `{target, message}` | `{ok:true, affected}` | — |

`affected` = number of bots the command was dispatched to. On cooldown → `error.code = "cooldown"`.

### 7.5 Persisted admin state

| Method | Params | Result |
|--------|--------|--------|
| `nicks.add` | `{nick}` | `{ok:true}` |
| `nicks.remove` | `{nick}` | `{ok:true}` |
| `owners.add` | `{mask}` | `{ok:true}` |
| `owners.remove` | `{mask}` | `{ok:true}` |
| `bnc.start` | `{bot_id}` | `{port, password, ssh_command}` |
| `bnc.stop` | `{bot_id}` | `{ok:true}` |

### 7.6 Events

| Method | Params | Result |
|--------|--------|--------|
| `events.subscribe` | `{topics?: string[], replay_last?: number}` | `{cursor, replayed}` |
| `events.unsubscribe` | — | `{ok:true}` |

- `topics` is an allowlist. Omit → all default-stream topics.
- `replay_last` copies the last N matching events from the ring buffer immediately before switching to live stream. Default 0.
- `cursor` = current `seq` at subscribe time.
- `events.unsubscribe` drops the session's filter; attach streams are independent and keep flowing.

## 8. Event catalog

Events are delivered only to sessions that called `events.subscribe`. Attach events are additionally gated by `bot.attach` on the matching `bot_id`.

### 8.1 Default stream (lifecycle)

| event | data |
|-------|------|
| `bot.connected` | `{bot_id, nick, server}` |
| `bot.disconnected` | `{bot_id, reason?}` |
| `bot.nick_changed` | `{bot_id, old, new}` |
| `bot.nick_captured` | `{bot_id, nick, kind:"letter"\|"priority"}` |
| `bot.joined_channel` | `{bot_id, channel}` (self-join only) |
| `bot.parted_channel` | `{bot_id, channel}` (self-part only) |
| `bot.kicked` | `{bot_id, channel, by, reason}` |
| `bot.banned_from_server` | `{bot_id, code:465\|466}` |
| `node.bot_added` | `{bot_id, config:{server,port,ssl,vhost}}` |
| `node.bot_removed` | `{bot_id}` |
| `nicks.changed` | `{nicks}` |
| `owners.changed` | `{owners}` |

### 8.2 Attach stream (per `bot.attach`)

All events carry `{bot_id, ...}`. Delivered only when the session is attached to that bot.

| event | data |
|-------|------|
| `bot.attach.privmsg` | `{from:{nick,user,host}, target, text}` |
| `bot.attach.notice` | `{from, target, text}` |
| `bot.attach.join` | `{who:{nick,user,host}, channel}` |
| `bot.attach.part` | `{who, channel, reason?}` |
| `bot.attach.quit` | `{who, reason?}` |
| `bot.attach.kick` | `{by:{nick,user,host}, channel, target, reason}` |
| `bot.attach.mode` | `{from, target, modes, args}` |
| `bot.attach.topic` | `{from, channel, topic}` |
| `bot.attach.nick` | `{from:{nick,user,host}, new_nick}` |
| `bot.attach.ctcp` | `{from, target, command, args}` |
| `bot.attach.raw_in` | `{line}` |
| `bot.attach.raw_out` | `{line}` |

### 8.3 Session-local events

Not broadcast — emitted only to the single session they concern.

| event | data |
|-------|------|
| `api.backpressure` | `{dropped_count}` — emitted after a session's outbound queue recovers from overflow, reporting how many events were dropped. |

## 9. Threading / backpressure / shutdown

- **Per session:** one read goroutine, one write goroutine, one outbound channel `chan EventMsg` buffered 256.
- **Outbound overflow policy:** if buffer full, drop the event, bump session-local counter. When backpressure clears (channel drained below half), emit one informational event to that session (`api.backpressure` — see §8.3). Never block the EventHub on a slow panel.
- **EventHub:**
  - Single source publishing to ring buffer + all subscriber channels.
  - Subscriber list protected by `sync.RWMutex`; read-locked during fan-out, write-locked on subscribe/unsubscribe.
  - Ring buffer fixed-size, `seq` is `atomic.Uint64`.
- **AttachManager:** `map[bot_id]map[session_id]struct{}`. Hooks into Bot's registered IRC callbacks (added as additional callbacks; does not alter existing ones). Panel-detach and session-close both clean up.
- **Auth rate-limit:** max 5 failed `auth.login` per remote IP per 60 s. 6th attempt → immediate close 4001.
- **Graceful shutdown:** `http.Server.Shutdown(ctx)` with 5 s timeout. Sessions see close 1001 "going away".

## 10. Security posture

- Token compared with `subtle.ConstantTimeCompare`.
- Token length validated at load time (>= 32 chars); refuse to start API with a weaker token.
- Bind defaults to `127.0.0.1` — operator must intentionally open it up.
- CORS: since this is pure WebSocket + panel is a separate origin served by cloudflared, we allow any origin for the upgrade (token auth is the gate). Origin check optionally enforced via `api.allowed_origins: [...]` if set (YAGNI for now, not in v1).
- No logging of the token. Log only `session_id` + remote address for audit.
- TLS path: when `tls_cert_file` + `tls_key_file` both set, use `ListenAndServeTLS`. If only one set → refuse to start.

## 11. Testing strategy

- **Unit tests** for:
  - `bot_id` computation (determinism across reorderings).
  - Envelope JSON marshal/unmarshal round-trip.
  - Dispatch: happy path + error codes per method.
  - EventHub: ring buffer replay, subscriber fan-out, slow consumer drop.
  - Auth: constant-time compare, rate-limit.
- **Integration tests** (`httptest` + real `coder/websocket` client):
  - Handshake flow (success, bad token, timeout).
  - Full request/response for each method against fake BotManager/NickManager.
  - Attach stream: events reach only attached sessions.
  - Replay: subscribe with `replay_last:N` gets last N events before live.
  - Shutdown: server.Shutdown closes sessions with 1001.

## 12. File layout

```
internal/api/
  config.go        — APIConfig struct + validate()
  server.go        — http.Server boot, TLS selection, Run/Shutdown
  session.go       — per-WS session lifecycle, read/write loops
  protocol.go      — envelope types + codes
  dispatch.go      — method → handler map
  handlers_auth.go — auth.login
  handlers_node.go — node.info, mass_*
  handlers_bot.go  — bot.* single control
  handlers_list.go — bot.list, nicks.list, owners.list
  handlers_admin.go — nicks.add/remove, owners.add/remove, bnc.*
  handlers_events.go — events.subscribe/unsubscribe, bot.attach/detach
  events.go        — EventHub + ring buffer
  attach.go        — AttachManager + IRC callback hooks
  auth.go          — token compare + rate-limit
  bot_id.go        — computeBotID
  *_test.go        — per-file unit/integration tests
```

Plus:
- `internal/config/config.go` — add `APIConfig`.
- `internal/types/interfaces.go` — extend `Bot`/`BotManager` if needed.
- `cmd/main.go` — wire API startup/shutdown.
- `configs/config.example.yaml` — document the `api:` block.

## 13. Out of scope (v1)

- Panel itself (separate project).
- Multi-tenant auth / per-user permissions (single god-mode token by design).
- Persistent event log (ring buffer only).
- Metrics/Prometheus (can be added later).
- Origin allowlist (YAGNI; cloudflared already gates).

## 14. Success criteria

- `go build ./...` clean.
- `go vet ./...` clean.
- `gofmt -l` returns empty.
- `go test -race ./...` passes for new tests and existing suite.
- Manual smoke: `./gNb -dev` with `api.enabled: true`, connect via `websocat` (or equivalent), run `auth.login` → `bot.list` → `events.subscribe` → observe live events as bots connect/change nicks.
