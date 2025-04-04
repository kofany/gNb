package command

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/kofany/gNb/internal/types"
	"github.com/kofany/gNb/internal/util"
)

// CommandExecutor is a service that executes commands
type CommandExecutor struct {
	// handlers maps command names to their handlers
	handlers map[string]CommandHandler
	// stats maps command names to their stats
	stats map[string]*CommandStats
	// priorityQueues are queues for commands of different priorities
	priorityQueues map[CommandPriority]chan *CommandRequest
	// commandTimeout is the default timeout for commands
	commandTimeout time.Duration
	// circuitBreakerThreshold is the number of consecutive failures before tripping
	circuitBreakerThreshold int
	// circuitBreakerCooldown is the time to wait before resetting the circuit breaker
	circuitBreakerCooldown time.Duration
	// workerCount is the number of worker goroutines
	workerCount int
	// shutdownChan is closed to signal shutdown
	shutdownChan chan struct{}
	// wg is used to wait for all goroutines to finish
	wg sync.WaitGroup
	// mutex protects access to maps
	mutex sync.RWMutex
	// active indicates if the executor is active
	active atomic.Bool
}

var (
	// singleton instance of the CommandExecutor
	instance *CommandExecutor
	// singleton mutex
	once sync.Once
)

// DefaultCommandTimeout is the default timeout for commands
const DefaultCommandTimeout = 30 * time.Second

// DefaultCircuitBreakerThreshold is the default threshold for circuit breakers
const DefaultCircuitBreakerThreshold = 5

// DefaultCircuitBreakerCooldown is the default cooldown for circuit breakers
const DefaultCircuitBreakerCooldown = 5 * time.Minute

// DefaultWorkerCount is the default number of worker goroutines
const DefaultWorkerCount = 5

// GetExecutor returns the singleton instance of the CommandExecutor
func GetExecutor() *CommandExecutor {
	once.Do(func() {
		instance = NewCommandExecutor(
			DefaultCommandTimeout,
			DefaultCircuitBreakerThreshold,
			DefaultCircuitBreakerCooldown,
			DefaultWorkerCount,
		)
		instance.Start()
	})
	return instance
}

// NewCommandExecutor creates a new CommandExecutor
func NewCommandExecutor(
	commandTimeout time.Duration,
	circuitBreakerThreshold int,
	circuitBreakerCooldown time.Duration,
	workerCount int,
) *CommandExecutor {
	executor := &CommandExecutor{
		handlers:                make(map[string]CommandHandler),
		stats:                   make(map[string]*CommandStats),
		priorityQueues:          make(map[CommandPriority]chan *CommandRequest),
		commandTimeout:          commandTimeout,
		circuitBreakerThreshold: circuitBreakerThreshold,
		circuitBreakerCooldown:  circuitBreakerCooldown,
		workerCount:             workerCount,
		shutdownChan:            make(chan struct{}),
	}

	// Initialize priority queues
	executor.priorityQueues[CriticalPriority] = make(chan *CommandRequest, 100)
	executor.priorityQueues[HighPriority] = make(chan *CommandRequest, 100)
	executor.priorityQueues[NormalPriority] = make(chan *CommandRequest, 500)
	executor.priorityQueues[LowPriority] = make(chan *CommandRequest, 1000)

	return executor
}

// RegisterHandler registers a command handler
func (e *CommandExecutor) RegisterHandler(command string, handler CommandHandler) {
	e.mutex.Lock()
	defer e.mutex.Unlock()

	command = strings.ToUpper(command)
	e.handlers[command] = handler

	// Initialize stats for the command if they don't exist
	if _, exists := e.stats[command]; !exists {
		e.stats[command] = &CommandStats{
			TotalExecutions:      0,
			SuccessfulExecutions: 0,
			FailedExecutions:     0,
			TotalExecutionTime:   0,
		}
	}
}

// Start starts the command executor workers
func (e *CommandExecutor) Start() {
	if e.active.Load() {
		return
	}

	e.active.Store(true)
	util.Info("CommandExecutor: Starting %d workers", e.workerCount)

	// Start worker goroutines - one set per priority level
	for priority := range e.priorityQueues {
		for i := 0; i < e.workerCount; i++ {
			e.wg.Add(1)
			go e.worker(priority)
		}
	}
}

// Stop stops the command executor
func (e *CommandExecutor) Stop() {
	if !e.active.Load() {
		return
	}

	util.Info("CommandExecutor: Stopping")
	close(e.shutdownChan)
	e.wg.Wait()
	e.active.Store(false)
}

// ExecuteCommand submits a command for execution and returns immediately
func (e *CommandExecutor) ExecuteCommand(
	ctx context.Context,
	command string,
	args []string,
	bot types.Bot,
	sourceID string,
	cmdType CommandType,
	priority CommandPriority,
) chan *CommandResult {
	resultChan := make(chan *CommandResult, 1)

	// Skip if executor is not active
	if !e.active.Load() {
		resultChan <- &CommandResult{
			Error:   fmt.Errorf("command executor is not active"),
			Handled: false,
		}
		close(resultChan)
		return resultChan
	}

	// Create a timeout context if not provided
	if ctx == nil {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(context.Background(), e.commandTimeout)
		go func() {
			<-ctx.Done()
			cancel()
		}()
	}

	// Create the command request
	request := &CommandRequest{
		Command:    command,
		Args:       args,
		Bot:        bot,
		SourceID:   sourceID,
		Type:       cmdType,
		ResultChan: resultChan,
		Context:    ctx,
		Timestamp:  time.Now(),
	}

	// Put the request in the appropriate queue based on priority
	select {
	case e.priorityQueues[priority] <- request:
		util.Debug("CommandExecutor: Queued command %s with priority %d from %s",
			command, priority, sourceID)
	case <-ctx.Done():
		// Context was canceled while trying to queue
		resultChan <- &CommandResult{
			Error:   ctx.Err(),
			Handled: false,
		}
		close(resultChan)
		util.Warning("CommandExecutor: Command %s timed out while queuing", command)
	case <-time.After(5 * time.Second):
		// Queue is full or blocked for too long
		resultChan <- &CommandResult{
			Error:   fmt.Errorf("command queue is full or blocked"),
			Handled: false,
		}
		close(resultChan)
		util.Warning("CommandExecutor: Queue full or blocked for command %s", command)
	}

	return resultChan
}

// worker processes commands from a specific priority queue
func (e *CommandExecutor) worker(priority CommandPriority) {
	defer e.wg.Done()

	queue := e.priorityQueues[priority]
	util.Debug("CommandExecutor: Worker started for priority %d", priority)

	for {
		select {
		case <-e.shutdownChan:
			util.Debug("CommandExecutor: Worker for priority %d shutting down", priority)
			return
		case request := <-queue:
			e.processCommand(request)
		}
	}
}

// processCommand processes a single command request
func (e *CommandExecutor) processCommand(request *CommandRequest) {
	result := &CommandResult{
		Handled: false,
	}

	// Skip if the context is already done
	select {
	case <-request.Context.Done():
		result.Error = request.Context.Err()
		util.Warning("CommandExecutor: Command %s context already done: %v",
			request.Command, result.Error)
		request.ResultChan <- result
		close(request.ResultChan)
		return
	default:
		// Continue processing
	}

	// Check if we have a handler for this command
	e.mutex.RLock()
	handler, exists := e.handlers[request.Command]
	stats, statsExist := e.stats[request.Command]
	e.mutex.RUnlock()

	if !exists {
		result.Error = fmt.Errorf("no handler registered for command: %s", request.Command)
		util.Warning("CommandExecutor: %v", result.Error)
		request.ResultChan <- result
		close(request.ResultChan)
		return
	}

	// Check if circuit breaker is tripped
	if statsExist && stats.CircuitBreakerTripped {
		// Check if cooldown has passed
		if time.Since(stats.CircuitBreakerTrippedAt) > e.circuitBreakerCooldown {
			// Reset circuit breaker
			e.mutex.Lock()
			stats.CircuitBreakerTripped = false
			e.mutex.Unlock()
			util.Info("CommandExecutor: Reset circuit breaker for command %s after cooldown",
				request.Command)
		} else {
			// Circuit breaker still active
			result.Error = fmt.Errorf("command %s is temporarily disabled due to previous failures",
				request.Command)
			util.Warning("CommandExecutor: %v", result.Error)
			request.ResultChan <- result
			close(request.ResultChan)
			return
		}
	}

	// Execute the command with panic recovery
	startTime := time.Now()
	cmdComplete := make(chan struct{})

	go func() {
		defer func() {
			if r := recover(); r != nil {
				result.Error = fmt.Errorf("panic in command handler: %v", r)
				util.Error("CommandExecutor: %v", result.Error)
			}
			close(cmdComplete)
		}()

		// Execute the handler
		handlerResult := handler(request)
		if handlerResult != nil {
			*result = *handlerResult
		}

		// Update successful result
		result.Handled = true
	}()

	// Wait for command to complete or timeout
	select {
	case <-cmdComplete:
		// Command completed normally
		execDuration := time.Since(startTime)
		result.Duration = execDuration

		// Update stats
		e.updateStats(request.Command, result.Error == nil, execDuration, result.Error)

		util.Debug("CommandExecutor: Command %s from %s completed in %v",
			request.Command, request.SourceID, execDuration)

	case <-request.Context.Done():
		// Command timed out or was canceled
		result.Error = request.Context.Err()
		result.Duration = time.Since(startTime)

		// Update timeout stats
		e.updateTimeoutStats(request.Command, result.Duration)

		util.Warning("CommandExecutor: Command %s from %s timed out after %v",
			request.Command, request.SourceID, result.Duration)
	}

	// Send result and close channel
	request.ResultChan <- result
	close(request.ResultChan)
}

// updateStats updates the statistics for a command
func (e *CommandExecutor) updateStats(command string, success bool, duration time.Duration, err error) {
	e.mutex.Lock()
	defer e.mutex.Unlock()

	stats, exists := e.stats[command]
	if !exists {
		return
	}

	// Update basic stats
	stats.TotalExecutions++
	stats.TotalExecutionTime += duration
	stats.AverageExecutionTime = stats.TotalExecutionTime / time.Duration(stats.TotalExecutions)
	stats.LastExecutionTime = time.Now()
	stats.LastExecutionDuration = duration

	if success {
		// Successful execution
		stats.SuccessfulExecutions++
		// Reset consecutive failures if this was a success
		if stats.CircuitBreakerTripped && stats.SuccessfulExecutions > int64(e.circuitBreakerThreshold) {
			stats.CircuitBreakerTripped = false
			util.Info("CommandExecutor: Reset circuit breaker for command %s after %d consecutive successes",
				command, e.circuitBreakerThreshold)
		}
	} else {
		// Failed execution
		stats.FailedExecutions++
		stats.LastError = err

		// Check if we should trip the circuit breaker
		if stats.FailedExecutions >= int64(e.circuitBreakerThreshold) {
			stats.CircuitBreakerTripped = true
			stats.CircuitBreakerTrippedAt = time.Now()
			util.Warning("CommandExecutor: Tripped circuit breaker for command %s after %d consecutive failures",
				command, e.circuitBreakerThreshold)
		}
	}
}

// updateTimeoutStats updates timeout statistics for a command
func (e *CommandExecutor) updateTimeoutStats(command string, duration time.Duration) {
	e.mutex.Lock()
	defer e.mutex.Unlock()

	stats, exists := e.stats[command]
	if !exists {
		return
	}

	// Update timeout stats
	stats.TimeoutExecutions++
	stats.TotalExecutions++
	stats.FailedExecutions++
	stats.TotalExecutionTime += duration
	stats.AverageExecutionTime = stats.TotalExecutionTime / time.Duration(stats.TotalExecutions)
	stats.LastExecutionTime = time.Now()
	stats.LastExecutionDuration = duration
	stats.LastError = fmt.Errorf("command timed out after %v", duration)

	// Check if we should trip the circuit breaker
	if stats.TimeoutExecutions >= int64(e.circuitBreakerThreshold) {
		stats.CircuitBreakerTripped = true
		stats.CircuitBreakerTrippedAt = time.Now()
		util.Warning("CommandExecutor: Tripped circuit breaker for command %s after %d consecutive timeouts",
			command, e.circuitBreakerThreshold)
	}
}

// GetCommandStats returns stats for a specific command
func (e *CommandExecutor) GetCommandStats(command string) *CommandStats {
	e.mutex.RLock()
	defer e.mutex.RUnlock()

	command = strings.ToUpper(command)
	if stats, exists := e.stats[command]; exists {
		// Return a copy to avoid concurrent modification issues
		return &CommandStats{
			TotalExecutions:      stats.TotalExecutions,
			SuccessfulExecutions: stats.SuccessfulExecutions,
			FailedExecutions:     stats.FailedExecutions,
			TotalExecutionTime:   stats.TotalExecutionTime,
		}
	}

	return nil
}

// ResetCircuitBreaker manually resets the circuit breaker for a command
func (e *CommandExecutor) ResetCircuitBreaker(command string) bool {
	e.mutex.Lock()
	defer e.mutex.Unlock()

	if stats, exists := e.stats[command]; exists && stats.CircuitBreakerTripped {
		stats.CircuitBreakerTripped = false
		util.Info("CommandExecutor: Manually reset circuit breaker for command %s", command)
		return true
	}

	return false
}
