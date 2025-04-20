package types

import (
	"time"

	"github.com/kofany/gNb/internal/auth"
	irc "github.com/kofany/go-ircevo"
)

type Bot interface {
	AttemptNickChange(nick string)
	GetCurrentNick() string
	IsConnected() bool
	SetOwnerList(owners auth.OwnerList)
	SetChannels(channels []string)
	RequestISON(nicks []string) ([]string, error)
	Connect() error
	Quit(message string)
	Reconnect()
	SendMessage(target, message string)
	JoinChannel(channel string)
	PartChannel(channel string)
	ChangeNick(newNick string)
	HandleCommands(e *irc.Event)
	SetBotManager(manager BotManager)
	GetBotManager() BotManager

	GetServerName() string
	SendRaw(message string)
	RemoveBot()
}

type BotManager interface {
	StartBots()
	Stop()
	CanExecuteMassCommand(cmdName string) bool
	AddOwner(ownerMask string) error
	RemoveOwner(ownerMask string) error
	GetOwners() []string
	GetBots() []Bot

	GetTotalCreatedBots() int
	SetMassCommandCooldown(duration time.Duration)
	GetMassCommandCooldown() time.Duration
	CollectReactions(channel, message string, action func() error)
	SendSingleMsg(channel, message string)

	// Methods for reconnection storm prevention
	DetectMassDisconnect() bool
	HandleNetworkOutage()
	RemoveBotFromManager(bot Bot)
	IsBotInManager(bot Bot) bool
}

type ReactionRequest struct {
	Channel   string
	Message   string
	Timestamp time.Time
	Action    func() error
}
