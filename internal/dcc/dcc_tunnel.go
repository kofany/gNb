package dcc

import (
	"bufio"
	"fmt"
	"net"
	"strings"
	"sync"
	"time"

	"github.com/kofany/gNb/internal/command"
	"github.com/kofany/gNb/internal/irc"
	"github.com/kofany/gNb/internal/types"
	"github.com/kofany/gNb/internal/util"
)

// DCCTunnel reprezentuje tunel DCC do komunikacji z botem
type DCCTunnel struct {
	conn           net.Conn
	bot            types.Bot
	active         bool
	mu             sync.Mutex
	ignoredEvents  map[string]bool
	onStop         func()
	formatter      *MessageFormatter
	botManager     types.BotManager
	readDone       chan struct{}
	writeDone      chan struct{}
	sessionID      string
	partyLine      *PartyLine
	ownerNick      string
	commandAdapter *command.DCCAdapter // New field for command processing
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
		bot:            bot,
		active:         false,
		ignoredEvents:  map[string]bool{"303": true},
		onStop:         onStop,
		formatter:      NewMessageFormatter(bot.GetCurrentNick()),
		botManager:     bot.GetBotManager(),
		sessionID:      sessionID,
		partyLine:      GetGlobalPartyLine(),
		ownerNick:      ownerNick,
		commandAdapter: command.NewDCCAdapter(), // Initialize the command adapter
	}

	// Initialize the command system
	dt.commandAdapter.Initialize()

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

	// Apply TCP optimizations if we have a TCP connection
	if tcpConn, ok := conn.(*net.TCPConn); ok {
		// Disable Nagle's algorithm for interactive connections
		tcpConn.SetNoDelay(true)

		// Enable TCP keepalives at the socket level
		tcpConn.SetKeepAlive(true)
		tcpConn.SetKeepAlivePeriod(60 * time.Second)

		util.Debug("DCC: Applied TCP optimizations to connection for bot %s", dt.bot.GetCurrentNick())
	}

	dt.mu.Unlock()

	util.Debug("DCC: DCC tunnel started for bot %s", dt.bot.GetCurrentNick())

	welcomeMessage := dt.getWelcomeMessage()
	dt.conn.Write([]byte(welcomeMessage + "\r\n"))

	// Dołączamy do PartyLine
	dt.partyLine.AddSession(dt)

	// Start a keep-alive goroutine to prevent connection timeouts
	go dt.keepAliveLoop()

	go dt.readLoop()
}

// keepAliveLoop sends periodic keep-alive messages to maintain the connection
func (dt *DCCTunnel) keepAliveLoop() {
	// Create a ticker that fires every 180 seconds (3 minutes)
	// This is below the typical NAT timeout of 5 minutes
	keepAliveTicker := time.NewTicker(180 * time.Second)
	defer keepAliveTicker.Stop()

	for {
		select {
		case <-keepAliveTicker.C:
			// Check if the tunnel is still active
			if !dt.IsActive() {
				return
			}

			// Send a keep-alive message
			dt.sendKeepAlive()

		case <-dt.readDone:
			// Tunnel was closed, exit the loop
			return
		}
	}
}

// sendKeepAlive sends a minimal keep-alive ping to keep the connection alive
func (dt *DCCTunnel) sendKeepAlive() {
	dt.mu.Lock()
	if !dt.active || dt.conn == nil {
		dt.mu.Unlock()
		return
	}
	dt.mu.Unlock()

	// Send a minimal keep-alive message (a single space followed by newline is invisible to the user)
	// This is smaller than a PING/PONG and serves just to keep the connection alive
	done := make(chan bool, 1)
	writeErr := make(chan error, 1)

	go func() {
		dt.mu.Lock()
		if !dt.active || dt.conn == nil {
			dt.mu.Unlock()
			writeErr <- nil // Not really an error, just signaling we're done
			return
		}

		util.Debug("DCC: Sending keep-alive to maintain connection for %s", dt.bot.GetCurrentNick())
		// Set a deadline for the write operation
		dt.conn.SetWriteDeadline(time.Now().Add(2 * time.Second))
		_, err := dt.conn.Write([]byte(" \r\n"))
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
		// Keep-alive sent successfully
	case err := <-writeErr:
		if err != nil {
			util.Warning("DCC: Keep-alive write error: %v", err)
			dt.Stop()
		}
	case <-time.After(3 * time.Second):
		util.Warning("DCC: Keep-alive write timeout, stopping tunnel")
		dt.Stop()
	}
}

func (dt *DCCTunnel) readLoop() {
	defer func() {
		// Recover from any panics
		if r := recover(); r != nil {
			util.Error("DCC: Panic in readLoop: %v", r)
		}

		// Clean up resources
		dt.mu.Lock()
		dt.active = false
		if dt.readDone != nil {
			close(dt.readDone)
		}
		dt.mu.Unlock()
		dt.Stop()
	}()

	// Set a longer read deadline now that we have keepalives - 5 minutes (300 seconds)
	// Our keepalive is every 3 minutes, so this gives ample time for a keepalive to arrive
	dt.mu.Lock()
	if dt.conn != nil {
		dt.conn.SetReadDeadline(time.Now().Add(300 * time.Second))
	}
	dt.mu.Unlock()

	scanner := bufio.NewScanner(dt.conn)
	scanner.Buffer(make([]byte, 4096), 1024*1024) // Increase buffer size to handle larger messages

	readTimeoutTicker := time.NewTicker(120 * time.Second) // Check less frequently since we have keepalives
	defer readTimeoutTicker.Stop()

	// Use a separate goroutine to read from the scanner
	readChan := make(chan string, 10)
	errChan := make(chan error, 1)
	quitChan := make(chan struct{})

	go func() {
		defer close(readChan)
		defer close(errChan)

		for {
			select {
			case <-quitChan:
				return
			default:
				// Check if scanner can read more
				if !scanner.Scan() {
					if err := scanner.Err(); err != nil {
						errChan <- err
					}
					return
				}

				// Send the line to the read channel
				text := scanner.Text()

				// Skip empty keepalive messages when they come back
				if strings.TrimSpace(text) != "" {
					readChan <- text
				}

				// Reset the read deadline after receiving any data - adding 5 minutes
				dt.mu.Lock()
				if dt.conn != nil {
					dt.conn.SetReadDeadline(time.Now().Add(300 * time.Second))
				}
				dt.mu.Unlock()
			}
		}
	}()

	// Main loop to process read lines
	for {
		select {
		case line, ok := <-readChan:
			if !ok {
				// Channel closed, exit the loop
				return
			}

			util.Debug("DCC: Received from DCC connection: %s", line)

			// Process the line in a separate goroutine to avoid blocking
			go func(input string) {
				defer func() {
					if r := recover(); r != nil {
						util.Error("DCC: Panic in handleUserInput: %v", r)
					}
				}()

				dt.handleUserInput(input)
			}(line)

		case err := <-errChan:
			util.Error("DCC: Error reading from DCC Chat connection: %v", err)
			return

		case <-readTimeoutTicker.C:
			// Check if the connection is still active
			dt.mu.Lock()
			isActive := dt.active
			dt.mu.Unlock()

			if !isActive {
				close(quitChan)
				return
			}

			// No need to reset deadline here as the keepaliveLoop handles that
		}
	}
}

// Stop zatrzymuje tunel DCC
func (dt *DCCTunnel) Stop() {
	// First check if already stopped without locking
	if !dt.IsActive() {
		return
	}

	dt.mu.Lock()
	if !dt.active {
		dt.mu.Unlock()
		return
	}

	// Mark as inactive first
	dt.active = false

	// Get references to needed fields before unlocking
	partyLine := dt.partyLine
	sessionID := dt.sessionID
	ownerNick := dt.ownerNick
	botNick := "unknown"
	if dt.bot != nil {
		botNick = dt.bot.GetCurrentNick()
	}

	// Close the connection with proper error handling
	var conn net.Conn
	if dt.conn != nil {
		conn = dt.conn
		dt.conn = nil // Clear the reference
	}
	dt.mu.Unlock()

	// Log the stop operation
	util.Debug("DCC: Stopping tunnel for owner %s on bot %s (session: %s)", ownerNick, botNick, sessionID)

	// Close the connection outside the lock
	if conn != nil {
		// Set a short deadline to avoid blocking on close
		conn.SetDeadline(time.Now().Add(1 * time.Second))
		err := conn.Close()
		if err != nil {
			util.Warning("DCC: Error closing connection: %v", err)
		}
	}

	// Remove from party line
	if partyLine != nil {
		partyLine.RemoveSession(sessionID)
	}

	// Call the onStop callback
	if dt.onStop != nil {
		dt.onStop()
	}

	// Signal that the read loop should exit
	dt.mu.Lock()
	if dt.readDone != nil {
		close(dt.readDone)
		dt.readDone = nil
	}
	if dt.writeDone != nil {
		close(dt.writeDone)
		dt.writeDone = nil
	}
	dt.mu.Unlock()

	util.Debug("DCC: Tunnel stopped for owner %s on bot %s", ownerNick, botNick)
}

// handleUserInput przetwarza dane wejściowe od użytkownika
func (dt *DCCTunnel) handleUserInput(input string) {
	// Sanitize input
	input = strings.TrimSpace(input)
	input = strings.ReplaceAll(input, "\r", "")
	input = strings.ReplaceAll(input, "\n", "")

	// Check if the tunnel is still active
	dt.mu.Lock()
	isActive := dt.active
	ownerNick := dt.ownerNick
	botNick := dt.bot.GetCurrentNick()
	sessionID := dt.sessionID
	dt.mu.Unlock()

	if !isActive {
		util.Debug("DCC: Ignoring input from inactive tunnel: %s", input)
		return
	}

	// Process the input with timeout protection
	timeoutChan := time.After(5 * time.Second)
	doneChan := make(chan struct{})

	go func() {
		defer func() {
			close(doneChan)
			if r := recover(); r != nil {
				util.Error("DCC: Panic in handleUserInput: %v", r)
			}
		}()

		if strings.HasPrefix(input, ".") {
			// Use the new command system to process commands
			dt.commandAdapter.ProcessCommand(
				input,
				dt.bot,
				ownerNick,
				dt.sendToClient,
			)
		} else if input != "" { // Only broadcast non-empty messages
			// Regular message to party line - don't use the global mutex for this
			// to avoid blocking party line messages when commands are being processed
			timestamp := colorText(time.Now().Format("15:04:05"), 14)
			formattedMsg := fmt.Sprintf("[%s] %s%s%s %s",
				timestamp,
				colorText("<", 13),
				colorText(ownerNick, 14),
				colorText(">", 13),
				input)

			// Check if partyLine is available
			if dt.partyLine != nil {
				util.Debug("DCC: Broadcasting party line message from %s on bot %s: %s",
					ownerNick, botNick, input)

				// Create message object
				msg := PartyLineMessage{
					Timestamp: time.Now(),
					Sender:    ownerNick,
					Message:   input,
				}

				// Add to message log first
				dt.partyLine.addToMessageLog(msg)

				// Use the command adapter's party line feature
				dt.commandAdapter.SendPartyLineMessage(input, ownerNick, sessionID)

				// Then broadcast to all - use a separate goroutine to avoid blocking
				go dt.partyLine.broadcast(formattedMsg, sessionID)
			} else {
				util.Warning("DCC: PartyLine is nil, cannot broadcast message")
			}
		}
	}()

	// Wait for processing to complete or timeout
	select {
	case <-doneChan:
		// Processing completed
	case <-timeoutChan:
		util.Warning("DCC: Timeout processing user input: %s", input)
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

// sendToClient wysyła wiadomość do klienta DCC
func (dt *DCCTunnel) sendToClient(message string) {
	// Quick check without lock first
	if !dt.active {
		return
	}

	// Use a separate goroutine to avoid blocking
	go func(msg string) {
		dt.mu.Lock()
		if !dt.active || dt.conn == nil {
			dt.mu.Unlock()
			return
		}

		// Set a deadline for the write operation
		dt.conn.SetWriteDeadline(time.Now().Add(2 * time.Second))
		_, err := dt.conn.Write([]byte(msg + "\r\n"))
		dt.conn.SetWriteDeadline(time.Time{}) // Reset deadline
		dt.mu.Unlock()

		if err != nil {
			util.Warning("DCC: Error sending message to client: %v", err)
			// Don't call Stop() here to avoid potential deadlocks
			// Instead, schedule a stop
			go dt.Stop()
		}
	}(message)
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
	if tunnel == nil {
		util.Warning("PartyLine: Attempted to add nil tunnel")
		return
	}

	pl.mutex.Lock()

	// Generate a unique session ID
	sessionID := tunnel.sessionID
	botNick := tunnel.bot.GetCurrentNick()
	ownerNick := tunnel.ownerNick

	// Check if session already exists
	if _, exists := pl.sessions[sessionID]; exists {
		util.Warning("PartyLine: Session %s already exists", sessionID)
		pl.mutex.Unlock()
		return
	}

	// Check if owner already has a session and close it if needed
	for existingID, existingTunnel := range pl.sessions {
		if existingTunnel != nil && existingTunnel.ownerNick == ownerNick {
			util.Debug("PartyLine: Owner %s already has a session %s, will be replaced by new session %s",
				ownerNick, existingID, sessionID)
			// Don't close it here to avoid deadlocks, just note it
		}
	}

	// Add the session
	pl.sessions[sessionID] = tunnel

	// Log session addition
	util.Debug("PartyLine: Added session %s (bot: %s, owner: %s)", sessionID, botNick, ownerNick)

	// Create a copy of the message log to avoid holding the lock while sending
	messageLogCopy := make([]PartyLineMessage, len(pl.messageLog))
	copy(messageLogCopy, pl.messageLog)

	// Get current online users from active tunnels only
	var onlineUsers []string
	for _, t := range pl.sessions {
		if t != nil && t.IsActive() && t.ownerNick != "" {
			onlineUsers = append(onlineUsers, t.ownerNick)
		}
	}
	pl.mutex.Unlock()

	// Send welcome message in a separate goroutine to avoid blocking
	go func() {
		// Send welcome message
		welcomeMsg := fmt.Sprintf("*** Welcome to the PartyLine, %s! ***", ownerNick)
		tunnel.sendToClient(welcomeMsg)

		// Send current users message
		if len(onlineUsers) > 0 {
			usersMsg := fmt.Sprintf("*** Current users: %s ***", strings.Join(onlineUsers, ", "))
			tunnel.sendToClient(usersMsg)
		}

		// Send message history to the new client
		for _, msg := range messageLogCopy {
			formattedTime := msg.Timestamp.Format("15:04:05")
			formattedMsg := fmt.Sprintf("[%s] %s%s%s %s",
				colorText(formattedTime, 14),
				colorText("<", 13),
				colorText(msg.Sender, 14),
				colorText(">", 13),
				msg.Message)
			tunnel.sendToClient(formattedMsg)
		}

		// Broadcast join message to other users
		pl.broadcast(fmt.Sprintf("*** %s joined the party line ***", ownerNick), sessionID)
	}()
}

// addToMessageLog dodaje wiadomość do historii PartyLine
func (pl *PartyLine) addToMessageLog(msg PartyLineMessage) {
	pl.mutex.Lock()
	defer pl.mutex.Unlock()

	// Add the message to the log
	pl.messageLog = append(pl.messageLog, msg)

	// Trim the log if it exceeds the maximum size
	if len(pl.messageLog) > pl.maxLogSize {
		pl.messageLog = pl.messageLog[len(pl.messageLog)-pl.maxLogSize:]
	}

	util.Debug("PartyLine: Added message to log from %s: %s", msg.Sender, msg.Message)
}

func (pl *PartyLine) RemoveSession(sessionID string) {
	if sessionID == "" {
		util.Warning("PartyLine: Attempted to remove empty sessionID")
		return
	}

	pl.mutex.Lock()
	tunnel, exists := pl.sessions[sessionID]
	if !exists {
		util.Debug("PartyLine: Session %s not found for removal", sessionID)
		pl.mutex.Unlock()
		return
	}

	// Get the owner nick and bot nick before deleting the session
	ownerNick := tunnel.ownerNick
	botNick := "unknown"
	if tunnel.bot != nil {
		botNick = tunnel.bot.GetCurrentNick()
	}

	util.Debug("PartyLine: Removing session %s (bot: %s, owner: %s)", sessionID, botNick, ownerNick)

	// Delete the session
	delete(pl.sessions, sessionID)

	// Check if there are any other sessions for this owner
	hasOtherSessions := false
	for _, t := range pl.sessions {
		if t != nil && t.ownerNick == ownerNick && t.IsActive() {
			hasOtherSessions = true
			util.Debug("PartyLine: Owner %s still has another active session", ownerNick)
			break
		}
	}
	pl.mutex.Unlock()

	// Only broadcast leave message if this was the owner's last session
	if ownerNick != "" && !hasOtherSessions {
		// Broadcast the leave message outside the lock
		go pl.broadcast(fmt.Sprintf("*** %s left the party line ***", ownerNick), sessionID)
	}
}

func (pl *PartyLine) broadcast(message string, excludeSessionID string) {
	// Sanitize input
	message = strings.TrimSpace(message)

	// Log the broadcast operation
	util.Debug("PartyLine: Broadcasting message: %s (excluding session: %s)", message, excludeSessionID)

	// Acquire lock to safely access sessions
	pl.mutex.Lock()

	// Log the sender's information if this is a user message
	if !strings.HasPrefix(message, "***") && excludeSessionID != "" {
		if tunnel, exists := pl.sessions[excludeSessionID]; exists && tunnel != nil {
			if tunnel.bot != nil {
				// Use the owner's nick, not the bot's nick
				util.Debug("PartyLine: Message sender is %s on bot %s", tunnel.ownerNick, tunnel.bot.GetCurrentNick())
			}
		}
	}

	// Create a safe copy of sessions to avoid holding the lock while sending
	sessionsCopy := make(map[string]*DCCTunnel)
	var activeOwners []string
	for id, tunnel := range pl.sessions {
		if id != "" && tunnel != nil && tunnel.IsActive() {
			// Only add to the copy if it's not the sender's session
			if id != excludeSessionID {
				sessionsCopy[id] = tunnel
				util.Debug("PartyLine: Will send to session %s (bot: %s, owner: %s)",
					id, tunnel.bot.GetCurrentNick(), tunnel.ownerNick)
			}

			// Add to active owners list for logging
			if tunnel.ownerNick != "" {
				activeOwners = append(activeOwners, tunnel.ownerNick)
			}
		}
	}
	pl.mutex.Unlock()

	util.Debug("PartyLine: Broadcasting to %d sessions, active owners: %s",
		len(sessionsCopy), strings.Join(activeOwners, ", "))

	// Then broadcast to all other sessions
	var wg sync.WaitGroup
	for _, tunnel := range sessionsCopy {
		// Skip nil tunnels
		if tunnel == nil {
			continue
		}

		// Use a goroutine with timeout to prevent blocking if one client is slow
		wg.Add(1)
		go func(t *DCCTunnel, msg string) {
			defer wg.Done()

			// Use a timeout to prevent hanging
			timeoutChan := time.After(2 * time.Second)
			doneChan := make(chan struct{})

			go func() {
				defer close(doneChan)
				defer func() {
					if r := recover(); r != nil {
						util.Error("DCC: Panic in broadcast sendToClient: %v", r)
					}
				}()

				// Check if tunnel is still active before sending
				if t.IsActive() {
					t.sendToClient(msg)
				}
			}()

			// Wait for the message to be sent or timeout
			select {
			case <-doneChan:
				// Message sent successfully
			case <-timeoutChan:
				// Message sending timed out
				util.Warning("DCC: Timeout sending broadcast message to client %s", t.ownerNick)
			}
		}(tunnel, message)
	}

	// Wait for all broadcasts to complete with a timeout
	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()

	select {
	case <-done:
		// All broadcasts completed
		util.Debug("PartyLine: Broadcast completed successfully")
	case <-time.After(5 * time.Second):
		// Timeout occurred
		util.Warning("DCC: Timeout waiting for all broadcast messages to complete")
	}

	// Note: We don't add to message log here anymore
	// This is now handled by the caller (handleUserInput) to avoid duplicates
	// and ensure proper ordering of messages
}

// This method has been replaced by the improved version above

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
