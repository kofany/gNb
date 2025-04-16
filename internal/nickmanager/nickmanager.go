package nickmanager

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/kofany/gNb/internal/types"
	"github.com/kofany/gNb/internal/util"
)

type NickManager struct {
	nicksToCatch         []string
	priorityNicks        []string
	secondaryNicks       []string
	bots                 []types.Bot
	connectedBots        []types.Bot // Nowe pole dla połączonych botów
	botIndex             int
	isonInterval         time.Duration
	tempUnavailableNicks map[string]time.Time
	NoLettersServers     map[string]bool
	mutex                sync.RWMutex         // Zmiana na RWMutex dla lepszej wydajności
	connectedBotsMutex   sync.RWMutex         // Osobny mutex dla listy połączonych botów
	lastConnectedUpdate  time.Time            // Timestamp ostatniej aktualizacji listy
	updateInterval       time.Duration        // Interwał odświeżania listy połączonych botów
	expectedNicks        map[types.Bot]string // Mapa oczekiwanych nicków dla botów
	expectedNickMutex    sync.RWMutex         // Mutex dla mapy oczekiwanych nicków
	activeISONRequests   sync.Map             // Mapa aktywnych zapytań ISON per bot
}

type NicksData struct {
	Nicks []string `json:"nicks"`
}

const tempUnavailableTimeout = time.Duration(60) * time.Second // blokada na 60 sekund

func NewNickManager() *NickManager {
	return &NickManager{
		tempUnavailableNicks: make(map[string]time.Time),
		NoLettersServers:     make(map[string]bool),
		connectedBots:        make([]types.Bot, 0, 1000), // Prealokacja z przewidywanym rozmiarem
		updateInterval:       10 * time.Second,           // Aktualizacja co 10 sekund
		expectedNicks:        make(map[types.Bot]string), // Inicjalizacja mapy oczekiwanych nicków
		isonInterval:         1 * time.Second,            // Domyślny interwał ISON
	}
}

func (nm *NickManager) updateConnectedBots() {
	util.Debug("NickManager: Updating connected bots list")

	// Kopiujemy listę wszystkich botów pod RLock
	nm.mutex.RLock()
	allBots := make([]types.Bot, len(nm.bots))
	copy(allBots, nm.bots)
	nm.mutex.RUnlock()

	// Sprawdzamy, które boty są połączone
	newConnected := make([]types.Bot, 0, len(allBots))
	for _, bot := range allBots {
		// Dodajemy dodatkowe sprawdzenie, czy bot nie jest nil
		if bot != nil && bot.IsConnected() {
			newConnected = append(newConnected, bot)
		}
	}

	// Aktualizujemy listę połączonych botów
	nm.connectedBotsMutex.Lock()
	nm.connectedBots = newConnected
	nm.lastConnectedUpdate = time.Now()
	nm.connectedBotsMutex.Unlock()

	util.Debug("NickManager: Connected bots list updated, %d bots connected", len(newConnected))
}

func (nm *NickManager) LoadNicks(filename string) error {
	nm.mutex.Lock()
	defer nm.mutex.Unlock()

	data, err := os.ReadFile(filename)
	if err != nil {
		return err
	}

	var nicksData NicksData
	if err := json.Unmarshal(data, &nicksData); err != nil {
		return err
	}

	nm.priorityNicks = nicksData.Nicks

	// Add single-letter nicks to secondary nicks
	letters := "abcdefghijklmnopqrstuvwxyz"
	for _, c := range letters {
		nick := string(c)
		nm.secondaryNicks = append(nm.secondaryNicks, nick)
	}

	// Combine both lists into nicksToCatch
	nm.nicksToCatch = append(nm.priorityNicks, nm.secondaryNicks...)

	return nil
}

func (nm *NickManager) Start() {
	go nm.monitorNicks()

	// Uruchom periodyczną weryfikację spójności nicków co 5 minut
	go func() {
		verifyTicker := time.NewTicker(5 * time.Minute)
		for range verifyTicker.C {
			nm.verifyAllBotsNickState()
		}
	}()
}

func (nm *NickManager) monitorNicks() {
	updateTicker := time.NewTicker(nm.updateInterval)
	isonTicker := time.NewTicker(nm.isonInterval)

	go func() {
		for range updateTicker.C {
			nm.updateConnectedBots()
		}
	}()

	for range isonTicker.C {
		nm.connectedBotsMutex.RLock()
		connectedBots := nm.connectedBots
		botsCount := len(connectedBots)
		if botsCount == 0 {
			nm.connectedBotsMutex.RUnlock()
			continue
		}

		// Używamy lokalnego indeksu dla połączonych botów
		localIndex := nm.botIndex % botsCount
		bot := connectedBots[localIndex]
		nm.botIndex = (nm.botIndex + 1) % botsCount
		nm.connectedBotsMutex.RUnlock()

		// Sprawdź czy bot nie ma już aktywnego zapytania ISON
		if _, busy := nm.activeISONRequests.LoadOrStore(bot, true); busy {
			// Bot już ma aktywne zapytanie, pomijamy
			util.Debug("NickManager: Bot %s already has an active ISON request, skipping", bot.GetCurrentNick())
			continue
		}

		// Request ISON and wait for response
		// Wykonujemy w osobnej goroutine, aby nie blokować głównej pętli
		go func(currentBot types.Bot) {
			// Zawsze usuwamy flagę aktywnego zapytania po zakończeniu
			defer nm.activeISONRequests.Delete(currentBot)

			// Sprawdzamy, czy bot jest nadal połączony
			if !currentBot.IsConnected() {
				util.Warning("NickManager: Bot %s is no longer connected, skipping ISON request", currentBot.GetCurrentNick())
				return
			}

			// Kopiujemy listę nicków do złapania
			nm.mutex.RLock()
			if len(nm.nicksToCatch) == 0 {
				nm.mutex.RUnlock()
				util.Debug("NickManager: No nicks to catch, skipping ISON request")
				return
			}

			nicksCopy := make([]string, len(nm.nicksToCatch))
			copy(nicksCopy, nm.nicksToCatch)
			nm.mutex.RUnlock()

			// Wysyłamy zapytanie ISON z timeoutem
			doneChan := make(chan struct{})
			var onlineNicks []string
			var err error

			go func() {
				onlineNicks, err = currentBot.RequestISON(nicksCopy)
				close(doneChan)
			}()

			// Czekamy na zakończenie zapytania z timeoutem
			select {
			case <-doneChan:
				// Zapytanie zakończone
				if err != nil {
					util.Error("Error requesting ISON from bot %s: %v", currentBot.GetCurrentNick(), err)
				} else {
					nm.handleISONResponse(onlineNicks)
				}
			case <-time.After(2 * time.Second): // Zmniejszony timeout z 6s na 2s
				// Timeout - zapytanie trwa zbyt długo
				util.Warning("NickManager: ISON request to bot %s timed out", currentBot.GetCurrentNick())
			}
		}(bot)
	}
}

// Zmodyfikowana funkcja do rejestracji bota
func (nm *NickManager) RegisterBot(bot types.Bot) {
	nm.mutex.Lock()
	nm.bots = append(nm.bots, bot)
	nm.mutex.Unlock()

	// Jeśli bot jest połączony, dodaj go do listy połączonych
	if bot.IsConnected() {
		nm.connectedBotsMutex.Lock()
		nm.connectedBots = append(nm.connectedBots, bot)
		nm.connectedBotsMutex.Unlock()
	}
}

// UnregisterBot wyrejestrowuje bota z NickManagera
func (nm *NickManager) UnregisterBot(botToRemove types.Bot) {
	util.Debug("NickManager: Unregistering bot %s", botToRemove.GetCurrentNick())

	// Usuń z głównej listy botów
	nm.mutex.Lock()
	newBots := make([]types.Bot, 0, len(nm.bots))
	for _, bot := range nm.bots {
		if bot != botToRemove {
			newBots = append(newBots, bot)
		}
	}
	nm.bots = newBots
	nm.mutex.Unlock()

	// Usuń z listy połączonych botów
	nm.connectedBotsMutex.Lock()
	newConnected := make([]types.Bot, 0, len(nm.connectedBots))
	for _, bot := range nm.connectedBots {
		if bot != botToRemove {
			newConnected = append(newConnected, bot)
		}
	}
	nm.connectedBots = newConnected
	nm.connectedBotsMutex.Unlock()

	util.Debug("NickManager: Bot %s unregistered successfully", botToRemove.GetCurrentNick())
}

func (nm *NickManager) handleISONResponse(onlineNicks []string) {
	// Sprawdzamy czy otrzymaliśmy jakąś odpowiedź
	if onlineNicks == nil {
		util.Warning("NickManager received nil ISON response")
		return
	}

	// Blokujemy mutex tylko na czas niezbędnych operacji
	nm.mutex.Lock()

	util.Debug("NickManager received ISON response: %v", onlineNicks)

	currentTime := time.Now()
	nm.cleanupTempUnavailableNicks(currentTime)

	// Kopiujemy listy nicków, aby zminimalizować czas blokowania mutexa
	priorityNicksCopy := make([]string, len(nm.priorityNicks))
	copy(priorityNicksCopy, nm.priorityNicks)

	secondaryNicksCopy := make([]string, len(nm.secondaryNicks))
	copy(secondaryNicksCopy, nm.secondaryNicks)

	// Pobieramy dostępne boty
	availableBots := nm.getAvailableBots()
	if len(availableBots) == 0 {
		nm.mutex.Unlock()
		util.Debug("No available bots to assign nicks")
		return
	}

	// Kopiujemy mapę serwerów bez liter
	noLettersServersCopy := make(map[string]bool)
	for server, value := range nm.NoLettersServers {
		noLettersServersCopy[server] = value
	}

	nm.mutex.Unlock()

	// Filtrujemy dostępne nicki bez blokowania mutexa
	availablePriorityNicks := nm.filterAvailableNicksNonLocking(priorityNicksCopy, onlineNicks)
	availableSecondaryNicks := nm.filterAvailableNicksNonLocking(secondaryNicksCopy, onlineNicks)

	// Teraz musimy zablokować mutex, aby zaktualizować stan
	nm.mutex.Lock()

	assignedBots := 0

	// Assign priority nicks first
	for assignedBots < len(availableBots) && len(availablePriorityNicks) > 0 {
		nick := availablePriorityNicks[0]
		availablePriorityNicks = availablePriorityNicks[1:]
		bot := availableBots[assignedBots]

		// Skip single-letter nicks for servers that don't accept them
		if len(nick) == 1 && nm.NoLettersServers[bot.GetServerName()] {
			util.Debug("Skipping single-letter nick %s for server %s", nick, bot.GetServerName())
			continue
		}

		assignedBots++
		util.Debug("Assigning priority nick %s to bot %s on server %s", nick, bot.GetCurrentNick(), bot.GetServerName())
		go bot.AttemptNickChange(nick)
	}

	// Then assign secondary nicks
	for assignedBots < len(availableBots) && len(availableSecondaryNicks) > 0 {
		nick := availableSecondaryNicks[0]
		availableSecondaryNicks = availableSecondaryNicks[1:]
		bot := availableBots[assignedBots]

		// Skip single-letter nicks for servers that don't accept them
		if len(nick) == 1 && nm.NoLettersServers[bot.GetServerName()] {
			util.Debug("Skipping single-letter nick %s for server %s", nick, bot.GetServerName())
			continue
		}

		assignedBots++
		util.Debug("Assigning secondary nick %s to bot %s on server %s", nick, bot.GetCurrentNick(), bot.GetServerName())
		go bot.AttemptNickChange(nick)
	}

	// Odblokowujemy mutex
	nm.mutex.Unlock()
}

func (nm *NickManager) SetBots(bots []types.Bot) {
	nm.mutex.Lock()
	defer nm.mutex.Unlock()
	nm.bots = bots
}

func (nm *NickManager) getAvailableBots() []types.Bot {
	var availableBots []types.Bot
	for _, bot := range nm.bots {
		if bot.IsConnected() && !util.IsTargetNick(bot.GetCurrentNick(), nm.nicksToCatch) {
			availableBots = append(availableBots, bot)
		}
	}
	return availableBots
}

func (nm *NickManager) ReturnNickToPool(nick string) {
	nm.mutex.Lock()
	defer nm.mutex.Unlock()

	// Sprawdź, czy nick jest w pliku nicks.json lub jest pojedynczą literą
	if util.IsTargetNick(nick, nm.priorityNicks) || (len(nick) == 1 && nick >= "a" && nick <= "z") {
		delete(nm.tempUnavailableNicks, strings.ToLower(nick))

		// Natychmiast spróbuj przydzielić ten nick innemu botowi
		availableBots := nm.getAvailableBots()
		if len(availableBots) > 0 {
			go availableBots[0].AttemptNickChange(nick)
		}
	}
}

func (nm *NickManager) GetNicksToCatch() []string {
	nm.mutex.Lock()
	defer nm.mutex.Unlock()
	nicksCopy := make([]string, len(nm.nicksToCatch))
	copy(nicksCopy, nm.nicksToCatch)
	return nicksCopy
}

func (nm *NickManager) AddNick(nick string) error {
	nm.mutex.Lock()
	defer nm.mutex.Unlock()

	// Sprawdź, czy nick już istnieje
	for _, n := range nm.priorityNicks {
		if n == nick {
			return fmt.Errorf("nick '%s' already exists", nick)
		}
	}

	// Dodaj nick do listy priorytetowej
	nm.priorityNicks = append(nm.priorityNicks, nick)
	nm.nicksToCatch = append(nm.nicksToCatch, nick)

	// Zapisz do pliku
	return nm.saveNicksToFile()
}

func (nm *NickManager) RemoveNick(nick string) error {
	nm.mutex.Lock()
	defer nm.mutex.Unlock()

	// Usuń nick z listy priorytetowej
	index := -1
	for i, n := range nm.priorityNicks {
		if n == nick {
			index = i
			break
		}
	}

	if index == -1 {
		return fmt.Errorf("nick '%s' not found", nick)
	}

	nm.priorityNicks = append(nm.priorityNicks[:index], nm.priorityNicks[index+1:]...)

	// Usuń nick z listy nicksToCatch
	index = -1
	for i, n := range nm.nicksToCatch {
		if n == nick {
			index = i
			break
		}
	}

	if index != -1 {
		nm.nicksToCatch = append(nm.nicksToCatch[:index], nm.nicksToCatch[index+1:]...)
	}

	// Zapisz do pliku
	return nm.saveNicksToFile()
}

func (nm *NickManager) GetNicks() []string {
	nm.mutex.Lock()
	defer nm.mutex.Unlock()

	nicksCopy := make([]string, len(nm.priorityNicks))
	copy(nicksCopy, nm.priorityNicks)
	return nicksCopy
}

func (nm *NickManager) saveNicksToFile() error {
	data := NicksData{
		Nicks: nm.priorityNicks,
	}

	jsonData, err := json.MarshalIndent(data, "", "  ")
	if err != nil {
		return err
	}

	return os.WriteFile("data/nicks.json", jsonData, 0644)
}

func (nm *NickManager) cleanupTempUnavailableNicks(currentTime time.Time) {
	for nick, unblockTime := range nm.tempUnavailableNicks {
		if currentTime.After(unblockTime) {
			delete(nm.tempUnavailableNicks, nick)
		}
	}
}

// filterAvailableNicks - wersja z blokadą mutexa
func (nm *NickManager) filterAvailableNicks(nicks []string, onlineNicks []string) []string {
	currentTime := time.Now()
	var available []string

	for _, nick := range nicks {
		lowerNick := strings.ToLower(nick)
		if !util.ContainsIgnoreCase(onlineNicks, nick) {
			// Sprawdź czy nick nie jest zablokowany
			if blockTime, exists := nm.tempUnavailableNicks[lowerNick]; exists {
				if currentTime.After(blockTime) {
					// Blokada wygasła, usuń ją
					delete(nm.tempUnavailableNicks, lowerNick)
					available = append(available, nick)
					util.Debug("Nick %s block expired, removing block", nick)
				} else {
					util.Debug("Nick %s still blocked for %v", nick, blockTime.Sub(currentTime))
				}
			} else {
				available = append(available, nick)
			}
		}
	}
	return available
}

// filterAvailableNicksNonLocking - wersja bez blokady mutexa
func (nm *NickManager) filterAvailableNicksNonLocking(nicks []string, onlineNicks []string) []string {
	currentTime := time.Now()
	var available []string

	// Kopiujemy mapę tymczasowo niedostępnych nicków
	nm.mutex.Lock()
	tempUnavailableCopy := make(map[string]time.Time)
	for nick, blockTime := range nm.tempUnavailableNicks {
		tempUnavailableCopy[nick] = blockTime
	}
	nm.mutex.Unlock()

	// Filtrujemy dostępne nicki bez blokowania mutexa
	for _, nick := range nicks {
		lowerNick := strings.ToLower(nick)
		if !util.ContainsIgnoreCase(onlineNicks, nick) {
			// Sprawdź czy nick nie jest zablokowany
			if blockTime, exists := tempUnavailableCopy[lowerNick]; exists {
				if currentTime.After(blockTime) {
					// Blokada wygasła, usuniemy ją później
					available = append(available, nick)
					util.Debug("Nick %s block expired, will be unblocked", nick)

					// Aktualizujemy mapę tymczasowo niedostępnych nicków
					nm.mutex.Lock()
					delete(nm.tempUnavailableNicks, lowerNick)
					nm.mutex.Unlock()
				} else {
					util.Debug("Nick %s still blocked for %v", nick, blockTime.Sub(currentTime))
				}
			} else {
				available = append(available, nick)
			}
		}
	}
	return available
}

func (nm *NickManager) MarkNickAsTemporarilyUnavailable(nick string) {
	nm.mutex.Lock()
	defer nm.mutex.Unlock()

	// Ustaw czas wygaśnięcia blokady
	nm.tempUnavailableNicks[strings.ToLower(nick)] = time.Now().Add(tempUnavailableTimeout)
	util.Debug("Nick %s marked as temporarily unavailable until %v",
		nick, time.Now().Add(tempUnavailableTimeout))
}

func (nm *NickManager) NotifyNickChange(oldNick, newNick string) {
	nm.mutex.Lock()
	defer nm.mutex.Unlock()

	if util.IsTargetNick(oldNick, nm.nicksToCatch) {
		// Oznacz stary nick jako dostępny
		delete(nm.tempUnavailableNicks, strings.ToLower(oldNick))

		// Natychmiast spróbuj przydzielić ten nick innemu botowi
		availableBots := nm.getAvailableBots()
		if len(availableBots) > 0 {
			go availableBots[0].AttemptNickChange(oldNick)
		}
	}

	// Aktualizuj oczekiwany nick dla bota
	for _, bot := range nm.bots {
		if bot.GetCurrentNick() == newNick {
			nm.expectedNickMutex.Lock()
			nm.expectedNicks[bot] = newNick
			nm.expectedNickMutex.Unlock()
			break
		}
	}

	// Nie oznaczamy nowego nicka jako tymczasowo niedostępnego,
	// ponieważ jest to losowy nick, a nie z puli do łapania
}

func (nm *NickManager) MarkServerNoLetters(serverName string) {
	nm.mutex.Lock()
	defer nm.mutex.Unlock()
	nm.NoLettersServers[serverName] = true
}

// NickChangeFailed obsługuje przypadki, gdy zmiana nicka nie powiodła się
func (nm *NickManager) NickChangeFailed(oldNick, newNick string) {
	nm.mutex.Lock()
	defer nm.mutex.Unlock()

	// Usuń z listy tymczasowo niedostępnych, aby był dostępny ponownie
	delete(nm.tempUnavailableNicks, strings.ToLower(newNick))

	// Dodaj z powrotem do puli do złapania, jeśli to taki nick
	if util.IsTargetNick(newNick, nm.nicksToCatch) {
		util.Debug("Nick %s returned to pool after failed change", newNick)
	}
}

// verifyAllBotsNickState weryfikuje spójność stanu nicków botów
func (nm *NickManager) verifyAllBotsNickState() {
	util.Debug("NickManager: Verifying nick state consistency for all bots")

	// Kopiujemy listę botów pod RLock
	nm.mutex.RLock()
	allBots := make([]types.Bot, len(nm.bots))
	copy(allBots, nm.bots)
	nm.mutex.RUnlock()

	// Kopiujemy mapę oczekiwanych nicków
	nm.expectedNickMutex.RLock()
	expectedNicksCopy := make(map[types.Bot]string)
	for bot, nick := range nm.expectedNicks {
		expectedNicksCopy[bot] = nick
	}
	nm.expectedNickMutex.RUnlock()

	// Sprawdzamy każdego bota
	for _, bot := range allBots {
		if !bot.IsConnected() {
			continue
		}

		currentNick := bot.GetCurrentNick()
		expectedNick, exists := expectedNicksCopy[bot]

		// Jeśli nie ma oczekiwanego nicka, zaktualizuj go
		if !exists {
			nm.expectedNickMutex.Lock()
			nm.expectedNicks[bot] = currentNick
			nm.expectedNickMutex.Unlock()
			continue
		}

		// Jeśli aktualny nick nie zgadza się z oczekiwanym
		if currentNick != expectedNick {
			util.Warning("NickManager: Nick state inconsistency detected for bot %s (current: %s, expected: %s)",
				currentNick, currentNick, expectedNick)

			// Aktualizuj oczekiwany nick, aby odzwierciedlał rzeczywistość
			nm.expectedNickMutex.Lock()
			nm.expectedNicks[bot] = currentNick
			nm.expectedNickMutex.Unlock()

			// Jeśli oczekiwany nick był z puli do złapania, zwróć go do puli
			if util.IsTargetNick(expectedNick, nm.GetNicksToCatch()) {
				util.Debug("NickManager: Returning nick %s to pool after inconsistency detection", expectedNick)
				nm.ReturnNickToPool(expectedNick)
			}
		}
	}

	util.Debug("NickManager: Nick state verification completed")
}

// SetISONInterval ustawia interwał zapytań ISON
func (nm *NickManager) SetISONInterval(interval time.Duration) {
	nm.mutex.Lock()
	defer nm.mutex.Unlock()

	if interval < 1*time.Second {
		util.Warning("NickManager: ISON interval too small (%v), using 1 second minimum", interval)
		interval = 1 * time.Second
	}

	nm.isonInterval = interval
	util.Debug("NickManager: ISON interval set to %v", interval)
}

// UpdateExpectedNick aktualizuje oczekiwany nick dla bota
func (nm *NickManager) UpdateExpectedNick(bot types.Bot, nick string) {
	nm.expectedNickMutex.Lock()
	defer nm.expectedNickMutex.Unlock()

	nm.expectedNicks[bot] = nick
	util.Debug("NickManager: Updated expected nick for bot %s to %s", bot.GetCurrentNick(), nick)
}
