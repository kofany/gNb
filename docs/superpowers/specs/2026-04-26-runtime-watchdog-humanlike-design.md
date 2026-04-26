# Runtime State, Watchdog Toggle, Human-like Channel Activity

**Status:** approved
**Date:** 2026-04-26
**Author:** kofany + Claude

## Goal

Two new capabilities controlled live through the Panel API:

1. **Channel watchdog** that the operator can disable (default off) and optionally point at a single overriding channel. While disabled, the bot does not auto-join configured channels at all — neither after the initial `RPL_WELCOME`, nor periodically every 5 minutes, nor after a `KICK`. While enabled, the existing rejoin behavior runs against `b.channels` (from YAML) or, if a per-watchdog channel override is set, against that single channel only.
2. **"Human-like" activity** per bot: each bot independently joins one randomly-picked channel from a shared list, idles for a random duration in `[2 min, 60 min]` (with second granularity), parts, waits a random duration in `[30 min, 180 min]` (also seconds-granular), then repeats. Editing the channel list and toggling the feature on/off must be doable through the API. When the feature is enabled the bot first waits the `[30, 180]`-minute off-period, **then** picks and joins.

Both features must persist across restarts in `data/runtime.json`. Both default to **off** so a fresh `runtime.json` matches what users expect when first installing this version.

## Non-goals

- No multi-channel watchdog override (single string only).
- No per-bot human-like list (the list is shared per node; the *timers* and *picks* are per bot).
- No mutation of `configs/config.yaml` from the API. YAML stays read-only at runtime.
- No coordination layer that prevents human-like channels from overlapping with watchdog channels — this is a soft user contract documented in the API help text, not enforced. If they overlap, the watchdog will rejoin a channel that human-like just parted, which is a user error.

## Architecture

### New package: `internal/runtime`

```
internal/runtime/
  state.go        // RuntimeState, persistence, observers, validation
  state_test.go
```

**`RuntimeState`** is the single source of truth. One instance per process, constructed at startup, passed by pointer to `BotManager` (and through it to each `*Bot`). It owns its own JSON file (atomic tmp+rename writes) and a list of change observers.

```go
type RuntimeState struct {
    mu        sync.RWMutex
    path      string
    watchdog  WatchdogConfig
    humanlike HumanlikeConfig
    listeners []func(Snapshot)
}

type WatchdogConfig struct {
    Enabled bool   `json:"enabled"`
    Channel string `json:"channel"` // "" = use Bot.channels
}

type HumanlikeConfig struct {
    Enabled  bool     `json:"enabled"`
    Channels []string `json:"channels"`
}

type Snapshot struct {
    Watchdog  WatchdogConfig
    Humanlike HumanlikeConfig
}
```

**API surface:**

```go
func LoadOrCreate(path string) (*RuntimeState, error)
func (s *RuntimeState) Get() Snapshot
func (s *RuntimeState) Watchdog() WatchdogConfig
func (s *RuntimeState) Humanlike() HumanlikeConfig
func (s *RuntimeState) SetWatchdog(WatchdogConfig) error
func (s *RuntimeState) SetHumanlike(HumanlikeConfig) error
func (s *RuntimeState) OnChange(func(Snapshot))
```

**Validation rules** (enforced in setters):

- `WatchdogConfig.Channel` empty OR a syntactically valid channel name (`#`, `&`, `+`, `!` prefix; no whitespace; ≤ 50 chars).
- `HumanlikeConfig.Channels` — every element validated as above; deduped (case-insensitive); empty list allowed.

**Persistence shape:**

```json
{
  "watchdog": {"enabled": false, "channel": ""},
  "humanlike": {"enabled": false, "channels": []}
}
```

Format: pretty 2-space JSON, mirroring `configs/owners.json` and `data/nicks.json`. Atomic write: `os.WriteFile(path+".tmp", ...)` then `os.Rename(path+".tmp", path)` under `mu`.

**Observers** run *after* `mu` is unlocked, and serially in registration order, to make deadlock impossible (an observer is allowed to call back into `RuntimeState`).

### Wiring

- `cmd/main.go`: load `RuntimeState` after config load, before `NewBotManager`. Pass to `NewBotManager`. If `data/runtime.json` is missing it is auto-created with defaults.
- `bot.NewBotManager(cfg, owners, nm, runtimeState)` — new parameter. Stored on the manager. Each bot gets the pointer at construction (`NewBot`) so it can read state and start/stop its human-like runner.
- `BotManager` registers a single `OnChange` handler that fans the new snapshot out to every live bot's `applyRuntimeChange(snap)`.
- `types.BotManager` and `types.Bot` interfaces gain `RuntimeState() *runtime.RuntimeState` (for the API server) and an internal `applyRuntimeChange(...)` (concrete only — not in the interface, to avoid leaking implementation detail to the api package).
- `api.Deps` adds `Runtime *runtime.RuntimeState` so handlers don't have to drill through the bot manager.

### Watchdog gating in `internal/bot/bot.go`

Every existing rejoin path is gated on `runtimeState.Watchdog().Enabled`. The list of "what to watch" is computed by a new `b.watchdogTargets()` helper:

```go
func (b *Bot) watchdogTargets() []string {
    wd := b.runtimeState.Watchdog()
    if wd.Channel != "" {
        return []string{wd.Channel}
    }
    return b.configuredChannels()
}
```

Three call sites change:

1. **001 callback** (`bot.go:402-406`): only join + start the 5-min checker if `watchdog.enabled`.
2. **5-min ticker / `checkAndRejoinChannels`** (`bot.go:881-898`): iterate `b.watchdogTargets()`, with an early exit if `watchdog.enabled == false` (so toggling off mid-life makes the next tick a no-op without restarting the goroutine).
3. **KICK callback rejoin block** (`bot.go:586-603`): wrapped in `if watchdog.enabled && contains(targets, channel)`.

**Toggle semantics live:**

- OFF → ON, while bot is connected: immediately JOIN every target the bot is not already on. Start the 5-min checker if it isn't running. Equivalent to a fresh 001 sequence.
- ON → OFF: stop the 5-min checker. **Do not** part anything — the operator may want to leave the bot where it currently is.
- channel changes (`""` ↔ `"#x"` or different `#`s): treat as ON re-application — JOIN any newly-targeted channel the bot is not on; do **not** part old targets.

This is implemented in `b.applyRuntimeChange(snap)`, which is the single bottleneck through which any runtime mutation is applied to a bot.

### Human-like runner

A new file `internal/bot/humanlike.go` introduces `humanlikeRunner`:

```go
type humanlikeRunner struct {
    bot     *Bot
    bounds  humanlikeBounds       // testable injection
    rng     *rand.Rand            // math/rand/v2 PCG, per-bot seed
    nowFunc func() time.Time      // testable injection
    after   func(time.Duration) (<-chan time.Time, func() bool)

    stop     chan struct{}
    done     chan struct{}

    joinedMu sync.Mutex
    joined   string                // current humanlike channel, "" if none
}

type humanlikeBounds struct {
    minOff, maxOff   time.Duration  // [30min, 180min] in prod
    minStay, maxStay time.Duration  // [2min, 60min] in prod
}
```

**Lifecycle:**

- A bot has at most one runner. It is constructed and `go runner.run()`-started on enable transition; `runner.close()`'d on disable transition or on `Bot.Quit`.
- The goroutine's loop:

```text
for {
    select { wait off-duration | <-stop }
    pick channel from current humanlike list, excluding b.IsOnChannel(c)
    if none → continue (next off-duration)
    if !b.IsConnected() → continue
    JOIN; record joined
    select { wait stay-duration | <-stop }
    PART (if still joined and connected)
    clear joined
}
```

- Random durations: `bounds.minOff + rand.IntN(int(bounds.maxOff-bounds.minOff))`; same shape for stay. With `time.Second` precision (production calls take seconds, not minutes, as the unit). The result is converted through `time.Duration` and used as `time.After` argument.
- On `stop` (operator toggled off, or bot is being removed): the runner parts the currently-joined channel (if any) before returning. This is the "clean up after yourself when disabled" semantics agreed with the user.
- The shared channel list is read fresh each cycle from `runtimeState.Humanlike().Channels` — list edits during a stay-period are not interrupted; they take effect on the next pick.

**Per-bot RNG:** seed derives from `crypto/rand` 16 bytes into a `rand.NewPCG(seed1, seed2)`. This guarantees independent streams across bots even if multiple are seeded in the same nanosecond.

### API methods

Following the existing `nicks.add` / `owners.add` style. Two new namespaces:

| Method | Params | Result |
|---|---|---|
| `watchdog.get` | none | `{enabled bool, channel string}` |
| `watchdog.set` | `{enabled bool, channel string}` | full new state |
| `humanlike.get` | none | `{enabled bool, channels []string}` |
| `humanlike.set` | `{enabled bool, channels string}` (space-separated input) | full new state with `channels []string` |

Validation errors return `invalid_params`. Persistence errors return `internal`.

### Events

Two new event topics on the existing `EventHub`, emitted by the API handler **after** `runtime.Set*` returns success:

- `runtime.watchdog_changed` payload `{enabled, channel}`
- `runtime.humanlike_changed` payload `{enabled, channels}`

Why emit from the handler and not from the runtime state's observer? Two reasons: (1) the runtime state is consumed by both BotManager (to drive bots) and the API (to publish events), and the existing pattern is "API handlers publish events" (`nicks.add` → `nicks.changed`). (2) keeps `internal/runtime` free of API concerns.

### Defaults / migration

- First run on this version: `data/runtime.json` is created with both features off. **The bot stops auto-rejoining configured channels until the operator toggles `watchdog.enabled = true` via the panel.** This is intentional and matches the user's request; it is documented in the migration note in CHANGELOG/commit message.
- Subsequent restarts read the file and resume the last persisted state.

## Concurrency model

- `RuntimeState.mu` (RWMutex) — `Get*` uses `RLock`, `Set*` uses `Lock`.
- `Set*` order: validate → mutate in-memory → persist to disk → unlock → notify observers (slice copied under lock, ranged over after unlock).
- Each `*Bot` has its own existing `b.mutex` for internal state. `applyRuntimeChange` enters under `b.mutex` only for the `humanlike *humanlikeRunner` slot and the watchdog ticker fields; the heavy work (`JoinChannel`, runner start) happens after release.
- The runner's goroutine never holds `b.mutex` while waiting on a timer.

## Testing

- `internal/runtime/state_test.go`:
  - load-or-create when file is missing → defaults persisted.
  - validate rejects malformed channel names.
  - atomic write survives a crash mid-write (verified by checking `*.tmp` is gone post-write).
  - observers fire post-unlock with a snapshot reflecting the latest mutation.
- `internal/bot/humanlike_test.go`:
  - cycle picks a channel, JOINs, waits stay, PARTs (using fake clock + injected RNG with deterministic sequence + tiny bounds, e.g. 5ms / 5ms).
  - empty list → cycle skips JOIN entirely.
  - all-already-joined → cycle skips JOIN.
  - stop mid-stay parts the channel before exit.
  - disconnected bot at JOIN time → cycle skips, retries next iteration.
- `internal/bot/bot_test.go`: extend with a `RuntimeState`-aware test verifying that watchdog-disabled means 001 doesn't trigger JOIN. (The existing `SendSingleMsg` tests stay as-is.)
- `internal/api/handlers_runtime_test.go`: round-trip test for `watchdog.get/set` + `humanlike.get/set` using the existing `testServer` harness, plus a check that `runtime.*_changed` events are published.

## Files touched (summary)

- **new** `internal/runtime/state.go`, `internal/runtime/state_test.go`
- **new** `internal/bot/humanlike.go`, `internal/bot/humanlike_test.go`
- **new** `internal/api/handlers_runtime.go`, `internal/api/handlers_runtime_test.go`
- **modified** `internal/types/interfaces.go` — `BotManager.RuntimeState()` getter
- **modified** `internal/bot/bot.go` — gating, `applyRuntimeChange`, `watchdogTargets`, runner field
- **modified** `internal/bot/manager.go` — accept `*runtime.RuntimeState`, register observer, `RuntimeState()` getter
- **modified** `internal/bot/bot_test.go`, `internal/api/fakes_test.go` — stubs gain `RuntimeState() *runtime.RuntimeState` returning nil where unused
- **modified** `internal/api/server.go` — `Deps.Runtime`, register new routes
- **modified** `internal/api/sink.go` — no change (events emitted from handlers)
- **modified** `cmd/main.go` — load runtime state, plumb to BotManager
