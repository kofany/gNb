package api

import (
	"context"
	"fmt"
	"strings"
)

type nickParam struct {
	Nick string `json:"nick"`
}

type ownerParam struct {
	Mask string `json:"mask"`
}

func handleNicksAdd(_ context.Context, s *Session, req *RequestMsg) (any, *HandlerError) {
	var p nickParam
	if e := decodeParams(req, &p); e != nil {
		return nil, e
	}
	if p.Nick == "" {
		return nil, paramErr("nick required")
	}
	if s.server.deps.NickManager == nil {
		return nil, notFound("nick manager unavailable")
	}
	if err := s.server.deps.NickManager.AddNick(p.Nick); err != nil {
		return nil, internalErr(err)
	}
	return map[string]bool{"ok": true}, nil
}

func handleNicksRemove(_ context.Context, s *Session, req *RequestMsg) (any, *HandlerError) {
	var p nickParam
	if e := decodeParams(req, &p); e != nil {
		return nil, e
	}
	if p.Nick == "" {
		return nil, paramErr("nick required")
	}
	if s.server.deps.NickManager == nil {
		return nil, notFound("nick manager unavailable")
	}
	if err := s.server.deps.NickManager.RemoveNick(p.Nick); err != nil {
		return nil, internalErr(err)
	}
	return map[string]bool{"ok": true}, nil
}

func handleOwnersAdd(_ context.Context, s *Session, req *RequestMsg) (any, *HandlerError) {
	var p ownerParam
	if e := decodeParams(req, &p); e != nil {
		return nil, e
	}
	if p.Mask == "" {
		return nil, paramErr("mask required")
	}
	if s.server.deps.BotManager == nil {
		return nil, notFound("bot manager unavailable")
	}
	if err := s.server.deps.BotManager.AddOwner(p.Mask); err != nil {
		return nil, internalErr(err)
	}
	return map[string]bool{"ok": true}, nil
}

func handleOwnersRemove(_ context.Context, s *Session, req *RequestMsg) (any, *HandlerError) {
	var p ownerParam
	if e := decodeParams(req, &p); e != nil {
		return nil, e
	}
	if p.Mask == "" {
		return nil, paramErr("mask required")
	}
	if s.server.deps.BotManager == nil {
		return nil, notFound("bot manager unavailable")
	}
	if err := s.server.deps.BotManager.RemoveOwner(p.Mask); err != nil {
		return nil, internalErr(err)
	}
	return map[string]bool{"ok": true}, nil
}

func handleBNCStart(_ context.Context, s *Session, req *RequestMsg) (any, *HandlerError) {
	var p botIDParam
	if e := decodeParams(req, &p); e != nil {
		return nil, e
	}
	b := s.server.BotByID(p.BotID)
	if b == nil {
		return nil, notFound("bot not found")
	}
	port, password, err := b.StartBNC()
	if err != nil {
		return nil, internalErr(err)
	}
	host := ""
	if cfg, ok := s.server.configForBot(p.BotID); ok {
		host = cfg.Vhost
	}
	if host == "" {
		host = "<your-host>"
	}
	// IPv6 literals must be bracketed in ssh URIs.
	if strings.Contains(host, ":") && !strings.HasPrefix(host, "[") {
		host = "[" + host + "]"
	}
	return map[string]any{
		"port":        port,
		"password":    password,
		"ssh_command": fmt.Sprintf("ssh -p %d %s@%s %s", port, b.GetCurrentNick(), host, password),
	}, nil
}

func handleBNCStop(_ context.Context, s *Session, req *RequestMsg) (any, *HandlerError) {
	var p botIDParam
	if e := decodeParams(req, &p); e != nil {
		return nil, e
	}
	b := s.server.BotByID(p.BotID)
	if b == nil {
		return nil, notFound("bot not found")
	}
	b.StopBNC()
	return map[string]bool{"ok": true}, nil
}
