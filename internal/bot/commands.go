package bot

import (
	"fmt"
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
	// New commands for nick management
	case "addnick":
		b.handleAddNickCommand(args, sender)
	case "delnick":
		b.handleDelNickCommand(args, sender)
	case "listnicks":
		b.handleListNicksCommand(sender)
	// New commands for owner management
	case "addowner":
		b.handleAddOwnerCommand(args, sender)
	case "delowner":
		b.handleDelOwnerCommand(args, sender)
	case "listowners":
		b.handleListOwnersCommand(sender)
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

// Command handlers for nick management

func (b *Bot) handleAddNickCommand(args []string, sender string) {
	if len(args) >= 2 {
		nick := args[1]
		err := b.nickManager.AddNick(nick)
		if err != nil {
			b.SendNotice(sender, fmt.Sprintf("Error adding nick: %v", err))
		} else {
			b.SendNotice(sender, fmt.Sprintf("Nick '%s' has been added.", nick))
		}
	} else {
		b.SendNotice(sender, "Usage: addnick <nick>")
	}
}

func (b *Bot) handleDelNickCommand(args []string, sender string) {
	if len(args) >= 2 {
		nick := args[1]
		err := b.nickManager.RemoveNick(nick)
		if err != nil {
			b.SendNotice(sender, fmt.Sprintf("Error removing nick: %v", err))
		} else {
			b.SendNotice(sender, fmt.Sprintf("Nick '%s' has been removed.", nick))
		}
	} else {
		b.SendNotice(sender, "Usage: delnick <nick>")
	}
}

func (b *Bot) handleListNicksCommand(sender string) {
	nicks := b.nickManager.GetNicks()
	b.SendNotice(sender, fmt.Sprintf("Current nicks: %s", strings.Join(nicks, ", ")))
}

// Command handlers for owner management

func (b *Bot) handleAddOwnerCommand(args []string, sender string) {
	if len(args) >= 2 {
		ownerMask := args[1]
		err := b.botManager.AddOwner(ownerMask)
		if err != nil {
			b.SendNotice(sender, fmt.Sprintf("Error adding owner: %v", err))
		} else {
			b.SendNotice(sender, fmt.Sprintf("Owner '%s' has been added.", ownerMask))
		}
	} else {
		b.SendNotice(sender, "Usage: addowner <mask>")
	}
}

func (b *Bot) handleDelOwnerCommand(args []string, sender string) {
	if len(args) >= 2 {
		ownerMask := args[1]
		err := b.botManager.RemoveOwner(ownerMask)
		if err != nil {
			b.SendNotice(sender, fmt.Sprintf("Error removing owner: %v", err))
		} else {
			b.SendNotice(sender, fmt.Sprintf("Owner '%s' has been removed.", ownerMask))
		}
	} else {
		b.SendNotice(sender, "Usage: delowner <mask>")
	}
}

func (b *Bot) handleListOwnersCommand(sender string) {
	owners := b.botManager.GetOwners()
	b.SendNotice(sender, fmt.Sprintf("Current owners: %s", strings.Join(owners, ", ")))
}
