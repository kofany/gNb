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

func TestNodeInfoRequiresAuth(t *testing.T) {
	_, ts, _ := testServer(t)
	defer ts.Close()
	c := wsDial(t, ts.URL)
	defer c.Close(websocket.StatusNormalClosure, "")

	ctx := context.Background()
	req := map[string]interface{}{"type": "request", "id": "1", "method": "node.info"}
	_ = wsjson.Write(ctx, c, req)
	var got map[string]interface{}
	rctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	_ = wsjson.Read(rctx, c, &got)
	if got["type"] != "error" || got["code"] != "forbidden" {
		t.Fatalf("want forbidden, got %+v", got)
	}
}

func TestNodeInfoAfterAuth(t *testing.T) {
	_, ts, _ := testServer(t)
	defer ts.Close()
	c := authed(t, ts)
	defer c.Close(websocket.StatusNormalClosure, "")

	ctx := context.Background()
	_ = wsjson.Write(ctx, c, map[string]interface{}{"type": "request", "id": "2", "method": "node.info"})
	var resp map[string]interface{}
	rctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	_ = wsjson.Read(rctx, c, &resp)
	if resp["type"] != "response" {
		t.Fatalf("want response, got %+v", resp)
	}
	result := resp["result"].(map[string]interface{})
	if result["node_name"] != "test-node" {
		t.Fatalf("bad result: %+v", result)
	}
	if int(result["num_bots"].(float64)) != 1 {
		t.Fatalf("num_bots: %+v", result)
	}
}

func TestBotList(t *testing.T) {
	_, ts, _ := testServer(t)
	defer ts.Close()
	c := authed(t, ts)
	defer c.Close(websocket.StatusNormalClosure, "")
	ctx := context.Background()
	_ = wsjson.Write(ctx, c, map[string]interface{}{"type": "request", "id": "2", "method": "bot.list"})
	var resp map[string]interface{}
	rctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	_ = wsjson.Read(rctx, c, &resp)
	r := resp["result"].(map[string]interface{})
	bots := r["bots"].([]interface{})
	if len(bots) != 1 {
		t.Fatalf("want 1 bot, got %d", len(bots))
	}
	b0 := bots[0].(map[string]interface{})
	if b0["current_nick"] != "a" {
		t.Fatalf("bad nick: %+v", b0)
	}
	if b0["bot_id"] == "" {
		t.Fatalf("empty bot_id")
	}
	chs := b0["joined_channels"].([]interface{})
	if len(chs) != 1 || chs[0] != "#x" {
		t.Fatalf("joined_channels: %+v", chs)
	}
}

func TestNicksListAfterAuth(t *testing.T) {
	_, ts, _ := testServer(t)
	defer ts.Close()
	c := authed(t, ts)
	defer c.Close(websocket.StatusNormalClosure, "")
	ctx := context.Background()
	_ = wsjson.Write(ctx, c, map[string]interface{}{"type": "request", "id": "2", "method": "nicks.list"})
	var resp map[string]interface{}
	rctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	_ = wsjson.Read(rctx, c, &resp)
	r := resp["result"].(map[string]interface{})
	nicks := r["nicks"].([]interface{})
	if len(nicks) != 1 || nicks[0] != "foo" {
		t.Fatalf("nicks: %+v", nicks)
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
