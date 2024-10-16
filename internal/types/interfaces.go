package types

import (
	"time"

	"github.com/kofany/gNb/internal/auth"
	irc "github.com/kofany/go-ircevent"
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
}

type NickManager interface {
	RegisterBot(bot Bot)
	ReturnNickToPool(nick string)
	SetBots(bots []Bot)
	GetNicksToCatch() []string
	AddNick(nick string) error
	RemoveNick(nick string) error
	GetNicks() []string
	MarkNickAsTemporarilyUnavailable(nick string)
	NotifyNickChange(oldNick, newNick string)
	Start()
}

type BotManager interface {
	StartBots()
	Stop()
	ShouldHandleCommand(bot Bot) bool
	CanExecuteMassCommand(cmdName string) bool
	AddOwner(ownerMask string) error
	RemoveOwner(ownerMask string) error
	GetOwners() []string
	GetBots() []Bot
	GetNickManager() NickManager
	SetMassCommandCooldown(duration time.Duration)
	GetMassCommandCooldown() time.Duration
}
