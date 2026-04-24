package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"

	"github.com/kofany/gNb/internal/config"
	"github.com/kofany/gNb/internal/types"
)

func testServer(t *testing.T) (*Server, *httptest.Server, *fakeBotManager) {
	t.Helper()
	apiCfg := config.APIConfig{
		Enabled:        true,
		AuthToken:      strings.Repeat("x", 32),
		BindAddr:       "127.0.0.1:0",
		EventBuffer:    100,
		MaxConnections: 4,
		NodeName:       "test-node",
	}
	apiCfg.ApplyDefaults()

	fb := &fakeBot{nick: "a", connected: true, server: "irc.example", channels: []string{"#x"}}
	fbm := &fakeBotManager{bots: []types.Bot{fb}}

	cfgFull := &config.Config{
		Bots:     []config.BotConfig{{Server: "irc.example", Port: 6667, SSL: false, Vhost: "v"}},
		Channels: []string{"#x"},
		API:      apiCfg,
	}
	s := New(apiCfg, "testnode", Deps{
		Config:      cfgFull,
		BotManager:  fbm,
		NickManager: &fakeNickManager{nicks: []string{"foo"}},
	})
	ts := httptest.NewServer(http.HandlerFunc(s.handleWS))
	return s, ts, fbm
}

func wsDial(t *testing.T, url string) *websocket.Conn {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	c, _, err := websocket.Dial(ctx, strings.Replace(url, "http://", "ws://", 1), nil)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	return c
}

func authed(t *testing.T, ts *httptest.Server) *websocket.Conn {
	t.Helper()
	c := wsDial(t, ts.URL)
	ctx := context.Background()
	_ = wsjson.Write(ctx, c, map[string]interface{}{
		"type": "request", "id": "login", "method": "auth.login",
		"params": map[string]string{"token": strings.Repeat("x", 32)},
	})
	var resp map[string]interface{}
	rctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	_ = wsjson.Read(rctx, c, &resp)
	if resp["type"] != "response" {
		t.Fatalf("handshake failed: %+v", resp)
	}
	return c
}

func TestHandshakeSuccess(t *testing.T) {
	_, ts, _ := testServer(t)
	defer ts.Close()
	c := wsDial(t, ts.URL)
	defer c.Close(websocket.StatusNormalClosure, "")

	ctx := context.Background()
	req := map[string]interface{}{
		"type":   "request",
		"id":     "1",
		"method": "auth.login",
		"params": map[string]string{"token": strings.Repeat("x", 32)},
	}
	if err := wsjson.Write(ctx, c, req); err != nil {
		t.Fatal(err)
	}
	var got map[string]interface{}
	rctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	if err := wsjson.Read(rctx, c, &got); err != nil {
		t.Fatal(err)
	}
	if got["type"] != "response" || got["ok"] != true {
		b, _ := json.Marshal(got)
		t.Fatalf("bad response: %s", b)
	}
}

func TestHandshakeBadToken(t *testing.T) {
	_, ts, _ := testServer(t)
	defer ts.Close()
	c := wsDial(t, ts.URL)
	defer c.Close(websocket.StatusNormalClosure, "")

	ctx := context.Background()
	req := map[string]interface{}{
		"type":   "request",
		"id":     "1",
		"method": "auth.login",
		"params": map[string]string{"token": "wrong"},
	}
	_ = wsjson.Write(ctx, c, req)
	var got map[string]interface{}
	rctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	_ = wsjson.Read(rctx, c, &got)
	if got["type"] != "error" {
		t.Fatalf("want error, got %+v", got)
	}
	if got["code"] != "unauthorized" {
		t.Fatalf("want code=unauthorized, got %v", got["code"])
	}
}

func TestHandshakeTimeout(t *testing.T) {
	_, ts, _ := testServer(t)
	defer ts.Close()
	c := wsDial(t, ts.URL)
	defer c.Close(websocket.StatusNormalClosure, "")

	ctx, cancel := context.WithTimeout(context.Background(), 7*time.Second)
	defer cancel()
	var got map[string]interface{}
	err := wsjson.Read(ctx, c, &got)
	if err == nil {
		t.Fatalf("want close, got message: %+v", got)
	}
}
