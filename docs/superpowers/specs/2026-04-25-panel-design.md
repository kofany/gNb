# gNb Panel — Design Spec

**Date:** 2026-04-25
**Status:** Approved (brainstorming complete, awaiting implementation plan)
**Companion specs:** [`2026-04-24-api-websocket-design.md`](./2026-04-24-api-websocket-design.md) — gNb node WebSocket API (panel consumes this)

---

## 1 — Goal & Scope

Build a self-hosted **web panel** for managing one or more `gNb` IRC bot runner instances ("nodes"). The panel runs as a single bun process (backend + static SPA on one port). Multiple human operators ("admins") log in to manage all nodes.

### In scope

- Multi-admin login (no roles — all admins are equal).
- Multi-node management (one panel, N nodes; the panel maintains one persistent WSS connection per node).
- Read views: node status, bot list per node, channels per node, nicks list, owners list.
- Per-bot actions: `say`, `join`, `part`, `quit`, `reconnect`, `change_nick`, `raw`, `bnc.start`, `bnc.stop`.
- Per-node mass actions: `mass_join`, `mass_part`, `mass_reconnect`, `mass_raw`, `mass_say`.
- Nicks/owners CRUD per node.
- **Attach mode**: a terminal-like view that streams a single bot's full IRC traffic (raw I/O) and accepts input lines parsed like the existing DCC partyline (`/msg`, `/raw`, `/join`, `/part`, `/quit`, `/notice`, `/me`).
- Live lifecycle event feed (connect/disconnect/nick/join/part/kick/ban/added/removed) aggregated across all nodes.
- Audit log of all admin actions (who/when/what), retained 90 days.
- Command palette (Ctrl-K) for power navigation.
- Dark mode only.

### Out of scope

- RBAC / per-bot or per-node permissions.
- Mobile-native apps (responsive web is sufficient).
- Multi-tenant (one panel = one organization).
- Light theme.
- Localization (English only — error messages, UI strings).
- Token encryption at rest in SQLite (file perms 0600 only).
- High-availability panel (single bun process; restart = brief downtime).

---

## 2 — Stack (locked versions)

Snapshot 2026-04-25, verified live against npm registry / GitHub releases. Pinned in `package.json` with caret (`^`); `bun.lockb` committed to git for reproducible builds.

| Layer | Package | Version |
|---|---|---|
| Runtime | `bun` | **1.3.13** |
| Language | `typescript` | **6.0.3** |
| Frontend framework | `react` / `react-dom` | **19.2.5** |
| React typings | `@types/react` / `@types/react-dom` | **19.2.14** / **19.2.3** |
| Build / dev | `vite` | **8.0.10** |
| Vite plugin | `@vitejs/plugin-react` | **6.0.1** |
| Styling | `tailwindcss` | **4.2.4** |
| Tailwind plugin | `@tailwindcss/vite` | **4.2.4** |
| UI components | `shadcn` (CLI; copies into repo) | **4.4.0** |
| Icons | `lucide-react` | **1.11.0** |
| Client state | `zustand` | **5.0.12** |
| HTTP cache | `@tanstack/react-query` | **5.100.1** |
| Routing | `@tanstack/react-router` | **1.168.24** |
| Forms | `react-hook-form` | **7.73.1** |
| Validation | `zod` | **4.3.6** |
| Form ↔ Zod | `@hookform/resolvers` | **5.2.2** |
| ORM | `drizzle-orm` | **0.45.2** |
| Migrations | `drizzle-kit` | **0.31.10** |
| Backend types | `@types/bun` | **1.3.13** |
| Unit / integration tests | `vitest` | **4.1.5** |
| React testing | `@testing-library/react` | **16.3.2** |
| E2E | `@playwright/test` | **1.59.1** |
| Lint / format | `@biomejs/biome` | **2.4.13** |

**Built into bun (no separate deps required):**
- `bun:sqlite` — SQLite driver
- `Bun.serve({ websocket })` — HTTP + WebSocket server
- global `WebSocket` — WebSocket client (panel-bun → gNb nodes)
- `Bun.password.hash` / `Bun.password.verify` — argon2id
- `Bun` env — reads `.env` natively

**Tailwind 4 note:** CSS-first config (`@import "tailwindcss"; @theme { ... }`), no `tailwind.config.js`. shadcn 4.x is compatible.

**Zod 4 note:** rewrite from Zod 3, several APIs changed (`z.string().email()` → `z.email()`).

**Vite 8 note:** ESM-only, runs natively on bun.

**Always re-verify versions live before scaffolding** (per project rule in CLAUDE.md). Numbers above are a snapshot — bump to current latest when actually running `bun create vite`.

---

## 3 — Architecture

### 3.1 — Process & topology

```
┌──────────────── browser (admin) — React 19 SPA ─────────────────┐
│  TanStack Router │ TanStack Query (REST) │ WebSocket client     │
└─────────┬───────────────────┬──────────────────────┬────────────┘
          │ HTTPS GET /assets │ HTTPS REST /api/*    │ WSS /ws
          │ (SPA static)      │ (login, CRUD, audit) │ (live events)
          └──────────┬────────┴──────────────────────┘
                     ▼
        ┌─────────────────────────────────────┐
        │         single bun process           │
        │  Bun.serve({ fetch, websocket })     │
        │  ┌────────────────────────────────┐  │
        │  │ static SPA serve (dist/)       │  │
        │  │ /api/*  REST handlers          │  │
        │  │ /ws     WS upgrade (browser)   │  │
        │  ├────────────────────────────────┤  │
        │  │ session store (cookie+SQLite)  │  │
        │  │ NodeRegistry (SQLite)          │  │
        │  │ NodePool (N WSS clients)       │  │
        │  │ EventBus (in-process pub/sub)  │  │
        │  │ AttachManager                  │  │
        │  │ AuditLog                       │  │
        │  └────────────────────────────────┘  │
        └──────────┬───────────────────────────┘
                   │ N × persistent WSS
                   │ (token-auth per gNb spec)
                   ▼
        gNb node #1, #2, …, #N
```

**Browser never connects to gNb nodes directly.** All node URLs and tokens live in SQLite on the panel side. The browser only knows the panel host.

### 3.2 — Deployment

- **Dev**: `bun run dev` → spawns Vite (`5173`, HMR) and bun-server (`3000`) concurrently. Vite proxies `/api/*` and `/ws` to `localhost:3000`.
- **Prod**: `bun run build` → `dist/client/` (Vite SPA bundle) + `dist/server.js` (bundled bun-server). One process serves static assets and API on a single port.
- **TLS**: same model as gNb — default plain HTTP upstream, TLS termination at edge (cloudflared tunnel recommended). Optional native TLS via `Bun.serve({ tls })` if no edge proxy.
- **systemd**: `gnbpanel.service` with `Restart=always`, `RestartSec=2`. Single binary + `data/panel.db` + `migrations/` + `.env`.

### 3.3 — Repo layout

Sibling to `gnb`: `/home/projekt/dev/gnb-panel/`. Independent git repo, independent CI, independent release cadence. Spec docs duplicated into `gnb-panel/docs/superpowers/specs/` once the repo is created.

---

## 4 — Information Architecture (UI)

### 4.1 — Layout

Persistent left sidebar (always visible when logged in):

```
┌─ gNb Panel ──────────────────────┐
│  ▸ Dashboard                     │
│  ▾ Nodes                         │
│      • node-warsaw   (12/12 ✓)   │
│      • node-tokyo    (8/10  ⚠)   │
│      • node-lab      (offline ✗) │
│  ▸ Settings                      │
│                                  │
│  [Ctrl-K  Command Palette]       │
└──────────────────────────────────┘
```

Each node row in the sidebar shows live status (✓/⚠/✗) and `connected_bots / total_bots`.

### 4.2 — Routes

| Route | Purpose |
|---|---|
| `/login` | Login form |
| `/` | Dashboard: node tiles, aggregate counts, last-N lifecycle events live feed |
| `/nodes` | Node CRUD table (name, URL, status, bots, actions) + "Add node" modal |
| `/nodes/:nodeId` | Node detail (tabs container) |
| `/nodes/:nodeId/bots` | Virtualized bot table; row click → bot drawer |
| `/nodes/:nodeId/channels` | Aggregated channel list + which bots are on each |
| `/nodes/:nodeId/nicks` | Priority nicks CRUD (mirrors `nicks.json`) |
| `/nodes/:nodeId/owners` | Owner masks CRUD (mirrors `owners.json`) |
| `/nodes/:nodeId/mass` | Mass-action panel with confirmation dialogs |
| `/nodes/:nodeId/events` | Live lifecycle event feed for this node, filterable |
| `/nodes/:nodeId/bots/:botId/attach` | **Full-screen attach mode** (raw IRC stream + input bar) |
| `/settings` | Settings tabs container |
| `/settings/admins` | Admin list, add admin, remove admin (not own, not last) |
| `/settings/account` | Change own password |
| `/settings/audit` | Audit log table (paginated, filterable) |
| `/settings/about` | Version info |

### 4.3 — Attach mode UX

Full-screen route. Top: scrollback pane, color-coded by direction (outbound yellow, inbound white, errors red, system events grey). Bottom: input bar with the partyline parser:

| Input | Translates to (sent via `bot.raw`) |
|---|---|
| `/msg #chan text…` | `PRIVMSG #chan :text…` |
| `/msg nick text…` | `PRIVMSG nick :text…` |
| `/me #chan action` | `PRIVMSG #chan :\x01ACTION action\x01` |
| `/notice target text` | `NOTICE target :text` |
| `/join #chan` | `JOIN #chan` |
| `/part #chan [reason]` | `PART #chan :reason` |
| `/quit [reason]` | `QUIT :reason` |
| `/raw <line>` | `<line>` (passthrough — expert escape) |
| `<text without slash>` | Treated as `/msg <last-target> <text>` if context exists; otherwise prompted for target |

`Esc` or `Ctrl-D` detaches and returns to bot drawer. "Detach" button visible top-right.

### 4.4 — Cross-cutting UX

- **Command palette (Ctrl-K)** — fuzzy search across nodes, bots, actions ("warsaw bot 5 attach", "settings admins", "mass say #literki hello").
- **Toasts (sonner)** — every action: success or error with API code+message.
- **Theme**: dark only.

---

## 5 — Wire Protocol: Browser ↔ panel-bun

### 5.1 — REST `/api/*`

Authenticated by `gnbpanel_sid` cookie (HttpOnly, Secure, SameSite=Strict). Every mutating endpoint must include header `X-Requested-With: panel` (CSRF bumper, set by SPA fetch wrapper). All responses JSON.

#### Auth

| Method | Path | Body | Result |
|---|---|---|---|
| POST | `/api/auth/login` | `{ username, password }` | `204`, sets cookie |
| POST | `/api/auth/logout` | — | `204` |
| GET | `/api/auth/me` | — | `{ id, username, created_at, must_change_password }` |
| POST | `/api/auth/password` | `{ current, new }` | `204` |

#### Admins

| Method | Path | Body | Result |
|---|---|---|---|
| GET | `/api/admins` | — | `[{ id, username, created_at, created_by_username }]` |
| POST | `/api/admins` | `{ username, password }` | `201 { id }` |
| DELETE | `/api/admins/:id` | — | `204` (errors: 403 if self / last admin) |

#### Nodes (CRUD)

| Method | Path | Body / Query | Result |
|---|---|---|---|
| GET | `/api/nodes` | — | `[{ id, name, ws_url, status, last_connected_at, num_bots, num_connected_bots, version }]` |
| POST | `/api/nodes` | `{ name, ws_url, token }` | `201 { id }` |
| POST | `/api/nodes/test` | `{ ws_url, token }` | `200 { ok: true, version }` or `502 { error }` (test-before-save) |
| PATCH | `/api/nodes/:id` | `{ name?, ws_url?, token?, disabled? }` | `204` |
| DELETE | `/api/nodes/:id` | — | `204` |

#### Per-node read (panel proxies to gNb API, caches briefly)

| Method | Path | Maps to gNb method |
|---|---|---|
| GET | `/api/nodes/:id/info` | `node.info` |
| GET | `/api/nodes/:id/bots` | `bot.list` |
| GET | `/api/nodes/:id/nicks` | `nicks.list` |
| GET | `/api/nodes/:id/owners` | `owners.list` |

#### Per-bot actions (proxied via WSS to node)

All `POST`, body documented per row:

| Path | Body | gNb method |
|---|---|---|
| `/api/nodes/:id/bots/:botId/say` | `{ channel, message }` | `bot.say` |
| `/api/nodes/:id/bots/:botId/join` | `{ channel }` | `bot.join` |
| `/api/nodes/:id/bots/:botId/part` | `{ channel, reason? }` | `bot.part` |
| `/api/nodes/:id/bots/:botId/quit` | `{ message? }` | `bot.quit` |
| `/api/nodes/:id/bots/:botId/reconnect` | — | `bot.reconnect` |
| `/api/nodes/:id/bots/:botId/change_nick` | `{ nick }` | `bot.change_nick` |
| `/api/nodes/:id/bots/:botId/raw` | `{ line }` | `bot.raw` |
| `/api/nodes/:id/bots/:botId/bnc/start` | — | `bnc.start` → `{ port, password, ssh_command }` |
| `/api/nodes/:id/bots/:botId/bnc/stop` | — | `bnc.stop` |

#### Mass per-node

| Path | Body | gNb method |
|---|---|---|
| `/api/nodes/:id/mass/join` | `{ channel }` | `node.mass_join` |
| `/api/nodes/:id/mass/part` | `{ channel, reason? }` | `node.mass_part` |
| `/api/nodes/:id/mass/reconnect` | — | `node.mass_reconnect` |
| `/api/nodes/:id/mass/raw` | `{ line }` | `node.mass_raw` |
| `/api/nodes/:id/mass/say` | `{ channel, message }` | `node.mass_say` |

#### Nicks / owners

| Method | Path | Body | gNb method |
|---|---|---|---|
| POST | `/api/nodes/:id/nicks` | `{ nick }` | `nicks.add` |
| DELETE | `/api/nodes/:id/nicks/:nick` | — | `nicks.remove` |
| POST | `/api/nodes/:id/owners` | `{ mask }` | `owners.add` |
| DELETE | `/api/nodes/:id/owners/:b64mask` | — | `owners.remove` (mask is base64url-encoded in URL because masks contain `*!@`) |

#### Audit

| Method | Path | Query | Result |
|---|---|---|---|
| GET | `/api/audit` | `?cursor=&limit=&user=&action=&node=` | `{ items: [{ id, ts, admin_username, ip, action, node_id, bot_id, details }], next_cursor }` |

#### Error envelope

All error responses share this shape:

```json
{ "error": { "code": "<code>", "message": "<human msg>", "details": {} } }
```

| HTTP | `code` | When |
|---|---|---|
| 400 | `invalid_params` | Missing/malformed param |
| 401 | `unauthorized` | No / expired session |
| 403 | `forbidden` | Action denied (self-delete, last-admin delete, etc.) |
| 404 | `not_found` | Resource doesn't exist |
| 409 | `conflict` | Unique violation (duplicate name, wrong current password) |
| 429 | `rate_limited` | Login bruteforce throttle |
| 502 | `upstream_error` | Node WSS down or returned an error; `details: { node_status, node_code, node_message }` |
| 500 | `internal` | Unhandled |

### 5.2 — WebSocket `/ws`

One WS per browser tab. Authenticated via session cookie at `Upgrade` time. `Origin` header validated (must match panel host) for CSRF.

**Envelope** — identical shape to gNb API spec for cognitive consistency:

```jsonc
// client → server
{ "type": "request", "id": "c1", "method": "events.subscribe", "params": { /* … */ } }

// server → client
{ "type": "response", "id": "c1", "result": { /* … */ } }
{ "type": "error",    "id": "c1", "error": { "code": "...", "message": "..." } }
{ "type": "event",    "event": "node.bot_nick_changed", "node_id": "...",
  "ts": "2026-04-25T12:34:56.789Z", "seq": 42, "data": { /* … */ } }
```

**Methods (client → server):**

| Method | Params | Result |
|---|---|---|
| `events.subscribe` | `{ node_ids: ["all"] \| [id…], cursor?: bigint, replay_last?: 0..1024 }` | `{ cursor, replayed, gap: bool }` — `gap=true` only when `cursor` was supplied and is older than the EventBus ring (1024 events); SPA must invalidate caches and refetch in that case |
| `events.unsubscribe` | — | `{ ok: true }` |
| `attach.open` | `{ node_id, bot_id }` | `{ ok: true, already_attached: bool }` |
| `attach.close` | `{ node_id, bot_id }` | `{ ok: true }` |
| `attach.send` | `{ node_id, bot_id, line }` | `{ ok: true }` (also routed through audit) |

**Events (server → client):**

| Event | When | Data |
|---|---|---|
| `panel.node_status` | Panel-bun's WSS to a node changed state | `{ status: "connecting" \| "connected" \| "error" \| "disabled", error?: string }` |
| `node.<lifecycle>` | Forwarded from gNb (`node.bot_added`, `node.bot_removed`, `node.bot_connected`, `node.bot_disconnected`, `node.bot_nick_changed`, `node.bot_joined`, `node.bot_parted`, `node.bot_kicked`, `node.bot_banned`, `nicks.changed`, `owners.changed`) | Per gNb spec §8.1 |
| `bot.attach.<kind>` | Only delivered to sessions that opened attach for the matching `(node_id, bot_id)` | Per gNb spec §8.2 (`raw_in`, `raw_out`, `privmsg`, `notice`, `join`, `part`, `quit`, `kick`, `mode`, `topic`, `nick`, `ctcp`) |
| `panel.heartbeat` | Every 30s | `{ nodes_up: N, nodes_total: M }` |

**Backpressure:** each session has a 512-slot outbound queue. Overflow → server closes session with code `1008`. Client treats this like any other disconnect (auto-reconnect).

### 5.3 — Auth flow

1. **Login**: `POST /api/auth/login`. Server reads admin row by `username`, calls `Bun.password.verify(submittedPassword, row.passwordHash)`. On success, generate 64-hex `session_id`, insert into `sessions` with `expires_at = now + 24h`, set cookie `gnbpanel_sid=...; HttpOnly; Secure; SameSite=Strict; Path=/; Max-Age=86400`.
2. **Per request**: middleware reads cookie, looks up session, checks not expired, joins to admin row, attaches `req.admin` to handler context. Expired → `401` and clear cookie.
3. **Login throttle**: 5 failed attempts / 60 seconds per IP → `429`. Each fail audited (`login.fail` with reason: `bad_password` / `unknown_user` / `rate_limited`).
4. **Logout**: deletes session row, clears cookie.
5. **Bootstrap**: on first run with empty `admins` table, panel-bun generates a one-time password, prints to stdout (and `data/bootstrap.txt`, perms 0600), inserts admin row with `username=admin` and `must_change_password=1`. The `must_change_password` flag forces a password change on first login.
6. **CSRF**:
   - REST: `SameSite=Strict` cookie + required `X-Requested-With: panel` header on mutating endpoints.
   - WS: `Origin` header validated at `Upgrade` (must match panel host).

### 5.4 — Multi-admin event isolation

All admins are equal — every lifecycle event broadcasts to every connected session. Exception: `bot.attach.*` events route only to sessions that have explicitly called `attach.open` for that `(node_id, bot_id)`. Two admins attached to the same bot see the same stream and can both send input (race is theirs to manage, like DCC partyline).

---

## 6 — panel-bun internals

### 6.1 — `NodePool`

Singleton `Map<nodeId, NodeClient>`.

`NodeClient` per node:
- States: `connecting → connected → (error → connecting) | disabled`.
- Holds active `WebSocket`, `pendingRequests: Map<reqId, {resolve, reject, timeout}>`, `reconnectAttempt: number`.
- `connect()` flow:
  1. Open WSS to `node.ws_url`.
  2. Send `{ method: "auth.login", params: { token } }`, await response.
  3. Send `{ method: "events.subscribe", params: { topics: ["lifecycle"], replay_last: 128 } }`.
  4. Mark `connected`, publish `panel.node_status` event.
- On disconnect / error: reject all pending with `upstream_error`, schedule reconnect with backoff `1, 2, 4, 8, 16, 30s` (cap 30s).
- **`auth.login` returning `unauthorized`**: stop retrying, mark `error: "auth_failed"`. Requires `PATCH /api/nodes/:id/token` to recover.
- `request(method, params, timeoutMs=10_000)`: returns Promise resolved by matching `response`/`error` envelope, or rejected on timeout/disconnect.

NodePool lifecycle hooks:
- Startup: load all `nodes WHERE disabled=0`, spawn one client each.
- `POST /api/nodes`: insert row, spawn client.
- `PATCH /api/nodes/:id { disabled: true }`: close + remove from map.
- `PATCH /api/nodes/:id { ws_url? | token? }`: close + reconnect with new params.
- `DELETE /api/nodes/:id`: close + delete row.

### 6.2 — `EventBus`

In-process pub/sub.
- 1024-slot ring buffer (FIFO), monotonic `seq` counter.
- `publish(event)`: assign `seq`, push to ring, fan out to subscribers whose filter matches.
- `replay(sub, cursor)`: return events from ring with `seq > cursor && filter matches`. If cursor not in ring → return all in-ring, set `gap: true`.
- Producers: `NodeClient` (forwards `event` envelopes from gNb, decorating with `node_id`), `NodePool` (`panel.node_status`), `AttachManager` (forwards `bot.attach.*`).
- Subscribers: each open `PanelSession` (browser WS).

Per-session filter:
- `events.subscribe { node_ids: ["all"] }` → match all `node.*` and `panel.node_status`.
- `events.subscribe { node_ids: [n1, n3] }` → match only those.
- `attach.open(n, b)` adds `attach:n:b` to filter; `bot.attach.*` only matches sessions whose filter includes the matching key.

### 6.3 — `AttachManager`

`active: Map<"<nodeId>:<botId>", Set<PanelSession>>`.

- `open(sess, nodeId, botId)`:
  - If first session for this `(nodeId, botId)` → call `NodePool.get(nodeId).request("bot.attach", { bot_id: botId })`.
  - Add session to set; record `key` in `sess.attaches`.
- `close(sess, nodeId, botId)`:
  - Remove session; if last → call `bot.detach`, remove key from map.
- `send(sess, nodeId, botId, line)`:
  - Parse via shared partyline parser (see §4.3).
  - Audit (`attach.send`, line truncated to 256 chars).
  - Call `NodePool.get(nodeId).request("bot.raw", { bot_id, line: parsed.raw })`.
- `onSessionClose(sess)`: iterate `sess.attaches`, call `close()` for each.

### 6.4 — `AuditLog`

Single insert per logged event. `details` is `JSON.stringify(...)` of the params or context. Actions logged:

- Auth: `login.ok`, `login.fail`, `logout`, `password.change`, `session.expired`.
- Admins: `admin.add`, `admin.remove`.
- Nodes: `node.add`, `node.update`, `node.delete`, `node.disable`, `node.enable`, `node.connected` (NodeClient enters `connected`), `node.disconnected` (NodeClient enters `error`).
- Bot commands: `bot.say`, `bot.join`, `bot.part`, `bot.quit`, `bot.reconnect`, `bot.change_nick`, `bot.raw`, `bnc.start`, `bnc.stop`.
- Mass: `mass.join`, `mass.part`, `mass.reconnect`, `mass.raw`, `mass.say`.
- Lists: `nicks.add`, `nicks.remove`, `owners.add`, `owners.remove`.
- Attach: `attach.open`, `attach.close`, `attach.send` (with `line` truncated to 256 chars).

Retention: daily `setInterval` (24h cadence) deletes rows where `ts < now - 90 days` and expired sessions.

---

## 7 — Reconnection summary

| Link | Strategy |
|---|---|
| panel-bun → node | Exponential backoff `1, 2, 4, 8, 16, 30s` (cap). On reconnect: `events.subscribe { replay_last: 128 }` to replay missed events from gNb's 256-event ring. |
| browser → panel-bun | Exponential backoff `1, 2, 4, 8, 16, 30s` (cap). On reconnect: `events.subscribe { cursor: lastSeenSeq }` with replay from EventBus 1024-event ring. If `gap: true`, SPA invalidates all TanStack Query caches and refetches. Open attaches are re-issued automatically. |

---

## 8 — Persistence (SQLite)

File: `data/panel.db`, perms `0600`, WAL mode (`PRAGMA journal_mode=WAL`).
ORM: `drizzle-orm` + `bun:sqlite`. Migrations generated by `drizzle-kit` to `migrations/*.sql`, applied at startup if `schema_version` < latest.

```ts
// src/server/db/schema.ts
import { sqliteTable, integer, text, index } from 'drizzle-orm/sqlite-core';

export const admins = sqliteTable('admins', {
  id: integer('id').primaryKey({ autoIncrement: true }),
  username: text('username').notNull().unique(),
  passwordHash: text('password_hash').notNull(),
  mustChangePassword: integer('must_change_password').notNull().default(0),
  createdAt: integer('created_at').notNull(),
  createdBy: integer('created_by').references(() => admins.id),
});

export const sessions = sqliteTable('sessions', {
  id: text('id').primaryKey(),
  adminId: integer('admin_id').notNull()
    .references(() => admins.id, { onDelete: 'cascade' }),
  createdAt: integer('created_at').notNull(),
  expiresAt: integer('expires_at').notNull(),
  ip: text('ip').notNull(),
  userAgent: text('user_agent'),
}, (t) => [ index('sessions_expires_at_idx').on(t.expiresAt) ]);

export const nodes = sqliteTable('nodes', {
  id: integer('id').primaryKey({ autoIncrement: true }),
  name: text('name').notNull().unique(),
  wsUrl: text('ws_url').notNull(),
  token: text('token').notNull(),               // plain (decision 9A)
  disabled: integer('disabled').notNull().default(0),
  createdAt: integer('created_at').notNull(),
  addedBy: integer('added_by').references(() => admins.id),
  lastConnectedAt: integer('last_connected_at'),
});

export const audit = sqliteTable('audit', {
  id: integer('id').primaryKey({ autoIncrement: true }),
  ts: integer('ts').notNull(),
  adminId: integer('admin_id').references(() => admins.id, { onDelete: 'set null' }),
  adminUsername: text('admin_username'),        // denormalized — survives admin deletion
  ip: text('ip'),
  action: text('action').notNull(),
  nodeId: integer('node_id').references(() => nodes.id, { onDelete: 'set null' }),
  botId: text('bot_id'),                        // gNb sha1[:12]
  details: text('details'),                     // JSON
}, (t) => [
  index('audit_ts_idx').on(t.ts),
  index('audit_admin_idx').on(t.adminId),
]);
```

---

## 9 — Error handling

| Scenario | Behavior |
|---|---|
| Node WSS fails on startup | `NodeClient` enters `error`, retries with backoff. Proxy REST returns `502 upstream_error { node_status: "error" }`. |
| Node WSS drops mid-flight | Pending requests reject with `502`. Browser shows toast. NodeClient reconnects. |
| Node returns `unauthorized` on `auth.login` | NodeClient marks `error: "auth_failed"`, **stops retrying** (token won't fix itself). Requires `PATCH /api/nodes/:id { token }`. |
| Node returns `429 rate_limited` | Backoff + retry, max 3 attempts; then `502` to caller. |
| Browser WS drops | SPA reconnects with backoff, calls `events.subscribe { cursor }`, replays from EventBus or full-refetch on `gap`. |
| SQLite `BUSY` | Drizzle retry + our wrapper retries 3× with 50ms delay. |
| panel-bun process crash | systemd `Restart=always`, `RestartSec=2`. |
| Login bruteforce | Throttle 5/60s per IP → `429`, audit `login.fail`. |

---

## 10 — Testing strategy

| Layer | Tool | Scope |
|---|---|---|
| Unit (server) | `vitest` | `NodePool` reconnect logic, `EventBus` filtering + replay, `AttachManager` ref-counting, attach input parser, audit retention prune, password hashing/verify. In-memory SQLite (`new Database(":memory:")`) per suite. |
| Integration (server) | `vitest` | Full panel-bun against a **fake gNb WSS server** that implements `auth.login`, `events.subscribe`, `bot.list`, `bot.raw`, attach lifecycle, etc. Verifies full REST → NodePool.request → fake → response cycle. |
| Unit (UI) | `vitest` + `@testing-library/react` | Components in isolation, mock TanStack Query cache, mock WS client. |
| E2E | `@playwright/test` | Full path: login → add node → bots list visible → `bot.say` → toast; attach mode → input parsed → output visible. Run against panel-bun + a real `gNb -dev` instance on `127.0.0.1`. |
| Type check | `tsc --noEmit` in CI |  |
| Lint / format | `biome check` + `biome format` |  |

**Out of scope:** Radix/shadcn internals, Vite build itself, `bun:sqlite` (all upstream-tested).

---

## 11 — File layout (`gnb-panel/`)

```
gnb-panel/
├── README.md
├── package.json
├── bun.lockb
├── biome.json
├── tsconfig.json
├── vite.config.ts                # SPA build; dev: proxy /api/* + /ws → :3000
├── tailwind.config.ts            # T4 minimal; theme in CSS
├── components.json               # shadcn config
├── drizzle.config.ts
├── playwright.config.ts
├── docs/
│   └── superpowers/
│       ├── specs/                # this doc copied here once repo exists
│       └── plans/
├── data/                         # gitignored
│   └── .gitkeep
├── migrations/                   # drizzle-generated SQL
├── public/
├── src/
│   ├── client/                   # React SPA
│   │   ├── main.tsx
│   │   ├── app.tsx
│   │   ├── routes/               # TanStack Router file-based
│   │   │   ├── __root.tsx
│   │   │   ├── login.tsx
│   │   │   ├── index.tsx
│   │   │   ├── nodes/
│   │   │   │   ├── index.tsx
│   │   │   │   ├── $nodeId/
│   │   │   │   │   ├── index.tsx
│   │   │   │   │   ├── bots.tsx
│   │   │   │   │   ├── channels.tsx
│   │   │   │   │   ├── nicks.tsx
│   │   │   │   │   ├── owners.tsx
│   │   │   │   │   ├── mass.tsx
│   │   │   │   │   ├── events.tsx
│   │   │   │   │   └── bots/$botId/attach.tsx
│   │   │   └── settings/
│   │   ├── components/
│   │   │   └── ui/               # shadcn-imported components
│   │   ├── stores/               # Zustand (events, attaches, sidebar)
│   │   ├── lib/
│   │   │   ├── api.ts            # fetch wrapper
│   │   │   ├── ws.ts             # WS client + reconnect
│   │   │   ├── attach-parser.ts  # shared with server
│   │   │   └── format.ts
│   │   └── styles/
│   │       └── app.css           # @import "tailwindcss"; @theme {...}
│   ├── server/                   # bun process
│   │   ├── index.ts              # Bun.serve entry
│   │   ├── env.ts                # zod-validated env
│   │   ├── db/
│   │   │   ├── client.ts
│   │   │   ├── schema.ts
│   │   │   └── migrate.ts
│   │   ├── auth/
│   │   │   ├── session.ts
│   │   │   ├── password.ts
│   │   │   └── ratelimit.ts
│   │   ├── api/
│   │   │   ├── router.ts
│   │   │   ├── auth.ts
│   │   │   ├── admins.ts
│   │   │   ├── nodes.ts
│   │   │   ├── proxy.ts          # /api/nodes/:id/* — proxy via NodePool
│   │   │   └── audit.ts
│   │   ├── ws/
│   │   │   ├── server.ts         # upgrade + session bind
│   │   │   ├── session.ts        # PanelSession class
│   │   │   ├── handlers.ts       # events.subscribe, attach.*
│   │   │   └── envelope.ts       # validation + types
│   │   ├── nodes/
│   │   │   ├── pool.ts
│   │   │   ├── client.ts
│   │   │   └── attach.ts
│   │   ├── bus/
│   │   │   └── event-bus.ts
│   │   ├── audit/
│   │   │   ├── log.ts
│   │   │   └── retention.ts
│   │   └── shared/
│   │       ├── envelope.ts       # gNb wire types (mirror of gNb spec)
│   │       └── attach-parser.ts
│   └── shared/                   # cross-cutting types, module aliases
├── tests/
│   ├── unit/
│   ├── integration/
│   │   └── fake-gnb-server.ts
│   └── e2e/
└── scripts/
    ├── dev.ts
    ├── build.ts
    └── seed-bootstrap-admin.ts
```

**Dev workflow:**

```bash
bun install
bun run dev      # vite (5173, HMR) + bun src/server/index.ts (3000) concurrently
bun test         # vitest unit + integration
bun run e2e      # playwright (requires gNb -dev on 127.0.0.1)
bun run build    # vite build → dist/client; bun build server → dist/server.js
bun run start    # bun dist/server.js (single port, prod)
```

**Prod artifact:** one `dist/server.js` + `dist/client/` + `migrations/` + `data/panel.db` + `.env` + `gnbpanel.service`.

---

## 12 — Open / future work

Explicitly deferred (do **not** include in v1):

- RBAC (per-bot or per-node permissions).
- Light theme.
- Localization.
- Token encryption at rest.
- Push notifications (browser API).
- Mobile-native apps.
- Multi-tenant.
- WebHooks for external systems (e.g., Slack alerts on bot.disconnected).

If/when added, each warrants its own design spec.

---

## 13 — Decision log

Brainstorming 2026-04-25 — full Q&A:

| # | Question | Decision |
|---|---|---|
| 1 | User model | **B** — Multiple admins, no roles |
| 2 | Topology | **A** — panel-bun as hub, persistent WSS to each node |
| 3 | Persistence | **A** — SQLite via `bun:sqlite` + drizzle-orm |
| 4 | Frontend framework | **A** — React 19 + Vite + TS + Tailwind 4 + shadcn/ui |
| 5 | Version pinning | **B** — caret (`^`) + `bun.lockb` committed |
| 6 | Repo location | **A** — sibling repo `gnb-panel/` |
| 7 | Audit log | **A** — yes, dedicated table + `/settings/audit` view |
| 8 | UI themes | **A** — dark only |
| 9 | Token encryption at rest | **A** — plain in SQLite, file perms 0600 |
| 10 | Audit retention | **B** — prune > 90 days, daily cron |
| 11 | `attach.send` audit | **A** — yes, line truncated to 256 chars |

---

## 14 — Success criteria

- One bun process serves SPA + API on a single port.
- Multiple admins can log in, manage multiple gNb nodes, see live aggregated lifecycle events, perform per-bot and mass actions.
- Attach mode streams a single bot's full IRC traffic and accepts partyline-style input.
- Audit log captures all admin actions (90-day retention).
- panel-bun reconnects to nodes on transient failures with exponential backoff and event replay.
- Browser reconnects to panel-bun with cursor-based event replay.
- All panel-bun interactions with gNb conform to `2026-04-24-api-websocket-design.md`.
- `bun test` (unit + integration) green; `playwright` E2E green against a live `gNb -dev`; `tsc --noEmit` clean; `biome check` clean.
