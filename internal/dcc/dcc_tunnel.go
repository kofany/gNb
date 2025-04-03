package dcc

import (
	"bufio"
	"fmt"
	"io"
	"net"
	"strings"
	"sync"
	"time"

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
	readDone      chan struct{}
	writeDone     chan struct{}
	sessionID     string
	partyLine     *PartyLine
	ownerNick     string // Dodane pole
}

type PartyLine struct {
	sessions   map[string]*DCCTunnel
	mutex      sync.RWMutex
	messageLog []PartyLineMessage
	maxLogSize int
}

type PartyLineMessage struct {
	Timestamp time.Time
	Sender    string
	Message   string
}

var (
	globalPartyLine *PartyLine
	partyLineOnce   sync.Once
)

// NewDCCTunnel tworzy nową instancję tunelu DCC
func NewDCCTunnel(bot types.Bot, ownerNick string, onStop func()) *DCCTunnel {
	sessionID := fmt.Sprintf("dcc-%s-%d", bot.GetCurrentNick(), time.Now().UnixNano())
	dt := &DCCTunnel{
		bot:           bot,
		active:        false,
		ignoredEvents: map[string]bool{"303": true},
		onStop:        onStop,
		formatter:     NewMessageFormatter(bot.GetCurrentNick()),
		botManager:    bot.GetBotManager(),
		sessionID:     sessionID,
		partyLine:     GetGlobalPartyLine(),
		ownerNick:     ownerNick,
	}
	return dt
}

// Start inicjuje tunel DCC
func (dt *DCCTunnel) Start(conn net.Conn) {
	dt.mu.Lock()
	if dt.active {
		util.Warning("DCC: DCC tunnel already active for bot %s", dt.bot.GetCurrentNick())
		dt.mu.Unlock()
		return
	}

	dt.conn = conn
	dt.active = true
	dt.readDone = make(chan struct{})
	dt.writeDone = make(chan struct{})
	dt.mu.Unlock()

	util.Debug("DCC: DCC tunnel started for bot %s", dt.bot.GetCurrentNick())

	welcomeMessage := dt.getWelcomeMessage()
	dt.conn.Write([]byte(welcomeMessage + "\r\n"))

	// Dołączamy do PartyLine
	dt.partyLine.AddSession(dt)

	go dt.readLoop()
}

func (dt *DCCTunnel) readLoop() {
	defer func() {
		dt.mu.Lock()
		dt.active = false
		if dt.readDone != nil {
			close(dt.readDone)
		}
		dt.mu.Unlock()
		dt.Stop()
	}()

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

// Stop zatrzymuje tunel DCC
func (dt *DCCTunnel) Stop() {
	dt.mu.Lock()
	if !dt.active {
		dt.mu.Unlock()
		return
	}

	dt.active = false

	// Close the connection with proper error handling
	if dt.conn != nil {
		// Set a short deadline to avoid blocking on close
		dt.conn.SetDeadline(time.Now().Add(1 * time.Second))
		err := dt.conn.Close()
		if err != nil {
			util.Warning("DCC: Error closing connection: %v", err)
		}
		dt.conn = nil
	}

	// Opuszczamy PartyLine - do this outside the lock to avoid deadlocks
	partyLine := dt.partyLine
	sessionID := dt.sessionID
	dt.mu.Unlock()

	// Remove from party line
	if partyLine != nil {
		partyLine.RemoveSession(sessionID)
	}

	// Call the onStop callback
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
		timestamp := colorText(time.Now().Format("15:04:05"), 14)
		formattedMsg := fmt.Sprintf("[%s] %s%s%s %s",
			timestamp,
			colorText("<", 13),
			colorText(dt.ownerNick, 14),
			colorText(">", 13),
			input)
		dt.partyLine.broadcast(formattedMsg, dt.sessionID)
	}
}

func (dt *DCCTunnel) WriteToConn(data string) {
	// Quick check without lock first
	if !dt.active {
		return
	}

	// Ignorowanie określonych zdarzeń
	if strings.Contains(data, " 303 ") || strings.Contains(data, "PING") {
		return
	}

	// Parse the message outside the lock
	parsedMessage := dt.parseIRCMessage(data)
	if parsedMessage == "" {
		return
	}

	// Use a separate goroutine to avoid blocking the main thread
	go func(msg string) {
		dt.mu.Lock()
		if !dt.active || dt.conn == nil {
			dt.mu.Unlock()
			return
		}
		dt.mu.Unlock()

		// Nieblokujące wysyłanie z timeoutem
		done := make(chan bool, 1)
		writeErr := make(chan error, 1)

		go func() {
			dt.mu.Lock()
			if !dt.active || dt.conn == nil {
				dt.mu.Unlock()
				writeErr <- fmt.Errorf("connection closed")
				return
			}

			// Set a deadline for the write operation
			dt.conn.SetWriteDeadline(time.Now().Add(2 * time.Second))
			_, err := dt.conn.Write([]byte(msg + "\r\n"))
			dt.conn.SetWriteDeadline(time.Time{}) // Reset deadline
			dt.mu.Unlock()

			if err != nil {
				writeErr <- err
			} else {
				done <- true
			}
		}()

		select {
		case <-done:
			// Message sent successfully
		case err := <-writeErr:
			util.Warning("DCC: Write error: %v", err)
			dt.Stop()
		case <-time.After(3 * time.Second):
			util.Warning("DCC: Write timeout, stopping tunnel")
			dt.Stop()
		}
	}(parsedMessage)
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
	dt.mu.Lock()
	if !dt.active || dt.conn == nil {
		dt.mu.Unlock()
		return
	}

	// Set a deadline for the write operation
	dt.conn.SetWriteDeadline(time.Now().Add(2 * time.Second))
	_, err := dt.conn.Write([]byte(message + "\r\n"))
	dt.conn.SetWriteDeadline(time.Time{}) // Reset deadline
	dt.mu.Unlock()

	if err != nil {
		util.Warning("DCC: Error sending message to client: %v", err)
		// Don't call Stop() here to avoid potential deadlocks
		// Instead, schedule a stop
		go dt.Stop()
	}
}

// getWelcomeMessage zwraca wiadomość powitalną dla połączenia DCC
func (dt *DCCTunnel) getWelcomeMessage() string {
	welcomeMessage :=
		colorText("Welcome to the Bot Interface", 7) + "\n" +
			colorText("===========================\n", 11) + "\n" +
			colorText("    <<<<<<<<<<<<<<<<<<<<<<<<<<<<<<<<<<<<<<<<<[phantom Node bot]\n", 10) +
			colorText("                 ___      __             __      \n", 13) +
			colorText("    ____  ____  [ m ]__  / /_  __  __   / /____  ____ _____ ___\n", 13) +
			colorText("   / __ \\/ __ \\  / / _ \\/ __ \\/ / / /  / __/ _ \\/ __ `/ __ `__ \\\n", 13) +
			colorText("  / /_/ / /_/ / / /  __/ /_/ / /_/ /  / /_/  __/ /_/ / / / / / /\n", 13) +
			colorText(" / .___/\\____/_/ /\\___/_.___/\\__, /blo\\__/\\___/\\__,_/_/ /_/ /_/\n", 13) +
			colorText("/_/  ruciu  /___/   dominik /____/                     kofany\n\n", 13) +
			colorText("Type your IRC commands here using '.' as the prefix.\n", 12) +
			colorText("Type .help to see available commands.\n\n", 12)

	return welcomeMessage
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

// PARTYLINE

func GetGlobalPartyLine() *PartyLine {
	partyLineOnce.Do(func() {
		globalPartyLine = &PartyLine{
			sessions:   make(map[string]*DCCTunnel),
			maxLogSize: 100, // Przechowujemy ostatnie 100 wiadomości
			messageLog: make([]PartyLineMessage, 0, 100),
		}
	})
	return globalPartyLine
}

// Metody PartyLine
func (pl *PartyLine) AddSession(tunnel *DCCTunnel) {
	if tunnel == nil {
		util.Warning("PartyLine: Attempted to add nil tunnel")
		return
	}

	pl.mutex.Lock()
	// Check if session already exists
	if _, exists := pl.sessions[tunnel.sessionID]; exists {
		util.Warning("PartyLine: Session %s already exists", tunnel.sessionID)
		pl.mutex.Unlock()
		return
	}

	// Add the session
	pl.sessions[tunnel.sessionID] = tunnel

	// Create a copy of the message log to avoid holding the lock while sending messages
	messageLogCopy := make([]PartyLineMessage, len(pl.messageLog))
	copy(messageLogCopy, pl.messageLog)
	pl.mutex.Unlock()

	// Send message history to the new client
	for _, msg := range messageLogCopy {
		formattedMsg := fmt.Sprintf("[%s] %s: %s",
			msg.Timestamp.Format("15:04:05"),
			msg.Sender,
			msg.Message)
		tunnel.sendToClient(formattedMsg)
	}

	// Broadcast join message
	pl.broadcast(fmt.Sprintf("*** %s joined the party line ***", tunnel.ownerNick), "")
}

func (pl *PartyLine) RemoveSession(sessionID string) {
	if sessionID == "" {
		util.Warning("PartyLine: Attempted to remove empty sessionID")
		return
	}

	pl.mutex.Lock()
	tunnel, exists := pl.sessions[sessionID]
	if !exists {
		pl.mutex.Unlock()
		return
	}

	// Get the owner nick before deleting the session
	ownerNick := tunnel.ownerNick

	// Delete the session
	delete(pl.sessions, sessionID)
	pl.mutex.Unlock()

	// Broadcast the leave message outside the lock
	if ownerNick != "" {
		pl.broadcast(fmt.Sprintf("*** %s left the party line ***", ownerNick), sessionID)
	}
}

func (pl *PartyLine) broadcast(message string, excludeSessionID string) {
	// First, safely get the sender's nick if this is a user message
	var senderNick string
	if !strings.HasPrefix(message, "***") && excludeSessionID != "" {
		if tunnel, exists := pl.sessions[excludeSessionID]; exists {
			senderNick = tunnel.bot.GetCurrentNick()
		}
	}

	// Then broadcast to all other sessions
	for id, tunnel := range pl.sessions {
		if id != excludeSessionID {
			// Use a goroutine to prevent blocking if one client is slow
			go func(t *DCCTunnel, msg string) {
				t.sendToClient(msg)
			}(tunnel, message)
		}
	}

	// Dodajemy do historii tylko wiadomości od użytkowników (nie systemowe)
	if !strings.HasPrefix(message, "***") && senderNick != "" {
		pl.addToMessageLog(PartyLineMessage{
			Timestamp: time.Now(),
			Sender:    senderNick,
			Message:   message,
		})
	}
}

func (pl *PartyLine) addToMessageLog(msg PartyLineMessage) {
	pl.mutex.Lock()
	defer pl.mutex.Unlock()

	if len(pl.messageLog) >= pl.maxLogSize {
		pl.messageLog = pl.messageLog[1:]
	}
	pl.messageLog = append(pl.messageLog, msg)
}

// HandleDisconnect handles a disconnection event
func (dt *DCCTunnel) HandleDisconnect() {
	// Check if the tunnel is still active before sending a message
	dt.mu.Lock()
	isActive := dt.active
	dt.mu.Unlock()

	if isActive {
		util.Info("DCC: Handling disconnect for bot %s", dt.bot.GetCurrentNick())
		dt.sendToClient("Connection closed by remote host")
		dt.Stop()
	}
}
