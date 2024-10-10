// Plik internal/irc/parser.go

package irc

import (
	"regexp"
	"strings"
)

// Event reprezentuje sparsowaną wiadomość IRC.
type Event struct {
	Raw     string
	Source  string
	Nick    string
	User    string
	Host    string
	Command string
	Args    []string
	Message string
}

// ParseIRCMessage parsuje surową wiadomość IRC do struktury Event.
func ParseIRCMessage(raw string) *Event {
	e := &Event{
		Raw: raw,
	}

	// Parsowanie prefiksu
	if strings.HasPrefix(raw, ":") {
		parts := strings.SplitN(raw[1:], " ", 2)
		e.Source = parts[0]
		raw = parts[1]

		// Parsowanie nick!user@host
		re := regexp.MustCompile(`^(?P<Nick>[^!]+)!?(?P<User>[^@]*)@?(?P<Host>.*)$`)
		match := re.FindStringSubmatch(e.Source)
		if match != nil {
			e.Nick = match[1]
			e.User = match[2]
			e.Host = match[3]
		} else {
			e.Nick = e.Source
		}
	}

	// Parsowanie komendy i argumentów
	if idx := strings.Index(raw, " :"); idx != -1 {
		e.Args = strings.Fields(raw[:idx])
		e.Message = raw[idx+2:]
	} else {
		e.Args = strings.Fields(raw)
	}

	if len(e.Args) > 0 {
		e.Command = strings.ToUpper(e.Args[0])
		e.Args = e.Args[1:]
	}

	return e
}
