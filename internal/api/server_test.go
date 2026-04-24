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

	fb := &fakeBot{
		botID:     ComputeBotID("irc.example", 6667, "v", 0),
		nick:      "a",
		connected: true,
		server:    "irc.example",
		channels:  []string{"#x"},
	}
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

func TestBotRaw(t *testing.T) {
	_, ts, fbm := testServer(t)
	defer ts.Close()
	c := authed(t, ts)
	defer c.Close(websocket.StatusNormalClosure, "")
	botID := ComputeBotID("irc.example", 6667, "v", 0)
	ctx := context.Background()
	_ = wsjson.Write(ctx, c, map[string]interface{}{
		"type": "request", "id": "1", "method": "bot.raw",
		"params": map[string]string{"bot_id": botID, "line": "WHO foo\r\nEXTRA"},
	})
	var resp map[string]interface{}
	rctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	_ = wsjson.Read(rctx, c, &resp)
	if resp["type"] != "response" {
		t.Fatalf("bad: %+v", resp)
	}
	fb := fbm.bots[0].(*fakeBot)
	fb.mu.Lock()
	defer fb.mu.Unlock()
	if len(fb.raw) != 1 {
		t.Fatalf("want 1 raw line, got %d", len(fb.raw))
	}
	// CR/LF must be stripped.
	if fb.raw[0] != "WHO fooEXTRA" {
		t.Fatalf("raw not sanitized: %q", fb.raw[0])
	}
}

func TestBotChangeNickPropagates(t *testing.T) {
	_, ts, fbm := testServer(t)
	defer ts.Close()
	c := authed(t, ts)
	defer c.Close(websocket.StatusNormalClosure, "")
	botID := ComputeBotID("irc.example", 6667, "v", 0)
	ctx := context.Background()
	_ = wsjson.Write(ctx, c, map[string]interface{}{
		"type": "request", "id": "1", "method": "bot.change_nick",
		"params": map[string]string{"bot_id": botID, "new_nick": "zz"},
	})
	var resp map[string]interface{}
	rctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	_ = wsjson.Read(rctx, c, &resp)
	if resp["type"] != "response" {
		t.Fatalf("bad: %+v", resp)
	}
	fb := fbm.bots[0].(*fakeBot)
	fb.mu.Lock()
	defer fb.mu.Unlock()
	if fb.newNick != "zz" {
		t.Fatalf("want newNick=zz, got %q", fb.newNick)
	}
}

func TestBotNotFound(t *testing.T) {
	_, ts, _ := testServer(t)
	defer ts.Close()
	c := authed(t, ts)
	defer c.Close(websocket.StatusNormalClosure, "")
	ctx := context.Background()
	_ = wsjson.Write(ctx, c, map[string]interface{}{
		"type": "request", "id": "1", "method": "bot.raw",
		"params": map[string]string{"bot_id": "nonexistent", "line": "WHO"},
	})
	var resp map[string]interface{}
	rctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	_ = wsjson.Read(rctx, c, &resp)
	if resp["type"] != "error" || resp["code"] != "not_found" {
		t.Fatalf("want not_found error, got %+v", resp)
	}
}

func TestMassRawBroadcasts(t *testing.T) {
	_, ts, fbm := testServer(t)
	defer ts.Close()
	fbm.bots = append(fbm.bots, &fakeBot{nick: "b", connected: true, server: "irc.example"})
	c := authed(t, ts)
	defer c.Close(websocket.StatusNormalClosure, "")
	ctx := context.Background()
	_ = wsjson.Write(ctx, c, map[string]interface{}{
		"type": "request", "id": "1", "method": "node.mass_raw",
		"params": map[string]string{"line": "PING :x"},
	})
	var resp map[string]interface{}
	rctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	_ = wsjson.Read(rctx, c, &resp)
	r := resp["result"].(map[string]interface{})
	if int(r["affected"].(float64)) != 2 {
		t.Fatalf("want affected=2, got %v", r["affected"])
	}
	for _, b := range fbm.bots {
		fb := b.(*fakeBot)
		fb.mu.Lock()
		if len(fb.raw) != 1 || fb.raw[0] != "PING :x" {
			t.Errorf("bot raw not delivered: %+v", fb.raw)
		}
		fb.mu.Unlock()
	}
}

func TestMassJoinCooldown(t *testing.T) {
	_, ts, fbm := testServer(t)
	defer ts.Close()
	fbm.massBlocked = map[string]bool{"join": true}
	c := authed(t, ts)
	defer c.Close(websocket.StatusNormalClosure, "")
	ctx := context.Background()
	_ = wsjson.Write(ctx, c, map[string]interface{}{
		"type": "request", "id": "1", "method": "node.mass_join",
		"params": map[string]string{"channel": "#x"},
	})
	var resp map[string]interface{}
	rctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	_ = wsjson.Read(rctx, c, &resp)
	if resp["type"] != "error" || resp["code"] != "cooldown" {
		t.Fatalf("want cooldown error, got %+v", resp)
	}
}

func TestNicksAddAndOwnersAdd(t *testing.T) {
	_, ts, fbm := testServer(t)
	defer ts.Close()
	c := authed(t, ts)
	defer c.Close(websocket.StatusNormalClosure, "")
	ctx := context.Background()

	_ = wsjson.Write(ctx, c, map[string]interface{}{
		"type": "request", "id": "1", "method": "nicks.add",
		"params": map[string]string{"nick": "newnick"},
	})
	var resp map[string]interface{}
	rctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	_ = wsjson.Read(rctx, c, &resp)
	cancel()
	if resp["type"] != "response" {
		t.Fatalf("nicks.add: %+v", resp)
	}

	_ = wsjson.Write(ctx, c, map[string]interface{}{
		"type": "request", "id": "2", "method": "owners.add",
		"params": map[string]string{"mask": "*!*foo@bar"},
	})
	rctx2, cancel2 := context.WithTimeout(ctx, 2*time.Second)
	_ = wsjson.Read(rctx2, c, &resp)
	cancel2()
	if resp["type"] != "response" {
		t.Fatalf("owners.add: %+v", resp)
	}
	if len(fbm.owners) != 1 || fbm.owners[0] != "*!*foo@bar" {
		t.Fatalf("owners not tracked: %+v", fbm.owners)
	}
}

func TestBNCStart(t *testing.T) {
	_, ts, fbm := testServer(t)
	defer ts.Close()
	fb := fbm.bots[0].(*fakeBot)
	fb.mu.Lock()
	fb.bncPort = 4242
	fb.bncPass = "pass"
	fb.mu.Unlock()

	c := authed(t, ts)
	defer c.Close(websocket.StatusNormalClosure, "")
	botID := ComputeBotID("irc.example", 6667, "v", 0)
	ctx := context.Background()
	_ = wsjson.Write(ctx, c, map[string]interface{}{
		"type": "request", "id": "1", "method": "bnc.start",
		"params": map[string]string{"bot_id": botID},
	})
	var resp map[string]interface{}
	rctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	_ = wsjson.Read(rctx, c, &resp)
	if resp["type"] != "response" {
		t.Fatalf("bad: %+v", resp)
	}
	r := resp["result"].(map[string]interface{})
	if int(r["port"].(float64)) != 4242 || r["password"] != "pass" {
		t.Fatalf("bad bnc result: %+v", r)
	}
}

func TestEventsSubscribeAndReceive(t *testing.T) {
	srv, ts, _ := testServer(t)
	defer ts.Close()
	c := authed(t, ts)
	defer c.Close(websocket.StatusNormalClosure, "")
	ctx := context.Background()
	_ = wsjson.Write(ctx, c, map[string]interface{}{
		"type": "request", "id": "1", "method": "events.subscribe",
		"params": map[string]interface{}{"topics": []string{"bot.connected"}, "replay_last": 0},
	})
	var resp map[string]interface{}
	rctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	_ = wsjson.Read(rctx, c, &resp)
	if resp["type"] != "response" {
		t.Fatalf("bad: %+v", resp)
	}

	srv.hub.Publish("bot.connected", map[string]string{"bot_id": "x"})
	var ev map[string]interface{}
	rctx2, cancel2 := context.WithTimeout(ctx, 2*time.Second)
	defer cancel2()
	_ = wsjson.Read(rctx2, c, &ev)
	if ev["type"] != "event" || ev["event"] != "bot.connected" {
		t.Fatalf("bad event: %+v", ev)
	}
}

func TestEventsReplay(t *testing.T) {
	srv, ts, _ := testServer(t)
	defer ts.Close()
	for i := 0; i < 3; i++ {
		srv.hub.Publish("bot.connected", map[string]int{"i": i})
	}
	c := authed(t, ts)
	defer c.Close(websocket.StatusNormalClosure, "")
	ctx := context.Background()
	_ = wsjson.Write(ctx, c, map[string]interface{}{
		"type": "request", "id": "1", "method": "events.subscribe",
		"params": map[string]interface{}{"replay_last": 2},
	})
	seen := map[string]int{}
	for i := 0; i < 3; i++ {
		var m map[string]interface{}
		rctx, cancel := context.WithTimeout(ctx, 2*time.Second)
		if err := wsjson.Read(rctx, c, &m); err != nil {
			cancel()
			t.Fatal(err)
		}
		cancel()
		seen[m["type"].(string)]++
	}
	if seen["response"] != 1 || seen["event"] != 2 {
		t.Fatalf("bad breakdown: %+v", seen)
	}
}

func TestBotAttachReceivesEvent(t *testing.T) {
	srv, ts, _ := testServer(t)
	defer ts.Close()
	c := authed(t, ts)
	defer c.Close(websocket.StatusNormalClosure, "")
	botID := ComputeBotID("irc.example", 6667, "v", 0)
	ctx := context.Background()
	_ = wsjson.Write(ctx, c, map[string]interface{}{
		"type": "request", "id": "1", "method": "bot.attach",
		"params": map[string]string{"bot_id": botID},
	})
	var resp map[string]interface{}
	rctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	_ = wsjson.Read(rctx, c, &resp)
	if resp["type"] != "response" {
		t.Fatalf("bad: %+v", resp)
	}
	srv.attach.Publish(srv, botID, srv.NewAttachEvent("bot.attach.privmsg", map[string]string{"target": "#c", "text": "hi"}))
	var ev map[string]interface{}
	rctx2, cancel2 := context.WithTimeout(ctx, 2*time.Second)
	defer cancel2()
	_ = wsjson.Read(rctx2, c, &ev)
	if ev["event"] != "bot.attach.privmsg" {
		t.Fatalf("bad event: %+v", ev)
	}
}

func TestBotDetachStopsDelivery(t *testing.T) {
	srv, ts, _ := testServer(t)
	defer ts.Close()
	c := authed(t, ts)
	defer c.Close(websocket.StatusNormalClosure, "")
	botID := ComputeBotID("irc.example", 6667, "v", 0)
	ctx := context.Background()
	_ = wsjson.Write(ctx, c, map[string]interface{}{
		"type": "request", "id": "1", "method": "bot.attach",
		"params": map[string]string{"bot_id": botID},
	})
	var resp map[string]interface{}
	rctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	_ = wsjson.Read(rctx, c, &resp)
	cancel()

	_ = wsjson.Write(ctx, c, map[string]interface{}{
		"type": "request", "id": "2", "method": "bot.detach",
		"params": map[string]string{"bot_id": botID},
	})
	rctx2, cancel2 := context.WithTimeout(ctx, 2*time.Second)
	_ = wsjson.Read(rctx2, c, &resp)
	cancel2()
	if resp["type"] != "response" {
		t.Fatalf("detach bad: %+v", resp)
	}

	// Publish after detach — must not deliver; expect read timeout.
	srv.attach.Publish(srv, botID, srv.NewAttachEvent("bot.attach.privmsg", nil))
	var ev map[string]interface{}
	rctx3, cancel3 := context.WithTimeout(ctx, 500*time.Millisecond)
	defer cancel3()
	if err := wsjson.Read(rctx3, c, &ev); err == nil {
		t.Fatalf("expected timeout, got event: %+v", ev)
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
