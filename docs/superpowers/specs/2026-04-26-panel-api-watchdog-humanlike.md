# Panel API — Channel Watchdog & Human-like Activity

**Status:** shipped (gNb commit `9a5c710` on `main`, 2026-04-26)
**Audience:** the panel-bun / panel-SPA session that needs to expose these features to operators.
**Stand-alone:** this document is self-contained — read it cold without needing the underlying gNb spec.

This document describes four new methods on the gNb node WebSocket API and two new event topics. They are siblings to `nicks.*` and `owners.*` — they read and mutate persisted runtime state, with success events broadcast to all subscribers of the lifecycle stream.

The companion design doc (gNb-internal architecture, persistence shape, gating logic) is `docs/superpowers/specs/2026-04-26-runtime-watchdog-humanlike-design.md`. You do not need it to implement the panel side — everything panel-relevant is here.

---

## 1. Why these features exist

**Channel watchdog.** Until now the bot always auto-rejoined its configured channels (initial JOIN after `001`, periodic check every 5 min, retry 30 s after KICK). That's hardcoded. Operators asked for two things:

1. A kill-switch — stop auto-rejoining entirely. Useful when a channel keeps `+i`/`+b`'ing the bot and you don't want a JOIN-storm in the server log, or when you want the bot present on the network but not in any channel.
2. A single-channel override — point the watchdog at one specific channel instead of the configured list.

**Human-like activity.** A new behavior: each bot, independently, joins a randomly-picked channel from a shared list, idles for a random window, parts, waits longer, repeats. Designed to look organic, not periodic. The list and the on/off switch are the only knobs the operator gets — timings are randomized per bot inside the bot.

Both features default to **off**. This is intentional: a fresh node needs explicit operator opt-in. The watchdog's "off by default" is **a behavior change** vs. previous versions of gNb — bots upgraded to the new build will connect but stay out of channels until you toggle the watchdog on through the panel.

---

## 2. Method catalog

All four require a successful prior `auth.login` in the session (otherwise the dispatcher returns `forbidden` per the existing protocol). All four are full-state replace, never partial-patch — every `set` request must carry every field.

### 2.1 `watchdog.get`

| | |
|---|---|
| **Method** | `watchdog.get` |
| **Params** | none (or `null`) |
| **Result** | `{enabled: bool, channel: string}` |

`channel` is `""` when the operator hasn't set an override (watchdog uses the bot's configured channel list from `configs/config.yaml`). When `channel` is non-empty, the watchdog watches that single channel **instead of** the configured list.

### 2.2 `watchdog.set`

| | |
|---|---|
| **Method** | `watchdog.set` |
| **Params** | `{enabled: bool, channel: string}` |
| **Result** | `{enabled: bool, channel: string}` (the new state, post-validate, post-persist) |
| **Errors** | `invalid_params` if channel fails validation; `not_found` if runtime state is unwired (shouldn't happen in production) |

The result is the full canonical new state. Use it to update local UI without a follow-up `watchdog.get`.

`channel` validation:

- empty string is allowed and means "no override"
- otherwise must start with `#`, `&`, `+`, or `!`
- max length 50
- no whitespace, no comma, no NUL, no BEL

Leading/trailing whitespace is trimmed before validation.

### 2.3 `humanlike.get`

| | |
|---|---|
| **Method** | `humanlike.get` |
| **Params** | none (or `null`) |
| **Result** | `{enabled: bool, channels: string[]}` |

`channels` is always a JSON array (never `null`). Empty array is the default state.

### 2.4 `humanlike.set`

| | |
|---|---|
| **Method** | `humanlike.set` |
| **Params** | `{enabled: bool, channels: string}` ← **string**, not array |
| **Result** | `{enabled: bool, channels: string[]}` |
| **Errors** | `invalid_params` if any channel in the list fails validation; `not_found` if runtime state is unwired |

The asymmetry is deliberate: the operator types one space-separated string into the panel ("`#chan1 #chan2 #chan3`") and the API parses it. No need for the panel to pre-split. The API:

1. trims surrounding whitespace
2. splits on any run of whitespace (space, tab, newline)
3. validates each token as a channel name (same rules as `watchdog.channel`, but here the empty case is handled by token elimination)
4. case-insensitively dedupes (preserves first-seen casing)

If any token fails validation the whole set is rejected with `invalid_params` — atomic, all-or-nothing. The panel should re-render the previous state on rejection.

The result returns the normalized list as an array. Order in the result reflects first-seen order in the input.

---

## 3. Event catalog

Both events are emitted on the existing event hub after a successful `set`. Subscribers added via `events.subscribe` (with no `topics` filter, or with these topic names in the filter) receive them. They are also stored in the ring buffer, so `replay_last` will return them.

### 3.1 `runtime.watchdog_changed`

| | |
|---|---|
| **event** | `runtime.watchdog_changed` |
| **data** | `{enabled: bool, channel: string}` |
| **when** | After `watchdog.set` succeeds and the new state has been persisted to disk |

### 3.2 `runtime.humanlike_changed`

| | |
|---|---|
| **event** | `runtime.humanlike_changed` |
| **data** | `{enabled: bool, channels: string[]}` |
| **when** | After `humanlike.set` succeeds and the new state has been persisted to disk |

Multiple panel sessions watching the same node will all see the event — keep your local state in sync by updating on these events the same way you'd update on `nicks.changed` / `owners.changed`.

---

## 4. Behavioral semantics — what the bot actually does

The panel doesn't drive bot behavior directly; it edits runtime state, the gNb daemon reacts. Useful to know what your toggles will cause so you can write good help text and not surprise operators.

### 4.1 Watchdog

| `enabled` | `channel` | What happens on each bot |
|---|---|---|
| `false` | (any) | No auto-JOIN after `001`. No 5-min rejoin ticker. No 30-s post-kick retry. The bot stays connected to IRC but does not enter any channel on its own. Operator can still drive `bot.join` manually through the API. |
| `true` | `""` | Standard pre-existing behavior: JOIN every channel from `config.yaml`'s `channels:` list on `001`, keep the 5-min ticker re-JOINing any that fall off, retry once 30 s after a KICK. |
| `true` | `"#foo"` | Same three mechanisms, but they target **only** `#foo`. The configured channels are not auto-joined and not auto-rejoined. The bot may still be in them (they were joined manually or the watchdog used to be wider) — those memberships are not parted. |

**Live toggle semantics** — what the bot does when the operator changes the state on a live session:

- `false → true`: every connected bot immediately JOINs any watchdog target it isn't on, and starts its 5-min checker if it wasn't running.
- `true → false`: every connected bot stops its 5-min checker. Channels the bot is currently in are **not** parted — the operator may have a reason to want the bot to stay where it is, just not auto-rejoin.
- `channel: "" → "#x"` (or vice-versa, or `#x → #y`): bot JOINs the new target if not already there; **does not** part the old target(s).

This means flipping `channel` while `enabled=true` is purely additive from the bot's point of view. If you want the bot to leave a channel, the operator parts it explicitly via `bot.part` (or shuts the watchdog off and parts manually).

### 4.2 Human-like

When `enabled=true` and `channels` is non-empty, every bot independently runs this loop:

1. Wait a random duration in `[30 min, 180 min]` (second precision, e.g. `103m 47s`).
2. Pick a random channel from the list. Skip channels the bot is already on.
3. If no eligible channel (list empty, or bot is already on every channel): log and go to step 1.
4. If the bot is disconnected from IRC: log and go to step 1.
5. JOIN the picked channel.
6. Wait a random duration in `[2 min, 60 min]` (second precision).
7. PART the channel.
8. Go to step 1.

Important properties:

- **Per-bot randomness**, not per-node. Each bot has its own PCG generator seeded from `crypto/rand` at start, so two bots never march in lockstep.
- **First action is a wait**, not a JOIN. After the operator flips `enabled=true`, the bot does nothing visible for up to 3 hours. The panel UI should mention this so users don't think the feature is broken.
- **Channel list edits don't preempt the current cycle.** If the bot is mid-stay (joined `#foo`, halfway through the 2..60 min idle), removing `#foo` from the list does not part it now — it parts at the natural end of the stay timer. Subsequent cycles will use the new list.
- **Operator turning the feature off mid-cycle** parts the currently-joined channel and stops the loop cleanly. The bot will not be left holding a channel.
- **Operator turning the feature off then back on** starts a fresh cycle (i.e. a new random off-period from step 1, not resumption of the previous cycle).

There is no enforced disjointness between `humanlike.channels` and watchdog targets. The operator should set them to non-overlapping channels — if `humanlike` parts `#foo` and the watchdog has `#foo` as a target, the watchdog will rejoin within 5 min, undoing humanlike's PART. The panel can warn about overlap if it wants but the API does not enforce it.

---

## 5. Wire examples

All examples assume the session has already authenticated.

### 5.1 Read current state

Request:

```json
{"type":"request","id":"r1","method":"watchdog.get"}
```

Response (default state, fresh node):

```json
{"type":"response","id":"r1","ok":true,"result":{"enabled":false,"channel":""}}
```

### 5.2 Enable watchdog with override channel

Request:

```json
{"type":"request","id":"r2","method":"watchdog.set",
 "params":{"enabled":true,"channel":"#staff"}}
```

Response:

```json
{"type":"response","id":"r2","ok":true,
 "result":{"enabled":true,"channel":"#staff"}}
```

Event observed by every subscribed session (including the one that issued the call):

```json
{"type":"event","event":"runtime.watchdog_changed","node_id":"...","ts":"...","seq":42,
 "data":{"enabled":true,"channel":"#staff"}}
```

### 5.3 Reject bad channel name

Request:

```json
{"type":"request","id":"r3","method":"watchdog.set",
 "params":{"enabled":true,"channel":"no-prefix"}}
```

Response:

```json
{"type":"error","id":"r3","code":"invalid_params",
 "message":"watchdog.channel: must start with #, &, + or ! (got \"n\")"}
```

The panel should re-render whatever state it last knew (typically from a recent `watchdog.get` or the last `runtime.watchdog_changed` event) — no state changed server-side.

### 5.4 Set human-like list

Request:

```json
{"type":"request","id":"r4","method":"humanlike.set",
 "params":{"enabled":true,"channels":"#chan1 #chan2 #chan3"}}
```

Response:

```json
{"type":"response","id":"r4","ok":true,
 "result":{"enabled":true,"channels":["#chan1","#chan2","#chan3"]}}
```

Event:

```json
{"type":"event","event":"runtime.humanlike_changed","node_id":"...","ts":"...","seq":43,
 "data":{"enabled":true,"channels":["#chan1","#chan2","#chan3"]}}
```

### 5.5 Reject when one token in the list is bad

Request:

```json
{"type":"request","id":"r5","method":"humanlike.set",
 "params":{"enabled":true,"channels":"#good badname #also-good"}}
```

Response:

```json
{"type":"error","id":"r5","code":"invalid_params",
 "message":"humanlike.channels: channel \"badname\": must start with #, &, + or ! (got \"b\")"}
```

Atomic rejection: even though two of three tokens were valid, none are persisted.

---

## 6. Suggested panel UX

These are recommendations — not requirements.

### 6.1 Where to put it

Both features are per-node, not per-bot. Surface them in a "Node Settings" section, alongside whatever pages already host `nicks.*` / `owners.*`.

### 6.2 Watchdog widget

- A toggle (Enabled / Disabled).
- A single-line text input labeled "Override channel (leave empty to use config)".
- Save button → `watchdog.set`. On success, render the response. On `invalid_params`, surface the error message in the form.
- On `runtime.watchdog_changed` events from the node, replace local state.
- **Help text must mention:** "When disabled, the bot stays connected but does not auto-join any channels."

### 6.3 Human-like widget

- A toggle (Enabled / Disabled).
- A single text input (a `<textarea>` or wide `<input>`) for the channel list, space-separated. Pre-fill with the array joined by spaces.
- Save button → `humanlike.set`. On success, replace state with the response (which is the normalized array). On `invalid_params`, surface the error.
- On `runtime.humanlike_changed`, replace state.
- **Help text must mention:** "When enabled, each bot waits a random 30–180 minutes before its first join, then visits one channel from this list at a time, idles 2–60 minutes, parts, and repeats. Timings are independent per bot."

### 6.4 Cross-feature warnings (optional)

If the operator sets a channel that appears in both watchdog and humanlike, the panel can render a soft warning. Don't block the save — the API doesn't, and there are legitimate edge cases (e.g. you want the watchdog OFF but humanlike to drive joins).

### 6.5 Initial-load order

When opening the node settings page:

1. Subscribe to events (if not already) including `runtime.watchdog_changed` and `runtime.humanlike_changed`.
2. Issue `watchdog.get` and `humanlike.get` in parallel.
3. Render.

After that, just react to the events.

### 6.6 Presence in `node.info` / health endpoints

These features are not currently surfaced in `node.info`. If you want a dashboard summary ("watchdog: on, target: #staff") you need to call `watchdog.get` / `humanlike.get` explicitly. Not changing `node.info` is intentional — it's hot-path and these knobs aren't.

---

## 7. Validation reference (canonical)

Channel name validation, applied identically by `watchdog.channel` and to each token in `humanlike.channels`:

| Check | Behavior on failure |
|---|---|
| empty string | `watchdog.channel` allows it ("no override"); `humanlike` ignores empty tokens silently |
| length > 50 | `invalid_params` |
| first byte not in `#&+!` | `invalid_params` |
| contains space, tab, CR, LF, comma, NUL (`\x00`), or BEL (`\x07`) | `invalid_params` |

Trimming: leading/trailing whitespace is removed before any other check, so `" #foo "` is treated as `"#foo"`.

`humanlike.channels` additionally:

- splits the input string on any run of ASCII whitespace
- ignores empty resulting tokens
- preserves the first occurrence of each channel name (case-insensitive dedupe — `"#A #a"` → `["#A"]`)

---

## 8. Authorization

Identical to all other authenticated methods: the dispatcher rejects with `{"type":"error","code":"forbidden","message":"not authenticated"}` if `auth.login` has not succeeded on the session.

There are no per-method ACLs in gNb v1 — any authenticated session can read or write these settings.

---

## 9. Persistence and durability

The state is persisted to `data/runtime.json` on the node side via atomic write (write-tmp + rename). Survives restarts. The operator can also edit the file directly when the daemon is stopped — schema is:

```json
{
  "watchdog": {"enabled": false, "channel": ""},
  "humanlike": {"enabled": false, "channels": []}
}
```

The panel does not need to know this file path or shape — it's API-only. It's documented here as background.

---

## 10. Error code summary

| Code | Reason |
|---|---|
| `forbidden` | session not authenticated |
| `unknown_method` | typo in method name (don't ship this) |
| `invalid_params` | bad JSON envelope, or validation failure (channel format, etc.) |
| `not_found` | runtime state is unwired (shouldn't happen in production; signals operator misconfiguration) |
| `internal` | persistence failure (disk full, permission denied) |

`invalid_params` and `not_found` are the only ones the panel needs to handle gracefully in the watchdog/humanlike forms. Treat `internal` as a toast-level error.

---

## 11. Behavioral test recipes (panel-side)

For a panel-bun integration test, exercise:

1. Login → `watchdog.get` returns defaults `{enabled:false,channel:""}`.
2. `watchdog.set {enabled:true,channel:"#x"}` → response 200 with the new state, then `runtime.watchdog_changed` event with same data.
3. `watchdog.set {enabled:true,channel:"badname"}` → `invalid_params`. State on server is unchanged (verify with another `watchdog.get`).
4. `humanlike.set {enabled:true,channels:"#a #b #a"}` → response with `channels:["#a","#b"]` (dedupe).
5. `humanlike.set {enabled:true,channels:"#a junk"}` → `invalid_params`, no state change.
6. Open a second authenticated session. Subscribe both. `watchdog.set` from session 1 produces `runtime.watchdog_changed` on session 2.

Reference Go-side tests for the same behaviors (use as oracle for "what the API actually does"): `internal/api/handlers_runtime_test.go`.

---

## 12. Out of scope for this delta

- No per-bot watchdog/humanlike — both features are node-wide. (Each *bot* runs its own humanlike loop, but the on/off and the channel list are global per node.)
- No watchdog list (only single override). If the operator needs the watchdog to monitor multiple channels, they leave `channel: ""` and let the configured list cover them.
- No history of changes (no audit feed beyond the `runtime.*_changed` events). The panel-bun layer's audit log can record these on its own if it wants.
- No support for editing per-bot `config.yaml` channel lists from the API — only the override is mutable.

---

## 13. Quick reference card

```
Methods
  watchdog.get      ()                                  → {enabled, channel}
  watchdog.set      {enabled, channel}                  → {enabled, channel}
  humanlike.get     ()                                  → {enabled, channels:[]}
  humanlike.set     {enabled, channels:"# #..."}        → {enabled, channels:[]}

Events
  runtime.watchdog_changed       {enabled, channel}
  runtime.humanlike_changed      {enabled, channels:[]}

Defaults (fresh node)
  watchdog  = disabled, no channel
  humanlike = disabled, empty list

Auth: every method requires prior auth.login
Persistence: data/runtime.json on the node, atomic write
```
