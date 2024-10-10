package irc

import (
	"regexp"
	"strings"
)

// Event represents a parsed IRC message
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

// ParseIRCMessage parses a raw IRC message into an Event structure
func ParseIRCMessage(raw string) *Event {
	e := &Event{
		Raw: raw,
	}

	// Parse prefix
	if strings.HasPrefix(raw, ":") {
		parts := strings.SplitN(raw[1:], " ", 2)
		e.Source = parts[0]
		raw = parts[1]

		// Parse nick!user@host
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

	// Parse command and arguments
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
