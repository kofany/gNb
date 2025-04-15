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
	botIndex             int
	tempUnavailableNicks map[string]time.Time
	NoLettersServers     map[string]bool
	mutex                sync.RWMutex  // Zmiana na RWMutex dla lepszej wydajności
	connectedBotsMutex   sync.RWMutex  // Osobny mutex dla listy połączonych botów
	lastConnectedUpdate  time.Time     // Timestamp ostatniej aktualizacji listy
	updateInterval       time.Duration // Interwał odświeżania listy połączonych botów
	isonTicker           *time.Ticker  // Ticker dla zapytań ISON
	updateTicker         *time.Ticker  // Ticker dla aktualizacji listy botów
	isRunning            bool          // Flaga wskazująca, czy monitorowanie jest aktywne
	stopChan             chan struct{} // Kanał do zatrzymania monitorowania
	// Statystyki dla debugowania
	isonRequestsSent      int64      // Licznik wysłanych zapytań ISON
	isonResponsesReceived int64      // Licznik otrzymanych odpowiedzi ISON
	isonStats             sync.Mutex // Mutex dla statystyk ISON
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
		stopChan:             make(chan struct{}),        // Inicjalizacja kanału stop
	}
}

// UpdateConnectedBots aktualizuje listę połączonych botów
func (nm *NickManager) UpdateConnectedBots() {
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
	nm.mutex.Lock()
	if nm.isRunning {
		nm.mutex.Unlock()
		return
	}
	nm.isRunning = true
	nm.mutex.Unlock()

	// Inicjalizacja tickerów
	nm.isonTicker = time.NewTicker(1 * time.Second)
	nm.updateTicker = time.NewTicker(nm.updateInterval)

	// Uruchomienie monitorowania
	go nm.monitorNicks()
	util.Info("NickManager: Monitoring started")
}

// Stop zatrzymuje monitorowanie nicków
func (nm *NickManager) Stop() {
	nm.mutex.Lock()
	if !nm.isRunning {
		nm.mutex.Unlock()
		return
	}

	// Zatrzymujemy tickery
	if nm.isonTicker != nil {
		nm.isonTicker.Stop()
	}
	if nm.updateTicker != nil {
		nm.updateTicker.Stop()
	}

	// Sygnalizujemy zatrzymanie
	close(nm.stopChan)
	nm.isRunning = false
	nm.mutex.Unlock()

	util.Info("NickManager: Monitoring stopped")
}

// Restart restartuje monitorowanie nicków
func (nm *NickManager) Restart() {
	nm.Stop()
	// Tworzymy nowy kanał stop
	nm.mutex.Lock()
	nm.stopChan = make(chan struct{})
	nm.mutex.Unlock()
	// Uruchamiamy ponownie
	nm.Start()
	util.Info("NickManager: Monitoring restarted")
}

func (nm *NickManager) monitorNicks() {
	// Uruchamiamy goroutine do aktualizacji listy połączonych botów
	go func() {
		for {
			select {
			case <-nm.updateTicker.C:
				nm.UpdateConnectedBots()
			case <-nm.stopChan:
				return
			}
		}
	}()

	// Główna pętla monitorowania
	for {
		select {
		case <-nm.isonTicker.C:
			// Pobieramy listę połączonych botów
			nm.connectedBotsMutex.RLock()
			connectedBots := nm.connectedBots
			botsCount := len(connectedBots)
			nm.connectedBotsMutex.RUnlock()

			if botsCount == 0 {
				continue
			}

			// Używamy lokalnego indeksu dla połączonych botów
			nm.mutex.Lock()
			localIndex := nm.botIndex % botsCount
			nm.botIndex = (nm.botIndex + 1) % botsCount
			nm.mutex.Unlock()

			bot := connectedBots[localIndex]

			// Wykonujemy zapytanie ISON w osobnej goroutine
			go nm.performISONRequest(bot)

		case <-nm.stopChan:
			// Zatrzymujemy monitorowanie
			return
		}
	}
}

// performISONRequest wykonuje zapytanie ISON dla danego bota
func (nm *NickManager) performISONRequest(currentBot types.Bot) {
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

	// Aktualizujemy licznik wysłanych zapytań ISON
	nm.isonStats.Lock()
	nm.isonRequestsSent++
	requestCount := nm.isonRequestsSent
	nm.isonStats.Unlock()

	// Logujemy co 100 zapytań
	if requestCount%100 == 0 {
		util.Info("NickManager: ISON stats - Sent: %d, Received: %d",
			requestCount, atomic.LoadInt64(&nm.isonResponsesReceived))
	}

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
			// Aktualizujemy licznik otrzymanych odpowiedzi
			atomic.AddInt64(&nm.isonResponsesReceived, 1)
			// Przetwarzamy odpowiedź
			nm.handleISONResponse(onlineNicks)
		}
	case <-time.After(6 * time.Second):
		// Timeout - zapytanie trwa zbyt długo
		util.Warning("NickManager: ISON request to bot %s timed out", currentBot.GetCurrentNick())
	}
}

// IncrementFailedRequestCount - implementacja interfejsu, ale nie używana
func (nm *NickManager) IncrementFailedRequestCount(bot types.Bot) {
	// Funkcja pozostawiona dla zgodności z interfejsem, ale nie robi nic
	// Usunięto koncepcję nieudanych prób ISON
}

// ResetFailedRequestCount - implementacja interfejsu, ale nie używana
func (nm *NickManager) ResetFailedRequestCount(bot types.Bot) {
	// Funkcja pozostawiona dla zgodności z interfejsem, ale nie robi nic
	// Usunięto koncepcję nieudanych prób ISON
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

	// Logujemy co 100 odpowiedzi
	responseCount := atomic.LoadInt64(&nm.isonResponsesReceived)
	if responseCount%100 == 0 {
		util.Debug("NickManager received ISON response #%d: %v", responseCount, onlineNicks)
	}

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
	noLettersServersCopy := make(map[string]bool, len(nm.NoLettersServers))
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

// filterAvailableNicksNonLocking - wersja bez blokady mutexa
func (nm *NickManager) filterAvailableNicksNonLocking(nicks []string, onlineNicks []string) []string {
	currentTime := time.Now()
	var available []string

	// Kopiujemy mapę tymczasowo niedostępnych nicków
	nm.mutex.Lock()
	tempUnavailableCopy := make(map[string]time.Time, len(nm.tempUnavailableNicks))
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

	lowerNick := strings.ToLower(nick)
	// Sprawdzamy, czy nick jest już zablokowany
	if blockTime, exists := nm.tempUnavailableNicks[lowerNick]; exists {
		// Jeśli jest już zablokowany, zwiększamy czas blokady o 30 sekund
		newBlockTime := blockTime.Add(30 * time.Second)
		nm.tempUnavailableNicks[lowerNick] = newBlockTime
		util.Debug("Nick %s block extended until %v", nick, newBlockTime)
	} else {
		// Ustaw czas wygaśnięcia blokady
		nm.tempUnavailableNicks[lowerNick] = time.Now().Add(tempUnavailableTimeout)
		util.Debug("Nick %s marked as temporarily unavailable until %v",
			nick, time.Now().Add(tempUnavailableTimeout))
	}
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

	// Nie oznaczamy nowego nicka jako tymczasowo niedostępnego,
	// ponieważ jest to losowy nick, a nie z puli do łapania
}

func (nm *NickManager) MarkServerNoLetters(serverName string) {
	nm.mutex.Lock()
	defer nm.mutex.Unlock()
	nm.NoLettersServers[serverName] = true
}
