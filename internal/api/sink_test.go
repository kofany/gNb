package api

import "testing"

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
}
