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
	SetNickManager(manager NickManager)
	GetNickManager() NickManager
	GetServerName() string
	StartBNC() (int, string, error)
	StopBNC()
	SendRaw(message string)
	RemoveBot()
}

type NickManager interface {
	RegisterBot(bot Bot)
	UnregisterBot(bot Bot)
	ReturnNickToPool(nick string)
	SetBots(bots []Bot)
	GetNicksToCatch() []string
	AddNick(nick string) error
	RemoveNick(nick string) error
	GetNicks() []string
	MarkNickAsTemporarilyUnavailable(nick string)
	NotifyNickChange(oldNick, newNick string)
	MarkServerNoLetters(serverName string)
	Start()
	Stop()
	Restart()
	UpdateConnectedBots()
	ResetFailedRequestCount(bot Bot)
	IncrementFailedRequestCount(bot Bot)
}

type BotManager interface {
	StartBots()
	Stop()
	CanExecuteMassCommand(cmdName string) bool
	AddOwner(ownerMask string) error
	RemoveOwner(ownerMask string) error
	GetOwners() []string
	GetBots() []Bot
	GetNickManager() NickManager
	GetTotalCreatedBots() int
	SetMassCommandCooldown(duration time.Duration)
	GetMassCommandCooldown() time.Duration
	CollectReactions(channel, message string, action func() error)
	SendSingleMsg(channel, message string)
}

type ReactionRequest struct {
	Channel   string
	Message   string
	Timestamp time.Time
	Action    func() error
}
