package bot

import (
	"fmt"
	"strings"

	"github.com/kofany/gNb/internal/nickcatcher"
	"github.com/kofany/gNb/internal/util"
	irc "github.com/kofany/go-ircevo"
)

// Inicjalizacja komend związanych z systemem przechwytywania nicków
func init() {
	// Dodaj komendy do istniejącej mapy komend
	commandMap["nicklist"] = Command{Type: SingleCommand, Handler: handleNickListCommand}
	commandMap["addnick"] = Command{Type: SingleCommand, Handler: handleAddNickCommand}
	commandMap["delnick"] = Command{Type: SingleCommand, Handler: handleDelNickCommand}
	commandMap["nickstatus"] = Command{Type: SingleCommand, Handler: handleNickStatusCommand}
}

// handleNickListCommand obsługuje komendę wyświetlającą listę priorytetowych nicków
func handleNickListCommand(b *Bot, e *irc.Event, args []string) {
	sender := e.Nick
	target := e.Arguments[0]
	isChannelMsg := strings.HasPrefix(target, "#")

	nc := nickcatcher.GetNickCatcherInstance()
	if nc == nil {
		b.sendReply(isChannelMsg, target, sender, "Nick catcher system not available")
		return
	}

	response, err := nc.ExecuteNickCatcherCommandForIRC("nicklist", args)
	if err != nil {
		b.sendReply(isChannelMsg, target, sender, fmt.Sprintf("Error: %v", err))
		return
	}

	b.sendReply(isChannelMsg, target, sender, response)
}

// handleAddNickCommand obsługuje komendę dodającą nick do listy priorytetowych
func handleAddNickCommand(b *Bot, e *irc.Event, args []string) {
	sender := e.Nick
	target := e.Arguments[0]
	isChannelMsg := strings.HasPrefix(target, "#")

	if len(args) < 1 {
		b.sendReply(isChannelMsg, target, sender, "Usage: addnick <nick>")
		return
	}

	nc := nickcatcher.GetNickCatcherInstance()
	if nc == nil {
		b.sendReply(isChannelMsg, target, sender, "Nick catcher system not available")
		return
	}

	response, err := nc.ExecuteNickCatcherCommandForIRC("addnick", args)
	if err != nil {
		b.sendReply(isChannelMsg, target, sender, fmt.Sprintf("Error: %v", err))
		return
	}

	b.sendReply(isChannelMsg, target, sender, response)
	util.Info("Nick %s added to priority list by %s", args[0], sender)
}

// handleDelNickCommand obsługuje komendę usuwającą nick z listy priorytetowych
func handleDelNickCommand(b *Bot, e *irc.Event, args []string) {
	sender := e.Nick
	target := e.Arguments[0]
	isChannelMsg := strings.HasPrefix(target, "#")

	if len(args) < 1 {
		b.sendReply(isChannelMsg, target, sender, "Usage: delnick <nick>")
		return
	}

	nc := nickcatcher.GetNickCatcherInstance()
	if nc == nil {
		b.sendReply(isChannelMsg, target, sender, "Nick catcher system not available")
		return
	}

	response, err := nc.ExecuteNickCatcherCommandForIRC("delnick", args)
	if err != nil {
		b.sendReply(isChannelMsg, target, sender, fmt.Sprintf("Error: %v", err))
		return
	}

	b.sendReply(isChannelMsg, target, sender, response)
	util.Info("Nick %s removed from priority list by %s", args[0], sender)
}

// handleNickStatusCommand obsługuje komendę wyświetlającą status systemu przechwytywania nicków
func handleNickStatusCommand(b *Bot, e *irc.Event, args []string) {
	sender := e.Nick
	target := e.Arguments[0]
	isChannelMsg := strings.HasPrefix(target, "#")

	nc := nickcatcher.GetNickCatcherInstance()
	if nc == nil {
		b.sendReply(isChannelMsg, target, sender, "Nick catcher system not available")
		return
	}

	// Dla kanałów zawsze używamy kompaktowej wersji
	// Dla prywatnych wiadomości możemy użyć szczegółowej wersji
	detailed := len(args) > 0 && args[0] == "detail" && !isChannelMsg
	modifiedArgs := args
	if detailed {
		modifiedArgs = []string{"detail"}
	} else {
		modifiedArgs = []string{}
	}

	response, err := nc.ExecuteNickCatcherCommandForIRC("nickstatus", modifiedArgs)
	if err != nil {
		b.sendReply(isChannelMsg, target, sender, fmt.Sprintf("Error: %v", err))
		return
	}

	b.sendReply(isChannelMsg, target, sender, response)
}
