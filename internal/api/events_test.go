package api

import (
	"sync"
	"testing"
	"time"
)

func TestEventHubPublishAndSubscribe(t *testing.T) {
	h := NewEventHub("n1", 10)
	sub := h.Subscribe(nil, 4)
	defer h.Unsubscribe(sub)

	h.Publish("bot.connected", map[string]string{"bot_id": "a"})
	select {
	case msg := <-sub.Ch():
		if msg.Event != "bot.connected" {
			t.Fatalf("want bot.connected, got %s", msg.Event)
		}
		if msg.Seq != 1 {
			t.Fatalf("want seq=1, got %d", msg.Seq)
		}
	case <-time.After(time.Second):
		t.Fatal("timeout waiting for event")
	}
}

func TestEventHubTopicFilter(t *testing.T) {
	h := NewEventHub("n1", 10)
	sub := h.Subscribe([]string{"bot.connected"}, 4)
	defer h.Unsubscribe(sub)

	h.Publish("bot.disconnected", nil)
	h.Publish("bot.connected", nil)

	select {
	case msg := <-sub.Ch():
		if msg.Event != "bot.connected" {
			t.Fatalf("filter broken, got %s", msg.Event)
		}
	case <-time.After(time.Second):
		t.Fatal("timeout")
	}
}

func TestEventHubRingBufferEviction(t *testing.T) {
	h := NewEventHub("n1", 3)
	for i := 0; i < 5; i++ {
		h.Publish("e", i)
	}
	sub := h.Subscribe(nil, 0)
	defer h.Unsubscribe(sub)

	got := h.Replay(sub, 10)
	if len(got) != 3 {
		t.Fatalf("want 3 events retained, got %d", len(got))
	}
	if got[0].Seq != 3 || got[2].Seq != 5 {
		t.Fatalf("ring order wrong: %+v", got)
	}
}

func TestEventHubBackpressureDrop(t *testing.T) {
	h := NewEventHub("n1", 10)
	sub := h.Subscribe(nil, 1)
	defer h.Unsubscribe(sub)

	h.Publish("a", nil)
	h.Publish("b", nil)
	h.Publish("c", nil)

	if got := sub.Dropped(); got < 2 {
		t.Fatalf("want >=2 drops, got %d", got)
	}
}

func TestEventHubConcurrentPublish(t *testing.T) {
	h := NewEventHub("n1", 1000)
	sub := h.Subscribe(nil, 1000)
	defer h.Unsubscribe(sub)

	var wg sync.WaitGroup
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 50; j++ {
				h.Publish("x", nil)
			}
		}()
	}
	wg.Wait()

	if h.Seq() != 500 {
		t.Fatalf("want seq=500, got %d", h.Seq())
	}
}
