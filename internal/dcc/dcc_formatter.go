package dcc

import (
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/kofany/gNb/internal/irc"
)

// MessageFormatter formats IRC messages to a friendly format
type MessageFormatter struct {
	nickname     string
	timeFormat   string
	colorEnabled bool
	prefixes     map[string]string
}

// NewMessageFormatter creates a new instance of message formatter
func NewMessageFormatter(nickname string) *MessageFormatter {
	return &MessageFormatter{
		nickname:     nickname,
		timeFormat:   "15:04:05",
		colorEnabled: true,
		prefixes: map[string]string{
			"PRIVMSG": colorText("<*>", 9),  // Jasnozielony
			"NOTICE":  colorText("-*-", 13), // Różowy
			"JOIN":    colorText(">>>", 10), // Turkusowy
			"PART":    colorText("<<<", 12), // Niebieski
			"QUIT":    colorText("---", 5),  // Brązowy
			"NICK":    colorText("***", 6),  // Fioletowy
			"MODE":    colorText("***", 3),  // Zielony
			"KICK":    colorText("<!>", 4),  // Czerwony
		},
	}
}

// Helper functions for coloring and styling text
func colorText(text string, colorCode int) string {
	return fmt.Sprintf("\x03%02d%s\x03", colorCode, text)
}

func boldText(text string) string {
	return fmt.Sprintf("\x02%s\x02", text)
}

// formatPrivMsg formats private messages
func (mf *MessageFormatter) formatPrivMsg(event *irc.Event) string {
	timestamp := colorText(time.Now().Format(mf.timeFormat), 14)
	sender := mf.formatNickname(event.Nick)
	target := ""
	if len(event.Args) > 0 {
		target = event.Args[0]
	}
	message := mf.formatText(event.Message)

	if target == mf.nickname {
		// Private message
		return fmt.Sprintf("[%s] %s %s",
			timestamp,
			sender,
			message)
	} else {
		// Channel message
		channel := colorText(target, 13)
		return fmt.Sprintf("[%s] %s %s: %s",
			timestamp,
			channel,
			sender,
			message)
	}
}

// formatNotice formats notices
func (mf *MessageFormatter) formatNotice(event *irc.Event) string {
	timestamp := colorText(time.Now().Format(mf.timeFormat), 14)
	sender := mf.formatNickname(event.Nick)
	message := mf.formatText(event.Message)

	return fmt.Sprintf("[%s] %s %s %s",
		timestamp,
		mf.prefixes["NOTICE"],
		sender,
		message)
}

// formatJoin formats join messages
func (mf *MessageFormatter) formatJoin(event *irc.Event) string {
	timestamp := colorText(time.Now().Format(mf.timeFormat), 14)
	nick := mf.formatNickname(event.Nick)
	channel := ""
	if len(event.Args) > 0 {
		channel = colorText(event.Args[0], 13)
	}

	return fmt.Sprintf("[%s] %s %s has joined %s",
		timestamp,
		mf.prefixes["JOIN"],
		nick,
		channel)
}

// formatPart formats part messages
func (mf *MessageFormatter) formatPart(event *irc.Event) string {
	timestamp := colorText(time.Now().Format(mf.timeFormat), 14)
	nick := mf.formatNickname(event.Nick)
	channel := ""
	if len(event.Args) > 0 {
		channel = colorText(event.Args[0], 13)
	}

	message := event.Message
	if message != "" {
		return fmt.Sprintf("[%s] %s %s has left %s (%s)",
			timestamp,
			mf.prefixes["PART"],
			nick,
			channel,
			mf.formatText(message))
	}

	return fmt.Sprintf("[%s] %s %s has left %s",
		timestamp,
		mf.prefixes["PART"],
		nick,
		channel)
}

// formatQuit formats quit messages
func (mf *MessageFormatter) formatQuit(event *irc.Event) string {
	timestamp := colorText(time.Now().Format(mf.timeFormat), 14)
	nick := mf.formatNickname(event.Nick)
	reason := mf.formatText(event.Message)

	return fmt.Sprintf("[%s] %s %s has quit (%s)",
		timestamp,
		mf.prefixes["QUIT"],
		nick,
		reason)
}

// formatNick formats nick change messages
func (mf *MessageFormatter) formatNick(event *irc.Event) string {
	timestamp := colorText(time.Now().Format(mf.timeFormat), 14)
	oldNick := mf.formatNickname(event.Nick)
	newNick := mf.formatNickname(event.Message)

	return fmt.Sprintf("[%s] %s %s is now known as %s",
		timestamp,
		mf.prefixes["NICK"],
		oldNick,
		newNick)
}

// formatMode formats mode changes
func (mf *MessageFormatter) formatMode(event *irc.Event) string {
	timestamp := colorText(time.Now().Format(mf.timeFormat), 14)
	nick := mf.formatNickname(event.Nick)
	target := ""
	modes := ""
	if len(event.Args) >= 2 {
		target = colorText(event.Args[0], 13)
		modes = colorText(strings.Join(event.Args[1:], " "), 3) // Zielony
	}

	return fmt.Sprintf("[%s] %s %s sets mode %s on %s",
		timestamp,
		mf.prefixes["MODE"],
		nick,
		modes,
		target)
}

// formatKick formats kick messages
func (mf *MessageFormatter) formatKick(event *irc.Event) string {
	timestamp := colorText(time.Now().Format(mf.timeFormat), 14)
	nick := mf.formatNickname(event.Nick)
	channel := ""
	user := ""
	reason := mf.formatText(event.Message)

	if len(event.Args) >= 2 {
		channel = colorText(event.Args[0], 13)
		user = mf.formatNickname(event.Args[1])
	}

	return fmt.Sprintf("[%s] %s %s has kicked %s from %s (%s)",
		timestamp,
		mf.prefixes["KICK"],
		nick,
		user,
		channel,
		reason)
}

// formatOther formats other types of messages
func (mf *MessageFormatter) formatOther(event *irc.Event) string {
	timestamp := colorText(time.Now().Format(mf.timeFormat), 14)
	cmd := colorText(event.Command, 6) // Fioletowy
	nick := mf.formatNickname(event.Nick)

	// Basic info
	info := fmt.Sprintf("[%s] %s %s", timestamp, cmd, nick)

	// Add arguments if any
	if len(event.Args) > 0 {
		args := colorText(strings.Join(event.Args, " "), 14)
		info += " " + args
	}

	// Add message if any
	if event.Message != "" {
		message := mf.formatText(event.Message)
		info += " :" + message
	}

	return info
}

// Helper functions for formatting

// formatNickname formats user's nickname
func (mf *MessageFormatter) formatNickname(nick string) string {
	if !mf.colorEnabled {
		return nick
	}

	if nick == mf.nickname {
		return boldText(colorText(nick, 10)) // Turkusowy i pogrubiony
	}
	return colorText(nick, 11) // Jasnoniebieski
}

// formatText formats message text
func (mf *MessageFormatter) formatText(text string) string {
	if !mf.colorEnabled {
		return text
	}
	return colorText(mf.processIRCFormatting(text), 14) // Szary
}

// processIRCFormatting processes IRC formatting codes
func (mf *MessageFormatter) processIRCFormatting(text string) string {
	if !mf.colorEnabled {
		// If colors are disabled, strip all formatting codes
		return stripIRCFormatting(text)
	}
	return text // Keep original formatting
}

// stripIRCFormatting strips IRC formatting codes
func stripIRCFormatting(text string) string {
	// Remove color codes (including parameters)
	colorRegex := regexp.MustCompile(`\x03\d{1,2}(?:,\d{1,2})?`)
	text = colorRegex.ReplaceAllString(text, "")

	// Remove other formatting codes
	formatCodes := []string{
		"\x02", // Bold
		"\x1D", // Italic
		"\x1F", // Underline
		"\x0F", // Reset formatting
		"\x16", // Reverse
	}

	for _, code := range formatCodes {
		text = strings.ReplaceAll(text, code, "")
	}

	return text
}

// Setters and getters

// SetColorEnabled enables or disables color output
func (mf *MessageFormatter) SetColorEnabled(enabled bool) {
	mf.colorEnabled = enabled
}

// SetTimeFormat sets the time format
func (mf *MessageFormatter) SetTimeFormat(format string) {
	mf.timeFormat = format
}

// SetPrefix sets the prefix for a given message type
func (mf *MessageFormatter) SetPrefix(msgType string, prefix string) {
	mf.prefixes[msgType] = prefix
}

// GetTimeFormat returns the current time format
func (mf *MessageFormatter) GetTimeFormat() string {
	return mf.timeFormat
}

// IsColorEnabled checks if coloring is enabled
func (mf *MessageFormatter) IsColorEnabled() bool {
	return mf.colorEnabled
}

// GetPrefix returns the prefix for a given message type
func (mf *MessageFormatter) GetPrefix(msgType string) string {
	if prefix, ok := mf.prefixes[msgType]; ok {
		return prefix
	}
	return colorText("***", 6) // Domyślny prefix w kolorze fioletowym
}
