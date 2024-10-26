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
	"strconv"
	"strings"
	"time"

	"github.com/kofany/gNb/internal/types"
	"github.com/kofany/gNb/internal/util"
)

// processCommand przetwarza komendy od użytkownika
func (dt *DCCTunnel) processCommand(command string) {
	fields := strings.Fields(command)
	if len(fields) == 0 {
		return
	}

	// Usuń prefiks '.' i przekonwertuj na wielkie litery
	cmd := strings.ToUpper(strings.TrimPrefix(fields[0], "."))

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
	default:
		dt.sendToClient(fmt.Sprintf("Unknown command: %s", cmd))
	}
}

// Handlery podstawowych komend

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

func (dt *DCCTunnel) handleCfloodCommand(args []string) {
	if len(args) < 3 {
		dt.sendToClient("Usage: .cflo <#channel> <loops> <message>")
		return
	}

	channel := args[0]
	loops, err := strconv.Atoi(args[1])
	if err != nil || loops <= 0 {
		dt.sendToClient("Number of loops must be a positive integer.")
		return
	}

	maxLoops := 100
	if loops > maxLoops {
		dt.sendToClient(fmt.Sprintf("Maximum number of loops is %d.", maxLoops))
		return
	}

	message := strings.Join(args[2:], " ")
	dt.sendToClient(fmt.Sprintf("Starting flood test with %d loops.", loops))

	if bm := dt.bot.GetBotManager(); bm != nil {
		for _, bot := range bm.GetBots() {
			go dt.executeCflood(bot, channel, loops, message)
		}
	}
}

func (dt *DCCTunnel) handleNfloodCommand(args []string) {
	if len(args) < 3 {
		dt.sendToClient("Usage: .nflo <nick> <loops> <message>")
		return
	}

	targetNick := args[0]
	loops, err := strconv.Atoi(args[1])
	if err != nil || loops <= 0 {
		dt.sendToClient("Number of loops must be a positive integer.")
		return
	}

	maxLoops := 100
	if loops > maxLoops {
		dt.sendToClient(fmt.Sprintf("Maximum number of loops is %d.", maxLoops))
		return
	}

	message := strings.Join(args[2:], " ")
	dt.sendToClient(fmt.Sprintf("Starting flood test with %d loops.", loops))

	if bm := dt.bot.GetBotManager(); bm != nil {
		for _, bot := range bm.GetBots() {
			go dt.executeNflood(bot, targetNick, loops, message)
		}
	}
}

func (dt *DCCTunnel) handleInfoCommand(_ []string) {
	if bm := dt.bot.GetBotManager(); bm != nil {
		info := dt.generateSystemInfo()
		dt.sendToClient(info)
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

// Implementacje funkcji flood

func (dt *DCCTunnel) executeCflood(bot types.Bot, channel string, loops int, message string) {
	// Dołączanie do kanału
	joinCmd := fmt.Sprintf("JOIN %s", channel)
	bot.SendRaw(joinCmd)
	time.Sleep(500 * time.Millisecond)

	for i := 0; i < loops; i++ {
		// Wysyłanie wiadomości
		privmsgCmd := fmt.Sprintf("PRIVMSG %s :%s", channel, message)
		bot.SendRaw(privmsgCmd)
		time.Sleep(1 * time.Second)

		// Zmiana nicku
		newNick := dt.generateRandomNick()
		nickCmd := fmt.Sprintf("NICK %s", newNick)
		bot.SendRaw(nickCmd)
		time.Sleep(1 * time.Second)

		// Ponowne wysyłanie wiadomości
		bot.SendRaw(privmsgCmd)
		time.Sleep(1 * time.Second)

		// Ponowna zmiana nicku
		newNick = dt.generateRandomNick()
		bot.SendRaw(nickCmd)
	}

	// Wyjście z kanału
	partCmd := fmt.Sprintf("PART %s", channel)
	bot.SendRaw(partCmd)
	time.Sleep(90 * time.Second)

	// Końcowa zmiana nicku
	newNick := dt.generateRandomNick()
	util.Info("==================>>> Executing final nick change to ------>>> %s", newNick)
	bot.ChangeNick(newNick)
	time.Sleep(3 * time.Second)
}

func (dt *DCCTunnel) executeNflood(bot types.Bot, targetNick string, loops int, message string) {
	for i := 0; i < loops; i++ {
		// Zmiana nicku
		newNick := dt.generateRandomNick()
		nickCmd := fmt.Sprintf("NICK %s", newNick)
		bot.SendRaw(nickCmd)
		time.Sleep(1 * time.Second)

		// Wysyłanie wiadomości prywatnej
		privmsgCmd := fmt.Sprintf("PRIVMSG %s :%s", targetNick, message)
		bot.SendRaw(privmsgCmd)
	}

	// Końcowa zmiana nicku po zakończeniu flood
	time.Sleep(10 * time.Second)
	newNick := dt.generateRandomNick()
	bot.ChangeNick(newNick)
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

func (dt *DCCTunnel) sendHelpMessage() {
	helpMessage := `
 Available commands:
 ==================
 
 Standard IRC commands:
 --------------------
 .msg <target> <message>       - Send a private message
 .join <channel>               - Join a channel
 .part <channel>               - Leave a channel
 .mode <target> [modes] [args] - Change channel or user modes
 .kick <channel> <user> [reason] - Kick a user
 .quit                         - Disconnect the bot
 .nick <newnick>              - Change nickname
 .raw <command>               - Send raw IRC command
 
 Mass commands:
 ------------
 .mjoin <channel>             - All bots join channel
 .mpart <channel>             - All bots leave channel
 .mreconnect                  - Reconnect all bots
 
 Admin commands:
 -------------
 .addnick <nick>              - Add nick to catch list
 .delnick <nick>              - Remove nick from catch list
 .listnicks                   - List nicks to catch
 .addowner <mask>             - Add owner mask
 .delowner <mask>             - Remove owner mask
 .listowners                  - List owner masks
 .info                        - Display detailed bot information
 
 Flood test commands:
 -----------------
 .cflo <#channel> <loops> <message> - Channel flood test
 .nflo <nick> <loops> <message>     - Nick flood test
 
 Type .help to see this message again.
 `
	dt.sendToClient(helpMessage)
}
