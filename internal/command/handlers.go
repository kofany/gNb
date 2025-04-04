package command

import (
	"fmt"
	"strings"
)

// RegisterStandardHandlers registers the standard handlers for common commands
func RegisterStandardHandlers(executor *CommandExecutor) {
	// Basic commands
	executor.RegisterHandler("HELP", handleHelpCommand)
	executor.RegisterHandler("INFO", handleInfoCommand)

	// List commands
	executor.RegisterHandler("LISTOWNERS", handleListOwnersCommand)
	executor.RegisterHandler("LISTNICKS", handleListNicksCommand)
	executor.RegisterHandler("BOTS", handleBotsCommand)
	executor.RegisterHandler("SERVERS", handleServersCommand)

	// User management commands
	executor.RegisterHandler("ADDOWNER", handleAddOwnerCommand)
	executor.RegisterHandler("DELOWNER", handleDelOwnerCommand)

	// Nick management commands
	executor.RegisterHandler("ADDNICK", handleAddNickCommand)
	executor.RegisterHandler("DELNICK", handleDelNickCommand)

	// IRC commands
	executor.RegisterHandler("MSG", handleMsgCommand)
	executor.RegisterHandler("JOIN", handleJoinCommand)
	executor.RegisterHandler("PART", handlePartCommand)
	executor.RegisterHandler("KICK", handleKickCommand)
	executor.RegisterHandler("MODE", handleModeCommand)
	executor.RegisterHandler("NICK", handleNickCommand)
	executor.RegisterHandler("RAW", handleRawCommand)
	executor.RegisterHandler("QUIT", handleQuitCommand)

	// Mass commands
	executor.RegisterHandler("MJOIN", handleMassJoinCommand)
	executor.RegisterHandler("MPART", handleMassPartCommand)
	executor.RegisterHandler("MRECONNECT", handleMassReconnectCommand)
}

// handleListOwnersCommand handles the LISTOWNERS command
func handleListOwnersCommand(req *CommandRequest) *CommandResult {
	result := &CommandResult{
		Handled: false,
	}

	if bm := req.Bot.GetBotManager(); bm != nil {
		// Get owners directly without extra goroutines to avoid potential deadlocks
		owners := bm.GetOwners()
		result.Output = fmt.Sprintf("Current owners: %s", strings.Join(owners, ", "))
		result.Handled = true
	} else {
		result.Error = fmt.Errorf("bot manager is not available")
	}

	return result
}

// handleListNicksCommand handles the LISTNICKS command
func handleListNicksCommand(req *CommandRequest) *CommandResult {
	result := &CommandResult{
		Handled: false,
	}

	if nm := req.Bot.GetNickManager(); nm != nil {
		nicks := nm.GetNicks()
		result.Output = fmt.Sprintf("Monitored nicks: %s", strings.Join(nicks, ", "))
		result.Handled = true
	} else {
		result.Error = fmt.Errorf("nick manager is not available")
	}

	return result
}

// handleBotsCommand handles the BOTS command
func handleBotsCommand(req *CommandRequest) *CommandResult {
	result := &CommandResult{
		Handled: false,
	}

	if bm := req.Bot.GetBotManager(); bm != nil {
		bots := bm.GetBots()
		totalCreatedBots := bm.GetTotalCreatedBots()
		totalBotsNow := len(bots)

		// Count connected bots with a simplified approach
		var connectedBotNicks []string
		totalConnectedBots := 0

		// Check connection status with a simple loop - no goroutines
		for _, bot := range bots {
			if bot.IsConnected() {
				connectedBotNicks = append(connectedBotNicks, bot.GetCurrentNick())
				totalConnectedBots++
			}
		}

		// Format the response based on the arguments
		if len(req.Args) == 0 {
			result.Output = fmt.Sprintf(
				"Total created bots: %d\nTotal bots now: %d\nTotal fully connected bots: %d",
				totalCreatedBots, totalBotsNow, totalConnectedBots)
			result.Handled = true
		} else if len(req.Args) == 1 && strings.ToLower(req.Args[0]) == "n" {
			if totalConnectedBots == 0 {
				result.Output = "No bots are currently connected."
			} else {
				result.Output = "Connected bots: " + strings.Join(connectedBotNicks, ", ")
			}
			result.Handled = true
		} else {
			result.Output = "Usage: .bots or .bots n"
			result.Handled = true
		}
	} else {
		result.Error = fmt.Errorf("bot manager is not available")
	}

	return result
}

// handleServersCommand handles the SERVERS command
func handleServersCommand(req *CommandRequest) *CommandResult {
	result := &CommandResult{
		Handled: false,
	}

	if bm := req.Bot.GetBotManager(); bm != nil {
		bots := bm.GetBots()

		// Use a map to deduplicate servers
		servers := make(map[string][]string)

		// Check each bot's server with a simple loop - no goroutines
		for _, bot := range bots {
			if bot.IsConnected() {
				serverName := bot.GetServerName()
				if serverName != "" {
					servers[serverName] = append(servers[serverName], bot.GetCurrentNick())
				}
			}
		}

		// Format the response
		var output strings.Builder
		output.WriteString("Active servers:\n")

		if len(servers) == 0 {
			output.WriteString("No active servers found.")
		} else {
			for server, botNicks := range servers {
				output.WriteString(fmt.Sprintf("%s (%d bots): %s\n",
					server, len(botNicks), strings.Join(botNicks, ", ")))
			}
		}

		result.Output = output.String()
		result.Handled = true
	} else {
		result.Error = fmt.Errorf("bot manager is not available")
	}

	return result
}

// Add placeholder implementations for the remaining handlers
// These will be implemented in subsequent phases

func handleHelpCommand(req *CommandRequest) *CommandResult {
	// Will be fully implemented in Phase 2
	return &CommandResult{
		Output:  "Help command will be implemented in Phase 2",
		Handled: true,
	}
}

func handleInfoCommand(req *CommandRequest) *CommandResult {
	// Will be fully implemented in Phase 2
	return &CommandResult{
		Output:  "Info command will be implemented in Phase 2",
		Handled: true,
	}
}

func handleAddOwnerCommand(req *CommandRequest) *CommandResult {
	// Will be fully implemented in Phase 2
	return &CommandResult{
		Output:  "AddOwner command will be implemented in Phase 2",
		Handled: true,
	}
}

func handleDelOwnerCommand(req *CommandRequest) *CommandResult {
	// Will be fully implemented in Phase 2
	return &CommandResult{
		Output:  "DelOwner command will be implemented in Phase 2",
		Handled: true,
	}
}

func handleAddNickCommand(req *CommandRequest) *CommandResult {
	// Will be fully implemented in Phase 2
	return &CommandResult{
		Output:  "AddNick command will be implemented in Phase 2",
		Handled: true,
	}
}

func handleDelNickCommand(req *CommandRequest) *CommandResult {
	// Will be fully implemented in Phase 2
	return &CommandResult{
		Output:  "DelNick command will be implemented in Phase 2",
		Handled: true,
	}
}

func handleMsgCommand(req *CommandRequest) *CommandResult {
	// Will be fully implemented in Phase 2
	return &CommandResult{
		Output:  "Msg command will be implemented in Phase 2",
		Handled: true,
	}
}

func handleJoinCommand(req *CommandRequest) *CommandResult {
	// Will be fully implemented in Phase 2
	return &CommandResult{
		Output:  "Join command will be implemented in Phase 2",
		Handled: true,
	}
}

func handlePartCommand(req *CommandRequest) *CommandResult {
	// Will be fully implemented in Phase 2
	return &CommandResult{
		Output:  "Part command will be implemented in Phase 2",
		Handled: true,
	}
}

func handleKickCommand(req *CommandRequest) *CommandResult {
	// Will be fully implemented in Phase 2
	return &CommandResult{
		Output:  "Kick command will be implemented in Phase 2",
		Handled: true,
	}
}

func handleModeCommand(req *CommandRequest) *CommandResult {
	// Will be fully implemented in Phase 2
	return &CommandResult{
		Output:  "Mode command will be implemented in Phase 2",
		Handled: true,
	}
}

func handleNickCommand(req *CommandRequest) *CommandResult {
	// Will be fully implemented in Phase 2
	return &CommandResult{
		Output:  "Nick command will be implemented in Phase 2",
		Handled: true,
	}
}

func handleRawCommand(req *CommandRequest) *CommandResult {
	// Will be fully implemented in Phase 2
	return &CommandResult{
		Output:  "Raw command will be implemented in Phase 2",
		Handled: true,
	}
}

func handleQuitCommand(req *CommandRequest) *CommandResult {
	// Will be fully implemented in Phase 2
	return &CommandResult{
		Output:  "Quit command will be implemented in Phase 2",
		Handled: true,
	}
}

func handleMassJoinCommand(req *CommandRequest) *CommandResult {
	// Will be fully implemented in Phase 2
	return &CommandResult{
		Output:  "MassJoin command will be implemented in Phase 2",
		Handled: true,
	}
}

func handleMassPartCommand(req *CommandRequest) *CommandResult {
	// Will be fully implemented in Phase 2
	return &CommandResult{
		Output:  "MassPart command will be implemented in Phase 2",
		Handled: true,
	}
}

func handleMassReconnectCommand(req *CommandRequest) *CommandResult {
	// Will be fully implemented in Phase 2
	return &CommandResult{
		Output:  "MassReconnect command will be implemented in Phase 2",
		Handled: true,
	}
}
