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
	"github.com/kofany/gNb/internal/bnc"
	"github.com/kofany/gNb/internal/config"
	"github.com/kofany/gNb/internal/dcc"
	"github.com/kofany/gNb/internal/nickmanager"
	"github.com/kofany/gNb/internal/types"
	"github.com/kofany/gNb/internal/util"
	irc "github.com/kofany/go-ircevo"
)

// Bot represents a single IRC bot
type Bot struct {
	Config          *config.BotConfig
	GlobalConfig    *config.GlobalConfig
	Connection      *irc.Connection
	CurrentNick     string
	PreviousNick    string // Store the previous nick for recovery during reconnection
	Username        string
	Realname        string
	isConnected     atomic.Bool
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
	isonResponse       chan []string            // Legacy field, kept for compatibility
	isonRequests       map[string]chan []string // Map of request IDs to response channels
	isonRequestsMutex  sync.Mutex               // Mutex to protect access to the isonRequests map
	ServerName         string                   // Nazwa serwera otrzymana po połączeniu
	bncServer          *bnc.BNCServer
	mutex              sync.Mutex
	dccTunnel          *dcc.DCCTunnel
	channelCheckTicker *time.Ticker // Ticker for periodic channel check
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
		isonResponse:   make(chan []string, 10), // Legacy field, kept for compatibility
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

		b.Connection = irc.IRC(b.CurrentNick, b.Username)
		b.Connection.SetLocalIP(b.Config.Vhost)
		b.Connection.VerboseCallbackHandler = false
		b.Connection.Debug = false
		b.Connection.UseTLS = b.Config.SSL
		b.Connection.RealName = b.Realname
		// Increase timeout for better stability
		b.Connection.Timeout = 2 * time.Minute
		b.Connection.KeepAlive = 5 * time.Minute
		// Set HandleErrorAsDisconnect to false to allow the library to handle reconnections
		b.Connection.HandleErrorAsDisconnect = false

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
	// Get the current nick before locking the mutex
	currentNick := b.GetCurrentNick()

	b.mutex.Lock()
	defer b.mutex.Unlock()

	// Store current nick as previous nick for potential recovery
	if currentNick != "" {
		b.PreviousNick = currentNick
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
	// Callback for successful connection
	b.Connection.AddCallback("001", func(e *irc.Event) {
		if b.IsConnected() {
			// Bot jest już połączony, nie rób nic
			return
		}

		b.markAsConnected()
		b.ServerName = e.Source
		b.lastConnectTime = time.Now()

		// Synchronize internal state with the connection
		connectionNick := b.Connection.GetNick()
		b.mutex.Lock()
		if b.CurrentNick != connectionNick {
			util.Debug("Synchronizing internal nick state: %s -> %s", b.CurrentNick, connectionNick)
			b.CurrentNick = connectionNick
		}
		b.mutex.Unlock()

		util.Info("Bot %s fully connected to %s as %s", b.GetCurrentNick(), b.ServerName, b.GetCurrentNick())

		// Reset joined channels map
		b.mutex.Lock()
		b.joinedChannels = make(map[string]bool)
		b.mutex.Unlock()

		// Join channels
		for _, channel := range b.channels {
			b.JoinChannel(channel)
		}

		// Start channel checker
		b.startChannelChecker()

		// Signal that connection has been established
		select {
		case <-b.connected:
			// Kanał już zamknięty, ignorujemy
		default:
			close(b.connected)
		}
	})

	// Callback for nick changes
	b.Connection.AddCallback("NICK", func(e *irc.Event) {
		// Get the current nick from the connection
		oldNick := b.GetCurrentNick()

		// Check if this nick change event is for our bot
		if e.Nick == oldNick {
			newNick := e.Message()

			// Update our internal state to match the connection
			b.mutex.Lock()
			b.CurrentNick = newNick
			b.mutex.Unlock()

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
		if e.Nick == b.Connection.GetNick() {
			channel := e.Arguments[0]
			util.Debug("Bot %s has joined channel %s", b.GetCurrentNick(), channel)
			b.mutex.Lock()
			b.joinedChannels[channel] = true
			b.mutex.Unlock()
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

			// Try to rejoin after a delay if it's in our channel list
			b.mutex.Lock()
			channelsList := make([]string, len(b.channels))
			copy(channelsList, b.channels)
			b.mutex.Unlock()

			if slices.Contains(channelsList, channel) {
				go func(channel string) {
					time.Sleep(30 * time.Second)
					if b.IsConnected() {
						util.Info("Bot %s attempting to rejoin channel %s after kick",
							b.GetCurrentNick(), channel)
						b.JoinChannel(channel)
					}
				}(channel)
			}
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
			b.botManager.(*BotManager).RemoveBotFromManager(b)
		}

		// Czyścimy referencje
		b.mutex.Lock()
		b.Connection = nil
		b.botManager = nil
		b.nickManager = nil
		b.mutex.Unlock()
	})

	// Callback dla ERR_YOUWILLBEBANNED (466)
	b.Connection.AddCallback("466", func(e *irc.Event) {
		util.Warning("Bot %s will be banned from server %s", b.GetCurrentNick(), b.ServerName)

		// Zamykamy połączenie
		b.Quit("Pre-emptive disconnect due to incoming ban")

		// Usuwamy bota z managera
		if b.botManager != nil {
			b.botManager.(*BotManager).RemoveBotFromManager(b)
		}

		// Czyścimy referencje
		b.mutex.Lock()
		b.Connection = nil
		b.botManager = nil
		b.nickManager = nil
		b.mutex.Unlock()
	})

	// Callback for disconnection
	b.Connection.AddCallback("DISCONNECTED", func(e *irc.Event) {
		// Get the current nick before any locks
		currentNick := b.GetCurrentNick()
		util.Warning("Bot %s disconnected from server %s", currentNick, b.ServerName)

		// Store current nick for potential recovery
		b.mutex.Lock()
		b.PreviousNick = currentNick
		util.Debug("Stored previous nick %s for potential recovery during disconnect", b.PreviousNick)
		b.mutex.Unlock()

		wasConnected := b.isConnected.Swap(false)
		b.markAsDisconnected()

		if wasConnected {
			go b.handleReconnect()
		} else {
			util.Info("Bot %s was already disconnected from %s", currentNick, b.ServerName)
		}
	})
}

// RemoveBot implementuje interfejs types.Bot
func (b *Bot) RemoveBot() {
	// Get the current nick before closing the connection
	currentNick := b.GetCurrentNick()

	// Zamykamy połączenie
	b.Quit("Bot removed from system")

	// Usuwamy bota z managera
	if b.botManager != nil {
		// Use type assertion outside of the lock to avoid potential deadlocks
		if bm, ok := b.botManager.(*BotManager); ok {
			bm.RemoveBotFromManager(b)
		}
	}

	// Czyścimy referencje
	b.mutex.Lock()
	b.Connection = nil
	b.botManager = nil
	b.nickManager = nil
	b.mutex.Unlock()

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

// Removed unused generateRequestID method

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
	// Check if the bot is connected
	if !b.IsConnected() {
		return nil, fmt.Errorf("bot %s is not connected", b.GetCurrentNick())
	}

	// Pobierz aktualny nick bota (nie oczekiwany)
	currentNick := b.GetCurrentNick()

	// Clean up old requests to prevent memory leaks
	b.cleanupOldRequests()

	// Create a unique ID for this request - używamy aktualnego nicka
	requestID := fmt.Sprintf("%s-%d", currentNick, time.Now().UnixNano())

	// Create a channel for this specific request
	responseChan := make(chan []string, 1)

	// Register this request
	b.isonRequestsMutex.Lock()
	b.isonRequests[requestID] = responseChan
	b.isonRequestsMutex.Unlock()

	// Send the ISON command
	command := fmt.Sprintf("ISON %s", strings.Join(nicks, " "))
	util.Debug("Bot %s is sending ISON command (ID: %s): %s", currentNick, requestID, command)
	b.Connection.SendRaw(command)

	// Wait for the response with a timeout
	var response []string
	var err error

	select {
	case response = <-responseChan:
		util.Debug("Bot %s received ISON response for request %s", currentNick, requestID)
	case <-time.After(5 * time.Second):
		util.Warning("Bot %s did not receive ISON response in time for request %s", currentNick, requestID)
		err = fmt.Errorf("bot %s did not receive ISON response in time", currentNick)
	}

	// Clean up this request
	b.isonRequestsMutex.Lock()
	delete(b.isonRequests, requestID)
	b.isonRequestsMutex.Unlock()

	return response, err
}

func (b *Bot) ChangeNick(newNick string) {
	if b.IsConnected() {
		oldNick := b.GetCurrentNick()
		util.Info("Bot %s is attempting to change nick to %s", oldNick, newNick)
		b.Connection.Nick(newNick)

		time.Sleep(1 * time.Second)

		// Check if the nick change was successful by checking the connection's nick
		if b.Connection.GetNick() == newNick {
			util.Info("Bot successfully changed nick from %s to %s", oldNick, newNick)

			// Update our internal state to match the connection
			b.mutex.Lock()
			b.CurrentNick = newNick
			b.mutex.Unlock()

			// Notify the nick manager about the change
			if b.nickManager != nil {
				b.nickManager.NotifyNickChange(oldNick, newNick)
			} else {
				util.Warning("NickManager is not set for bot %s", oldNick)
			}
		} else {
			util.Warning("Failed to change nick for bot %s from %s to %s", oldNick, oldNick, newNick)

			// Powiadom NickManager o niepowodzeniu zmiany nicka
			if b.nickManager != nil {
				if nm, ok := b.nickManager.(*nickmanager.NickManager); ok {
					nm.NickChangeFailed(oldNick, newNick)
				}
			}
		}
	} else {
		util.Debug("Bot %s is not connected; cannot change nick", b.GetCurrentNick())
	}
}

func (b *Bot) JoinChannel(channel string) {
	if b.IsConnected() {
		util.Debug("Bot %s is joining channel %s", b.GetCurrentNick(), channel)
		b.Connection.Join(channel)
	} else {
		util.Debug("Bot %s is not connected; cannot join channel %s", b.GetCurrentNick(), channel)
	}
}

func (b *Bot) PartChannel(channel string) {
	if b.IsConnected() {
		util.Debug("Bot %s is leaving channel %s", b.GetCurrentNick(), channel)
		b.Connection.Part(channel)

		// Update joined channels map
		b.mutex.Lock()
		delete(b.joinedChannels, channel)
		b.mutex.Unlock()
	} else {
		util.Debug("Bot %s is not connected; cannot part channel %s", b.GetCurrentNick(), channel)
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
	if !b.IsConnected() {
		return
	}

	b.mutex.Lock()
	channels := make([]string, len(b.channels))
	copy(channels, b.channels)
	b.mutex.Unlock()

	for _, channel := range channels {
		if !b.isChannelJoined(channel) {
			util.Info("Bot %s is not in channel %s, rejoining", b.GetCurrentNick(), channel)
			b.JoinChannel(channel)
		}
	}
}

func (b *Bot) Reconnect() {
	if b.IsConnected() {
		oldNick := b.GetCurrentNick()

		// Store the current nick as previous nick for potential recovery
		b.PreviousNick = oldNick
		util.Debug("Stored previous nick %s for potential recovery", b.PreviousNick)

		b.Quit("Reconnecting")

		if b.nickManager != nil {
			b.nickManager.ReturnNickToPool(oldNick)
		}

		time.Sleep(5 * time.Second)

		// First try to reconnect with the same nick
		util.Info("Attempting to reconnect with the same nick: %s", oldNick)
		b.mutex.Lock()
		b.CurrentNick = oldNick
		b.mutex.Unlock()

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

		b.mutex.Lock()
		b.CurrentNick = newNick
		b.mutex.Unlock()

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
				if b.nickManager != nil {
					b.nickManager.NotifyNickChange(oldNick, shuffledNick)
				}
			}
		} else {
			util.Info("Bot %s successfully reconnected with new nick: %s", oldNick, newNick)
			if b.nickManager != nil {
				b.nickManager.NotifyNickChange(oldNick, newNick)
			}
		}
	} else {
		util.Debug("Bot %s is not connected; cannot reconnect", b.GetCurrentNick())
	}
}

func (b *Bot) connectWithNewNick(nick string) error {
	b.Connection = irc.IRC(nick, b.Username)
	b.Connection.SetLocalIP(b.Config.Vhost)
	b.Connection.VerboseCallbackHandler = false
	b.Connection.Debug = false
	b.Connection.UseTLS = b.Config.SSL
	b.Connection.RealName = b.Realname

	return b.Connect()
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
	if b.PreviousNick != "" && b.PreviousNick != b.CurrentNick {
		util.Info("Attempting to reconnect with previous nick: %s", b.PreviousNick)

		// Save current nick temporarily
		currentNick := b.CurrentNick

		// Try with previous nick
		b.CurrentNick = b.PreviousNick
		err := b.connectWithRetry()
		if err == nil {
			util.Info("Successfully reconnected with previous nick: %s", b.GetCurrentNick())

			// Ensure we rejoin all channels
			time.Sleep(5 * time.Second) // Give the server a moment
			b.checkAndRejoinChannels()
			return
		}

		util.Warning("Failed to reconnect with previous nick %s: %v", b.PreviousNick, err)

		// Restore current nick for further attempts
		b.CurrentNick = currentNick
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
			b.CurrentNick = newNick
			util.Info("Using new random nick for reconnection attempt %d: %s", attempts+1, b.GetCurrentNick())
		}

		util.Info("Bot %s is attempting to reconnect (attempt %d/%d, retry interval: %v)",
			b.GetCurrentNick(), attempts+1, maxRetries, retryInterval)

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

	util.Error("Bot %s could not reconnect after %d attempts", b.GetCurrentNick(), maxRetries)
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
	if b.shouldChangeNick(nick) {
		util.Info("Bot %s is attempting to change nick to %s", currentNick, nick)

		// Aktualizuj oczekiwany nick w NickManager przed próbą zmiany
		if nm, ok := b.nickManager.(*nickmanager.NickManager); ok {
			nm.UpdateExpectedNick(b, nick)
		}

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

func (b *Bot) GetCurrentNick() string {
	if b.Connection != nil {
		return b.Connection.GetNick()
	}
	// Fallback do lokalnego stanu tylko jeśli nie ma połączenia
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
	util.Debug("CTCP Event | Nick: %s | Args: %v | Message: %s", e.Nick, e.Arguments, e.Message())

	ctcpMessage := e.Message()
	if strings.HasPrefix(ctcpMessage, "DCC ") {
		b.handleDCCRequest(e)
	}
}
