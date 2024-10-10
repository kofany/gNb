// File: internal/nickmanager/nickmanager.go

package nickmanager

import (
	"encoding/json"
	"os"
	"sync"

	"github.com/kofany/gNb/internal/types"
	"github.com/kofany/gNb/internal/util"
)

type NickManager struct {
	nicksToCatch   []string
	availableNicks chan string
	bots           []types.Bot
	mutex          sync.Mutex
}

type NicksData struct {
	Nicks []string `json:"nicks"`
}

func NewNickManager() *NickManager {
	return &NickManager{
		availableNicks: make(chan string, 100), // Buffer for 100 nicks
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

func (nm *NickManager) RegisterBot(bot types.Bot) {
	nm.mutex.Lock()
	defer nm.mutex.Unlock()
	nm.bots = append(nm.bots, bot)
}

func (nm *NickManager) HandleISONResponse(onlineNicks []string) {
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
		nm.distributeNicks()
	} else {
		util.Debug("NickManager: No available nicks to process")
	}
}

func (nm *NickManager) distributeNicks() {
	nm.mutex.Lock()
	defer nm.mutex.Unlock()

	botIndex := 0
	botsCount := len(nm.bots)

	for {
		select {
		case nick := <-nm.availableNicks:
			if botsCount == 0 {
				util.Debug("No bots available to distribute nick %s", nick)
				return
			}

			// Find a bot that is connected and can change nick
			for i := 0; i < botsCount; i++ {
				bot := nm.bots[botIndex]
				botIndex = (botIndex + 1) % botsCount

				if bot.IsConnected() {
					util.Debug("Assigning nick %s to bot %s", nick, bot.GetCurrentNick())
					go bot.AttemptNickChange(nick)
					break
				}
			}
		default:
			return
		}
	}
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
	return nm.nicksToCatch
}
