package bot

import (
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
}

// NewBotManager creates a new BotManager instance
func NewBotManager(cfg *config.Config, owners auth.OwnerList, nm types.NickManager) *BotManager {
	requiredWords := len(cfg.Bots)*3 + 10 // 3 słowa na bota (nick, ident, realname) + 10 zapasowych

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
	}

	// Tworzenie botów
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
	for _, bot := range bm.bots {
		err := bot.Connect()
		if err != nil {
			util.Error("Failed to connect bot: %v", err)
			continue
		}
	}
}

// Stop safely shuts down all bots
func (bm *BotManager) Stop() {
	close(bm.stopChan)
	bm.wg.Wait()
	for _, bot := range bm.bots {
		bot.Quit("Shutting down")
	}
	util.Info("All bots have been stopped.")
}

// ShouldHandleCommand determines if a given bot should handle a command
func (bm *BotManager) ShouldHandleCommand(bot types.Bot, cmdName string) bool {
	bm.mutex.Lock()
	defer bm.mutex.Unlock()

	util.Debug("ShouldHandleCommand called for bot %s, command: %s", bot.GetCurrentNick(), cmdName)

	// Zawsze pozwól na obsługę komendy !bnc przez bota, który ją otrzymał
	if cmdName == "bnc" {
		util.Debug("BNC command will be handled by bot %s", bot.GetCurrentNick())
		return true
	}

	if len(bm.bots) == 0 {
		util.Debug("No bots available to handle command")
		return false
	}

	if bm.bots[bm.commandBotIndex] == bot {
		util.Debug("Bot %s selected to handle command", bot.GetCurrentNick())
		// Move index to next bot
		bm.commandBotIndex = (bm.commandBotIndex + 1) % len(bm.bots)
		return true
	}

	util.Debug("Bot %s not selected to handle command", bot.GetCurrentNick())
	return false
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
