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
	botIndex             int
	isonInterval         time.Duration
	tempUnavailableNicks map[string]time.Time // Mapa do blokowania nicków
	NoLettersServers     map[string]bool      // Mapa serwerów, które nie akceptują jednoliterowych nicków
	mutex                sync.Mutex
}

type NicksData struct {
	Nicks []string `json:"nicks"`
}

func NewNickManager() *NickManager {
	return &NickManager{
		tempUnavailableNicks: make(map[string]time.Time),
		NoLettersServers:     make(map[string]bool),
	}
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
	for {
		nm.mutex.Lock()
		if len(nm.bots) == 0 {
			nm.mutex.Unlock()
			time.Sleep(1 * time.Second)
			continue
		}

		// Get the next bot in the queue to send ISON
		bot := nm.bots[nm.botIndex]
		nm.botIndex = (nm.botIndex + 1) % len(nm.bots)
		nm.mutex.Unlock()

		if bot.IsConnected() {
			// Request ISON and wait for response
			onlineNicks, err := bot.RequestISON(nm.nicksToCatch)
			if err != nil {
				util.Error("Error requesting ISON from bot %s: %v", bot.GetCurrentNick(), err)
				time.Sleep(1 * time.Second)
				continue
			}
			nm.handleISONResponse(onlineNicks)
		} else {
			util.Debug("Bot %s is not connected; skipping", bot.GetCurrentNick())
		}

		// Wait before sending the next ISON
		time.Sleep(time.Duration(nm.isonInterval) * time.Second)
	}
}

func (nm *NickManager) handleISONResponse(onlineNicks []string) {
	nm.mutex.Lock()
	defer nm.mutex.Unlock()

	util.Debug("NickManager received ISON response: %v", onlineNicks)

	currentTime := time.Now()
	nm.cleanupTempUnavailableNicks(currentTime)

	availablePriorityNicks := nm.filterAvailableNicks(nm.priorityNicks, onlineNicks)
	availableSecondaryNicks := nm.filterAvailableNicks(nm.secondaryNicks, onlineNicks)

	// Get list of available bots
	availableBots := nm.getAvailableBots()
	if len(availableBots) == 0 {
		util.Debug("No available bots to assign nicks")
		return
	}

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
}

func (nm *NickManager) RegisterBot(bot types.Bot) {
	nm.mutex.Lock()
	defer nm.mutex.Unlock()
	nm.bots = append(nm.bots, bot)
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

func (nm *NickManager) filterAvailableNicks(nicks []string, onlineNicks []string) []string {
	var available []string
	for _, nick := range nicks {
		lowerNick := strings.ToLower(nick)
		if !util.ContainsIgnoreCase(onlineNicks, nick) {
			// Sprawdź, czy nick nie jest tymczasowo niedostępny
			if _, exists := nm.tempUnavailableNicks[lowerNick]; !exists {
				available = append(available, nick)
			}
		}
	}
	return available
}

func (nm *NickManager) MarkNickAsTemporarilyUnavailable(nick string) {
	nm.mutex.Lock()
	defer nm.mutex.Unlock()

	nm.tempUnavailableNicks[strings.ToLower(nick)] = time.Now().Add(1 * time.Minute)
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
