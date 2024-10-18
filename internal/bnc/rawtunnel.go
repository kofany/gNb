package bnc

import (
	"bufio"
	"fmt"
	"io"
	"strings"
	"sync"
	"time"

	"github.com/kofany/gNb/internal/types"
	"github.com/kofany/gNb/internal/util"
)

type RawTunnel struct {
	conn          io.ReadWriteCloser
	bot           types.Bot
	active        bool
	mu            sync.Mutex
	ignoredEvents map[string]bool
}

func NewRawTunnel(bot types.Bot) *RawTunnel {
	return &RawTunnel{
		bot:           bot,
		active:        false,
		ignoredEvents: map[string]bool{"303": true}, // Domyślnie ignoruj 303
	}
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

	welcomeMessage := `
Welcome to the BNC SSH connection!

Command usage:
1. To send raw IRC commands, simply type them as usual.
2. To send a message to a channel or user, use the following format:
   .<target> <message>

   Examples:
   .#channel Hello, everyone!
   .nickname Hi there, how are you?

Enjoy your session!

`
	rt.conn.Write([]byte(welcomeMessage))

	time.Sleep(100 * time.Millisecond)

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
		command := scanner.Text()

		if strings.HasPrefix(command, ".") {
			parts := strings.SplitN(command[1:], " ", 2)
			if len(parts) == 2 {
				target := parts[0]
				message := parts[1]
				command = fmt.Sprintf("PRIVMSG %s :%s", target, message)
			}
		}

		rt.bot.SendRaw(command)

		// Format and display the outgoing command
		formattedMessage := rt.formatOutgoingMessage(command)
		if formattedMessage != "" {
			rt.conn.Write([]byte(formattedMessage + "\r\n"))
		}
	}

	if err := scanner.Err(); err != nil {
		util.Error("Error reading from BNC connection: %v", err)
	}
}

func (rt *RawTunnel) WriteToConn(data string) {
	rt.mu.Lock()
	defer rt.mu.Unlock()

	if !rt.active || rt.conn == nil {
		return
	}

	// Ignore 303 (ISON) events completely
	if strings.Contains(data, " 303 ") {
		return
	}

	formattedMessage := rt.formatIncomingMessage(data)
	if formattedMessage != "" {
		_, err := rt.conn.Write([]byte(formattedMessage + "\r\n"))
		if err != nil {
			util.Error("Error writing formatted message to BNC connection: %v", err)
		}
	}
}

func (rt *RawTunnel) formatOutgoingMessage(raw string) string {
	parts := strings.SplitN(raw, " ", 3)
	if len(parts) < 2 {
		return ""
	}

	command := parts[0]
	switch command {
	case "PRIVMSG":
		if len(parts) < 3 {
			return ""
		}
		channel := parts[1]
		message := strings.TrimPrefix(parts[2], ":")
		return fmt.Sprintf("%s | <%s> %s", channel, rt.bot.GetCurrentNick(), message)
	case "JOIN":
		return fmt.Sprintf("Join %s %s", parts[1], rt.bot.GetCurrentNick())
	case "PART":
		if len(parts) > 2 {
			return fmt.Sprintf("Part %s %s (%s)", parts[1], rt.bot.GetCurrentNick(), parts[2])
		}
		return fmt.Sprintf("Part %s %s", parts[1], rt.bot.GetCurrentNick())
	case "QUIT":
		if len(parts) > 1 {
			return fmt.Sprintf("Quit %s (%s)", rt.bot.GetCurrentNick(), parts[1])
		}
		return fmt.Sprintf("Quit %s", rt.bot.GetCurrentNick())
	case "NICK":
		return fmt.Sprintf("%s changed nick to %s", rt.bot.GetCurrentNick(), parts[1])
	default:
		return ""
	}
}

func (rt *RawTunnel) formatIncomingMessage(raw string) string {
	if !strings.HasPrefix(raw, ":") {
		return ""
	}

	parts := strings.SplitN(raw, " ", 4)
	if len(parts) < 2 {
		return ""
	}

	prefix := parts[0][1:] // Remove leading ':'
	command := parts[1]

	switch command {
	case "PRIVMSG":
		if len(parts) < 4 {
			return ""
		}
		channel := parts[2]
		message := strings.TrimPrefix(parts[3], ":")
		return fmt.Sprintf("%s | <%s> %s", channel, rt.getNickFromPrefix(prefix), message)
	case "MODE":
		if len(parts) < 4 {
			return ""
		}
		channel := parts[2]
		modeChanges := strings.Join(parts[3:], " ")
		return fmt.Sprintf("Mode %s %s by %s", channel, modeChanges, rt.getNickFromPrefix(prefix))
	case "JOIN":
		if len(parts) < 3 {
			return ""
		}
		channel := strings.TrimPrefix(parts[2], ":")
		return fmt.Sprintf("Join %s %s", channel, rt.getNickFromPrefix(prefix))
	case "PART":
		if len(parts) < 3 {
			return ""
		}
		channel := parts[2]
		message := ""
		if len(parts) > 3 {
			message = " (" + strings.TrimPrefix(parts[3], ":") + ")"
		}
		return fmt.Sprintf("Part %s %s%s", channel, rt.getNickFromPrefix(prefix), message)
	case "QUIT":
		message := ""
		if len(parts) > 2 {
			message = " (" + strings.TrimPrefix(parts[2], ":") + ")"
		}
		return fmt.Sprintf("Quit %s%s", rt.getNickFromPrefix(prefix), message)
	case "NICK":
		if len(parts) < 3 {
			return ""
		}
		oldNick := rt.getNickFromPrefix(prefix)
		newNick := strings.TrimPrefix(parts[2], ":")
		return fmt.Sprintf("%s changed nick to %s", oldNick, newNick)
	default:
		return ""
	}
}

func (rt *RawTunnel) getNickFromPrefix(prefix string) string {
	if strings.Contains(prefix, "!") {
		return strings.SplitN(prefix, "!", 2)[0]
	}
	return prefix
}
