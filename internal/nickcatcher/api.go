package nickcatcher

import (
	"fmt"
	"strings"
	"time"
)

// NickStatusInfo zawiera informacje o statusie systemu przechwytywania nicków
type NickStatusInfo struct {
	ConnectedBots          int
	PriorityNicksCount     int
	SingleLetterNicksCount int
	TempUnavailableCount   int
	UnavailableTimeout     time.Duration
	BotNicks               []string
	TempUnavailableNicks   []TempUnavailableNickInfo
}

// TempUnavailableNickInfo zawiera informacje o tymczasowo niedostępnym nicku
type TempUnavailableNickInfo struct {
	Nick      string
	Server    string
	ExpiresIn time.Duration
}

// GetPriorityNicksList zwraca listę priorytetowych nicków
func (nc *NickCatcher) GetPriorityNicksList() []string {
	return nc.GetPriorityNicks()
}

// AddPriorityNickToList dodaje nick do listy priorytetowych
func (nc *NickCatcher) AddPriorityNickToList(nick string) error {
	if nick == "" {
		return fmt.Errorf("nick cannot be empty")
	}
	return nc.AddPriorityNick(nick)
}

// RemovePriorityNickFromList usuwa nick z listy priorytetowych
func (nc *NickCatcher) RemovePriorityNickFromList(nick string) error {
	if nick == "" {
		return fmt.Errorf("nick cannot be empty")
	}
	return nc.RemovePriorityNick(nick)
}

// GetNickCatcherStatus zwraca status systemu przechwytywania nicków
func (nc *NickCatcher) GetNickCatcherStatus(detailed bool) NickStatusInfo {
	// Pobierz wszystkie boty
	bots := nc.botManager.GetBots()

	// Zlicz boty z priorytetowymi nickami
	priorityNicksCount := 0
	singleLetterNicksCount := 0
	botNicks := make([]string, 0)

	for _, bot := range bots {
		if !bot.IsConnected() {
			continue
		}

		currentNick := bot.GetCurrentNick()
		botNicks = append(botNicks, currentNick)

		if nc.IsPriorityNick(currentNick) {
			priorityNicksCount++
		} else if len(currentNick) == 1 && currentNick >= "a" && currentNick <= "z" {
			singleLetterNicksCount++
		}
	}

	// Pobierz tymczasowo niedostępne nicki
	nc.mutex.RLock()
	tempUnavailableCount := len(nc.tempUnavailableNicks)

	// Przygotuj informacje o statusie
	status := NickStatusInfo{
		ConnectedBots:          len(botNicks),
		PriorityNicksCount:     priorityNicksCount,
		SingleLetterNicksCount: singleLetterNicksCount,
		TempUnavailableCount:   tempUnavailableCount,
		UnavailableTimeout:     nc.unavailableTimeout,
		BotNicks:               botNicks,
	}

	// Dodaj szczegóły tymczasowo niedostępnych nicków, jeśli potrzeba
	if detailed && tempUnavailableCount > 0 {
		tempUnavailableNicks := make([]TempUnavailableNickInfo, 0, tempUnavailableCount)
		now := time.Now()

		for nick, info := range nc.tempUnavailableNicks {
			timeLeft := nc.unavailableTimeout - now.Sub(info.Timestamp)
			if timeLeft > 0 {
				tempUnavailableNicks = append(tempUnavailableNicks, TempUnavailableNickInfo{
					Nick:      nick,
					Server:    info.Server,
					ExpiresIn: timeLeft.Round(time.Second),
				})
			}
		}

		status.TempUnavailableNicks = tempUnavailableNicks
	}

	nc.mutex.RUnlock()

	return status
}

// FormatNickList formatuje listę priorytetowych nicków jako string
func FormatNickList(nicks []string) string {
	if len(nicks) == 0 {
		return "No priority nicks defined"
	}
	return fmt.Sprintf("Priority nicks: %s", strings.Join(nicks, ", "))
}

// FormatNickAdded formatuje komunikat o dodaniu nicka
func FormatNickAdded(nick string) string {
	return fmt.Sprintf("Nick %s added to priority list", nick)
}

// FormatNickRemoved formatuje komunikat o usunięciu nicka
func FormatNickRemoved(nick string) string {
	return fmt.Sprintf("Nick %s removed from priority list", nick)
}

// FormatNickStatus formatuje status systemu przechwytywania nicków
func FormatNickStatus(status NickStatusInfo, detailed bool, compact bool) string {
	var response string

	if compact {
		response = fmt.Sprintf(
			"Nick Catcher Status: Connected bots: %d, Priority nicks: %d, Single-letter nicks: %d, Timeout: %s",
			status.ConnectedBots,
			status.PriorityNicksCount,
			status.SingleLetterNicksCount,
			status.UnavailableTimeout.String(),
		)
	} else {
		response = fmt.Sprintf(
			"Nick Catcher Status:\n"+
				"- Connected bots: %d\n"+
				"- Bots with priority nicks: %d\n"+
				"- Bots with single-letter nicks: %d\n"+
				"- Temporarily unavailable nicks: %d\n"+
				"- Unavailable timeout: %s\n",
			status.ConnectedBots,
			status.PriorityNicksCount,
			status.SingleLetterNicksCount,
			status.TempUnavailableCount,
			status.UnavailableTimeout.String(),
		)
	}

	if detailed {
		response += "\nBot nicks: " + strings.Join(status.BotNicks, ", ")

		if len(status.TempUnavailableNicks) > 0 {
			response += "\n\nTemporarily unavailable nicks:"
			for _, info := range status.TempUnavailableNicks {
				response += fmt.Sprintf("\n- %s (server: %s, expires in: %s)",
					info.Nick, info.Server, info.ExpiresIn.String())
			}
		}
	}

	return response
}

// GetNickCatcherInstance zwraca globalną instancję NickCatcher
// Funkcja pomocnicza dla adapterów
func GetNickCatcherInstance() *NickCatcher {
	return GetGlobalInstance()
}

// ValidateNickCatcherCommand sprawdza, czy komenda jest obsługiwana przez NickCatcher
func ValidateNickCatcherCommand(command string) bool {
	command = strings.ToLower(command)
	switch command {
	case "nicklist", "addnick", "delnick", "nickstatus":
		return true
	default:
		return false
	}
}

// ExecuteNickCatcherCommand wykonuje komendę NickCatcher i zwraca wynik
func (nc *NickCatcher) ExecuteNickCatcherCommand(command string, args []string) (string, error) {
	command = strings.ToLower(command)

	switch command {
	case "nicklist":
		nicks := nc.GetPriorityNicksList()
		return FormatNickList(nicks), nil

	case "addnick":
		if len(args) < 1 {
			return "", fmt.Errorf("usage: addnick <nick>")
		}

		nick := args[0]
		err := nc.AddPriorityNickToList(nick)
		if err != nil {
			return "", err
		}

		return FormatNickAdded(nick), nil

	case "delnick":
		if len(args) < 1 {
			return "", fmt.Errorf("usage: delnick <nick>")
		}

		nick := args[0]
		err := nc.RemovePriorityNickFromList(nick)
		if err != nil {
			return "", err
		}

		return FormatNickRemoved(nick), nil

	case "nickstatus":
		detailed := len(args) > 0 && args[0] == "detail"
		compact := false

		status := nc.GetNickCatcherStatus(detailed)
		return FormatNickStatus(status, detailed, compact), nil

	default:
		return "", fmt.Errorf("unknown command: %s", command)
	}
}

// ExecuteNickCatcherCommandForIRC wykonuje komendę NickCatcher dla IRC
func (nc *NickCatcher) ExecuteNickCatcherCommandForIRC(command string, args []string) (string, error) {
	command = strings.ToLower(command)

	if command == "nickstatus" {
		detailed := len(args) > 0 && args[0] == "detail"
		compact := true

		status := nc.GetNickCatcherStatus(detailed)
		return FormatNickStatus(status, detailed, compact), nil
	}

	return nc.ExecuteNickCatcherCommand(command, args)
}
