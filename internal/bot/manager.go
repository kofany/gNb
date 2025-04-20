package bot

import (
	"context"
	"encoding/json"
	"fmt"
	"math/rand"
	"os"
	"sync"
	"time"

	"github.com/kofany/gNb/internal/auth"
	"github.com/kofany/gNb/internal/config"
	"github.com/kofany/gNb/internal/types"
	"github.com/kofany/gNb/internal/util"
)

// Ensure BotManager implements types.BotManager
var _ types.BotManager = (*BotManager)(nil)

// BotManager manages multiple IRC bots
type BotManager struct {
	bots                []types.Bot
	owners              auth.OwnerList
	wg                  sync.WaitGroup
	stopChan            chan struct{}
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
	errorHandled        bool       // Dodaj flagę do obsługi błędów
	errorMutex          sync.Mutex // Mutex do kontrolowania dostępu do errorHandled
	totalCreatedBots    int
	startTime           time.Time
	// For reconnection storm prevention
	recentDisconnects []time.Time
	disconnectMutex   sync.Mutex
}

// NewBotManager creates a new BotManager instance
func NewBotManager(cfg *config.Config, owners auth.OwnerList) *BotManager {
	// Initialize random number generator with a time-based seed for jitter calculations
	// Note: In Go 1.20+ rand.Seed is deprecated, but we keep it for compatibility
	// with older Go versions. In newer versions, the random source is auto-seeded.
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
		lastMassCommand:     make(map[string]time.Time),
		massCommandCooldown: time.Duration(cfg.Global.MassCommandCooldown) * time.Second,
		wordPool:            wordPool,
		wordPoolMutex:       sync.Mutex{},
		reactionRequests:    make(map[string]types.ReactionRequest),
		ctx:                 ctx,
		cancel:              cancel,
		startTime:           time.Now(),
		recentDisconnects:   make([]time.Time, 0),
	}

	// Creating bots
	for i, botCfg := range cfg.Bots {
		bot := NewBot(&botCfg, &cfg.Global, manager)
		bot.SetOwnerList(manager.owners)
		bot.SetChannels(cfg.Channels)
		bot.SetBotManager(manager)
		manager.bots[i] = bot
		util.Debug("BotManager added bot %s", bot.Connection.GetNick())
	}
	go manager.cleanupDisconnectedBots()
	return manager
}

func (bm *BotManager) cleanupDisconnectedBots() {
	// Czekamy 240 sekund od startu
	time.Sleep(240 * time.Second)

	util.Info("Starting cleanup of disconnected bots after startup grace period")

	// Najpierw identyfikujemy boty do usunięcia pod RLock
	bm.mutex.RLock()
	botsToRemove := make([]types.Bot, 0)
	for _, bot := range bm.bots {
		if !bot.IsConnected() {
			botsToRemove = append(botsToRemove, bot)
		}
	}
	bm.mutex.RUnlock()

	// Teraz usuwamy każdy bot pojedynczo, aby zminimalizować czas blokowania
	removedCount := 0
	for _, bot := range botsToRemove {
		// Logujemy informację o usuwanym bocie
		util.Warning("Removing bot %s due to connection failure after startup period", bot.GetCurrentNick())

		// Zwalniamy zasoby bota w osobnej goroutine, aby nie blokować
		go func(botToRemove types.Bot) {
			botToRemove.Quit("Cleanup - connection failure")
		}(bot)

		// Usuwamy bota z managera
		bm.RemoveBotFromManager(bot)
		removedCount++
	}

	// Logujemy podsumowanie
	if removedCount > 0 {
		bm.mutex.RLock()
		remainingBots := len(bm.bots)
		bm.mutex.RUnlock()

		util.Info("Cleanup completed: removed %d disconnected bots, %d bots remaining",
			removedCount, remainingBots)

		// Aktualizujemy totalCreatedBots o liczbę usuniętych botów
		bm.mutex.Lock()
		bm.totalCreatedBots -= removedCount
		bm.mutex.Unlock()
	} else {
		util.Info("Cleanup completed: all bots are connected")
	}
}

func (bm *BotManager) StartBots() {
	util.Info("=== STARTING BOT INITIALIZATION PROCESS ===")
	util.Info("Initializing %d bots", len(bm.bots))

	var wg sync.WaitGroup
	connectedBots := make([]types.Bot, 0)
	var connectedBotsMutex sync.Mutex

	// Log initial bot information
	for i, bot := range bm.bots {
		// Get nick safely for logging
		currentNick := "unknown"
		try := func() (nick string, ok bool) {
			defer func() {
				if r := recover(); r != nil {
					ok = false
				}
			}()
			return bot.GetCurrentNick(), true
		}
		if nick, ok := try(); ok {
			currentNick = nick
		}

		util.Debug("Bot %d/%d: %s will be initialized", i+1, len(bm.bots), currentNick)
	}

	// Start bots with staggered delays
	util.Info("Starting connection process for all bots with staggered delays")
	totalBots := len(bm.bots)

	// Calculate a more significant delay between bots to avoid rate limiting
	// For many servers, connecting too many bots too quickly can trigger rate limiting
	baseDelay := 5 * time.Second
	maxTotalTime := 2 * time.Minute

	// Ensure we don't exceed maxTotalTime for all bots
	staggerInterval := baseDelay
	if totalBots > 1 {
		maxInterval := maxTotalTime / time.Duration(totalBots-1)
		if baseDelay > maxInterval {
			staggerInterval = maxInterval
		}
	}

	util.Info("Using stagger interval of %v between bots (total startup time: ~%v)",
		staggerInterval, staggerInterval*time.Duration(totalBots-1))

	for i, bot := range bm.bots {
		// Calculate staggered delay to prevent connection storms
		delay := time.Duration(i) * staggerInterval
		util.Debug("Bot %d/%d will start connecting in %v", i+1, len(bm.bots), delay)

		wg.Add(1)
		go func(bot types.Bot, position int, startDelay time.Duration) {
			defer wg.Done()

			// Get nick safely for logging
			currentNick := "unknown"
			try := func() (nick string, ok bool) {
				defer func() {
					if r := recover(); r != nil {
						ok = false
					}
				}()
				return bot.GetCurrentNick(), true
			}
			if nick, ok := try(); ok {
				currentNick = nick
			}

			// Apply staggered delay
			util.Debug("Waiting %v before starting connection process for bot %s", startDelay, currentNick)
			time.Sleep(startDelay)
			util.Info("Starting connection process for bot %d/%d: %s", position+1, len(bm.bots), currentNick)

			// Kanał do monitorowania timeout
			timeoutDuration := 180 * time.Second
			timeoutChan := time.After(timeoutDuration)
			connectChan := make(chan bool)
			util.Debug("Connection timeout set to %v for bot %s", timeoutDuration, currentNick)

			// Startujemy proces łączenia w osobnej goroutynie
			go func() {
				util.Debug("Starting connection retry process for bot %s", currentNick)
				success := bm.startBotWithRetry(bot)
				connectChan <- success
			}()

			// Czekamy na rezultat z timeoutem
			util.Debug("Waiting for connection result or timeout for bot %s", currentNick)
			select {
			case success := <-connectChan:
				if success {
					util.Info("Bot %s successfully connected and added to active bots", currentNick)
					// Double-check that the bot is actually connected
					if bot.IsConnected() {
						connectedBotsMutex.Lock()
						connectedBots = append(connectedBots, bot)
						connectedBotsMutex.Unlock()
					} else {
						util.Warning("Bot %s reported success but IsConnected() returned false", currentNick)
					}
				} else {
					util.Warning("Bot %s connection process failed", currentNick)
					// Check if the bot is actually connected despite reporting failure
					if bot.IsConnected() {
						util.Warning("Bot %s reported failure but IsConnected() returned true, adding to connected bots", currentNick)
						connectedBotsMutex.Lock()
						connectedBots = append(connectedBots, bot)
						connectedBotsMutex.Unlock()
					}
				}
			case <-timeoutChan:
				// Bot nie połączył się w wymaganym czasie
				util.Warning("Bot %s failed to connect within timeout (%v)", currentNick, timeoutDuration)
				// Check if the bot is actually connected despite the timeout
				if bot.IsConnected() {
					util.Warning("Bot %s is actually connected despite timeout, adding to connected bots", currentNick)
					connectedBotsMutex.Lock()
					connectedBots = append(connectedBots, bot)
					connectedBotsMutex.Unlock()
				} else {
					util.Warning("Bot %s is not connected, removing", currentNick)
					bot.Quit("Connection timeout")
				}
			}
		}(bot, i, delay)
	}

	// Czekamy na zakończenie wszystkich prób połączeń
	util.Debug("Waiting for all bot connection attempts to complete")
	wg.Wait()
	util.Info("All bot connection attempts completed")

	// Aktualizujemy listę botów tylko o te połączone
	bm.mutex.Lock()
	oldCount := len(bm.bots)
	bm.bots = connectedBots
	newCount := len(bm.bots)
	bm.mutex.Unlock()

	util.Info("=== BOT INITIALIZATION COMPLETE: %d/%d bots successfully connected ===", newCount, oldCount)
}

func (bm *BotManager) startBotWithRetry(bot types.Bot) bool {
	retryInterval := 5 * time.Second
	maxTime := 120 * time.Second
	startTime := time.Now()

	// Get bot nick safely for logging
	currentNick := "unknown"
	try := func() (nick string, ok bool) {
		defer func() {
			if r := recover(); r != nil {
				ok = false
			}
		}()
		return bot.GetCurrentNick(), true
	}
	if nick, ok := try(); ok {
		currentNick = nick
	}

	util.Debug("=== STARTING BOT CONNECTION PROCESS FOR %s ===", currentNick)
	util.Debug("Max connection time: %v, Retry interval: %v", maxTime, retryInterval)

	attemptCount := 0
	for {
		attemptCount++
		util.Debug("Connection attempt #%d for bot %s", attemptCount, currentNick)

		if time.Since(startTime) > maxTime {
			util.Warning("Connection process for bot %s timed out after %v", currentNick, maxTime)
			return false
		}

		// Use defer/recover to catch any panics during connection
		func() {
			defer func() {
				if r := recover(); r != nil {
					util.Error("Recovered from panic in startBotWithRetry: %v", r)
				}
			}()

			// Try to connect
			util.Debug("Calling Connect() for bot %s", currentNick)
			err := bot.Connect()
			if err == nil {
				util.Debug("Connect() succeeded for bot %s, waiting for full connection", currentNick)

				// Wait for full connection
				for attempts := 0; attempts < 10; attempts++ {
					util.Debug("Checking connection status for bot %s (attempt %d/10)", currentNick, attempts+1)

					// Check if bot is still in the manager (not banned/removed)
					if !bm.IsBotInManager(bot) {
						util.Warning("Bot %s was removed from manager during connection process", currentNick)
						return
					}

					// Check connection status
					util.Debug("Checking if bot %s is connected (attempt %d/10)", currentNick, attempts+1)
					if bot.IsConnected() {
						// Get nick safely
						currentNick := "unknown"
						try := func() (nick string, ok bool) {
							defer func() {
								if r := recover(); r != nil {
									ok = false
								}
							}()
							return bot.GetCurrentNick(), true
						}
						if nick, ok := try(); ok {
							currentNick = nick
						}

						util.Info("Bot %s successfully connected and ready", currentNick)
						util.Debug("=== BOT CONNECTION PROCESS COMPLETED SUCCESSFULLY FOR %s ===", currentNick)

						// Verify the bot is in the manager's list
						if !bm.IsBotInManager(bot) {
							util.Warning("Bot %s is connected but not in the manager's list, adding it back", currentNick)
							bm.mutex.Lock()
							bm.bots = append(bm.bots, bot)
							bm.mutex.Unlock()
						}

						return
					}
					util.Debug("Bot %s not fully connected yet, waiting 1 second", currentNick)
					time.Sleep(1 * time.Second)
				}

				util.Warning("Bot %s connected but did not become fully connected within timeout", currentNick)
			}

			// Get nick safely for error message
			currentNick := "unknown"
			try := func() (nick string, ok bool) {
				defer func() {
					if r := recover(); r != nil {
						ok = false
					}
				}()
				return bot.GetCurrentNick(), true
			}
			if nick, ok := try(); ok {
				currentNick = nick
			}

			if err != nil {
				util.Warning("Bot %s connection attempt failed: %v", currentNick, err)
			}
		}()

		// Check if bot is still in the manager (not banned/removed)
		if !bm.IsBotInManager(bot) {
			util.Warning("Bot %s was removed from manager, stopping retry attempts", currentNick)
			return false
		}

		util.Debug("Waiting %v before next connection attempt for bot %s", retryInterval, currentNick)
		time.Sleep(retryInterval)
	}
}

// IsBotInManager checks if a bot is still in the manager
func (bm *BotManager) IsBotInManager(botToCheck types.Bot) bool {
	bm.mutex.RLock()
	defer bm.mutex.RUnlock()

	for _, bot := range bm.bots {
		if bot == botToCheck {
			return true
		}
	}

	return false
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

// RemoveBotFromManager usuwa bota z managera
func (bm *BotManager) RemoveBotFromManager(botToRemove types.Bot) {
	// Najpierw sprawdzamy, czy bot istnieje w liście
	var found bool

	bm.mutex.RLock()
	for _, bot := range bm.bots {
		if bot == botToRemove {
			found = true
			break
		}
	}
	bm.mutex.RUnlock()

	if !found {
		util.Debug("Bot %s already removed from BotManager", botToRemove.GetCurrentNick())
		return // Bot już został usunięty
	}

	// Teraz usuwamy bota z listy pod mutexem
	bm.mutex.Lock()

	// Sprawdź ponownie, czy bot nadal istnieje (mógł zostać usunięty w międzyczasie)
	newBots := make([]types.Bot, 0, len(bm.bots))
	found = false

	for _, bot := range bm.bots {
		if bot != botToRemove {
			newBots = append(newBots, bot)
		} else {
			found = true
		}
	}

	if !found {
		bm.mutex.Unlock()
		util.Debug("Bot %s already removed from BotManager", botToRemove.GetCurrentNick())
		return // Bot już został usunięty
	}

	// Aktualizujemy listę botów
	bm.bots = newBots

	// Aktualizujemy indeks dla komend
	if bm.commandBotIndex >= len(bm.bots) {
		bm.commandBotIndex = 0
	}
	bm.mutex.Unlock()

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
	// Używamy RLock zamiast Lock, aby umożliwić równoczesny odczyt
	bm.mutex.RLock()
	ownersCopy := make([]string, len(bm.owners.Owners))
	copy(ownersCopy, bm.owners.Owners)
	bm.mutex.RUnlock()
	return ownersCopy
}

func (bm *BotManager) saveOwnersToFile() error {
	// Najpierw przygotujmy dane do zapisu
	jsonData, err := json.MarshalIndent(bm.owners, "", "  ")
	if err != nil {
		return err
	}

	// Zapisujemy do pliku bez blokowania mutexa
	err = os.WriteFile("configs/owners.json", jsonData, 0644)
	if err != nil {
		return err
	}

	// Kopiujemy listę botów i listę właścicieli pod mutexem
	bm.mutex.RLock()
	botsCopy := make([]types.Bot, len(bm.bots))
	copy(botsCopy, bm.bots)
	ownersCopy := bm.owners
	bm.mutex.RUnlock()

	// Aktualizujemy listę właścicieli w botach bez blokowania mutexa
	for _, bot := range botsCopy {
		bot.SetOwnerList(ownersCopy)
	}

	return nil
}

// GetBots returns a copy of the bot slice
func (bm *BotManager) GetBots() []types.Bot {
	bm.mutex.RLock()
	botsCopy := make([]types.Bot, len(bm.bots))
	copy(botsCopy, bm.bots)
	bm.mutex.RUnlock()
	return botsCopy
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
	bm.reactionMutex.Lock()
	defer bm.reactionMutex.Unlock()

	key := channel + ":" + message
	now := time.Now()

	if req, exists := bm.reactionRequests[key]; exists && now.Sub(req.Timestamp) < 5*time.Second {
		return // Ignore duplicates within 5 seconds
	}

	// Execute action
	if action != nil {
		err := action()
		if err != nil {
			bm.errorMutex.Lock()
			if !bm.errorHandled {
				bm.SendSingleMsg(channel, fmt.Sprintf("Error: %v", err))
				bm.errorHandled = true            // Ustaw flagę po obsłużeniu błędu
				go bm.cleanupReactionRequest(key) // Wywołaj cleanup po błędzie
			}
			bm.errorMutex.Unlock()
			return
		}
	}

	if message != "" {
		bm.SendSingleMsg(channel, message)
	}

	// Save request to ignore duplicates for the next 5 seconds
	bm.reactionRequests[key] = types.ReactionRequest{
		Channel:   channel,
		Message:   message,
		Timestamp: now,
		Action:    action,
	}

	// Run cleanup after 5 seconds for successful command
	go bm.cleanupReactionRequest(key)
}

// Zaktualizowana funkcja cleanupReactionRequest
func (bm *BotManager) cleanupReactionRequest(key string) {
	time.Sleep(5 * time.Second)
	bm.reactionMutex.Lock()
	defer bm.reactionMutex.Unlock()

	// Usuń zapis reakcji
	delete(bm.reactionRequests, key)

	// Resetuj flagę błędu po zakończeniu reakcji
	bm.errorMutex.Lock()
	bm.errorHandled = false
	bm.errorMutex.Unlock()
}

func (bm *BotManager) SendSingleMsg(channel, message string) {
	// Pobieramy bota pod RLock
	var bot types.Bot
	bm.mutex.RLock()
	if len(bm.bots) == 0 {
		bm.mutex.RUnlock()
		return
	}
	bot = bm.bots[bm.commandBotIndex]
	bm.mutex.RUnlock()

	// Aktualizujemy indeks pod Lock
	bm.mutex.Lock()
	bm.commandBotIndex = (bm.commandBotIndex + 1) % len(bm.bots)
	bm.mutex.Unlock()

	// Wysyłamy wiadomość bez blokowania mutexa
	bot.SendMessage(channel, message)
}

func (bm *BotManager) GetTotalCreatedBots() int {
	return bm.totalCreatedBots
}

// DetectMassDisconnect checks if there have been multiple disconnects in a short time period
func (bm *BotManager) DetectMassDisconnect() bool {
	const threshold = 3                 // Number of disconnects to consider it a mass disconnect
	const timeWindow = 10 * time.Second // Time window to count disconnects

	bm.disconnectMutex.Lock()
	defer bm.disconnectMutex.Unlock()

	// Count recent disconnects
	now := time.Now()
	recentDisconnects := 0

	for _, disconnectTime := range bm.recentDisconnects {
		if now.Sub(disconnectTime) < timeWindow {
			recentDisconnects++
		}
	}

	// Add current disconnect
	bm.recentDisconnects = append(bm.recentDisconnects, now)

	// Clean up old entries
	newList := make([]time.Time, 0)
	for _, t := range bm.recentDisconnects {
		if now.Sub(t) < timeWindow {
			newList = append(newList, t)
		}
	}
	bm.recentDisconnects = newList

	return recentDisconnects >= threshold
}

// HandleNetworkOutage implements staggered reconnection with jitter to prevent reconnection storms
func (bm *BotManager) HandleNetworkOutage() {
	util.Warning("Network outage detected! Implementing staggered reconnection strategy")

	// Get all disconnected bots
	disconnectedBots := bm.getDisconnectedBots()
	totalBots := len(disconnectedBots)

	if totalBots == 0 {
		return
	}

	// Base parameters for reconnection timing
	maxTotalReconnectTime := 3 * time.Minute

	util.Info("Staggering reconnection of %d bots over %v", totalBots, maxTotalReconnectTime)

	for i, bot := range disconnectedBots {
		// Calculate staggered delay
		staggerPosition := float64(i) / float64(totalBots)
		baseStaggerDelay := time.Duration(staggerPosition * float64(maxTotalReconnectTime))

		// Add jitter (±30%)
		jitterFactor := 0.7 + 0.6*rand.Float64()
		finalDelay := time.Duration(float64(baseStaggerDelay) * jitterFactor)

		// Schedule reconnection
		go func(b types.Bot, delay time.Duration, position int) {
			util.Debug("Bot %s scheduled to reconnect in %v (position %d/%d)",
				b.GetCurrentNick(), delay, position+1, totalBots)
			time.Sleep(delay)

			// Try to reconnect
			util.Info("Staggered reconnection: attempting to reconnect bot %s", b.GetCurrentNick())
			err := b.Connect()
			if err != nil {
				util.Error("Staggered reconnection failed for bot %s: %v", b.GetCurrentNick(), err)
			} else {
				util.Info("Staggered reconnection successful for bot %s", b.GetCurrentNick())
			}
		}(bot, finalDelay, i)
	}
}

// getDisconnectedBots returns a list of bots that are currently disconnected
func (bm *BotManager) getDisconnectedBots() []types.Bot {
	bm.mutex.RLock()
	defer bm.mutex.RUnlock()

	disconnectedBots := make([]types.Bot, 0)
	for _, bot := range bm.bots {
		if !bot.IsConnected() {
			disconnectedBots = append(disconnectedBots, bot)
		}
	}

	return disconnectedBots
}
