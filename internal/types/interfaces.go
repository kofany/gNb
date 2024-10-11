package types

import "github.com/kofany/gNb/internal/auth"

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
}

type NickManager interface {
	RegisterBot(bot Bot)
	ReturnNickToPool(nick string)
	SetBots(bots []Bot)
	GetNicksToCatch() []string
	AddNick(nick string) error
	RemoveNick(nick string) error
	GetNicks() []string
	MarkNickAsTemporarilyUnavailable(nick string) // Dodaj tę linię
}
type BotManager interface {
	ShouldHandleCommand(bot Bot) bool
	AddOwner(ownerMask string) error
	RemoveOwner(ownerMask string) error
	GetOwners() []string
}
