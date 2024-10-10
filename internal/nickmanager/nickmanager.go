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
	availableNicks chan string
	bots           []types.Bot
	mutex          sync.Mutex
	botManager     types.BotManager
	isonResponses  chan []string
}

type NicksData struct {
	Nicks []string `json:"nicks"`
}

func NewNickManager() *NickManager {
	return &NickManager{
		availableNicks: make(chan string, 100),  // Buffer for 100 nicks
		isonResponses:  make(chan []string, 10), // Buffer for ISON responses
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

	nm.nicksToCatch = nicksData.Nicks

	// Add single-letter nicks
	letters := "abcdefghijklmnopqrstuvwxyz"
	for _, c := range letters {
		nick := string(c)
		if !util.Contains(nm.nicksToCatch, nick) {
			nm.nicksToCatch = append(nm.nicksToCatch, nick)
		}
	}

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

	var availableNicks []string
	for _, nick := range nm.nicksToCatch {
		if !util.Contains(onlineNicks, nick) {
			availableNicks = append(availableNicks, nick)
		}
	}

	if len(availableNicks) > 0 {
		util.Debug("NickManager processing available nicks: %v", availableNicks)
		for _, nick := range availableNicks {
			select {
			case nm.availableNicks <- nick:
				util.Debug("Nick %s added to available nicks pool", nick)
			default:
				util.Debug("Channel full, skipping nick %s", nick)
			}
		}
	} else {
		util.Debug("NickManager: No available nicks to process")
	}
}

func (nm *NickManager) distributeNicksLoop() {
	for {
		select {
		case nick := <-nm.availableNicks:
			nm.distributeNick(nick)
		}
	}
}

func (nm *NickManager) distributeNick(nick string) {
	nm.mutex.Lock()
	bot := nm.getAvailableBot()
	nm.mutex.Unlock()

	if bot == nil {
		util.Debug("No available bots to assign nick %s", nick)
		// Requeue the nick after some time
		go func() {
			time.Sleep(5 * time.Second)
			nm.ReturnNickToPool(nick)
		}()
		return
	}
	util.Debug("Assigning nick %s to bot %s", nick, bot.GetCurrentNick())
	go bot.AttemptNickChange(nick)
}

func (nm *NickManager) RegisterBot(bot types.Bot) {
	nm.mutex.Lock()
	defer nm.mutex.Unlock()
	nm.bots = append(nm.bots, bot)
}

func (nm *NickManager) getAvailableBot() types.Bot {
	for _, bot := range nm.bots {
		if bot.IsConnected() && !util.IsTargetNick(bot.GetCurrentNick(), nm.nicksToCatch) {
			return bot
		}
	}
	return nil
}

func (nm *NickManager) ReturnNickToPool(nick string) {
	select {
	case nm.availableNicks <- nick:
	default:
		// Channel full, skipping nick
	}
}

func (nm *NickManager) GetNicksToCatch() []string {
	nm.mutex.Lock()
	defer nm.mutex.Unlock()
	nicksCopy := make([]string, len(nm.nicksToCatch))
	copy(nicksCopy, nm.nicksToCatch)
	return nicksCopy
}
