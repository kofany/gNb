package api

import (
	"sync"
	"time"
)

// AttachManager tracks per-bot subscribers (sessions that called bot.attach).
type AttachManager struct {
	mu   sync.RWMutex
	subs map[string]map[uint64]struct{} // bot_id -> session_id set
}

func NewAttachManager() *AttachManager {
	return &AttachManager{subs: make(map[string]map[uint64]struct{})}
}

func (am *AttachManager) Attach(botID string, sessionID uint64) {
	am.mu.Lock()
	defer am.mu.Unlock()
	set, ok := am.subs[botID]
	if !ok {
		set = make(map[uint64]struct{})
		am.subs[botID] = set
	}
	set[sessionID] = struct{}{}
}

func (am *AttachManager) Detach(botID string, sessionID uint64) {
	am.mu.Lock()
	defer am.mu.Unlock()
	if set, ok := am.subs[botID]; ok {
		delete(set, sessionID)
		if len(set) == 0 {
			delete(am.subs, botID)
		}
	}
}

func (am *AttachManager) DetachAll(sessionID uint64) {
	am.mu.Lock()
	defer am.mu.Unlock()
	for botID, set := range am.subs {
		delete(set, sessionID)
		if len(set) == 0 {
			delete(am.subs, botID)
		}
	}
}

// Sessions returns a snapshot of session_ids attached to bot_id.
func (am *AttachManager) Sessions(botID string) []uint64 {
	am.mu.RLock()
	defer am.mu.RUnlock()
	set, ok := am.subs[botID]
	if !ok {
		return nil
	}
	out := make([]uint64, 0, len(set))
	for sid := range set {
		out = append(out, sid)
	}
	return out
}

// Publish delivers msg to every session attached to bot_id.
func (am *AttachManager) Publish(srv *Server, botID string, msg EventMsg) {
	ids := am.Sessions(botID)
	if len(ids) == 0 {
		return
	}
	srv.sessMu.Lock()
	sessions := make([]*Session, 0, len(ids))
	for _, sid := range ids {
		if sess, ok := srv.sessions[sid]; ok {
			sessions = append(sessions, sess)
		}
	}
	srv.sessMu.Unlock()
	for _, sess := range sessions {
		sess.send(msg)
	}
}

// nowUTC returns an RFC3339Nano UTC timestamp.
func nowUTC() string { return time.Now().UTC().Format(time.RFC3339Nano) }
