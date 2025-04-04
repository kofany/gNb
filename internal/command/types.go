package command

import (
	"context"
	"time"

	"github.com/kofany/gNb/internal/types"
)

// CommandType represents the type of command
type CommandType int

const (
	// UserCommand is a command from a user
	UserCommand CommandType = iota
	// SystemCommand is a command from the system
	SystemCommand
	// PartyLineCommand is a command from the party line
	PartyLineCommand
)

// CommandRequest represents a request to execute a command
type CommandRequest struct {
	// Command is the command to execute (without the leading dot)
	Command string
	// Args are the arguments to the command
	Args []string
	// Bot is the bot that received the command
	Bot types.Bot
	// SourceID is the ID of the source (user nick, session ID, etc.)
	SourceID string
	// Type is the type of command
	Type CommandType
	// ResultChan is a channel to send the result back
	ResultChan chan<- *CommandResult
	// Context is the context for the command execution
	Context context.Context
	// Timestamp is when the command was created
	Timestamp time.Time
}

// CommandResult represents the result of executing a command
type CommandResult struct {
	// Output is the output of the command
	Output string
	// Error is any error that occurred
	Error error
	// Handled indicates if the command was handled
	Handled bool
	// Duration is how long the command took to execute
	Duration time.Duration
}

// CommandHandler is a function that handles a command
type CommandHandler func(*CommandRequest) *CommandResult

// CommandPriority represents the priority of a command
type CommandPriority int

const (
	// LowPriority is for non-essential commands
	LowPriority CommandPriority = iota
	// NormalPriority is for regular commands
	NormalPriority
	// HighPriority is for important commands
	HighPriority
	// CriticalPriority is for critical commands
	CriticalPriority
)

// CommandStats tracks performance and health of commands
type CommandStats struct {
	// TotalExecutions is the number of times the command has been executed
	TotalExecutions int64
	// SuccessfulExecutions is the number of successful executions
	SuccessfulExecutions int64
	// FailedExecutions is the number of failed executions
	FailedExecutions int64
	// TimeoutExecutions is the number of executions that timed out
	TimeoutExecutions int64
	// TotalExecutionTime is the total time spent executing the command
	TotalExecutionTime time.Duration
	// AverageExecutionTime is the average time spent executing the command
	AverageExecutionTime time.Duration
	// LastExecutionTime is when the command was last executed
	LastExecutionTime time.Time
	// LastExecutionDuration is how long the last execution took
	LastExecutionDuration time.Duration
	// LastError is the last error that occurred
	LastError error
	// CircuitBreakerTripped indicates if the circuit breaker is tripped
	CircuitBreakerTripped bool
	// CircuitBreakerTrippedAt is when the circuit breaker was tripped
	CircuitBreakerTrippedAt time.Time
}
