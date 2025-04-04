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
	Config             *config.BotConfig
	GlobalConfig       *config.GlobalConfig
	Connection         *irc.Connection
	CurrentNick        string
	PreviousNick       string // Store the previous nick for recovery during reconnection
	Username           string
	Realname           string
	isConnected        atomic.Bool
	owners             auth.OwnerList
	channels           []string
	joinedChannels     map[string]bool // Track which channels the bot has successfully joined
	nickManager        types.NickManager
	isReconnecting     bool
	lastConnectTime    time.Time
	connected          chan struct{}
	botManager         types.BotManager
	gaveUp             bool
	isonResponse       chan []string
	ServerName         string // Nazwa serwera otrzymana po połączeniu
	bncServer          *bnc.BNCServer
	mutex              sync.Mutex
	dccTunnel          *dcc.DCCTunnel            // Legacy field - kept for backward compatibility
	dccTunnels         map[string]*dcc.DCCTunnel // Map of owner nick -> tunnel
	dccTunnelsMutex    sync.Mutex
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
		isonResponse:   make(chan []string, 1),
		joinedChannels: make(map[string]bool),
		dccTunnels:     make(map[string]*dcc.DCCTunnel),
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

	// Sprawdzamy czy bot jest w pełni połączony i czy upłynęło mniej niż 240 sekund od startu
	if bm := b.GetBotManager(); bm != nil {
		manager := bm.(*BotManager)
		if time.Since(manager.startTime) > 240*time.Second && !b.Connection.IsFullyConnected() {
			return false
		}
	}

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
			b.CurrentNick, b.Config.ServerAddress(), attempts+1, maxRetries, retryInterval)

		if err := b.Connection.Connect(b.Config.ServerAddress()); err != nil {
			lastError = err
			util.Error("Attempt %d: Failed to connect bot %s: %v",
				attempts+1, b.CurrentNick, err)
			b.markAsDisconnected()
			time.Sleep(retryInterval)
			continue
		}

		go b.Connection.Loop()

		select {
		case <-b.connected:
			util.Info("Bot %s successfully connected", b.CurrentNick)
			return nil
		case <-time.After(45 * time.Second): // Increased timeout for connection establishment
			if b.IsConnected() {
				util.Info("Bot %s is fully connected, proceeding", b.CurrentNick)
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
	b.mutex.Lock()
	defer b.mutex.Unlock()

	// Store current nick as previous nick for potential recovery
	if b.Connection != nil && b.Connection.GetNick() != "" {
		b.PreviousNick = b.Connection.GetNick()
		util.Debug("Stored previous nick %s for potential recovery", b.PreviousNick)
	} else if b.CurrentNick != "" {
		b.PreviousNick = b.CurrentNick
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

		util.Info("Bot %s fully connected to %s as %s", b.CurrentNick, b.ServerName, b.CurrentNick)

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
	// Callback for nick changes
	b.Connection.AddCallback("NICK", func(e *irc.Event) {
		oldNick := b.CurrentNick
		if e.Nick == b.Connection.GetNick() {
			newNick := e.Message()
			if b.Connection.GetNick() == newNick {
				if b.nickManager != nil {
					b.CurrentNick = newNick
					b.nickManager.NotifyNickChange(oldNick, b.CurrentNick)

					// Sprawdź zmianę nicka pod kątem #literki
					wasOneLetter := len(oldNick) == 1
					isOneLetter := len(newNick) == 1

					// Bot zdobył jednoznakowy nick
					if !wasOneLetter && isOneLetter {
						util.Debug("Bot %s got single letter nick, joining #literki", b.CurrentNick)
						b.JoinChannel("#literki")
					}

					// Bot stracił jednoznakowy nick
					if wasOneLetter && !isOneLetter {
						util.Debug("Bot %s lost single letter nick, leaving #literki", b.CurrentNick)
						b.PartChannel("#literki")
					}
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
			util.Debug("Bot %s has joined channel %s", b.CurrentNick, channel)
			b.mutex.Lock()
			b.joinedChannels[channel] = true
			b.mutex.Unlock()
		}
	})

	// Callback for PART events
	b.Connection.AddCallback("PART", func(e *irc.Event) {
		if e.Nick == b.Connection.GetNick() {
			channel := e.Arguments[0]
			util.Debug("Bot %s has left channel %s", b.CurrentNick, channel)
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
				b.CurrentNick, channel, e.Nick, reason)
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
							b.CurrentNick, channel)
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
		util.Warning("Bot %s disconnected from server %s", b.CurrentNick, b.ServerName)

		// Store current nick for potential recovery
		b.mutex.Lock()
		if b.Connection != nil && b.Connection.GetNick() != "" {
			b.PreviousNick = b.Connection.GetNick()
			util.Debug("Stored previous nick %s for potential recovery during disconnect", b.PreviousNick)
		} else if b.CurrentNick != "" {
			b.PreviousNick = b.CurrentNick
			util.Debug("Stored previous nick %s for potential recovery during disconnect", b.PreviousNick)
		}
		b.mutex.Unlock()

		wasConnected := b.isConnected.Swap(false)
		b.markAsDisconnected()

		if wasConnected {
			go b.handleReconnect()
		} else {
			util.Info("Bot %s was already disconnected from %s", b.CurrentNick, b.ServerName)
		}
	})
}

// RemoveBot implementuje interfejs types.Bot
func (b *Bot) RemoveBot() {
	// Zamykamy połączenie
	b.Quit("Bot removed from system")

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

	util.Info("Bot %s has been removed from the system", b.CurrentNick)
}

func (b *Bot) GetServerName() string {
	b.mutex.Lock()
	defer b.mutex.Unlock()
	return b.ServerName
}

func (b *Bot) handleISONResponse(e *irc.Event) {
	isonResponse := strings.Fields(e.Message())
	util.Debug("Bot %s received ISON response: %v", b.CurrentNick, isonResponse)
	select {
	case b.isonResponse <- isonResponse:
	default:
		util.Warning("Bot %s isonResponse channel is full", b.CurrentNick)
	}
}

func (b *Bot) handleInvite(e *irc.Event) {
	inviter := e.Nick
	channel := e.Arguments[1]

	if auth.IsOwner(e, b.owners) {
		util.Info("Bot %s received INVITE to %s from owner %s", b.CurrentNick, channel, inviter)
		b.JoinChannel(channel)
	} else {
		util.Debug("Bot %s ignored INVITE to %s from non-owner %s", b.CurrentNick, channel, inviter)
	}
}

func (b *Bot) RequestISON(nicks []string) ([]string, error) {
	if !b.IsConnected() {
		return nil, fmt.Errorf("bot %s is not connected", b.CurrentNick)
	}

	command := fmt.Sprintf("ISON %s", strings.Join(nicks, " "))
	util.Debug("Bot %s is sending ISON command: %s", b.CurrentNick, command)
	b.Connection.SendRaw(command)

	select {
	case response := <-b.isonResponse:
		return response, nil
	case <-time.After(10 * time.Second):
		return nil, fmt.Errorf("bot %s did not receive ISON response in time", b.CurrentNick)
	}
}

func (b *Bot) ChangeNick(newNick string) {
	if b.IsConnected() {
		oldNick := b.CurrentNick
		util.Info("Bot %s is attempting to change nick to %s", oldNick, newNick)
		b.Connection.Nick(newNick)

		time.Sleep(1 * time.Second)

		if b.Connection.GetNick() == newNick {
			util.Info("Bot successfully changed nick from %s to %s", oldNick, newNick)
			b.CurrentNick = newNick

			if b.nickManager != nil {
				b.nickManager.NotifyNickChange(oldNick, newNick)
			} else {
				util.Warning("NickManager is not set for bot %s", oldNick)
			}
		} else {
			util.Warning("Failed to change nick for bot %s from %s to %s", oldNick, oldNick, newNick)
		}
	} else {
		util.Debug("Bot %s is not connected; cannot change nick", b.CurrentNick)
	}
}

func (b *Bot) JoinChannel(channel string) {
	if b.IsConnected() {
		util.Debug("Bot %s is joining channel %s", b.CurrentNick, channel)
		b.Connection.Join(channel)
	} else {
		util.Debug("Bot %s is not connected; cannot join channel %s", b.CurrentNick, channel)
	}
}

func (b *Bot) PartChannel(channel string) {
	if b.IsConnected() {
		util.Debug("Bot %s is leaving channel %s", b.CurrentNick, channel)
		b.Connection.Part(channel)

		// Update joined channels map
		b.mutex.Lock()
		delete(b.joinedChannels, channel)
		b.mutex.Unlock()
	} else {
		util.Debug("Bot %s is not connected; cannot part channel %s", b.CurrentNick, channel)
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
			util.Info("Bot %s is not in channel %s, rejoining", b.CurrentNick, channel)
			b.JoinChannel(channel)
		}
	}
}

func (b *Bot) Reconnect() {
	if b.IsConnected() {
		oldNick := b.CurrentNick

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
		b.CurrentNick = oldNick
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

		b.CurrentNick = newNick
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
		util.Debug("Bot %s is not connected; cannot reconnect", b.CurrentNick)
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
			util.Info("Successfully reconnected with previous nick: %s", b.CurrentNick)

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
			util.Info("Using new random nick for reconnection attempt %d: %s", attempts+1, b.CurrentNick)
		}

		util.Info("Bot %s is attempting to reconnect (attempt %d/%d, retry interval: %v)",
			b.CurrentNick, attempts+1, maxRetries, retryInterval)

		err := b.connectWithRetry()
		if err == nil {
			util.Info("Bot %s reconnected", b.CurrentNick)

			// Ensure we rejoin all channels
			time.Sleep(5 * time.Second) // Give the server a moment
			b.checkAndRejoinChannels()
			return
		}
		util.Error("Attempt %d failed: %v", attempts+1, err)
		time.Sleep(retryInterval)
	}

	util.Error("Bot %s could not reconnect after %d attempts", b.CurrentNick, maxRetries)
	b.gaveUp = true
}

// sendMessageSemaphore is a semaphore to limit the number of concurrent SendMessage operations
var sendMessageSemaphore = make(chan struct{}, 5) // Allow up to 5 concurrent operations

func (b *Bot) SendMessage(target, message string) {
	// Sanitize inputs
	target = strings.TrimSpace(target)
	message = strings.TrimSpace(message)

	// Skip empty messages or targets
	if target == "" || message == "" {
		util.Warning("SendMessage: Empty target or message")
		return
	}

	// Check if the bot is connected
	if !b.IsConnected() {
		util.Debug("Bot %s is not connected; cannot send message to %s", b.CurrentNick, target)
		return
	}

	// Log the message being sent
	util.Debug("Bot %s is sending message to %s: %s", b.CurrentNick, target, message)

	// Use a non-blocking select to check if we can acquire the semaphore
	select {
	// Try to acquire the semaphore
	case sendMessageSemaphore <- struct{}{}:
		// Semaphore acquired, proceed with sending the message
		go func(t, m string) {
			// Ensure we release the semaphore when done
			defer func() {
				<-sendMessageSemaphore // Release the semaphore
				if r := recover(); r != nil {
					util.Error("Panic in SendMessage: %v", r)
				}
			}()

			// Send the message directly without a nested goroutine
			// Set a deadline on the connection to prevent blocking
			b.Connection.Privmsg(t, m)
			util.Debug("SendMessage: Message sent successfully to %s via bot %s", t, b.CurrentNick)
		}(target, message)

	default:
		// Semaphore full, log a warning and drop the message
		util.Warning("SendMessage: Too many concurrent messages, dropping message to %s", target)
	}
}

func (b *Bot) AttemptNickChange(nick string) {
	util.Debug("Bot %s received request to change nick to %s", b.CurrentNick, nick)
	if b.shouldChangeNick(nick) {
		util.Info("Bot %s is attempting to change nick to %s", b.CurrentNick, nick)
		b.ChangeNick(nick)
	} else {
		util.Debug("Bot %s decided not to change nick to %s", b.CurrentNick, nick)
		b.nickManager.ReturnNickToPool(nick)
	}
}

func (b *Bot) shouldChangeNick(nick string) bool {
	if util.IsTargetNick(b.CurrentNick, b.nickManager.GetNicksToCatch()) {
		return false
	}
	return b.CurrentNick != nick
}

func (b *Bot) handlePrivMsg(e *irc.Event) {
	util.Debug("Received PRIVMSG: target=%s, sender=%s, message=%s", e.Arguments[0], e.Nick, e.Message())

	// Execute command handling directly without a timeout
	// This is important because the timeout in HandleCommands is sufficient
	b.HandleCommands(e)
}

func (b *Bot) SetOwnerList(owners auth.OwnerList) {
	b.mutex.Lock()
	defer b.mutex.Unlock()
	b.owners = owners
	util.Debug("Bot %s set owners: %v", b.CurrentNick, owners)
}

func (b *Bot) SetChannels(channels []string) {
	b.mutex.Lock()
	defer b.mutex.Unlock()
	b.channels = channels
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
	// Quick check for empty data
	if data == "" {
		return
	}

	// Forward to BNC tunnel if available
	if b.bncServer != nil && b.bncServer.Tunnel != nil {
		b.bncServer.Tunnel.WriteToConn(data)
	}

	// Forward to all active DCC tunnels
	b.dccTunnelsMutex.Lock()

	// Create a copy of active tunnels to avoid holding the lock while sending
	activeTunnels := make([]*dcc.DCCTunnel, 0, len(b.dccTunnels))
	for _, tunnel := range b.dccTunnels {
		if tunnel != nil && tunnel.IsActive() {
			activeTunnels = append(activeTunnels, tunnel)
		}
	}
	b.dccTunnelsMutex.Unlock()

	// Forward to all active tunnels without holding the lock
	for _, tunnel := range activeTunnels {
		// Use a separate goroutine to avoid blocking if one tunnel is slow
		go tunnel.WriteToConn(data)
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

	// Lock before accessing the tunnels map
	b.dccTunnelsMutex.Lock()

	// Get the owner's nick
	ownerNick := e.Nick

	// Check if owner already has an active tunnel
	if existingTunnel, exists := b.dccTunnels[ownerNick]; exists && existingTunnel.IsActive() {
		util.Debug("DCC: Owner %s already has an active DCC tunnel - closing old one", ownerNick)
		existingTunnel.Stop()
	}

	// Create new tunnel with proper cleanup
	tunnel := dcc.NewDCCTunnel(b, ownerNick, func() {
		b.dccTunnelsMutex.Lock()
		delete(b.dccTunnels, ownerNick)
		b.dccTunnelsMutex.Unlock()
	})

	// Store the tunnel in both maps (legacy and new)
	b.dccTunnel = tunnel // For backward compatibility
	b.dccTunnels[ownerNick] = tunnel
	b.dccTunnelsMutex.Unlock()

	util.Debug("DCC: Starting DCC tunnel with %s", ownerNick)
	tunnel.Start(conn)
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
