package api

import (
	"context"
	"crypto/rand"
	"encoding/hex"
)

func newAttachID() string {
	var b [8]byte
	_, _ = rand.Read(b[:])
	return hex.EncodeToString(b[:])
}

func handleBotAttach(_ context.Context, s *Session, req *RequestMsg) (interface{}, *HandlerError) {
	var p botIDParam
	if e := decodeParams(req, &p); e != nil {
		return nil, e
	}
	if s.server.BotByID(p.BotID) == nil {
		return nil, notFound("bot not found")
	}
	s.server.attach.Attach(p.BotID, s.id)
	s.trackAttach(p.BotID)
	return map[string]string{"attach_id": newAttachID()}, nil
}

func handleBotDetach(_ context.Context, s *Session, req *RequestMsg) (interface{}, *HandlerError) {
	var p botIDParam
	if e := decodeParams(req, &p); e != nil {
		return nil, e
	}
	s.server.attach.Detach(p.BotID, s.id)
	s.untrackAttach(p.BotID)
	return map[string]bool{"ok": true}, nil
}
