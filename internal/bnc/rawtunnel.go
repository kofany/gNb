package bnc

import (
	"bufio"
	"fmt"
	"io"
	"strings"
	"sync"
	"time"

	"github.com/kofany/gNb/internal/irc"
	"github.com/kofany/gNb/internal/types"
	"github.com/kofany/gNb/internal/util"
)

type RawTunnel struct {
	conn          io.ReadWriteCloser
	bot           types.Bot
	active        bool
	mu            sync.Mutex
	ignoredEvents map[string]bool
	formatter     *MessageFormatter
	helpMessage   string
}

func NewRawTunnel(bot types.Bot) *RawTunnel {
	rt := &RawTunnel{
		bot:           bot,
		active:        false,
		ignoredEvents: map[string]bool{"303": true}, // Ignore ISON responses
		formatter:     &MessageFormatter{nickname: bot.GetCurrentNick()},
	}

	// Initialize the help message
	rt.helpMessage = rt.constructHelpMessage()

	return rt
}

func (rt *RawTunnel) SetIgnoredEvent(event string, ignore bool) {
	rt.mu.Lock()
	defer rt.mu.Unlock()
	rt.ignoredEvents[event] = ignore
}

func (rt *RawTunnel) Start(conn io.ReadWriteCloser) {
	rt.mu.Lock()
	defer rt.mu.Unlock()

	if rt.active {
		util.Warning("Raw tunnel already active for bot %s", rt.bot.GetCurrentNick())
		return
	}

	rt.conn = conn
	rt.active = true

	util.Debug("RawTunnel: Raw tunnel started for bot %s", rt.bot.GetCurrentNick())

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

	rt.conn.Write([]byte(welcomeMessage + "\r\n"))

	go rt.readFromConn()
}

func (rt *RawTunnel) Stop() {
	rt.mu.Lock()
	defer rt.mu.Unlock()

	if !rt.active {
		return
	}

	rt.active = false
	if rt.conn != nil {
		rt.conn.Close()
	}
}

func (rt *RawTunnel) readFromConn() {
	defer rt.Stop()

	scanner := bufio.NewScanner(rt.conn)
	for scanner.Scan() {
		line := scanner.Text()
		util.Debug("RawTunnel: Received from SSH connection: %s", line)
		rt.handleUserInput(line)
	}

	if err := scanner.Err(); err != nil {
		util.Error("RawTunnel: Error reading from SSH connection: %v", err)
	}
}

func (rt *RawTunnel) handleUserInput(input string) {
	if strings.HasPrefix(input, ".") {
		// User entered a command
		rt.processCommand(input)
	} else {
		// Regular message, send as PRIVMSG to a default target
		defaultTarget := rt.bot.GetCurrentNick() // Change this to a default channel or user if desired
		rt.bot.SendMessage(defaultTarget, input)
	}
}

func (rt *RawTunnel) processCommand(command string) {
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
			rt.bot.SendMessage(target, message)
		} else {
			rt.sendToClient("Usage: .msg <target> <message>")
		}
	case "JOIN":
		if len(fields) >= 2 {
			channel := fields[1]
			rt.bot.JoinChannel(channel)
		} else {
			rt.sendToClient("Usage: .join <channel>")
		}
	case "PART":
		if len(fields) >= 2 {
			channel := fields[1]
			rt.bot.PartChannel(channel)
		} else {
			rt.sendToClient("Usage: .part <channel>")
		}
	case "MODE":
		if len(fields) >= 3 {
			target := fields[1]
			modes := strings.Join(fields[2:], " ")
			command := fmt.Sprintf("MODE %s %s", target, modes)
			rt.bot.SendRaw(command)
		} else if len(fields) >= 2 {
			target := fields[1]
			command := fmt.Sprintf("MODE %s", target)
			rt.bot.SendRaw(command)
		} else {
			rt.sendToClient("Usage: .mode <target> [modes] [args]")
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
			rt.bot.SendRaw(command)
		} else {
			rt.sendToClient("Usage: .kick <channel> <user> [reason]")
		}
	case "QUIT":
		rt.bot.Quit("Quit via SSH")
		rt.Stop()
	case "NICK":
		if len(fields) >= 2 {
			newNick := fields[1]
			rt.bot.ChangeNick(newNick)
		} else {
			rt.sendToClient("Usage: .nick <newnick>")
		}
	case "RAW":
		if len(fields) >= 2 {
			rawCmd := strings.Join(fields[1:], " ")
			rt.bot.SendRaw(rawCmd)
		} else {
			rt.sendToClient("Usage: .raw <command>")
		}
	case "HELP":
		rt.sendHelpMessage()
	default:
		rt.sendToClient(fmt.Sprintf("Unknown command: %s", cmd))
	}
}

func (rt *RawTunnel) sendHelpMessage() {
	helpMessage := rt.helpMessage + "\r\n"
	rt.conn.Write([]byte(helpMessage))
}

func (rt *RawTunnel) constructHelpMessage() string {
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

func (rt *RawTunnel) sendToClient(message string) {
	rt.conn.Write([]byte(message + "\r\n"))
}

func (rt *RawTunnel) WriteToConn(data string) {
	rt.mu.Lock()
	defer rt.mu.Unlock()

	if !rt.active || rt.conn == nil {
		return
	}

	// Ignore certain events
	if strings.Contains(data, " 303 ") {
		return
	}

	// Parse the IRC message
	parsedMessage := rt.parseIRCMessage(data)
	if parsedMessage == "" {
		return
	}

	util.Debug("RawTunnel: Sending to SSH connection: %s", parsedMessage)
	rt.conn.Write([]byte(parsedMessage + "\r\n"))
}

func (rt *RawTunnel) parseIRCMessage(raw string) string {
	// Use your own irc package to parse the message
	event := irc.ParseIRCMessage(raw)
	if event == nil {
		util.Debug("RawTunnel: Failed to parse IRC message")
		return ""
	}

	switch event.Command {
	case "PRIVMSG":
		return rt.formatter.formatPrivMsg(event)
	case "NOTICE":
		return rt.formatter.formatNotice(event)
	case "JOIN":
		return rt.formatter.formatJoin(event)
	case "PART":
		return rt.formatter.formatPart(event)
	case "QUIT":
		return rt.formatter.formatQuit(event)
	case "NICK":
		return rt.formatter.formatNick(event)
	case "MODE":
		return rt.formatter.formatMode(event)
	case "KICK":
		return rt.formatter.formatKick(event)
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
