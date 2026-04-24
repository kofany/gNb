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
	errorHandled        bool       // Dodaj flagę do obsługi błędów
	errorMutex          sync.Mutex // Mutex do kontrolowania dostępu do errorHandled
	totalCreatedBots    int
	startTime           time.Time
	sink                types.EventSink
	sinkMu              sync.RWMutex
}

// NewBotManager creates a new BotManager instance
func NewBotManager(cfg *config.Config, owners auth.OwnerList, nm types.NickManager) *BotManager {
	ctx, cancel := context.WithCancel(context.Background())
	requiredWords := len(cfg.Bots)*3 + 10 // 3 words per bot (nick, ident, realname) + 10 spare

	wordPool, err := util.GetWordsFromAPI(
		cfg.Global.NickAPI.URL,
		cfg.Global.MaxNickLength,
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
		bot.botID = computeBotID(botCfg.Server, botCfg.Port, botCfg.Vhost, i)
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

// SetEventSink installs the observer used by the Panel API and fans it out
// to every managed bot plus the NickManager. Safe to call at startup or
// later to swap the sink.
func (bm *BotManager) SetEventSink(sink types.EventSink) {
	bm.sinkMu.Lock()
	bm.sink = sink
	bm.sinkMu.Unlock()

	bm.mutex.RLock()
	bots := append([]types.Bot(nil), bm.bots...)
	nm := bm.nickManager
	bm.mutex.RUnlock()

	for _, b := range bots {
		b.SetEventSink(sink)
	}
	if nm != nil {
		nm.SetEventSink(sink)
	}
}

// currentSink returns the active EventSink or nil.
func (bm *BotManager) currentSink() types.EventSink {
	bm.sinkMu.RLock()
	defer bm.sinkMu.RUnlock()
	return bm.sink
}

// cleanupDisconnectedBots is a one-shot startup grace period. 240 seconds
// after NewBotManager, any bot still not connected is removed from the
// manager and its nick returned to the pool. After the grace window expires
// the goroutine exits -- bots disconnecting later are removed via the
// error-driven paths (IRC 465/466, explicit .reconnect, etc.) rather than
// by a periodic sweeper. This is intentional: the fleet is static after
// boot, so a recurring sweep would have no work to do.
func (bm *BotManager) cleanupDisconnectedBots() {
	select {
	case <-time.After(240 * time.Second):
	case <-bm.ctx.Done():
		return
	}

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
	var wg sync.WaitGroup
	connectedBots := make([]types.Bot, 0)
	var connectedBotsMutex sync.Mutex

	for _, bot := range bm.bots {
		wg.Add(1)
		go func(bot types.Bot) {
			defer wg.Done()
			if bm.startBotWithRetry(bot) {
				connectedBotsMutex.Lock()
				connectedBots = append(connectedBots, bot)
				connectedBotsMutex.Unlock()
			} else {
				util.Warning("Bot %s failed to connect within startup window, removing", bot.GetCurrentNick())
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
		select {
		case <-bm.ctx.Done():
			return false
		default:
		}

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
		select {
		case <-time.After(retryInterval):
		case <-bm.ctx.Done():
			return false
		}
	}
}

// Stop safely shuts down all bots
func (bm *BotManager) Stop() {
	bm.cancel()
	close(bm.stopChan)
	bm.wg.Wait()
	if bm.nickManager != nil {
		bm.nickManager.Stop()
	}
	for _, bot := range bm.bots {
		bot.Quit("Shutting down")
	}
	util.Info("All bots have been stopped.")
}

// RemoveBotFromManager usuwa bota z managera
func (bm *BotManager) RemoveBotFromManager(botToRemove types.Bot) {
	// Najpierw sprawdzamy, czy bot istnieje w liście
	var found bool
	var currentNick string

	bm.mutex.RLock()
	for _, bot := range bm.bots {
		if bot == botToRemove {
			found = true
			currentNick = botToRemove.GetCurrentNick()
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

	if sink := bm.currentSink(); sink != nil {
		sink.BotRemoved(botToRemove.GetBotID())
	}

	// Aktualizujemy NickManager w osobnej goroutine, aby nie blokować
	if bm.nickManager != nil {
		go func() {
			// Zwracamy nick do puli
			bm.nickManager.ReturnNickToPool(currentNick)

			// Wyrejestrowujemy bota z NickManagera
			nm := bm.nickManager.(*nickmanager.NickManager)
			nm.UnregisterBot(botToRemove)
		}()
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
	for _, owner := range bm.owners.Owners {
		if owner == ownerMask {
			bm.mutex.Unlock()
			return fmt.Errorf("owner '%s' already exists", ownerMask)
		}
	}
	bm.owners.Owners = append(bm.owners.Owners, ownerMask)
	snapshot, bots, list, err := bm.prepareOwnersSnapshotLocked()
	bm.mutex.Unlock()
	if err != nil {
		return err
	}
	return bm.persistOwners(snapshot, bots, list)
}

func (bm *BotManager) RemoveOwner(ownerMask string) error {
	bm.mutex.Lock()
	index := -1
	for i, owner := range bm.owners.Owners {
		if owner == ownerMask {
			index = i
			break
		}
	}
	if index == -1 {
		bm.mutex.Unlock()
		return fmt.Errorf("owner '%s' not found", ownerMask)
	}
	bm.owners.Owners = append(bm.owners.Owners[:index], bm.owners.Owners[index+1:]...)
	snapshot, bots, list, err := bm.prepareOwnersSnapshotLocked()
	bm.mutex.Unlock()
	if err != nil {
		return err
	}
	return bm.persistOwners(snapshot, bots, list)
}

// prepareOwnersSnapshotLocked serializes the current owner list and snapshots
// the bot slice. Called with bm.mutex held (read or write); returns values
// the caller can safely use after releasing the lock.
func (bm *BotManager) prepareOwnersSnapshotLocked() ([]byte, []types.Bot, []string, error) {
	data, err := json.MarshalIndent(bm.owners, "", "  ")
	if err != nil {
		return nil, nil, nil, err
	}
	bots := make([]types.Bot, len(bm.bots))
	copy(bots, bm.bots)
	list := make([]string, len(bm.owners.Owners))
	copy(list, bm.owners.Owners)
	return data, bots, list, nil
}

// persistOwners writes the serialized owner list to disk, fans the updated
// OwnerList out to bots, and emits OwnersChanged on the sink. Caller must
// not hold bm.mutex.
func (bm *BotManager) persistOwners(data []byte, bots []types.Bot, list []string) error {
	if err := os.WriteFile("configs/owners.json", data, 0644); err != nil {
		return err
	}
	snapshot := auth.OwnerList{Owners: list}
	for _, b := range bots {
		b.SetOwnerList(snapshot)
	}
	if sink := bm.currentSink(); sink != nil {
		sink.OwnersChanged(list)
	}
	return nil
}

func (bm *BotManager) GetOwners() []string {
	// Używamy RLock zamiast Lock, aby umożliwić równoczesny odczyt
	bm.mutex.RLock()
	ownersCopy := make([]string, len(bm.owners.Owners))
	copy(ownersCopy, bm.owners.Owners)
	bm.mutex.RUnlock()
	return ownersCopy
}

// GetBots returns a copy of the bot slice
func (bm *BotManager) GetBots() []types.Bot {
	bm.mutex.RLock()
	botsCopy := make([]types.Bot, len(bm.bots))
	copy(botsCopy, bm.bots)
	bm.mutex.RUnlock()
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
	if len(bm.bots) == 0 {
		bm.mutex.Unlock()
		return
	}

	start := bm.commandBotIndex % len(bm.bots)
	var bot types.Bot
	channelTarget := isChannelTarget(channel)
	for i := 0; i < len(bm.bots); i++ {
		idx := (start + i) % len(bm.bots)
		candidate := bm.bots[idx]
		if candidate == nil || !candidate.IsConnected() {
			continue
		}
		if channelTarget && !candidate.IsOnChannel(channel) {
			continue
		}
		bot = candidate
		bm.commandBotIndex = (idx + 1) % len(bm.bots)
		break
	}
	bm.mutex.Unlock()

	if bot == nil {
		if channelTarget {
			util.Warning("No connected bot currently joined to %s; skipping reply", channel)
		}
		return
	}

	bot.SendMessage(channel, message)
}

func isChannelTarget(target string) bool {
	if target == "" {
		return false
	}

	switch {
	case strings.HasPrefix(target, "#"):
		return true
	case strings.HasPrefix(target, "&"):
		return true
	case strings.HasPrefix(target, "+"):
		return true
	case strings.HasPrefix(target, "!"):
		return true
	default:
		return false
	}
}

func (bm *BotManager) GetTotalCreatedBots() int {
	return bm.totalCreatedBots
}
