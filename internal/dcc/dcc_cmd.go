package dcc

import (
	"context"
	"crypto/tls"
	"fmt"
	"io"
	"math/rand"
	"net"
	"net/http"
	"os"
	"os/user"
	"strings"
	"sync"
	"time"

	"github.com/kofany/gNb/internal/types"
	"github.com/kofany/gNb/internal/util"
)

// dccCommandMutex is a global mutex to ensure only one DCC command is processed at a time
var dccCommandMutex sync.Mutex

// processCommand przetwarza komendy od użytkownika
func (dt *DCCTunnel) processCommand(command string) {
	// Sanitize input
	command = strings.TrimSpace(command)
	command = strings.ReplaceAll(command, "\r", "")
	command = strings.ReplaceAll(command, "\n", "")

	fields := strings.Fields(command)
	if len(fields) == 0 {
		return
	}

	// Usuń prefiks '.' i przekonwertuj na wielkie litery
	cmd := strings.ToUpper(strings.TrimPrefix(fields[0], "."))
	args := fields[1:]

	// Create a map of command handlers for better organization and maintainability
	commandHandlers := map[string]func([]string){
		"MSG":        dt.handleMsgCommand,
		"JOIN":       dt.handleJoinCommand,
		"PART":       dt.handlePartCommand,
		"MODE":       dt.handleModeCommand,
		"KICK":       dt.handleKickCommand,
		"QUIT":       dt.handleQuitCommand,
		"NICK":       dt.handleNickCommand,
		"RAW":        dt.handleRawCommand,
		"HELP":       func([]string) { dt.sendHelpMessage() },
		"MJOIN":      dt.handleMassJoinCommand,
		"MPART":      dt.handleMassPartCommand,
		"MRECONNECT": dt.handleMassReconnectCommand,
		"ADDNICK":    dt.handleAddNickCommand,
		"DELNICK":    dt.handleDelNickCommand,
		"LISTNICKS":  dt.handleListNicksCommand,
		"ADDOWNER":   dt.handleAddOwnerCommand,
		"DELOWNER":   dt.handleDelOwnerCommand,
		"LISTOWNERS": dt.handleListOwnersCommand,
		"INFO":       dt.handleInfoCommand,
		"BOTS":       dt.handleBotsCommand,
		"SERVERS":    dt.handleServersCommand,
	}

	// Find the handler for the command
	handler, exists := commandHandlers[cmd]
	if !exists {
		dt.sendToClient(fmt.Sprintf("Unknown command: %s", cmd))
		return
	}

	// Use a mutex to ensure only one DCC command is processed at a time
	// This is important to prevent race conditions and resource contention
	dccCommandMutex.Lock()
	defer dccCommandMutex.Unlock()

	// Execute the command with a longer timeout
	timeoutChan := time.After(30 * time.Second) // Increased timeout
	doneChan := make(chan struct{})

	go func() {
		defer func() {
			if r := recover(); r != nil {
				util.Error("DCC: Panic in command handler for %s: %v", cmd, r)
				dt.sendToClient(fmt.Sprintf("Error executing command: %v", r))
			}
			close(doneChan)
		}()

		// Execute the command handler
		handler(args)
	}()

	// Wait for the command to complete or timeout
	select {
	case <-doneChan:
		// Command completed normally
		util.Debug("DCC: Command %s completed successfully", cmd)
	case <-timeoutChan:
		// Command timed out
		util.Warning("DCC: Command %s timed out", cmd)
		dt.sendToClient(fmt.Sprintf("Command %s timed out. Please try again later.", cmd))
	}
}

// Handlery podstawowych komend

func (dt *DCCTunnel) handleBotsCommand(args []string) {
	// Use a timeout to prevent hanging
	timeoutChan := time.After(5 * time.Second)
	doneChan := make(chan struct{})

	go func() {
		defer func() {
			if r := recover(); r != nil {
				util.Error("DCC: Panic in handleBotsCommand: %v", r)
				dt.sendToClient(fmt.Sprintf("Error executing command: %v", r))
			}
			close(doneChan)
		}()

		bm := dt.bot.GetBotManager()
		if bm == nil {
			dt.sendToClient("BotManager is not available.")
			return
		}

		// Make a safe copy of the bots slice to avoid concurrent access issues
		bots := bm.GetBots()
		totalCreatedBots := bm.GetTotalCreatedBots()
		totalBotsNow := len(bots)

		// Liczymy w pełni połączone boty
		totalConnectedBots := 0
		var connectedBotNicks []string

		// Use a separate goroutine for each bot check to avoid blocking
		var wg sync.WaitGroup
		var mu sync.Mutex // To protect connectedBotNicks

		for _, bot := range bots {
			wg.Add(1)
			go func(b types.Bot) {
				defer wg.Done()
				if b.IsConnected() {
					mu.Lock()
					totalConnectedBots++
					connectedBotNicks = append(connectedBotNicks, b.GetCurrentNick())
					mu.Unlock()
				}
			}(bot)
		}

		// Wait for all bot checks to complete with a timeout
		done := make(chan struct{})
		go func() {
			wg.Wait()
			close(done)
		}()

		select {
		case <-done:
			// All checks completed
		case <-time.After(3 * time.Second):
			// Timeout occurred, continue with what we have
			util.Warning("DCC: Timeout while checking bot connections")
		}

		if len(args) == 0 {
			// Bez dodatkowych argumentów, wyświetlamy podsumowanie
			output := fmt.Sprintf(
				"Total created bots: %d\nTotal bots now: %d\nTotal fully connected bots: %d",
				totalCreatedBots, totalBotsNow, totalConnectedBots)
			dt.sendToClient(output)
		} else if len(args) == 1 && strings.ToLower(args[0]) == "n" {
			// Wyświetlamy nicki w pełni połączonych botów
			if totalConnectedBots == 0 {
				dt.sendToClient("No bots are currently connected.")
			} else {
				dt.sendToClient("Connected bots: " + strings.Join(connectedBotNicks, ", "))
			}
		} else {
			dt.sendToClient("Usage: .bots or .bots n")
		}
	}()

	// Wait for the command to complete or timeout
	select {
	case <-doneChan:
		// Command completed normally
	case <-timeoutChan:
		// Command timed out
		dt.sendToClient("Command timed out. Please try again later.")
	}
}

func (dt *DCCTunnel) handleMsgCommand(args []string) {
	if len(args) >= 2 {
		target := args[0]
		message := strings.Join(args[1:], " ")
		dt.bot.SendMessage(target, message)
	} else {
		dt.sendToClient("Usage: .msg <target> <message>")
	}
}

func (dt *DCCTunnel) handleJoinCommand(args []string) {
	if len(args) >= 1 {
		channel := args[0]
		dt.bot.JoinChannel(channel)
	} else {
		dt.sendToClient("Usage: .join <channel>")
	}
}

func (dt *DCCTunnel) handlePartCommand(args []string) {
	if len(args) >= 1 {
		channel := args[0]
		dt.bot.PartChannel(channel)
	} else {
		dt.sendToClient("Usage: .part <channel>")
	}
}

func (dt *DCCTunnel) handleModeCommand(args []string) {
	if len(args) >= 2 {
		target := args[0]
		modes := strings.Join(args[1:], " ")
		command := fmt.Sprintf("MODE %s %s", target, modes)
		dt.bot.SendRaw(command)
	} else if len(args) >= 1 {
		target := args[0]
		command := fmt.Sprintf("MODE %s", target)
		dt.bot.SendRaw(command)
	} else {
		dt.sendToClient("Usage: .mode <target> [modes] [args]")
	}
}

func (dt *DCCTunnel) handleKickCommand(args []string) {
	if len(args) >= 2 {
		channel := args[0]
		user := args[1]
		reason := ""
		if len(args) > 2 {
			reason = strings.Join(args[2:], " ")
		}
		command := fmt.Sprintf("KICK %s %s :%s", channel, user, reason)
		dt.bot.SendRaw(command)
	} else {
		dt.sendToClient("Usage: .kick <channel> <user> [reason]")
	}
}

func (dt *DCCTunnel) handleQuitCommand(_ []string) {
	dt.bot.Quit("Quit via DCC")
	dt.Stop()
}

func (dt *DCCTunnel) handleNickCommand(args []string) {
	if len(args) >= 1 {
		newNick := args[0]
		dt.bot.ChangeNick(newNick)
	} else {
		dt.sendToClient("Usage: .nick <newnick>")
	}
}

func (dt *DCCTunnel) handleRawCommand(args []string) {
	if len(args) >= 1 {
		rawCmd := strings.Join(args, " ")
		dt.bot.SendRaw(rawCmd)
	} else {
		dt.sendToClient("Usage: .raw <command>")
	}
}

func (dt *DCCTunnel) handleMassJoinCommand(args []string) {
	if len(args) >= 1 {
		channel := args[0]
		if bm := dt.bot.GetBotManager(); bm != nil {
			bm.CollectReactions(
				dt.bot.GetCurrentNick(),
				fmt.Sprintf("All bots are joining channel %s", channel),
				func() error {
					for _, bot := range bm.GetBots() {
						bot.JoinChannel(channel)
					}
					return nil
				},
			)
		}
	} else {
		dt.sendToClient("Usage: .mjoin <channel>")
	}
}

// Handlery komend masowych i administracyjnych

func (dt *DCCTunnel) handleMassPartCommand(args []string) {
	if len(args) >= 1 {
		channel := args[0]
		if bm := dt.bot.GetBotManager(); bm != nil {
			bm.CollectReactions(
				dt.bot.GetCurrentNick(),
				fmt.Sprintf("All bots are leaving channel %s", channel),
				func() error {
					for _, bot := range bm.GetBots() {
						bot.PartChannel(channel)
					}
					return nil
				},
			)
		}
	} else {
		dt.sendToClient("Usage: .mpart <channel>")
	}
}

func (dt *DCCTunnel) handleMassReconnectCommand(_ []string) {
	if bm := dt.bot.GetBotManager(); bm != nil {
		bm.CollectReactions(
			dt.bot.GetCurrentNick(),
			"All bots are reconnecting...",
			func() error {
				for _, bot := range bm.GetBots() {
					go bot.Reconnect()
				}
				return nil
			},
		)
	}
}

func (dt *DCCTunnel) handleAddNickCommand(args []string) {
	if len(args) >= 1 {
		nick := args[0]
		if bm := dt.bot.GetBotManager(); bm != nil {
			bm.CollectReactions(
				dt.bot.GetCurrentNick(),
				fmt.Sprintf("Nick '%s' has been added.", nick),
				func() error { return dt.bot.GetNickManager().AddNick(nick) },
			)
		}
	} else {
		dt.sendToClient("Usage: .addnick <nick>")
	}
}

func (dt *DCCTunnel) handleDelNickCommand(args []string) {
	if len(args) >= 1 {
		nick := args[0]
		if bm := dt.bot.GetBotManager(); bm != nil {
			bm.CollectReactions(
				dt.bot.GetCurrentNick(),
				fmt.Sprintf("Nick '%s' has been removed.", nick),
				func() error { return dt.bot.GetNickManager().RemoveNick(nick) },
			)
		}
	} else {
		dt.sendToClient("Usage: .delnick <nick>")
	}
}

func (dt *DCCTunnel) handleListNicksCommand(_ []string) {
	if bm := dt.bot.GetBotManager(); bm != nil {
		nicks := dt.bot.GetNickManager().GetNicks()
		dt.sendToClient(fmt.Sprintf("Current nicks: %s", strings.Join(nicks, ", ")))
	}
}

func (dt *DCCTunnel) handleAddOwnerCommand(args []string) {
	if len(args) >= 1 {
		ownerMask := args[0]
		if bm := dt.bot.GetBotManager(); bm != nil {
			bm.CollectReactions(
				dt.bot.GetCurrentNick(),
				fmt.Sprintf("Owner '%s' has been added.", ownerMask),
				func() error { return bm.AddOwner(ownerMask) },
			)
		}
	} else {
		dt.sendToClient("Usage: .addowner <mask>")
	}
}

func (dt *DCCTunnel) handleDelOwnerCommand(args []string) {
	if len(args) >= 1 {
		ownerMask := args[0]
		if bm := dt.bot.GetBotManager(); bm != nil {
			bm.CollectReactions(
				dt.bot.GetCurrentNick(),
				fmt.Sprintf("Owner '%s' has been removed.", ownerMask),
				func() error { return bm.RemoveOwner(ownerMask) },
			)
		}
	} else {
		dt.sendToClient("Usage: .delowner <mask>")
	}
}

func (dt *DCCTunnel) handleListOwnersCommand(_ []string) {
	if bm := dt.bot.GetBotManager(); bm != nil {
		owners := bm.GetOwners()
		dt.sendToClient(fmt.Sprintf("Current owners: %s", strings.Join(owners, ", ")))
	}
}

func (dt *DCCTunnel) handleInfoCommand(_ []string) {
	// Use a timeout to prevent hanging
	timeoutChan := time.After(15 * time.Second) // Longer timeout for this command
	doneChan := make(chan struct{})

	dt.sendToClient("Gathering system information, please wait...")

	go func() {
		defer func() {
			if r := recover(); r != nil {
				util.Error("DCC: Panic in handleInfoCommand: %v", r)
				dt.sendToClient(fmt.Sprintf("Error gathering system information: %v", r))
			}
			close(doneChan)
		}()

		if bm := dt.bot.GetBotManager(); bm != nil {
			info := dt.generateSystemInfo()
			dt.sendToClient(info)
		} else {
			dt.sendToClient("BotManager is not available.")
		}
	}()

	// Wait for the command to complete or timeout
	select {
	case <-doneChan:
		// Command completed normally
	case <-timeoutChan:
		// Command timed out
		dt.sendToClient("Command timed out while gathering system information. Some data may be incomplete.")
	}
}

// Funkcje pomocnicze

func (dt *DCCTunnel) generateSystemInfo() string {
	currentUser, err := user.Current()
	if err != nil {
		currentUser = &user.User{}
	}

	cwd, err := os.Getwd()
	if err != nil {
		cwd = "unknown"
	}

	serverHost := dt.bot.GetServerName()
	ips, _ := net.LookupIP(serverHost)
	var ipv4, ipv6 string
	for _, ip := range ips {
		if ip.To4() != nil {
			ipv4 = ip.String()
		} else {
			ipv6 = ip.String()
		}
	}

	externalIPv4 := dt.getExternalIP("tcp4")
	externalIPv6 := dt.getExternalIP("tcp6")

	return fmt.Sprintf(`
Bot Information:
---------------
Current Working Directory: %s
Username: %s
Home Directory: %s

Server Information:
------------------
Server Name: %s
Server IPv4: %s
Server IPv6: %s

External IP Information:
----------------------
External IPv4: %s
External IPv6: %s

Process Information:
------------------
Process ID: %d
Parent Process ID: %d`,
		cwd,
		currentUser.Username,
		currentUser.HomeDir,
		serverHost,
		ipv4,
		ipv6,
		externalIPv4,
		externalIPv6,
		os.Getpid(),
		os.Getppid())
}

// getExternalIPSemaphore is a semaphore to limit the number of concurrent getExternalIP operations
var getExternalIPSemaphore = make(chan struct{}, 2) // Allow up to 2 concurrent operations

func (dt *DCCTunnel) getExternalIP(network string) string {
	// Try to acquire the semaphore with a very short timeout
	select {
	case getExternalIPSemaphore <- struct{}{}:
		// Semaphore acquired, proceed with the request
		defer func() { <-getExternalIPSemaphore }() // Release the semaphore when done
	case <-time.After(100 * time.Millisecond):
		// Couldn't acquire the semaphore quickly, return unavailable
		return "unavailable (busy)"
	}

	// Create a channel to receive the result
	resultChan := make(chan string, 1)

	// Execute the HTTP request in a separate goroutine
	go func() {
		defer func() {
			if r := recover(); r != nil {
				util.Error("Panic in getExternalIP: %v", r)
				resultChan <- "unavailable (error)"
			}
		}()

		// Set up a client with very short timeouts
		client := &http.Client{
			Transport: &http.Transport{
				TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
				DialContext: func(ctx context.Context, _, addr string) (net.Conn, error) {
					d := net.Dialer{Timeout: 1 * time.Second}
					return d.DialContext(ctx, network, addr)
				},
			},
			Timeout: 2 * time.Second, // Very short timeout
		}

		// Try to get the IP
		resp, err := client.Get("https://ip.shr.al")
		if err != nil {
			resultChan <- "unavailable"
			return
		}
		defer resp.Body.Close()

		// Read with a timeout
		bodyBytes := make([]byte, 64) // IP addresses are short
		n, err := resp.Body.Read(bodyBytes)
		if err != nil && err != io.EOF {
			resultChan <- "unavailable"
			return
		}

		// Return the result
		resultChan <- strings.TrimSpace(string(bodyBytes[:n]))
	}()

	// Wait for the result with a very short timeout
	select {
	case result := <-resultChan:
		return result
	case <-time.After(2 * time.Second): // Very short overall timeout
		return "unavailable (timeout)"
	}
}

// Pomocnicza funkcja do generowania losowych nicków
func (dt *DCCTunnel) generateRandomNick() string {
	rand.Seed(time.Now().UnixNano())

	// Generowanie głównej części nicka (4-7 znaków)
	mainLength := rand.Intn(4) + 4
	mainPart := make([]byte, mainLength)

	for i := range mainPart {
		if rand.Intn(2) == 0 {
			mainPart[i] = byte('A' + rand.Intn(26))
		} else {
			mainPart[i] = byte('a' + rand.Intn(26))
		}
	}

	// Generowanie końcówki (1-4 znaki)
	suffixLength := rand.Intn(4) + 1
	suffixPart := make([]byte, suffixLength)

	for i := range suffixPart {
		choice := rand.Intn(3)
		if choice == 0 {
			suffixPart[i] = byte('A' + rand.Intn(26))
		} else if choice == 1 {
			suffixPart[i] = byte('a' + rand.Intn(26))
		} else {
			suffixPart[i] = byte('0' + rand.Intn(10))
		}
	}

	// Łączenie części nicka
	return fmt.Sprintf("%s%s", string(mainPart), string(suffixPart))
}

func colorCommand(command, description string) string {
	return fmt.Sprintf("%s %s", colorText(command, 9), description)
}

func (dt *DCCTunnel) sendHelpMessage() {
	helpMessage := boldText(colorText("\nAvailable commands:\n==================\n", 16)) + "\n" +
		colorText("[ Standard ] IRC commands:", 10) + "\n" +
		"--------------------\n" +
		colorCommand(".msg <target> <message>", "- Send a private message") + "\n" +
		colorCommand(".join <channel>", "- Join a channel") + "\n" +
		colorCommand(".part <channel>", "- Leave a channel") + "\n" +
		colorCommand(".mode <target> [modes] [args]", "- Change channel or user modes") + "\n" +
		colorCommand(".kick <channel> <user> [reason]", "- Kick a user") + "\n" +
		colorCommand(".quit", "- Disconnect the bot") + "\n" +
		colorCommand(".nick <newnick>", "- Change nickname") + "\n" +
		colorCommand(".raw <command>", "- Send raw IRC command") + "\n\n" +
		colorText("[ Mass ] commands (all bots on all nodes):", 10) + "\n" +
		"------------\n" +
		colorCommand(".mjoin <channel>", "- All bots join channel") + "\n" +
		colorCommand(".mpart <channel>", "- All bots leave channel") + "\n" +
		colorCommand(".mreconnect", "- Reconnect all bots (including linked bots)") + "\n\n" +
		colorText("[ Admin ] commands (For now only local node):", 10) + "\n" +
		"-------------\n" +
		colorCommand(".addnick <nick>", "- Add nick to catch list") + "\n" +
		colorCommand(".delnick <nick>", "- Remove nick from catch list") + "\n" +
		colorCommand(".listnicks", "- List nicks to catch") + "\n" +
		colorCommand(".addowner <mask>", "- Add owner mask") + "\n" +
		colorCommand(".delowner <mask>", "- Remove owner mask") + "\n" +
		colorCommand(".listowners", "- List owner masks") + "\n" +
		colorCommand(".info", "- Display detailed bot information") + "\n" +
		colorCommand(".bots", "- Show bot statistics") + "\n" +
		colorCommand(".bots n", "- Show list of connected bot nicknames") + "\n" +
		colorCommand(".servers", "- Show server connection statistics") + "\n" +
		colorCommand(".servers <nick>", "- Show server for specific bot") + "\n\n" +
		colorText("ISON monitoring:", 10) + "\n" +
		"--------------\n" +
		"Type " + boldText(".help") + " to see this message again.\n"

	dt.sendToClient(helpMessage)
}

func (dt *DCCTunnel) handleServersCommand(args []string) {
	bm := dt.bot.GetBotManager()
	if bm == nil {
		dt.sendToClient("BotManager is not available.")
		return
	}

	bots := bm.GetBots()
	connectedBots := make([]types.Bot, 0)
	for _, bot := range bots {
		if bot.IsConnected() {
			connectedBots = append(connectedBots, bot)
		}
	}

	if len(args) == 0 {
		// Bez argumentów, wyświetlamy statystyki serwerów
		serverCounts := make(map[string]int)
		for _, bot := range connectedBots {
			serverName := bot.GetServerName()
			serverCounts[serverName]++
		}

		if len(serverCounts) == 0 {
			dt.sendToClient("No bots are currently connected.")
		} else {
			var outputLines []string
			for server, count := range serverCounts {
				outputLines = append(outputLines, fmt.Sprintf("%s: %d", server, count))
			}
			dt.sendToClient("Server connections:\n" + strings.Join(outputLines, "\n"))
		}
	} else if len(args) == 1 {
		// Jeśli podano argument, traktujemy go jako nick bota
		botNick := args[0]
		var foundBot types.Bot
		for _, bot := range connectedBots {
			if strings.EqualFold(bot.GetCurrentNick(), botNick) {
				foundBot = bot
				break
			}
		}

		if foundBot != nil {
			serverName := foundBot.GetServerName()
			dt.sendToClient(fmt.Sprintf("Bot %s is connected to server: %s", botNick, serverName))
		} else {
			dt.sendToClient(fmt.Sprintf("Bot with nick '%s' is not found or not connected.", botNick))
		}
	} else {
		dt.sendToClient("Usage: .servers or .servers <bot_nick>")
	}
}
