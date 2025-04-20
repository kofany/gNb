package nickcatcher

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

// NickConfig przechowuje konfigurację systemu przechwytywania nicków
type NickConfig struct {
	Nicks []string `json:"nicks"`
}

// TempUnavailableNick reprezentuje tymczasowo niedostępny nick
type TempUnavailableNick struct {
	Nick      string
	Timestamp time.Time
	Server    string
}

// NickCatcher zarządza systemem przechwytywania nicków
type NickCatcher struct {
	priorityNicks        []string                       // Nicki priorytetowe z pliku konfiguracyjnego
	singleLetterNicks    []string                       // Nicki jednoliterowe (a-z)
	tempUnavailableNicks map[string]TempUnavailableNick // Mapa tymczasowo niedostępnych nicków
	botManager           types.BotManager               // Referencja do BotManagera
	mutex                sync.RWMutex                   // Mutex do synchronizacji dostępu do danych
	stopChan             chan struct{}                  // Kanał do zatrzymania pętli monitorowania
	botIndex             int                            // Indeks aktualnego bota do rotacji
	isRunning            bool                           // Flaga wskazująca, czy system jest uruchomiony
	unavailableTimeout   time.Duration                  // Czas, po którym nick przestaje być tymczasowo niedostępny
	serverBlacklist      map[string][]string            // Mapa serwerów i nicków, których nie obsługują
}

// NewNickCatcher tworzy nową instancję NickCatcher
func NewNickCatcher(botManager types.BotManager) (*NickCatcher, error) {
	// Wczytaj nicki priorytetowe z pliku konfiguracyjnego
	priorityNicks, err := loadNicksFromFile("data/nicks.json")
	if err != nil {
		return nil, fmt.Errorf("failed to load priority nicks: %v", err)
	}

	// Generuj nicki jednoliterowe (a-z)
	singleLetterNicks := generateSingleLetterNicks()

	// Inicjalizuj mapę serwerów i nicków, których nie obsługują
	serverBlacklist := initServerBlacklist()

	return &NickCatcher{
		priorityNicks:        priorityNicks,
		singleLetterNicks:    singleLetterNicks,
		tempUnavailableNicks: make(map[string]TempUnavailableNick),
		botManager:           botManager,
		stopChan:             make(chan struct{}),
		botIndex:             0,
		isRunning:            false,
		unavailableTimeout:   5 * time.Minute, // Domyślny timeout dla niedostępnych nicków
		serverBlacklist:      serverBlacklist,
	}, nil
}

// loadNicksFromFile wczytuje nicki z pliku JSON
func loadNicksFromFile(filePath string) ([]string, error) {
	data, err := os.ReadFile(filePath)
	if err != nil {
		return nil, err
	}

	var config NickConfig
	if err := json.Unmarshal(data, &config); err != nil {
		return nil, err
	}

	return config.Nicks, nil
}

// generateSingleLetterNicks generuje listę nicków jednoliterowych (a-z)
func generateSingleLetterNicks() []string {
	nicks := make([]string, 26)
	for i := 0; i < 26; i++ {
		nicks[i] = string('a' + rune(i))
	}
	return nicks
}

// initServerBlacklist inicjalizuje mapę serwerów i nicków, których nie obsługują
func initServerBlacklist() map[string][]string {
	return map[string][]string{
		"poznan.irc.pl": {"a", "b", "c", "d", "e", "f", "g", "h", "i", "j", "k", "l", "m", "n", "o", "p", "q", "r", "s", "t", "u", "v", "w", "x", "y", "z"},
		// Dodaj więcej serwerów i nicków według potrzeb
	}
}

// Start uruchamia pętlę monitorowania nicków
func (nc *NickCatcher) Start() {
	nc.mutex.Lock()
	if nc.isRunning {
		nc.mutex.Unlock()
		return
	}
	nc.isRunning = true
	nc.mutex.Unlock()

	util.Info("Starting nick catcher system")
	go nc.monitorNicks()
}

// Stop zatrzymuje pętlę monitorowania nicków
func (nc *NickCatcher) Stop() {
	nc.mutex.Lock()
	defer nc.mutex.Unlock()

	if !nc.isRunning {
		return
	}

	close(nc.stopChan)
	nc.isRunning = false
	util.Info("Nick catcher system stopped")
}

// monitorNicks to główna pętla monitorowania nicków
func (nc *NickCatcher) monitorNicks() {
	ticker := time.NewTicker(1 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			nc.checkNicks()
		case <-nc.stopChan:
			return
		}
	}
}

// checkNicks sprawdza dostępność nicków i próbuje je przejąć
func (nc *NickCatcher) checkNicks() {
	// Pobierz wszystkie boty
	bots := nc.botManager.GetBots()
	if len(bots) == 0 {
		util.Debug("No bots available for nick checking")
		return
	}

	// Wybierz kolejny bot z rotacji
	nc.mutex.Lock()
	botIndex := nc.botIndex
	nc.botIndex = (nc.botIndex + 1) % len(bots)
	nc.mutex.Unlock()

	bot := bots[botIndex]
	if !bot.IsConnected() {
		util.Debug("Bot %s is not connected, skipping ISON check", bot.GetCurrentNick())
		return
	}

	// Przygotuj listę nicków do sprawdzenia
	nicksToCheck := nc.prepareNicksToCheck()
	if len(nicksToCheck) == 0 {
		util.Debug("No nicks to check")
		return
	}

	// Wyślij zapytanie ISON
	util.Debug("Sending ISON request for %d nicks using bot %s", len(nicksToCheck), bot.GetCurrentNick())
	response, err := bot.RequestISON(nicksToCheck)
	if err != nil {
		util.Warning("Failed to get ISON response: %v", err)
		return
	}

	// Przetwórz odpowiedź
	nc.handleISONResponse(response, nicksToCheck, bot)
}

// prepareNicksToCheck przygotowuje listę nicków do sprawdzenia
func (nc *NickCatcher) prepareNicksToCheck() []string {
	nc.mutex.RLock()
	defer nc.mutex.RUnlock()

	// Połącz nicki priorytetowe i jednoliterowe
	allNicks := append([]string{}, nc.priorityNicks...)
	allNicks = append(allNicks, nc.singleLetterNicks...)

	return allNicks
}

// handleISONResponse przetwarza odpowiedź ISON i przydziela dostępne nicki
func (nc *NickCatcher) handleISONResponse(response, checkedNicks []string, requestBot types.Bot) {
	// Usuń przedawnione blokady
	nc.cleanupExpiredUnavailableNicks()

	// Logowanie informacji o zapytaniu
	util.Debug("Processing ISON response from bot %s for %d checked nicks", requestBot.GetCurrentNick(), len(checkedNicks))

	// Utwórz mapę nicków online (tych, które są w odpowiedzi ISON)
	onlineNicks := make(map[string]bool)
	for _, nick := range response {
		onlineNicks[strings.ToLower(nick)] = true
	}

	// Znajdź dostępne nicki (te, których nie ma w odpowiedzi ISON)
	var availablePriorityNicks []string
	var availableSingleLetterNicks []string

	nc.mutex.RLock()
	// Sprawdź nicki priorytetowe
	for _, nick := range nc.priorityNicks {
		lowerNick := strings.ToLower(nick)
		if !onlineNicks[lowerNick] && !nc.isNickTempUnavailable(lowerNick) {
			availablePriorityNicks = append(availablePriorityNicks, nick)
		}
	}

	// Sprawdź nicki jednoliterowe
	for _, nick := range nc.singleLetterNicks {
		lowerNick := strings.ToLower(nick)
		if !onlineNicks[lowerNick] && !nc.isNickTempUnavailable(lowerNick) {
			availableSingleLetterNicks = append(availableSingleLetterNicks, nick)
		}
	}
	nc.mutex.RUnlock()

	// Pobierz wszystkie boty
	bots := nc.botManager.GetBots()
	connectedBots := make([]types.Bot, 0)
	for _, bot := range bots {
		if bot.IsConnected() {
			connectedBots = append(connectedBots, bot)
		}
	}

	// Sprawdzamy, czy bot, który wykonał zapytanie, jest nadal połączony
	requestBotStillConnected := false
	for _, bot := range connectedBots {
		if bot == requestBot {
			requestBotStillConnected = true
			break
		}
	}

	if !requestBotStillConnected {
		util.Warning("Bot %s that requested ISON is no longer connected", requestBot.GetCurrentNick())
	}

	// Zlicz boty z wartościowymi nickami
	valuableNicksCount := 0
	priorityNicksCount := 0
	singleLetterNicksCount := 0
	for _, bot := range connectedBots {
		currentNick := bot.GetCurrentNick()
		if nc.isPriorityNick(currentNick) {
			priorityNicksCount++
			valuableNicksCount++
		} else if nc.isSingleLetterNick(currentNick) {
			singleLetterNicksCount++
			valuableNicksCount++
		}
	}

	// Logowanie informacji o znalezionych dostępnych nickach i aktualnym stanie
	util.Debug("Found %d available priority nicks and %d available single-letter nicks. Current state: %d bots with valuable nicks (%d priority, %d single-letter)",
		len(availablePriorityNicks), len(availableSingleLetterNicks), valuableNicksCount, priorityNicksCount, singleLetterNicksCount)

	// Przydziel nicki priorytetowe
	nc.assignNicks(availablePriorityNicks, connectedBots, true)

	// Przydziel nicki jednoliterowe
	nc.assignNicks(availableSingleLetterNicks, connectedBots, false)
}

// isSingleLetterNick sprawdza, czy nick jest jednoliterowy (a-z)
func (nc *NickCatcher) isSingleLetterNick(nick string) bool {
	return len(nick) == 1 && nick >= "a" && nick <= "z"
}

// isValuableNick sprawdza, czy nick jest wartościowy (priorytetowy lub jednoliterowy)
func (nc *NickCatcher) isValuableNick(nick string) bool {
	return nc.isPriorityNick(nick) || nc.isSingleLetterNick(nick)
}

// assignNicks przydziela dostępne nicki do botów
func (nc *NickCatcher) assignNicks(availableNicks []string, bots []types.Bot, isPriority bool) {
	if len(availableNicks) == 0 || len(bots) == 0 {
		return
	}

	nickType := "priority"
	if !isPriority {
		nickType = "single-letter"
	}

	util.Debug("Assigning %d available %s nicks to %d bots", len(availableNicks), nickType, len(bots))

	// Dla każdego dostępnego nicka
	for _, nick := range availableNicks {
		// Znajdź bota, który może przejąć ten nick
		assigned := false
		for _, bot := range bots {
			// Sprawdź, czy bot jest połączony
			if !bot.IsConnected() {
				continue
			}

			// Sprawdź, czy bot już ma wartościowy nick (priorytetowy lub jednoliterowy)
			currentNick := bot.GetCurrentNick()
			if nc.isValuableNick(currentNick) {
				util.Debug("Bot %s already has valuable nick %s, skipping", bot.GetCurrentNick(), currentNick)
				continue // Bot już ma wartościowy nick, nie zmieniaj
			}

			// Sprawdź, czy serwer obsługuje ten nick (dla jednoliterowych)
			if !isPriority {
				serverName := bot.GetServerName()
				if nc.isNickBlacklistedForServer(nick, serverName) {
					util.Debug("Nick %s is blacklisted for server %s, skipping", nick, serverName)
					continue
				}
			}

			// Próbuj zmienić nick
			util.Info("Bot %s attempting to change nick to %s", bot.GetCurrentNick(), nick)
			bot.AttemptNickChange(nick)

			// Oznacz nick jako tymczasowo niedostępny, aby uniknąć równoczesnych prób
			nc.markNickAsTemporarilyUnavailable(nick, bot.GetServerName())

			assigned = true
			break
		}

		if !assigned {
			util.Debug("Could not assign nick %s to any bot", nick)
		}
	}
}

// cleanupExpiredUnavailableNicks usuwa przedawnione blokady nicków
func (nc *NickCatcher) cleanupExpiredUnavailableNicks() {
	nc.mutex.Lock()
	defer nc.mutex.Unlock()

	now := time.Now()
	for nick, info := range nc.tempUnavailableNicks {
		if now.Sub(info.Timestamp) > nc.unavailableTimeout {
			util.Debug("Nick %s is no longer temporarily unavailable", nick)
			delete(nc.tempUnavailableNicks, nick)
		}
	}
}

// isNickTempUnavailable sprawdza, czy nick jest tymczasowo niedostępny
func (nc *NickCatcher) isNickTempUnavailable(nick string) bool {
	// Używamy RLock, ponieważ tylko odczytujemy dane
	lowerNick := strings.ToLower(nick)
	_, exists := nc.tempUnavailableNicks[lowerNick]
	return exists
}

// markNickAsTemporarilyUnavailable oznacza nick jako tymczasowo niedostępny
func (nc *NickCatcher) markNickAsTemporarilyUnavailable(nick, server string) {
	nc.mutex.Lock()
	defer nc.mutex.Unlock()

	lowerNick := strings.ToLower(nick)
	nc.tempUnavailableNicks[lowerNick] = TempUnavailableNick{
		Nick:      nick,
		Timestamp: time.Now(),
		Server:    server,
	}
	util.Debug("Nick %s marked as temporarily unavailable on server %s", nick, server)
}

// isPriorityNick sprawdza, czy nick jest na liście priorytetowych
func (nc *NickCatcher) isPriorityNick(nick string) bool {
	lowerNick := strings.ToLower(nick)
	for _, priorityNick := range nc.priorityNicks {
		if strings.ToLower(priorityNick) == lowerNick {
			return true
		}
	}
	return false
}

// IsPriorityNick sprawdza, czy nick jest na liście priorytetowych (metoda eksportowana)
func (nc *NickCatcher) IsPriorityNick(nick string) bool {
	nc.mutex.RLock()
	defer nc.mutex.RUnlock()
	return nc.isPriorityNick(nick)
}

// isNickBlacklistedForServer sprawdza, czy nick jest na czarnej liście dla danego serwera
func (nc *NickCatcher) isNickBlacklistedForServer(nick, server string) bool {
	lowerNick := strings.ToLower(nick)
	lowerServer := strings.ToLower(server)

	// Sprawdź, czy serwer jest na liście
	blacklistedNicks, exists := nc.serverBlacklist[lowerServer]
	if !exists {
		return false
	}

	// Sprawdź, czy nick jest na czarnej liście dla tego serwera
	for _, blacklistedNick := range blacklistedNicks {
		if strings.ToLower(blacklistedNick) == lowerNick {
			return true
		}
	}

	return false
}

// AddPriorityNick dodaje nick do listy priorytetowych
func (nc *NickCatcher) AddPriorityNick(nick string) error {
	nc.mutex.Lock()
	defer nc.mutex.Unlock()

	// Sprawdź, czy nick już istnieje
	for _, existingNick := range nc.priorityNicks {
		if strings.EqualFold(existingNick, nick) {
			return fmt.Errorf("nick %s already exists in priority list", nick)
		}
	}

	// Dodaj nick do listy
	nc.priorityNicks = append(nc.priorityNicks, nick)

	// Zapisz zaktualizowaną listę do pliku
	return nc.saveNicksToFile()
}

// RemovePriorityNick usuwa nick z listy priorytetowych
func (nc *NickCatcher) RemovePriorityNick(nick string) error {
	nc.mutex.Lock()
	defer nc.mutex.Unlock()

	lowerNick := strings.ToLower(nick)
	index := -1
	for i, existingNick := range nc.priorityNicks {
		if strings.ToLower(existingNick) == lowerNick {
			index = i
			break
		}
	}

	if index == -1 {
		return fmt.Errorf("nick %s not found in priority list", nick)
	}

	// Usuń nick z listy
	nc.priorityNicks = append(nc.priorityNicks[:index], nc.priorityNicks[index+1:]...)

	// Zapisz zaktualizowaną listę do pliku
	return nc.saveNicksToFile()
}

// GetPriorityNicks zwraca kopię listy priorytetowych nicków
func (nc *NickCatcher) GetPriorityNicks() []string {
	nc.mutex.RLock()
	defer nc.mutex.RUnlock()

	result := make([]string, len(nc.priorityNicks))
	copy(result, nc.priorityNicks)
	return result
}

// saveNicksToFile zapisuje listę priorytetowych nicków do pliku
func (nc *NickCatcher) saveNicksToFile() error {
	config := NickConfig{
		Nicks: nc.priorityNicks,
	}

	data, err := json.MarshalIndent(config, "", "  ")
	if err != nil {
		return err
	}

	return os.WriteFile("data/nicks.json", data, 0644)
}

// SetUnavailableTimeout ustawia czas, po którym nick przestaje być tymczasowo niedostępny
func (nc *NickCatcher) SetUnavailableTimeout(timeout time.Duration) {
	nc.mutex.Lock()
	defer nc.mutex.Unlock()
	nc.unavailableTimeout = timeout
}

// GetUnavailableTimeout zwraca czas, po którym nick przestaje być tymczasowo niedostępny
func (nc *NickCatcher) GetUnavailableTimeout() time.Duration {
	nc.mutex.RLock()
	defer nc.mutex.RUnlock()
	return nc.unavailableTimeout
}

// AddServerBlacklist dodaje serwer i nicki do czarnej listy
func (nc *NickCatcher) AddServerBlacklist(server string, nicks []string) {
	nc.mutex.Lock()
	defer nc.mutex.Unlock()

	lowerServer := strings.ToLower(server)
	nc.serverBlacklist[lowerServer] = nicks
}

// RemoveServerBlacklist usuwa serwer z czarnej listy
func (nc *NickCatcher) RemoveServerBlacklist(server string) {
	nc.mutex.Lock()
	defer nc.mutex.Unlock()

	lowerServer := strings.ToLower(server)
	delete(nc.serverBlacklist, lowerServer)
}

// GetServerBlacklist zwraca kopię czarnej listy serwerów
func (nc *NickCatcher) GetServerBlacklist() map[string][]string {
	nc.mutex.RLock()
	defer nc.mutex.RUnlock()

	result := make(map[string][]string)
	for server, nicks := range nc.serverBlacklist {
		nicksCopy := make([]string, len(nicks))
		copy(nicksCopy, nicks)
		result[server] = nicksCopy
	}
	return result
}
