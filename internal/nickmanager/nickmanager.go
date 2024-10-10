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
	nicksToCatch       []string
	priorityNicks      []string
	secondaryNicks     []string
	availableBots      []types.Bot
	bots               []types.Bot
	mutex              sync.Mutex
	botManager         types.BotManager
	isonResponses      chan []string
	priorityNickQueue  []string
	secondaryNickQueue []string
}

type NicksData struct {
	Nicks []string `json:"nicks"`
}

func NewNickManager() *NickManager {
	return &NickManager{
		isonResponses: make(chan []string, 10), // Buffer for ISON responses
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
	go nm.processISONResponses()
	go nm.distributeNicksLoop()
}

func (nm *NickManager) processISONResponses() {
	for onlineNicks := range nm.isonResponses {
		nm.handleISONResponse(onlineNicks)
	}
}

func (nm *NickManager) ReceiveISONResponse(onlineNicks []string) {
	select {
	case nm.isonResponses <- onlineNicks:
	default:
		util.Debug("ISON response channel full; dropping response")
	}
}

func (nm *NickManager) handleISONResponse(onlineNicks []string) {
	nm.mutex.Lock()
	defer nm.mutex.Unlock()

	util.Debug("NickManager received ISON response: %v", onlineNicks)

	// Update the nick queues based on availability
	for _, nick := range nm.priorityNicks {
		if !util.Contains(onlineNicks, nick) && !util.Contains(nm.priorityNickQueue, nick) {
			nm.priorityNickQueue = append(nm.priorityNickQueue, nick)
		}
	}
	for _, nick := range nm.secondaryNicks {
		if !util.Contains(onlineNicks, nick) && !util.Contains(nm.secondaryNickQueue, nick) {
			nm.secondaryNickQueue = append(nm.secondaryNickQueue, nick)
		}
	}
}

func (nm *NickManager) distributeNicksLoop() {
	for {
		nm.mutex.Lock()
		availableBots := nm.getAvailableBots()
		nm.mutex.Unlock()

		if len(availableBots) == 0 {
			time.Sleep(1 * time.Second)
			continue
		}

		nm.mutex.Lock()
		assignedBots := 0

		// Assign priority nicks first
		for assignedBots < len(availableBots) && len(nm.priorityNickQueue) > 0 {
			nick := nm.priorityNickQueue[0]
			nm.priorityNickQueue = nm.priorityNickQueue[1:]
			bot := availableBots[assignedBots]
			assignedBots++
			util.Debug("Assigning priority nick %s to bot %s", nick, bot.GetCurrentNick())
			go bot.AttemptNickChange(nick)
		}

		// Then assign secondary nicks
		for assignedBots < len(availableBots) && len(nm.secondaryNickQueue) > 0 {
			nick := nm.secondaryNickQueue[0]
			nm.secondaryNickQueue = nm.secondaryNickQueue[1:]
			bot := availableBots[assignedBots]
			assignedBots++
			util.Debug("Assigning secondary nick %s to bot %s", nick, bot.GetCurrentNick())
			go bot.AttemptNickChange(nick)
		}

		nm.mutex.Unlock()

		time.Sleep(1 * time.Second)
	}
}

func (nm *NickManager) RegisterBot(bot types.Bot) {
	nm.mutex.Lock()
	defer nm.mutex.Unlock()
	nm.bots = append(nm.bots, bot)
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

	if util.Contains(nm.priorityNicks, nick) && !util.Contains(nm.priorityNickQueue, nick) {
		nm.priorityNickQueue = append(nm.priorityNickQueue, nick)
	} else if util.Contains(nm.secondaryNicks, nick) && !util.Contains(nm.secondaryNickQueue, nick) {
		nm.secondaryNickQueue = append(nm.secondaryNickQueue, nick)
	}
}

func (nm *NickManager) GetNicksToCatch() []string {
	nm.mutex.Lock()
	defer nm.mutex.Unlock()
	nicksCopy := make([]string, len(nm.nicksToCatch))
	copy(nicksCopy, nm.nicksToCatch)
	return nicksCopy
}
