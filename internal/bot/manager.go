package bot

import (
	"encoding/json"
	"fmt"
	"os"
	"sync"

	"github.com/kofany/gNb/internal/auth"
	"github.com/kofany/gNb/internal/config"
	"github.com/kofany/gNb/internal/types"
	"github.com/kofany/gNb/internal/util"
)

// Ensure BotManager implements types.BotManager
var _ types.BotManager = (*BotManager)(nil)

// BotManager manages multiple IRC bots
type BotManager struct {
	bots            []types.Bot
	owners          auth.OwnerList
	wg              sync.WaitGroup
	stopChan        chan struct{}
	nickManager     types.NickManager
	commandBotIndex int
	mutex           sync.Mutex
}

// NewBotManager creates a new BotManager instance
func NewBotManager(cfg *config.Config, owners auth.OwnerList, nm types.NickManager) *BotManager {
	manager := &BotManager{
		bots:        make([]types.Bot, len(cfg.Bots)),
		owners:      owners,
		stopChan:    make(chan struct{}),
		nickManager: nm,
	}

	// Creating bots
	for i, botCfg := range cfg.Bots {
		bot := NewBot(&botCfg, &cfg.Global, nm, manager)
		bot.SetOwnerList(manager.owners)
		bot.SetChannels(cfg.Channels)
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
func (bm *BotManager) ShouldHandleCommand(bot types.Bot) bool {
	bm.mutex.Lock()
	defer bm.mutex.Unlock()

	if len(bm.bots) == 0 {
		return false
	}

	if bm.bots[bm.commandBotIndex] == bot {
		// Move index to next bot
		bm.commandBotIndex = (bm.commandBotIndex + 1) % len(bm.bots)
		util.Debug("BotManager: Bot %s will handle the command", bot.GetCurrentNick())
		return true
	}
	util.Debug("BotManager: Bot %s will not handle the command", bot.GetCurrentNick())
	return false
}

func (bm *BotManager) AddOwner(ownerMask string) error {
	bm.mutex.Lock()
	defer bm.mutex.Unlock()

	// Sprawdź, czy owner już istnieje
	for _, owner := range bm.owners.Owners {
		if owner == ownerMask {
			return fmt.Errorf("owner '%s' already exists", ownerMask)
		}
	}

	bm.owners.Owners = append(bm.owners.Owners, ownerMask)

	// Zapisz do pliku
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

	// Zapisz do pliku
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

	// Zaktualizuj listę właścicieli w botach
	for _, bot := range bm.bots {
		bot.SetOwnerList(bm.owners)
	}

	return nil
}
