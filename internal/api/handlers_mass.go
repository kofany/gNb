package api

import (
	"context"
)

type massChanParam struct {
	Channel string `json:"channel"`
}

type massRawParam struct {
	Line string `json:"line"`
}

type massSayParam struct {
	Target  string `json:"target"`
	Message string `json:"message"`
}

func cooldownErr(name string) *HandlerError {
	return &HandlerError{Code: ErrCooldown, Message: "mass-" + name + " cooldown active"}
}

func handleMassJoin(_ context.Context, s *Session, req *RequestMsg) (interface{}, *HandlerError) {
	var p massChanParam
	if e := decodeParams(req, &p); e != nil {
		return nil, e
	}
	if p.Channel == "" {
		return nil, paramErr("channel required")
	}
	bm := s.server.deps.BotManager
	if bm == nil {
		return nil, notFound("bot manager unavailable")
	}
	if !bm.CanExecuteMassCommand("join") {
		return nil, cooldownErr("join")
	}
	bots := bm.GetBots()
	for _, b := range bots {
		b.JoinChannel(p.Channel)
	}
	return map[string]interface{}{"ok": true, "affected": len(bots)}, nil
}

func handleMassPart(_ context.Context, s *Session, req *RequestMsg) (interface{}, *HandlerError) {
	var p massChanParam
	if e := decodeParams(req, &p); e != nil {
		return nil, e
	}
	if p.Channel == "" {
		return nil, paramErr("channel required")
	}
	bm := s.server.deps.BotManager
	if bm == nil {
		return nil, notFound("bot manager unavailable")
	}
	if !bm.CanExecuteMassCommand("part") {
		return nil, cooldownErr("part")
	}
	bots := bm.GetBots()
	for _, b := range bots {
		b.PartChannel(p.Channel)
	}
	return map[string]interface{}{"ok": true, "affected": len(bots)}, nil
}

func handleMassReconnect(_ context.Context, s *Session, _ *RequestMsg) (interface{}, *HandlerError) {
	bm := s.server.deps.BotManager
	if bm == nil {
		return nil, notFound("bot manager unavailable")
	}
	if !bm.CanExecuteMassCommand("reconnect") {
		return nil, cooldownErr("reconnect")
	}
	bots := bm.GetBots()
	for _, b := range bots {
		go b.Reconnect()
	}
	return map[string]interface{}{"ok": true, "affected": len(bots)}, nil
}

func handleMassRaw(_ context.Context, s *Session, req *RequestMsg) (interface{}, *HandlerError) {
	var p massRawParam
	if e := decodeParams(req, &p); e != nil {
		return nil, e
	}
	if p.Line == "" {
		return nil, paramErr("line required")
	}
	bm := s.server.deps.BotManager
	if bm == nil {
		return nil, notFound("bot manager unavailable")
	}
	line := sanitizeIRCLine(p.Line)
	bots := bm.GetBots()
	for _, b := range bots {
		b.SendRaw(line)
	}
	return map[string]interface{}{"ok": true, "affected": len(bots)}, nil
}

func handleMassSay(_ context.Context, s *Session, req *RequestMsg) (interface{}, *HandlerError) {
	var p massSayParam
	if e := decodeParams(req, &p); e != nil {
		return nil, e
	}
	if p.Target == "" || p.Message == "" {
		return nil, paramErr("target and message required")
	}
	bm := s.server.deps.BotManager
	if bm == nil {
		return nil, notFound("bot manager unavailable")
	}
	bots := bm.GetBots()
	for _, b := range bots {
		b.SendMessage(p.Target, p.Message)
	}
	return map[string]interface{}{"ok": true, "affected": len(bots)}, nil
}
