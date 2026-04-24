package api

import "context"

type nickParam struct {
	Nick string `json:"nick"`
}

type ownerParam struct {
	Mask string `json:"mask"`
}

func handleNicksAdd(_ context.Context, s *Session, req *RequestMsg) (interface{}, *HandlerError) {
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

func handleNicksRemove(_ context.Context, s *Session, req *RequestMsg) (interface{}, *HandlerError) {
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

func handleOwnersAdd(_ context.Context, s *Session, req *RequestMsg) (interface{}, *HandlerError) {
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

func handleOwnersRemove(_ context.Context, s *Session, req *RequestMsg) (interface{}, *HandlerError) {
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

func handleBNCStart(_ context.Context, s *Session, req *RequestMsg) (interface{}, *HandlerError) {
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
	return map[string]interface{}{
		"port":     port,
		"password": password,
	}, nil
}

func handleBNCStop(_ context.Context, s *Session, req *RequestMsg) (interface{}, *HandlerError) {
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
