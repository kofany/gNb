package bot

import (
	"fmt"
	"strings"
	"time"

	"github.com/kofany/gNb/internal/auth"
	"github.com/kofany/gNb/internal/types"
	"github.com/kofany/gNb/internal/util"
	irc "github.com/kofany/go-ircevent"
)

type CommandType int

const (
	SingleCommand CommandType = iota
	MassCommand
)

type Command struct {
	Type    CommandType
	Handler func(*Bot, *irc.Event, []string)
}

var commandMap map[string]Command

func init() {
	commandMap = map[string]Command{
		"quit":       {Type: SingleCommand, Handler: handleQuitCommand},
		"say":        {Type: SingleCommand, Handler: handleSayCommand},
		"join":       {Type: MassCommand, Handler: handleJoinCommand},
		"part":       {Type: MassCommand, Handler: handlePartCommand},
		"reconnect":  {Type: MassCommand, Handler: handleReconnectCommand},
		"addnick":    {Type: SingleCommand, Handler: handleAddNickCommand},
		"delnick":    {Type: SingleCommand, Handler: handleDelNickCommand},
		"listnicks":  {Type: SingleCommand, Handler: handleListNicksCommand},
		"addowner":   {Type: SingleCommand, Handler: handleAddOwnerCommand},
		"delowner":   {Type: SingleCommand, Handler: handleDelOwnerCommand},
		"bnc":        {Type: SingleCommand, Handler: handleBNCCommand},
		"listowners": {Type: SingleCommand, Handler: handleListOwnersCommand},
	}
}

// Modyfikujemy funkcję HandleCommands w pliku commands.go
func (b *Bot) HandleCommands(e *irc.Event) {
	util.Debug("Received command for bot %s: %s", b.GetCurrentNick(), e.Message())

	message := e.Message()
	sender := e.Nick
	target := e.Arguments[0]
	isChannelMsg := strings.HasPrefix(target, "#")

	if !startsWithAny(message, b.GlobalConfig.CommandPrefixes) {
		return
	}

	if !auth.IsOwner(e, b.owners) {
		util.Debug("Command rejected: sender %s is not an owner", sender)
		return
	}

	commandLine := strings.TrimLeft(message, strings.Join(b.GlobalConfig.CommandPrefixes, ""))
	args := strings.Fields(commandLine)
	if len(args) == 0 {
		return
	}

	cmdName := strings.ToLower(args[0])
	cmd, exists := commandMap[cmdName]
	if !exists {
		b.sendReply(isChannelMsg, target, sender, "Unknown command")
		return
	}

	util.Debug("Command %s recognized for bot %s", cmdName, b.GetCurrentNick())

	switch cmd.Type {
	case SingleCommand:
		util.Debug("Executing command %s for bot %s", cmdName, b.GetCurrentNick())
		cmd.Handler(b, e, args[1:])
	case MassCommand:
		if b.botManager.CanExecuteMassCommand(cmdName) {
			util.Debug("Executing mass command %s for bot %s", cmdName, b.GetCurrentNick())
			cmd.Handler(b, e, args[1:])
		} else {
			util.Debug("Mass command %s not executed due to cooldown", cmdName)
		}
	}
}

func (b *Bot) sendReply(isChannelMsg bool, target, sender, message string) {
	if isChannelMsg {
		b.GetBotManager().CollectReactions(target, message)
	} else {
		b.SendMessage(sender, message)
	}
}

func startsWithAny(s string, prefixes []string) bool {
	for _, prefix := range prefixes {
		if strings.HasPrefix(s, prefix) {
			return true
		}
	}
	return false
}

func handleQuitCommand(b *Bot, e *irc.Event, args []string) {
	sender := e.Nick
	target := e.Arguments[0]
	isChannelMsg := strings.HasPrefix(target, "#")

	b.sendReply(isChannelMsg, target, sender, "Quitting...")
	b.Quit("Command from owner")
}

func handleSayCommand(b *Bot, e *irc.Event, args []string) {
	sender := e.Nick
	target := e.Arguments[0]
	isChannelMsg := strings.HasPrefix(target, "#")

	if len(args) >= 2 {
		targetChannel := args[0]
		msg := strings.Join(args[1:], " ")
		if strings.HasPrefix(targetChannel, "#") {
			b.GetBotManager().CollectReactions(targetChannel, msg)
		} else {
			b.SendMessage(targetChannel, msg)
		}
	} else {
		b.sendReply(isChannelMsg, target, sender, "Usage: say <channel/nick> <message>")
	}
}

func handleJoinCommand(b *Bot, e *irc.Event, args []string) {
	sender := e.Nick
	target := e.Arguments[0]
	isChannelMsg := strings.HasPrefix(target, "#")

	if len(args) >= 1 {
		channel := args[0]
		if isChannelMsg {
			for _, bot := range b.botManager.GetBots() {
				bot.JoinChannel(channel)
			}
			b.GetBotManager().CollectReactions(target, fmt.Sprintf("All bots are joining channel %s", channel))
		} else {
			b.JoinChannel(channel)
			b.sendReply(isChannelMsg, target, sender, fmt.Sprintf("Joined channel %s", channel))
		}
	} else {
		b.sendReply(isChannelMsg, target, sender, "Usage: join <channel>")
	}
}

func handlePartCommand(b *Bot, e *irc.Event, args []string) {
	sender := e.Nick
	target := e.Arguments[0]
	isChannelMsg := strings.HasPrefix(target, "#")

	if len(args) >= 1 {
		channel := args[0]
		if isChannelMsg {
			for _, bot := range b.botManager.GetBots() {
				bot.PartChannel(channel)
			}
			b.GetBotManager().CollectReactions(target, fmt.Sprintf("All bots are leaving channel %s", channel))
		} else {
			b.PartChannel(channel)
			b.sendReply(isChannelMsg, target, sender, fmt.Sprintf("Left channel %s", channel))
		}
	} else {
		b.sendReply(isChannelMsg, target, sender, "Usage: part <channel>")
	}
}

func handleReconnectCommand(b *Bot, e *irc.Event, args []string) {
	sender := e.Nick
	target := e.Arguments[0]
	isChannelMsg := strings.HasPrefix(target, "#")

	if isChannelMsg {
		b.GetBotManager().CollectReactions(target, "All bots are reconnecting with new nicks...")
		for _, bot := range b.GetBotManager().GetBots() {
			go func(bot types.Bot) {
				bot.Reconnect()
			}(bot)
		}
	} else {
		b.sendReply(isChannelMsg, target, sender, "Reconnecting with a new nick...")
		b.Reconnect()
	}
}

func handleAddNickCommand(b *Bot, e *irc.Event, args []string) {
	sender := e.Nick
	target := e.Arguments[0]
	isChannelMsg := strings.HasPrefix(target, "#")

	if len(args) >= 1 {
		nick := args[0]
		err := b.nickManager.AddNick(nick)
		if err != nil {
			b.sendReply(isChannelMsg, target, sender, fmt.Sprintf("Error adding nick: %v", err))
		} else {
			b.sendReply(isChannelMsg, target, sender, fmt.Sprintf("Nick '%s' has been added.", nick))
		}
	} else {
		b.sendReply(isChannelMsg, target, sender, "Usage: addnick <nick>")
	}
}

func handleDelNickCommand(b *Bot, e *irc.Event, args []string) {
	sender := e.Nick
	target := e.Arguments[0]
	isChannelMsg := strings.HasPrefix(target, "#")

	if len(args) >= 1 {
		nick := args[0]
		err := b.nickManager.RemoveNick(nick)
		if err != nil {
			b.sendReply(isChannelMsg, target, sender, fmt.Sprintf("Error removing nick: %v", err))
		} else {
			b.sendReply(isChannelMsg, target, sender, fmt.Sprintf("Nick '%s' has been removed.", nick))
		}
	} else {
		b.sendReply(isChannelMsg, target, sender, "Usage: delnick <nick>")
	}
}

func handleListNicksCommand(b *Bot, e *irc.Event, args []string) {
	sender := e.Nick
	target := e.Arguments[0]
	isChannelMsg := strings.HasPrefix(target, "#")

	nicks := b.nickManager.GetNicks()
	b.sendReply(isChannelMsg, target, sender, fmt.Sprintf("Current nicks: %s", strings.Join(nicks, ", ")))
}

func handleAddOwnerCommand(b *Bot, e *irc.Event, args []string) {
	sender := e.Nick
	target := e.Arguments[0]
	isChannelMsg := strings.HasPrefix(target, "#")

	if len(args) >= 1 {
		ownerMask := args[0]
		err := b.botManager.AddOwner(ownerMask)
		if err != nil {
			b.sendReply(isChannelMsg, target, sender, fmt.Sprintf("Error adding owner: %v", err))
		} else {
			b.sendReply(isChannelMsg, target, sender, fmt.Sprintf("Owner '%s' has been added.", ownerMask))
		}
	} else {
		b.sendReply(isChannelMsg, target, sender, "Usage: addowner <mask>")
	}
}

func handleDelOwnerCommand(b *Bot, e *irc.Event, args []string) {
	sender := e.Nick
	target := e.Arguments[0]
	isChannelMsg := strings.HasPrefix(target, "#")

	if len(args) >= 1 {
		ownerMask := args[0]
		err := b.botManager.RemoveOwner(ownerMask)
		if err != nil {
			b.sendReply(isChannelMsg, target, sender, fmt.Sprintf("Error removing owner: %v", err))
		} else {
			b.sendReply(isChannelMsg, target, sender, fmt.Sprintf("Owner '%s' has been removed.", ownerMask))
		}
	} else {
		b.sendReply(isChannelMsg, target, sender, "Usage: delowner <mask>")
	}
}

func handleListOwnersCommand(b *Bot, e *irc.Event, args []string) {
	sender := e.Nick
	target := e.Arguments[0]
	isChannelMsg := strings.HasPrefix(target, "#")

	owners := b.botManager.GetOwners()
	b.sendReply(isChannelMsg, target, sender, fmt.Sprintf("Current owners: %s", strings.Join(owners, ", ")))
}

func handleBNCCommand(b *Bot, e *irc.Event, args []string) {
	sender := e.Nick
	target := e.Arguments[0]
	isChannelMsg := strings.HasPrefix(target, "#")

	if len(args) < 1 {
		b.sendReply(isChannelMsg, target, sender, "Usage: !bnc <start|stop>")
		return
	}

	switch args[0] {
	case "start":
		port, password, err := b.StartBNC()
		if err != nil {
			b.sendReply(isChannelMsg, target, sender, fmt.Sprintf("Failed to start BNC: %v", err))
		} else {
			b.sendReply(false, sender, sender, "BNC started successfully. Use the following command to connect:")
			sshCommand := fmt.Sprintf("ssh -p %d %s@%s %s", port, b.GetCurrentNick(), b.Config.Vhost, password)
			time.Sleep(1 * time.Second)
			b.sendReply(false, sender, sender, sshCommand)
		}
	case "stop":
		b.StopBNC()
		b.sendReply(isChannelMsg, target, sender, "BNC stopped")
	default:
		b.sendReply(isChannelMsg, target, sender, "Invalid BNC command. Use 'start' or 'stop'")
	}
}
