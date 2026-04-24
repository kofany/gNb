package api

import (
	"context"
	"strings"
)

type botIDParam struct {
	BotID string `json:"bot_id"`
}

type botSayParam struct {
	BotID   string `json:"bot_id"`
	Target  string `json:"target"`
	Message string `json:"message"`
}

type botChanParam struct {
	BotID   string `json:"bot_id"`
	Channel string `json:"channel"`
}

type botQuitParam struct {
	BotID  string `json:"bot_id"`
	Reason string `json:"reason"`
}

type botNickParam struct {
	BotID   string `json:"bot_id"`
	NewNick string `json:"new_nick"`
}

type botRawParam struct {
	BotID string `json:"bot_id"`
	Line  string `json:"line"`
}

// sanitizeIRCLine strips CR and LF to prevent IRC command injection.
func sanitizeIRCLine(line string) string {
	r := strings.NewReplacer("\r", "", "\n", "")
	return r.Replace(line)
}

func handleBotSay(_ context.Context, s *Session, req *RequestMsg) (any, *HandlerError) {
	var p botSayParam
	if e := decodeParams(req, &p); e != nil {
		return nil, e
	}
	if p.Target == "" || p.Message == "" {
		return nil, paramErr("target and message required")
	}
	b := s.server.BotByID(p.BotID)
	if b == nil {
		return nil, notFound("bot not found")
	}
	b.SendMessage(p.Target, p.Message)
	return map[string]bool{"ok": true}, nil
}

func handleBotJoin(_ context.Context, s *Session, req *RequestMsg) (any, *HandlerError) {
	var p botChanParam
	if e := decodeParams(req, &p); e != nil {
		return nil, e
	}
	if p.Channel == "" {
		return nil, paramErr("channel required")
	}
	b := s.server.BotByID(p.BotID)
	if b == nil {
		return nil, notFound("bot not found")
	}
	b.JoinChannel(p.Channel)
	return map[string]bool{"ok": true}, nil
}

func handleBotPart(_ context.Context, s *Session, req *RequestMsg) (any, *HandlerError) {
	var p botChanParam
	if e := decodeParams(req, &p); e != nil {
		return nil, e
	}
	if p.Channel == "" {
		return nil, paramErr("channel required")
	}
	b := s.server.BotByID(p.BotID)
	if b == nil {
		return nil, notFound("bot not found")
	}
	b.PartChannel(p.Channel)
	return map[string]bool{"ok": true}, nil
}

func handleBotQuit(_ context.Context, s *Session, req *RequestMsg) (any, *HandlerError) {
	var p botQuitParam
	if e := decodeParams(req, &p); e != nil {
		return nil, e
	}
	b := s.server.BotByID(p.BotID)
	if b == nil {
		return nil, notFound("bot not found")
	}
	reason := p.Reason
	if reason == "" {
		reason = "API quit"
	}
	b.Quit(reason)
	return map[string]bool{"ok": true}, nil
}

func handleBotReconnect(_ context.Context, s *Session, req *RequestMsg) (any, *HandlerError) {
	var p botIDParam
	if e := decodeParams(req, &p); e != nil {
		return nil, e
	}
	b := s.server.BotByID(p.BotID)
	if b == nil {
		return nil, notFound("bot not found")
	}
	go b.Reconnect()
	return map[string]bool{"ok": true}, nil
}

func handleBotChangeNick(_ context.Context, s *Session, req *RequestMsg) (any, *HandlerError) {
	var p botNickParam
	if e := decodeParams(req, &p); e != nil {
		return nil, e
	}
	if p.NewNick == "" {
		return nil, paramErr("new_nick required")
	}
	b := s.server.BotByID(p.BotID)
	if b == nil {
		return nil, notFound("bot not found")
	}
	b.ChangeNick(p.NewNick)
	return map[string]bool{"ok": true}, nil
}

func handleBotRaw(_ context.Context, s *Session, req *RequestMsg) (any, *HandlerError) {
	var p botRawParam
	if e := decodeParams(req, &p); e != nil {
		return nil, e
	}
	if p.Line == "" {
		return nil, paramErr("line required")
	}
	b := s.server.BotByID(p.BotID)
	if b == nil {
		return nil, notFound("bot not found")
	}
	b.SendRaw(sanitizeIRCLine(p.Line))
	return map[string]bool{"ok": true}, nil
}
