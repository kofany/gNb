# Nick-Catcher Stability Fixes — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Eliminate 25 correctness, concurrency, and hygiene issues identified in the 2026-04-24 code audit so the bot fleet runs stably under auto-reconnect, partyline DCC, and long-lived deployments.

**Architecture:** Preserve public interfaces (`types.Bot`, `types.NickManager`, `types.BotManager`) and the go-ircevo callback set. Fixes are surgical — do not refactor unrelated code. Each fix lands as one commit. Verification after every commit: `go vet ./... && go build ./... && go test ./...` must pass.

**Tech Stack:** Go 1.23+, `github.com/kofany/go-ircevo` v1.2.4, `sevlyar/go-daemon`, `gliderlabs/ssh`, stub-based tests in `internal/bot/bot_test.go`.

---

## Fix ordering

Priority P0 (correctness bugs that cause crashes or silently lose functionality) first; then P1 (races/perf/consistency); then P2 (cosmetics/cruft).

- Fix 01: DCC `onStop` + partyline leak on EOF (P0 ★)
- Fix 02: Double `handleDCCRequest` on CTCP + CTCP_DCC (P0)
- Fix 03: Channel checker dies after first disconnect (P0)
- Fix 04: Nil-deref of `b.Connection`/`b.nickManager`/`b.botManager` after 465/466/RemoveBot (P0)
- Fix 05: `Reconnect()` vs library auto-reconnect (P0 — needs verification)
- Fix 06: Races on `b.ServerName`/`b.lastConnectTime`/`b.channels` in 001 callback (P1)
- Fix 07: `channelCheckTicker` access unsynchronized (P1)
- Fix 08: DCC/BNC `WriteToConn` spawns 2 goroutines per event (P1)
- Fix 09: `ignoredEvents` declared but never consulted (P1)
- Fix 10: Logger `SetPrefix` + stdout double-print under daemon (P1)
- Fix 11: Regex recompiled per owner-match (P1)
- Fix 12: `ChangeNick`/`Reconnect` use `time.Sleep` instead of callbacks (P1)
- Fix 13: Remove no-op `rand.Seed` (P1)
- Fix 14: Replace `log.Print` in main.go with `util.Info` consistency (P1)
- Fix 15: `SendSingleMsg` uses write-lock for read iteration (P1)
- Fix 16: Remove duplicate `Contains` / `ContainsIgnoreCase` (P1)
- Fix 17: `gofmt -w internal/bnc/server.go` (P2)
- Fix 18: Remove dead `Bot.isonResponse` field (P2)
- Fix 19: Remove dead `DCCTunnel.readDone`/`writeDone` (P2)
- Fix 20: Remove / justify unused `IsActive`, `GetBot`, `SetIgnoredEvent` on DCCTunnel (P2)
- Fix 21: Remove unused `OwnerList.GetOwners()` (P2)
- Fix 22: Fix `RawTunnel` default target (P2)
- Fix 23: Convert read-only `NickManager.mutex` calls to RLock where safe (P2)
- Fix 24: Document `cleanupDisconnectedBots` one-shot intent (P2)
- Fix 25: Final sweep — `go vet`, `go test -race`, manual dry-run (P2)

---

## Task 01 — DCC tunnel onStop + partyline session never cleaned on EOF

**Files:**
- Modify: `internal/dcc/dcc_tunnel.go:95-116` (readLoop defer)

**Root cause:** The defer in `readLoop` sets `dt.active = false` *before* calling `Stop()`. `Stop()` then sees `active==false` and early-returns without removing the session from `globalPartyLine.sessions` or calling `dt.onStop` (which sets `b.dccTunnel = nil`). Result: ghost entries on the partyline, silent write errors, leaked bot tunnel reference.

**Fix:** Remove the pre-set of `active=false` and `readDone` closure from the defer; delegate everything to `Stop()` which already owns the teardown.

- [ ] **Step 1:** Replace readLoop defer with a single `dt.Stop()` call. Drop `readDone` closure logic from the defer (see Fix 19 for `readDone` removal).

Exact change:
```go
// Before:
func (dt *DCCTunnel) readLoop() {
    defer func() {
        dt.mu.Lock()
        dt.active = false
        if dt.readDone != nil {
            close(dt.readDone)
        }
        dt.mu.Unlock()
        dt.Stop()
    }()
    ...
}

// After:
func (dt *DCCTunnel) readLoop() {
    defer dt.Stop()
    ...
}
```

- [ ] **Step 2:** Verify: `go vet ./... && go build ./... && go test ./...`
- [ ] **Step 3:** Commit: `fix(dcc): ensure Stop() runs on readLoop EOF to clean partyline`

---

## Task 02 — Double `handleDCCRequest` on CTCP+CTCP_DCC

**Files:**
- Modify: `internal/bot/bot.go:472,1091-1098`

**Root cause:** Library fires both `CTCP` (wrapper) and `CTCP_DCC` (subtype) for a DCC CHAT. `handleCTCP` forwards to `handleDCCRequest` when message begins `"DCC "`; `CTCP_DCC` is also bound to `handleDCCRequest`. Two `net.Dial` attempts; second overwrites first tunnel, first leaks.

**Fix:** Strip the `"DCC "` forward from `handleCTCP`. Leave `handleCTCP` as a debug logger, or drop the registration entirely. Keep the separate `CTCP_DCC` callback.

- [ ] **Step 1:** Edit `handleCTCP` to only log.

```go
func (b *Bot) handleCTCP(e *irc.Event) {
    util.Debug("CTCP Event | Nick: %s | Args: %v | Message: %s", e.Nick, e.Arguments, e.Message())
    // DCC requests are routed via the CTCP_DCC callback; no dispatch here.
}
```

- [ ] **Step 2:** Verify build+test.
- [ ] **Step 3:** Commit: `fix(bot): stop dispatching DCC from handleCTCP (double-dial)`

---

## Task 03 — Channel checker dies after first disconnect

**Files:**
- Modify: `internal/bot/bot.go:26-53` (Bot struct: add `checkerStop chan struct{}`)
- Modify: `internal/bot/bot.go:789-813` (startChannelChecker)
- Modify: `internal/bot/bot.go:306-336` (Quit)

**Root cause:** Checker goroutine selects on `b.connected` as its cancellation signal, but `b.connected` is closed on first Quit and never recreated for library-managed auto-reconnect. Second-life checker exits immediately.

**Fix:** Introduce a dedicated `checkerStop` chan per-checker. Start-new replaces previous; Quit closes current.

- [ ] **Step 1:** Add field `channelCheckerStop chan struct{}` to Bot struct (under mutex protection documentation).
- [ ] **Step 2:** Rewrite `startChannelChecker` to own its own stop channel:

```go
func (b *Bot) startChannelChecker() {
    b.mutex.Lock()
    if b.channelCheckTicker != nil { b.channelCheckTicker.Stop() }
    if b.channelCheckerStop != nil { close(b.channelCheckerStop) }
    b.channelCheckTicker = time.NewTicker(5 * time.Minute)
    stop := make(chan struct{})
    b.channelCheckerStop = stop
    ticker := b.channelCheckTicker
    b.mutex.Unlock()

    go func() {
        for {
            select {
            case <-ticker.C:
                if b.IsConnected() { b.checkAndRejoinChannels() }
            case <-stop:
                return
            }
        }
    }()
}
```

- [ ] **Step 3:** Update `Quit` to close `channelCheckerStop` (if set) under mutex.
- [ ] **Step 4:** Verify build+test.
- [ ] **Step 5:** Commit: `fix(bot): give channel checker its own stop channel so it survives reconnects`

---

## Task 04 — Nil-deref of Connection/NickManager/BotManager after 465/466

**Files:**
- Modify: `internal/bot/bot.go:551-589` (465 handler)
- Modify: `internal/bot/bot.go:601-624` (RemoveBot)

**Root cause:** After ban/removal the code sets `b.Connection = nil`, `b.botManager = nil`, `b.nickManager = nil`. Remaining callbacks still wired on the same `*Connection` can fire (DISCONNECTED, `*`, `PRIVMSG` in flight), dereferencing nils.

**Fix:** Don't nil the fields. The library's `Quit()` + `DISCONNECTED` is enough to stop traffic; removing the bot from the manager eliminates external reachability; GC finishes the job once goroutines drain. Keep `b.gaveUp = true` (atomic or under mutex) to short-circuit paths that could still run.

- [ ] **Step 1:** Add `b.gaveUp` check (already exists as `gaveUp bool`) in sender methods that shouldn't fire after removal.
- [ ] **Step 2:** Remove the three nil-out assignments in the 465 handler, 466 handler, and `RemoveBot()`.
- [ ] **Step 3:** Verify build+test.
- [ ] **Step 4:** Commit: `fix(bot): don't nil Connection/managers on ban to avoid callback NPEs`

---

## Task 05 — `Reconnect()` vs library auto-reconnect

**Files:**
- Modify: `internal/bot/bot.go:834-884`

**Root cause:** `Reconnect()` does `conn.Quit()`, sleeps, then builds a fresh `irc.IRC()` and connects. If `Quit()` does not disable the library's auto-reconnect, two connections bloom.

**Verification steps:**
- [ ] **Step 1:** Inspect go-ircevo source to see what `Quit()` does. If it calls `Disconnect()` internally or sets a "shutdown" flag, we're safe; if not, we need `Disconnect()` before rebuild.

```bash
# The module should be present under $GOPATH/pkg/mod after `go build`. Check Quit semantics:
grep -RIn "func (irc \*Connection) Quit\b" "$(go env GOMODCACHE)/github.com/kofany/go-ircevo@v1.2.4/"
grep -RIn "func (irc \*Connection) Disconnect\b" "$(go env GOMODCACHE)/github.com/kofany/go-ircevo@v1.2.4/"
```

- [ ] **Step 2:** If `Quit()` schedules reconnect: replace with `Disconnect()` in Reconnect.
- [ ] **Step 3:** Verify build+test.
- [ ] **Step 4:** Commit: `fix(bot): use Disconnect() in Reconnect to prevent double connection`

---

## Task 06 — Races in 001 callback field assignment

**Files:**
- Modify: `internal/bot/bot.go:339-378`

**Fix:** Wrap `ServerName`, `lastConnectTime`, and the `channels` read in `b.mutex`.

- [ ] **Step 1:** Reorganize 001 callback:

```go
b.Connection.AddCallback("001", func(e *irc.Event) {
    if b.IsConnected() { return }
    b.markAsConnected()
    b.mutex.Lock()
    b.ServerName = e.Source
    b.lastConnectTime = time.Now()
    b.CurrentNick = b.Connection.GetNick()
    b.joinedChannels = make(map[string]bool)
    channelsCopy := make([]string, len(b.channels))
    copy(channelsCopy, b.channels)
    b.mutex.Unlock()

    util.Info("Bot %s fully connected to %s as %s", b.GetCurrentNick(), b.ServerName, b.GetCurrentNick())
    util.Info("Bot %s preparing to join configured channels: %v", b.GetCurrentNick(), channelsCopy)
    for _, channel := range channelsCopy { b.JoinChannel(channel) }
    b.startChannelChecker()

    select {
    case <-b.connected:
    default:
        close(b.connected)
    }
})
```

- [ ] **Step 2:** Verify build+test.
- [ ] **Step 3:** Commit: `fix(bot): lock ServerName/channels in 001 callback`

---

## Task 07 — `channelCheckTicker` access unsynchronized

Covered by Task 03 (consolidated under mutex). No separate commit needed; mark done after Task 03.

---

## Task 08 — DCC/BNC `WriteToConn` spawns 2 goroutines per event

**Files:**
- Modify: `internal/dcc/dcc_tunnel.go:166-210`
- Modify: `internal/bnc/rawtunnel.go:241-262`

**Fix:** Single synchronous `conn.Write` with `SetWriteDeadline`. If write fails, call `Stop()` once and return. Drop goroutines.

- [ ] **Step 1:** Rewrite `DCCTunnel.WriteToConn`:

```go
func (dt *DCCTunnel) WriteToConn(data string) {
    dt.mu.Lock()
    if !dt.active || dt.conn == nil { dt.mu.Unlock(); return }
    if strings.Contains(data, " 303 ") { dt.mu.Unlock(); return }
    parsedMessage := dt.parseIRCMessage(data)
    if parsedMessage == "" { dt.mu.Unlock(); return }
    conn := dt.conn
    dt.mu.Unlock()

    _ = conn.SetWriteDeadline(time.Now().Add(2 * time.Second))
    if _, err := conn.Write([]byte(parsedMessage + "\r\n")); err != nil {
        util.Warning("DCC: write failed: %v", err)
        dt.Stop()
    }
}
```

Similarly simplify `sendToClient`. Apply same pattern to `RawTunnel.WriteToConn`.

- [ ] **Step 2:** Verify build+test.
- [ ] **Step 3:** Commit: `perf(dcc,bnc): sync write with deadline; remove goroutine-per-event`

---

## Task 09 — `ignoredEvents` declared but never consulted

**Files:**
- Modify: `internal/dcc/dcc_tunnel.go` (consult map in WriteToConn)
- Modify: `internal/bnc/rawtunnel.go` (same)

**Decision:** Either honor the map (parse event code from raw line and check) or drop the field. Simpler + less surface: drop the map and keep the hardcoded `" 303 "` filter with a named constant.

- [ ] **Step 1:** Remove `ignoredEvents` field + `SetIgnoredEvent` method + init from both files. Keep current hardcoded filter.
- [ ] **Step 2:** Verify build+test.
- [ ] **Step 3:** Commit: `chore(dcc,bnc): remove unused ignoredEvents field`

---

## Task 10 — Logger double-output under daemon mode

**Files:**
- Modify: `internal/util/logger.go:82-86`

**Fix:** Only print to stdout when NOT daemonized. Simplest: take a `bool` flag at init, or omit stdout altogether (the daemon mode logs to file; dev mode also logs to its own `bot_dev.log`).

- [ ] **Step 1:** Drop the `fmt.Println(prefix + msg)` line in `logMessage`.
- [ ] **Step 2:** Verify build+test.
- [ ] **Step 3:** Commit: `chore(util): stop double-printing logs to stdout`

---

## Task 11 — Regex recompiled per owner match

**Files:**
- Modify: `internal/util/matcher.go`
- Modify: `internal/bot/bot.go` (SetOwnerList) or `internal/auth/owner.go` (IsOwner)

**Fix:** Cache compiled `*regexp.Regexp` per mask in a sync.Map in `Matcher`, keyed by the original mask string. `MatchUserHost` looks up or compiles once.

- [ ] **Step 1:** Change `Matcher` to hold `sync.Map` (string→*regexp.Regexp).
- [ ] **Step 2:** `matchWildcard` consults cache.
- [ ] **Step 3:** Add unit test verifying match behavior is unchanged.
- [ ] **Step 4:** Verify build+test.
- [ ] **Step 5:** Commit: `perf(util): cache compiled owner-mask regex`

---

## Task 12 — `ChangeNick`/`Reconnect` use `time.Sleep`

**Files:**
- Modify: `internal/bot/bot.go:738-755, 856-860`

**Fix:** Remove opportunistic sleeps; let the NICK / 001 callbacks drive state transitions. Log at point-of-send, not after an arbitrary wait.

- [ ] **Step 1:** Drop `time.Sleep(1 * time.Second)` + nick-check block in `ChangeNick`.
- [ ] **Step 2:** Drop `time.Sleep(5 * time.Second); b.checkAndRejoinChannels()` in `Reconnect`. The channel checker ticker will catch missing channels within 5min; 001 handler rejoins immediately on successful reconnect.
- [ ] **Step 3:** Verify build+test.
- [ ] **Step 4:** Commit: `refactor(bot): remove arbitrary sleeps after NICK/Reconnect`

---

## Task 13 — `rand.Seed` no-op

**Files:**
- Modify: `cmd/main.go:138-139`

**Fix:** Delete the two lines. Go 1.20+ seeds automatically.

- [ ] **Step 1:** Remove the seed call and its preceding log line.
- [ ] **Step 2:** Verify build+test.
- [ ] **Step 3:** Commit: `chore(main): drop no-op rand.Seed (Go 1.20+)`

---

## Task 14 — `log.Print` vs. `util.Info`

**Files:**
- Modify: `cmd/main.go:164`

**Fix:** Replace `log.Print("Daemon started")` with `util.Info("Daemon started")`. Also remove the now-unused `log` import if possible (keep if still used elsewhere in main.go).

- [ ] **Step 1:** Replace call.
- [ ] **Step 2:** Verify import still needed for `log.Printf` elsewhere in main; it is (used in `log.Printf("Failed to initialize logger after daemonization: %v", err)` which fires before our logger is ready). Leave that one in place.
- [ ] **Step 3:** Verify build+test.
- [ ] **Step 4:** Commit: `chore(main): use util.Info for daemon start log`

---

## Task 15 — `SendSingleMsg` uses write-lock for read iteration

**Files:**
- Modify: `internal/bot/manager.go:473-506`

**Fix:** Use `RLock` for iteration, but `commandBotIndex` update needs exclusive. Split: RLock for scan, then Lock briefly to advance index. Or keep single Lock but document why. Given tight critical section, simplest: continue with Lock. No commit required for this if we choose to keep it.

**Decision:** Keep as-is; the critical section is small and contention is low. Close task without change; note in plan doc.

- [ ] **Step 1:** Decision documented — no code change, task closed.

---

## Task 16 — Duplicate `Contains` / `ContainsIgnoreCase`

**Files:**
- Modify: `internal/util/helpers.go`

**Fix:** Delete `Contains`; all call sites use `ContainsIgnoreCase` or can. Or vice versa — pick the clearer name.

- [ ] **Step 1:** Check call sites: `grep -RIn "util.Contains\b" ./...`. If none, just delete.
- [ ] **Step 2:** Delete unused one.
- [ ] **Step 3:** Verify build+test.
- [ ] **Step 4:** Commit: `chore(util): remove duplicate Contains helper`

---

## Task 17 — gofmt

**Files:**
- Modify: `internal/bnc/server.go`

- [ ] **Step 1:** `gofmt -w internal/bnc/server.go`
- [ ] **Step 2:** Verify `gofmt -l .` is clean.
- [ ] **Step 3:** Commit: `chore(bnc): gofmt server.go`

---

## Task 18 — Remove dead `Bot.isonResponse`

**Files:**
- Modify: `internal/bot/bot.go`

- [ ] **Step 1:** Remove `isonResponse chan []string` field + init in `NewBot`.
- [ ] **Step 2:** Verify build+test.
- [ ] **Step 3:** Commit: `chore(bot): remove unused isonResponse legacy field`

---

## Task 19 — Remove dead `DCCTunnel.readDone`/`writeDone`

**Files:**
- Modify: `internal/dcc/dcc_tunnel.go`

- [ ] **Step 1:** Remove fields + inits + the write in readLoop defer (already done in Task 01).
- [ ] **Step 2:** Verify build+test.
- [ ] **Step 3:** Commit: `chore(dcc): drop unused readDone/writeDone channels`

---

## Task 20 — Unused DCCTunnel methods

**Files:**
- Modify: `internal/dcc/dcc_tunnel.go`

**Fix:** Remove `IsActive`, `GetBot`, `SetIgnoredEvent` (SetIgnoredEvent already removed in Task 09). `IsActive` + `GetBot` are currently unused externally per `grep`.

- [ ] **Step 1:** Confirm unused via grep.
- [ ] **Step 2:** Remove.
- [ ] **Step 3:** Verify build+test.
- [ ] **Step 4:** Commit: `chore(dcc): remove unused exported tunnel helpers`

---

## Task 21 — `OwnerList.GetOwners()` unused

**Files:**
- Modify: `internal/auth/owner.go`

- [ ] **Step 1:** Confirm unused.
- [ ] **Step 2:** Remove method.
- [ ] **Step 3:** Verify build+test.
- [ ] **Step 4:** Commit: `chore(auth): remove unused OwnerList.GetOwners`

---

## Task 22 — `RawTunnel` default target sends to self

**Files:**
- Modify: `internal/bnc/rawtunnel.go:108-117`

**Fix:** Plain-text input without `.` prefix should be a user error — tell them to use `.msg` or `.raw`. Current behavior (send PRIVMSG to bot's own nick) is weird.

- [ ] **Step 1:** Replace default branch with `rt.sendToClient("Prefix commands with '.'. Type '.help' for a list.")`.
- [ ] **Step 2:** Verify build+test.
- [ ] **Step 3:** Commit: `fix(bnc): prompt user instead of sending PRIVMSG to self on bare input`

---

## Task 23 — NickManager RLock opportunities

**Files:**
- Modify: `internal/nickmanager/nickmanager.go`

**Fix:** Switch `Lock()` to `RLock()` in read-only paths: `GetNicksToCatch`, `GetNicks`. Keep `Lock` where mutation happens.

- [ ] **Step 1:** Convert the two pure-read methods.
- [ ] **Step 2:** Verify `go test -race ./internal/nickmanager/...` stays clean (no tests exist here — only build).
- [ ] **Step 3:** Verify build+test.
- [ ] **Step 4:** Commit: `perf(nickmanager): use RLock for pure-read accessors`

---

## Task 24 — Document one-shot `cleanupDisconnectedBots`

**Files:**
- Modify: `internal/bot/manager.go:97-148`

**Fix:** Add comment explaining the intentional one-shot nature + link to CLAUDE.md section.

- [ ] **Step 1:** Add doc comment on method.
- [ ] **Step 2:** Commit: `docs(bot): document one-shot cleanup grace period`

---

## Task 25 — Final sweep

- [ ] **Step 1:** `gofmt -l .` empty.
- [ ] **Step 2:** `go vet ./...` clean.
- [ ] **Step 3:** `go test -race ./...` passes (race detector).
- [ ] **Step 4:** `go build ./...` clean.
- [ ] **Step 5:** Manual: grep for any remaining `time.Sleep` in bot.go that could be removed; note and leave if justified.
- [ ] **Step 6:** No commit unless something needed fixing; then commit with message `chore: post-audit sweep`.
