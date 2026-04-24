package api

import "context"

// replayCap is an upper bound on replay_last. Larger requests are silently
// clamped: the session outbound buffer is 256 slots, so feeding more than
// that synchronously would trigger the backpressure-close path before
// the panel even starts consuming.
const replayCap = 128

type subscribeParam struct {
	Topics     []string `json:"topics"`
	ReplayLast int      `json:"replay_last"`
}

func handleEventsSubscribe(_ context.Context, s *Session, req *RequestMsg) (any, *HandlerError) {
	var p subscribeParam
	if e := decodeParams(req, &p); e != nil {
		return nil, e
	}
	if s.sub != nil {
		s.server.hub.Unsubscribe(s.sub)
		s.sub = nil
	}
	sub := s.server.hub.Subscribe(p.Topics, 256)
	s.sub = sub
	s.startSubPump(sub)

	n := p.ReplayLast
	if n > replayCap {
		n = replayCap
	}
	replayed := s.server.hub.Replay(sub, n)
	for _, m := range replayed {
		s.send(m)
	}

	return map[string]any{
		"cursor":   s.server.hub.Seq(),
		"replayed": len(replayed),
	}, nil
}

func handleEventsUnsubscribe(_ context.Context, s *Session, _ *RequestMsg) (any, *HandlerError) {
	if s.sub != nil {
		s.server.hub.Unsubscribe(s.sub)
		s.sub = nil
	}
	return map[string]bool{"ok": true}, nil
}
