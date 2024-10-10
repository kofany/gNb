package types

import "github.com/kofany/gNb/internal/auth"

type Bot interface {
	AttemptNickChange(nick string)
	GetCurrentNick() string
	IsConnected() bool
	SetOwnerList(owners auth.OwnerList)
	SetChannels(channels []string)
	SendISON(nicks []string)
	Connect() error
	Quit(message string)
	Reconnect()
}

type NickManager interface {
	ReceiveISONResponse(onlineNicks []string)
	ReturnNickToPool(nick string)
	RegisterBot(bot Bot)
	GetNicksToCatch() []string
}

type BotManager interface {
	ShouldHandleCommand(bot Bot) bool
	GetAvailableBots() []Bot
	AssignBotForNick(nick string) Bot
}
