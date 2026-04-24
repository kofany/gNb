package api

import (
	"context"
)

type BotSummary struct {
	BotID              string   `json:"bot_id"`
	Server             string   `json:"server"`
	Port               int      `json:"port"`
	SSL                bool     `json:"ssl"`
	Vhost              string   `json:"vhost"`
	CurrentNick        string   `json:"current_nick"`
	Connected          bool     `json:"connected"`
	IsSingleLetterNick bool     `json:"is_single_letter_nick"`
	JoinedChannels     []string `json:"joined_channels"`
}

func handleBotList(_ context.Context, s *Session, _ *RequestMsg) (interface{}, *HandlerError) {
	srv := s.server
	if srv.deps.BotManager == nil || srv.deps.Config == nil {
		return map[string]interface{}{"bots": []BotSummary{}}, nil
	}
	bots := srv.deps.BotManager.GetBots()
	cfgBots := srv.deps.Config.Bots
	out := make([]BotSummary, 0, len(bots))
	for i, b := range bots {
		if i >= len(cfgBots) {
			break
		}
		bc := cfgBots[i]
		nick := b.GetCurrentNick()
		out = append(out, BotSummary{
			BotID:              srv.BotIDByIndex(i),
			Server:             bc.Server,
			Port:               bc.Port,
			SSL:                bc.SSL,
			Vhost:              bc.Vhost,
			CurrentNick:        nick,
			Connected:          b.IsConnected(),
			IsSingleLetterNick: len(nick) == 1,
			JoinedChannels:     channelsBotIsOn(srv.deps.Config.Channels, b),
		})
	}
	return map[string]interface{}{"bots": out}, nil
}

// channelsBotIsOn filters the configured channel list by whether the bot
// reports IsOnChannel. It is the best snapshot we can produce without
// storing per-bot joined state directly on the public Bot interface.
func channelsBotIsOn(all []string, b interface {
	IsOnChannel(string) bool
}) []string {
	out := []string{}
	for _, ch := range all {
		if b.IsOnChannel(ch) {
			out = append(out, ch)
		}
	}
	return out
}

func handleNicksList(_ context.Context, s *Session, _ *RequestMsg) (interface{}, *HandlerError) {
	if s.server.deps.NickManager == nil {
		return map[string]interface{}{"nicks": []string{}}, nil
	}
	return map[string]interface{}{"nicks": s.server.deps.NickManager.GetNicks()}, nil
}

func handleOwnersList(_ context.Context, s *Session, _ *RequestMsg) (interface{}, *HandlerError) {
	if s.server.deps.BotManager == nil {
		return map[string]interface{}{"owners": []string{}}, nil
	}
	return map[string]interface{}{"owners": s.server.deps.BotManager.GetOwners()}, nil
}
