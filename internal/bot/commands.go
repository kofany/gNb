package bot

import (
	"strings"

	"github.com/kofany/gNb/internal/auth"
	irc "github.com/kofany/go-ircevent"
)

// HandleCommands processes owner commands received by the bot.
func (b *Bot) HandleCommands(e *irc.Event) {
	message := e.Message()
	sender := e.Nick

	// Check if message starts with any command prefixes
	if !startsWithAny(message, b.GlobalConfig.CommandPrefixes) {
		return
	}

	// Check if sender is an owner
	if !auth.IsOwner(e, b.owners) {
		return
	}

	// Before processing the command, check if this bot should handle it
	if !b.shouldHandleCommand() {
		return
	}

	// Parse the command
	commandLine := strings.TrimLeft(message, strings.Join(b.GlobalConfig.CommandPrefixes, ""))
	args := strings.Fields(commandLine)

	if len(args) == 0 {
		return
	}

	// Handle commands
	switch strings.ToLower(args[0]) {
	case "quit":
		b.Quit("Command from owner")
	case "say":
		if len(args) >= 3 {
			targetChannel := args[1]
			msg := strings.Join(args[2:], " ")
			b.SendMessage(targetChannel, msg)
		} else {
			b.SendNotice(sender, "Usage: say <channel> <message>")
		}
	case "join":
		if len(args) >= 2 {
			channel := args[1]
			b.JoinChannel(channel)
		} else {
			b.SendNotice(sender, "Usage: join <channel>")
		}
	case "part":
		if len(args) >= 2 {
			channel := args[1]
			b.PartChannel(channel)
		} else {
			b.SendNotice(sender, "Usage: part <channel>")
		}
	case "reconnect":
		b.Reconnect()
	default:
		b.SendNotice(sender, "Unknown command")
	}
}

// Helper function to check if the string starts with any of the provided prefixes
func startsWithAny(s string, prefixes []string) bool {
	for _, prefix := range prefixes {
		if strings.HasPrefix(s, prefix) {
			return true
		}
	}
	return false
}
