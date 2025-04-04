package bot

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
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

// BotManager manages multiple IRC bots
type BotManager struct {
	bots                []types.Bot
	owners              auth.OwnerList
	wg                  sync.WaitGroup
	stopChan            chan struct{}
	nickManager         types.NickManager
	commandBotIndex     int
	mutex               sync.Mutex
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
	bm.mutex.Lock()
	defer bm.mutex.Unlock()

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
	// 1. Acquire main lock
	bm.mutex.Lock()

	key := channel + ":" + message
	now := time.Now()

	// 2. Check duplicates (briefly locking reactionMutex)
	bm.reactionMutex.Lock()
	if req, exists := bm.reactionRequests[key]; exists && now.Sub(req.Timestamp) < 5*time.Second {
		bm.reactionMutex.Unlock()
		bm.mutex.Unlock() // Release main lock before returning
		util.Debug("Duplicate reaction request ignored for key: %s", key)
		return
	}
	// Register request
	bm.reactionRequests[key] = types.ReactionRequest{
		Channel:   channel,
		Message:   message, // Store message to know if we need to send later
		Timestamp: now,
	}
	bm.reactionMutex.Unlock() // Release reaction lock

	// Schedule cleanup (independent goroutine)
	go bm.cleanupReactionRequest(key)

	// 3. Execute action (holding main lock)
	var actionErr error
	if action != nil {
		actionErr = action()
	}

	// Prepare for message sending
	var botToSend types.Bot
	shouldSendMessage := message != "" && actionErr == nil
	shouldSendError := actionErr != nil
	var errorMessage string

	if shouldSendMessage || shouldSendError {
		if len(bm.bots) > 0 {
			// 4. Select bot (holding main lock)
			botIndexToSend := bm.commandBotIndex
			bm.commandBotIndex = (bm.commandBotIndex + 1) % len(bm.bots)
			botToSend = bm.bots[botIndexToSend] // Store reference
			if shouldSendError {
				errorMessage = fmt.Sprintf("Error: %v", actionErr)
			}
		} else {
			util.Warning("CollectReactions: No bots available to send response.")
			shouldSendMessage = false
			shouldSendError = false // Cannot send error if no bots
		}
	}

	// 5. Release main lock *before* sending
	bm.mutex.Unlock()

	// 6. Send message/error (after releasing main lock)
	if shouldSendError {
		bm.errorMutex.Lock()
		sendError := false
		if !bm.errorHandled {
			bm.errorHandled = true
			sendError = true
			go bm.resetErrorHandledFlag() // Schedule reset
		}
		bm.errorMutex.Unlock()

		if sendError && botToSend != nil {
			util.Error("Error executing action for key %s: %v", key, actionErr)
			botToSend.SendMessage(channel, errorMessage)
		}
	} else if shouldSendMessage && botToSend != nil {
		botToSend.SendMessage(channel, message)
	}
}

// cleanupReactionRequest removes the request entry after cooldown.
func (bm *BotManager) cleanupReactionRequest(key string) {
	time.Sleep(5 * time.Second)
	bm.reactionMutex.Lock()
	defer bm.reactionMutex.Unlock()
	delete(bm.reactionRequests, key)
	util.Debug("Cleaned up reaction request for key: %s", key)
}

// resetErrorHandledFlag resets the error flag after a delay.
func (bm *BotManager) resetErrorHandledFlag() {
	time.Sleep(5 * time.Second) // Use the same cooldown
	bm.errorMutex.Lock()
	defer bm.errorMutex.Unlock()
	bm.errorHandled = false
	util.Debug("Reset errorHandled flag.")
}

func (bm *BotManager) SendSingleMsg(channel, message string) {
	bm.mutex.Lock()
	defer bm.mutex.Unlock()

	if len(bm.bots) == 0 {
		return
	}
	bot := bm.bots[bm.commandBotIndex]
	bm.commandBotIndex = (bm.commandBotIndex + 1) % len(bm.bots)
	bot.SendMessage(channel, message)
}

func (bm *BotManager) GetTotalCreatedBots() int {
	return bm.totalCreatedBots
}
