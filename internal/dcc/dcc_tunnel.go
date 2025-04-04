package dcc

import (
	"bufio"
	"fmt"
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
	if dt.conn != nil {
		dt.conn.Close()
	}

	// Opuszczamy PartyLine
	dt.partyLine.RemoveSession(dt.sessionID)

	if dt.onStop != nil {
		dt.onStop()
	}
	dt.mu.Unlock()
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
	dt.mu.Lock()
	defer dt.mu.Unlock()

	if !dt.active || dt.conn == nil {
		return
	}

	// Ignorowanie określonych zdarzeń
	if strings.Contains(data, " 303 ") {
		return
	}

	parsedMessage := dt.parseIRCMessage(data)
	if parsedMessage == "" {
		return
	}

	// Nieblokujące wysyłanie z timeoutem
	done := make(chan bool, 1)
	go func() {
		dt.conn.Write([]byte(parsedMessage + "\r\n"))
		done <- true
	}()

	select {
	case <-done:
		util.Debug("DCC: Message sent successfully")
	case <-time.After(time.Second * 5):
		util.Warning("DCC: Write timeout, stopping tunnel")
		dt.Stop()
	}
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

// sendToClient wysyła wiadomość do klienta DCC
func (dt *DCCTunnel) sendToClient(message string) {
	if dt.conn != nil {
		dt.conn.Write([]byte(message + "\r\n"))
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
	pl.mutex.Lock()
	defer pl.mutex.Unlock()

	pl.sessions[tunnel.sessionID] = tunnel

	for _, msg := range pl.messageLog {
		formattedMsg := fmt.Sprintf("[%s] %s: %s",
			msg.Timestamp.Format("15:04:05"),
			msg.Sender,
			msg.Message)
		tunnel.sendToClient(formattedMsg)
	}

	pl.broadcast(fmt.Sprintf("*** %s joined the party line ***", tunnel.ownerNick), "")
}

func (pl *PartyLine) RemoveSession(sessionID string) {
	pl.mutex.Lock()
	defer pl.mutex.Unlock()

	if tunnel, exists := pl.sessions[sessionID]; exists {
		pl.broadcast(fmt.Sprintf("*** %s left the party line ***", tunnel.ownerNick), sessionID)
		delete(pl.sessions, sessionID)
	}
}

func (pl *PartyLine) broadcast(message string, excludeSessionID string) {
	pl.mutex.Lock()
	defer pl.mutex.Unlock()

	// Wysyłanie wiadomości do tuneli
	for id, tunnel := range pl.sessions {
		if id != excludeSessionID {
			tunnel.sendToClient(message)
		}
	}

	// Dodajemy do historii tylko wiadomości od użytkowników (nie systemowe)
	if !strings.HasPrefix(message, "***") {
		// Bezpieczne sprawdzenie czy istnieje sesja wyłączona z broadcastu
		if excludeSessionID != "" {
			if tunnel, exists := pl.sessions[excludeSessionID]; exists {
				pl.addToMessageLog(PartyLineMessage{
					Timestamp: time.Now(),
					Sender:    tunnel.bot.GetCurrentNick(),
					Message:   message,
				})
			}
		}
	}
}

func (pl *PartyLine) addToMessageLog(msg PartyLineMessage) {
	if len(pl.messageLog) >= pl.maxLogSize {
		pl.messageLog = pl.messageLog[1:]
	}
	pl.messageLog = append(pl.messageLog, msg)
}

// W dcc_tunnel.go
func (dt *DCCTunnel) HandleDisconnect() {
	dt.sendToClient("Connection closed by remote host")
	dt.Stop()
}
