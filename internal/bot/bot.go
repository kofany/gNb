package bot

import (
	"fmt"
	"strings"
	"time"

	"github.com/kofany/gNb/internal/auth"
	"github.com/kofany/gNb/internal/config"
	"github.com/kofany/gNb/internal/types"
	"github.com/kofany/gNb/internal/util"
	irc "github.com/kofany/go-ircevent"
)

// Bot represents a single IRC bot
type Bot struct {
	Config          *config.BotConfig
	GlobalConfig    *config.GlobalConfig
	Connection      *irc.Connection
	CurrentNick     string
	Username        string
	Realname        string
	isConnected     bool
	owners          auth.OwnerList
	channels        []string
	nickManager     types.NickManager
	isReconnecting  bool
	lastConnectTime time.Time
	connected       chan struct{}
	botManager      types.BotManager
	gaveUp          bool
	isonResponse    chan []string
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
	b.botManager = manager
}

// SetNickManager sets the NickManager for this bot
func (b *Bot) SetNickManager(manager types.NickManager) {
	b.nickManager = manager
}

// NewBot creates a new Bot instance
func NewBot(cfg *config.BotConfig, globalConfig *config.GlobalConfig, nm types.NickManager, bm types.BotManager) *Bot {
	nick, err := util.GenerateRandomNick(globalConfig.NickAPI.URL, globalConfig.NickAPI.MaxWordLength, globalConfig.NickAPI.Timeout)
	if err != nil {
		nick = util.GenerateFallbackNick()
	}

	ident, err := util.GenerateRandomNick(globalConfig.NickAPI.URL, globalConfig.NickAPI.MaxWordLength, globalConfig.NickAPI.Timeout)
	if err != nil {
		ident = "botuser"
	}

	realname, err := util.GenerateRandomNick(globalConfig.NickAPI.URL, globalConfig.NickAPI.MaxWordLength, globalConfig.NickAPI.Timeout)
	if err != nil {
		realname = "Nick Catcher Bot"
	}

	bot := &Bot{
		Config:       cfg,
		GlobalConfig: globalConfig,
		CurrentNick:  nick,
		Username:     ident,
		Realname:     realname,
		isConnected:  false,
		nickManager:  nm,
		connected:    make(chan struct{}),
		botManager:   bm,
		isonResponse: make(chan []string, 1),
	}

	nm.RegisterBot(bot)
	return bot
}

// IsConnected returns the connection status of the bot
func (b *Bot) IsConnected() bool {
	return b.isConnected
}

// Connect establishes a connection to the IRC server with retry logic
func (b *Bot) Connect() error {
	return b.connectWithRetry()
}

// connectWithRetry attempts to connect to the server with a specified number of retries
func (b *Bot) connectWithRetry() error {
	maxRetries := b.GlobalConfig.ReconnectRetries
	retryInterval := time.Duration(b.GlobalConfig.ReconnectInterval) * time.Second

	for attempts := 0; attempts < maxRetries; attempts++ {
		b.Connection = irc.IRC(b.CurrentNick, b.Username, b.Config.Vhost)
		b.Connection.VerboseCallbackHandler = false
		b.Connection.Debug = false
		b.Connection.UseTLS = b.Config.SSL
		b.Connection.RealName = b.Realname

		// Initialize connected channel
		b.connected = make(chan struct{})

		// Add callbacks
		b.addCallbacks()

		util.Info("Bot %s is attempting to connect to %s", b.CurrentNick, b.Config.ServerAddress())
		err := b.Connection.Connect(b.Config.ServerAddress())
		if err != nil {
			util.Error("Attempt %d: Failed to connect bot %s: %v", attempts+1, b.CurrentNick, err)
			time.Sleep(retryInterval)
			continue
		}

		go b.Connection.Loop()

		// Wait for connection confirmation or timeout
		select {
		case <-b.connected:
			// Connection established
			util.Info("Bot %s successfully connected", b.CurrentNick)
			return nil
		case <-time.After(30 * time.Second):
			util.Warning("Bot %s did not receive connection confirmation within 30 seconds", b.CurrentNick)
			b.Connection.Quit()
		}
	}

	return fmt.Errorf("bot %s could not connect after %d attempts", b.CurrentNick, maxRetries)
}

// addCallbacks adds necessary callbacks to the IRC connection
func (b *Bot) addCallbacks() {
	// Callback for successful connection
	b.Connection.AddCallback("001", func(e *irc.Event) {
		b.isConnected = true
		b.gaveUp = false // Reset flag upon successful connection
		b.lastConnectTime = time.Now()
		util.Info("Bot %s connected to %s as %s", b.CurrentNick, b.Config.Server, b.CurrentNick)
		// Join channels
		for _, channel := range b.channels {
			b.JoinChannel(channel)
		}
		// Signal that connection has been established
		select {
		case <-b.connected:
			// Kanał już zamknięty, nic nie robimy
		default:
			close(b.connected)
		}
	})

	// Callback for nick changes
	b.Connection.AddCallback("NICK", func(e *irc.Event) {
		if e.Nick == b.Connection.GetNick() {
			oldNick := b.CurrentNick
			b.CurrentNick = e.Message()
			util.Info("Bot %s changed nick to %s", oldNick, b.CurrentNick)
		}
	})

	// Callback for ISON response
	b.Connection.AddCallback("303", b.handleISONResponse)

	// List of nick-related error codes
	nickErrorCodes := []string{"431", "432", "433", "436", "437", "484"}

	for _, code := range nickErrorCodes {
		codeCopy := code
		b.Connection.AddCallback(codeCopy, func(e *irc.Event) {
			util.Warning("Bot %s encountered error %s: %s", b.CurrentNick, codeCopy, e.Message())
			// Jeśli dotyczy zmiany nicka, dodaj nick do tempUnavailableNicks
			if len(e.Arguments) > 1 {
				nickInQuestion := e.Arguments[1]
				b.nickManager.MarkNickAsTemporarilyUnavailable(nickInQuestion)
			}
		})
	}

	// Callback for private and public messages
	b.Connection.AddCallback("PRIVMSG", b.handlePrivMsg)

	// Callback for disconnection
	b.Connection.AddCallback("DISCONNECTED", func(e *irc.Event) {
		util.Warning("Bot %s disconnected from server", b.CurrentNick)
		b.isConnected = false
		if !b.isReconnecting && !b.gaveUp {
			go b.handleReconnect()
		} else if b.gaveUp {
			util.Info("Bot %s has given up on reconnecting", b.CurrentNick)
		}
	})
}

// handleISONResponse handles the ISON responses and forwards them to the NickManager
func (b *Bot) handleISONResponse(e *irc.Event) {
	isonResponse := strings.Fields(e.Message())
	util.Debug("Bot %s received ISON response: %v", b.CurrentNick, isonResponse)
	// Send the response to the NickManager via the channel
	select {
	case b.isonResponse <- isonResponse:
	default:
		util.Warning("Bot %s isonResponse channel is full", b.CurrentNick)
	}
}

// RequestISON sends an ISON command and waits for the response
func (b *Bot) RequestISON(nicks []string) ([]string, error) {
	if !b.IsConnected() {
		return nil, fmt.Errorf("bot %s is not connected", b.CurrentNick)
	}

	command := fmt.Sprintf("ISON %s", strings.Join(nicks, " "))
	util.Debug("Bot %s is sending ISON command: %s", b.CurrentNick, command)
	b.Connection.SendRaw(command)

	// Wait for the ISON response or timeout
	select {
	case response := <-b.isonResponse:
		return response, nil
	case <-time.After(10 * time.Second):
		return nil, fmt.Errorf("bot %s did not receive ISON response in time", b.CurrentNick)
	}
}

// ChangeNick attempts to change the bot's nick to a new one
func (b *Bot) ChangeNick(newNick string) {
	if b.IsConnected() {
		util.Info("Bot %s is attempting to change nick to %s", b.CurrentNick, newNick)
		b.Connection.Nick(newNick)
	} else {
		util.Debug("Bot %s is not connected; cannot change nick", b.CurrentNick)
	}
}

// JoinChannel joins a specified channel
func (b *Bot) JoinChannel(channel string) {
	if b.IsConnected() {
		util.Debug("Bot %s is joining channel %s", b.CurrentNick, channel)
		b.Connection.Join(channel)
	} else {
		util.Debug("Bot %s is not connected; cannot join channel %s", b.CurrentNick, channel)
	}
}

// PartChannel leaves a specified channel
func (b *Bot) PartChannel(channel string) {
	if b.IsConnected() {
		util.Debug("Bot %s is leaving channel %s", b.CurrentNick, channel)
		b.Connection.Part(channel)
	} else {
		util.Debug("Bot %s is not connected; cannot part channel %s", b.CurrentNick, channel)
	}
}

// Reconnect disconnects and reconnects the bot to the IRC server
func (b *Bot) Reconnect() {
	if b.IsConnected() {
		b.Quit("Reconnecting")
	}
	time.Sleep(5 * time.Second)
	err := b.Connect()
	if err != nil {
		util.Error("Failed to reconnect bot %s: %v", b.CurrentNick, err)
	}
}

// handleReconnect handles reconnection attempts after disconnection
func (b *Bot) handleReconnect() {
	b.isReconnecting = true
	defer func() { b.isReconnecting = false }()

	maxRetries := b.GlobalConfig.ReconnectRetries
	retryInterval := time.Duration(b.GlobalConfig.ReconnectInterval) * time.Second

	for attempts := 0; attempts < maxRetries; attempts++ {
		util.Info("Bot %s is attempting to reconnect (attempt %d/%d)", b.CurrentNick, attempts+1, maxRetries)
		err := b.connectWithRetry()
		if err == nil {
			util.Info("Bot %s reconnected", b.CurrentNick)
			return
		}
		util.Error("Attempt %d failed: %v", attempts+1, err)
		time.Sleep(retryInterval)
	}

	util.Error("Bot %s could not reconnect after %d attempts", b.CurrentNick, maxRetries)
	b.gaveUp = true
}

// SendMessage sends a message to a specified target (channel or user)
func (b *Bot) SendMessage(target, message string) {
	if b.IsConnected() {
		util.Debug("Bot %s is sending message to %s: %s", b.CurrentNick, target, message)
		b.Connection.Privmsg(target, message)
	} else {
		util.Debug("Bot %s is not connected; cannot send message to %s", b.CurrentNick, target)
	}
}

// Quit disconnects the bot from the IRC server
func (b *Bot) Quit(message string) {
	if b.IsConnected() {
		util.Info("Bot %s is disconnecting: %s", b.CurrentNick, message)
		b.Connection.QuitMessage = message
		b.Connection.Quit()
		b.isConnected = false
	}
}

// AttemptNickChange attempts to change the bot's nick to an available nick
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

// shouldChangeNick determines if the bot should change its nick
func (b *Bot) shouldChangeNick(nick string) bool {
	// Check if current nick is already a target nick
	if util.IsTargetNick(b.CurrentNick, b.nickManager.GetNicksToCatch()) {
		return false
	}
	return b.CurrentNick != nick
}

// handlePrivMsg handles private and public messages and owner commands
func (b *Bot) handlePrivMsg(e *irc.Event) {
	b.HandleCommands(e)
}

// SetOwnerList sets the list of owners for the bot
func (b *Bot) SetOwnerList(owners auth.OwnerList) {
	b.owners = owners
	util.Debug("Bot %s set owners: %v", b.CurrentNick, owners)
}

// SetChannels sets the list of channels for the bot to join
func (b *Bot) SetChannels(channels []string) {
	b.channels = channels
	util.Debug("Bot %s set channels: %v", b.CurrentNick, channels)
}

// GetCurrentNick returns the bot's current nick
func (b *Bot) GetCurrentNick() string {
	return b.CurrentNick
}
