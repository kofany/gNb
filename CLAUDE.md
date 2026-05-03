# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Project-wide rules

- **Always use the latest stable versions** of every package, library, framework, and runtime. Before proposing or upgrading any dep, verify the current latest release live (npmjs / pkg.go.dev / jsr / GitHub releases) — do not rely on assistant memory for version numbers, it may be stale.
- **Passwordless sudo is available** in this environment — invoke `sudo` directly for any system-level installs/services without prompting the user for a password.

## Build, run, test

- `make` — runs `go mod tidy` then builds a CGO-free static binary named `gNb` (capital N) via `cmd/main.go`. If invoked as root, additionally copies it to `/bin/gNb`. There is no separate `lint` or `build` target — `make tidy` runs `go mod tidy` in isolation.
- `./gNb -dev` — foreground mode; forces log level to `DEBUG` regardless of `global.log_level` and writes to `bot_dev.log`.
- `./gNb` (no flag) — daemonizes via `sevlyar/go-daemon`, forces log level to `WARNING`, writes `bot.log` and `bot.pid`.
- `./gNb -v` / `-version` — print version and exit.
- On first run, missing `configs/{config.yaml,owners.json}` and `data/nicks.json` are auto-created with placeholder examples and the program exits so the operator can edit them (see `internal/config/config.go:CheckAndCreateConfigFiles`).
- `go test ./...` runs everything. Current tests live in `internal/bot/bot_test.go` and `internal/util/nickgen_test.go`. Single test: `go test ./internal/bot -run TestSendSingleMsgUsesOnlyBotsJoinedToChannel`.
- `go build -o buildnickbanks ./cmd/buildnickbanks` builds the auxiliary tool that regenerates `data/nick_leads.gob` and `data/nick_tails.gob` from external word-source repos cloned into `/tmp/gNb-wordsrc` (see flags `-src-root`, `-out-dir`).

## Architecture

Entry point `cmd/main.go` wires the whole program in a fixed order that matters:

1. `config.CheckAndCreateConfigFiles` — scaffolds configs on first run.
2. `config.LoadConfig("configs/config.yaml")` — loaded *before* daemonization because oidentd needs it.
3. `oidentd.SetupOidentd(cfg)` — only runs when euid == 0 AND `/etc/oidentd.conf` exists AND `/etc/os-release` identifies Debian. It rewrites `/etc/oidentd.conf` with `len(cfg.Bots)+10` random idents generated from `data/words.gob` and restarts `oidentd.service`. This happens *before* the daemon fork.
4. Daemonize (or stay in foreground for `-dev`), then initialize the logger (`util.InitLogger`). Two different log files are used depending on mode.
5. `auth.LoadOwners`, `nickmanager.NewNickManager` + `LoadNicks`, `bot.NewBotManager(cfg, owners, nm)` — in that order.
6. `go botManager.StartBots()` followed by `nm.Start()` (inside the same goroutine, after bots have finished their connect attempts).
7. Block on SIGINT/SIGTERM, then `botManager.Stop()`.

### Three core types, one interface file

`internal/types/interfaces.go` defines `Bot`, `NickManager`, and `BotManager` as interfaces. Concrete implementations live in:

- `internal/bot/bot.go` — `*Bot`, one per IRC connection. Wraps `github.com/kofany/go-ircevo` `*irc.Connection`. The set of registered callbacks (`001`, `NICK`, `303`, `432`, `433`, `436`, `437`, `JOIN`, `PART`, `KICK`, `465`, `466`, `INVITE`, `PRIVMSG`, `CTCP`, `CTCP_DCC`, `CTCP_VERSION`, `DISCONNECTED`, `403/404/405/471/473/474/475/476/477`, and `*`) *is* the bot's behavior. When editing, preserve this set and note that the library (≥ v1.2.0) manages auto-reconnect and keepalive internally — do not reintroduce manual reconnect loops or `Connection.KeepAlive` overrides. The bot sets `Connection.AutoNickRecoveryPostRegistration = false` (go-ircevo ≥ v1.3.0) so library auto-mangle of failed NICK attempts (`u → u_ → u__`) is disabled post-001 — pre-001 recovery is still active so registration completes.
- `internal/bot/manager.go` — `*BotManager`, owns the `[]types.Bot` slice, owner list, mass-command cooldown map, and a word pool pre-sized at `len(cfg.Bots)*3 + 10` (one each for nick, ident, realname plus spares). `cleanupDisconnectedBots` fires 240s after startup and prunes bots that never connected. `RemoveBotFromManager` is the single path for bot removal and must be called *outside* `bm.mutex`.
- `internal/nickmanager/nickmanager.go` — `*NickManager`. Runs two tickers: `updateInterval` (10s) refreshes the `connectedBots` cache under a separate `connectedBotsMutex`; an internal 1s ticker picks the next connected bot round-robin and fires a single `RequestISON` with the full `nicksToCatch` list (priority nicks from `data/nicks.json` plus the 26 single letters `a`–`z`). Free target nicks are assigned to available bots via `bot.AttemptNickChange`, with a 60s temp-unavailable block to prevent thrashing. Per-server "no single-letter nicks" flag set from IRC 432 responses (`MarkServerNoLetters`) is honored when assigning.

### Cross-cutting behaviors worth knowing

- **Channel reply routing.** `BotManager.SendSingleMsg(channel, msg)` round-robins *only over bots actually joined to the target channel* (tracked in `Bot.joinedChannels`). If no bot is on the channel, the reply is silently dropped with a warning. `bot_test.go` pins this behavior — keep it.
- **Mass commands.** `join`, `part`, and `reconnect` are dispatched to every bot simultaneously but gated by `mass_command_cooldown` via `CanExecuteMassCommand` (per-command-name lock). All other commands in `internal/bot/commands.go` are single-bot.
- **`#literki` auto-membership.** The NICK callback in `bot.go` auto-joins `#literki` when a bot acquires a single-letter nick and auto-parts when it loses one. This is hardcoded; disable by editing the NICK callback as noted in the README.
- **Nick generation is fully local.** `util/nickgen.go` composes nicks from `data/nick_leads.gob` + `data/nick_tails.gob` (with hardcoded fallback lists). The names `GetWordsFromAPI` and `GenerateRandomNick` still take a URL parameter and the YAML has a `nick_api` section, but the URL/timeout are ignored — no HTTP is performed. `cmd/buildnickbanks` is how the gob banks get rebuilt. `util.GenerateFallbackNick` is the last-resort path.
- **ISON concurrency.** Each `Bot` maintains `isonRequests map[requestID]chan []string` with its own mutex; the `303` callback fans responses out to every pending channel. `RequestISON` uses `isonInFlight` atomic CAS to ensure only one in-flight request per bot at a time and times out at 5s. `cleanupOldRequests` flushes the map if it ever exceeds 100 entries.
- **BNC.** `internal/bnc/server.go` starts an ephemeral SSH server per bot: random port 4000–4999, in-memory ed25519 host key, 16-char password. Auth requires SSH username == bot's current nick AND the password passed as the SSH command argument. Once authenticated, `rawtunnel.go` forwards raw IRC both directions.
- **DCC.** `Bot.handleDCCRequest` accepts DCC CHAT only from owners (`auth.IsOwner`), supports both numeric IPv4 and textual IPv6 addresses, and opens a single `dcc.DCCTunnel` per bot.
- **Owner auth.** Owners are IRC-style masks (`*!ident@host`) in `configs/owners.json`; matching happens in `util/matcher.go`. `BotManager.AddOwner`/`RemoveOwner` persist back to the JSON file and then push the updated list into every bot via `SetOwnerList`.
- **Error-driven removal.** IRC numerics 465 (banned) and 466 (will-be-banned) trigger `RemoveBotFromManager` and nil-out the bot's `Connection`, `botManager`, and `nickManager` refs — treat these codes as terminal for that connection.

### Source-style notes

- Comments and occasional log strings are in Polish; when editing existing code, matching style (or leaving surrounding Polish comments intact) is fine. New standalone code can be in English.
- Several files use `b.mutex` for `Bot` internals and `bm.mutex` / `bm.connectedBotsMutex` separately in `BotManager` / `NickManager`. Read the surrounding code before adding a lock — there are explicit non-locking variants (`filterAvailableNicksNonLocking`) that exist specifically to keep the main mutex short.
