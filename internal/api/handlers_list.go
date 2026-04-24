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
	out := make([]BotSummary, 0, len(bots))
	for _, b := range bots {
		bc, ok := srv.configForBot(b.GetBotID())
		if !ok {
			continue
		}
		nick := b.GetCurrentNick()
		out = append(out, BotSummary{
			BotID:              b.GetBotID(),
			Server:             bc.Server,
			Port:               bc.Port,
			SSL:                bc.SSL,
			Vhost:              bc.Vhost,
			CurrentNick:        nick,
			Connected:          b.IsConnected(),
			IsSingleLetterNick: len(nick) == 1,
			JoinedChannels:     b.GetJoinedChannels(),
		})
	}
	return map[string]interface{}{"bots": out}, nil
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
