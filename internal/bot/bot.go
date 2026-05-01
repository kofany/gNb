package bot

import (
	"fmt"
	"math/rand"
	"net"
	"slices"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/kofany/gNb/internal/auth"
	"github.com/kofany/gNb/internal/bnc"
	"github.com/kofany/gNb/internal/config"
	"github.com/kofany/gNb/internal/dcc"
	"github.com/kofany/gNb/internal/nickmanager"
	"github.com/kofany/gNb/internal/runtime"
	"github.com/kofany/gNb/internal/types"
	"github.com/kofany/gNb/internal/util"
	irc "github.com/kofany/go-ircevo"
)

// tmhcMaxAttempts is how many times we let a bot react to a server-side
// "Too many host connections" rejection before we drop it from the manager.
// tmhcRetryDelay is the cooldown between attempts. The library's auto-reconnect
// retries within ~1 s, which would hammer the server while the host limit is
// active; we suppress that by quitting the connection on each detection and
// only re-attempting after the cooldown.
const (
	tmhcMaxAttempts = 3
	tmhcRetryDelay  = 20 * time.Second
)

// isTooManyHostConnectionsError matches the IRC ERROR message both ircd
// variants emit when the per-host connection limit is hit, e.g.
//
//	Closing Link: nick[user@host] (Too many host connections (local))
//	Closing Link: nick[user@host] (Too many host connections (global))
//
// Match is case-insensitive and substring-based so server message phrasing
// variations still trigger the same handling.
func isTooManyHostConnectionsError(msg string) bool {
	return strings.Contains(strings.ToLower(msg), "too many host connections")
}

// Bot represents a single IRC bot
type Bot struct {
	Config          *config.BotConfig
	GlobalConfig    *config.GlobalConfig
	Connection      *irc.Connection
	CurrentNick     string
	Username        string
	Realname        string
	isConnected     atomic.Bool
	isonInFlight    atomic.Bool
	nickChangeBusy  atomic.Bool
	owners          auth.OwnerList
	channels        []string
	joinedChannels  map[string]bool // Track which channels the bot has successfully joined
	nickManager     types.NickManager
	isReconnecting  bool
	lastConnectTime time.Time
	connected       chan struct{}
	botManager      types.BotManager
	gaveUp          bool
	// ISON response handling
	isonRequests       map[string]chan []string // Map of request IDs to response channels
	isonRequestsMutex  sync.Mutex               // Mutex to protect access to the isonRequests map
	ServerName         string                   // Nazwa serwera otrzymana po połączeniu
	bncServer          *bnc.BNCServer
	mutex              sync.Mutex
	dccTunnel          *dcc.DCCTunnel
	channelCheckTicker *time.Ticker  // Ticker for periodic channel check
	channelCheckerStop chan struct{} // Stop signal for the current checker goroutine
	botID              string
	sink               types.EventSink
	runtimeState       *runtime.RuntimeState
	humanlike          *humanlikeRunner
	// Too many host connections handling — see tmhcMaxAttempts/tmhcRetryDelay.
	// tmhcAttempts is guarded by b.mutex; tmhcInProgress is a CAS gate that
	// prevents the library's tight reconnect spin from inflating the counter
	// while a backoff goroutine is in flight.
	tmhcAttempts   int
	tmhcInProgress atomic.Bool
	// banHandled latches on the first 465/466 — subsequent fires (e.g. when
	// the library auto-reconnects between our Quit and quit-flag actually
	// taking effect) are short-circuited so we don't re-emit BotBanned and
	// don't pile up cleanup goroutines.
	banHandled atomic.Bool
}

// GetBotManager returns the BotManager for this bot
func (b *Bot) GetBotManager() types.BotManager {
	return b.botManager
}

// GetNickManager returns the NickManager for this bot
func (b *Bot) GetNickManager() types.NickManager {
	return b.nickManager
}

// SetBotManager sets the BotManager for this bot
func (b *Bot) SetBotManager(manager types.BotManager) {
	b.mutex.Lock()
	defer b.mutex.Unlock()
	b.botManager = manager
}

// SetNickManager sets the NickManager for this bot
func (b *Bot) SetNickManager(manager types.NickManager) {
	b.mutex.Lock()
	defer b.mutex.Unlock()
	b.nickManager = manager
}

// SetEventSink installs the observer used by the Panel API. Called after bot
// construction from the main wiring code; nil is a valid "no-op" value.
func (b *Bot) SetEventSink(sink types.EventSink) {
	b.mutex.Lock()
	b.sink = sink
	b.mutex.Unlock()
}

// GetBotID returns the stable identifier computed from the bot's config slot.
func (b *Bot) GetBotID() string { return b.botID }

// GetJoinedChannels returns a snapshot of channels the bot has joined,
// including auto-joined ones (e.g. #literki on single-letter nicks).
func (b *Bot) GetJoinedChannels() []string {
	b.mutex.Lock()
	defer b.mutex.Unlock()
	out := make([]string, 0, len(b.joinedChannels))
	for ch := range b.joinedChannels {
		out = append(out, ch)
	}
	return out
}

// currentSink returns the active EventSink, or nil if none. Safe under lock.
func (b *Bot) currentSink() types.EventSink {
	b.mutex.Lock()
	defer b.mutex.Unlock()
	return b.sink
}

// NewBot creates a new Bot instance
func NewBot(cfg *config.BotConfig, globalConfig *config.GlobalConfig, nm types.NickManager, bm *BotManager) *Bot {
	nick := bm.getWordFromPool()
	ident := bm.getWordFromPool()
	realname := bm.getWordFromPool()

	bot := &Bot{
		Config:         cfg,
		GlobalConfig:   globalConfig,
		CurrentNick:    nick,
		Username:       ident,
		Realname:       realname,
		nickManager:    nm,
		botManager:     bm,
		isonRequests:   make(map[string]chan []string),
		joinedChannels: make(map[string]bool),
	}

	bot.isConnected.Store(false)
	bot.connected = make(chan struct{})

	nm.RegisterBot(bot)
	return bot
}

func (bm *BotManager) getWordFromPool() string {
	bm.wordPoolMutex.Lock()
	defer bm.wordPoolMutex.Unlock()

	if len(bm.wordPool) == 0 {
		return util.GenerateFallbackNick()
	}

	word := bm.wordPool[0]
	bm.wordPool = bm.wordPool[1:]
	return word
}

// bot.go

func (b *Bot) setConnected(state bool) {
	b.isConnected.Store(state)
}

func (b *Bot) IsConnected() bool {
	if !b.isConnected.Load() || b.Connection == nil {
		return false
	}

	// For tests, just check if the connection is fully connected
	return b.Connection.IsFullyConnected()
}

func (b *Bot) markAsDisconnected() {
	// Snapshot the lib's nick OUTSIDE b.mutex — Connection.GetNick takes
	// irc.Lock, which Connection.Disconnect holds across irc.Wait() (can
	// park up to ~17 min on a silent socket). Holding b.mutex while waiting
	// for irc.Lock would pin the bot's mutex for the same duration.
	var nick string
	if b.Connection != nil {
		nick = b.Connection.GetNick()
	}
	b.mutex.Lock()
	defer b.mutex.Unlock()
	b.setConnected(false)
	if nick != "" {
		b.CurrentNick = nick
	}
}

func (b *Bot) markAsConnected() {
	b.mutex.Lock()
	defer b.mutex.Unlock()
	b.setConnected(true)
}

func (b *Bot) connectWithRetry() error {
	maxRetries := b.GlobalConfig.ReconnectRetries
	baseRetryInterval := time.Duration(b.GlobalConfig.ReconnectInterval) * time.Second

	var lastError error
	for attempts := 0; attempts < maxRetries; attempts++ {
		// Exponential backoff with jitter
		retryInterval := baseRetryInterval * time.Duration(1<<uint(attempts))
		// Add jitter (±20%)
		jitter := float64(retryInterval) * (0.8 + 0.4*rand.Float64())
		retryInterval = time.Duration(jitter)

		// Cap the retry interval at 5 minutes
		retryInterval = minDuration(retryInterval, 5*time.Minute)

		b.mutex.Lock()
		b.connected = make(chan struct{})
		b.mutex.Unlock()

		b.Connection = irc.IRC(b.CurrentNick, b.Username)
		b.Connection.SetLocalIP(b.Config.Vhost)
		b.Connection.VerboseCallbackHandler = false
		b.Connection.Debug = false
		b.Connection.UseTLS = b.Config.SSL
		b.Connection.RealName = b.Realname
		// Increase timeout for better stability
		b.Connection.Timeout = 2 * time.Minute
		// Bound the per-event callback dispatch. With the default (0) the
		// library waits forever on every callback; one slow callback (e.g.
		// the API attach path stalling on a backpressure-close handshake)
		// would park readLoop, which would then never observe irc.end being
		// closed by Disconnect, which holds irc.Lock, which would freeze
		// every API caller of this bot's GetCurrentNick / IsConnected.
		b.Connection.CallbackTimeout = 30 * time.Second
		// Library 1.2.0 manages keepalive and reconnect internally; avoid overriding here

		b.addCallbacks()

		util.Info("Bot %s is attempting to connect to %s (attempt %d/%d, retry interval: %v)",
			b.GetCurrentNick(), b.Config.ServerAddress(), attempts+1, maxRetries, retryInterval)

		if err := b.Connection.Connect(b.Config.ServerAddress()); err != nil {
			lastError = err
			util.Error("Attempt %d: Failed to connect bot %s: %v",
				attempts+1, b.GetCurrentNick(), err)
			b.markAsDisconnected()
			time.Sleep(retryInterval)
			continue
		}

		go b.Connection.Loop()

		select {
		case <-b.connected:
			util.Info("Bot %s successfully connected", b.GetCurrentNick())
			return nil
		case <-time.After(45 * time.Second): // Increased timeout for connection establishment
			if b.IsConnected() {
				util.Info("Bot %s is fully connected, proceeding", b.GetCurrentNick())
				return nil
			}
			lastError = fmt.Errorf("connection timeout")
			b.markAsDisconnected()
			if b.Connection != nil {
				b.Connection.Quit()
			}
		}

		time.Sleep(retryInterval)
	}

	return lastError
}

func (b *Bot) Connect() error {
	b.mutex.Lock()
	if b.IsConnected() {
		b.mutex.Unlock()
		return nil
	}

	b.setConnected(false)
	b.connected = make(chan struct{})
	b.mutex.Unlock()

	err := b.connectWithRetry()
	if err != nil {
		// Capture conn under the lock, then run Connection.Quit() OUTSIDE
		// the lock — Quit sleeps 1 s internally, and Connect is called in a
		// loop by bm.startBotWithRetry for any bot that fails to come up.
		// Holding b.mutex through that 1 s would freeze every API path
		// touching this bot (bot.list -> GetJoinedChannels takes b.mutex);
		// with N unconnected bots and the API iterating them sequentially
		// that compounds into multi-second bot.list timeouts until the
		// 240 s cleanup pulls the unconnected bots out of bm.bots.
		b.mutex.Lock()
		b.setConnected(false)
		conn := b.Connection
		b.mutex.Unlock()
		if conn != nil {
			conn.Quit()
		}
	}

	return err
}

func (b *Bot) logJoinFailure(event *irc.Event) {
	channel := ""
	if len(event.Arguments) > 1 {
		channel = event.Arguments[1]
	} else if len(event.Arguments) > 0 {
		channel = event.Arguments[0]
	}

	util.Warning(
		"Bot %s failed to join %s on %s with %s: %s (args=%v)",
		b.GetCurrentNick(),
		channel,
		b.Config.Server,
		event.Code,
		event.Message(),
		event.Arguments,
	)
}

func (b *Bot) scheduleJoinVerification(channel string) {
	go func(expectedChannel string, nickSnapshot string) {
		time.Sleep(12 * time.Second)

		if !b.IsConnected() {
			util.Warning("Bot %s disconnected before join to %s could be confirmed", nickSnapshot, expectedChannel)
			return
		}

		if b.IsOnChannel(expectedChannel) {
			util.Info("Bot %s confirmed in channel %s", b.GetCurrentNick(), expectedChannel)
			return
		}

		util.Warning(
			"Bot %s did not confirm join to %s within 12s on %s; joined=%v configured_channels=%v",
			b.GetCurrentNick(),
			expectedChannel,
			b.Config.Server,
			b.joinedChannelList(),
			b.configuredChannels(),
		)
	}(channel, b.GetCurrentNick())
}

func (b *Bot) joinedChannelList() []string {
	b.mutex.Lock()
	defer b.mutex.Unlock()

	joined := make([]string, 0, len(b.joinedChannels))
	for channel, ok := range b.joinedChannels {
		if ok {
			joined = append(joined, channel)
		}
	}
	slices.Sort(joined)
	return joined
}

func (b *Bot) configuredChannels() []string {
	b.mutex.Lock()
	defer b.mutex.Unlock()

	channels := make([]string, len(b.channels))
	copy(channels, b.channels)
	return channels
}

func (b *Bot) Quit(message string) {
	// Pull the human-like runner out under the lock, then stop it after
	// release — the runner's part path takes b.mutex, which would
	// deadlock if we held it here.
	b.mutex.Lock()
	runner := b.humanlike
	b.humanlike = nil
	b.mutex.Unlock()
	if runner != nil {
		runner.stopAndPart()
	}

	// Tear down in-process state under the lock — fast, never blocks on I/O.
	b.mutex.Lock()
	if b.channelCheckTicker != nil {
		b.channelCheckTicker.Stop()
		b.channelCheckTicker = nil
	}
	if b.channelCheckerStop != nil {
		close(b.channelCheckerStop)
		b.channelCheckerStop = nil
	}
	b.joinedChannels = make(map[string]bool)
	conn := b.Connection
	if conn != nil {
		conn.QuitMessage = message
	}
	b.setConnected(false)
	if b.dccTunnel != nil {
		b.dccTunnel.Stop()
		b.dccTunnel = nil
	}
	select {
	case <-b.connected:
	default:
		close(b.connected)
	}
	b.mutex.Unlock()

	// Network teardown happens OUTSIDE the lock. Connection.Quit() sleeps
	// 1 s internally before sending QUIT and setting irc.quit=true; if we
	// held b.mutex through that, every API path that touches this bot
	// (bot.list -> GetJoinedChannels, currentSink, markAsDisconnected,
	// etc.) would block for that 1 s. Multiplied by N banned bots during
	// a K-line storm, the API would freeze for many seconds.
	if conn != nil {
		conn.Quit()
	}
}

// disconnectWithDeadline calls conn.Disconnect() but returns within d
// regardless of whether it actually completed. The library's Disconnect()
// holds irc.Lock for the duration of irc.Wait(); when readLoop is parked
// in br.ReadString on a silent socket, Wait() can stall up to the read
// deadline (irc.Timeout + irc.PingFreq, ~17 min). Returning early bounds
// the lock-hold visible to other code paths (notably API GetCurrentNick).
// The leaked goroutine finishes on its own when readLoop's read deadline
// fires; in the meantime we move on.
func disconnectWithDeadline(conn *irc.Connection, d time.Duration) {
	if conn == nil {
		return
	}
	done := make(chan struct{})
	go func() {
		conn.Disconnect()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(d):
		util.Warning("Connection.Disconnect() exceeded %s; leaving cleanup to GC", d)
	}
}

// handleBanCleanup runs after a 465/466 strike. Must NOT be called from a
// library callback goroutine — Disconnect() does irc.Wait() which blocks on
// readLoop exit, and readLoop is the goroutine running the callback. Pattern
// mirrors handleTMHCBackoff: drain Quit (1 s sleep, sets quit=true), then
// Disconnect to close the socket so Loop's outer for-loop sees isQuitting()
// and stops auto-reconnect, then drop from the manager.
func (b *Bot) handleBanCleanup(message string) {
	b.Quit(message)
	b.mutex.Lock()
	conn := b.Connection
	b.mutex.Unlock()
	disconnectWithDeadline(conn, 5*time.Second)
	if b.botManager != nil {
		b.botManager.(*BotManager).RemoveBotFromManager(b)
	}
}

func (b *Bot) addCallbacks() {
	// Callback for successful connection
	b.Connection.AddCallback("001", func(e *irc.Event) {
		if b.IsConnected() {
			return
		}

		// If the bot has been marked terminal (banned, removed, TMHC-exhausted)
		// and the library still reconnected before our quit flag took hold,
		// force-stop the connection here instead of running the normal connected
		// path. Otherwise we'd emit BotConnected for a corpse.
		b.mutex.Lock()
		giveUp := b.gaveUp
		b.mutex.Unlock()
		if giveUp {
			util.Warning("Bot %s: ignoring 001 after gaveUp=true (library auto-reconnected); force-stopping", b.GetCurrentNick())
			go func() {
				b.mutex.Lock()
				conn := b.Connection
				b.mutex.Unlock()
				if conn != nil {
					conn.QuitMessage = "removed"
					conn.Quit()
					disconnectWithDeadline(conn, 5*time.Second)
				}
			}()
			return
		}

		b.markAsConnected()

		// Read from the library OUTSIDE b.mutex — Connection.GetNick takes
		// irc.Lock, which can be held for up to ~17 min by a parallel
		// Connection.Disconnect on another bot's goroutine waiting for that
		// bot's readLoop to exit. Holding b.mutex while waiting for irc.Lock
		// would propagate the stall to every API caller of this bot.
		nick := b.Connection.GetNick()

		// All mutable Bot fields accessed here need the mutex. Previously
		// ServerName, lastConnectTime and the read of b.channels were
		// raced against SetChannels/GetServerName.
		b.mutex.Lock()
		b.ServerName = e.Source
		b.lastConnectTime = time.Now()
		b.CurrentNick = nick
		b.joinedChannels = make(map[string]bool)
		channelsCopy := make([]string, len(b.channels))
		copy(channelsCopy, b.channels)
		serverName := b.ServerName
		currentNick := b.CurrentNick
		// A successful registration clears any prior host-connection-limit
		// strikes — the bot is healthy on this host now.
		b.tmhcAttempts = 0
		b.mutex.Unlock()

		util.Info("Bot %s fully connected to %s as %s", currentNick, serverName, currentNick)

		if sink := b.currentSink(); sink != nil {
			sink.BotConnected(b.botID, currentNick, serverName)
		}

		// The channel watchdog gates auto-join entirely. With the watchdog
		// off, the bot lands on the server but does not enter any channel
		// — the operator can still drive joins manually via the panel.
		wd := b.runtimeWatchdog()
		if wd.Enabled {
			targets := channelsCopy
			if wd.Channel != "" {
				targets = []string{wd.Channel}
			}
			util.Info("Bot %s preparing to join watchdog targets: %v", currentNick, targets)
			for _, channel := range targets {
				b.JoinChannel(channel)
			}
			b.startChannelChecker()
		} else {
			util.Info("Bot %s connected; watchdog disabled — staying out of channels", currentNick)
		}

		// Bring up the human-like runner if the operator already toggled it
		// on while this bot was disconnected.
		b.applyHumanlikeChange(b.runtimeHumanlike())

		// Signal that connection has been established
		select {
		case <-b.connected:
		default:
			close(b.connected)
		}
	})

	// Callback for nick changes
	b.Connection.AddCallback("NICK", func(e *irc.Event) {
		// Library updates connection nick internally. Treat this event as ours
		// if the new nick equals the current connection nick.
		if e.Message() == b.Connection.GetNick() {
			oldNick := e.Nick
			newNick := e.Message()
			b.mutex.Lock()
			b.CurrentNick = newNick
			b.mutex.Unlock()

			if sink := b.currentSink(); sink != nil {
				sink.BotNickChanged(b.botID, oldNick, newNick)
				if len(newNick) == 1 {
					sink.BotNickCaptured(b.botID, newNick, "letter")
				} else if b.nickManager != nil && util.IsTargetNick(newNick, b.nickManager.GetNicksToCatch()) {
					sink.BotNickCaptured(b.botID, newNick, "priority")
				}
			}

			// Notify the nick manager about the change
			if b.nickManager != nil {
				b.nickManager.NotifyNickChange(oldNick, newNick)

				// Check for single letter nick changes
				wasOneLetter := len(oldNick) == 1
				isOneLetter := len(newNick) == 1

				// Bot got a single letter nick
				if !wasOneLetter && isOneLetter {
					util.Debug("Bot %s got single letter nick, joining #literki", newNick)
					b.JoinChannel("#literki")
				}

				// Bot lost a single letter nick
				if wasOneLetter && !isOneLetter {
					util.Debug("Bot %s lost single letter nick, leaving #literki", newNick)
					b.PartChannel("#literki")
				}
			}
		}
	})

	// Callback for ISON response
	b.Connection.AddCallback("303", b.handleISONResponse)
	b.Connection.ClearCallback("CTCP_VERSION")

	b.Connection.AddCallback("CTCP_VERSION", func(e *irc.Event) {
		response := "\x01VERSION WeeChat 4.4.2\x01"
		b.Connection.SendRawf("NOTICE %s :%s", e.Nick, response)
	})

	// List of nick-related error codes
	b.Connection.AddCallback("432", func(e *irc.Event) {
		currentNick := b.Connection.GetNick()
		util.Warning("Bot %s encountered error 432: %s", currentNick, e.Message())
		if len(e.Arguments) > 1 {
			nickInQuestion := e.Arguments[1]
			if len(nickInQuestion) == 1 {
				util.Warning("Server %s does not accept single-letter nick %s. Marking it.",
					b.ServerName, nickInQuestion)
				b.nickManager.MarkServerNoLetters(b.ServerName)
			} else {
				b.nickManager.MarkNickAsTemporarilyUnavailable(nickInQuestion)
			}
		}
	})

	// Callback for unavailable resource (437)
	b.Connection.AddCallback("437", func(e *irc.Event) {
		if !b.IsConnected() {
			// Jeśli bot nie jest w pełni połączony, pozwól bibliotece go-ircevo obsłużyć to standardowo
			return
		}

		if len(e.Arguments) > 1 {
			unavailableNick := e.Arguments[1]

			// Sprawdź czy to nick z naszej listy do złapania lub literka
			if b.nickManager != nil {
				isTargetNick := util.IsTargetNick(unavailableNick, b.nickManager.GetNicksToCatch())
				isLetter := len(unavailableNick) == 1

				if isTargetNick || isLetter {
					util.Warning("Nick %s temporarily unavailable on %s - marking for next iteration",
						unavailableNick, b.ServerName)

					// Oznacz nick jako tymczasowo niedostępny
					if nm, ok := b.nickManager.(*nickmanager.NickManager); ok {
						nm.MarkNickAsTemporarilyUnavailable(unavailableNick)
					}
				}
			}
		}
	})

	for _, code := range []string{"403", "404", "405", "471", "473", "474", "475", "476", "477"} {
		b.Connection.AddCallback(code, b.logJoinFailure)
	}

	// BNC + DCC
	b.Connection.AddCallback("CTCP", b.handleCTCP)
	b.Connection.AddCallback("*", func(e *irc.Event) {
		// Log all events without the "DCC:" prefix
		if b.dccTunnel != nil {
			b.dccTunnel.WriteToConn(e.Raw)
		}
		rawMessage := e.Raw
		b.ForwardToTunnel(rawMessage)
		if sink := b.currentSink(); sink != nil {
			sink.BotIRCEvent(b.botID, e)
		}
	})

	// DCC support
	b.Connection.AddCallback("CTCP_DCC", b.handleDCCRequest)

	b.Connection.AddCallback("CTCP_*", func(e *irc.Event) {
		util.Debug("DCC: CTCP Event Code: %s | Nick: %s | Args: %v | Message: %s", e.Code, e.Nick, e.Arguments, e.Message())
	})

	// Callback for private and public messages
	b.Connection.AddCallback("PRIVMSG", b.handlePrivMsg)

	// Callback for invite handling
	b.Connection.AddCallback("INVITE", b.handleInvite)

	// Callback for JOIN events
	b.Connection.AddCallback("JOIN", func(e *irc.Event) {
		if e.Nick == b.Connection.GetNick() {
			channel := e.Arguments[0]
			util.Info("Bot %s has joined channel %s", b.GetCurrentNick(), channel)
			b.mutex.Lock()
			b.joinedChannels[channel] = true
			b.mutex.Unlock()
			if sink := b.currentSink(); sink != nil {
				sink.BotJoinedChannel(b.botID, channel)
			}
		}
	})

	// Callback for PART events
	b.Connection.AddCallback("PART", func(e *irc.Event) {
		if e.Nick == b.Connection.GetNick() {
			channel := e.Arguments[0]
			util.Debug("Bot %s has left channel %s", b.GetCurrentNick(), channel)
			b.mutex.Lock()
			delete(b.joinedChannels, channel)
			b.mutex.Unlock()
			if sink := b.currentSink(); sink != nil {
				sink.BotPartedChannel(b.botID, channel)
			}
		}
	})

	// Callback for KICK events
	b.Connection.AddCallback("KICK", func(e *irc.Event) {
		if len(e.Arguments) >= 2 && e.Arguments[1] == b.Connection.GetNick() {
			channel := e.Arguments[0]
			reason := ""
			if len(e.Arguments) >= 3 {
				reason = e.Arguments[2]
			}
			util.Warning("Bot %s was kicked from channel %s by %s: %s",
				b.GetCurrentNick(), channel, e.Nick, reason)
			b.mutex.Lock()
			delete(b.joinedChannels, channel)
			b.mutex.Unlock()
			if sink := b.currentSink(); sink != nil {
				sink.BotKicked(b.botID, channel, e.Nick, reason)
			}

			// Try to rejoin after a delay if the watchdog is on and the
			// channel is one of its targets.
			wd := b.runtimeWatchdog()
			if wd.Enabled && slices.Contains(b.watchdogTargets(), channel) {
				go func(channel string) {
					time.Sleep(30 * time.Second)
					if b.IsConnected() && b.runtimeWatchdog().Enabled {
						util.Info("Bot %s attempting to rejoin channel %s after kick",
							b.GetCurrentNick(), channel)
						b.JoinChannel(channel)
					}
				}(channel)
			}
		}
	})

	// Callback dla ERR_YOUREBANNEDCREEP (465) i ERR_YOUWILLBEBANNED (466).
	// Cleanup MUST run off the read-loop goroutine: Connection.Disconnect()
	// internally calls irc.Wait() which blocks until readLoop exits; doing
	// that synchronously from a callback would deadlock readLoop. Quit()'s
	// 1 s sleep would also stall the read-loop for that whole second,
	// blocking every other event the library has in flight. The atomic
	// banHandled flag latches the first hit so library-driven reconnects
	// (which can happen during the race window between our cleanup goroutine
	// starting and the quit flag actually being set) don't re-emit BotBanned
	// or pile up duplicate cleanup goroutines.
	b.Connection.AddCallback("465", func(e *irc.Event) {
		reason := e.Message()
		util.Warning("Bot %s banned from server %s: %s", b.GetCurrentNick(), b.ServerName, reason)
		if !b.banHandled.CompareAndSwap(false, true) {
			return
		}
		b.mutex.Lock()
		b.gaveUp = true
		b.mutex.Unlock()
		if sink := b.currentSink(); sink != nil {
			sink.BotBanned(b.botID, 465)
		}
		go b.handleBanCleanup("Banned from server")
	})

	b.Connection.AddCallback("466", func(_ *irc.Event) {
		util.Warning("Bot %s will be banned from server %s", b.GetCurrentNick(), b.ServerName)
		if !b.banHandled.CompareAndSwap(false, true) {
			return
		}
		b.mutex.Lock()
		b.gaveUp = true
		b.mutex.Unlock()
		if sink := b.currentSink(); sink != nil {
			sink.BotBanned(b.botID, 466)
		}
		go b.handleBanCleanup("Pre-emptive disconnect due to incoming ban")
	})

	// Callback for ERROR — specifically the per-host connection limit. The
	// library's auto-reconnect loop spins ~1 Hz against this rejection, which
	// both spams the server and uses up its connection budget. We claim the
	// in-flight slot via CAS, count the attempt, and let handleTMHCBackoff
	// stop the library's spin and either retry once after the cooldown or
	// drop the bot from the manager once we've exhausted the budget.
	b.Connection.AddCallback("ERROR", func(e *irc.Event) {
		if !isTooManyHostConnectionsError(e.Message()) {
			return
		}
		if !b.tmhcInProgress.CompareAndSwap(false, true) {
			return
		}

		b.mutex.Lock()
		b.tmhcAttempts++
		attempt := b.tmhcAttempts
		b.mutex.Unlock()

		nick := b.GetCurrentNick()
		util.Warning("Bot %s: server reported too many host connections (attempt %d/%d): %s",
			nick, attempt, tmhcMaxAttempts, e.Message())

		go b.handleTMHCBackoff(attempt, nick)
	})

	// Callback for disconnection
	b.Connection.AddCallback("DISCONNECTED", func(e *irc.Event) {
		currentNick := b.GetCurrentNick()
		util.Warning("Bot %s disconnected from server %s: %s", currentNick, b.ServerName, e.Message())
		b.markAsDisconnected()
		if sink := b.currentSink(); sink != nil {
			sink.BotDisconnected(b.botID, e.Message())
		}
		// Library auto-reconnects; we only log here.
	})
}

// RemoveBot implementuje interfejs types.Bot
func (b *Bot) RemoveBot() {
	currentNick := b.GetCurrentNick()

	b.mutex.Lock()
	b.gaveUp = true
	b.mutex.Unlock()

	// Zamykamy połączenie. Nie nil-ujemy pól b.Connection / b.botManager /
	// b.nickManager — callbacki biblioteki mogą jeszcze w locie odpalić
	// po Quit (DISCONNECTED, "*"), a dereference nila zabił by goroutine
	// biblioteki. GC wyczyści jak znikną referencje z managera.
	b.Quit("Bot removed from system")

	if b.botManager != nil {
		if bm, ok := b.botManager.(*BotManager); ok {
			bm.RemoveBotFromManager(b)
		}
	}

	util.Info("Bot %s has been removed from the system", currentNick)
}

func (b *Bot) GetServerName() string {
	b.mutex.Lock()
	defer b.mutex.Unlock()
	return b.ServerName
}

func (b *Bot) handleISONResponse(e *irc.Event) {
	isonResponse := strings.Fields(e.Message())
	util.Debug("Bot %s received ISON response: %v", b.GetCurrentNick(), isonResponse)

	// Lock the mutex to safely access the isonRequests map
	b.isonRequestsMutex.Lock()
	defer b.isonRequestsMutex.Unlock()

	// If there are no active requests, just log and return
	if len(b.isonRequests) == 0 {
		util.Debug("Bot %s received ISON response but no active requests", b.GetCurrentNick())
		return
	}

	// Send the response to all active request channels
	// This ensures that even if multiple requests were made in quick succession,
	// they all receive the response
	for id, ch := range b.isonRequests {
		select {
		case ch <- isonResponse:
			util.Debug("Bot %s sent ISON response to request %s", b.GetCurrentNick(), id)
		default:
			// If a channel is not receiving (which shouldn't happen with proper timeout handling),
			// we'll clean it up in the next cleanup cycle
			util.Warning("Bot %s could not send ISON response to request %s", b.GetCurrentNick(), id)
		}
	}
}

func (b *Bot) handleInvite(e *irc.Event) {
	inviter := e.Nick
	channel := e.Arguments[1]
	currentNick := b.GetCurrentNick()

	if auth.IsOwner(e, b.owners) {
		util.Info("Bot %s received INVITE to %s from owner %s", currentNick, channel, inviter)
		b.JoinChannel(channel)
	} else {
		util.Debug("Bot %s ignored INVITE to %s from non-owner %s", currentNick, channel, inviter)
	}
}

// generateRequestID creates a unique ID for each ISON request
func (b *Bot) generateRequestID() string {
	return fmt.Sprintf("%s-%d", b.GetCurrentNick(), time.Now().UnixNano())
}

// cleanupOldRequests removes request channels that are older than the specified duration
func (b *Bot) cleanupOldRequests() {
	b.isonRequestsMutex.Lock()
	defer b.isonRequestsMutex.Unlock()

	// If there are too many requests (more than 100), clean up to prevent memory leaks
	if len(b.isonRequests) > 100 {
		util.Warning("Bot %s has %d pending ISON requests, cleaning up", b.GetCurrentNick(), len(b.isonRequests))
		b.isonRequests = make(map[string]chan []string)
	}
}

func (b *Bot) RequestISON(nicks []string) ([]string, error) {
	if !b.IsConnected() {
		return nil, fmt.Errorf("bot %s is not connected", b.GetCurrentNick())
	}
	if !b.isonInFlight.CompareAndSwap(false, true) {
		return nil, fmt.Errorf("bot %s already has an active ISON request", b.GetCurrentNick())
	}
	defer b.isonInFlight.Store(false)

	b.cleanupOldRequests()

	// Create a unique ID for this request
	requestID := b.generateRequestID()

	// Create a channel for this specific request
	responseChan := make(chan []string, 1)

	// Register this request
	b.isonRequestsMutex.Lock()
	b.isonRequests[requestID] = responseChan
	b.isonRequestsMutex.Unlock()

	// Send the ISON command
	command := fmt.Sprintf("ISON %s", strings.Join(nicks, " "))
	util.Debug("Bot %s is sending ISON command (ID: %s): %s", b.GetCurrentNick(), requestID, command)
	b.Connection.SendRaw(command)

	// Wait for the response with a timeout
	var response []string
	var err error

	select {
	case response = <-responseChan:
		util.Debug("Bot %s received ISON response for request %s", b.GetCurrentNick(), requestID)
	case <-time.After(5 * time.Second):
		util.Warning("Bot %s did not receive ISON response in time for request %s", b.GetCurrentNick(), requestID)
		err = fmt.Errorf("bot %s did not receive ISON response in time", b.GetCurrentNick())
	}

	// Clean up this request
	b.isonRequestsMutex.Lock()
	delete(b.isonRequests, requestID)
	b.isonRequestsMutex.Unlock()

	return response, err
}

func (b *Bot) ChangeNick(newNick string) {
	if !b.IsConnected() {
		util.Debug("Bot %s is not connected; cannot change nick", b.GetCurrentNick())
		return
	}
	// Fire and forget: success/failure is reflected asynchronously through
	// the NICK / 432 / 437 callbacks. Checking GetNick() after an arbitrary
	// sleep raced the server response and produced misleading "failed" warnings.
	oldNick := b.GetCurrentNick()
	util.Info("Bot %s sending NICK %s", oldNick, newNick)
	b.Connection.Nick(newNick)
	if sink := b.currentSink(); sink != nil {
		sink.BotRawOut(b.botID, "NICK "+newNick)
	}
}

func (b *Bot) JoinChannel(channel string) {
	if b.IsConnected() {
		util.Info("Bot %s sending JOIN for %s on %s", b.GetCurrentNick(), channel, b.Config.Server)
		b.Connection.Join(channel)
		if sink := b.currentSink(); sink != nil {
			sink.BotRawOut(b.botID, "JOIN "+channel)
		}
		b.scheduleJoinVerification(channel)
	} else {
		util.Warning("Bot %s is not connected; cannot join channel %s", b.GetCurrentNick(), channel)
	}
}

func (b *Bot) PartChannel(channel string) {
	if b.IsConnected() {
		util.Debug("Bot %s is leaving channel %s", b.GetCurrentNick(), channel)
		b.Connection.Part(channel)
		if sink := b.currentSink(); sink != nil {
			sink.BotRawOut(b.botID, "PART "+channel)
		}

		// Update joined channels map
		b.mutex.Lock()
		delete(b.joinedChannels, channel)
		b.mutex.Unlock()
	} else {
		util.Debug("Bot %s is not connected; cannot part channel %s", b.GetCurrentNick(), channel)
	}
}

// IsOnChannel reports whether the bot has confirmed JOIN for the channel.
func (b *Bot) IsOnChannel(channel string) bool {
	b.mutex.Lock()
	defer b.mutex.Unlock()
	return b.joinedChannels[channel]
}

// startChannelChecker starts a periodic check to ensure the bot is in all
// required channels. Uses its own stop channel instead of b.connected, which
// is closed on first Quit and never recreated when the library auto-reconnects.
func (b *Bot) startChannelChecker() {
	b.mutex.Lock()
	if b.channelCheckTicker != nil {
		b.channelCheckTicker.Stop()
	}
	if b.channelCheckerStop != nil {
		close(b.channelCheckerStop)
	}
	ticker := time.NewTicker(5 * time.Minute)
	stop := make(chan struct{})
	b.channelCheckTicker = ticker
	b.channelCheckerStop = stop
	b.mutex.Unlock()

	go func() {
		for {
			select {
			case <-ticker.C:
				if b.IsConnected() {
					b.checkAndRejoinChannels()
				}
			case <-stop:
				return
			}
		}
	}()
}

// checkAndRejoinChannels checks if the bot is in all required channels and
// rejoins if necessary. Honors the runtime watchdog: a no-op when the
// feature is off, and uses the override channel (if set) instead of the
// bot's full configured list.
func (b *Bot) checkAndRejoinChannels() {
	if !b.IsConnected() {
		return
	}
	wd := b.runtimeWatchdog()
	if !wd.Enabled {
		return
	}
	for _, channel := range b.watchdogTargets() {
		if !b.IsOnChannel(channel) {
			util.Info("Bot %s is not in channel %s, rejoining (watchdog)", b.GetCurrentNick(), channel)
			b.JoinChannel(channel)
		}
	}
}

func (b *Bot) Reconnect() {
	if !b.nickChangeBusy.CompareAndSwap(false, true) {
		util.Debug("Bot %s is already busy, skipping reconnect request", b.GetCurrentNick())
		return
	}
	defer b.nickChangeBusy.Store(false)

	b.mutex.Lock()
	if b.isReconnecting {
		b.mutex.Unlock()
		return
	}
	b.isReconnecting = true
	conn := b.Connection
	b.mutex.Unlock()

	defer func() {
		b.mutex.Lock()
		b.isReconnecting = false
		b.mutex.Unlock()
	}()

	if conn != nil {
		conn.QuitMessage = "Reconnecting"
		// Quit() sends QUIT and sets irc.quit=true (after a 1s internal sleep),
		// which stops the library's auto-reconnect in Loop(). Disconnect()
		// closes the socket and end channel so the old Loop exits promptly
		// instead of lingering on the read. Without Disconnect there is a
		// 1–several-second window where both the old and new connections
		// are live against the server simultaneously.
		conn.Quit()
		disconnectWithDeadline(conn, 5*time.Second)
	}

	newNick, err := util.GenerateRandomNick(b.GlobalConfig.NickAPI.URL, b.GlobalConfig.MaxNickLength, b.GlobalConfig.NickAPI.Timeout)
	if err != nil {
		newNick = util.GenerateFallbackNick()
	}

	b.mutex.Lock()
	b.CurrentNick = newNick
	b.Connection = nil
	b.connected = make(chan struct{})
	b.mutex.Unlock()

	util.Info("Bot is reconnecting with new nick %s", newNick)
	if err := b.connectWithRetry(); err != nil {
		util.Error("Bot %s failed to reconnect: %v", newNick, err)
		b.mutex.Lock()
		b.gaveUp = true
		b.mutex.Unlock()
		return
	}
	// The 001 callback rejoins the configured channels on a successful
	// reconnect; the 5-minute channel checker catches any that are missed.
	// No arbitrary post-connect sleep needed.
}

// handleTMHCBackoff cools off after a "Too many host connections" rejection
// and either reattempts a fresh registration or removes the bot once the
// per-bot budget (tmhcMaxAttempts) is exhausted. Always called as a
// goroutine from the ERROR callback so the library's read loop is not
// blocked by Quit's internal 1 s pause.
func (b *Bot) handleTMHCBackoff(attempt int, nick string) {
	defer b.tmhcInProgress.Store(false)

	// Stop the library's tight reconnect spin on the current connection.
	// Quit() flips irc.quit=true (after a 1 s internal sleep) so the
	// library's Loop exits; Disconnect() closes the socket so the loop
	// doesn't linger on the read.
	b.mutex.Lock()
	conn := b.Connection
	b.mutex.Unlock()
	if conn != nil {
		conn.QuitMessage = "throttled (host connection limit)"
		conn.Quit()
		disconnectWithDeadline(conn, 5*time.Second)
	}

	if attempt >= tmhcMaxAttempts {
		util.Warning("Bot %s: removing from manager after %d host-connection-limit hits", nick, tmhcMaxAttempts)
		if sink := b.currentSink(); sink != nil {
			sink.BotDisconnected(b.botID, fmt.Sprintf("removed: too many host connections (after %d attempts)", attempt))
		}
		b.RemoveBot()
		return
	}

	util.Info("Bot %s: cooling off %s before retry %d/%d", nick, tmhcRetryDelay, attempt+1, tmhcMaxAttempts)
	time.Sleep(tmhcRetryDelay)

	// Bail out if the bot was removed (e.g. via .reconnect, ban, manager Stop)
	// while we were sleeping.
	b.mutex.Lock()
	if b.gaveUp {
		b.mutex.Unlock()
		return
	}
	b.Connection = nil
	b.connected = make(chan struct{})
	b.mutex.Unlock()

	if err := b.connectWithRetry(); err != nil {
		util.Warning("Bot %s: TMHC retry connect failed: %v", nick, err)
		b.mutex.Lock()
		b.gaveUp = true
		b.mutex.Unlock()
	}
}

func (b *Bot) SendMessage(target, message string) {
	if b.IsConnected() {
		util.Debug("Bot %s is sending message to %s: %s", b.GetCurrentNick(), target, message)
		b.Connection.Privmsg(target, message)
		if sink := b.currentSink(); sink != nil {
			sink.BotRawOut(b.botID, "PRIVMSG "+target+" :"+message)
		}
	} else {
		util.Debug("Bot %s is not connected; cannot send message to %s", b.GetCurrentNick(), target)
	}
}

func (b *Bot) AttemptNickChange(nick string) {
	if !b.nickChangeBusy.CompareAndSwap(false, true) {
		util.Debug("Bot %s is already handling a nick change, skipping %s", b.GetCurrentNick(), nick)
		return
	}
	defer b.nickChangeBusy.Store(false)

	currentNick := b.GetCurrentNick()
	util.Debug("Bot %s received request to change nick to %s", currentNick, nick)
	if b.shouldChangeNick(nick) {
		util.Info("Bot %s is attempting to change nick to %s", currentNick, nick)
		b.ChangeNick(nick)
	} else {
		util.Debug("Bot %s decided not to change nick to %s", currentNick, nick)
		b.nickManager.ReturnNickToPool(nick)
	}
}

func (b *Bot) shouldChangeNick(nick string) bool {
	currentNick := b.GetCurrentNick()
	if util.IsTargetNick(currentNick, b.nickManager.GetNicksToCatch()) {
		return false
	}
	return currentNick != nick
}

func (b *Bot) handlePrivMsg(e *irc.Event) {
	util.Debug("Received PRIVMSG: target=%s, sender=%s, message=%s", e.Arguments[0], e.Nick, e.Message())
	b.HandleCommands(e)
}

func (b *Bot) SetOwnerList(owners auth.OwnerList) {
	b.mutex.Lock()
	defer b.mutex.Unlock()
	b.owners = owners
	// Use b.CurrentNick directly since we already have the mutex locked
	util.Debug("Bot %s set owners: %v", b.CurrentNick, owners)
}

func (b *Bot) SetChannels(channels []string) {
	b.mutex.Lock()
	defer b.mutex.Unlock()
	b.channels = channels
	// Use b.CurrentNick directly since we already have the mutex locked
	util.Debug("Bot %s set channels: %v", b.CurrentNick, channels)
}

// GetCurrentNick returns the bot's confirmed nick from local state ONLY.
// Crucially this does NOT call into b.Connection.GetNick() — that would take
// the library's irc.Lock(), which Connection.Disconnect() holds while running
// irc.Wait(). When readLoop is parked in br.ReadString on a silent socket
// (no server close, no QUIT echo), Disconnect's Wait blocks until the read
// deadline fires (irc.Timeout + irc.PingFreq, up to ~17 minutes). During
// that whole window any caller of GetCurrentNick would hang too. bot.list
// iterates every bot and calls this method, so a single stuck bot would
// time out the whole API request — exactly the panel symptom we saw.
//
// b.CurrentNick is maintained under b.mutex by the 001 callback (post-
// registration confirmation) and the NICK callback (post-rename
// confirmation) so it is the same value Connection.GetNick() would return,
// just held in a mutex we control.
func (b *Bot) GetCurrentNick() string {
	b.mutex.Lock()
	defer b.mutex.Unlock()
	return b.CurrentNick
}

// BNC

type BNCServer struct {
	Port     int
	Password string
	Tunnel   *bnc.RawTunnel
}

func (b *Bot) StartBNC() (int, string, error) {
	util.Debug("StartBNC called for bot %s", b.GetCurrentNick())
	if b.bncServer != nil {
		util.Debug("BNC already active for bot %s", b.GetCurrentNick())
		return 0, "", fmt.Errorf("BNC already active for this bot")
	}

	server, err := bnc.StartBNCServer(b)
	if err != nil {
		util.Error("Failed to start BNC server for bot %s: %v", b.GetCurrentNick(), err)
		return 0, "", err
	}

	b.bncServer = server
	util.Debug("BNC server started successfully for bot %s on port %d", b.GetCurrentNick(), server.Port)
	return server.Port, server.Password, nil
}

func (b *Bot) StopBNC() {
	util.Debug("StopBNC called for bot %s", b.GetCurrentNick())

	if b.bncServer != nil {
		b.bncServer.Stop()
		b.bncServer = nil
		util.Debug("BNC server stopped for bot %s", b.GetCurrentNick())
	} else {
		util.Debug("No active BNC server for bot %s", b.GetCurrentNick())
	}
}

func (b *Bot) SendRaw(message string) {
	if b.IsConnected() {
		b.Connection.SendRaw(message)
		if b.bncServer != nil && b.bncServer.Tunnel != nil {
			b.bncServer.Tunnel.WriteToConn(message)
		}
		if sink := b.currentSink(); sink != nil {
			sink.BotRawOut(b.botID, message)
		}
	}
}

func (b *Bot) ForwardToTunnel(data string) {
	if b.bncServer != nil && b.bncServer.Tunnel != nil {
		b.bncServer.Tunnel.WriteToConn(data)
	}
}

// DCC support
func (b *Bot) handleDCCRequest(e *irc.Event) {
	util.Debug("DCC: handleDCCRequest called with Event Code: %s | Nick: %s | Args: %v | Message: %s",
		e.Code, e.Nick, e.Arguments, e.Message())

	ctcpMessage := e.Message()
	dccArgs := strings.Fields(ctcpMessage)
	util.Debug("DCC: Parsed DCC arguments: %v", dccArgs)

	if len(dccArgs) < 4 || strings.ToUpper(dccArgs[0]) != "DCC" || strings.ToUpper(dccArgs[1]) != "CHAT" {
		util.Debug("DCC: Not a DCC CHAT request from %s. DCC Command: %s", e.Nick, dccArgs[1])
		return
	}

	argIndex := 2
	if strings.ToLower(dccArgs[argIndex]) == "chat" {
		argIndex++
	}

	if len(dccArgs) <= argIndex+1 {
		util.Debug("DCC: Not enough arguments for DCC CHAT request from %s", e.Nick)
		return
	}

	ipStr := dccArgs[argIndex]
	portStr := dccArgs[argIndex+1]

	// Sprawdź, czy adres IP jest liczbą (dla IPv4)
	ipNum, err := strconv.ParseUint(ipStr, 10, 64)
	if err == nil {
		// Adres IP jest liczbą - konwertuj na IPv4
		ip := intToIP(uint32(ipNum))
		ipStr = ip.String()
		util.Debug("DCC: Converted numeric IP %s to dotted format: %s", dccArgs[argIndex], ipStr)
	} else {
		// Adres IP nie jest liczbą - załóż, że to adres tekstowy (IPv6 lub IPv4)
		util.Debug("DCC: IP address is not numeric, using textual IP: %s", ipStr)
		parsedIP := net.ParseIP(ipStr)
		if parsedIP == nil {
			util.Warning("DCC: Invalid IP address in DCC CHAT request from %s: %s", e.Nick, ipStr)
			return
		}
	}

	if !auth.IsOwner(e, b.owners) {
		util.Debug("DCC: Ignoring DCC CHAT request from non-owner %s", e.Nick)
		return
	}

	util.Info("DCC: Accepting DCC CHAT request from owner %s", e.Nick)

	port, err := strconv.Atoi(portStr)
	if err != nil {
		util.Warning("DCC: Invalid port in DCC CHAT request from %s: %v", e.Nick, err)
		return
	}

	addr := net.JoinHostPort(ipStr, strconv.Itoa(port))
	util.Debug("DCC: Connecting to %s for DCC CHAT", addr)

	// Wybierz odpowiedni protokół (tcp4 lub tcp6)
	var network string
	if strings.Contains(ipStr, ":") {
		network = "tcp6"
	} else {
		network = "tcp4"
	}

	conn, err := net.Dial(network, addr)
	if err != nil {
		util.Error("DCC: Failed to connect to DCC CHAT from %s: %v", e.Nick, err)
		return
	}

	b.dccTunnel = dcc.NewDCCTunnel(b, e.Nick, func() {
		b.dccTunnel = nil
	})

	util.Debug("DCC: Starting DCC tunnel with %s", e.Nick)
	b.dccTunnel.Start(conn)
}

func intToIP(intIP uint32) net.IP {
	return net.IPv4(byte(intIP>>24), byte(intIP>>16), byte(intIP>>8), byte(intIP))
}

func (b *Bot) handleCTCP(e *irc.Event) {
	// DCC requests arrive on CTCP_DCC via the dedicated callback; do not
	// re-dispatch here or the request is handled twice (double net.Dial,
	// the second call overwrites b.dccTunnel and the first leaks).
	util.Debug("CTCP Event | Nick: %s | Args: %v | Message: %s", e.Nick, e.Arguments, e.Message())
}

// minDuration returns the smaller of two time.Duration values.
func minDuration(a, b time.Duration) time.Duration {
	if a < b {
		return a
	}
	return b
}
