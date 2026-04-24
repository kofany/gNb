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
	IsOnChannel(channel string) bool
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
	SetEventSink(sink EventSink)
	GetBotID() string
	GetJoinedChannels() []string
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
	MarkServerNoLetters(serverName string)
	Start()
	Stop()
	SetEventSink(sink EventSink)
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
	SetEventSink(sink EventSink)
}

type ReactionRequest struct {
	Channel   string
	Message   string
	Timestamp time.Time
	Action    func() error
}

// EventSink is the outbound channel through which Bot / BotManager /
// NickManager notify external observers (currently the Panel API) about
// lifecycle changes. Methods must not block the caller. A nil sink means
// observation is disabled; callers check for nil before invoking.
type EventSink interface {
	BotConnected(botID, nick, server string)
	BotDisconnected(botID, reason string)
	BotNickChanged(botID, oldNick, newNick string)
	BotNickCaptured(botID, nick, kind string)
	BotJoinedChannel(botID, channel string)
	BotPartedChannel(botID, channel string)
	BotKicked(botID, channel, by, reason string)
	BotBanned(botID string, code int)
	BotAdded(botID, server string, port int, ssl bool, vhost string)
	BotRemoved(botID string)
	NicksChanged(nicks []string)
	OwnersChanged(owners []string)

	// BotIRCEvent delivers every IRC event a bot sees; the API routes it to
	// sessions attached to this bot_id. The event pointer must not be
	// retained past the call.
	BotIRCEvent(botID string, e *irc.Event)

	// BotRawOut delivers a line the bot has just sent to IRC, for panel
	// sessions attached to this bot_id.
	BotRawOut(botID, line string)
}
