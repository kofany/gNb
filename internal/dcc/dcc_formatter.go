package dcc

import (
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/kofany/gNb/internal/irc"
)

// MessageFormatter formatuje wiadomości IRC do przyjaznego formatu
type MessageFormatter struct {
	nickname     string
	timeFormat   string
	colorEnabled bool
	prefixes     map[string]string
}

// NewMessageFormatter tworzy nową instancję formatera wiadomości
func NewMessageFormatter(nickname string) *MessageFormatter {
	return &MessageFormatter{
		nickname:     nickname,
		timeFormat:   "15:04",
		colorEnabled: true,
		prefixes: map[string]string{
			"PRIVMSG": "<*>",
			"NOTICE":  "-*-",
			"JOIN":    ">>>",
			"PART":    "<<<",
			"QUIT":    "---",
			"NICK":    "***",
			"MODE":    "***",
			"KICK":    "<!>",
		},
	}
}

// FormatMessage formatuje surową wiadomość IRC
func (mf *MessageFormatter) FormatMessage(raw string) string {
	event := irc.ParseIRCMessage(raw)
	if event == nil {
		return ""
	}

	switch event.Command {
	case "PRIVMSG":
		return mf.formatPrivMsg(event)
	case "NOTICE":
		return mf.formatNotice(event)
	case "JOIN":
		return mf.formatJoin(event)
	case "PART":
		return mf.formatPart(event)
	case "QUIT":
		return mf.formatQuit(event)
	case "NICK":
		return mf.formatNick(event)
	case "MODE":
		return mf.formatMode(event)
	case "KICK":
		return mf.formatKick(event)
	default:
		return mf.formatOther(event)
	}
}

// formatPrivMsg formatuje wiadomości prywatne
func (mf *MessageFormatter) formatPrivMsg(event *irc.Event) string {
	timestamp := time.Now().Format(mf.timeFormat)
	sender := event.Nick
	target := ""
	if len(event.Args) > 0 {
		target = event.Args[0]
	}
	message := event.Message

	if target == mf.nickname {
		// Wiadomość prywatna
		return fmt.Sprintf("[%s] <%s> %s",
			timestamp,
			mf.formatNickname(sender),
			mf.formatText(message))
	} else {
		// Wiadomość na kanał
		return fmt.Sprintf("[%s] <%s:%s> %s",
			timestamp,
			target,
			mf.formatNickname(sender),
			mf.formatText(message))
	}
}

// formatNotice formatuje powiadomienia
func (mf *MessageFormatter) formatNotice(event *irc.Event) string {
	timestamp := time.Now().Format(mf.timeFormat)
	sender := event.Nick
	message := event.Message

	return fmt.Sprintf("[%s] %s %s %s",
		timestamp,
		mf.prefixes["NOTICE"],
		mf.formatNickname(sender),
		mf.formatText(message))
}

// formatJoin formatuje wiadomości o dołączeniu do kanału
func (mf *MessageFormatter) formatJoin(event *irc.Event) string {
	timestamp := time.Now().Format(mf.timeFormat)
	nick := event.Nick
	channel := ""
	if len(event.Args) > 0 {
		channel = event.Args[0]
	}

	return fmt.Sprintf("[%s] %s %s has joined %s",
		timestamp,
		mf.prefixes["JOIN"],
		mf.formatNickname(nick),
		channel)
}

// formatPart formatuje wiadomości o opuszczeniu kanału
func (mf *MessageFormatter) formatPart(event *irc.Event) string {
	timestamp := time.Now().Format(mf.timeFormat)
	nick := event.Nick
	channel := ""
	if len(event.Args) > 0 {
		channel = event.Args[0]
	}

	message := event.Message
	if message != "" {
		return fmt.Sprintf("[%s] %s %s has left %s (%s)",
			timestamp,
			mf.prefixes["PART"],
			mf.formatNickname(nick),
			channel,
			mf.formatText(message))
	}

	return fmt.Sprintf("[%s] %s %s has left %s",
		timestamp,
		mf.prefixes["PART"],
		mf.formatNickname(nick),
		channel)
}

// formatQuit formatuje wiadomości o wyjściu z sieci
func (mf *MessageFormatter) formatQuit(event *irc.Event) string {
	timestamp := time.Now().Format(mf.timeFormat)
	nick := event.Nick
	reason := event.Message

	return fmt.Sprintf("[%s] %s %s has quit (%s)",
		timestamp,
		mf.prefixes["QUIT"],
		mf.formatNickname(nick),
		mf.formatText(reason))
}

// formatNick formatuje wiadomości o zmianie nicka
func (mf *MessageFormatter) formatNick(event *irc.Event) string {
	timestamp := time.Now().Format(mf.timeFormat)
	oldNick := event.Nick
	newNick := event.Message

	return fmt.Sprintf("[%s] %s %s is now known as %s",
		timestamp,
		mf.prefixes["NICK"],
		mf.formatNickname(oldNick),
		mf.formatNickname(newNick))
}

// formatMode formatuje zmiany trybów
func (mf *MessageFormatter) formatMode(event *irc.Event) string {
	timestamp := time.Now().Format(mf.timeFormat)
	nick := event.Nick
	target := ""
	modes := ""
	if len(event.Args) >= 2 {
		target = event.Args[0]
		modes = strings.Join(event.Args[1:], " ")
	}

	return fmt.Sprintf("[%s] %s %s sets mode %s on %s",
		timestamp,
		mf.prefixes["MODE"],
		mf.formatNickname(nick),
		modes,
		target)
}

// formatKick formatuje wiadomości o wyrzuceniu z kanału
func (mf *MessageFormatter) formatKick(event *irc.Event) string {
	timestamp := time.Now().Format(mf.timeFormat)
	nick := event.Nick
	channel := ""
	user := ""
	reason := event.Message

	if len(event.Args) >= 2 {
		channel = event.Args[0]
		user = event.Args[1]
	}

	return fmt.Sprintf("[%s] %s %s has kicked %s from %s (%s)",
		timestamp,
		mf.prefixes["KICK"],
		mf.formatNickname(nick),
		mf.formatNickname(user),
		channel,
		mf.formatText(reason))
}

// formatOther formatuje pozostałe typy wiadomości
func (mf *MessageFormatter) formatOther(event *irc.Event) string {
	timestamp := time.Now().Format(mf.timeFormat)
	cmd := event.Command
	nick := event.Nick

	// Podstawowe informacje
	info := fmt.Sprintf("[%s] %s %s", timestamp, cmd, nick)

	// Dodaj argumenty jeśli istnieją
	if len(event.Args) > 0 {
		info += " " + strings.Join(event.Args, " ")
	}

	// Dodaj message jeśli istnieje
	if event.Message != "" {
		info += " :" + mf.formatText(event.Message)
	}

	return info
}

// Funkcje pomocnicze do formatowania

// formatNickname formatuje nick użytkownika
func (mf *MessageFormatter) formatNickname(nick string) string {
	if !mf.colorEnabled {
		return nick
	}

	// Jeśli to nasz własny nick, wyróżnij go
	if nick == mf.nickname {
		return fmt.Sprintf("\x02%s\x02", nick) // Pogrubienie
	}
	return nick
}

// formatText formatuje tekst wiadomości
func (mf *MessageFormatter) formatText(text string) string {
	if !mf.colorEnabled {
		return text
	}
	return mf.processIRCFormatting(text)
}

// processIRCFormatting przetwarza kody formatowania IRC
func (mf *MessageFormatter) processIRCFormatting(text string) string {
	if !mf.colorEnabled {
		// Jeśli kolory są wyłączone, usuń wszystkie kody formatowania
		return stripIRCFormatting(text)
	}
	return text // Zachowaj oryginalne formatowanie
}

// stripIRCFormatting usuwa kody formatowania IRC
func stripIRCFormatting(text string) string {
	// Usuń kody kolorów (łącznie z parametrami)
	colorRegex := regexp.MustCompile(`\x03\d{1,2}(?:,\d{1,2})?`)
	text = colorRegex.ReplaceAllString(text, "")

	// Usuń pozostałe kody formatowania
	formatCodes := []string{
		"\x02", // Pogrubienie
		"\x1D", // Italiki
		"\x1F", // Podkreślenie
		"\x0F", // Reset formatowania
		"\x16", // Odwrócenie
	}

	for _, code := range formatCodes {
		text = strings.ReplaceAll(text, code, "")
	}

	return text
}

// Settery i gettery

// SetColorEnabled włącza lub wyłącza kolorowanie wyjścia
func (mf *MessageFormatter) SetColorEnabled(enabled bool) {
	mf.colorEnabled = enabled
}

// SetTimeFormat ustawia format czasu
func (mf *MessageFormatter) SetTimeFormat(format string) {
	mf.timeFormat = format
}

// SetPrefix ustawia prefix dla danego typu wiadomości
func (mf *MessageFormatter) SetPrefix(msgType string, prefix string) {
	mf.prefixes[msgType] = prefix
}

// GetTimeFormat zwraca aktualny format czasu
func (mf *MessageFormatter) GetTimeFormat() string {
	return mf.timeFormat
}

// IsColorEnabled sprawdza czy kolorowanie jest włączone
func (mf *MessageFormatter) IsColorEnabled() bool {
	return mf.colorEnabled
}

// GetPrefix zwraca prefix dla danego typu wiadomości
func (mf *MessageFormatter) GetPrefix(msgType string) string {
	if prefix, ok := mf.prefixes[msgType]; ok {
		return prefix
	}
	return "***" // Domyślny prefix
}
