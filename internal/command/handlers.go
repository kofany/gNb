package command

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/kofany/gNb/internal/types"
	"github.com/kofany/gNb/internal/util"
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
	// Create a timeout context for getting the owners
	ctx, cancel := req.Context, func() {}
	if req.Context == nil {
		var cancelFunc func()
		ctx, cancelFunc = context.WithTimeout(context.Background(), 5*time.Second)
		cancel = cancelFunc
	}
	defer cancel()

	result := &CommandResult{
		Handled: false,
	}

	if bm := req.Bot.GetBotManager(); bm != nil {
		// Get owners with timeout
		ownerChan := make(chan []string, 1)
		errChan := make(chan error, 1)

		go func() {
			defer func() {
				if r := recover(); r != nil {
					errChan <- fmt.Errorf("panic while getting owners: %v", r)
				}
			}()

			owners := bm.GetOwners()
			select {
			case <-ctx.Done():
				// Context was canceled, don't try to send
				return
			default:
				ownerChan <- owners
			}
		}()

		// Wait for result or timeout
		select {
		case owners := <-ownerChan:
			result.Output = fmt.Sprintf("Current owners: %s", strings.Join(owners, ", "))
			result.Handled = true
		case err := <-errChan:
			result.Error = err
		case <-ctx.Done():
			result.Error = ctx.Err()
			util.Warning("Command listowners timed out")
		}
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

	// Use a timeout context for checking bot connections
	ctx, cancel := context.WithTimeout(req.Context, 5*time.Second)
	defer cancel()

	if bm := req.Bot.GetBotManager(); bm != nil {
		bots := bm.GetBots()
		totalCreatedBots := bm.GetTotalCreatedBots()
		totalBotsNow := len(bots)

		// Count connected bots with timeout protection
		var connectedBotNicks []string
		totalConnectedBots := 0

		// Use parallel checking with timeouts
		connectedChan := make(chan string, len(bots))
		doneChan := make(chan struct{})

		go func() {
			for _, bot := range bots {
				go func(b types.Bot) {
					// Use a short timeout for each check
					checkCtx, checkCancel := context.WithTimeout(ctx, 1*time.Second)
					defer checkCancel()

					// Run the check in a separate goroutine
					checkDone := make(chan bool, 1)
					go func() {
						checkDone <- b.IsConnected()
					}()

					// Wait for the check to complete or timeout
					select {
					case isConnected := <-checkDone:
						if isConnected {
							connectedChan <- b.GetCurrentNick()
						}
					case <-checkCtx.Done():
						// Timeout, skip this bot
						util.Debug("Command bots: Timeout checking connection for bot %s", b.GetCurrentNick())
					}
				}(bot)
			}

			// Wait a bit for all checks to complete
			select {
			case <-time.After(3 * time.Second):
				// Continue with what we have
			case <-ctx.Done():
				// Timeout occurred, continue with what we have
			}

			close(doneChan)
		}()

		// Collect results
		collectDone := make(chan struct{})
		go func() {
			defer close(collectDone)
			for {
				select {
				case nick, ok := <-connectedChan:
					if !ok {
						return
					}
					connectedBotNicks = append(connectedBotNicks, nick)
					totalConnectedBots++
				case <-doneChan:
					// Stop collecting when doneChan is closed
					close(connectedChan)
				case <-ctx.Done():
					// Timeout occurred
					return
				}
			}
		}()

		// Wait for collection to complete or timeout
		select {
		case <-collectDone:
			// Collection completed
		case <-ctx.Done():
			// Timeout occurred, continue with what we have
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

	// Use a timeout context for checking servers
	ctx, cancel := context.WithTimeout(req.Context, 5*time.Second)
	defer cancel()

	if bm := req.Bot.GetBotManager(); bm != nil {
		bots := bm.GetBots()

		// Use a map to deduplicate servers
		servers := make(map[string][]string)

		// Check each bot's server with timeout protection
		for _, bot := range bots {
			go func(b types.Bot) {
				if b.IsConnected() {
					serverName := b.GetServerName()
					if serverName != "" {
						// Lock-free approach using channels
						select {
						case <-ctx.Done():
							// Timeout occurred, skip this bot
							return
						default:
							// Add the bot to the server's list
							servers[serverName] = append(servers[serverName], b.GetCurrentNick())
						}
					}
				}
			}(bot)
		}

		// Give some time for the goroutines to complete
		select {
		case <-time.After(3 * time.Second):
			// Continue with what we have
		case <-ctx.Done():
			// Timeout occurred, continue with what we have
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
