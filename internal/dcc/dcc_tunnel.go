package dcc

import (
	"bufio"
	"fmt"
	"io"
	"net"
	"strings"
	"sync"

	"github.com/kofany/gNb/internal/irc"
	"github.com/kofany/gNb/internal/types"
	"github.com/kofany/gNb/internal/util"
)

// DCCTunnel reprezentuje tunel DCC do komunikacji z botem
type DCCTunnel struct {
	conn          net.Conn
	bot           types.Bot
	active        bool
	mu            sync.Mutex
	ignoredEvents map[string]bool
	onStop        func()
	formatter     *MessageFormatter
	botManager    types.BotManager
}

// NewDCCTunnel tworzy nową instancję tunelu DCC
func NewDCCTunnel(bot types.Bot, onStop func()) *DCCTunnel {
	dt := &DCCTunnel{
		bot:           bot,
		active:        false,
		ignoredEvents: map[string]bool{"303": true}, // Ignore ISON responses
		onStop:        onStop,
		formatter:     NewMessageFormatter(bot.GetCurrentNick()),
		botManager:    bot.GetBotManager(),
	}

	return dt
}

// Start inicjuje tunel DCC
func (dt *DCCTunnel) Start(conn net.Conn) {
	dt.mu.Lock()
	defer dt.mu.Unlock()

	if dt.active {
		util.Warning("DCC: DCC tunnel already active for bot %s", dt.bot.GetCurrentNick())
		return
	}

	dt.conn = conn
	dt.active = true

	util.Debug("DCC: DCC tunnel started for bot %s", dt.bot.GetCurrentNick())

	welcomeMessage := dt.getWelcomeMessage()
	dt.conn.Write([]byte(welcomeMessage + "\r\n"))

	go dt.readFromConn()
}

// Stop zatrzymuje tunel DCC
func (dt *DCCTunnel) Stop() {
	dt.mu.Lock()
	defer dt.mu.Unlock()

	if !dt.active {
		return
	}

	dt.active = false
	if dt.conn != nil {
		dt.conn.Close()
	}

	if dt.onStop != nil {
		dt.onStop()
	}
}

// readFromConn obsługuje odczyt danych z połączenia
func (dt *DCCTunnel) readFromConn() {
	defer dt.Stop()

	scanner := bufio.NewScanner(dt.conn)
	for scanner.Scan() {
		line := scanner.Text()
		util.Debug("DCC: Received from DCC connection: %s", line)
		dt.handleUserInput(line)
	}

	if err := scanner.Err(); err != nil {
		util.Error("DCC: Error reading from DCC Chat connection: %v", err)
	}
}

// handleUserInput przetwarza dane wejściowe od użytkownika
func (dt *DCCTunnel) handleUserInput(input string) {
	if strings.HasPrefix(input, ".") {
		dt.processCommand(input)
	} else {
		defaultTarget := dt.bot.GetCurrentNick()
		dt.bot.SendMessage(defaultTarget, input)
	}
}

func (dt *DCCTunnel) WriteToConn(data string) {
	dt.mu.Lock()
	defer dt.mu.Unlock()

	if !dt.active || dt.conn == nil {
		return
	}

	// Ignore certain events
	if strings.Contains(data, " 303 ") {
		return
	}

	// Parse the IRC message
	parsedMessage := dt.parseIRCMessage(data)
	if parsedMessage == "" {
		return
	}

	util.Debug("DCC: Sending to DCC connection: %s", parsedMessage)
	dt.conn.Write([]byte(parsedMessage + "\r\n"))
}

func (dt *DCCTunnel) parseIRCMessage(raw string) string {
	// Use your own irc package to parse the message
	event := irc.ParseIRCMessage(raw)
	if event == nil {
		util.Debug("DCC: Failed to parse IRC message")
		return ""
	}

	switch event.Command {
	case "PRIVMSG":
		return dt.formatter.formatPrivMsg(event)
	case "NOTICE":
		return dt.formatter.formatNotice(event)
	case "JOIN":
		return dt.formatter.formatJoin(event)
	case "PART":
		return dt.formatter.formatPart(event)
	case "QUIT":
		return dt.formatter.formatQuit(event)
	case "NICK":
		return dt.formatter.formatNick(event)
	case "MODE":
		return dt.formatter.formatMode(event)
	case "KICK":
		return dt.formatter.formatKick(event)
	default:
		return dt.formatter.formatOther(event)
	}
}

// shouldIgnoreEvent sprawdza czy dane zdarzenie powinno być ignorowane
func (dt *DCCTunnel) shouldIgnoreEvent(data string) bool {
	dt.mu.Lock()
	defer dt.mu.Unlock()

	// Ignoruj odpowiedzi ISON
	if strings.Contains(data, " 303 ") {
		return true
	}

	for event := range dt.ignoredEvents {
		if strings.Contains(data, " "+event+" ") {
			return true
		}
	}

	return false
}

// sendToClient wysyła wiadomość do klienta DCC
func (dt *DCCTunnel) sendToClient(message string) {
	if dt.conn != nil {
		dt.conn.Write([]byte(message + "\r\n"))
	}
}

// getWelcomeMessage zwraca wiadomość powitalną dla połączenia DCC
func (dt *DCCTunnel) getWelcomeMessage() string {
	return `
                 ___      __             __ <<<<<<[get Nick bot]
    ____  ____  [ m ]__  / /_  __  __   / /____  ____ _____ ___
   / __ \/ __ \  / / _ \/ __ \/ / / /  / __/ _ \/ __ ` + "`" + `/ __ ` + "`" + `__ \
  / /_/ / /_/ / / /  __/ /_/ / /_/ /  / /_/  __/ /_/ / / / / / /
 / .___/\____/_/ /\___/_.___/\__, /blo\__/\___/\__,_/_/ /_/ /_/
/_/  ruciu  /___/   dominik /____/                     kofany

Type your IRC commands here using '.' as the prefix.

Type .help to see available commands.
`
}

// SetIgnoredEvent dodaje lub usuwa zdarzenie z listy ignorowanych
func (dt *DCCTunnel) SetIgnoredEvent(event string, ignore bool) {
	dt.mu.Lock()
	defer dt.mu.Unlock()
	if ignore {
		dt.ignoredEvents[event] = true
	} else {
		delete(dt.ignoredEvents, event)
	}
}

// IsActive zwraca status aktywności tunelu
func (dt *DCCTunnel) IsActive() bool {
	dt.mu.Lock()
	defer dt.mu.Unlock()
	return dt.active
}

// GetBot zwraca referencję do bota
func (dt *DCCTunnel) GetBot() types.Bot {
	return dt.bot
}

// updateFormatter aktualizuje formatter wiadomości (np. po zmianie nicka)
func (dt *DCCTunnel) updateFormatter(newNick string) {
	dt.mu.Lock()
	defer dt.mu.Unlock()
	dt.formatter = NewMessageFormatter(newNick)
}

// Funkcje pomocnicze do debugowania i logowania

// logDebug loguje wiadomość debugowania
func (dt *DCCTunnel) logDebug(format string, args ...interface{}) {
	botNick := dt.bot.GetCurrentNick()
	message := fmt.Sprintf(format, args...)
	util.Debug("DCC[%s]: %s", botNick, message)
}

// logError loguje błąd
func (dt *DCCTunnel) logError(format string, args ...interface{}) {
	botNick := dt.bot.GetCurrentNick()
	message := fmt.Sprintf(format, args...)
	util.Error("DCC[%s]: %s", botNick, message)
}

// logWarning loguje ostrzeżenie
func (dt *DCCTunnel) logWarning(format string, args ...interface{}) {
	botNick := dt.bot.GetCurrentNick()
	message := fmt.Sprintf(format, args...)
	util.Warning("DCC[%s]: %s", botNick, message)
}

// handleConnectionError obsługuje błędy połączenia
func (dt *DCCTunnel) handleConnectionError(err error) {
	if err != nil && err != io.EOF {
		dt.logError("Connection error: %v", err)
	}
	dt.Stop()
}

// isValidCommand sprawdza czy komenda jest poprawna
func (dt *DCCTunnel) isValidCommand(command string) bool {
	if !strings.HasPrefix(command, ".") {
		return false
	}

	cmd := strings.Fields(command)
	if len(cmd) == 0 {
		return false
	}

	// Usuń prefiks "." i przekonwertuj na wielkie litery
	cmdName := strings.ToUpper(strings.TrimPrefix(cmd[0], "."))

	// Lista dozwolonych komend
	validCommands := map[string]bool{
		"MSG": true, "JOIN": true, "PART": true,
		"MODE": true, "KICK": true, "QUIT": true,
		"NICK": true, "RAW": true, "HELP": true,
		"MJOIN": true, "MPART": true, "MRECONNECT": true,
		"ADDNICK": true, "DELNICK": true, "LISTNICKS": true,
		"ADDOWNER": true, "DELOWNER": true, "LISTOWNERS": true,
		"CFLO": true, "NFLO": true, "INFO": true,
	}

	return validCommands[cmdName]
}

// validateInput sprawdza i czyści dane wejściowe
func (dt *DCCTunnel) validateInput(input string) string {
	// Usuń znaki nowej linii i powrotu karetki
	input = strings.TrimSpace(input)
	input = strings.ReplaceAll(input, "\r", "")
	input = strings.ReplaceAll(input, "\n", "")

	// Ogranicz długość wejścia
	maxLength := 512 // Standardowe ograniczenie IRC
	if len(input) > maxLength {
		input = input[:maxLength]
	}

	return input
}
