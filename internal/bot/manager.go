package bot

import (
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
	bots              []types.Bot
	owners            auth.OwnerList
	isonInterval      time.Duration
	wg                sync.WaitGroup
	stopChan          chan struct{}
	nickManager       types.NickManager
	commandBotIndex   int
	isonBotIndex      int
	mutex             sync.Mutex
	availableBots     []types.Bot
	availableBotsLock sync.Mutex
}

// NewBotManager creates a new BotManager instance
func NewBotManager(cfg *config.Config, owners auth.OwnerList, nm types.NickManager) *BotManager {
	manager := &BotManager{
		bots:         make([]types.Bot, len(cfg.Bots)),
		owners:       owners,
		isonInterval: time.Duration(cfg.Global.IsonInterval) * time.Second,
		stopChan:     make(chan struct{}),
		nickManager:  nm,
	}

	// Creating bots
	for i, botCfg := range cfg.Bots {
		bot := NewBot(&botCfg, &cfg.Global, nm, manager)
		bot.SetOwnerList(manager.owners)
		bot.SetChannels(cfg.Channels)
		manager.bots[i] = bot
		util.Debug("BotManager added bot %s", bot.GetCurrentNick())
	}

	manager.updateAvailableBots()
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

	bm.wg.Add(1)
	go bm.monitorNicks()
}

// monitorNicks periodically sends ISON commands via bots in a rotational manner
func (bm *BotManager) monitorNicks() {
	defer bm.wg.Done()
	ticker := time.NewTicker(bm.isonInterval)
	defer ticker.Stop()

	for {
		select {
		case <-bm.stopChan:
			util.Info("Stopped monitoring nicks")
			return
		case <-ticker.C:
			bm.mutex.Lock()
			if len(bm.bots) > 0 {
				bot := bm.getNextIsonBot()
				if bot.IsConnected() {
					util.Debug("BotManager: Bot %s is sending ISON", bot.GetCurrentNick())
					bot.SendISON(bm.nickManager.GetNicksToCatch())
				} else {
					util.Debug("BotManager: Bot %s is not connected; skipping ISON", bot.GetCurrentNick())
				}
			} else {
				util.Debug("BotManager: No available bots to send ISON")
			}
			bm.mutex.Unlock()
		}
	}
}

// getNextIsonBot gets the next bot in the queue to send an ISON command
func (bm *BotManager) getNextIsonBot() types.Bot {
	bot := bm.bots[bm.isonBotIndex]
	bm.isonBotIndex = (bm.isonBotIndex + 1) % len(bm.bots)
	return bot
}

// Stop safely shuts down all bots and their goroutines
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

// UpdateAvailableBots updates the list of bots available to catch nicks
func (bm *BotManager) updateAvailableBots() {
	bm.availableBotsLock.Lock()
	defer bm.availableBotsLock.Unlock()

	bm.availableBots = []types.Bot{}
	for _, bot := range bm.bots {
		if bot.IsConnected() && !util.IsTargetNick(bot.GetCurrentNick(), bm.nickManager.GetNicksToCatch()) {
			bm.availableBots = append(bm.availableBots, bot)
		}
	}
}

// GetAvailableBots returns the list of bots available to catch nicks
func (bm *BotManager) GetAvailableBots() []types.Bot {
	bm.availableBotsLock.Lock()
	defer bm.availableBotsLock.Unlock()

	return bm.availableBots
}

// AssignBotForNick assigns an available bot to attempt to catch a nick
func (bm *BotManager) AssignBotForNick(nick string) types.Bot {
	bm.availableBotsLock.Lock()
	defer bm.availableBotsLock.Unlock()

	if len(bm.availableBots) == 0 {
		return nil
	}

	// Round-robin assignment
	bot := bm.availableBots[0]
	bm.availableBots = append(bm.availableBots[1:], bot)
	return bot
}
