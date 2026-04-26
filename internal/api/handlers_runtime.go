package api

import (
	"context"

	"github.com/kofany/gNb/internal/runtime"
)

// watchdogSetParams accepts the full new state (replace, not patch).
// Channel is allowed to be empty: that means "use the bot's configured
// channel list", which is the watchdog's pre-runtime-state default.
type watchdogSetParams struct {
	Enabled bool   `json:"enabled"`
	Channel string `json:"channel"`
}

// humanlikeSetParams accepts the full new state. Channels is space-separated
// per the panel design — one input field, one save button.
type humanlikeSetParams struct {
	Enabled  bool   `json:"enabled"`
	Channels string `json:"channels"`
}

// watchdogResponse mirrors what handlers return on get/set so the panel can
// render the current state without an extra round-trip.
type watchdogResponse struct {
	Enabled bool   `json:"enabled"`
	Channel string `json:"channel"`
}

type humanlikeResponse struct {
	Enabled  bool     `json:"enabled"`
	Channels []string `json:"channels"`
}

func runtimeUnavailable() *HandlerError {
	return notFound("runtime state unavailable")
}

func handleWatchdogGet(_ context.Context, s *Session, _ *RequestMsg) (any, *HandlerError) {
	rt := s.server.deps.Runtime
	if rt == nil {
		return nil, runtimeUnavailable()
	}
	wd := rt.Watchdog()
	return watchdogResponse{Enabled: wd.Enabled, Channel: wd.Channel}, nil
}

func handleWatchdogSet(_ context.Context, s *Session, req *RequestMsg) (any, *HandlerError) {
	rt := s.server.deps.Runtime
	if rt == nil {
		return nil, runtimeUnavailable()
	}
	var p watchdogSetParams
	if e := decodeParams(req, &p); e != nil {
		return nil, e
	}
	cfg := runtime.WatchdogConfig{Enabled: p.Enabled, Channel: p.Channel}
	if err := rt.SetWatchdog(cfg); err != nil {
		return nil, paramErr("%v", err)
	}
	now := rt.Watchdog()
	s.server.hub.Publish("runtime.watchdog_changed", map[string]any{
		"enabled": now.Enabled,
		"channel": now.Channel,
	})
	return watchdogResponse{Enabled: now.Enabled, Channel: now.Channel}, nil
}

func handleHumanlikeGet(_ context.Context, s *Session, _ *RequestMsg) (any, *HandlerError) {
	rt := s.server.deps.Runtime
	if rt == nil {
		return nil, runtimeUnavailable()
	}
	hl := rt.Humanlike()
	return humanlikeResponse{Enabled: hl.Enabled, Channels: hl.Channels}, nil
}

func handleHumanlikeSet(_ context.Context, s *Session, req *RequestMsg) (any, *HandlerError) {
	rt := s.server.deps.Runtime
	if rt == nil {
		return nil, runtimeUnavailable()
	}
	var p humanlikeSetParams
	if e := decodeParams(req, &p); e != nil {
		return nil, e
	}
	cfg := runtime.HumanlikeConfig{
		Enabled:  p.Enabled,
		Channels: runtime.SplitChannelList(p.Channels),
	}
	if err := rt.SetHumanlike(cfg); err != nil {
		return nil, paramErr("%v", err)
	}
	now := rt.Humanlike()
	s.server.hub.Publish("runtime.humanlike_changed", map[string]any{
		"enabled":  now.Enabled,
		"channels": now.Channels,
	})
	return humanlikeResponse{Enabled: now.Enabled, Channels: now.Channels}, nil
}
