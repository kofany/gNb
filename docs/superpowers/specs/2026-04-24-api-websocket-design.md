# gNb Panel API (WebSocket) — Design Spec

**Date:** 2026-04-24
**Status:** Implemented and verified (see `internal/api/`).

## 1. Goal

Give the gNb bot runner ("node" / "instance") a live control plane that a browser panel (bun + TypeScript + Tailwind + Vite) can connect to. One panel can connect to many nodes concurrently, each via its own WebSocket. The API exposes every operation an IRC owner has through DCC (and slightly more: per-bot `change_nick`, `raw`), plus live lifecycle events (bot connected, nick captured, etc.) and an opt-in attach pipe for full IRC firehose per bot. Designed to be served behind cloudflared (plain HTTP upstream, cloudflared terminates TLS as `wss://`), with optional native TLS when operator runs standalone.

## 2. Terminology

- **Node / instance** — single `gNb` process. Exposes one API endpoint.
- **Bot** — single IRC connection inside a node (one entry in `cfg.Bots`). Multiple bots per node.
- **Panel** — web client (separate project, out of scope for this spec — API must accommodate it).

## 3. High-level architecture

```
gNb node (process)
├── BotManager ── Bot[0..N] (IRC connections via go-ircevo v1.2.4)
├── NickManager (ISON round-robin)
└── internal/api (NEW)
     ├── HTTP server (net/http) bound to api.bind_addr
     ├── WebSocket upgrade (github.com/coder/websocket v1.8.14)
     ├── Session auth (constant-time token compare + per-IP rate limit)
     ├── Dispatch: method name → handler
     ├── EventHub (ring buffer + subscriber fan-out)
     ├── AttachManager (per-bot IRC firehose subscribers)
     └── serverSink (implements types.EventSink — receives lifecycle/IRC
                     events from BotManager / Bot / NickManager and routes
                     them to EventHub / AttachManager)
```

API runs as additional goroutines alongside BotManager. It does **not** modify DCC or BNC — those remain independent control channels.

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

- **Validation:** `APIConfig.Validate()` runs at config load. When `enabled: true`:
  - `auth_token` must be ≥ 32 characters.
  - `bind_addr` must parse as `host:port`.
  - `tls_cert_file` and `tls_key_file` must both be set together or both be empty.
- **Defaults:** `APIConfig.ApplyDefaults()` runs unconditionally — `bind_addr` → `127.0.0.1:7766`, `event_buffer` → `1000`, `max_connections` → `4`, `node_name` → `"gnb-node"`.
- **Auto-scaffolding:** `CheckAndCreateConfigFiles` writes a commented-out `api:` block with a freshly-generated 64-hex token on first run. Operator un-comments and flips `enabled: true` manually.
- **Persistent node_id:** `data/node_id` holds a 32-character random hex string (16 bytes from `crypto/rand`), auto-generated on first API start. Used in every event payload for multi-node disambiguation in panel.
- **Startup wiring:** in `cmd/main.go`, the API server is constructed and `BotManager.SetEventSink(apiSrv.Sink())` is invoked **before** the StartBots goroutine spawns. This ordering is load-bearing: any bot that completes its IRC handshake before the sink is installed would otherwise drop its first `bot.connected` event. Shutdown joined with SIGTERM handler (graceful 5 s drain via `http.Server.Shutdown`).

## 5. Node / bot identification

- **Node:**
  - `node_name` — from `api.node_name` (not unique; human-friendly).
  - `node_id` — 32-character lowercase hex string persisted in `data/node_id` (unique, stable across restarts). Generated from 16 random bytes; not a true UUID v4 (no version/variant bits) but treated as opaque by the panel.
  - Both sent in `auth.login` response. Every `event` payload carries `node_id` (panel uses it to demux events from multiple connected nodes).

- **Bot (`bot_id`):** `sha1("{server}:{port}:{vhost}:{index}")[:12]` where `index` is the position in `cfg.Bots`. Deterministic from YAML alone, stable across restarts, unique even when multiple bots share `{server, port, vhost}` (distinguished by index). No user-supplied `bot_id` field. The same algorithm is reproduced in both `internal/api/bot_id.go` (`ComputeBotID`) and `internal/bot/bot_id.go` (`computeBotID`) to avoid an import cycle.

## 6. Wire protocol

JSON over WebSocket text frames. Single envelope with `type` discriminator — TypeScript consumes a discriminated union. Max message size 64 KB (set via `Conn.SetReadLimit`).

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
  ts: string;              // RFC3339Nano
  seq: number;             // monotonic per-node uint64 counter
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
| 1008 | Backpressure — outbound queue overflow (StatusPolicyViolation). |
| 4001 | Authentication failed. |
| 4002 | (reserved — too many connections; currently rejected with HTTP 503 before WS upgrade). |
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
  "node_id":"<32-hex>",
  "node_name":"catcher-dc1",
  "api_version":"1.0",
  "session_id": 7
}}
```

`session_id` is a per-node monotonically-increasing uint64 (rendered by JSON as a number, not a string). It is informational — the panel does not need to echo it in subsequent requests; the WebSocket connection itself is the session.

On failure → `{"type":"error","id":"1","code":"unauthorized","message":"invalid token"}` then close 4001.

If the panel sends a request other than `auth.login` first → `{"type":"error","id":"<that>","code":"forbidden","message":"first message must be auth.login"}` then close 4001.

If 5 s pass with no message → close 4003.

### 6.4 Keepalive

- Server sends WS ping every 30 s (`Conn.Ping`).
- Server-side read deadline 45 s; if no inbound frame arrives in that window, the read errors and the session ends.
- `coder/websocket`'s context-aware Read/Write/Ping handle the cancellation cleanly.

## 7. Method catalog

All methods require prior `auth.login` success (otherwise the dispatcher returns `forbidden`). A second `auth.login` after success returns `forbidden` with message "already authenticated".

### 7.1 Auth

| Method | Params | Result |
|--------|--------|--------|
| `auth.login` | `{token}` | `{node_id, node_name, api_version, session_id}` |

### 7.2 Read-only

| Method | Params | Result |
|--------|--------|--------|
| `node.info` | — | `{node_id, node_name, api_version, version, pid, uptime_seconds, num_bots, num_connected_bots, started_at}` |
| `bot.list` | — | `{bots: BotSummary[]}` |
| `nicks.list` | — | `{nicks: string[]}` |
| `owners.list` | — | `{owners: string[]}` |

`BotSummary = {bot_id, server, port, ssl, vhost, current_nick, connected, is_single_letter_nick, joined_channels: string[]}`

`joined_channels` reflects the bot's actual joined set (including auto-joined channels like `#literki` for single-letter nicks), sourced via `Bot.GetJoinedChannels()` — not just the intersection with the configured channel list.

### 7.3 Single-bot control

| Method | Params | Result | Notes |
|--------|--------|--------|-------|
| `bot.say` | `{bot_id, target, message}` | `{ok:true}` | PRIVMSG via `Bot.SendMessage`. Emits `bot.attach.raw_out` with `PRIVMSG <target> :<message>`. |
| `bot.join` | `{bot_id, channel}` | `{ok:true}` | `Bot.JoinChannel`. Emits `bot.attach.raw_out` with `JOIN <channel>`. |
| `bot.part` | `{bot_id, channel}` | `{ok:true}` | `Bot.PartChannel`. Emits `bot.attach.raw_out` with `PART <channel>`. |
| `bot.quit` | `{bot_id, reason?}` | `{ok:true}` | `Bot.Quit(reason or "API quit")`. Library handles clean disconnect; `bot.disconnected` event follows. Manager cleanup of dead connections happens per existing `cleanupDisconnectedBots` logic. |
| `bot.reconnect` | `{bot_id}` | `{ok:true}` | Spawns `Bot.Reconnect()` in a goroutine. |
| `bot.change_nick` | `{bot_id, new_nick}` | `{ok:true}` | `Bot.ChangeNick(new_nick)` (fire-and-forget; result arrives via `bot.nick_changed` event). Emits `bot.attach.raw_out` with `NICK <new_nick>`. |
| `bot.raw` | `{bot_id, line}` | `{ok:true}` | `Bot.SendRaw(line)`. CR/LF stripped to prevent IRC command injection. Length bounded only by 64 KB WS read limit. Emits `bot.attach.raw_out` with the (sanitized) line. |
| `bot.attach` | `{bot_id}` | `{attach_id}` | Subscribes session to attach pipe for this bot. `attach_id` is informational. |
| `bot.detach` | `{bot_id}` | `{ok:true}` | Unsubscribes from this bot's attach pipe. Auto-fires for all attached bots on session close. |

### 7.4 Node-wide mass control

Respects existing `mass_command_cooldown` (from `global.mass_command_cooldown` seconds) for `join`/`part`/`reconnect` — the same cooldown machinery the IRC and DCC paths use.

| Method | Params | Result | Notes |
|--------|--------|--------|-------|
| `node.mass_join` | `{channel}` | `{ok:true, affected}` | cooldown-gated under name `"join"` |
| `node.mass_part` | `{channel}` | `{ok:true, affected}` | cooldown-gated under name `"part"` |
| `node.mass_reconnect` | — | `{ok:true, affected}` | cooldown-gated under name `"reconnect"` |
| `node.mass_raw` | `{line}` | `{ok:true, affected}` | CR/LF stripped. Not cooldown-gated. |
| `node.mass_say` | `{target, message}` | `{ok:true, affected}` | Not cooldown-gated. |

`affected` = number of bots the command was dispatched to. On cooldown → `error.code = "cooldown"`, no bots receive the command.

### 7.5 Persisted admin state

| Method | Params | Result |
|--------|--------|--------|
| `nicks.add` | `{nick}` | `{ok:true}` |
| `nicks.remove` | `{nick}` | `{ok:true}` |
| `owners.add` | `{mask}` | `{ok:true}` |
| `owners.remove` | `{mask}` | `{ok:true}` |
| `bnc.start` | `{bot_id}` | `{port, password, ssh_command}` |
| `bnc.stop` | `{bot_id}` | `{ok:true}` |

`bnc.start` returns a ready-to-paste `ssh -p <port> <nick>@<host> <password>` command. IPv6 vhosts are bracketed (`ssh -p X user@[2001:db8::1] pw`).

`nicks.{add,remove}` and `owners.{add,remove}` persist to disk (`data/nicks.json` / `configs/owners.json`) and emit `nicks.changed` / `owners.changed` events to all subscribers.

### 7.6 Events

| Method | Params | Result |
|--------|--------|--------|
| `events.subscribe` | `{topics?: string[], replay_last?: number}` | `{cursor, replayed}` |
| `events.unsubscribe` | — | `{ok:true}` |

- `topics` is an allowlist filtered against the `event` field. Omit → all default-stream topics.
- `replay_last` copies the last N matching events from the ring buffer immediately before switching to live stream. Default 0. **Capped at 128** to prevent the synchronous flush from overflowing the session outbound queue (which would close the connection for backpressure).
- `cursor` = current `seq` at subscribe time (informational).
- `replayed` = actual number of events sent (≤ requested cap, ≤ ring-buffer contents).
- `events.unsubscribe` drops the session's lifecycle filter; attach streams are independent and keep flowing.
- Calling `events.subscribe` twice replaces the previous subscription (the old ring-buffer subscriber is closed and a new one is created).

## 8. Event catalog

Lifecycle events are delivered to any session that called `events.subscribe`. Attach events are additionally gated by `bot.attach` on the matching `bot_id`.

### 8.1 Default stream (lifecycle)

| event | data | when |
|-------|------|------|
| `bot.connected` | `{bot_id, nick, server}` | IRC 001 welcome received. |
| `bot.disconnected` | `{bot_id, reason}` | IRC `DISCONNECTED` callback. |
| `bot.nick_changed` | `{bot_id, old, new}` | Self NICK confirmed by server. |
| `bot.nick_captured` | `{bot_id, nick, kind:"letter"\|"priority"}` | Self NICK to a single letter (`"letter"`) or to a name on the priority list (`"priority"`). |
| `bot.joined_channel` | `{bot_id, channel}` | Self JOIN confirmed. |
| `bot.parted_channel` | `{bot_id, channel}` | Self PART confirmed. |
| `bot.kicked` | `{bot_id, channel, by, reason}` | Self KICK; `by` is the kicker nick. |
| `bot.banned_from_server` | `{bot_id, code:465\|466}` | IRC numerics 465 (banned) / 466 (will be banned). |
| `node.bot_added` | `{bot_id, config:{server, port, ssl, vhost}}` | Emitted once per bot the first time `BotManager.SetEventSink` is called with a non-nil sink. (Bots are static; there is no runtime add path today.) |
| `node.bot_removed` | `{bot_id}` | `BotManager.RemoveBotFromManager` succeeded. |
| `nicks.changed` | `{nicks: string[]}` | `NickManager.AddNick` / `RemoveNick` after disk persistence. |
| `owners.changed` | `{owners: string[]}` | `BotManager.AddOwner` / `RemoveOwner` after disk persistence. |

### 8.2 Attach stream (per `bot.attach`)

All payloads carry `{bot_id, ...the listed fields...}`. Delivered only to sessions attached to that `bot_id`.

| event | data |
|-------|------|
| `bot.attach.privmsg` | `{from:{nick,user,host}, target, text}` |
| `bot.attach.notice` | `{from:{nick,user,host}, target, text}` |
| `bot.attach.join` | `{who:{nick,user,host}, channel}` |
| `bot.attach.part` | `{who:{nick,user,host}, channel, reason}` |
| `bot.attach.quit` | `{who:{nick,user,host}, reason}` |
| `bot.attach.kick` | `{by:{nick,user,host}, channel, target, reason}` |
| `bot.attach.mode` | `{from:{nick,user,host}, target, args:string[]}` — `args` includes the mode string + parameters as the IRC server sent them after the target. |
| `bot.attach.topic` | `{from:{nick,user,host}, channel, topic}` |
| `bot.attach.nick` | `{from:{nick,user,host}, new_nick}` |
| `bot.attach.ctcp` | `{from:{nick,user,host}, target, command, text}` — `command` is the IRC event code (`CTCP`, `CTCP_ACTION`, `CTCP_VERSION`). |
| `bot.attach.raw_in` | `{line}` — emitted for **every** inbound IRC event (alongside any high-level event above). |
| `bot.attach.raw_out` | `{line}` — emitted from `Bot.SendMessage`, `Bot.SendRaw`, `Bot.JoinChannel`, `Bot.PartChannel`, `Bot.ChangeNick`. **Not** emitted for library-internal writes (PING/PONG/auto-NICK on reconnect handshake) — those are opaque to the bot package. |

## 9. Threading / backpressure / shutdown

- **Per session:** one read goroutine, one write goroutine, one outbound channel `chan any` buffered 256 (`outboundBuffer`), plus an optional sub-pump goroutine for the lifecycle event subscription.
- **Outbound overflow policy:** if the session's outbound channel is full, the WebSocket is closed with `StatusPolicyViolation` (1008). A consumer that cannot keep up is treated as broken; the panel is expected to reconnect and, if it wants history, replay via `events.subscribe({replay_last: N})`. Never block the EventHub on a slow panel. The `events.subscribe` replay is itself capped at 128 events for the same reason.
- **EventHub:**
  - Single source publishing to ring buffer + all subscriber channels.
  - Subscriber list protected by `sync.RWMutex`; read-locked during fan-out, write-locked on subscribe/unsubscribe.
  - Ring buffer is a fixed-size `[]EventMsg` with FIFO eviction; `seq` is `atomic.Uint64`.
  - `Replay(sub, n)` walks newest-to-oldest collecting the last N matching events, then reverses to chronological order — O(n) total.
- **AttachManager:** `map[bot_id]map[session_id]struct{}`. Receives events via `serverSink.BotIRCEvent` (which is wired into Bot's existing `*` wildcard callback) and `serverSink.BotRawOut` (called from outbound paths in Bot). Panel-detach and session-close both clean up via `Detach` / `DetachAll`.
- **Connection limit:** `handleWS` reserves a slot in `activeN` via a CAS loop before upgrading. Past limit → HTTP 503 "too many connections". The defer always refunds the slot on return.
- **Auth rate-limit:** max 5 failed `auth.login` per remote IP per 60 s sliding window. 6th attempt → `error.code = "rate_limited"`, then close 4001.
- **Graceful shutdown:** `http.Server.Shutdown(ctx)` with 5 s timeout. Open sessions get close 1001 "going away".

## 10. Security posture

- Token compared with `subtle.ConstantTimeCompare`.
- Token length validated at config load (≥ 32 chars); refuse to start API with a weaker token.
- Bind defaults to `127.0.0.1` — operator must intentionally open it up by editing the config.
- **Origin / CORS:** `websocket.AcceptOptions.InsecureSkipVerify: true` — Origin header is not checked. Token auth is the gate. This is deliberate: the typical deployment (cloudflared in front) sets a public hostname, and origin restrictions would have to track which panel domain(s) are allowed. A future `api.allowed_origins` knob is YAGNI for v1.
- No logging of the token. Audit log entries record `session_id` + remote address only.
- TLS path: when `tls_cert_file` AND `tls_key_file` both set, use `ListenAndServeTLS`. If only one set → config validation refuses to start. Empty pair = plain HTTP (intended for cloudflared upstream).

## 11. EventSink interface

The bridge between the `internal/bot` / `internal/nickmanager` packages and the API is the `types.EventSink` interface (declared in `internal/types/interfaces.go`):

```go
type EventSink interface {
    BotConnected(botID, nick, server string)
    BotDisconnected(botID, reason string)
    BotNickChanged(botID, oldNick, newNick string)
    BotNickCaptured(botID, nick, kind string)         // kind: "letter" | "priority"
    BotJoinedChannel(botID, channel string)
    BotPartedChannel(botID, channel string)
    BotKicked(botID, channel, by, reason string)
    BotBanned(botID string, code int)                  // 465 | 466
    BotAdded(botID, server string, port int, ssl bool, vhost string)
    BotRemoved(botID string)
    NicksChanged(nicks []string)
    OwnersChanged(owners []string)
    BotIRCEvent(botID string, e *irc.Event)            // every inbound IRC line
    BotRawOut(botID, line string)                      // every outbound line we can hook
}
```

`BotManager`, `Bot` and `NickManager` carry an optional `sink` (nil = observation off). The API package implements `EventSink` as `serverSink` (`internal/api/sink.go`) which routes lifecycle methods to `EventHub.Publish` and IRC-firehose methods to `AttachManager.Publish`. `BotManager.SetEventSink` fans the sink out to every bot and to NickManager, and the first non-nil install triggers a `BotAdded` per bot so a panel subscribing at boot gets the full roster without a separate `bot.list` call.

## 12. Testing strategy

**Unit tests (existing in `internal/api/`):**
- `bot_id_test.go` — determinism, duplicate disambiguation, server-tuple distinguishing.
- `node_id_test.go` — create-and-persist; regenerate-on-corrupt-file.
- `protocol_test.go` — envelope JSON round-trip, decode error paths.
- `events_test.go` — publish/subscribe; topic filter; ring-buffer eviction; backpressure drop count; concurrent publish under `-race`.
- `auth_test.go` — match/reject/per-IP-rate-limit.
- `config_test.go` (in `internal/config/`) — `APIConfig` defaults and validation table.

**Integration tests (`httptest.Server` + real `coder/websocket` client):**
- Handshake: success, bad token, timeout, forbidden-before-auth.
- Per method: bot.list, nicks.list, owners.list, node.info, bot.raw (with CR/LF sanitization), bot.change_nick, bot-not-found, bnc.start (incl. IPv6 ssh_command), nicks.add, owners.add.
- Mass: mass_raw broadcast, cooldown rejection.
- Events: subscribe + receive; replay_last with N events pre-published.
- Attach: attach + receive; detach + read timeout (no event).
- BotByID survives BotManager reordering its `bm.bots` slice.
- node.info includes `version` and `pid`.
- End-to-end: subscribe, drive sink methods, observe lifecycle events on the wire.
- Sink: lifecycle methods land on the hub.

## 13. File layout

```
internal/api/
  bot_id.go              — ComputeBotID (sha1[:12])
  node_id.go             — LoadOrCreateNodeID (16 random bytes → 32 hex)
  protocol.go            — RequestMsg / ResponseMsg / ErrorMsg / EventMsg + ErrorCode
  dispatch.go            — Router + decodeParams + paramErr/notFound/internalErr
  events.go              — EventHub + Subscriber (ring buffer + fan-out)
  attach.go              — AttachManager + nowUTC helper
  auth.go                — tokenChecker (constant-time + per-IP rate limit)
  server.go              — Deps, Server, New, registerRoutes, Run, handleWS,
                            BotByID, configForBot, NewAttachEvent, closeAllSessions
  session.go             — Session, handshake, readerLoop, writerLoop, send,
                            startSubPump, attach tracking, close
  sink.go                — serverSink (types.EventSink impl)
  sink_translate.go      — irc.Event → bot.attach.* high-level events
  handlers_core.go       — auth.login, node.info
  handlers_list.go       — bot.list, nicks.list, owners.list
  handlers_bot.go        — bot.{say,join,part,quit,reconnect,change_nick,raw}
                            + sanitizeIRCLine
  handlers_mass.go       — node.mass_{join,part,reconnect,raw,say} + cooldownErr
  handlers_admin.go      — nicks.{add,remove}, owners.{add,remove}, bnc.{start,stop}
  handlers_events.go     — events.{subscribe,unsubscribe} + replayCap
  handlers_attach.go     — bot.{attach,detach} + newAttachID
  *_test.go              — fakes_test, server_test, sink_test, integration_test, etc.
```

Plus:
- `internal/config/config.go` — `APIConfig` struct, defaults, validation, scaffold-on-first-run.
- `internal/config/config_test.go` — `APIConfig` tests.
- `internal/types/interfaces.go` — extended `Bot` with `SetEventSink`, `GetBotID`, `GetJoinedChannels`; extended `BotManager` and `NickManager` with `SetEventSink`; new `EventSink` interface.
- `internal/bot/bot.go` — sink field + `currentSink()` accessor; emit lifecycle and `BotRawOut` events from callbacks and outbound methods.
- `internal/bot/bot_id.go` — duplicate of `ComputeBotID` (acyclic dep).
- `internal/bot/manager.go` — sink field + `SetEventSink` (fans out + emits initial `BotAdded`); refactored `AddOwner`/`RemoveOwner` to release `bm.mutex` before disk write to avoid self-deadlock; `BotRemoved` emission.
- `internal/nickmanager/nickmanager.go` — sink field + `SetEventSink`; `NicksChanged` emission.
- `cmd/main.go` — construct API server and call `SetEventSink` BEFORE spawning StartBots goroutine; cancel API context on SIGTERM.
- `configs/config.example.yaml` — documents the `api:` block.

## 14. Out of scope (v1)

- Panel itself (separate project).
- Multi-tenant auth / per-user permissions (single god-mode token by design — the panel implements its own user model on top).
- Persistent event log (ring buffer only).
- Metrics / Prometheus exporter (can be added later).
- Origin allowlist (YAGNI; cloudflared already gates).
- Hooking outbound IRC lines that the go-ircevo library writes itself (PING/PONG, reconnect handshake) — would require library changes.

## 15. Success criteria

- `gofmt -l $(git ls-files '*.go')` returns empty.
- `go vet ./...` clean.
- `go build ./...` clean.
- `go test -race -timeout 120s ./...` green for new tests and existing suite.
- Manual smoke: `./gNb -dev` with `api.enabled: true`, the listener appears on `BindAddr` and `GET /healthz` returns `ok`. Connect via `websocat` (or the equivalent Go client), run `auth.login` → `node.info` → `bot.list` → `events.subscribe` → observe live events as bots connect, change nicks, etc.

## 16. Implementation status

All 16 sections above reflect what is in the tree. The branch (commits prefixed `api:` since `89598af`) was reviewed twice by an independent code-reviewer subagent; both passes' findings have been addressed:

- **First pass** (2 critical, 8 important): `BotByID` no longer trusts the config index after BotManager reorders; lifecycle emissions for `BotRemoved` / `NicksChanged` / `OwnersChanged` were wired; the `AddOwner` self-deadlock was fixed; `bot.attach.raw_out`, `ssh_command`, `version`, `pid` were added; `handleWS` switched to a CAS loop; `Replay` is now O(n); spec §8.3 (the never-implemented `api.backpressure` event) was removed.
- **Second pass** (1 critical, 4 important): the `SetEventSink`-after-`StartBots` race was eliminated by reordering main; `BotAdded` is now emitted on first sink install; `bot.attach.raw_out` covers `Join`/`Part`/`ChangeNick` in addition to `Say`/`Raw`; `bnc.start` brackets IPv6 vhosts; `events.subscribe` caps `replay_last` at 128.

Remaining nits (`Bot.currentSink` uses the heavy `b.mutex`; potential deadlock window if go-ircevo ever changes its callback dispatch to fire DISCONNECTED synchronously from inside `Connection.Quit()`; library-internal writes are not hooked) are deferred — none affect correctness today.
