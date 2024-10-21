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

type DCCTunnel struct {
	conn          net.Conn
	bot           types.Bot
	active        bool
	mu            sync.Mutex
	ignoredEvents map[string]bool
	onStop        func()
	formatter     *MessageFormatter
	helpMessage   string
}

func NewDCCTunnel(bot types.Bot, onStop func()) *DCCTunnel {
	dt := &DCCTunnel{
		bot:           bot,
		active:        false,
		ignoredEvents: map[string]bool{"303": true}, // Ignore ISON responses
		onStop:        onStop,
		formatter:     &MessageFormatter{nickname: bot.GetCurrentNick()},
	}

	// Initialize the help message
	dt.helpMessage = dt.constructHelpMessage()

	return dt
}

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

	welcomeMessage := `
                 ___      __             __ <<<<<<[get Nick bot]
    ____  ____  [ m ]__  / /_  __  __   / /____  ____ _____ ___
   / __ \/ __ \  / / _ \/ __ \/ / / /  / __/ _ \/ __ ` + "`" + `/ __ ` + "`" + `__ \
  / /_/ / /_/ / / /  __/ /_/ / /_/ /  / /_/  __/ /_/ / / / / / /
 / .___/\____/_/ /\___/_.___/\__, /blo\__/\___/\__,_/_/ /_/ /_/
/_/  ruciu  /___/   dominik /____/                     kofany

Type your IRC commands here using '.' as the prefix.

Type .help to see available commands.
`
	dt.conn.Write([]byte(welcomeMessage + "\r\n"))

	go dt.readFromConn()
}

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

	// Call the onStop callback
	if dt.onStop != nil {
		dt.onStop()
	}
}

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

func (dt *DCCTunnel) handleUserInput(input string) {
	if strings.HasPrefix(input, ".") {
		// User entered a command
		dt.processCommand(input)
	} else {
		// Regular message, send as PRIVMSG to a default target
		defaultTarget := dt.bot.GetCurrentNick() // Change this to a default channel or user if desired
		dt.bot.SendMessage(defaultTarget, input)
	}
}

func (dt *DCCTunnel) processCommand(command string) {
	fields := strings.Fields(command)
	if len(fields) == 0 {
		return
	}

	// Remove the '.' prefix and convert to upper case for command matching
	cmd := strings.ToUpper(strings.TrimPrefix(fields[0], "."))

	switch cmd {
	case "MSG":
		if len(fields) >= 3 {
			target := fields[1]
			message := strings.Join(fields[2:], " ")
			dt.bot.SendMessage(target, message)
		} else {
			dt.sendToClient("Usage: .msg <target> <message>")
		}
	case "JOIN":
		if len(fields) >= 2 {
			channel := fields[1]
			dt.bot.JoinChannel(channel)
		} else {
			dt.sendToClient("Usage: .join <channel>")
		}
	case "PART":
		if len(fields) >= 2 {
			channel := fields[1]
			dt.bot.PartChannel(channel)
		} else {
			dt.sendToClient("Usage: .part <channel>")
		}
	case "MODE":
		if len(fields) >= 3 {
			target := fields[1]
			modes := strings.Join(fields[2:], " ")
			command := fmt.Sprintf("MODE %s %s", target, modes)
			dt.bot.SendRaw(command)
		} else if len(fields) >= 2 {
			target := fields[1]
			command := fmt.Sprintf("MODE %s", target)
			dt.bot.SendRaw(command)
		} else {
			dt.sendToClient("Usage: .mode <target> [modes] [args]")
		}
	case "KICK":
		if len(fields) >= 3 {
			channel := fields[1]
			user := fields[2]
			reason := ""
			if len(fields) > 3 {
				reason = strings.Join(fields[3:], " ")
			}
			command := fmt.Sprintf("KICK %s %s :%s", channel, user, reason)
			dt.bot.SendRaw(command)
		} else {
			dt.sendToClient("Usage: .kick <channel> <user> [reason]")
		}
	case "QUIT":
		dt.bot.Quit("Quit via DCC")
		dt.Stop()
	case "NICK":
		if len(fields) >= 2 {
			newNick := fields[1]
			dt.bot.ChangeNick(newNick)
		} else {
			dt.sendToClient("Usage: .nick <newnick>")
		}
	case "RAW":
		if len(fields) >= 2 {
			rawCmd := strings.Join(fields[1:], " ")
			dt.bot.SendRaw(rawCmd)
		} else {
			dt.sendToClient("Usage: .raw <command>")
		}
	case "HELP":
		dt.sendHelpMessage()
	default:
		dt.sendToClient(fmt.Sprintf("Unknown command: %s", cmd))
	}
}

func (dt *DCCTunnel) sendHelpMessage() {
	helpMessage := dt.helpMessage + "\r\n"
	dt.conn.Write([]byte(helpMessage))
}

func (dt *DCCTunnel) constructHelpMessage() string {
	return `
Available commands (SSH/DCC only):
.msg <target> <message>       - Send a private message to a user or channel
.join <channel>               - Join a channel
.part <channel>               - Leave a channel
.mode <target> [modes] [args] - Change channel or user modes
.kick <channel> <user> [reason] - Kick a user from a channel
.quit                         - Disconnect the bot and close the SSH session
.nick <newnick>               - Change the bot's nickname
.raw <command>                - Send a raw IRC command
.help                         - Display this help message

Available commands (IRC only):
.quit                         - Quit the bot
.say <target> <message>       - Make the bot say a message
.reconnect                    - Reconnect the bot
.addnick <nick>               - Add a nick to the bot
.delnick <nick>               - Remove a nick from the bot
.listnicks                    - List the bot's nicks
.addowner <mask>              - Add an owner mask
.delowner <mask>              - Remove an owner mask
.listowners                   - List owner masks
.bnc <start|stop>             - Start or stop the BNC

Type your messages without a prefix to send a message to the default target.

Enjoy your session!
`
}

func (dt *DCCTunnel) sendToClient(message string) {
	dt.conn.Write([]byte(message + "\r\n"))
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
		// Handle other message types as needed
		return ""
	}
}

// MessageFormatter formats IRC messages into a user-friendly format
type MessageFormatter struct {
	nickname string
}

func (mf *MessageFormatter) formatPrivMsg(event *irc.Event) string {
	timestamp := time.Now().Format("15:04")
	sender := event.Nick
	target := ""
	if len(event.Args) > 0 {
		target = event.Args[0]
	}
	message := event.Message

	if target == mf.nickname {
		// Private message
		return fmt.Sprintf("[%s] <%s> %s", timestamp, sender, message)
	} else {
		// Channel message
		return fmt.Sprintf("[%s] <%s:%s> %s", timestamp, target, sender, message)
	}
}

func (mf *MessageFormatter) formatNotice(event *irc.Event) string {
	timestamp := time.Now().Format("15:04")
	sender := event.Nick
	message := event.Message
	return fmt.Sprintf("[%s] -%s- %s", timestamp, sender, message)
}

func (mf *MessageFormatter) formatJoin(event *irc.Event) string {
	timestamp := time.Now().Format("15:04")
	nick := event.Nick
	channel := ""
	if len(event.Args) > 0 {
		channel = event.Args[0]
	}
	return fmt.Sprintf("[%s] *** %s has joined %s", timestamp, nick, channel)
}

func (mf *MessageFormatter) formatPart(event *irc.Event) string {
	timestamp := time.Now().Format("15:04")
	nick := event.Nick
	channel := ""
	if len(event.Args) > 0 {
		channel = event.Args[0]
	}
	return fmt.Sprintf("[%s] *** %s has left %s", timestamp, nick, channel)
}

func (mf *MessageFormatter) formatQuit(event *irc.Event) string {
	timestamp := time.Now().Format("15:04")
	nick := event.Nick
	reason := event.Message
	return fmt.Sprintf("[%s] *** %s has quit (%s)", timestamp, nick, reason)
}

func (mf *MessageFormatter) formatNick(event *irc.Event) string {
	timestamp := time.Now().Format("15:04")
	oldNick := event.Nick
	newNick := event.Message
	return fmt.Sprintf("[%s] *** %s is now known as %s", timestamp, oldNick, newNick)
}

func (mf *MessageFormatter) formatMode(event *irc.Event) string {
	timestamp := time.Now().Format("15:04")
	nick := event.Nick
	target := ""
	modes := ""
	if len(event.Args) >= 2 {
		target = event.Args[0]
		modes = strings.Join(event.Args[1:], " ")
	}
	return fmt.Sprintf("[%s] *** %s sets mode %s on %s", timestamp, nick, modes, target)
}

func (mf *MessageFormatter) formatKick(event *irc.Event) string {
	timestamp := time.Now().Format("15:04")
	nick := event.Nick
	channel := ""
	user := ""
	reason := event.Message
	if len(event.Args) >= 2 {
		channel = event.Args[0]
		user = event.Args[1]
	}
	return fmt.Sprintf("[%s] *** %s has kicked %s from %s (%s)", timestamp, nick, user, channel, reason)
}
