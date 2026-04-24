package api

import (
	"context"
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

func TestSinkPublishesLifecycleEvents(t *testing.T) {
	srv, _, _ := testServer(t)
	sub := srv.hub.Subscribe(nil, 16)
	defer srv.hub.Unsubscribe(sub)

	sink := srv.Sink()
	sink.BotConnected("b1", "bot1", "irc.x")
	msg := <-sub.Ch()
	if msg.Event != "bot.connected" {
		t.Fatalf("bad: %s", msg.Event)
	}

	sink.BotNickChanged("b1", "old", "new")
	msg = <-sub.Ch()
	if msg.Event != "bot.nick_changed" {
		t.Fatalf("bad: %s", msg.Event)
	}
	d := msg.Data.(map[string]any)
	if d["old"] != "old" || d["new"] != "new" {
		t.Fatalf("bad payload: %+v", d)
	}

	sink.BotAdded("b1", "irc.x", 6667, true, "vh")
	msg = <-sub.Ch()
	if msg.Event != "node.bot_added" {
		t.Fatalf("bad: %s", msg.Event)
	}

	sink.NicksChanged([]string{"foo", "bar"})
	msg = <-sub.Ch()
	if msg.Event != "nicks.changed" {
		t.Fatalf("bad: %s", msg.Event)
	}

	sink.OwnersChanged([]string{"*!*@host"})
	msg = <-sub.Ch()
	if msg.Event != "owners.changed" {
		t.Fatalf("bad: %s", msg.Event)
	}

	sink.BotRemoved("b1")
	msg = <-sub.Ch()
	if msg.Event != "node.bot_removed" {
		t.Fatalf("bad: %s", msg.Event)
	}
}

func TestBNCSSHCommandBracketsIPv6(t *testing.T) {
	// Use the standard test server but swap the config to an IPv6 vhost.
	apiCfg := config.APIConfig{
		Enabled:        true,
		AuthToken:      strings.Repeat("x", 32),
		BindAddr:       "127.0.0.1:0",
		EventBuffer:    100,
		MaxConnections: 4,
		NodeName:       "test-node",
	}
	apiCfg.ApplyDefaults()
	cfg := &config.Config{
		Bots:     []config.BotConfig{{Server: "irc.example", Port: 6667, Vhost: "2001:db8::1"}},
		Channels: []string{"#x"},
		API:      apiCfg,
	}
	botID := ComputeBotID("irc.example", 6667, "2001:db8::1", 0)
	fb := &fakeBot{botID: botID, nick: "a", connected: true, server: "irc.example", bncPort: 4242, bncPass: "pw"}
	srv := New(apiCfg, "testnode", Deps{Config: cfg, BotManager: &fakeBotManager{bots: []types.Bot{fb}}, NickManager: &fakeNickManager{}})
	ts := httptest.NewServer(http.HandlerFunc(srv.handleWS))
	defer ts.Close()

	c := authed(t, ts)
	defer c.Close(websocket.StatusNormalClosure, "")
	ctx := context.Background()
	_ = wsjson.Write(ctx, c, map[string]any{
		"type": "request", "id": "1", "method": "bnc.start",
		"params": map[string]string{"bot_id": botID},
	})
	var resp map[string]any
	rctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	_ = wsjson.Read(rctx, c, &resp)
	r := resp["result"].(map[string]any)
	sshCmd := r["ssh_command"].(string)
	if !strings.Contains(sshCmd, "[2001:db8::1]") {
		t.Fatalf("IPv6 not bracketed in ssh_command: %q", sshCmd)
	}
}
