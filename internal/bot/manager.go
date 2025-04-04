package bot

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/kofany/gNb/internal/auth"
	"github.com/kofany/gNb/internal/config"
	"github.com/kofany/gNb/internal/nickmanager"
	"github.com/kofany/gNb/internal/types"
	"github.com/kofany/gNb/internal/util"
)

// Ensure BotManager implements types.BotManager
var _ types.BotManager = (*BotManager)(nil)

// ChannelCommand represents a command issued on a channel
type ChannelCommand struct {
	Sender    string    // The nick of the sender
	Channel   string    // The channel where the command was issued
	Command   string    // The command name
	Args      []string  // The command arguments
	Timestamp time.Time // When the command was issued
	Handled   bool      // Whether the command has been handled
}

// BotManager manages multiple IRC bots
type BotManager struct {
	bots                []types.Bot
	owners              auth.OwnerList
	wg                  sync.WaitGroup
	stopChan            chan struct{}
	nickManager         types.NickManager
	commandBotIndex     int
	mutex               sync.RWMutex
	lastMassCommand     map[string]time.Time
	massCommandCooldown time.Duration
	wordPool            []string
	wordPoolMutex       sync.Mutex
	reactionRequests    map[string]types.ReactionRequest
	reactionMutex       sync.Mutex
	ctx                 context.Context
	cancel              context.CancelFunc
	totalCreatedBots    int
	startTime           time.Time

	// New fields for command coordination
	channelCommands map[string]*ChannelCommand // Map of command IDs to commands
	commandMutex    sync.Mutex                 // Mutex for channelCommands
}

// NewBotManager creates a new BotManager instance
func NewBotManager(cfg *config.Config, owners auth.OwnerList, nm types.NickManager) *BotManager {
	ctx, cancel := context.WithCancel(context.Background())
	requiredWords := len(cfg.Bots)*3 + 10 // 3 words per bot (nick, ident, realname) + 10 spare

	wordPool, err := util.GetWordsFromAPI(
		cfg.Global.NickAPI.URL,
		cfg.Global.NickAPI.MaxWordLength,
		cfg.Global.NickAPI.Timeout,
		requiredWords,
	)

	if err != nil {
		util.Error("Failed to get words from API: %v", err)
		wordPool = make([]string, requiredWords)
		for i := range wordPool {
			wordPool[i] = util.GenerateFallbackNick()
		}
	}

	manager := &BotManager{
		bots:                make([]types.Bot, len(cfg.Bots)),
		totalCreatedBots:    len(cfg.Bots),
		owners:              owners,
		stopChan:            make(chan struct{}),
		nickManager:         nm,
		lastMassCommand:     make(map[string]time.Time),
		massCommandCooldown: time.Duration(cfg.Global.MassCommandCooldown) * time.Second,
		wordPool:            wordPool,
		wordPoolMutex:       sync.Mutex{},
		reactionRequests:    make(map[string]types.ReactionRequest),
		ctx:                 ctx,
		cancel:              cancel,
		startTime:           time.Now(),
		channelCommands:     make(map[string]*ChannelCommand),
	}

	// Creating bots
	for i, botCfg := range cfg.Bots {
		bot := NewBot(&botCfg, &cfg.Global, nm, manager)
		bot.SetOwnerList(manager.owners)
		bot.SetChannels(cfg.Channels)
		bot.SetBotManager(manager)
		bot.SetNickManager(nm)
		manager.bots[i] = bot
		util.Debug("BotManager added bot %s", bot.GetCurrentNick())
	}

	nm.SetBots(manager.bots)
	go manager.cleanupDisconnectedBots()
	return manager
}

func (bm *BotManager) cleanupDisconnectedBots() {
	// Czekamy 240 sekund od startu
	time.Sleep(240 * time.Second)

	bm.mutex.Lock()
	defer bm.mutex.Unlock()

	util.Info("Starting cleanup of disconnected bots after startup grace period")

	// Tworzymy nową listę tylko z połączonymi botami
	connectedBots := make([]types.Bot, 0)
	removedCount := 0

	for _, bot := range bm.bots {
		if bot.IsConnected() {
			connectedBots = append(connectedBots, bot)
		} else {
			// Logujemy informację o usuwanym bocie
			util.Warning("Removing bot %s due to connection failure after startup period", bot.GetCurrentNick())

			// Zwalniamy zasoby bota
			bot.Quit("Cleanup - connection failure")

			// Jeśli bot miał przypisany nick do złapania, zwracamy go do puli
			if bm.nickManager != nil {
				currentNick := bot.GetCurrentNick()
				bm.nickManager.ReturnNickToPool(currentNick)
				nm := bm.nickManager.(*nickmanager.NickManager)
				nm.UnregisterBot(bot)
			}

			removedCount++
		}
	}

	// Aktualizujemy listę botów
	bm.bots = connectedBots

	// Aktualizujemy indeks dla komend jeśli jest potrzeba
	if bm.commandBotIndex >= len(bm.bots) {
		bm.commandBotIndex = 0
	}

	// Logujemy podsumowanie
	if removedCount > 0 {
		util.Info("Cleanup completed: removed %d disconnected bots, %d bots remaining",
			removedCount, len(bm.bots))
	} else {
		util.Info("Cleanup completed: all bots are connected")
	}

	// Aktualizujemy totalCreatedBots o liczbę usuniętych botów
	bm.totalCreatedBots -= removedCount
}

func (bm *BotManager) StartBots() {
	var wg sync.WaitGroup
	connectedBots := make([]types.Bot, 0)
	var connectedBotsMutex sync.Mutex

	// Startujemy wszystkie boty asynchronicznie
	for _, bot := range bm.bots {
		wg.Add(1)
		go func(bot types.Bot) {
			defer wg.Done()

			// Kanał do monitorowania timeout
			timeoutChan := time.After(180 * time.Second)
			connectChan := make(chan bool)

			// Startujemy proces łączenia w osobnej goroutynie
			go func() {
				success := bm.startBotWithRetry(bot)
				connectChan <- success
			}()

			// Czekamy na rezultat z timeoutem
			select {
			case success := <-connectChan:
				if success {
					connectedBotsMutex.Lock()
					connectedBots = append(connectedBots, bot)
					connectedBotsMutex.Unlock()
				}
			case <-timeoutChan:
				// Bot nie połączył się w wymaganym czasie
				util.Warning("Bot %s failed to connect within timeout, removing", bot.GetCurrentNick())
				bot.Quit("Connection timeout")
			}
		}(bot)
	}

	// Czekamy na zakończenie wszystkich prób połączeń
	wg.Wait()

	// Aktualizujemy listę botów tylko o te połączone
	bm.mutex.Lock()
	bm.bots = connectedBots
	bm.mutex.Unlock()
}

func (bm *BotManager) startBotWithRetry(bot types.Bot) bool {
	retryInterval := 5 * time.Second
	maxTime := 120 * time.Second
	startTime := time.Now()

	for {
		if time.Since(startTime) > maxTime {
			return false
		}

		err := bot.Connect()
		if err == nil {
			// Czekamy na pełne połączenie (fully connected)
			for attempts := 0; attempts < 10; attempts++ { // 10 prób sprawdzenia stanu
				if bot.IsConnected() {
					util.Info("Bot %s successfully connected and ready", bot.GetCurrentNick())
					return true
				}
				time.Sleep(1 * time.Second)
			}
		}

		util.Warning("Bot %s connection attempt failed: %v", bot.GetCurrentNick(), err)
		time.Sleep(retryInterval)
	}
}

// Stop safely shuts down all bots
func (bm *BotManager) Stop() {
	bm.cancel() // Anuluj kontekst, aby zasygnalizować wszystkim goroutynom, że powinny się zakończyć
	close(bm.stopChan)
	bm.wg.Wait()
	for _, bot := range bm.bots {
		bot.Quit("Shutting down")
	}
	util.Info("All bots have been stopped.")
}

// Dodajemy nową metodę do BotManager
func (bm *BotManager) RemoveBotFromManager(botToRemove types.Bot) {
	bm.mutex.Lock()
	defer bm.mutex.Unlock()

	// Znajdź i usuń bota z listy
	newBots := make([]types.Bot, 0, len(bm.bots))
	found := false

	for _, bot := range bm.bots {
		if bot != botToRemove {
			newBots = append(newBots, bot)
		} else {
			found = true
		}
	}

	if !found {
		return // Bot już został usunięty
	}

	bm.bots = newBots

	// Aktualizuj indeks dla komend
	if bm.commandBotIndex >= len(bm.bots) {
		bm.commandBotIndex = 0
	}

	// Aktualizuj NickManager
	if bm.nickManager != nil {
		currentNick := botToRemove.GetCurrentNick()
		bm.nickManager.ReturnNickToPool(currentNick)
		nm := bm.nickManager.(*nickmanager.NickManager)
		nm.UnregisterBot(botToRemove)
	}

	util.Info("Bot %s has been removed from BotManager", botToRemove.GetCurrentNick())
}

// CanExecuteMassCommand checks if a mass command can be executed
func (bm *BotManager) CanExecuteMassCommand(cmdName string) bool {
	bm.mutex.Lock()
	defer bm.mutex.Unlock()

	lastExecution, exists := bm.lastMassCommand[cmdName]
	if !exists || time.Since(lastExecution) > bm.massCommandCooldown {
		bm.lastMassCommand[cmdName] = time.Now()
		util.Debug("BotManager: Mass command %s can be executed", cmdName)
		return true
	}

	util.Debug("BotManager: Mass command %s is on cooldown", cmdName)
	return false
}

func (bm *BotManager) AddOwner(ownerMask string) error {
	bm.mutex.Lock()
	defer bm.mutex.Unlock()

	// Check if owner already exists
	for _, owner := range bm.owners.Owners {
		if owner == ownerMask {
			return fmt.Errorf("owner '%s' already exists", ownerMask)
		}
	}

	bm.owners.Owners = append(bm.owners.Owners, ownerMask)

	// Save to file
	return bm.saveOwnersToFile()
}

func (bm *BotManager) RemoveOwner(ownerMask string) error {
	bm.mutex.Lock()
	defer bm.mutex.Unlock()

	index := -1
	for i, owner := range bm.owners.Owners {
		if owner == ownerMask {
			index = i
			break
		}
	}

	if index == -1 {
		return fmt.Errorf("owner '%s' not found", ownerMask)
	}

	bm.owners.Owners = append(bm.owners.Owners[:index], bm.owners.Owners[index+1:]...)

	// Save to file
	return bm.saveOwnersToFile()
}

func (bm *BotManager) GetOwners() []string {
	// Use a read lock to allow multiple readers
	// This helps prevent blocking when multiple users request owner lists
	bm.mutex.RLock()
	defer bm.mutex.RUnlock()

	ownersCopy := make([]string, len(bm.owners.Owners))
	copy(ownersCopy, bm.owners.Owners)
	return ownersCopy
}

func (bm *BotManager) saveOwnersToFile() error {
	jsonData, err := json.MarshalIndent(bm.owners, "", "  ")
	if err != nil {
		return err
	}

	err = os.WriteFile("configs/owners.json", jsonData, 0644)
	if err != nil {
		return err
	}

	// Update owner list in bots
	for _, bot := range bm.bots {
		bot.SetOwnerList(bm.owners)
	}

	return nil
}

// GetBots returns a copy of the bot slice
func (bm *BotManager) GetBots() []types.Bot {
	bm.mutex.Lock()
	defer bm.mutex.Unlock()

	botsCopy := make([]types.Bot, len(bm.bots))
	copy(botsCopy, bm.bots)
	return botsCopy
}

// GetNickManager returns the NickManager
func (bm *BotManager) GetNickManager() types.NickManager {
	return bm.nickManager
}

// SetMassCommandCooldown sets the cooldown duration for mass commands
func (bm *BotManager) SetMassCommandCooldown(duration time.Duration) {
	bm.mutex.Lock()
	defer bm.mutex.Unlock()
	bm.massCommandCooldown = duration
}

// GetMassCommandCooldown returns the current cooldown duration for mass commands
func (bm *BotManager) GetMassCommandCooldown() time.Duration {
	bm.mutex.Lock()
	defer bm.mutex.Unlock()
	return bm.massCommandCooldown
}

func (bm *BotManager) CollectReactions(channel, message string, action func() error) {
	// Sanitize inputs to prevent issues
	channel = strings.TrimSpace(channel)
	message = strings.TrimSpace(message)

	// Skip empty channels or messages
	if channel == "" || message == "" {
		util.Warning("CollectReactions called with empty channel or message")
		return
	}

	// Generate a unique key for this reaction
	key := channel + ":" + message + ":" + fmt.Sprintf("%d", time.Now().UnixNano())
	now := time.Now()

	// Check for duplicates (simplified to avoid potential deadlocks)
	bm.reactionMutex.Lock()
	for existingKey, req := range bm.reactionRequests {
		// If we find a similar request (same channel and message) that's recent, ignore this one
		if strings.HasPrefix(existingKey, channel+":"+message) && now.Sub(req.Timestamp) < 5*time.Second {
			util.Debug("Duplicate reaction request detected: %s", existingKey)
			bm.reactionMutex.Unlock()
			return // Ignore duplicates within 5 seconds
		}
	}

	// Register this request
	bm.reactionRequests[key] = types.ReactionRequest{
		Channel:      channel,
		Message:      message,
		Timestamp:    now,
		Action:       action,
		ErrorHandled: false,
	}
	bm.reactionMutex.Unlock()

	// Log the reaction request
	util.Debug("CollectReactions: Registered reaction request %s for channel %s", key, channel)

	// Execute action with timeout if provided
	if action != nil {
		// Use a timeout to prevent hanging
		timeoutChan := time.After(10 * time.Second)
		actionDone := make(chan error, 1)

		// Execute the action in a separate goroutine
		go func() {
			defer func() {
				if r := recover(); r != nil {
					util.Error("Panic in CollectReactions action: %v", r)
					actionDone <- fmt.Errorf("internal error: %v", r)
				}
			}()

			// Execute the action and send the result
			actionDone <- action()
		}()

		// Wait for the action to complete or timeout
		var err error
		select {
		case err = <-actionDone:
			// Action completed
			util.Debug("CollectReactions: Action completed for %s", key)
		case <-timeoutChan:
			// Action timed out
			err = fmt.Errorf("action timed out")
			util.Warning("CollectReactions: Action timed out for channel %s, key %s", channel, key)
		}

		if err != nil {
			// Handle error (simplified to avoid potential deadlocks)
			bm.reactionMutex.Lock()
			if req, exists := bm.reactionRequests[key]; exists && !req.ErrorHandled {
				req.ErrorHandled = true
				bm.reactionRequests[key] = req
			}
			bm.reactionMutex.Unlock()

			// Send error message
			bm.SendSingleMsg(channel, fmt.Sprintf("Error: %v", err))

			// Schedule cleanup
			go bm.cleanupReactionRequest(key)
			return
		}
	}

	// Send success message if provided
	if message != "" {
		// Send the message directly to avoid potential issues
		for _, bot := range bm.GetBots() {
			if bot.IsConnected() {
				bot.SendMessage(channel, message)
				break // Only need one bot to send the message
			}
		}
	}

	// Schedule cleanup
	go bm.cleanupReactionRequest(key)
}

// cleanupReactionRequest removes a reaction request after a delay
func (bm *BotManager) cleanupReactionRequest(key string) {
	time.Sleep(5 * time.Second)

	// Use a shorter lock scope to avoid potential deadlocks
	bm.reactionMutex.Lock()
	// Check if the key exists before deleting
	_, exists := bm.reactionRequests[key]
	if exists {
		// Just remove the reaction request - error handling is now per-request
		delete(bm.reactionRequests, key)
		util.Debug("Cleaned up reaction request: %s", key)
	} else {
		util.Debug("Reaction request not found for cleanup: %s", key)
	}
	bm.reactionMutex.Unlock()
}

func (bm *BotManager) SendSingleMsg(channel, message string) {
	// Sanitize inputs
	channel = strings.TrimSpace(channel)
	message = strings.TrimSpace(message)

	// Skip empty messages or channels
	if channel == "" || message == "" {
		util.Warning("SendSingleMsg: Empty channel or message")
		return
	}

	// Find a connected bot with a short lock
	var bot types.Bot
	var botFound bool

	bm.mutex.Lock()
	if len(bm.bots) == 0 {
		bm.mutex.Unlock()
		util.Warning("SendSingleMsg: No bots available to send message to %s", channel)
		return
	}

	// Try to find a connected bot
	triedBots := 0

	for triedBots < len(bm.bots) {
		bot = bm.bots[bm.commandBotIndex]
		bm.commandBotIndex = (bm.commandBotIndex + 1) % len(bm.bots)

		if bot != nil && bot.IsConnected() {
			botFound = true
			break
		}

		triedBots++
	}
	bm.mutex.Unlock()

	if !botFound {
		util.Warning("SendSingleMsg: No connected bots available to send message to %s", channel)
		return
	}

	// Log the message being sent
	util.Debug("SendSingleMsg: Sending message to %s via bot %s: %s",
		channel, bot.GetCurrentNick(), message)

	// Send the message with a timeout
	timeoutChan := time.After(5 * time.Second)
	doneChan := make(chan struct{})

	go func() {
		defer func() {
			if r := recover(); r != nil {
				util.Error("Panic in SendSingleMsg: %v", r)
			}
			close(doneChan)
		}()

		bot.SendMessage(channel, message)
	}()

	// Wait for the message to be sent or timeout
	select {
	case <-doneChan:
		// Message sent successfully
		util.Debug("SendSingleMsg: Message sent successfully to %s via bot %s",
			channel, bot.GetCurrentNick())
	case <-timeoutChan:
		// Message sending timed out
		util.Warning("SendSingleMsg: Timeout sending message to %s via bot %s",
			channel, bot.GetCurrentNick())
	}
}

func (bm *BotManager) GetTotalCreatedBots() int {
	return bm.totalCreatedBots
}

// CoordinateChannelCommand coordinates commands issued on a channel to prevent flooding
// Returns true if this bot should respond to the command, false otherwise
func (bm *BotManager) CoordinateChannelCommand(bot types.Bot, sender, channel, command string, args []string) bool {
	// Generate a unique command ID based on sender, channel, command, and args
	commandStr := command
	if len(args) > 0 {
		commandStr += " " + strings.Join(args, " ")
	}
	commandID := fmt.Sprintf("%s:%s:%s", sender, channel, commandStr)

	// Get the current time
	now := time.Now()

	// Lock the command mutex
	bm.commandMutex.Lock()
	defer bm.commandMutex.Unlock()

	// Check if we've seen this command before
	cmd, exists := bm.channelCommands[commandID]
	if exists {
		// If the command is recent (within 2 seconds) and has been handled, don't respond
		if now.Sub(cmd.Timestamp) < 2*time.Second && cmd.Handled {
			return false
		}

		// If the command is older than 2 seconds, treat it as a new command
		if now.Sub(cmd.Timestamp) >= 2*time.Second {
			// Update the command with the new timestamp and mark it as handled
			cmd.Timestamp = now
			cmd.Handled = true
			bm.channelCommands[commandID] = cmd

			// Schedule cleanup
			go bm.cleanupChannelCommand(commandID)

			// This bot should respond
			return true
		}

		// If the command is recent but not handled, mark it as handled
		cmd.Handled = true
		bm.channelCommands[commandID] = cmd

		// This bot should respond
		return true
	}

	// This is a new command, register it
	bm.channelCommands[commandID] = &ChannelCommand{
		Sender:    sender,
		Channel:   channel,
		Command:   command,
		Args:      args,
		Timestamp: now,
		Handled:   true, // Mark as handled immediately
	}

	// Schedule cleanup
	go bm.cleanupChannelCommand(commandID)

	// This bot should respond
	return true
}

// cleanupChannelCommand removes a channel command after a delay
func (bm *BotManager) cleanupChannelCommand(commandID string) {
	// Wait for 5 seconds
	time.Sleep(5 * time.Second)

	// Lock the command mutex
	bm.commandMutex.Lock()
	defer bm.commandMutex.Unlock()

	// Remove the command
	delete(bm.channelCommands, commandID)
}
