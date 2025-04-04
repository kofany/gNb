package command

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/kofany/gNb/internal/types"
	"github.com/kofany/gNb/internal/util"
)

var (
	// Singleton integration instance
	integrationInstance *Integration
	integrationOnce     sync.Once
)

// Integration is the main integration point for the command system
type Integration struct {
	executor *CommandExecutor
	mutex    sync.RWMutex
}

// GetIntegration returns the singleton integration instance
func GetIntegration() *Integration {
	integrationOnce.Do(func() {
		integrationInstance = &Integration{
			executor: GetExecutor(),
		}

		// Ensure the executor is started
		integrationInstance.executor.Start()

		// Register standard handlers
		RegisterStandardHandlers(integrationInstance.executor)

		util.Info("Command system integration initialized")
	})

	return integrationInstance
}

// ExecuteSystemCommand executes a system command without user interaction
func (i *Integration) ExecuteSystemCommand(
	command string,
	args []string,
	bot types.Bot,
	maxWaitTime time.Duration,
) (string, error) {
	// Create a context with timeout
	ctx, cancel := context.WithTimeout(context.Background(), maxWaitTime)
	defer cancel()

	// Execute the command
	resultChan := i.executor.ExecuteCommand(
		ctx,
		command,
		args,
		bot,
		"system",
		SystemCommand,
		HighPriority,
	)

	// Wait for the result
	select {
	case result := <-resultChan:
		if result.Error != nil {
			return "", result.Error
		}
		return result.Output, nil
	case <-ctx.Done():
		return "", fmt.Errorf("command execution timed out")
	}
}

// AddCustomHandler adds a custom command handler
func (i *Integration) AddCustomHandler(command string, handler CommandHandler) {
	i.executor.RegisterHandler(command, handler)
}

// GetCommandStats returns stats for a specific command
func (i *Integration) GetCommandStats(command string) *CommandStats {
	i.mutex.RLock()
	defer i.mutex.RUnlock()

	return i.executor.GetCommandStats(command)
}

// GetAllCommandStats returns stats for all commands
func (i *Integration) GetAllCommandStats() map[string]*CommandStats {
	i.mutex.RLock()
	defer i.mutex.RUnlock()

	// Make a copy to avoid concurrent access issues
	statsCopy := make(map[string]*CommandStats)

	for cmd, stats := range i.executor.stats {
		if stats != nil {
			// Deep copy the stats
			statsCopy[cmd] = &CommandStats{
				TotalExecutions:      stats.TotalExecutions,
				SuccessfulExecutions: stats.SuccessfulExecutions,
				FailedExecutions:     stats.FailedExecutions,
				TotalExecutionTime:   stats.TotalExecutionTime,
			}
		}
	}

	return statsCopy
}

// Shutdown cleanly shuts down the command system
func (i *Integration) Shutdown() {
	util.Info("Shutting down command system integration")
	i.executor.Stop()
}
