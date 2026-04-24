package api

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"sync/atomic"
	"time"

	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"

	"github.com/kofany/gNb/internal/util"
)

const (
	handshakeDeadline = 5 * time.Second
	readIdleDeadline  = 45 * time.Second
	pingInterval      = 30 * time.Second
	outboundBuffer    = 256
)

// Session is the per-connection state.
type Session struct {
	id     uint64
	remote string
	conn   *websocket.Conn
	server *Server

	authed atomic.Bool

	out chan interface{}
	sub *Subscriber

	attachedMu sync.Mutex
	attached   map[string]struct{}
}

func newSession(id uint64, remote string, c *websocket.Conn, s *Server) *Session {
	return &Session{
		id:       id,
		remote:   remote,
		conn:     c,
		server:   s,
		out:      make(chan interface{}, outboundBuffer),
		attached: make(map[string]struct{}),
	}
}

func (s *Session) isAuthed() bool { return s.authed.Load() }

func (s *Session) markAuthed() { s.authed.Store(true) }

// ID returns the session's numeric id (used by AttachManager).
func (s *Session) ID() uint64 { return s.id }

// Server returns the owning Server.
func (s *Session) Server() *Server { return s.server }

func (s *Session) trackAttach(botID string) {
	s.attachedMu.Lock()
	s.attached[botID] = struct{}{}
	s.attachedMu.Unlock()
}

func (s *Session) untrackAttach(botID string) {
	s.attachedMu.Lock()
	delete(s.attached, botID)
	s.attachedMu.Unlock()
}

func (s *Session) listAttached() []string {
	s.attachedMu.Lock()
	out := make([]string, 0, len(s.attached))
	for k := range s.attached {
		out = append(out, k)
	}
	s.attachedMu.Unlock()
	return out
}

// startSubPump drains sub's channel and forwards every event to the session's
// outbound writer. Runs until the subscriber is unsubscribed (channel close).
func (s *Session) startSubPump(sub *Subscriber) {
	go func() {
		for msg := range sub.Ch() {
			s.send(msg)
		}
	}()
}

// close handles cleanup: detach from attach manager, unsubscribe from hub.
func (s *Session) close() {
	for _, bid := range s.listAttached() {
		s.server.attach.Detach(bid, s.id)
	}
	if s.sub != nil {
		s.server.hub.Unsubscribe(s.sub)
		s.sub = nil
	}
}

// send enqueues a message to the outbound writer. Non-blocking: on overflow
// the session is closed with a PolicyViolation code.
func (s *Session) send(msg interface{}) {
	select {
	case s.out <- msg:
	default:
		s.conn.Close(websocket.StatusPolicyViolation, "backpressure")
	}
}

// run drives the handshake + read loop + write pump.
func (s *Session) run(ctx context.Context) {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	done := make(chan struct{})
	go s.writerLoop(ctx, done)

	if !s.handshake(ctx) {
		cancel()
		<-done
		return
	}
	s.readerLoop(ctx)
	cancel()
	<-done
}

// writeSync writes a message to the WebSocket directly, bypassing the
// outbound channel. Used during handshake where we must flush before close.
func (s *Session) writeSync(ctx context.Context, msg interface{}) {
	wctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	_ = wsjson.Write(wctx, s.conn, msg)
}

// handshake waits for auth.login with a deadline, validates token, and
// responds. Returns true if the session is authenticated.
func (s *Session) handshake(ctx context.Context) bool {
	rctx, cancel := context.WithTimeout(ctx, handshakeDeadline)
	defer cancel()

	_, raw, err := s.conn.Read(rctx)
	if err != nil {
		s.conn.Close(4003, "handshake timeout")
		return false
	}
	req, derr := DecodeRequest(raw)
	if derr != nil {
		s.writeSync(ctx, NewError("", ErrInvalidParams, derr.Error()))
		s.conn.Close(4004, "protocol error")
		return false
	}
	if req.Method != "auth.login" {
		s.writeSync(ctx, NewError(req.ID, ErrForbidden, "first message must be auth.login"))
		s.conn.Close(4001, "not authenticated")
		return false
	}
	var p struct {
		Token string `json:"token"`
	}
	if err := json.Unmarshal(req.Params, &p); err != nil {
		s.writeSync(ctx, NewError(req.ID, ErrInvalidParams, "missing token"))
		s.conn.Close(4001, "bad token")
		return false
	}
	ok, rl := s.server.auth.CheckToken(s.remote, p.Token)
	if rl {
		s.writeSync(ctx, NewError(req.ID, ErrRateLimited, "too many failed attempts"))
		s.conn.Close(4001, "rate limited")
		return false
	}
	if !ok {
		s.writeSync(ctx, NewError(req.ID, ErrUnauthorized, "invalid token"))
		s.conn.Close(4001, "unauthorized")
		return false
	}
	s.markAuthed()
	s.writeSync(ctx, NewResponse(req.ID, map[string]interface{}{
		"node_id":     s.server.nodeID,
		"node_name":   s.server.cfg.NodeName,
		"api_version": "1.0",
		"session_id":  s.id,
	}))
	util.Info("API: session %d authenticated from %s", s.id, s.remote)
	return true
}

func (s *Session) readerLoop(ctx context.Context) {
	for {
		rctx, cancel := context.WithTimeout(ctx, readIdleDeadline)
		_, raw, err := s.conn.Read(rctx)
		cancel()
		if err != nil {
			if !errors.Is(err, context.Canceled) {
				util.Debug("API: session %d read: %v", s.id, err)
			}
			return
		}
		req, derr := DecodeRequest(raw)
		if derr != nil {
			s.send(NewError("", ErrInvalidParams, derr.Error()))
			continue
		}
		result, herr := s.server.router.Dispatch(ctx, s, req)
		if herr != nil {
			s.send(NewError(req.ID, herr.Code, herr.Message))
			continue
		}
		s.send(NewResponse(req.ID, result))
	}
}

func (s *Session) writerLoop(ctx context.Context, done chan<- struct{}) {
	defer close(done)
	pingT := time.NewTicker(pingInterval)
	defer pingT.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case msg, ok := <-s.out:
			if !ok {
				return
			}
			wctx, cancel := context.WithTimeout(ctx, 10*time.Second)
			err := wsjson.Write(wctx, s.conn, msg)
			cancel()
			if err != nil {
				s.conn.Close(websocket.StatusInternalError, "write failed")
				return
			}
		case <-pingT.C:
			pctx, cancel := context.WithTimeout(ctx, 10*time.Second)
			err := s.conn.Ping(pctx)
			cancel()
			if err != nil {
				return
			}
		}
	}
}
