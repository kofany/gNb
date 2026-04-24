package api

import (
	"github.com/kofany/gNb/internal/types"
	irc "github.com/kofany/go-ircevo"
)

// serverSink adapts Server into types.EventSink.
type serverSink struct{ srv *Server }

// Sink returns the types.EventSink that Bot/BotManager/NickManager wire to.
func (s *Server) Sink() types.EventSink { return &serverSink{srv: s} }

func (s *serverSink) BotConnected(botID, nick, server string) {
	s.srv.hub.Publish("bot.connected", map[string]interface{}{"bot_id": botID, "nick": nick, "server": server})
}

func (s *serverSink) BotDisconnected(botID, reason string) {
	s.srv.hub.Publish("bot.disconnected", map[string]interface{}{"bot_id": botID, "reason": reason})
}

func (s *serverSink) BotNickChanged(botID, oldNick, newNick string) {
	s.srv.hub.Publish("bot.nick_changed", map[string]interface{}{"bot_id": botID, "old": oldNick, "new": newNick})
}

func (s *serverSink) BotNickCaptured(botID, nick, kind string) {
	s.srv.hub.Publish("bot.nick_captured", map[string]interface{}{"bot_id": botID, "nick": nick, "kind": kind})
}

func (s *serverSink) BotJoinedChannel(botID, channel string) {
	s.srv.hub.Publish("bot.joined_channel", map[string]interface{}{"bot_id": botID, "channel": channel})
}

func (s *serverSink) BotPartedChannel(botID, channel string) {
	s.srv.hub.Publish("bot.parted_channel", map[string]interface{}{"bot_id": botID, "channel": channel})
}

func (s *serverSink) BotKicked(botID, channel, by, reason string) {
	s.srv.hub.Publish("bot.kicked", map[string]interface{}{"bot_id": botID, "channel": channel, "by": by, "reason": reason})
}

func (s *serverSink) BotBanned(botID string, code int) {
	s.srv.hub.Publish("bot.banned_from_server", map[string]interface{}{"bot_id": botID, "code": code})
}

func (s *serverSink) BotAdded(botID, server string, port int, ssl bool, vhost string) {
	s.srv.hub.Publish("node.bot_added", map[string]interface{}{
		"bot_id": botID,
		"config": map[string]interface{}{
			"server": server,
			"port":   port,
			"ssl":    ssl,
			"vhost":  vhost,
		},
	})
}

func (s *serverSink) BotRemoved(botID string) {
	s.srv.hub.Publish("node.bot_removed", map[string]interface{}{"bot_id": botID})
}

func (s *serverSink) NicksChanged(nicks []string) {
	s.srv.hub.Publish("nicks.changed", map[string]interface{}{"nicks": nicks})
}

func (s *serverSink) OwnersChanged(owners []string) {
	s.srv.hub.Publish("owners.changed", map[string]interface{}{"owners": owners})
}

func (s *serverSink) BotIRCEvent(botID string, e *irc.Event) {
	srv := s.srv
	// Always emit the raw_in for attached sessions.
	srv.attach.Publish(srv, botID, srv.NewAttachEvent("bot.attach.raw_in", map[string]interface{}{
		"bot_id": botID,
		"line":   e.Raw,
	}))
	if hl := translateIRCEventToHighLevel(srv, botID, e); hl != nil {
		srv.attach.Publish(srv, botID, *hl)
	}
}
