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
	"unicode"

	"github.com/kofany/gNb/internal/auth"
	"github.com/kofany/gNb/internal/config"
	"github.com/kofany/gNb/internal/dcc"
	"github.com/kofany/gNb/internal/types"
	"github.com/kofany/gNb/internal/util"
	irc "github.com/kofany/go-ircevo"
)

// Bot represents a single IRC bot
type Bot struct {
	Config             *config.BotConfig
	GlobalConfig       *config.GlobalConfig
	Connection         *irc.Connection
	PreviousNick       string // Store the previous nick for recovery during reconnection
	Username           string
	Realname           string
	isConnected        atomic.Bool
	owners             auth.OwnerList
	channels           []string
	joinedChannels     map[string]bool // Track which channels the bot has successfully joined
	isReconnecting     bool
	lastConnectTime    time.Time
	connected          chan struct{}
	botManager         types.BotManager
	gaveUp             bool
	isonResponse       chan []string
	ServerName         string // Nazwa serwera otrzymana po połączeniu
	mutex              sync.Mutex
	dccTunnel          *dcc.DCCTunnel
	channelCheckTicker *time.Ticker // Ticker for periodic channel check
}

// GetBotManager returns the BotManager for this bot
func (b *Bot) GetBotManager() types.BotManager {
	return b.botManager
}

// SetBotManager sets the BotManager for this bot
func (b *Bot) SetBotManager(manager types.BotManager) {
	b.mutex.Lock()
	defer b.mutex.Unlock()
	b.botManager = manager
}

// NewBot creates a new Bot instance
func NewBot(cfg *config.BotConfig, globalConfig *config.GlobalConfig, bm *BotManager) *Bot {
	nick := bm.getWordFromPool()
	ident := bm.getWordFromPool()
	realname := bm.getWordFromPool()

	bot := &Bot{
		Config:         cfg,
		GlobalConfig:   globalConfig,
		Username:       ident,
		Realname:       realname,
		botManager:     bm,
		isonResponse:   make(chan []string, 10), // Zwiększamy bufor, aby uniknąć blokowania
		joinedChannels: make(map[string]bool),
	}

	bot.isConnected.Store(false)
	bot.connected = make(chan struct{})

	// Initialize the connection with the nick
	bot.Connection = irc.IRC(nick, ident)
	bot.Connection.SetLocalIP(cfg.Vhost)
	bot.Connection.VerboseCallbackHandler = false
	bot.Connection.Debug = false
	bot.Connection.UseTLS = cfg.SSL
	bot.Connection.RealName = realname

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
	// Get the current state for logging
	isConnectedAtomic := b.isConnected.Load()
	hasConnection := b.Connection != nil

	// First check our atomic flag and connection
	if !isConnectedAtomic || !hasConnection {
		// Only log at debug level to avoid spamming
		// util.Debug("Bot %s IsConnected check failed: isConnectedAtomic=%v, hasConnection=%v",
		// 	b.GetCurrentNick(), isConnectedAtomic, hasConnection)
		return false
	}

	// We'll primarily rely on our own isConnected flag
	// The library's IsFullyConnected() method seems unreliable
	// Only check if we have a valid ServerName as a secondary check
	hasServerName := b.ServerName != ""
	if !hasServerName {
		// Only log at debug level to avoid spamming
		// util.Debug("Bot %s IsConnected check failed: hasServerName=%v",
		// 	b.GetCurrentNick(), hasServerName)
		return false
	}

	// util.Debug("Bot %s IsConnected check passed: isConnectedAtomic=%v, hasConnection=%v, hasServerName=%v",
	// 	b.GetCurrentNick(), isConnectedAtomic, hasConnection, hasServerName)
	return true
}

func (b *Bot) markAsDisconnected() {
	b.mutex.Lock()
	defer b.mutex.Unlock()
	b.setConnected(false)
}

func (b *Bot) markAsConnected() {
	b.mutex.Lock()
	defer b.mutex.Unlock()
	b.setConnected(true)
}

func (b *Bot) connectWithRetry() error {
	maxRetries := b.GlobalConfig.ReconnectRetries
	baseRetryInterval := time.Duration(b.GlobalConfig.ReconnectInterval) * time.Second

	util.Debug("=== CONNECTION ATTEMPT STARTED ===")
	util.Debug("Bot config: Server=%s, SSL=%v, Vhost=%s",
		b.Config.ServerAddress(), b.Config.SSL, b.Config.Vhost)
	util.Debug("Max retries: %d, Base retry interval: %v", maxRetries, baseRetryInterval)

	var lastError error
	for attempts := range maxRetries {
		// Exponential backoff with jitter
		retryInterval := baseRetryInterval * time.Duration(1<<uint(attempts))
		// Add jitter (±20%)
		jitter := float64(retryInterval) * (0.8 + 0.4*rand.Float64())
		retryInterval = time.Duration(jitter)

		// Cap the retry interval at 5 minutes
		retryInterval = min(retryInterval, 5*time.Minute)

		b.mutex.Lock()
		b.connected = make(chan struct{})
		b.mutex.Unlock()

		// Use the existing connection or create a new one with the current nick from the library
		if b.Connection == nil {
			nick := util.GenerateFallbackNick()
			util.Debug("Creating new IRC connection with nick: %s, username: %s", nick, b.Username)
			b.Connection = irc.IRC(nick, b.Username)
		} else {
			util.Debug("Using existing IRC connection with nick: %s", b.Connection.GetNick())
		}

		// Log connection settings
		util.Debug("Setting connection parameters: Vhost=%s, SSL=%v, Realname=%s",
			b.Config.Vhost, b.Config.SSL, b.Realname)

		b.Connection.SetLocalIP(b.Config.Vhost)
		b.Connection.VerboseCallbackHandler = false
		b.Connection.Debug = true // Enable debug mode to see raw IRC messages
		b.Connection.UseTLS = b.Config.SSL
		b.Connection.RealName = b.Realname
		// Increase timeout for better stability
		b.Connection.Timeout = 3 * time.Minute // Increased from 2 to 3 minutes
		b.Connection.KeepAlive = 5 * time.Minute
		// Set HandleErrorAsDisconnect to false to allow the library to handle reconnections
		b.Connection.HandleErrorAsDisconnect = false
		// Set a very long ping frequency during initial connection
		// Note: Cannot set to 0 as it causes a panic in NewTicker
		b.Connection.PingFreq = 30 * time.Minute // Very infrequent pings during initial connection

		util.Debug("Adding IRC callbacks")
		b.addCallbacks()

		util.Info("Bot is attempting to connect to %s (attempt %d/%d, retry interval: %v)",
			b.Config.ServerAddress(), attempts+1, maxRetries, retryInterval)

		util.Debug("Calling Connection.Connect() to %s", b.Config.ServerAddress())

		// Create a channel to receive connection errors
		errChan := make(chan error, 1)

		// Connect in a separate goroutine to handle potential blocking
		go func() {
			errChan <- b.Connection.Connect(b.Config.ServerAddress())
		}()

		// Wait for connection result with timeout
		select {
		case err := <-errChan:
			if err != nil {
				lastError = err
				util.Error("Attempt %d: Failed to connect bot: %v",
					attempts+1, err)
				b.markAsDisconnected()
				util.Debug("Connection failed, will retry in %v", retryInterval)
				time.Sleep(retryInterval)
				continue
			}
			util.Debug("Connection.Connect() succeeded without error")
		case <-time.After(30 * time.Second):
			// If Connect() is taking too long, it might be stuck
			util.Warning("Connection.Connect() is taking too long, assuming it's stuck")
			b.markAsDisconnected()
			if b.Connection != nil {
				// Try to force close the connection
				util.Debug("Attempting to force close the stuck connection")
				b.Connection.Quit()
				b.Connection = nil
			}
			lastError = fmt.Errorf("connection attempt timed out")
			time.Sleep(retryInterval)
			continue
		}

		util.Debug("Connection.Connect() succeeded, starting event loop")
		go b.Connection.Loop()

		util.Debug("Waiting for connection confirmation or timeout")

		// Use a longer timeout for servers that might be slow to respond
		connectionTimeout := 90 * time.Second // Increased from 45 to 90 seconds
		util.Debug("Connection confirmation timeout set to %v", connectionTimeout)

		// Set up periodic checks for connection status
		checkInterval := 15 * time.Second
		checkTicker := time.NewTicker(checkInterval)
		defer checkTicker.Stop()

		// Set up timeout for overall connection process
		timeoutTimer := time.NewTimer(connectionTimeout)
		defer timeoutTimer.Stop()

		for {
			select {
			case <-b.connected:
				// Safely get the current nick
				currentNick := b.GetCurrentNick()
				util.Info("Bot successfully connected as %s", currentNick)
				util.Debug("=== CONNECTION ATTEMPT COMPLETED SUCCESSFULLY ===")
				return nil

			case <-checkTicker.C:
				// Periodically check if we're connected
				util.Debug("Performing periodic connection status check")
				if b.IsConnected() {
					// Safely get the current nick
					currentNick := b.GetCurrentNick()
					util.Info("Bot is fully connected as %s (detected in periodic check)", currentNick)
					util.Debug("=== CONNECTION ATTEMPT COMPLETED SUCCESSFULLY (detected in check) ===")
					return nil
				}

				// Check if we've received any server messages
				// Note: We can't access the lastMessage field directly as it's unexported
				if b.Connection != nil {
					util.Debug("Connection is still active, waiting for server response")
				}

			case <-timeoutTimer.C:
				// Final timeout reached
				util.Debug("Connection timeout reached (%v), checking if connected", connectionTimeout)
				if b.IsConnected() {
					// Safely get the current nick
					currentNick := b.GetCurrentNick()
					util.Info("Bot is fully connected as %s, proceeding", currentNick)
					util.Debug("=== CONNECTION ATTEMPT COMPLETED SUCCESSFULLY (after timeout) ===")
					return nil
				}

				lastError = fmt.Errorf("connection timeout after %v", connectionTimeout)
				util.Debug("Connection timed out and bot is not connected, will retry")
				b.markAsDisconnected()
				if b.Connection != nil {
					// Safely quit to avoid potential panics
					func() {
						defer func() {
							if r := recover(); r != nil {
								util.Error("Recovered from panic in Connection.Quit: %v", r)
							}
						}()
						util.Debug("Calling Connection.Quit() after timeout")
						b.Connection.Quit()
					}()
				}
				return lastError
			}
		}
	}

	util.Debug("=== CONNECTION ATTEMPTS EXHAUSTED ===")
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
		b.mutex.Lock()
		b.setConnected(false)
		if b.Connection != nil {
			b.Connection.Quit()
		}
		b.mutex.Unlock()
	}

	return err
}

func (b *Bot) Quit(message string) {
	b.mutex.Lock()
	defer b.mutex.Unlock()

	// Store current nick as previous nick for potential recovery
	if b.Connection != nil && b.Connection.GetNick() != "" {
		b.PreviousNick = b.Connection.GetNick()
		util.Debug("Stored previous nick %s for potential recovery", b.PreviousNick)
	}

	// Stop channel checker if running
	if b.channelCheckTicker != nil {
		b.channelCheckTicker.Stop()
		b.channelCheckTicker = nil
	}

	// Clear joined channels map
	b.joinedChannels = make(map[string]bool)

	if b.Connection != nil {
		b.Connection.QuitMessage = message
		b.Connection.Quit()
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
}
func (b *Bot) addCallbacks() {
	util.Debug("Setting up IRC callbacks for bot")

	// Add CAP negotiation callbacks
	b.Connection.AddCallback("CAP", func(e *irc.Event) {
		util.Debug("Received CAP event: %s", e.Raw)

		// If this is a CAP LS response, send CAP END to complete registration
		if len(e.Arguments) >= 2 && e.Arguments[1] == "LS" {
			util.Debug("Received CAP LS response, sending CAP END to complete registration")
			b.Connection.SendRaw("CAP END")
		}
	})

	// Callback for server NOTICE messages during connection
	b.Connection.AddCallback("NOTICE", func(e *irc.Event) {
		util.Debug("Received NOTICE during connection: %s", e.Raw)
	})

	// Callback for server numeric 020 (wait message)
	b.Connection.AddCallback("020", func(e *irc.Event) {
		util.Debug("Received 020 wait message: %s", e.Raw)
	})

	// Callback for successful connection
	b.Connection.AddCallback("001", func(e *irc.Event) {
		util.Debug("Received 001 (RPL_WELCOME) event: %s", e.Raw)

		if b.IsConnected() {
			util.Debug("Bot is already marked as connected, ignoring 001 event")
			return
		}

		util.Debug("Marking bot as connected")
		// First check if the connection is fully connected in the library
		if !b.Connection.IsFullyConnected() {
			util.Debug("Connection is not fully connected yet in the library")
			// We can't set it directly as the method is not exposed
			// We'll rely on our own isConnected flag instead
		}

		// Now mark our bot as connected
		b.markAsConnected()
		b.ServerName = e.Source
		b.lastConnectTime = time.Now()

		// Verify the connection state
		util.Debug("Connection state after marking as connected: isConnected=%v, Connection.IsFullyConnected=%v",
			b.isConnected.Load(), b.Connection.IsFullyConnected())

		// Set a reasonable ping frequency now that we're connected
		b.Connection.PingFreq = 3 * time.Minute
		util.Debug("Set PING frequency to %v now that we're connected", b.Connection.PingFreq)

		util.Info("Bot fully connected to %s as %s", b.ServerName, b.Connection.GetNick())

		// Reset joined channels map
		b.mutex.Lock()
		b.joinedChannels = make(map[string]bool)
		b.mutex.Unlock()

		// Join channels
		util.Debug("Joining %d configured channels", len(b.channels))
		for i, channel := range b.channels {
			util.Debug("Joining channel %d/%d: %s", i+1, len(b.channels), channel)
			b.JoinChannel(channel)
			// Add a small delay between joins to avoid flooding
			time.Sleep(500 * time.Millisecond)
		}

		// Start channel checker
		util.Debug("Starting channel checker")
		b.startChannelChecker()

		// Signal that connection has been established
		util.Debug("Signaling connection completion")
		select {
		case <-b.connected:
			util.Debug("Connected channel already closed, ignoring")
		default:
			util.Debug("Closing connected channel to signal successful connection")
			close(b.connected)
		}
	})

	// Callback for nick changes
	b.Connection.AddCallback("NICK", func(e *irc.Event) {
		oldNick := e.Nick
		newNick := e.Message()
		util.Debug("NICK event: %s -> %s", oldNick, newNick)

		// Check if this is our own nick change
		if e.Nick == b.Connection.GetNick() || (b.PreviousNick != "" && e.Nick == b.PreviousNick) {
			// Update PreviousNick for potential recovery
			b.PreviousNick = newNick
			util.Info("Bot nick changed from %s to %s", oldNick, newNick)

			// Check for single-letter nick changes
			wasOneLetter := len(oldNick) == 1
			isOneLetter := len(newNick) == 1

			// Bot got a single letter nick
			if !wasOneLetter && isOneLetter {
				util.Debug("Bot got single letter nick, joining #literki")
				b.JoinChannel("#literki")
			}

			// Bot lost a single letter nick
			if wasOneLetter && !isOneLetter {
				util.Debug("Bot lost single letter nick, leaving #literki")
				b.PartChannel("#literki")
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
	})

	// Callback for unavailable resource (437)
	b.Connection.AddCallback("437", func(e *irc.Event) {
		if !b.IsConnected() {
			// Jeśli bot nie jest w pełni połączony, pozwól bibliotece go-ircevo obsłużyć to standardowo
			return
		}

		if len(e.Arguments) > 1 {
			unavailableNick := e.Arguments[1]
			util.Warning("Nick %s temporarily unavailable on %s", unavailableNick, b.ServerName)
		}
	})

	// BNC + DCC
	b.Connection.AddCallback("CTCP", b.handleCTCP)
	b.Connection.AddCallback("*", func(e *irc.Event) {
		// Log all events without the "DCC:" prefix
		if b.dccTunnel != nil {
			b.dccTunnel.WriteToConn(e.Raw)
		}
		rawMessage := e.Raw
		b.ForwardToTunnel(rawMessage)
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
		util.Debug("Received JOIN event: %s", e.Raw)

		// Check if this is our own join
		if e.Nick == b.Connection.GetNick() {
			channel := e.Arguments[0]
			util.Info("Bot %s has joined channel %s", b.GetCurrentNick(), channel)

			// Update our channel tracking
			b.mutex.Lock()
			b.joinedChannels[channel] = true
			b.mutex.Unlock()

			util.Debug("Updated joinedChannels map, now in %d channels", len(b.joinedChannels))
		} else {
			util.Debug("User %s joined channel %s", e.Nick, e.Arguments[0])
		}
	})

	// Callback for PART events
	b.Connection.AddCallback("PART", func(e *irc.Event) {
		util.Debug("Received PART event: %s", e.Raw)

		// Check if this is our own part
		if e.Nick == b.Connection.GetNick() {
			channel := e.Arguments[0]
			util.Info("Bot %s has left channel %s", b.GetCurrentNick(), channel)

			// Update our channel tracking
			b.mutex.Lock()
			delete(b.joinedChannels, channel)
			util.Debug("Updated joinedChannels map, now in %d channels", len(b.joinedChannels))
			b.mutex.Unlock()
		} else {
			util.Debug("User %s left channel %s", e.Nick, e.Arguments[0])
		}
	})

	// Callback for KICK events
	b.Connection.AddCallback("KICK", func(e *irc.Event) {
		util.Debug("Received KICK event: %s", e.Raw)

		// Check if we were kicked
		if len(e.Arguments) >= 2 && e.Arguments[1] == b.Connection.GetNick() {
			channel := e.Arguments[0]
			reason := ""
			if len(e.Arguments) >= 3 {
				reason = e.Arguments[2]
			}
			util.Warning("Bot %s was kicked from channel %s by %s: %s",
				b.GetCurrentNick(), channel, e.Nick, reason)

			// Update our channel tracking
			b.mutex.Lock()
			delete(b.joinedChannels, channel)
			util.Debug("Updated joinedChannels map after kick, now in %d channels", len(b.joinedChannels))
			b.mutex.Unlock()

			// Try to rejoin after a delay if it's in our channel list
			b.mutex.Lock()
			channelsList := make([]string, len(b.channels))
			copy(channelsList, b.channels)
			b.mutex.Unlock()

			if slices.Contains(channelsList, channel) {
				util.Debug("Channel %s is in configured channels list, will attempt to rejoin after delay", channel)
				go func(channel string) {
					util.Debug("Waiting 30 seconds before rejoining %s", channel)
					time.Sleep(30 * time.Second)
					if b.IsConnected() {
						util.Info("Bot %s attempting to rejoin channel %s after kick",
							b.GetCurrentNick(), channel)
						b.JoinChannel(channel)
					} else {
						util.Warning("Cannot rejoin %s after kick: bot is not connected", channel)
					}
				}(channel)
			} else {
				util.Debug("Channel %s is not in configured channels list, not rejoining", channel)
			}
		} else {
			// Someone else was kicked
			util.Debug("User %s was kicked from %s by %s", e.Arguments[1], e.Arguments[0], e.Nick)
		}
	})

	// Callback dla ERR_YOUREBANNEDCREEP (465)
	b.Connection.AddCallback("465", func(e *irc.Event) {
		reason := e.Message()
		util.Warning("Bot %s banned from server %s: %s", b.GetCurrentNick(), b.ServerName, reason)

		// Zamykamy połączenie
		b.Quit("Banned from server")

		// Usuwamy bota z managera
		if b.botManager != nil {
			b.botManager.RemoveBotFromManager(b)
		}

		// Czyścimy referencje
		b.mutex.Lock()
		b.Connection = nil
		b.botManager = nil
		b.mutex.Unlock()
	})

	// Callback dla ERR_YOUWILLBEBANNED (466)
	b.Connection.AddCallback("466", func(e *irc.Event) {
		util.Warning("Bot %s will be banned from server %s", b.GetCurrentNick(), b.ServerName)

		// Zamykamy połączenie
		b.Quit("Pre-emptive disconnect due to incoming ban")

		// Usuwamy bota z managera
		if b.botManager != nil {
			b.botManager.RemoveBotFromManager(b)
		}

		// Czyścimy referencje
		b.mutex.Lock()
		b.Connection = nil
		b.botManager = nil
		b.mutex.Unlock()
	})

	// Callback for connection errors
	b.Connection.AddCallback("ERROR", func(e *irc.Event) {
		util.Error("Bot %s received ERROR from server: %s", b.GetCurrentNick(), e.Raw)

		// Check for specific error types
		errorMsg := e.Raw
		if strings.Contains(errorMsg, "Closing Link") {
			if strings.Contains(errorMsg, "banned") || strings.Contains(errorMsg, "K-Lined") {
				util.Warning("Bot appears to be banned from server: %s", errorMsg)

				// Remove bot from manager if banned
				if b.botManager != nil {
					b.botManager.RemoveBotFromManager(b)
				}
			} else if strings.Contains(errorMsg, "Excess Flood") {
				util.Warning("Bot disconnected due to excess flood: %s", errorMsg)
			} else if strings.Contains(errorMsg, "Ping timeout") {
				util.Warning("Bot disconnected due to ping timeout: %s", errorMsg)
			}
		}
	})

	// Callback for disconnection
	b.Connection.AddCallback("DISCONNECTED", func(e *irc.Event) {
		util.Warning("Bot %s disconnected from server %s", b.GetCurrentNick(), b.ServerName)

		// Store current nick for potential recovery
		b.mutex.Lock()
		if b.Connection != nil && b.Connection.GetNick() != "" {
			b.PreviousNick = b.Connection.GetNick()
			util.Debug("Stored previous nick %s for potential recovery during disconnect", b.PreviousNick)
		}
		b.mutex.Unlock()

		wasConnected := b.isConnected.Swap(false)
		b.markAsDisconnected()

		if wasConnected {
			// Check if this is part of a mass disconnect (reconnection storm prevention)
			if bm := b.GetBotManager(); bm != nil {
				if bm.DetectMassDisconnect() {
					util.Warning("Bot %s is part of a mass disconnect event, using staggered reconnection", b.GetCurrentNick())
					bm.HandleNetworkOutage()
					return
				}
			}
			// Normal single bot reconnection
			go b.handleReconnect()
		} else {
			util.Info("Bot %s was already disconnected from %s", b.GetCurrentNick(), b.ServerName)
		}
	})
}

// RemoveBot implementuje interfejs types.Bot
func (b *Bot) RemoveBot() {
	// Zamykamy połączenie
	b.Quit("Bot removed from system")

	// Usuwamy bota z managera
	if b.botManager != nil {
		b.botManager.RemoveBotFromManager(b)
	}

	// Czyścimy referencje
	b.mutex.Lock()
	b.Connection = nil
	b.botManager = nil
	b.mutex.Unlock()

	util.Info("Bot %s has been removed from the system", b.GetCurrentNick())
}

func (b *Bot) GetServerName() string {
	b.mutex.Lock()
	defer b.mutex.Unlock()
	return b.ServerName
}

func (b *Bot) handleISONResponse(e *irc.Event) {
	isonResponse := strings.Fields(e.Message())
	util.Debug("Bot %s received ISON response: %v", b.GetCurrentNick(), isonResponse)

	// Nieblokujące wysyłanie do kanału z timeoutem
	select {
	case b.isonResponse <- isonResponse:
		// Wysłano pomyślnie
	case <-time.After(100 * time.Millisecond):
		// Timeout - kanał jest pełny lub nikt nie czyta
		util.Warning("Bot %s isonResponse channel is full or no one is reading", b.GetCurrentNick())
		// Opróżniamy kanał, jeśli jest pełny
		select {
		case <-b.isonResponse: // Próbujemy opróżnić kanał
		default: // Kanał jest już pusty
		}
		// Próbujemy ponownie wysłać
		select {
		case b.isonResponse <- isonResponse:
		default:
			util.Error("Bot %s still cannot send ISON response", b.GetCurrentNick())
		}
	}
}

func (b *Bot) handleInvite(e *irc.Event) {
	inviter := e.Nick
	channel := e.Arguments[1]

	if auth.IsOwner(e, b.owners) {
		util.Info("Bot %s received INVITE to %s from owner %s", b.GetCurrentNick(), channel, inviter)
		b.JoinChannel(channel)
	} else {
		util.Debug("Bot %s ignored INVITE to %s from non-owner %s", b.GetCurrentNick(), channel, inviter)
	}
}

func (b *Bot) RequestISON(nicks []string) ([]string, error) {
	// Sprawdzamy czy bot jest połączony
	if !b.IsConnected() {
		return nil, fmt.Errorf("bot %s is not connected", b.GetCurrentNick())
	}

	// Opróżniamy kanał przed wysłaniem nowego zapytania
	select {
	case <-b.isonResponse: // Próbujemy opróżnić kanał
		util.Debug("Bot %s cleared old ISON response from channel", b.GetCurrentNick())
	default: // Kanał jest już pusty
	}

	// Wysyłamy zapytanie ISON
	command := fmt.Sprintf("ISON %s", strings.Join(nicks, " "))
	util.Debug("Bot %s is sending ISON command: %s", b.GetCurrentNick(), command)
	b.Connection.SendRaw(command)

	// Czekamy na odpowiedź z timeoutem
	select {
	case response := <-b.isonResponse:
		return response, nil
	case <-time.After(5 * time.Second): // Zmniejszamy timeout z 10 do 5 sekund
		util.Warning("Bot %s did not receive ISON response in time", b.GetCurrentNick())
		return []string{}, fmt.Errorf("bot %s did not receive ISON response in time", b.GetCurrentNick())
	}
}

func (b *Bot) ChangeNick(newNick string) {
	if b.IsConnected() {
		oldNick := b.GetCurrentNick()
		util.Info("Bot %s is attempting to change nick to %s", oldNick, newNick)

		// Set up a channel to receive the result of the nick change
		nickChangeDone := make(chan bool, 1)

		// Add a temporary callback for NICK events
		callbackID := b.Connection.AddCallback("NICK", func(e *irc.Event) {
			if e.Nick == oldNick && e.Message() == newNick {
				// Nick change was successful
				nickChangeDone <- true
			}
		})

		// Add a temporary callback for nick-related errors
		errorCallbackID := b.Connection.AddCallback("433", func(e *irc.Event) {
			// Nick already in use
			if len(e.Arguments) > 1 && e.Arguments[1] == newNick {
				nickChangeDone <- false
			}
		})

		// Clean up callbacks when we're done
		defer func() {
			b.Connection.RemoveCallback("NICK", callbackID)
			b.Connection.RemoveCallback("433", errorCallbackID)
		}()

		// Send the nick change command
		b.Connection.Nick(newNick)

		// Wait for the result with a timeout
		select {
		case success := <-nickChangeDone:
			if success {
				util.Info("Bot successfully changed nick from %s to %s", oldNick, newNick)
			} else {
				util.Warning("Failed to change nick for bot from %s to %s", oldNick, newNick)
			}
		case <-time.After(5 * time.Second):
			// Timeout - check the current nick status
			status := b.Connection.GetNickStatus()
			if status.Current == newNick {
				util.Info("Bot successfully changed nick from %s to %s", oldNick, newNick)
			} else {
				util.Warning("Nick change timed out. Current nick: %s, Error: %s",
					status.Current, status.Error)
			}
		}
	} else {
		util.Debug("Bot %s is not connected; cannot change nick", b.GetCurrentNick())
	}
}

func (b *Bot) JoinChannel(channel string) {
	if b.IsConnected() {
		util.Info("Bot %s is joining channel %s", b.GetCurrentNick(), channel)

		// Check if we're already in this channel
		b.mutex.Lock()
		alreadyJoined := b.joinedChannels[channel]
		b.mutex.Unlock()

		if alreadyJoined {
			util.Debug("Bot %s is already in channel %s, not sending JOIN command", b.GetCurrentNick(), channel)
			return
		}

		// Send the JOIN command
		util.Debug("Sending JOIN command for channel %s", channel)
		b.Connection.Join(channel)
	} else {
		util.Warning("Bot %s is not connected; cannot join channel %s", b.GetCurrentNick(), channel)
	}
}

func (b *Bot) PartChannel(channel string) {
	if b.IsConnected() {
		util.Info("Bot %s is leaving channel %s", b.GetCurrentNick(), channel)

		// Check if we're actually in this channel
		b.mutex.Lock()
		inChannel := b.joinedChannels[channel]
		b.mutex.Unlock()

		if !inChannel {
			util.Debug("Bot %s is not in channel %s, not sending PART command", b.GetCurrentNick(), channel)
			return
		}

		// Send the PART command
		util.Debug("Sending PART command for channel %s", channel)
		b.Connection.Part(channel)

		// Update joined channels map
		b.mutex.Lock()
		delete(b.joinedChannels, channel)
		util.Debug("Removed %s from joinedChannels map, now in %d channels", channel, len(b.joinedChannels))
		b.mutex.Unlock()
	} else {
		util.Warning("Bot %s is not connected; cannot part channel %s", b.GetCurrentNick(), channel)
	}
}

// isChannelJoined checks if a channel is marked as joined
func (b *Bot) isChannelJoined(channel string) bool {
	b.mutex.Lock()
	defer b.mutex.Unlock()
	return b.joinedChannels[channel]
}

// startChannelChecker starts a periodic check to ensure the bot is in all required channels
func (b *Bot) startChannelChecker() {
	if b.channelCheckTicker != nil {
		b.channelCheckTicker.Stop()
	}

	// Check channels every 5 minutes
	b.channelCheckTicker = time.NewTicker(5 * time.Minute)

	go func() {
		for {
			select {
			case <-b.channelCheckTicker.C:
				if !b.IsConnected() {
					continue
				}

				b.checkAndRejoinChannels()

			case <-b.connected:
				// Channel closed, bot disconnected
				return
			}
		}
	}()
}

// checkAndRejoinChannels checks if the bot is in all required channels and rejoins if necessary
func (b *Bot) checkAndRejoinChannels() {
	util.Debug("Running channel check for bot %s", b.GetCurrentNick())

	if !b.IsConnected() {
		util.Debug("Bot is not connected, skipping channel check")
		return
	}

	// Get a copy of the configured channels
	b.mutex.Lock()
	channels := make([]string, len(b.channels))
	copy(channels, b.channels)

	// Get a copy of the joined channels for logging
	joinedChannelsCopy := make(map[string]bool)
	for ch, joined := range b.joinedChannels {
		joinedChannelsCopy[ch] = joined
	}
	b.mutex.Unlock()

	util.Debug("Bot %s should be in %d channels, currently in %d channels",
		b.GetCurrentNick(), len(channels), len(joinedChannelsCopy))

	// Log current channel state
	var joinedList []string
	for ch := range joinedChannelsCopy {
		joinedList = append(joinedList, ch)
	}
	util.Debug("Currently joined channels: %v", joinedList)
	util.Debug("Configured channels: %v", channels)

	// Check each configured channel
	for _, channel := range channels {
		if !b.isChannelJoined(channel) {
			util.Info("Bot %s is not in channel %s, rejoining", b.GetCurrentNick(), channel)
			b.JoinChannel(channel)
			// Add a small delay between joins to avoid flooding
			time.Sleep(500 * time.Millisecond)
		} else {
			util.Debug("Bot %s is already in channel %s", b.GetCurrentNick(), channel)
		}
	}

	util.Debug("Channel check completed for bot %s", b.GetCurrentNick())
}

func (b *Bot) Reconnect() {
	if b.IsConnected() {
		oldNick := b.GetCurrentNick()

		// Store the current nick as previous nick for potential recovery
		b.PreviousNick = oldNick
		util.Debug("Stored previous nick %s for potential recovery", b.PreviousNick)

		b.Quit("Reconnecting")

		time.Sleep(5 * time.Second)

		// First try to reconnect with the same nick
		util.Info("Attempting to reconnect with the same nick: %s", oldNick)
		err := b.connectWithNewNick(oldNick)
		if err == nil {
			util.Info("Bot successfully reconnected with the same nick: %s", oldNick)
			return
		}
		util.Warning("Failed to reconnect with the same nick %s: %v", oldNick, err)

		// If that fails, try with a new random nick
		newNick, err := util.GenerateRandomNick(b.GlobalConfig.NickAPI.URL, b.GlobalConfig.NickAPI.MaxWordLength, b.GlobalConfig.NickAPI.Timeout)
		if err != nil {
			newNick = util.GenerateFallbackNick()
		}

		util.Info("Attempting to reconnect with new nick: %s", newNick)

		err = b.connectWithNewNick(newNick)
		if err != nil {
			util.Error("Failed to reconnect bot %s (new nick: %s): %v", oldNick, newNick, err)

			// If that also fails, try with a shuffled version of the original nick
			shuffledNick := shuffleNick(oldNick)
			util.Info("Attempting to reconnect with shuffled nick: %s", shuffledNick)

			err = b.connectWithNewNick(shuffledNick)
			if err != nil {
				util.Error("Failed to reconnect bot with shuffled nick %s: %v", shuffledNick, err)
			} else {
				util.Info("Bot successfully reconnected with shuffled nick: %s", shuffledNick)
			}
		} else {
			util.Info("Bot %s successfully reconnected with new nick: %s", oldNick, newNick)
		}
	} else {
		util.Debug("Bot %s is not connected; cannot reconnect", b.GetCurrentNick())
	}
}

func (b *Bot) connectWithNewNick(nick string) error {
	// Create a new connection with the specified nick
	b.Connection = irc.IRC(nick, b.Username)
	b.Connection.SetLocalIP(b.Config.Vhost)
	b.Connection.VerboseCallbackHandler = false
	b.Connection.Debug = false
	b.Connection.UseTLS = b.Config.SSL
	b.Connection.RealName = b.Realname

	// Connect using the new connection
	err := b.Connect()
	if err == nil {
		// Update PreviousNick for potential future recovery
		b.PreviousNick = nick
	}
	return err
}

func shuffleNick(nick string) string {
	runes := []rune(nick)
	rand.Shuffle(len(runes), func(i, j int) {
		runes[i], runes[j] = runes[j], runes[i]
	})

	if unicode.IsDigit(runes[0]) {
		return "a_" + string(runes)
	}

	return string(runes)
}

func (b *Bot) handleReconnect() {
	b.isReconnecting = true
	defer func() {
		b.isReconnecting = false
	}()

	maxRetries := b.GlobalConfig.ReconnectRetries
	baseRetryInterval := time.Duration(b.GlobalConfig.ReconnectInterval) * time.Second

	// First, try to reconnect with the previous nick if available
	if b.PreviousNick != "" {
		util.Info("Attempting to reconnect with previous nick: %s", b.PreviousNick)

		// Try with previous nick
		err := b.connectWithNewNick(b.PreviousNick)
		if err == nil {
			util.Info("Successfully reconnected with previous nick: %s", b.GetCurrentNick())

			// Ensure we rejoin all channels
			time.Sleep(5 * time.Second) // Give the server a moment
			b.checkAndRejoinChannels()
			return
		}

		util.Warning("Failed to reconnect with previous nick %s: %v", b.PreviousNick, err)
	}

	// If reconnecting with previous nick failed or wasn't possible, try with current nick or new nicks
	for attempts := range maxRetries {
		// Exponential backoff with jitter for reconnection
		retryInterval := baseRetryInterval * time.Duration(1<<uint(attempts))
		// Add jitter (±20%)
		jitter := float64(retryInterval) * (0.8 + 0.4*rand.Float64())
		retryInterval = time.Duration(jitter)

		// Cap the retry interval at 5 minutes
		retryInterval = min(retryInterval, 5*time.Minute)

		// For the first attempt, try with current nick
		// For subsequent attempts, try with new random nicks
		if attempts > 0 {
			newNick, err := util.GenerateRandomNick(b.GlobalConfig.NickAPI.URL, b.GlobalConfig.NickAPI.MaxWordLength, b.GlobalConfig.NickAPI.Timeout)
			if err != nil {
				newNick = util.GenerateFallbackNick()
			}

			// Use connectWithNewNick instead of setting CurrentNick directly
			err = b.connectWithNewNick(newNick)
			if err == nil {
				util.Info("Bot %s reconnected with new nick", b.GetCurrentNick())

				// Ensure we rejoin all channels
				time.Sleep(5 * time.Second) // Give the server a moment
				b.checkAndRejoinChannels()
				return
			}
			util.Error("Failed to connect with new nick: %v", err)
			time.Sleep(retryInterval)
			continue
		}

		util.Info("Bot is attempting to reconnect with previous settings (attempt %d/%d, retry interval: %v)",
			attempts+1, maxRetries, retryInterval)

		err := b.connectWithRetry()
		if err == nil {
			util.Info("Bot %s reconnected", b.GetCurrentNick())

			// Ensure we rejoin all channels
			time.Sleep(5 * time.Second) // Give the server a moment
			b.checkAndRejoinChannels()
			return
		}
		util.Error("Attempt %d failed: %v", attempts+1, err)
		time.Sleep(retryInterval)
	}

	util.Error("Bot could not reconnect after %d attempts", maxRetries)
	b.gaveUp = true
}

func (b *Bot) SendMessage(target, message string) {
	if b.IsConnected() {
		util.Debug("Bot %s is sending message to %s: %s", b.GetCurrentNick(), target, message)
		b.Connection.Privmsg(target, message)
	} else {
		util.Debug("Bot %s is not connected; cannot send message to %s", b.GetCurrentNick(), target)
	}
}

func (b *Bot) AttemptNickChange(nick string) {
	currentNick := b.GetCurrentNick()
	util.Debug("Bot %s received request to change nick to %s", currentNick, nick)
	if currentNick != nick {
		util.Info("Bot %s is attempting to change nick to %s", currentNick, nick)
		b.ChangeNick(nick)
	} else {
		util.Debug("Bot %s decided not to change nick to %s", currentNick, nick)
	}
}

func (b *Bot) handlePrivMsg(e *irc.Event) {
	util.Debug("Received PRIVMSG: target=%s, sender=%s, message=%s", e.Arguments[0], e.Nick, e.Message())
	b.HandleCommands(e)
}

func (b *Bot) SetOwnerList(owners auth.OwnerList) {
	b.mutex.Lock()
	defer b.mutex.Unlock()
	b.owners = owners
	util.Debug("Bot %s set owners: %v", b.GetCurrentNick(), owners)
}

func (b *Bot) SetChannels(channels []string) {
	b.mutex.Lock()
	defer b.mutex.Unlock()
	b.channels = channels
	util.Debug("Bot %s set channels: %v", b.GetCurrentNick(), channels)
}

func (b *Bot) GetCurrentNick() string {
	// First check if Connection is nil to avoid panic
	if b.Connection != nil {
		// Safely get nick with recovery for nil pointer cases
		defer func() {
			if r := recover(); r != nil {
				util.Error("Recovered from panic in GetCurrentNick: %v", r)
			}
		}()

		// Try to get nick, but handle potential nil pointer
		try := func() (nick string, ok bool) {
			defer func() {
				if r := recover(); r != nil {
					ok = false
				}
			}()
			return b.Connection.GetNick(), true
		}

		if nick, ok := try(); ok && nick != "" {
			return nick
		}
	}

	// If connection is nil or GetNick() failed, return the previous nick or a placeholder
	if b.PreviousNick != "" {
		return b.PreviousNick
	}
	return "disconnected-bot"
}

func (b *Bot) SendRaw(message string) {
	if b.IsConnected() {
		b.Connection.SendRaw(message)
	}
}

func (b *Bot) ForwardToTunnel(data string) {
	// This function is kept for future use but currently does nothing
	// since BNC has been removed
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
	util.Debug("CTCP Event | Nick: %s | Args: %v | Message: %s", e.Nick, e.Arguments, e.Message())

	ctcpMessage := e.Message()
	if strings.HasPrefix(ctcpMessage, "DCC ") {
		b.handleDCCRequest(e)
	}
}
