package nickcatcher

import (
	"fmt"
	"strings"

	"github.com/kofany/gNb/internal/types"
	irc "github.com/kofany/go-ircevo"
)

// RegisterCommands rejestruje komendy związane z systemem przechwytywania nicków
func (nc *NickCatcher) RegisterCommands(bot types.Bot, commandMap map[string]interface{}) {
	// Tutaj możemy dodać komendy związane z systemem przechwytywania nicków
	// Implementacja zależy od tego, jak zdefiniowane są komendy w systemie
}

// HandleDCCCommands obsługuje komendy DCC związane z systemem przechwytywania nicków
func (nc *NickCatcher) HandleDCCCommands(command string, args []string) (string, error) {
	return nc.ExecuteNickCatcherCommand(command, args)
}

// ProcessIRCCommand przetwarza komendę IRC związaną z systemem przechwytywania nicków
func (nc *NickCatcher) ProcessIRCCommand(e *irc.Event, args []string) (string, error) {
	if len(args) == 0 {
		return "", fmt.Errorf("no command specified")
	}

	command := strings.ToLower(args[0])
	cmdArgs := args[1:]

	return nc.ExecuteNickCatcherCommandForIRC(command, cmdArgs)
}
