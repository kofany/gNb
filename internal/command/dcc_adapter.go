package command

import (
	"context"
	"strings"
	"time"

	"github.com/kofany/gNb/internal/types"
	"github.com/kofany/gNb/internal/util"
)

// DCCAdapter is an adapter for DCC tunnels to use the command system
type DCCAdapter struct {
	executor *CommandExecutor
}

// NewDCCAdapter creates a new DCC adapter
func NewDCCAdapter() *DCCAdapter {
	return &DCCAdapter{
		executor: GetExecutor(),
	}
}

// ProcessCommand processes a command from a DCC tunnel
func (a *DCCAdapter) ProcessCommand(
	fullCommand string,
	bot types.Bot,
	ownerNick string,
	responseCallback func(string),
) {
	// Sanitize the command
	fullCommand = strings.TrimSpace(fullCommand)
	fullCommand = strings.TrimPrefix(fullCommand, ".")

	// Split into command and args
	parts := strings.Fields(fullCommand)
	if len(parts) == 0 {
		responseCallback("Invalid command format")
		return
	}

	command := strings.ToUpper(parts[0])
	var args []string
	if len(parts) > 1 {
		args = parts[1:]
	}

	// Create a context with a timeout - increased from 15 to 30 seconds
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)

	// Execute in a separate goroutine to avoid blocking the bot's event loop
	go func() {
		defer cancel() // Ensure context is cancelled when done

		// Get a result channel - this doesn't block
		resultChan := a.executor.ExecuteCommand(
			ctx,
			command,
			args,
			bot,
			ownerNick,
			PartyLineCommand,
			NormalPriority,
		)

		// Wait for the result with a timeout
		select {
		case result := <-resultChan:
			if result.Error != nil {
				responseCallback(result.Error.Error())
				util.Warning("DCC command %s from %s failed: %v", command, ownerNick, result.Error)
			} else if result.Output != "" {
				responseCallback(result.Output)
				util.Debug("DCC command %s from %s completed successfully", command, ownerNick)
			} else if !result.Handled {
				responseCallback("Command not handled: " + command)
				util.Warning("DCC command %s from %s was not handled", command, ownerNick)
			}
		case <-ctx.Done():
			responseCallback("Command timed out: " + command)
			util.Warning("DCC command %s from %s timed out", command, ownerNick)
		}
	}()
}

// IsKnownCommand checks if a command is registered in the executor
func (a *DCCAdapter) IsKnownCommand(cmd string) bool {
	a.executor.mutex.RLock()
	defer a.executor.mutex.RUnlock()

	_, exists := a.executor.handlers[strings.ToUpper(cmd)]
	return exists
}

// SendPartyLineMessage sends a message to the party line
// This will be implemented in Phase 2 as part of the party line refactoring
func (a *DCCAdapter) SendPartyLineMessage(
	message string,
	sender string,
	excludeSessionID string,
) {
	// Will be implemented in Phase 2
	util.Debug("PartyLine: Message from %s: %s", sender, message)
}

// Initialize ensures the command system is initialized and standard handlers are registered
func (a *DCCAdapter) Initialize() {
	RegisterStandardHandlers(a.executor)
}
