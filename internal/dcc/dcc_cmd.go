package dcc

import (
	"context"
	"crypto/tls"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/user"
	"strings"
	"time"

	"github.com/kofany/gNb/internal/types"
	"github.com/kofany/gNb/internal/util"
)

// processCommand przetwarza komendy od użytkownika
func (dt *DCCTunnel) processCommand(command string) {
	util.Debug("DCC: Processing command: %s for session: %s on bot %s", command, dt.sessionID, dt.bot.GetCurrentNick())

	fields := strings.Fields(command)
	if len(fields) == 0 {
		util.Warning("DCC: Empty command received from session: %s", dt.sessionID)
		return
	}

	// Usuń prefiks '.' i przekonwertuj na wielkie litery
	cmd := strings.ToUpper(strings.TrimPrefix(fields[0], "."))
	util.Debug("DCC: Command parsed: %s with %d arguments for bot %s", cmd, len(fields)-1, dt.bot.GetCurrentNick())

	switch cmd {
	case "MSG":
		dt.handleMsgCommand(fields[1:])
	case "JOIN":
		dt.handleJoinCommand(fields[1:])
	case "PART":
		dt.handlePartCommand(fields[1:])
	case "MODE":
		dt.handleModeCommand(fields[1:])
	case "KICK":
		dt.handleKickCommand(fields[1:])
	case "QUIT":
		dt.handleQuitCommand(fields[1:])
	case "NICK":
		dt.handleNickCommand(fields[1:])
	case "RAW":
		dt.handleRawCommand(fields[1:])
	case "HELP":
		dt.sendHelpMessage()
	case "MJOIN":
		dt.handleMassJoinCommand(fields[1:])
	case "MPART":
		dt.handleMassPartCommand(fields[1:])
	case "MRECONNECT":
		dt.handleMassReconnectCommand(fields[1:])
	case "ADDNICK":
		dt.handleAddNickCommand(fields[1:])
	case "DELNICK":
		dt.handleDelNickCommand(fields[1:])
	case "LISTNICKS":
		dt.handleListNicksCommand(fields[1:])
	case "ADDOWNER":
		dt.handleAddOwnerCommand(fields[1:])
	case "DELOWNER":
		dt.handleDelOwnerCommand(fields[1:])
	case "LISTOWNERS":
		dt.handleListOwnersCommand(fields[1:])
	case "INFO":
		dt.handleInfoCommand(fields[1:])
	case "BOTS":
		dt.handleBotsCommand(fields[1:])
	case "SERVERS":
		dt.handleServersCommand(fields[1:])
	default:
		dt.sendToClient(fmt.Sprintf("Unknown command: %s", cmd))
	}
}

// Handlery podstawowych komend

func (dt *DCCTunnel) handleBotsCommand(args []string) {
	bm := dt.bot.GetBotManager()
	if bm == nil {
		dt.sendToClient("BotManager is not available.")
		return
	}

	bots := bm.GetBots()
	totalCreatedBots := bm.GetTotalCreatedBots() // Dodamy tę metodę w BotManager
	totalBotsNow := len(bots)

	// Liczymy w pełni połączone boty
	totalConnectedBots := 0
	var connectedBotNicks []string
	for _, bot := range bots {
		if bot.IsConnected() {
			totalConnectedBots++
			connectedBotNicks = append(connectedBotNicks, bot.GetCurrentNick())
		}
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
	util.Debug("DCC: Executing .listnicks command for bot %s", dt.bot.GetCurrentNick())

	// Pobieramy BotManager
	bm := dt.bot.GetBotManager()
	if bm == nil {
		util.Warning("DCC: BotManager is nil for bot %s", dt.bot.GetCurrentNick())
		dt.sendToClient("Error: BotManager not available")
		return
	}

	// Pobieramy NickManager
	nm := dt.bot.GetNickManager()
	if nm == nil {
		util.Warning("DCC: NickManager is nil for bot %s", dt.bot.GetCurrentNick())
		dt.sendToClient("Error: NickManager not available")
		return
	}

	// Pobieramy listę nicków w osobnej goroutine, aby uniknąć blokowania
	go func() {
		util.Debug("DCC: Getting nicks list for bot %s", dt.bot.GetCurrentNick())
		nicks := nm.GetNicks()
		util.Debug("DCC: Got %d nicks for bot %s", len(nicks), dt.bot.GetCurrentNick())

		// Wysyłamy odpowiedź do klienta
		response := fmt.Sprintf("Current nicks: %s", strings.Join(nicks, ", "))
		dt.sendToClient(response)
	}()
}

func (dt *DCCTunnel) handleAddOwnerCommand(args []string) {
	util.Debug("DCC: Executing .addowner command for bot %s", dt.bot.GetCurrentNick())

	if len(args) < 1 {
		dt.sendToClient("Usage: .addowner <mask>")
		return
	}

	ownerMask := args[0]
	util.Debug("DCC: Adding owner %s for bot %s", ownerMask, dt.bot.GetCurrentNick())

	// Pobieramy BotManager
	bm := dt.bot.GetBotManager()
	if bm == nil {
		util.Warning("DCC: BotManager is nil for bot %s", dt.bot.GetCurrentNick())
		dt.sendToClient("Error: BotManager not available")
		return
	}

	// Dodajemy właściciela w osobnej goroutine, aby uniknąć blokowania
	go func() {
		util.Debug("DCC: Adding owner %s via BotManager for bot %s", ownerMask, dt.bot.GetCurrentNick())

		bm.CollectReactions(
			dt.bot.GetCurrentNick(),
			fmt.Sprintf("Owner '%s' has been added.", ownerMask),
			func() error {
				err := bm.AddOwner(ownerMask)
				if err != nil {
					util.Warning("DCC: Error adding owner %s: %v", ownerMask, err)
					dt.sendToClient(fmt.Sprintf("Error adding owner: %v", err))
				}
				return err
			},
		)
	}()
}

func (dt *DCCTunnel) handleDelOwnerCommand(args []string) {
	util.Debug("DCC: Executing .delowner command for bot %s", dt.bot.GetCurrentNick())

	if len(args) < 1 {
		dt.sendToClient("Usage: .delowner <mask>")
		return
	}

	ownerMask := args[0]
	util.Debug("DCC: Removing owner %s for bot %s", ownerMask, dt.bot.GetCurrentNick())

	// Pobieramy BotManager
	bm := dt.bot.GetBotManager()
	if bm == nil {
		util.Warning("DCC: BotManager is nil for bot %s", dt.bot.GetCurrentNick())
		dt.sendToClient("Error: BotManager not available")
		return
	}

	// Usuwamy właściciela w osobnej goroutine, aby uniknąć blokowania
	go func() {
		util.Debug("DCC: Removing owner %s via BotManager for bot %s", ownerMask, dt.bot.GetCurrentNick())

		bm.CollectReactions(
			dt.bot.GetCurrentNick(),
			fmt.Sprintf("Owner '%s' has been removed.", ownerMask),
			func() error {
				err := bm.RemoveOwner(ownerMask)
				if err != nil {
					util.Warning("DCC: Error removing owner %s: %v", ownerMask, err)
					dt.sendToClient(fmt.Sprintf("Error removing owner: %v", err))
				}
				return err
			},
		)
	}()
}

func (dt *DCCTunnel) handleListOwnersCommand(_ []string) {
	util.Debug("DCC: Executing .listowners command for bot %s", dt.bot.GetCurrentNick())

	// Pobieramy BotManager
	bm := dt.bot.GetBotManager()
	if bm == nil {
		util.Warning("DCC: BotManager is nil for bot %s", dt.bot.GetCurrentNick())
		dt.sendToClient("Error: BotManager not available")
		return
	}

	// Pobieramy listę właścicieli w osobnej goroutine, aby uniknąć blokowania
	go func() {
		util.Debug("DCC: Getting owners list for bot %s", dt.bot.GetCurrentNick())
		owners := bm.GetOwners()
		util.Debug("DCC: Got %d owners for bot %s", len(owners), dt.bot.GetCurrentNick())

		// Wysyłamy odpowiedź do klienta
		response := fmt.Sprintf("Current owners: %s", strings.Join(owners, ", "))
		dt.sendToClient(response)
	}()
}

func (dt *DCCTunnel) handleInfoCommand(_ []string) {
	util.Debug("DCC: Executing .info command for bot %s", dt.bot.GetCurrentNick())

	// Pobieramy BotManager
	bm := dt.bot.GetBotManager()
	if bm == nil {
		util.Warning("DCC: BotManager is nil for bot %s", dt.bot.GetCurrentNick())
		dt.sendToClient("Error: BotManager not available")
		return
	}

	// Generujemy informacje w osobnej goroutine, aby uniknąć blokowania
	go func() {
		util.Debug("DCC: Generating system info for bot %s", dt.bot.GetCurrentNick())
		info := dt.generateSystemInfo()
		util.Debug("DCC: Generated system info for bot %s", dt.bot.GetCurrentNick())

		// Wysyłamy odpowiedź do klienta
		dt.sendToClient(info)
	}()
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

func (dt *DCCTunnel) getExternalIP(network string) string {
	client := &http.Client{
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
			DialContext: func(ctx context.Context, _, addr string) (net.Conn, error) {
				d := net.Dialer{Timeout: 5 * time.Second}
				return d.DialContext(ctx, network, addr)
			},
		},
		Timeout: 5 * time.Second,
	}

	resp, err := client.Get("https://ip.shr.al")
	if err != nil {
		return "unavailable"
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "unavailable"
	}

	return strings.TrimSpace(string(body))
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
