package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"

	"github.com/kofany/gNb/internal/config"
	"github.com/kofany/gNb/internal/runtime"
	"github.com/kofany/gNb/internal/types"
)

// runtimeTestServer wires a server backed by a real RuntimeState rooted in a
// per-test temp dir so we can exercise persistence without leaking files.
func runtimeTestServer(t *testing.T) (*Server, *httptest.Server, *runtime.RuntimeState) {
	t.Helper()
	rs, err := runtime.LoadOrCreate(filepath.Join(t.TempDir(), "runtime.json"))
	if err != nil {
		t.Fatalf("LoadOrCreate: %v", err)
	}
	apiCfg := config.APIConfig{
		Enabled:        true,
		AuthToken:      strings.Repeat("x", 32),
		BindAddr:       "127.0.0.1:0",
		EventBuffer:    100,
		MaxConnections: 4,
		NodeName:       "test-node",
	}
	apiCfg.ApplyDefaults()
	fb := &fakeBot{
		botID:     ComputeBotID("irc.example", 6667, "v", 0),
		nick:      "a",
		connected: true,
		server:    "irc.example",
	}
	cfgFull := &config.Config{
		Bots:     []config.BotConfig{{Server: "irc.example", Port: 6667, SSL: false, Vhost: "v"}},
		Channels: []string{"#x"},
		API:      apiCfg,
	}
	srv := New(apiCfg, "testnode", Deps{
		Config:      cfgFull,
		BotManager:  &fakeBotManager{bots: []types.Bot{fb}},
		NickManager: &fakeNickManager{},
		Runtime:     rs,
	})
	ts := httptest.NewServer(http.HandlerFunc(srv.handleWS))
	return srv, ts, rs
}

func writeRead(t *testing.T, ctx context.Context, c *websocket.Conn, msg map[string]any) map[string]any {
	t.Helper()
	if err := wsjson.Write(ctx, c, msg); err != nil {
		t.Fatalf("write: %v", err)
	}
	var resp map[string]any
	rctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	if err := wsjson.Read(rctx, c, &resp); err != nil {
		t.Fatalf("read: %v", err)
	}
	return resp
}

func TestWatchdogGetSetRoundtrip(t *testing.T) {
	_, ts, rs := runtimeTestServer(t)
	defer ts.Close()
	c := authed(t, ts)
	defer c.Close(websocket.StatusNormalClosure, "")
	ctx := context.Background()

	// initial get returns defaults
	resp := writeRead(t, ctx, c, map[string]any{"type": "request", "id": "1", "method": "watchdog.get"})
	r := resp["result"].(map[string]any)
	if r["enabled"] != false || r["channel"] != "" {
		t.Fatalf("watchdog.get default: %+v", r)
	}

	// set new state
	resp = writeRead(t, ctx, c, map[string]any{
		"type": "request", "id": "2", "method": "watchdog.set",
		"params": map[string]any{"enabled": true, "channel": "#foo"},
	})
	if resp["type"] != "response" {
		t.Fatalf("watchdog.set: %+v", resp)
	}

	// get reflects update
	resp = writeRead(t, ctx, c, map[string]any{"type": "request", "id": "3", "method": "watchdog.get"})
	r = resp["result"].(map[string]any)
	if r["enabled"] != true || r["channel"] != "#foo" {
		t.Fatalf("watchdog.get post-set: %+v", r)
	}

	// runtime state authoritative copy matches
	if got := rs.Watchdog(); !got.Enabled || got.Channel != "#foo" {
		t.Fatalf("runtime authoritative: %+v", got)
	}
}

func TestWatchdogSet_RejectsBadChannel(t *testing.T) {
	_, ts, _ := runtimeTestServer(t)
	defer ts.Close()
	c := authed(t, ts)
	defer c.Close(websocket.StatusNormalClosure, "")
	ctx := context.Background()
	resp := writeRead(t, ctx, c, map[string]any{
		"type": "request", "id": "1", "method": "watchdog.set",
		"params": map[string]any{"enabled": true, "channel": "no-prefix"},
	})
	if resp["type"] != "error" || resp["code"] != "invalid_params" {
		t.Fatalf("expected invalid_params, got %+v", resp)
	}
}

func TestHumanlikeGetSetRoundtrip(t *testing.T) {
	_, ts, rs := runtimeTestServer(t)
	defer ts.Close()
	c := authed(t, ts)
	defer c.Close(websocket.StatusNormalClosure, "")
	ctx := context.Background()

	// default
	resp := writeRead(t, ctx, c, map[string]any{"type": "request", "id": "1", "method": "humanlike.get"})
	r := resp["result"].(map[string]any)
	if r["enabled"] != false {
		t.Fatalf("default not disabled: %+v", r)
	}
	chs := r["channels"].([]any)
	if len(chs) != 0 {
		t.Fatalf("default channels not empty: %+v", chs)
	}

	// set with space-separated string
	resp = writeRead(t, ctx, c, map[string]any{
		"type": "request", "id": "2", "method": "humanlike.set",
		"params": map[string]any{"enabled": true, "channels": "#a #b #c"},
	})
	if resp["type"] != "response" {
		t.Fatalf("humanlike.set: %+v", resp)
	}

	// reflect
	resp = writeRead(t, ctx, c, map[string]any{"type": "request", "id": "3", "method": "humanlike.get"})
	r = resp["result"].(map[string]any)
	if r["enabled"] != true {
		t.Fatalf("not enabled: %+v", r)
	}
	chs = r["channels"].([]any)
	if len(chs) != 3 || chs[0] != "#a" || chs[1] != "#b" || chs[2] != "#c" {
		t.Fatalf("channels round-trip: %+v", chs)
	}

	if got := rs.Humanlike().Channels; len(got) != 3 {
		t.Fatalf("runtime authoritative: %+v", got)
	}
}

func TestHumanlikeSet_RejectsBadChannel(t *testing.T) {
	_, ts, _ := runtimeTestServer(t)
	defer ts.Close()
	c := authed(t, ts)
	defer c.Close(websocket.StatusNormalClosure, "")
	ctx := context.Background()
	resp := writeRead(t, ctx, c, map[string]any{
		"type": "request", "id": "1", "method": "humanlike.set",
		"params": map[string]any{"enabled": false, "channels": "#ok bad #also-ok"},
	})
	if resp["type"] != "error" || resp["code"] != "invalid_params" {
		t.Fatalf("expected invalid_params, got %+v", resp)
	}
}

func TestRuntimeChangeEvents_Emitted(t *testing.T) {
	_, ts, _ := runtimeTestServer(t)
	defer ts.Close()
	c := authed(t, ts)
	defer c.Close(websocket.StatusNormalClosure, "")
	ctx := context.Background()

	// subscribe
	resp := writeRead(t, ctx, c, map[string]any{
		"type": "request", "id": "sub", "method": "events.subscribe",
		"params": map[string]any{
			"topics":      []string{"runtime.watchdog_changed", "runtime.humanlike_changed"},
			"replay_last": 0,
		},
	})
	if resp["type"] != "response" {
		t.Fatalf("subscribe: %+v", resp)
	}

	// trigger watchdog_changed
	resp = writeRead(t, ctx, c, map[string]any{
		"type": "request", "id": "1", "method": "watchdog.set",
		"params": map[string]any{"enabled": true, "channel": "#x"},
	})
	if resp["type"] != "response" {
		t.Fatalf("watchdog.set: %+v", resp)
	}
	var ev map[string]any
	rctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	if err := wsjson.Read(rctx, c, &ev); err != nil {
		cancel()
		t.Fatalf("read event: %v", err)
	}
	cancel()
	if ev["type"] != "event" || ev["event"] != "runtime.watchdog_changed" {
		t.Fatalf("expected watchdog event, got %+v", ev)
	}

	// trigger humanlike_changed
	resp = writeRead(t, ctx, c, map[string]any{
		"type": "request", "id": "2", "method": "humanlike.set",
		"params": map[string]any{"enabled": false, "channels": "#a"},
	})
	if resp["type"] != "response" {
		t.Fatalf("humanlike.set: %+v", resp)
	}
	rctx2, cancel2 := context.WithTimeout(ctx, 2*time.Second)
	if err := wsjson.Read(rctx2, c, &ev); err != nil {
		cancel2()
		t.Fatalf("read event2: %v", err)
	}
	cancel2()
	if ev["event"] != "runtime.humanlike_changed" {
		t.Fatalf("expected humanlike event, got %+v", ev)
	}
}

func TestRuntimeMethodsRequireAuth(t *testing.T) {
	_, ts, _ := runtimeTestServer(t)
	defer ts.Close()
	c := wsDial(t, ts.URL)
	defer c.Close(websocket.StatusNormalClosure, "")
	ctx := context.Background()
	resp := writeRead(t, ctx, c, map[string]any{"type": "request", "id": "1", "method": "watchdog.get"})
	if resp["type"] != "error" || resp["code"] != "forbidden" {
		t.Fatalf("expected forbidden, got %+v", resp)
	}
}

func TestRuntimeMethods_NoState_ReturnNotFound(t *testing.T) {
	// When the operator hasn't wired Runtime into Deps the handlers must
	// degrade gracefully rather than nil-panic.
	apiCfg := config.APIConfig{
		Enabled: true, AuthToken: strings.Repeat("x", 32), BindAddr: "127.0.0.1:0",
		EventBuffer: 100, MaxConnections: 4, NodeName: "test-node",
	}
	apiCfg.ApplyDefaults()
	srv := New(apiCfg, "n", Deps{
		Config:      &config.Config{API: apiCfg},
		BotManager:  &fakeBotManager{},
		NickManager: &fakeNickManager{},
	})
	ts := httptest.NewServer(http.HandlerFunc(srv.handleWS))
	defer ts.Close()
	c := authed(t, ts)
	defer c.Close(websocket.StatusNormalClosure, "")
	ctx := context.Background()
	resp := writeRead(t, ctx, c, map[string]any{"type": "request", "id": "1", "method": "watchdog.get"})
	if resp["type"] != "error" || resp["code"] != "not_found" {
		t.Fatalf("expected not_found, got %+v", resp)
	}
}
