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
	"github.com/kofany/gNb/internal/runtime"
	"github.com/kofany/gNb/internal/types"
	"github.com/kofany/gNb/internal/util"
)

// Deps bundles everything the API handlers need from the rest of the program.
type Deps struct {
	Config      *config.Config
	BotManager  types.BotManager
	NickManager types.NickManager
	Runtime     *runtime.RuntimeState
	Version     string
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

	indexByID map[string]int

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
		for i, bc := range deps.Config.Bots {
			s.indexByID[ComputeBotID(bc.Server, bc.Port, bc.Vhost, i)] = i
		}
	}
	s.registerRoutes()
	return s
}

// registerRoutes wires all method handlers.
func (s *Server) registerRoutes() {
	s.router.Register("auth.login", handleAuthLogin)
	s.router.Register("node.info", handleNodeInfo)
	s.router.Register("bot.list", handleBotList)
	s.router.Register("nicks.list", handleNicksList)
	s.router.Register("owners.list", handleOwnersList)
	s.router.Register("bot.say", handleBotSay)
	s.router.Register("bot.join", handleBotJoin)
	s.router.Register("bot.part", handleBotPart)
	s.router.Register("bot.quit", handleBotQuit)
	s.router.Register("bot.reconnect", handleBotReconnect)
	s.router.Register("bot.change_nick", handleBotChangeNick)
	s.router.Register("bot.raw", handleBotRaw)
	s.router.Register("node.mass_join", handleMassJoin)
	s.router.Register("node.mass_part", handleMassPart)
	s.router.Register("node.mass_reconnect", handleMassReconnect)
	s.router.Register("node.mass_raw", handleMassRaw)
	s.router.Register("node.mass_say", handleMassSay)
	s.router.Register("nicks.add", handleNicksAdd)
	s.router.Register("nicks.remove", handleNicksRemove)
	s.router.Register("owners.add", handleOwnersAdd)
	s.router.Register("owners.remove", handleOwnersRemove)
	s.router.Register("bnc.start", handleBNCStart)
	s.router.Register("bnc.stop", handleBNCStop)
	s.router.Register("events.subscribe", handleEventsSubscribe)
	s.router.Register("events.unsubscribe", handleEventsUnsubscribe)
	s.router.Register("bot.attach", handleBotAttach)
	s.router.Register("bot.detach", handleBotDetach)
	s.router.Register("watchdog.get", handleWatchdogGet)
	s.router.Register("watchdog.set", handleWatchdogSet)
	s.router.Register("humanlike.get", handleHumanlikeGet)
	s.router.Register("humanlike.set", handleHumanlikeSet)
}

// Hub returns the EventHub.
func (s *Server) Hub() *EventHub { return s.hub }

// AttachMgr returns the AttachManager.
func (s *Server) AttachMgr() *AttachManager { return s.attach }

// BotByID returns the live Bot matching the given bot_id, or nil.
// We iterate GetBots() and compare by Bot.GetBotID() — BotManager reorders
// (and occasionally shrinks) its bots slice at runtime, so a config-index
// lookup is unsafe after startup.
func (s *Server) BotByID(id string) types.Bot {
	if id == "" || s.deps.BotManager == nil {
		return nil
	}
	for _, b := range s.deps.BotManager.GetBots() {
		if b.GetBotID() == id {
			return b
		}
	}
	return nil
}

// configForBot returns the config slot that produced the given bot_id.
// Config ordering is stable, so this lookup is correct even after
// BotManager reorders its live bots slice.
func (s *Server) configForBot(id string) (config.BotConfig, bool) {
	if s.deps.Config == nil {
		return config.BotConfig{}, false
	}
	i, ok := s.indexByID[id]
	if !ok || i >= len(s.deps.Config.Bots) {
		return config.BotConfig{}, false
	}
	return s.deps.Config.Bots[i], true
}

// NewAttachEvent constructs an EventMsg with monotonic seq, reusing the hub's counter.
func (s *Server) NewAttachEvent(event string, data any) EventMsg {
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
	// Atomically reserve a connection slot (CAS loop). If the limit is
	// already reached, reject without upgrading.
	for {
		n := s.activeN.Load()
		if n >= int32(s.cfg.MaxConnections) {
			http.Error(w, "too many connections", http.StatusServiceUnavailable)
			return
		}
		if s.activeN.CompareAndSwap(n, n+1) {
			break
		}
	}
	defer s.activeN.Add(-1)

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
