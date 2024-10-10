// File: internal/bot/manager.go

package bot

import (
	"sync"
	"time"

	"github.com/kofany/gNb/internal/auth"
	"github.com/kofany/gNb/internal/config"
	"github.com/kofany/gNb/internal/types"
	"github.com/kofany/gNb/internal/util"
)

// Upewnij się, że BotManager implementuje types.BotManager
var _ types.BotManager = (*BotManager)(nil)

// BotManager zarządza wieloma botami IRC
type BotManager struct {
	bots            []types.Bot
	owners          auth.OwnerList
	isonInterval    time.Duration
	wg              sync.WaitGroup
	stopChan        chan struct{}
	nickManager     types.NickManager
	commandBotIndex int
	mutex           sync.Mutex
}

// NewBotManager tworzy nową instancję BotManager
func NewBotManager(cfg *config.Config, owners auth.OwnerList, nm types.NickManager) *BotManager {
	manager := &BotManager{
		bots:         make([]types.Bot, len(cfg.Bots)),
		owners:       owners,
		isonInterval: time.Duration(cfg.Global.IsonInterval) * time.Second,
		stopChan:     make(chan struct{}),
		nickManager:  nm,
	}

	// Tworzenie botów
	for i, botCfg := range cfg.Bots {
		bot := NewBot(&botCfg, &cfg.Global, nm, manager)
		bot.SetOwnerList(manager.owners)
		bot.SetChannels(cfg.Channels)
		manager.bots[i] = bot
		util.Debug("BotManager dodał bota %s", bot.GetCurrentNick())
	}

	return manager
}

// StartBots uruchamia wszystkie boty i łączy je z ich serwerami
func (bm *BotManager) StartBots() {
	for _, bot := range bm.bots {
		err := bot.Connect()
		if err != nil {
			util.Error("Nie udało się połączyć bota: %v", err)
			continue
		}
	}

	bm.wg.Add(1)
	go bm.monitorNicks()
}

// monitorNicks okresowo wysyła komendy ISON przez boty w sposób rotacyjny
func (bm *BotManager) monitorNicks() {
	defer bm.wg.Done()
	ticker := time.NewTicker(bm.isonInterval)
	defer ticker.Stop()

	botIndex := 0

	for {
		select {
		case <-bm.stopChan:
			util.Info("Zatrzymano monitorowanie nicków")
			return
		case <-ticker.C:
			bm.mutex.Lock()
			if len(bm.bots) > 0 {
				bot := bm.bots[botIndex]
				if bot.IsConnected() {
					util.Debug("BotManager: Bot %s wysyła ISON", bot.GetCurrentNick())
					bot.SendISON(bm.nickManager.GetNicksToCatch())
				} else {
					util.Debug("BotManager: Bot %s nie jest połączony; pomijanie ISON", bot.GetCurrentNick())
				}
				botIndex = (botIndex + 1) % len(bm.bots)
			} else {
				util.Debug("BotManager: Brak dostępnych botów do wysłania ISON")
			}
			bm.mutex.Unlock()
		}
	}
}

// Stop zamyka wszystkie boty i ich gorutiny w sposób bezpieczny
func (bm *BotManager) Stop() {
	close(bm.stopChan)
	bm.wg.Wait()
	for _, bot := range bm.bots {
		bot.Quit("Wyłączanie")
	}
	util.Info("Wszystkie boty zostały zatrzymane.")
}

// ShouldHandleCommand określa, czy dany bot powinien obsłużyć komendę
func (bm *BotManager) ShouldHandleCommand(bot types.Bot) bool {
	bm.mutex.Lock()
	defer bm.mutex.Unlock()

	if len(bm.bots) == 0 {
		return false
	}

	if bm.bots[bm.commandBotIndex] == bot {
		// Przesuń indeks do następnego bota
		bm.commandBotIndex = (bm.commandBotIndex + 1) % len(bm.bots)
		util.Debug("BotManager: Bot %s obsłuży komendę", bot.GetCurrentNick())
		return true
	}
	util.Debug("BotManager: Bot %s nie obsłuży komendy", bot.GetCurrentNick())
	return false
}
