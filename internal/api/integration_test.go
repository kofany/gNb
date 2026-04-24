package api

import (
	"context"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"
)

func TestEndToEndLifecycleFlow(t *testing.T) {
	srv, ts, _ := testServer(t)
	defer ts.Close()
	c := authed(t, ts)
	defer c.Close(websocket.StatusNormalClosure, "")
	ctx := context.Background()

	if err := wsjson.Write(ctx, c, map[string]any{
		"type": "request", "id": "sub", "method": "events.subscribe",
		"params": map[string]any{"replay_last": 0},
	}); err != nil {
		t.Fatal(err)
	}
	var resp map[string]any
	rctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	_ = wsjson.Read(rctx, c, &resp)
	if resp["type"] != "response" {
		t.Fatalf("sub failed: %+v", resp)
	}

	sink := srv.Sink()
	sink.BotConnected("abc", "bot1", "irc.example")
	sink.BotNickChanged("abc", "bot1", "a")
	sink.BotNickCaptured("abc", "a", "letter")

	want := []string{"bot.connected", "bot.nick_changed", "bot.nick_captured"}
	for _, ev := range want {
		var m map[string]any
		rctx2, cancel2 := context.WithTimeout(ctx, 2*time.Second)
		err := wsjson.Read(rctx2, c, &m)
		cancel2()
		if err != nil {
			t.Fatalf("reading %s: %v", ev, err)
		}
		if m["event"] != ev {
			t.Fatalf("want %s, got %v", ev, m["event"])
		}
		if m["node_id"] != "testnode" {
			t.Fatalf("want node_id=testnode, got %v", m["node_id"])
		}
	}
}
