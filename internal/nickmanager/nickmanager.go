package nickmanager

import (
	"encoding/json"
	"os"
	"sync"
	"time"

	"github.com/kofany/gNb/internal/types"
	"github.com/kofany/gNb/internal/util"
)

type NickManager struct {
	nicksToCatch   []string
	priorityNicks  []string
	secondaryNicks []string
	bots           []types.Bot
	botIndex       int
	mutex          sync.Mutex
	isonInterval   time.Duration
}

type NicksData struct {
	Nicks []string `json:"nicks"`
}

func NewNickManager() *NickManager {
	return &NickManager{}
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

	// Determine available nicks
	var availablePriorityNicks []string
	var availableSecondaryNicks []string

	for _, nick := range nm.priorityNicks {
		if !util.Contains(onlineNicks, nick) {
			availablePriorityNicks = append(availablePriorityNicks, nick)
		}
	}

	for _, nick := range nm.secondaryNicks {
		if !util.Contains(onlineNicks, nick) {
			availableSecondaryNicks = append(availableSecondaryNicks, nick)
		}
	}

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
		assignedBots++
		util.Debug("Assigning priority nick %s to bot %s", nick, bot.GetCurrentNick())
		go bot.AttemptNickChange(nick)
	}

	// Then assign secondary nicks
	for assignedBots < len(availableBots) && len(availableSecondaryNicks) > 0 {
		nick := availableSecondaryNicks[0]
		availableSecondaryNicks = availableSecondaryNicks[1:]
		bot := availableBots[assignedBots]
		assignedBots++
		util.Debug("Assigning secondary nick %s to bot %s", nick, bot.GetCurrentNick())
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
	// No action needed since we check availability each time
}

func (nm *NickManager) GetNicksToCatch() []string {
	nm.mutex.Lock()
	defer nm.mutex.Unlock()
	nicksCopy := make([]string, len(nm.nicksToCatch))
	copy(nicksCopy, nm.nicksToCatch)
	return nicksCopy
}
