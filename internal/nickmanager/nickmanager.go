package nickmanager

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"sync"
	"sync/atomic"
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
	botIndex             int32       // Zmienione na int32 dla atomic operations
	tempUnavailableNicks map[string]time.Time
	NoLettersServers     map[string]bool
	mutex                sync.RWMutex  // Zmiana na RWMutex dla lepszej wydajności
	connectedBotsMutex   sync.RWMutex  // Osobny mutex dla listy połączonych botów
	lastConnectedUpdate  time.Time     // Timestamp ostatniej aktualizacji listy
	updateInterval       time.Duration // Interwał odświeżania listy połączonych botów
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
	}
}

func (nm *NickManager) updateConnectedBots() {
	util.Debug("NickManager: Updating connected bots list")

	// Pobieramy listę wszystkich botów pod RLock
	nm.mutex.RLock()
	if len(nm.bots) == 0 {
		nm.mutex.RUnlock()
		return
	}

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
}

func (nm *NickManager) monitorNicks() {
	updateTicker := time.NewTicker(nm.updateInterval)
	isonTicker := time.NewTicker(1 * time.Second)
	defer updateTicker.Stop()
	defer isonTicker.Stop()

	for {
		select {
		case <-updateTicker.C:
			nm.updateConnectedBots()
		case <-isonTicker.C:
			// Uruchamiamy processISON w osobnej goroutinie aby nie blokować głównej pętli
			go nm.processISON()
		default:
			// Dodajemy default aby uniknąć blokowania
			time.Sleep(100 * time.Millisecond)
		}
	}
}

func (nm *NickManager) processISON() {
	nm.connectedBotsMutex.RLock()
	if len(nm.connectedBots) == 0 {
		nm.connectedBotsMutex.RUnlock()
		return
	}

	localIndex := int(atomic.AddInt32(&nm.botIndex, 1))
	botsCount := len(nm.connectedBots)
	bot := nm.connectedBots[localIndex%botsCount]
	nm.connectedBotsMutex.RUnlock()

	if !bot.IsConnected() {
		return
	}

	// Kopiujemy listę nicków do złapania
	nm.mutex.RLock()
	if len(nm.nicksToCatch) == 0 {
		nm.mutex.RUnlock()
		return
	}
	nicksCopy := make([]string, len(nm.nicksToCatch))
	copy(nicksCopy, nm.nicksToCatch)
	nm.mutex.RUnlock()

	// Wykonujemy ISON bezpośrednio tutaj, bez dodatkowych goroutyn
	if onlineNicks, err := bot.RequestISON(nicksCopy); err == nil {
		nm.handleISONResponse(onlineNicks)
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

		// Atomowa próba zmiany nicka
		if nm.attemptNickChange(bot, nick) {
			assignedBots++
			util.Debug("Assigning priority nick %s to bot %s on server %s", nick, bot.GetCurrentNick(), bot.GetServerName())
		}
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

		// Atomowa próba zmiany nicka
		if nm.attemptNickChange(bot, nick) {
			assignedBots++
			util.Debug("Assigning secondary nick %s to bot %s on server %s", nick, bot.GetCurrentNick(), bot.GetServerName())
		}
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

	// Sprawdź, czy nick jest w pliku nicks.json lub jest pojedynczą literą
	if !util.IsTargetNick(nick, nm.priorityNicks) && !(len(nick) == 1 && nick >= "a" && nick <= "z") {
		nm.mutex.Unlock()
		return
	}

	// Usuwamy blokadę nicka
	delete(nm.tempUnavailableNicks, strings.ToLower(nick))

	// Pobieramy dostępne boty
	availableBots := nm.getAvailableBots()
	nm.mutex.Unlock()

	// Natychmiast spróbuj przydzielić ten nick innemu botowi
	if len(availableBots) > 0 {
		nm.attemptNickChange(availableBots[0], nick)
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
	// Tworzymy listę nicków do usunięcia, aby uniknąć modyfikacji mapy podczas iteracji
	var nicksToRemove []string
	for nick, unblockTime := range nm.tempUnavailableNicks {
		if currentTime.After(unblockTime) {
			nicksToRemove = append(nicksToRemove, nick)
		}
	}

	// Usuwamy nicki z listy
	for _, nick := range nicksToRemove {
		delete(nm.tempUnavailableNicks, nick)
		util.Debug("Nick %s block expired, removing block", nick)
	}
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

	if !util.IsTargetNick(oldNick, nm.nicksToCatch) {
		nm.mutex.Unlock()
		return
	}

	// Oznacz stary nick jako dostępny
	delete(nm.tempUnavailableNicks, strings.ToLower(oldNick))

	// Pobieramy dostępne boty
	availableBots := nm.getAvailableBots()
	nm.mutex.Unlock()

	// Natychmiast spróbuj przydzielić ten nick innemu botowi
	if len(availableBots) > 0 {
		nm.attemptNickChange(availableBots[0], oldNick)
	}

	// Nie oznaczamy nowego nicka jako tymczasowo niedostępnego,
	// ponieważ jest to losowy nick, a nie z puli do łapania
}

func (nm *NickManager) MarkServerNoLetters(serverName string) {
	nm.mutex.Lock()
	defer nm.mutex.Unlock()
	nm.NoLettersServers[serverName] = true
}

// attemptNickChange próbuje zmienić nick bota w sposób atomowy
// Zwraca true jeśli próba została podjęta, false jeśli nick jest już blokowany
func (nm *NickManager) attemptNickChange(bot types.Bot, nick string) bool {
	key := strings.ToLower(nick)
	nm.mutex.Lock()
	if _, blocked := nm.tempUnavailableNicks[key]; blocked {
		nm.mutex.Unlock()
		return false
	}
	// Oznaczamy nick jako tymczasowo niedostępny
	nm.tempUnavailableNicks[key] = time.Now().Add(tempUnavailableTimeout)
	nm.mutex.Unlock()

	// Próbujemy zmienić nick z timeoutem
	success := make(chan bool, 1)
	go func() {
		bot.AttemptNickChange(nick)
		success <- true
	}()

	select {
	case <-success:
		return true
	case <-time.After(5 * time.Second):
		// Jeśli timeout, usuwamy blokadę
		nm.mutex.Lock()
		delete(nm.tempUnavailableNicks, key)
		nm.mutex.Unlock()
		return false
	}
}
