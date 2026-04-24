package api

import (
	irc "github.com/kofany/go-ircevo"
)

// translateIRCEventToHighLevel converts an irc.Event into a bot.attach.* event
// with a friendly payload. Returns nil for events we do not translate
// (raw_in is emitted separately by the caller for every event).
func translateIRCEventToHighLevel(srv *Server, botID string, e *irc.Event) *EventMsg {
	from := map[string]string{"nick": e.Nick, "user": e.User, "host": e.Host}
	who := from

	switch e.Code {
	case "PRIVMSG":
		target := ""
		if len(e.Arguments) > 0 {
			target = e.Arguments[0]
		}
		ev := srv.NewAttachEvent("bot.attach.privmsg", map[string]interface{}{
			"bot_id": botID, "from": from, "target": target, "text": e.Message(),
		})
		return &ev
	case "NOTICE":
		target := ""
		if len(e.Arguments) > 0 {
			target = e.Arguments[0]
		}
		ev := srv.NewAttachEvent("bot.attach.notice", map[string]interface{}{
			"bot_id": botID, "from": from, "target": target, "text": e.Message(),
		})
		return &ev
	case "JOIN":
		channel := ""
		if len(e.Arguments) > 0 {
			channel = e.Arguments[0]
		}
		ev := srv.NewAttachEvent("bot.attach.join", map[string]interface{}{
			"bot_id": botID, "who": who, "channel": channel,
		})
		return &ev
	case "PART":
		channel := ""
		if len(e.Arguments) > 0 {
			channel = e.Arguments[0]
		}
		ev := srv.NewAttachEvent("bot.attach.part", map[string]interface{}{
			"bot_id": botID, "who": who, "channel": channel, "reason": e.Message(),
		})
		return &ev
	case "QUIT":
		ev := srv.NewAttachEvent("bot.attach.quit", map[string]interface{}{
			"bot_id": botID, "who": who, "reason": e.Message(),
		})
		return &ev
	case "KICK":
		channel, target := "", ""
		if len(e.Arguments) >= 2 {
			channel = e.Arguments[0]
			target = e.Arguments[1]
		}
		ev := srv.NewAttachEvent("bot.attach.kick", map[string]interface{}{
			"bot_id": botID, "by": who, "channel": channel, "target": target, "reason": e.Message(),
		})
		return &ev
	case "MODE":
		target := ""
		args := []string{}
		if len(e.Arguments) >= 1 {
			target = e.Arguments[0]
		}
		if len(e.Arguments) >= 2 {
			args = e.Arguments[1:]
		}
		ev := srv.NewAttachEvent("bot.attach.mode", map[string]interface{}{
			"bot_id": botID, "from": from, "target": target, "args": args,
		})
		return &ev
	case "TOPIC":
		channel := ""
		if len(e.Arguments) > 0 {
			channel = e.Arguments[0]
		}
		ev := srv.NewAttachEvent("bot.attach.topic", map[string]interface{}{
			"bot_id": botID, "from": from, "channel": channel, "topic": e.Message(),
		})
		return &ev
	case "NICK":
		ev := srv.NewAttachEvent("bot.attach.nick", map[string]interface{}{
			"bot_id": botID, "from": from, "new_nick": e.Message(),
		})
		return &ev
	case "CTCP", "CTCP_ACTION", "CTCP_VERSION":
		target := ""
		if len(e.Arguments) > 0 {
			target = e.Arguments[0]
		}
		ev := srv.NewAttachEvent("bot.attach.ctcp", map[string]interface{}{
			"bot_id": botID, "from": from, "target": target, "command": e.Code, "text": e.Message(),
		})
		return &ev
	}
	return nil
}
