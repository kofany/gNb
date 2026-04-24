package api

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"sync"
	"sync/atomic"
	"time"

	"github.com/coder/websocket"

	"github.com/kofany/gNb/internal/config"
	"github.com/kofany/gNb/internal/types"
	"github.com/kofany/gNb/internal/util"
)

// Deps bundles everything the API handlers need from the rest of the program.
type Deps struct {
	Config      *config.Config
	BotManager  types.BotManager
	NickManager types.NickManager
}

// Server is the panel WebSocket API server.
type Server struct {
	cfg       config.APIConfig
	nodeID    string
	deps      Deps
	hub       *EventHub
	attach    *AttachManager
	auth      *tokenChecker
	router    *Router
	startTime time.Time

	httpSrv *http.Server

	botIDByIndex []string
	indexByID    map[string]int

	sessMu     sync.Mutex
	sessions   map[uint64]*Session
	nextSessID uint64
	activeN    atomic.Int32
}

// New builds a Server but does not start it.
func New(cfg config.APIConfig, nodeID string, deps Deps) *Server {
	s := &Server{
		cfg:       cfg,
		nodeID:    nodeID,
		deps:      deps,
		hub:       NewEventHub(nodeID, cfg.EventBuffer),
		auth:      newTokenChecker(cfg.AuthToken),
		router:    NewRouter(),
		startTime: time.Now(),
		sessions:  make(map[uint64]*Session),
		indexByID: make(map[string]int),
	}
	s.attach = NewAttachManager()
	if deps.Config != nil {
		s.botIDByIndex = make([]string, len(deps.Config.Bots))
		for i, bc := range deps.Config.Bots {
			id := ComputeBotID(bc.Server, bc.Port, bc.Vhost, i)
			s.botIDByIndex[i] = id
			s.indexByID[id] = i
		}
	}
	s.registerRoutes()
	return s
}

// registerRoutes wires all method handlers. Populated as handlers are added.
func (s *Server) registerRoutes() {
	// populated by subsequent tasks
}

// Hub returns the EventHub.
func (s *Server) Hub() *EventHub { return s.hub }

// AttachMgr returns the AttachManager.
func (s *Server) AttachMgr() *AttachManager { return s.attach }

// BotByID returns the Bot at the config-derived position for the given bot_id, or nil.
func (s *Server) BotByID(id string) types.Bot {
	i, ok := s.indexByID[id]
	if !ok {
		return nil
	}
	if s.deps.BotManager == nil {
		return nil
	}
	bots := s.deps.BotManager.GetBots()
	if i >= len(bots) {
		return nil
	}
	return bots[i]
}

// BotIDByIndex returns the bot_id for position i (or "" if out of range).
func (s *Server) BotIDByIndex(i int) string {
	if i < 0 || i >= len(s.botIDByIndex) {
		return ""
	}
	return s.botIDByIndex[i]
}

// NewAttachEvent constructs an EventMsg with monotonic seq, reusing the hub's counter.
func (s *Server) NewAttachEvent(event string, data interface{}) EventMsg {
	return EventMsg{
		Type:   "event",
		Event:  event,
		NodeID: s.nodeID,
		TS:     nowUTC(),
		Seq:    s.hub.seq.Add(1),
		Data:   data,
	}
}

// Run starts the HTTP listener and blocks until ctx is cancelled or listener errors.
func (s *Server) Run(ctx context.Context) error {
	mux := http.NewServeMux()
	mux.HandleFunc("/ws", s.handleWS)
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})

	s.httpSrv = &http.Server{
		Addr:              s.cfg.BindAddr,
		Handler:           mux,
		ReadHeaderTimeout: 10 * time.Second,
	}

	errCh := make(chan error, 1)
	go func() {
		if s.cfg.TLSCertFile != "" && s.cfg.TLSKeyFile != "" {
			util.Info("API: starting WSS server on %s (TLS)", s.cfg.BindAddr)
			errCh <- s.httpSrv.ListenAndServeTLS(s.cfg.TLSCertFile, s.cfg.TLSKeyFile)
		} else {
			util.Info("API: starting WS server on %s (plain HTTP)", s.cfg.BindAddr)
			errCh <- s.httpSrv.ListenAndServe()
		}
	}()

	select {
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = s.httpSrv.Shutdown(shutdownCtx)
		s.closeAllSessions(websocket.StatusGoingAway, "shutdown")
		return nil
	case err := <-errCh:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return fmt.Errorf("api server: %w", err)
	}
}

func (s *Server) handleWS(w http.ResponseWriter, r *http.Request) {
	if s.activeN.Load() >= int32(s.cfg.MaxConnections) {
		http.Error(w, "too many connections", http.StatusServiceUnavailable)
		return
	}

	c, err := websocket.Accept(w, r, &websocket.AcceptOptions{
		InsecureSkipVerify: true,
	})
	if err != nil {
		util.Warning("API: websocket accept failed: %v", err)
		return
	}
	c.SetReadLimit(64 * 1024)

	remote, _, _ := net.SplitHostPort(r.RemoteAddr)
	if remote == "" {
		remote = r.RemoteAddr
	}

	s.activeN.Add(1)
	defer s.activeN.Add(-1)

	s.sessMu.Lock()
	s.nextSessID++
	sid := s.nextSessID
	s.sessMu.Unlock()

	sess := newSession(sid, remote, c, s)

	s.sessMu.Lock()
	s.sessions[sid] = sess
	s.sessMu.Unlock()

	defer func() {
		s.sessMu.Lock()
		delete(s.sessions, sid)
		s.sessMu.Unlock()
		sess.close()
	}()

	sess.run(r.Context())
}

func (s *Server) closeAllSessions(code websocket.StatusCode, reason string) {
	s.sessMu.Lock()
	sessions := make([]*Session, 0, len(s.sessions))
	for _, sess := range s.sessions {
		sessions = append(sessions, sess)
	}
	s.sessMu.Unlock()
	for _, sess := range sessions {
		sess.conn.Close(code, reason)
	}
}
