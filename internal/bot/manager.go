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
	return manager
}

// StartBots starts all bots and connects them to their servers
func (bm *BotManager) StartBots() {
	botChannel := make(chan types.Bot, len(bm.bots))
	for _, bot := range bm.bots {
		botChannel <- bot
	}

	var wg sync.WaitGroup
	for i := 0; i < len(bm.bots); i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for bot := range botChannel {
				bm.startBotWithRetry(bot)
				time.Sleep(time.Second) // Krótkie opóźnienie przed uruchomieniem kolejnego bota
			}
		}()
	}

	close(botChannel)
	wg.Wait()
}

func (bm *BotManager) startBotWithRetry(bot types.Bot) {
	maxRetries := 3
	retryDelay := time.Second * 5

	for attempt := 1; attempt <= maxRetries; attempt++ {
		select {
		case <-bm.ctx.Done():
			return // Zakończ, jeśli kontekst został anulowany
		default:
			err := bot.Connect()
			if err == nil {
				util.Info("Bot %s connected successfully", bot.GetCurrentNick())
				return
			}

			util.Warning("Failed to connect bot %s (attempt %d/%d): %v",
				bot.GetCurrentNick(), attempt, maxRetries, err)

			if attempt < maxRetries {
				select {
				case <-time.After(retryDelay):
				case <-bm.ctx.Done():
					return
				}
			}
		}
	}

	util.Error("Failed to connect bot %s after %d attempts",
		bot.GetCurrentNick(), maxRetries)
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
	bm.mutex.Lock()
	defer bm.mutex.Unlock()

	if len(bm.bots) == 0 {
		return
	}
	bot := bm.bots[bm.commandBotIndex]
	bm.commandBotIndex = (bm.commandBotIndex + 1) % len(bm.bots)
	bot.SendMessage(channel, message)
}
